package gobuster

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
	banner := "[+] Url:                     http://box.htb:8080\nStarting gobuster in directory enumeration mode\n"
	t.Run("a named host and an older output style with a slash", func(t *testing.T) {
		d := parse(t, "gobuster dir -u http://box.htb:8080 -w x", banner+"/admin (Status: 301) [Size: 312] [--> http://box.htb:8080/admin/]\n/config.php (Status: 200)\n")
		if len(d.WebPaths) != 2 || d.WebPaths[0].Host != "box.htb" || d.WebPaths[0].Port != 8080 || d.WebPaths[0].Path != "/admin" || d.WebPaths[1].Path != "/config.php" {
			t.Errorf("paths = %+v", d.WebPaths)
		}
		if d.WebPaths[0].Note != "redirects to http://box.htb:8080/admin/" {
			t.Errorf("note = %q", d.WebPaths[0].Note)
		}
	})
	t.Run("other modes are left alone", func(t *testing.T) {
		for line, out := range map[string]string{
			"gobuster dns -d box.htb -w x":             "Starting gobuster in DNS enumeration mode\nFound: www.box.htb\n",
			"gobuster vhost -u http://box.htb -w x":    "[+] Url: http://box.htb\nStarting gobuster in VHOST enumeration mode\nFound: dev.box.htb Status: 200 [Size: 10]\n",
			"gobuster dir -u http://x -w y --wildcard": "",
		} {
			if d := parse(t, line, out); !d.IsEmpty() {
				t.Errorf("%q gave %+v", line, d)
			}
		}
	})
	t.Run("the mode and the URL come from the command in quiet mode", func(t *testing.T) {
		d := parse(t, "gobuster dir -q --url=http://10.0.0.1/ -w x", "admin (Status: 200) [Size: 1]\n")
		if len(d.WebPaths) != 1 || d.WebPaths[0].HostIP != "10.0.0.1" {
			t.Errorf("paths = %+v", d.WebPaths)
		}
	})
	t.Run("progress and errors are skipped", func(t *testing.T) {
		d := parse(t, "gobuster dir -u http://10.0.0.1 -w x", "Progress: 5 / 10 (50.00%)\n2026/10/22 00:02:39 error on running gobuster on http://10.0.0.1/: connection refused\n")
		if !d.IsEmpty() {
			t.Errorf("delta = %+v", d)
		}
	})
	t.Run("an endless output is cut", func(t *testing.T) {
		var b strings.Builder
		for i := range maxEntries + 10 {
			fmt.Fprintf(&b, "w%d (Status: 200) [Size: 1]\n", i)
		}
		d, err := New().Parse(context.Background(), recon.Command{Line: "gobuster dir -u http://10.0.0.1"}, b.String())
		if err == nil || len(d.WebPaths) > maxEntries {
			t.Errorf("err %v, %d paths", err, len(d.WebPaths))
		}
	})
}

func FuzzParse(f *testing.F) {
	f.Add("admin (Status: 301) [Size: 0] [--> /admin/]\n", "gobuster dir -q -u http://x")
	f.Add("\x00(Status", "gobuster")
	f.Fuzz(func(_ *testing.T, output, line string) {
		_, _ = New().Parse(context.Background(), recon.Command{Line: line}, output)
	})
}
