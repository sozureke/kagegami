package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sozureke/kagegami/internal/mapview"
	"github.com/sozureke/kagegami/internal/raw"
	"github.com/sozureke/kagegami/internal/recon"
	"github.com/sozureke/kagegami/internal/recon/nmap"
	"github.com/sozureke/kagegami/internal/store"
	"github.com/sozureke/kagegami/internal/terminal"
	"github.com/sozureke/kagegami/pkg/llm"
	"github.com/sozureke/kagegami/pkg/llm/llmtest"
)

const nonce = "0123456789abcdef0123456789abcdef"

// stream builds what a pane prints for the given commands: markers around the output.
func stream(cmds ...[3]string) string { // cmd, output, exit code
	var b strings.Builder
	for i, c := range cmds {
		id := i + 1
		fmt.Fprintf(&b, "prompt%% \x1b]7771;C;kg=%s;id=%d;cmd=%s\a%s\x1b]7771;D;kg=%s;id=%d;rc=%s\a", nonce, id, c[0], strings.ReplaceAll(c[1], "\n", "\r\n"), nonce, id, c[2])
	}
	return b.String()
}

type harness struct {
	t       *testing.T
	st      *store.Store
	rawDir  string
	agent   *Agent
	updates []Update
	mu      sync.Mutex
	logs    bytes.Buffer
}

