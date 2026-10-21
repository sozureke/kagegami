package terminal

import (
	"bytes"
	"flag"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func TestCleanString(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain text", "hello\nworld\n", "hello\nworld\n"},
		{"CRLF becomes LF", "a\r\nb\r\n", "a\nb\n"},
		{"unfinished last line is kept", "a\nb", "a\nb\n"},
		{"empty", "", ""},
		{"blank lines stay", "a\n\nb\n", "a\n\nb\n"},
		{"colours are dropped", "\x1b[1m\x1b[31mred\x1b[0m plain\n", "red plain\n"},
		{"256 and true colour", "\x1b[38;5;196mx\x1b[38;2;1;2;3my\x1b[0m\n", "xy\n"},
		{"carriage return rewrites", "progress 10%\rprogress 99%\rdone        \n", "done\n"},
		{"a shorter rewrite leaves the tail, as on a screen", "progress 99%\rdone\n", "doneress 99%\n"},
		{"carriage return keeps the longer tail", "abcdef\rXY\n", "XYcdef\n"},
		{"erase line", "old text\r\x1b[2Knew\n", "new\n"},
		{"erase to the end", "abcdef\r\x1b[3C\x1b[K\n", "abc\n"},
		{"erase to the start", "abcdef\x1b[3D\x1b[1K\n", "    ef\n"},
		{"progress bar between results", "\r\x1b[2KProgress 1/9\r\x1b[2Kfound.txt (200)\r\n\r\x1b[2KProgress 9/9\r\n", "found.txt (200)\nProgress 9/9\n"},
		{"backspace", "abc\b\bX\n", "aXc\n"},
		{"backspace at the start", "\b\bab\n", "ab\n"},
		{"tab", "a\tb\n", "a       b\n"},
		{"tab at a stop", "12345678\tb\n", "12345678        b\n"},
		{"cursor moves", "abc\x1b[2Dxy\n", "axy\n"},
		{"cursor to a column", "abcdef\x1b[3GXY\n", "abXYef\n"},
		{"delete characters", "abcdef\r\x1b[2C\x1b[2P\n", "abef\n"},
		{"private modes", "\x1b[?2004hprompt\x1b[?2004l\n", "prompt\n"},
		{"title sequence with BEL", "\x1b]0;my title\ax\n", "x\n"},
		{"title sequence with ST", "\x1b]0;my title\x1b\\x\n", "x\n"},
		{"hyperlink", "\x1b]8;;http://x\x1b\\text\x1b]8;;\x1b\\\n", "text\n"},
		{"charset designation", "\x1b(Bx\x1b)0y\n", "xy\n"},
		{"two-byte escapes", "\x1b=a\x1b>b\x1bcc\n", "abc\n"},
		{"clear screen", "junk\x1b[H\x1b[2Jtext\n", "text\n"},
		{"NUL and BEL and DEL", "a\x00b\ac\x7fd\n", "abcd\n"},
		{"trailing spaces go", "a   \nb \t\n", "a\nb\n"},
		{"leading spaces stay", "   a\n", "   a\n"},
		{"unicode", "привет мир 日本語 🙂\n", "привет мир 日本語 🙂\n"},
		{"invalid UTF-8", "a\xffb\xc3\n", "a\ufffdb\ufffd\n"},
		{"a broken sequence starts over", "a\x1b[1;\x1b[31mb\n", "ab\n"},
		{"an unfinished sequence at the end", "ab\x1b[3", "ab\n"},
		{"an unfinished title at the end", "ab\x1b]0;tit", "ab\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CleanString(tt.in); got != tt.want {
				t.Errorf("CleanString(%q)\n got %q\nwant %q", tt.in, got, tt.want)
			}
		})
	}
}

// Real output of real tools, recorded on Kali: what was cleaned must be what a person saw.
func TestCleanGolden(t *testing.T) {
	files, err := filepath.Glob("testdata/clean/*.raw")
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			got := CleanString(string(raw))
			golden := strings.TrimSuffix(f, ".raw") + ".clean"
			if *update {
				if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden) //nolint:gosec // a test fixture
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("cleaned output differs from %s:\n%s", golden, got)
			}
			if strings.ContainsAny(got, "\x1b\r\x00\a") {
				t.Error("control characters are left in the cleaned output")
			}
		})
	}
}

