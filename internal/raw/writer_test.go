package raw

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func sum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func mustCreate(t *testing.T, dir string, id int64, opts Options) *Writer {
	t.Helper()
	w, err := Create(dir, id, opts)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = w.Close() })
	return w
}

func mustClose(t *testing.T, w *Writer) Info {
	t.Helper()
	info, err := w.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	return info
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestPath(t *testing.T) {
	tests := []struct {
		id   int64
		want string
	}{
		{1, "raw/1.txt"},
		{42, "raw/42.txt"},
		{9_223_372_036_854_775_807, "raw/9223372036854775807.txt"},
	}
	for _, tt := range tests {
		if got := Path("raw", tt.id); got != filepath.FromSlash(tt.want) {
			t.Errorf("Path(raw, %d) = %q, want %q", tt.id, got, tt.want)
		}
	}
	// Whatever the id is, the file stays inside the directory.
	for _, id := range []int64{-1, 0, -9_223_372_036_854_775_808} {
		if got := Path("raw", id); filepath.Dir(got) != "raw" {
			t.Errorf("Path(raw, %d) = %q escapes the directory", id, got)
		}
	}
}

func TestWriteStoresTheBytesAndReportsInfo(t *testing.T) {
	dir := t.TempDir()
	data := []byte("PORT   STATE SERVICE\n22/tcp open  ssh\n80/tcp open  http\n")

	w := mustCreate(t, dir, 7, Options{})
	n, err := w.Write(data)
	if err != nil || n != len(data) {
		t.Fatalf("Write = %d, %v; want %d, nil", n, err, len(data))
	}
	if w.Size() != int64(len(data)) {
		t.Errorf("Size = %d while writing, want %d", w.Size(), len(data))
	}
	info := mustClose(t, w)

	if info.Size != int64(len(data)) {
		t.Errorf("Info.Size = %d, want %d", info.Size, len(data))
	}
	if want := sum(data); info.SHA256 != want {
		t.Errorf("Info.SHA256 = %s, want %s", info.SHA256, want)
	}
	if info.Truncated {
		t.Error("Truncated = true for output within the limit")
	}
	if got := readFile(t, Path(dir, 7)); !bytes.Equal(got, data) {
		t.Errorf("file content = %q, want %q", got, data)
	}
}

func TestWriteInManyPiecesGivesTheSameResult(t *testing.T) {
	data := bytes.Repeat([]byte("0123456789abcdef\n"), 5_000)
	whole := mustCreate(t, t.TempDir(), 1, Options{})
	if _, err := whole.Write(data); err != nil {
		t.Fatal(err)
	}
	a := mustClose(t, whole)

	for _, chunk := range []int{1, 7, 1000, len(data)} {
		w := mustCreate(t, t.TempDir(), 1, Options{})
		for rest := data; len(rest) > 0; {
			n := min(chunk, len(rest))
			if _, err := w.Write(rest[:n]); err != nil {
				t.Fatal(err)
			}
			rest = rest[n:]
		}
		if b := mustClose(t, w); b != a {
			t.Errorf("chunk size %d: %+v, want %+v", chunk, b, a)
		}
	}
}

func TestEmptyOutput(t *testing.T) {
	dir := t.TempDir()
	w := mustCreate(t, dir, 1, Options{})
	info := mustClose(t, w)

	if info.Size != 0 || info.Truncated {
		t.Errorf("info = %+v, want an empty, untruncated result", info)
	}
	// The hash of nothing, not an empty string.
	if want := sum(nil); info.SHA256 != want {
		t.Errorf("SHA256 = %s, want the hash of empty input %s", info.SHA256, want)
	}
	if st, err := os.Stat(Path(dir, 1)); err != nil || st.Size() != 0 {
		t.Errorf("the file must exist and be empty: %v", err)
	}
}

func TestBinaryAndNulBytesAreStoredAsIs(t *testing.T) {
	data := []byte{0, 1, 2, 0xff, 0xfe, '\n', 0, 0xc3, 0x28, 'x'}
	w := mustCreate(t, t.TempDir(), 1, Options{})
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	info := mustClose(t, w)
	if info.SHA256 != sum(data) || info.Size != int64(len(data)) {
		t.Errorf("info = %+v, want the raw bytes stored untouched", info)
	}
}

func TestEmptyWritesAreHarmless(t *testing.T) {
	w := mustCreate(t, t.TempDir(), 1, Options{MaxBytes: 4})
	if n, err := w.Write(nil); n != 0 || err != nil {
		t.Errorf("Write(nil) = %d, %v", n, err)
	}
	if _, err := w.Write([]byte("abcd")); err != nil {
		t.Fatal(err)
	}
	if n, err := w.Write([]byte{}); n != 0 || err != nil {
		t.Errorf("empty Write at the limit = %d, %v", n, err)
	}
	if info := mustClose(t, w); info.Truncated {
		t.Error("empty writes must not mark the output as truncated")
	}
}

// --- The limit --------------------------------------------------------------------------

func TestLimitTruncatesButKeepsTheWriterHappy(t *testing.T) {
	dir := t.TempDir()
	w := mustCreate(t, dir, 1, Options{MaxBytes: 10})

	n, err := io.Copy(w, strings.NewReader("0123456789ABCDEF"))
	if err != nil {
		t.Fatalf("io.Copy returned %v: output over the limit must not break the producer", err)
	}
	if n != 16 {
		t.Errorf("io.Copy copied %d, want all 16 reported as written", n)
	}
	info := mustClose(t, w)

	if info.Size != 10 || !info.Truncated {
		t.Errorf("info = %+v, want 10 bytes stored and Truncated", info)
	}
	if want := sum([]byte("0123456789")); info.SHA256 != want {
		t.Errorf("SHA256 = %s, want the hash of the stored bytes %s", info.SHA256, want)
	}
	if got := readFile(t, Path(dir, 1)); string(got) != "0123456789" {
		t.Errorf("file = %q, want only the first 10 bytes", got)
	}
}

func TestLimitBoundary(t *testing.T) {
	tests := []struct {
		name          string
		limit         int64
		data          string
		wantSize      int64
		wantTruncated bool
	}{
		{"one under the limit", 5, "1234", 4, false},
		{"exactly at the limit", 5, "12345", 5, false},
		{"one over the limit", 5, "123456", 5, true},
		{"far over the limit", 5, strings.Repeat("x", 1000), 5, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := mustCreate(t, t.TempDir(), 1, Options{MaxBytes: tt.limit})
			if n, err := w.Write([]byte(tt.data)); err != nil || n != len(tt.data) {
				t.Fatalf("Write = %d, %v; want %d, nil", n, err, len(tt.data))
			}
			info := mustClose(t, w)
			if info.Size != tt.wantSize || info.Truncated != tt.wantTruncated {
				t.Errorf("info = %+v, want size %d truncated %v", info, tt.wantSize, tt.wantTruncated)
			}
		})
	}
}

