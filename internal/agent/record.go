package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/sozureke/kagegami/internal/raw"
	"github.com/sozureke/kagegami/internal/store"
	"github.com/sozureke/kagegami/internal/terminal"
)

// recorder keeps the output of every command on disk, as a file under the raw directory and a
// row in the database that points to it (ADR-0004). It is the terminal.Recorder of a capture.
type recorder struct {
	st     *store.Store
	rawDir string
	limit  int64

	mu       sync.Mutex
	finished map[int]blobInfo
}

// blobInfo says where the output of a finished command is.
type blobInfo struct {
	ID   int64
	Size int64
}

func newRecorder(st *store.Store, rawDir string, limit int64) *recorder {
	return &recorder{st: st, rawDir: rawDir, limit: limit, finished: map[int]blobInfo{}}
}

// Begin implements terminal.Recorder.
func (r *recorder) Begin(seq int, cmd string) (terminal.Output, error) {
	blob, err := r.st.Repo().CreateBlob(context.Background(), cmd)
	if err != nil {
		return nil, fmt.Errorf("record the command: %w", err)
	}
	w, err := raw.Create(r.rawDir, blob.ID, raw.Options{MaxBytes: r.limit})
	if err != nil {
		return nil, fmt.Errorf("create the output file: %w", err)
	}
	return &output{rec: r, seq: seq, blobID: blob.ID, w: w}, nil
}

// take returns where the output of a finished command is, and forgets it.
func (r *recorder) take(seq int) (blobInfo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	info, ok := r.finished[seq]
	delete(r.finished, seq)
	return info, ok
}

type output struct {
	rec    *recorder
	seq    int
	blobID int64
	w      *raw.Writer
}

func (o *output) Write(p []byte) (int, error) { return o.w.Write(p) }

// Finish closes the file and completes the row. The exit code of a command that never ended
// properly is not known and is stored as -1.
func (o *output) Finish(end terminal.End) error {
	info, err := o.w.Close()
	if err != nil {
		return fmt.Errorf("close the output file: %w", err)
	}
	code := end.ExitCode
	if end.Aborted {
		code = -1
	}
	if _, err := o.rec.st.Repo().FinishBlob(context.Background(), o.blobID, code, info.Size, info.SHA256); err != nil {
		return fmt.Errorf("complete the record: %w", err)
	}
	o.rec.mu.Lock()
	o.rec.finished[o.seq] = blobInfo{ID: o.blobID, Size: info.Size}
	o.rec.mu.Unlock()
	return nil
}
