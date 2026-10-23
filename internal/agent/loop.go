package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"unicode"

	"github.com/sozureke/kagegami/internal/pipeline"
	"github.com/sozureke/kagegami/internal/raw"
	"github.com/sozureke/kagegami/internal/recon"
	"github.com/sozureke/kagegami/internal/store"
	"github.com/sozureke/kagegami/internal/terminal"
	"github.com/sozureke/kagegami/pkg/llm"
)

// Config configures an Agent.
type Config struct {
	Store  *store.Store
	RawDir string
	// Registry holds the deterministic parsers; nil means none.
	Registry *recon.Registry
	// Filter drops commands that are not worth reading; nil keeps everything.
	Filter *terminal.Filter
	// Tier1 reads the output of commands that no parser matched; nil means such commands are
	// recorded and not read.
	Tier1 *Tier1
	// Scope returns the scope of the engagement as it is now, so that a change of it is seen at
	// once. What a command found about an address outside it is left off the map, whoever read
	// the output; and with an empty scope no model is called. Nil means no scope is enforced.
	Scope func(ctx context.Context) ScopeCheck
	// Logger receives debug lines about what was done and dropped; nil discards them.
	Logger *slog.Logger
	// QueueSize is how many commands wait for the worker; zero means 8. When it is full the
	// oldest waiting command is skipped.
	QueueSize int
	// MaxText bounds the cleaned output given to parsers and the model; zero means 8 MiB.
	MaxText int
	// OutputLimit bounds the raw output kept per command; zero means the raw package default.
	OutputLimit int64
	// OnUpdate is called after a command has been handled. It runs on the worker, so it should be quick.
	OnUpdate func(Update)
}

// ScopeCheck is the part of a scope the agent needs.
type ScopeCheck interface {
	Contains(ip string) bool
	IsEmpty() bool
}

// Update tells what became of a command.
type Update struct {
	Seq    int
	Cmd    string
	BlobID int64
	// Exit is the exit code of the command; 0 for one that was aborted, which has none.
	Exit int
	// By says what read the output: "parser nmap", "tier1", or "" if nothing did.
	By string
	// Skipped is why the command was not read, if it was not.
	Skipped terminal.Reason
	// Queued marks the first update of a command that passed the filter: it will be read, and the
	// update comes at once, before the reading (which may wait for a model).
	Queued bool
	// Handled marks the last update of a command that was read.
	Handled bool
	Added   pipeline.Added
	// Err is a failure that stopped the command from being read or stored.
	Err error
}

// Agent turns the events of a capture into updates of the map.
type Agent struct {
	cfg Config
	rec *recorder
	log *slog.Logger
}

const (
	defaultQueue   = 8
	defaultMaxText = 8 << 20
)

// New checks the configuration and returns the agent.
func New(cfg Config) (*Agent, error) {
	if cfg.Store == nil || cfg.RawDir == "" {
		return nil, errors.New("agent: a store and a raw directory are needed")
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultQueue
	}
	if cfg.MaxText <= 0 {
		cfg.MaxText = defaultMaxText
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Agent{cfg: cfg, rec: newRecorder(cfg.Store, cfg.RawDir, cfg.OutputLimit), log: cfg.Logger}, nil
}

// Recorder is what a capture records the output of commands with.
func (a *Agent) Recorder() terminal.Recorder { return a.rec }

type job struct {
	seq    int
	cmd    string
	exit   int
	blobID int64
}

// Run handles the events until they stop (the channel is closed) or ctx is done. Commands
// that are worth reading wait in a queue for one worker; the capture is never held up by the
// model. When the events stop, what is queued is still handled.
func (a *Agent) Run(ctx context.Context, events <-chan terminal.Event) error {
	q := newQueue(a.cfg.QueueSize)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		a.work(ctx, q)
	}()

	silentNoted := false
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case e, ok := <-events:
			if !ok {
				break loop
			}
			switch e.Kind {
			case terminal.CommandFinished:
				a.finished(ctx, e, q)
			case terminal.HookSilent:
				if !silentNoted {
					silentNoted = true
					a.note(ctx, nil, "much output went by and no command was marked: the shell hook is probably not loaded in this pane (add: eval \"$(kagegami hook zsh)\" to your shell's startup file)")
				}
			case terminal.CommandStarted:
			}
		}
	}
	q.close()
	wg.Wait()
	return ctx.Err()
}