func chunked(t *testing.T, in []byte, size int) string {
	t.Helper()
	var out bytes.Buffer
	c := NewCleaner(&out)
	for len(in) > 0 {
		n := min(size, len(in))
		if _, err := c.Write(in[:n]); err != nil {
			t.Fatal(err)
		}
		in = in[n:]
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// The result must not depend on where the bytes were cut, also inside an escape sequence or a
// multi-byte character.
func TestCleanerIsIndependentOfChunking(t *testing.T) {
	inputs := [][]byte{[]byte("\x1b[1m\x1b[31mred\x1b[0m\r\x1b[2Kпривет 日本語\r\n\x1b]0;t\a\x1b]8;;u\x1b\\x\x1b(Bz\tw\b\bq\n")}
	files, _ := filepath.Glob("testdata/clean/*.raw")
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, raw)
	}
	r := rand.New(rand.NewPCG(1, 2))
	const alphabet = "ab \r\n\t\b\x1b[]();?0123mKGCDJP\x07\\я日\xff"
	for range 500 {
		var b []byte
		for range r.IntN(40) {
			b = append(b, alphabet[r.IntN(len(alphabet))])
		}
		inputs = append(inputs, b)
	}
	for i, in := range inputs {
		want := chunked(t, in, len(in)+1)
		for _, size := range []int{1, 2, 3, 7} {
			if got := chunked(t, in, size); got != want {
				t.Fatalf("input %d, chunks of %d bytes:\n got %q\nwant %q", i, size, got, want)
			}
		}
		if !utf8.ValidString(want) {
			t.Fatalf("input %d: the output is not valid UTF-8: %q", i, want)
		}
	}
}

func TestCleanerBoundsALine(t *testing.T) {
	var out bytes.Buffer
	c := NewCleaner(&out)
	chunk := bytes.Repeat([]byte("x"), 1<<20)
	for range 20 {
		_, _ = c.Write(chunk) // twenty megabytes without a newline
	}
	_ = c.Close()
	if out.Len() > maxLineRunes+1 {
		t.Errorf("a line of %d bytes came out, the limit is %d", out.Len(), maxLineRunes)
	}
	// a huge cursor jump must not make the line grow without bound either
	out.Reset()
	c = NewCleaner(&out)
	_, _ = c.Write([]byte("\x1b[999999999Cx\n\x1b[999999999Gy\n"))
	_ = c.Close()
	if out.Len() > 2*(maxLineRunes+1) {
		t.Errorf("cursor jumps made %d bytes of output", out.Len())
	}
}

type failingWriter struct{ n int }

func (w *failingWriter) Write(p []byte) (int, error) {
	if w.n--; w.n < 0 {
		return 0, os.ErrClosed
	}
	return len(p), nil
}

func TestCleanerReportsAWriteError(t *testing.T) {
	c := NewCleaner(&failingWriter{n: 1})
	_, err := c.Write([]byte("a\nb\nc\n"))
	if err == nil {
		t.Fatal("the error of the underlying writer was swallowed")
	}
	if _, err := c.Write([]byte("d\n")); err == nil {
		t.Error("the Cleaner went on after a failure")
	}
}

func FuzzCleaner(f *testing.F) {
	for _, s := range []string{"a\rb\n", "\x1b[2K\x1b[3C\x1b[K", "\x1b]0;x\a", "\xff\xfe", "\x1b[999999999G"} {
		f.Add([]byte(s), 3)
	}
	f.Fuzz(func(t *testing.T, in []byte, size int) {
		size = min(max(size, 1), 16)
		want := chunked(t, in, len(in)+1)
		if got := chunked(t, in, size); got != want {
			t.Fatalf("chunking changed the result:\n got %q\nwant %q", got, want)
		}
		if !utf8.ValidString(want) || strings.ContainsAny(want, "\x1b\r\x00\a\b") {
			t.Fatalf("bad output %q", want)
		}
	})
}