func newHarness(t *testing.T, mutate func(*Config)) *harness {
	t.Helper()
	h := &harness{t: t}
	dir := t.TempDir()
	st, err := store.Open(bg, filepath.Join(dir, "case.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	h.st, h.rawDir = st, filepath.Join(dir, "raw")
	cfg := Config{
		Store: st, RawDir: h.rawDir,
		Registry: recon.NewRegistry(nmap.New()),
		Filter:   terminal.NewFilter([]string{"ls", "cd", "pwd"}),
		Logger:   slog.New(slog.NewTextHandler(&h.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		OnUpdate: func(u Update) { h.mu.Lock(); h.updates = append(h.updates, u); h.mu.Unlock() },
	}
	if mutate != nil {
		mutate(&cfg)
	}
	if h.agent, err = New(cfg); err != nil {
		t.Fatal(err)
	}
	return h
}

// run feeds a stream through a real capture into the agent and waits for all to finish.
func (h *harness) run(streamText string) {
	h.t.Helper()
	events := make(chan terminal.Event, 64)
	done := make(chan error, 1)
	go func() { done <- h.agent.Run(bg, events) }()
	if err := terminal.Capture(bg, strings.NewReader(streamText), nonce, h.agent.Recorder(), events); err != nil {
		h.t.Fatal(err)
	}
	close(events)
	select {
	case err := <-done:
		if err != nil {
			h.t.Fatalf("Run: %v", err)
		}
	case <-time.After(20 * time.Second):
		h.t.Fatal("Run did not finish")
	}
}

func (h *harness) view() mapview.View {
	h.t.Helper()
	v, err := mapview.Load(bg, h.st)
	if err != nil {
		h.t.Fatal(err)
	}
	return v
}

func (h *harness) notes() []string {
	var out []string
	for _, n := range h.view().Notes {
		out = append(out, n.Text)
	}
	return out
}

func (h *harness) hasNote(substr string) bool {
	for _, n := range h.notes() {
		if strings.Contains(n, substr) {
			return true
		}
	}
	return false
}

func (h *harness) update(cmdPrefix string) Update {
	h.t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, u := range h.updates {
		if strings.HasPrefix(u.Cmd, cmdPrefix) {
			return u
		}
	}
	h.t.Fatalf("no update for %q in %+v", cmdPrefix, h.updates)
	return Update{}
}

func TestACommandWithAParserReachesTheMap(t *testing.T) {
	h := newHarness(t, nil)
	h.run(stream([3]string{"nmap -sV 127.0.0.1", nmapOut, "0"}))

	v := h.view()
	if len(v.Hosts) != 1 || v.Hosts[0].IP != "127.0.0.1" || len(v.Hosts[0].Services) != 2 {
		t.Fatalf("map = %+v", v.Hosts)
	}
	u := h.update("nmap")
	if u.By != "parser nmap" || u.Added.Hosts != 1 || u.Added.Services != 2 || u.Err != nil || u.BlobID == 0 {
		t.Errorf("update = %+v", u)
	}
	// every entry points to the command, and the output is on disk, exactly as printed
	hosts, _ := h.st.Repo().ListHosts(bg)
	if hosts[0].SourceBlobID == nil || *hosts[0].SourceBlobID != u.BlobID {
		t.Error("the host does not point to its command")
	}
	data, err := os.ReadFile(raw.Path(h.rawDir, u.BlobID))
	if err != nil || !strings.Contains(string(data), "8000/tcp") || !strings.Contains(string(data), "\r\n") {
		t.Errorf("raw output: %v %q", err, data)
	}
	blob, _ := h.st.Repo().GetBlob(bg, u.BlobID)
	if blob.Cmd != "nmap -sV 127.0.0.1" || blob.ExitCode == nil || *blob.ExitCode != 0 || blob.Size != int64(len(data)) || blob.FinishedAt == nil {
		t.Errorf("blob = %+v", blob)
	}
}

func TestATrivialCommandIsRecordedAndNotRead(t *testing.T) {
	h := newHarness(t, nil)
	h.run(stream([3]string{"ls -la", "a\nb\n", "0"}, [3]string{"touch x", "", "0"}))
	if u := h.update("ls"); u.Skipped != terminal.ReasonIgnored || u.BlobID == 0 || u.By != "" {
		t.Errorf("ls: %+v", u)
	}
	if u := h.update("touch"); u.Skipped != terminal.ReasonNoOutput {
		t.Errorf("touch: %+v", u)
	}
	if len(h.view().Hosts) != 0 {
		t.Error("something got on the map")
	}
	if !strings.Contains(h.logs.String(), "command not read") {
		t.Errorf("the skip is not in the debug log:\n%s", h.logs.String())
	}
}

func TestACommandWithoutAParserGoesToTheModel(t *testing.T) {
	answer := `{"hosts":[{"ip":"10.0.0.5"}],"services":[{"host_ip":"10.0.0.5","port":445,"proto":"tcp","state":"open","name":"microsoft-ds"}]}`
	f := llmtest.NewFake(llmtest.Text(answer))
	h := newHarness(t, func(c *Config) {
		c.Tier1 = &Tier1{Provider: f, InScope: func(ip string) bool { return ip == "10.0.0.5" }}
	})
	h.run(stream([3]string{"smbmap -H 10.0.0.5", "[+] 10.0.0.5:445 microsoft-ds\nshare ADMIN$", "0"}))

	if u := h.update("smbmap"); u.By != "tier1" || u.Added.Hosts != 1 || u.Added.Services != 1 || u.Err != nil {
		t.Errorf("update = %+v", u)
	}
	if v := h.view(); len(v.Hosts) != 1 || v.Hosts[0].Services[0].Port != 445 {
		t.Errorf("map = %+v", v.Hosts)
	}
	if len(f.Requests()) != 1 || !strings.Contains(f.Requests()[0].Messages[0].Content, "smbmap -H 10.0.0.5") {
		t.Error("the model did not see the command")
	}
}

func TestWithoutAModelAnUnknownCommandIsOnlyRecorded(t *testing.T) {
	h := newHarness(t, nil)
	h.run(stream([3]string{"smbmap -H 10.0.0.5", "some output", "0"}))
	u := h.update("smbmap")
	if u.By != "" || u.Err != nil || u.BlobID == 0 || len(h.view().Hosts) != 0 {
		t.Errorf("update = %+v", u)
	}
}

func TestAParserBeatsTheModel(t *testing.T) {
	f := llmtest.NewFake(llmtest.Text(`{"hosts":[{"ip":"1.2.3.4"}]}`))
	h := newHarness(t, func(c *Config) { c.Tier1 = &Tier1{Provider: f} })
	h.run(stream([3]string{"nmap 127.0.0.1", nmapOut, "0"}))
	if len(f.Requests()) != 0 {
		t.Error("the model was asked although a parser handled the command")
	}
}

func TestAToolThatFailedLeavesANoteAndTheModelIsNotAsked(t *testing.T) {
	f := llmtest.NewFake(llmtest.Text(`{}`))
	h := newHarness(t, func(c *Config) { c.Tier1 = &Tier1{Provider: f} })
	h.run(stream([3]string{"nmap nosuchhost", "Failed to resolve \"nosuchhost\".\nWARNING: No targets were specified", "1"}))
	if len(f.Requests()) != 0 {
		t.Error("the model was asked about a failed tool")
	}
	if !h.hasNote(`nmap exited with code 1: Failed to resolve "nosuchhost".`) {
		t.Errorf("notes = %v", h.notes())
	}
	// a tool that found nothing and succeeded leaves no note
	h2 := newHarness(t, nil)
	h2.run(stream([3]string{"nmap -sn 10.9.9.1", "Starting Nmap\nNmap done: 1 IP address (0 hosts up) scanned in 3.0 seconds", "0"}))
	if len(h2.notes()) != 0 {
		t.Errorf("notes = %v", h2.notes())
	}
}

func TestWhatTheModelCouldNotDoIsToldInTheJournal(t *testing.T) {
	cases := map[string]struct {
		err  error
		want string
	}{
		"refusal":     {&llm.RefusedError{Provider: "p/m", Reason: "policy"}, "the model refused to read the output of"},
		"unavailable": {&llm.UnavailableError{Provider: "ollama/m", Err: errors.New("connection refused"), Hint: "start it with: ollama serve"}, "the model is not available"},
		"missing":     {&llm.ModelMissingError{Provider: "ollama", Model: "m", Hint: "pull it with: ollama pull m"}, "ollama pull m"},
		"budget":      {llm.ErrBudgetExceeded, "token budget is used up"},
		"too long":    {llm.ErrContextTooLong, "does not fit"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, func(cfg *Config) {
				cfg.Tier1 = &Tier1{Provider: llmtest.NewFake(llmtest.Result{Err: c.err})}
			})
			h.run(stream([3]string{"smbmap -H 10.0.0.5", "output", "0"}))
			u := h.update("smbmap")
			if !errors.Is(u.Err, c.err) || !h.hasNote(c.want) {
				t.Errorf("update err %v, notes %v", u.Err, h.notes())
			}
			if blob, _ := h.st.Repo().GetBlob(bg, u.BlobID); blob.FinishedAt == nil {
				t.Error("the output was not kept")
			}
		})
	}
	t.Run("an answer that cannot be used", func(t *testing.T) {
		h := newHarness(t, func(cfg *Config) {
			cfg.Tier1 = &Tier1{Provider: llmtest.NewFake(llmtest.Text("no"), llmtest.Text("still no"))}
		})
		h.run(stream([3]string{"smbmap", "output", "0"}))
		if !h.hasNote("could not be used") {
			t.Errorf("notes = %v", h.notes())
		}
	})
}

func TestAddressesOutsideTheScopeAreNotedNotMapped(t *testing.T) {
	answer := `{"hosts":[{"ip":"10.0.0.5"},{"ip":"10.9.9.9"}],"services":[{"host_ip":"10.9.9.9","port":4444,"proto":"tcp"}]}`
	h := newHarness(t, func(c *Config) {
		c.Tier1 = &Tier1{Provider: llmtest.NewFake(llmtest.Text(answer)), InScope: func(ip string) bool { return ip == "10.0.0.5" }}
	})
	h.run(stream([3]string{"curl http://10.0.0.5/", "10.0.0.5 backup at 10.9.9.9 port 4444", "0"}))
	v := h.view()
	if len(v.Hosts) != 1 || v.Hosts[0].IP != "10.0.0.5" {
		t.Errorf("hosts = %+v", v.Hosts)
	}
	if !h.hasNote("outside the scope, left off the map: 10.9.9.9") {
		t.Errorf("notes = %v", h.notes())
	}
	if !strings.Contains(h.logs.String(), "outside the scope") || !strings.Contains(h.logs.String(), "tier1 dropped an entry") {
		t.Errorf("the drops are not in the debug log:\n%s", h.logs.String())
	}
}

func TestAFullQueueSkipsTheOldestAndSaysSo(t *testing.T) {
	release := make(chan struct{})
	slow := &blockingProvider{release: release}
	h := newHarness(t, func(c *Config) {
		c.Tier1 = &Tier1{Provider: slow}
		c.QueueSize = 2
	})
	var cmds [][3]string
	for i := range 6 {
		cmds = append(cmds, [3]string{fmt.Sprintf("tool%d", i), "output", "0"})
	}
	events := make(chan terminal.Event, 64)
	done := make(chan error, 1)
	go func() { done <- h.agent.Run(bg, events) }()
	if err := terminal.Capture(bg, strings.NewReader(stream(cmds...)), nonce, h.agent.Recorder(), events); err != nil {
		t.Fatal(err)
	}
	// the worker holds tool0; tools 1-5 arrive while it is busy: the queue keeps the last two
	waitFor(t, func() bool { return slow.started() >= 1 })
	time.Sleep(100 * time.Millisecond)
	close(events)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	if got := slow.calls(); len(got) != 3 || !strings.Contains(got[0], "tool0") || !strings.Contains(got[1], "tool4") || !strings.Contains(got[2], "tool5") {
		t.Errorf("the model read: %v", got)
	}
	if !h.hasNote("skipped 3 commands because the model was busy; their output is kept (records:") {
		t.Errorf("notes = %v", h.notes())
	}
	// everything is kept on disk, read or not
	for id := int64(1); id <= 6; id++ {
		if _, err := os.Stat(raw.Path(h.rawDir, id)); err != nil {
			t.Errorf("output %d is missing: %v", id, err)
		}
	}
}

type blockingProvider struct {
	release chan struct{}
	mu      sync.Mutex
	seen    []string
}

func (b *blockingProvider) Name() string                   { return "block/model" }
func (b *blockingProvider) Capabilities() llm.Capabilities { return llm.Capabilities{} }
func (b *blockingProvider) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	b.mu.Lock()
	b.seen = append(b.seen, req.Messages[len(req.Messages)-1].Content)
	b.mu.Unlock()
	select {
	case <-b.release:
	case <-ctx.Done():
		return llm.Response{}, ctx.Err()
	}
	return llm.Response{Text: `{}`, Finish: llm.FinishStop}, nil
}
func (b *blockingProvider) started() int { b.mu.Lock(); defer b.mu.Unlock(); return len(b.seen) }
func (b *blockingProvider) calls() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.seen...)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal("timed out")
}