// finished decides what to do with a command that ended.
func (a *Agent) finished(ctx context.Context, e terminal.Event, q *queue) {
	info, recorded := a.rec.take(e.Seq)
	if e.Err != nil {
		a.log.Warn("the output of a command was not recorded completely", "seq", e.Seq, "err", e.Err)
		a.note(ctx, nil, fmt.Sprintf("the output of %q could not be recorded completely: %v", short(e.Cmd), firstLine(e.Err.Error())))
	}
	exit := e.End.ExitCode
	if e.End.Aborted {
		exit = 0
	}
	if !recorded {
		a.update(Update{Seq: e.Seq, Cmd: e.Cmd, Exit: exit, Err: e.Err})
		return
	}
	if a.cfg.Filter != nil {
		if reason := a.cfg.Filter.Judge(e.Cmd, e.End, info.Size); reason != terminal.ReasonNone {
			a.log.Debug("command not read", "seq", e.Seq, "blob", info.ID, "reason", string(reason))
			a.update(Update{Seq: e.Seq, Cmd: e.Cmd, BlobID: info.ID, Exit: exit, Skipped: reason})
			return
		}
	}
	a.update(Update{Seq: e.Seq, Cmd: e.Cmd, BlobID: info.ID, Exit: exit, Queued: true})
	if dropped := q.push(job{seq: e.Seq, cmd: e.Cmd, exit: exit, blobID: info.ID}); dropped != nil {
		a.log.Debug("queue full, a command was skipped", "seq", dropped.seq, "blob", dropped.blobID)
	}
}

// work handles the queued commands one at a time.
func (a *Agent) work(ctx context.Context, q *queue) {
	for {
		j, skipped, ok := q.pop(ctx)
		if len(skipped) > 0 {
			a.note(ctx, nil, skipNote(skipped))
		}
		if !ok {
			return
		}
		a.handle(ctx, j)
	}
}

func skipNote(skipped []job) string {
	var ids []string
	for i, j := range skipped {
		if i == 5 {
			ids = append(ids, fmt.Sprintf("and %d more", len(skipped)-5))
			break
		}
		ids = append(ids, fmt.Sprintf("%d", j.blobID))
	}
	return fmt.Sprintf("skipped %d commands because the model was busy; their output is kept (records: %s)", len(skipped), strings.Join(ids, ", "))
}

// handle reads the output of one command and puts what is in it on the map.
func (a *Agent) handle(ctx context.Context, j job) {
	u := Update{Seq: j.seq, Cmd: j.cmd, BlobID: j.blobID, Exit: j.exit, Handled: true}
	text, err := a.cleanedText(j.blobID)
	if err != nil {
		u.Err = err
		a.log.Error("cannot read the output of a command", "blob", j.blobID, "err", err)
		a.update(u)
		return
	}

	var delta recon.StateDelta
	cmd := recon.Command{Line: j.cmd, ExitCode: j.exit}
	var parsers []string
	if a.cfg.Registry != nil {
		res := a.cfg.Registry.Run(ctx, cmd, text)
		parsers = res.Parsers
		delta = res.Delta
		if res.Err != nil {
			a.log.Debug("a parser failed", "blob", j.blobID, "err", res.Err)
		}
	}

	var inScope func(string) bool
	scoped := false
	if a.cfg.Scope != nil {
		if sc := a.cfg.Scope(ctx); sc != nil && !sc.IsEmpty() {
			inScope, scoped = sc.Contains, true
		}
	}

	switch {
	case len(parsers) > 0:
		u.By = "parser " + strings.Join(parsers, "+")
		if delta.IsEmpty() && j.exit != 0 {
			// the tool failed: the fact is worth keeping, and the model is not asked
			delta.Notes = append(delta.Notes, recon.NoteDelta{Text: fmt.Sprintf("%s exited with code %d: %s", strings.Join(parsers, "+"), j.exit, firstText(text))})
		}
		var dropped []recon.Drop
		delta, dropped = keepInScope(delta, inScope)
		if n, ok := scopeNote(j.cmd, dropped); ok {
			delta.Notes = append(delta.Notes, n)
		}
	case a.cfg.Tier1 != nil && (a.cfg.Scope == nil || scoped):
		u.By = "tier1"
		res, err := a.cfg.Tier1.Extract(ctx, j.cmd, j.exit, text)
		for _, d := range res.Dropped {
			a.log.Debug("tier1 dropped an entry", "blob", j.blobID, "kind", string(d.Kind), "detail", d.Detail)
		}
		if err != nil {
			a.log.Info("tier1 could not read a command", "blob", j.blobID, "err", err)
			a.note(ctx, &j.blobID, tier1Failure(j.cmd, err))
			u.Err = err
			a.update(u)
			return
		}
		delta = res.Delta
		if n, ok := res.ScopeNote(j.cmd); ok {
			delta.Notes = append(delta.Notes, n)
		}
	}

	added, err := pipeline.Apply(ctx, a.cfg.Store, delta, &j.blobID)
	if err != nil {
		u.Err = err
		a.log.Error("cannot store what a command showed", "blob", j.blobID, "err", err)
	}
	u.Added = added
	a.update(u)
}