func TestLimitAppliesAcrossWrites(t *testing.T) {
	w := mustCreate(t, t.TempDir(), 1, Options{MaxBytes: 10})
	for _, piece := range []string{"abcd", "efgh", "ijkl", "mnop"} { // 16 bytes in total
		if n, err := w.Write([]byte(piece)); err != nil || n != len(piece) {
			t.Fatalf("Write(%q) = %d, %v", piece, n, err)
		}
	}
	info := mustClose(t, w)
	if info.Size != 10 || !info.Truncated || info.SHA256 != sum([]byte("abcdefghij")) {
		t.Errorf("info = %+v, want the first 10 bytes and Truncated", info)
	}
}

func TestOnceTruncatedAlwaysTruncated(t *testing.T) {
	w := mustCreate(t, t.TempDir(), 1, Options{MaxBytes: 3})
	_, _ = w.Write([]byte("abcdef"))
	_, _ = w.Write([]byte("x"))
	_, _ = w.Write(nil) // an empty write must not clear the flag either
	if info := mustClose(t, w); !info.Truncated {
		t.Error("Truncated was reset by a later write")
	}
}

func TestDefaultAndInvalidLimits(t *testing.T) {
	w := mustCreate(t, t.TempDir(), 1, Options{})
	if w.limit != DefaultMaxBytes {
		t.Errorf("limit = %d, want the default %d", w.limit, DefaultMaxBytes)
	}
	if DefaultMaxBytes != 256<<20 {
		t.Errorf("DefaultMaxBytes = %d, want 256 MiB", DefaultMaxBytes)
	}

	dir := t.TempDir()
	if _, err := Create(dir, 2, Options{MaxBytes: -1}); err == nil {
		t.Error("a negative limit must be rejected")
	}
	if _, err := os.Stat(Path(dir, 2)); !errors.Is(err, fs.ErrNotExist) {
		t.Error("a rejected Create must not leave a file behind")
	}
}

