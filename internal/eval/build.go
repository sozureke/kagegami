package eval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sozureke/kagegami/internal/engagement"
	"github.com/sozureke/kagegami/internal/pipeline"
	"github.com/sozureke/kagegami/internal/raw"
	"github.com/sozureke/kagegami/internal/store"
)

// Env is a case built from a fixture: a database and the directory with the raw output.
type Env struct {
	Store  *store.Store
	RawDir string
	// Blobs holds the ids of the blobs, in the order of the steps.
	Blobs []int64

	dir string
}

// Close removes the case.
func (e *Env) Close() error {
	var err error
	if e.Store != nil { // Build closes a case it could not finish, before its database was opened
		err = e.Store.Close()
	}
	if rerr := os.RemoveAll(e.dir); err == nil {
		err = rerr
	}
	return err
}

// Build makes a fresh case from the fixture, the way a session would have: the engagement is set,
// every command has its output on disk and a blob in the database, and what each found is applied
// to the map with the blob as its source.
func Build(ctx context.Context, f Fixture) (_ *Env, err error) {
	dir, err := os.MkdirTemp("", "kagegami-eval-")
	if err != nil {
		return nil, err
	}
	env := &Env{RawDir: filepath.Join(dir, "raw"), dir: dir}
	defer func() {
		if err != nil {
			_ = env.Close()
		}
	}()
	if env.Store, err = store.Open(ctx, filepath.Join(dir, "case.db")); err != nil {
		return nil, err
	}

	c := engagement.Changes{Target: &f.Case.Target, Goal: &f.Case.Goal}
	if f.Case.Phase != "" {
		c.Phase = &f.Case.Phase
	}
	if f.Case.Scope != nil {
		c.Scope = &f.Case.Scope
	}
	if _, err = engagement.Update(ctx, env.Store, f.Name, c); err != nil {
		return nil, fmt.Errorf("fixture %s: %w", f.Name, err)
	}

	for i, s := range f.Steps {
		id, serr := env.addBlob(ctx, s.Command, s.Output)
		if serr != nil {
			return nil, fmt.Errorf("fixture %s, step %d: %w", f.Name, i+1, serr)
		}
		env.Blobs = append(env.Blobs, id)
		if s.Delta.IsEmpty() {
			continue
		}
		if _, err = pipeline.Apply(ctx, env.Store, s.Delta, &id); err != nil {
			return nil, fmt.Errorf("fixture %s, step %d: %w", f.Name, i+1, err)
		}
	}
	for _, text := range f.Journal {
		if _, err = env.Store.Repo().AddJournal(ctx, store.JournalEntry{Author: "human", Text: text}); err != nil {
			return nil, fmt.Errorf("fixture %s: %w", f.Name, err)
		}
	}
	return env, nil
}

func (e *Env) addBlob(ctx context.Context, command, output string) (int64, error) {
	blob, err := e.Store.Repo().CreateBlob(ctx, command)
	if err != nil {
		return 0, err
	}
	w, err := raw.Create(e.RawDir, blob.ID, raw.Options{})
	if err != nil {
		return 0, err
	}
	if _, err := w.Write([]byte(output)); err != nil {
		_, _ = w.Close()
		return 0, err
	}
	info, err := w.Close()
	if err != nil {
		return 0, err
	}
	if _, err := e.Store.Repo().FinishBlob(ctx, blob.ID, 0, info.Size, info.SHA256); err != nil {
		return 0, err
	}
	return blob.ID, nil
}

// Generated returns a generated output by name. They are made in code, and not kept as files,
// because what matters about them is their size and where one line sits in it.
func Generated(name string) (string, error) {
	switch name {
	case "long-unknown":
		return longUnknown(), nil
	}
	return "", fmt.Errorf("no generated output called %q", name)
}

// LongUnknownKey is the line of the long-unknown output that matters.
const LongUnknownKey = "[+] 10.10.11.5:6379/tcp OPEN redis 6.0.16 auth=none"

// longUnknown is the output of a tool that no parser knows: a long probe log with one line of
// interest, in the middle, further from both ends than tier 1's head and tail reach.
func longUnknown() string {
	var b strings.Builder
	b.WriteString("probescan 0.9 starting, target 10.10.11.5, 1000 ports\n")
	line := func(port int) {
		fmt.Fprintf(&b, "[-] 10.10.11.5:%d/tcp closed (conn refused)\n", port)
	}
	for p := 1000; p < 1300; p++ {
		line(p)
	}
	b.WriteString(LongUnknownKey + "\n")
	for p := 1300; p < 1600; p++ {
		line(p)
	}
	b.WriteString("probescan done: 1 open, 599 closed, 0 filtered\n")
	return b.String()
}