// tier1Failure says in a sentence why the model's help is missing, in terms a person can act on.
func tier1Failure(cmd string, err error) string {
	var refused *llm.RefusedError
	var missing *llm.ModelMissingError
	var unavailable *llm.UnavailableError
	switch {
	case errors.As(err, &refused):
		return fmt.Sprintf("the model refused to read the output of %q (%s); the output is kept", short(cmd), refused.Error())
	case errors.As(err, &missing):
		return fmt.Sprintf("the model is missing, so %q was not read: %s", short(cmd), missing.Error())
	case errors.As(err, &unavailable):
		return fmt.Sprintf("the model is not available, so %q was not read: %s", short(cmd), firstLine(unavailable.Error()))
	case errors.Is(err, llm.ErrBudgetExceeded):
		return fmt.Sprintf("the token budget is used up, so %q was not read", short(cmd))
	case errors.Is(err, llm.ErrContextTooLong):
		return fmt.Sprintf("the output of %q does not fit the model's context window", short(cmd))
	case errors.Is(err, ErrUnparseable):
		return fmt.Sprintf("the model's answer for %q could not be used; the output is kept", short(cmd))
	case errors.Is(err, context.Canceled):
		return fmt.Sprintf("reading %q was cancelled", short(cmd))
	}
	return fmt.Sprintf("reading %q failed: %s", short(cmd), firstLine(err.Error()))
}

// note writes a remark to the journal, as the agent's, through the same path as everything else.
func (a *Agent) note(ctx context.Context, blob *int64, text string) {
	if _, err := pipeline.Apply(ctx, a.cfg.Store, recon.StateDelta{Notes: []recon.NoteDelta{{Text: text}}}, blob); err != nil {
		a.log.Error("cannot write a note", "err", err)
	}
}

func (a *Agent) update(u Update) {
	if a.cfg.OnUpdate != nil {
		a.cfg.OnUpdate(u)
	}
}

// cleanedText reads the output file of a blob as text a person saw (see terminal.Cleaner),
// up to MaxText bytes.
func (a *Agent) cleanedText(blobID int64) (string, error) {
	f, err := os.Open(raw.Path(a.cfg.RawDir, blobID))
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	var sb strings.Builder
	c := terminal.NewCleaner(&limitedWriter{w: &sb, left: a.cfg.MaxText})
	if _, err := io.Copy(c, f); err != nil && !errors.Is(err, errFull) {
		return "", err
	}
	if err := c.Close(); err != nil && !errors.Is(err, errFull) {
		return "", err
	}
	return sb.String(), nil
}

var errFull = errors.New("enough text")

// limitedWriter stops a copy once it has taken left bytes.
type limitedWriter struct {
	w    io.Writer
	left int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.left <= 0 {
		return 0, errFull
	}
	if len(p) > l.left {
		n, _ := l.w.Write(p[:l.left])
		l.left = 0
		return n, errFull
	}
	l.left -= len(p)
	return l.w.Write(p)
}

// firstText is the first line of the text that holds something, short, for a note.
func firstText(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return short200(line)
		}
	}
	return "(no output)"
}

func short200(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	if r := []rune(s); len(r) > 200 {
		return string(r[:200]) + "..."
	}
	return s
}

func firstLine(s string) string { return short200(strings.SplitN(s, "\n", 2)[0]) }

// queue is a bounded queue for one reader. When it is full the oldest entry is dropped, and the
// reader is told about the dropped ones when it next asks.
type queue struct {
	mu      sync.Mutex
	cond    *sync.Cond
	items   []job
	max     int
	skipped []job
	closed  bool
}

func newQueue(size int) *queue {
	q := &queue{max: size}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// push adds a job and returns the one it had to drop, if any.
func (q *queue) push(j job) *job {
	q.mu.Lock()
	defer q.mu.Unlock()
	var dropped *job
	if len(q.items) >= q.max {
		d := q.items[0]
		q.items = q.items[1:]
		q.skipped = append(q.skipped, d)
		dropped = &d
	}
	q.items = append(q.items, j)
	q.cond.Signal()
	return dropped
}

func (q *queue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.cond.Broadcast()
}

// pop waits for a job. It returns the jobs that were dropped since the last call (a job is only
// dropped to make room for a new one, so there is always a job to go with them), and false when
// the queue is closed and empty or ctx is done.
func (q *queue) pop(ctx context.Context) (job, []job, bool) {
	stop := context.AfterFunc(ctx, q.cond.Broadcast)
	defer stop()
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed && ctx.Err() == nil {
		q.cond.Wait()
	}
	skipped := slices.Clone(q.skipped)
	q.skipped = nil
	if ctx.Err() != nil || len(q.items) == 0 {
		return job{}, skipped, false
	}
	j := q.items[0]
	q.items = q.items[1:]
	return j, skipped, true
}