func TestHookSilentIsToldOnce(t *testing.T) {
	h := newHarness(t, nil)
	events := make(chan terminal.Event, 8)
	events <- terminal.Event{Kind: terminal.HookSilent}
	events <- terminal.Event{Kind: terminal.HookSilent}
	close(events)
	if err := h.agent.Run(bg, events); err != nil {
		t.Fatal(err)
	}
	notes := h.notes()
	if len(notes) != 1 || !strings.Contains(notes[0], "hook is probably not loaded") || !strings.Contains(notes[0], "kagegami hook zsh") {
		t.Errorf("notes = %v", notes)
	}
}

func TestARecordingFailureIsToldAndTheCommandIsNotRead(t *testing.T) {
	h := newHarness(t, nil)
	// the raw directory cannot be made: a file stands where it should be
	if err := os.WriteFile(h.rawDir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.run(stream([3]string{"nmap 127.0.0.1", nmapOut, "0"}))
	if !h.hasNote("could not be recorded completely") {
		t.Errorf("notes = %v", h.notes())
	}
	if len(h.view().Hosts) != 0 {
		t.Error("a command without a record was read")
	}
}

func TestRunStopsWhenTheContextIsDone(t *testing.T) {
	h := newHarness(t, nil)
	events := make(chan terminal.Event)
	ctx, cancel := context.WithCancel(bg)
	done := make(chan error, 1)
	go func() { done <- h.agent.Run(ctx, events) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestRunStopsAWorkerThatIsWaitingForTheModel(t *testing.T) {
	slow := &blockingProvider{release: make(chan struct{})}
	h := newHarness(t, func(c *Config) { c.Tier1 = &Tier1{Provider: slow} })
	events := make(chan terminal.Event, 8)
	ctx, cancel := context.WithCancel(bg)
	done := make(chan error, 1)
	go func() { done <- h.agent.Run(ctx, events) }()
	go func() {
		_ = terminal.Capture(bg, strings.NewReader(stream([3]string{"tool", "output", "0"})), nonce, h.agent.Recorder(), events)
	}()
	waitFor(t, func() bool { return slow.started() == 1 })
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop although the context is done")
	}
}

func TestNewChecksItsInput(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Error("an empty configuration was accepted")
	}
	st, _ := store.Open(bg, filepath.Join(t.TempDir(), "x.db"))
	defer func() { _ = st.Close() }()
	a, err := New(Config{Store: st, RawDir: t.TempDir()})
	if err != nil || a.cfg.QueueSize != 8 || a.cfg.MaxText != 8<<20 {
		t.Errorf("defaults: %+v, %v", a.cfg, err)
	}
}

func TestHugeOutputIsCleanedUpToALimit(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.MaxText = 1000 })
	h.run(stream([3]string{"nmap 127.0.0.1", nmapOut + strings.Repeat("noise line\n", 5000), "0"}))
	// the interesting part is at the start and is read; the rest is cut off
	if v := h.view(); len(v.Hosts) != 1 || len(v.Hosts[0].Services) != 2 {
		t.Errorf("map = %+v", v.Hosts)
	}
}