// --- Files and permissions --------------------------------------------------------------

func TestCreateMakesTheDirectoryAndPrivateFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "case", "raw")
	w := mustCreate(t, dir, 1, Options{})
	mustClose(t, w)

	for path, want := range map[string]os.FileMode{dir: 0o700, Path(dir, 1): 0o600} {
		st, err := os.Stat(path)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		if got := st.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o", path, got, want)
		}
	}
}

func TestACreatedBlobIsNeverOverwritten(t *testing.T) {
	dir := t.TempDir()
	w := mustCreate(t, dir, 5, Options{})
	_, _ = w.Write([]byte("evidence"))
	mustClose(t, w)

	_, err := Create(dir, 5, Options{})
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("second Create err = %v, want one wrapping fs.ErrExist", err)
	}
	if got := readFile(t, Path(dir, 5)); string(got) != "evidence" {
		t.Errorf("the earlier output was damaged: %q", got)
	}
}

func TestDifferentBlobsDoNotShareAFile(t *testing.T) {
	dir := t.TempDir()
	a := mustCreate(t, dir, 1, Options{})
	b := mustCreate(t, dir, 2, Options{})
	_, _ = a.Write([]byte("first"))
	_, _ = b.Write([]byte("second"))
	ia, ib := mustClose(t, a), mustClose(t, b)

	if ia.SHA256 == ib.SHA256 {
		t.Error("two different outputs produced the same hash")
	}
	if string(readFile(t, Path(dir, 1))) != "first" || string(readFile(t, Path(dir, 2))) != "second" {
		t.Error("outputs were mixed up between blobs")
	}
}

func TestCreateFailsWhenTheDirectoryCannotBeMade(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A regular file where a directory is needed.
	if _, err := Create(filepath.Join(blocker, "raw"), 1, Options{}); err == nil {
		t.Error("expected an error when the directory cannot be created")
	}
}

// A symlink planted where the output file goes must not be written through.
func TestCreateDoesNotFollowASymlink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.Symlink(victim, Path(dir, 1)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := Create(dir, 1, Options{}); err == nil {
		t.Error("Create followed or replaced a symlink")
	}
	if _, err := os.Stat(victim); !errors.Is(err, fs.ErrNotExist) {
		t.Error("something was written through the symlink")
	}
}

// --- Closing ----------------------------------------------------------------------------

func TestCloseIsIdempotent(t *testing.T) {
	w := mustCreate(t, t.TempDir(), 1, Options{})
	_, _ = w.Write([]byte("data"))

	first, err1 := w.Close()
	second, err2 := w.Close()
	third, err3 := w.Close()
	if err1 != nil || err2 != nil || err3 != nil {
		t.Fatalf("Close errors: %v, %v, %v", err1, err2, err3)
	}
	if first != second || second != third {
		t.Errorf("repeated Close gave different results: %+v, %+v, %+v", first, second, third)
	}
}

