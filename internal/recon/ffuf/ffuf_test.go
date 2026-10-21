package ffuf

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/sozureke/kagegami/internal/recon"
	"github.com/sozureke/kagegami/internal/recon/recontest"
)

func TestGolden(t *testing.T) { recontest.Golden(t, New()) }

func parse(t *testing.T, line, output string) recon.StateDelta {
	t.Helper()
	d, err := New().Parse(context.Background(), recon.Command{Line: line}, output)
	if err != nil {
		t.Fatal(err)
	}
	checked, err := d.Validate()
	if err != nil {
		t.Fatal(err)
	}
	return checked
}

func TestSynthetic(t *testing.T) {
	banner := " :: URL              : http://box.htb/app/FUZZ\n"
	t.Run("a named host and a path under a directory", func(t *testing.T) {
		d := parse(t, "ffuf -u http://box.htb/app/FUZZ -w x", banner+"login                   [Status: 200, Size: 5, Words: 1, Lines: 1, Duration: 3ms]\n")
		if len(d.WebPaths) != 1 || d.WebPaths[0].Host != "box.htb" || d.WebPaths[0].HostIP != "" || d.WebPaths[0].Path != "/app/login" || d.WebPaths[0].Port != 80 {
			t.Errorf("paths = %+v", d.WebPaths)
		}
	})
	t.Run("https and a port", func(t *testing.T) {
		d := parse(t, "ffuf -u https://10.0.0.1:8443/FUZZ", " :: URL              : https://10.0.0.1:8443/FUZZ\na [Status: 403, Size: 1, Words: 1, Lines: 1, Duration: 1ms]\n")
		if len(d.WebPaths) != 1 || d.WebPaths[0].HostIP != "10.0.0.1" || d.WebPaths[0].Port != 8443 || *d.WebPaths[0].StatusCode != 403 {
			t.Errorf("paths = %+v", d.WebPaths)
		}
	})
	t.Run("the url comes from the command when the banner is gone", func(t *testing.T) {
		d := parse(t, "ffuf -s -u http://10.0.0.1/FUZZ -w x", "admin\nbackup\n")
		if len(d.WebPaths) != 2 || d.WebPaths[1].Path != "/backup" || d.WebPaths[0].StatusCode != nil {
			t.Errorf("paths = %+v", d.WebPaths)
		}
		d = parse(t, "ffuf -u=http://10.0.0.1/FUZZ -s", "admin\n")
		if len(d.WebPaths) != 1 {
			t.Errorf("-u=URL was not understood: %+v", d.WebPaths)
		}
	})
	t.Run("no URL, no result", func(t *testing.T) {
		if d := parse(t, "ffuf -w x", "admin [Status: 200, Size: 1, Words: 1, Lines: 1]\n"); !d.IsEmpty() {
			t.Errorf("delta = %+v", d)
		}
	})
	t.Run("the same hit is stored once", func(t *testing.T) {
		line := "a [Status: 200, Size: 1, Words: 1, Lines: 1, Duration: 1ms]\n"
		if d := parse(t, "ffuf -u http://10.0.0.1/FUZZ", " :: URL : http://10.0.0.1/FUZZ\n"+line+line); len(d.WebPaths) != 1 {
			t.Errorf("paths = %+v", d.WebPaths)
		}
	})
	t.Run("output of another command is not taken for hits", func(t *testing.T) {
		if d := parse(t, "ffuf -u http://10.0.0.1/FUZZ | tee o", " :: URL : http://10.0.0.1/FUZZ\nsome banner text with words\nnot a hit [Status: x]\n"); !d.IsEmpty() {
			t.Errorf("delta = %+v", d)
		}
	})
	t.Run("an endless output is cut", func(t *testing.T) {
		var b strings.Builder
		b.WriteString(" :: URL : http://10.0.0.1/FUZZ\n")
		for i := range maxEntries + 10 {
			fmt.Fprintf(&b, "w%d [Status: 200, Size: 1, Words: 1, Lines: 1]\n", i)
		}
		d, err := New().Parse(context.Background(), recon.Command{Line: "ffuf"}, b.String())
		if err == nil || len(d.WebPaths) > maxEntries {
			t.Errorf("err %v, %d paths", err, len(d.WebPaths))
		}
	})
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{" :: URL : http://x/FUZZ\na [Status: 200, Size: 1, Words: 1, Lines: 1]\n", "[Status: 1]", "\x00"} {
		f.Add(s, "ffuf -s -u http://x/FUZZ")
	}
	f.Fuzz(func(_ *testing.T, output, line string) {
		d, err := New().Parse(context.Background(), recon.Command{Line: line}, output)
		if err == nil {
			if _, verr := d.Validate(); verr != nil {
				_ = verr // invalid values are the registry's business; the parser must only not panic or hang
			}
		}
	})
}
