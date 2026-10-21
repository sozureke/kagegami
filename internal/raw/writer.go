package raw

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"os"
	"path/filepath"
	"strconv"
)

const (
	// DefaultMaxBytes is how much of one command's output is kept on disk.
	DefaultMaxBytes int64 = 256 << 20

	dirMode  = 0o700
	fileMode = 0o600
)

// Path returns the file that holds the raw output of the blob with the given id.
func Path(dir string, id int64) string {
	return filepath.Join(dir, strconv.FormatInt(id, 10)+".txt")
}

// Options configure a Writer.
type Options struct {
	// MaxBytes caps the stored output; zero means DefaultMaxBytes.
	MaxBytes int64
}

type Info struct {
	Size      int64
	SHA256    string // lower-case hex
	Truncated bool
}

// Writer streams command output to a file while counting and hashing it, so the output is never held in memory.
type Writer struct {
	f         *os.File
	h         hash.Hash
	size      int64
	limit     int64
	truncated bool

	closed   bool
	info     Info
	closeErr error
}

// Create opens the output file for the blob id inside dir, creating dir if needed.
// The file must not exist yet and a blob is written once. The error wraps fs.ErrExist if it does.
func Create(dir string, id int64, opts Options) (*Writer, error) {
	limit := opts.MaxBytes
	if limit < 0 {
		return nil, fmt.Errorf("raw output limit must not be negative, got %d", limit)
	}
	if limit == 0 {
		limit = DefaultMaxBytes
	}
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("create raw directory: %w", err)
	}

	f, err := os.OpenFile(Path(dir, id), os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode) //nolint:gosec // the name is derived from a numeric id
	if err != nil {
		return nil, fmt.Errorf("create raw output %d: %w", id, err)
	}
	return &Writer{f: f, h: sha256.New(), limit: limit}, nil
}

// Write stores p, up to the size limit. Output beyond the limit is dropped, but Write
// still reports it as written, so a command that keeps printing is not broken by the
// capture; Truncated in the Info records that data was lost.
func (w *Writer) Write(p []byte) (int, error) {
	if w.closed {
		return 0, os.ErrClosed
	}
	n := len(p)

	room := w.limit - w.size
	if room <= 0 {
		w.truncated = n > 0 || w.truncated
		return n, nil
	}
	if int64(len(p)) > room {
		p = p[:room]
		w.truncated = true
	}

	written, err := w.f.Write(p)
	w.h.Write(p[:written]) // hash.Hash.Write never returns an error
	w.size += int64(written)
	if err != nil {
		return written, err
	}
	return n, nil
}

// Size is the number of bytes stored so far.
func (w *Writer) Size() int64 { return w.size }

// Close flushes the file to disk and returns the final Info. Calling it again returns the same result.
func (w *Writer) Close() (Info, error) {
	if w.closed {
		return w.info, w.closeErr
	}
	w.closed = true

	syncErr := w.f.Sync() // the output is evidence, so make sure it survived before it is reported
	closeErr := w.f.Close()
	w.closeErr = errors.Join(syncErr, closeErr)
	w.info = Info{Size: w.size, SHA256: hex.EncodeToString(w.h.Sum(nil)), Truncated: w.truncated}
	return w.info, w.closeErr
}