func TestWriteAfterCloseFails(t *testing.T) {
	dir := t.TempDir()
	w := mustCreate(t, dir, 1, Options{})
	_, _ = w.Write([]byte("abc"))
	info := mustClose(t, w)

	n, err := w.Write([]byte("more"))
	if n != 0 || !errors.Is(err, os.ErrClosed) {
		t.Errorf("Write after Close = %d, %v; want 0, os.ErrClosed", n, err)
	}
	if got := readFile(t, Path(dir, 1)); string(got) != "abc" {
		t.Errorf("file changed after Close: %q", got)
	}
	if again, _ := w.Close(); again != info {
		t.Errorf("Info changed after a rejected write: %+v vs %+v", again, info)
	}
}

func TestWriteReportsDiskErrors(t *testing.T) {
	w := mustCreate(t, t.TempDir(), 1, Options{})
	// Close the file behind the writer's back: the next write must fail loudly instead
	// of pretending the output was stored.
	if err := w.f.Close(); err != nil {
		t.Fatal(err)
	}
	if n, err := w.Write([]byte("lost")); err == nil || n != 0 {
		t.Errorf("Write on a broken file = %d, %v; want an error", n, err)
	}
	if w.Size() != 0 {
		t.Errorf("Size = %d after a failed write, want 0", w.Size())
	}
	if _, err := w.Close(); err == nil {
		t.Error("Close on a broken file must report the error")
	}
}

// --- Streaming: the whole point ---------------------------------------------------------

// Writing a large output must not use memory in proportion to its size. This is the
// property that keeps a 10,000-line scan (or a 100 MB dump) out of the process heap.
func TestLargeOutputIsStreamedNotBuffered(t *testing.T) {
	if testing.Short() {
		t.Skip("writes about 100 MB")
	}
	dir := t.TempDir()
	w := mustCreate(t, dir, 1, Options{MaxBytes: 1 << 30})

	chunk := bytes.Repeat([]byte("one line of scanner output 0123456789\n"), 850) // about 32 KB
	const chunks = 3_200                                                          // about 100 MB
	want := sha256.New()

	var ms runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms)
	before := ms.HeapAlloc

	var peak uint64
	for i := range chunks {
		if _, err := w.Write(chunk); err != nil {
			t.Fatal(err)
		}
		want.Write(chunk)
		if i%200 == 0 {
			runtime.ReadMemStats(&ms)
			peak = max(peak, ms.HeapAlloc)
		}
	}
	info := mustClose(t, w)

	total := int64(len(chunk)) * chunks
	if info.Size != total || info.Truncated {
		t.Fatalf("info = %+v, want %d bytes untruncated", info, total)
	}
	if info.SHA256 != hex.EncodeToString(want.Sum(nil)) {
		t.Error("hash of the 100 MB output is wrong")
	}
	if st, err := os.Stat(Path(dir, 1)); err != nil || st.Size() != total {
		t.Errorf("file size = %v, %v; want %d", st, err, total)
	}

	// The output is about 100 MB; the heap must stay a small fraction of that.
	const allowed = 16 << 20
	if grown := int64(peak) - int64(before); grown > allowed {
		t.Errorf("heap grew by %d MB while streaming 100 MB: the output is being buffered", grown>>20)
	}
}

func TestTenThousandLines(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 10_000; i++ {
		b.WriteString("line ")
		b.WriteString(strings.Repeat("x", i%40))
		b.WriteByte('\n')
	}
	data := []byte(b.String())

	dir := t.TempDir()
	w := mustCreate(t, dir, 1, Options{})
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	info := mustClose(t, w)
	if info.Size != int64(len(data)) || info.SHA256 != sum(data) {
		t.Errorf("info = %+v, want %d bytes with a matching hash", info, len(data))
	}
	if got := readFile(t, Path(dir, 1)); !bytes.Equal(got, data) {
		t.Error("the 10,000-line output was not stored in full")
	}
}