// A dump of ten thousand lines is no larger a request to the model than a short output is: the
// model sees the head and the tail, and the rest stays on disk (M5.8).
func TestADumpOfTenThousandLinesIsBoundedForTheModel(t *testing.T) {
	var dump strings.Builder
	for i := range 10_000 {
		fmt.Fprintf(&dump, "line %s of a long dump from a tool nobody wrote a parser for\n", words(i))
	}
	dump.WriteString("THE LAST LINE\n")
	f := llmtest.NewFake(llmtest.Text(`{}`))
	h := newHarness(t, func(c *Config) { c.Tier1 = &Tier1{Provider: f} })
	h.run(stream([3]string{"unknowntool --everything", dump.String(), "0"}))

	reqs := f.Requests()
	if len(reqs) != 1 {
		t.Fatalf("%d requests", len(reqs))
	}
	total := len(reqs[0].System) + len(reqs[0].Schema)
	for _, m := range reqs[0].Messages {
		total += len(m.Content)
	}
	opts := DefaultTier1Options()
	// the output part is at most head + tail; the rest of the request is the fixed prompt and schema
	if user := reqs[0].Messages[0].Content; len(user) > opts.Head+opts.Tail+1024 {
		t.Errorf("the user message is %d bytes for a dump of %d bytes", len(user), dump.Len())
	}
	if total > opts.Head+opts.Tail+8<<10 {
		t.Errorf("the whole request is %d bytes", total)
	}
	user := reqs[0].Messages[0].Content
	if !strings.Contains(user, "line "+words(0)) || !strings.Contains(user, "THE LAST LINE") || !strings.Contains(user, "bytes omitted") || strings.Contains(user, "line "+words(5000)) {
		t.Error("the model must see the start and the end of the dump and be told that the middle is missing")
	}
	// and nothing was lost: the whole dump is on disk
	blob, _ := h.st.Repo().GetBlob(bg, h.update("unknowntool").BlobID)
	if blob.Size < int64(dump.Len()) {
		t.Errorf("only %d of %d bytes were kept", blob.Size, dump.Len())
	}
}
