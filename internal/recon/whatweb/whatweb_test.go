package whatweb

import (
	"context"
	"strings"
	"testing"

	"github.com/sozureke/kagegami/internal/recon"
	"github.com/sozureke/kagegami/internal/recon/recontest"
)

func TestGolden(t *testing.T) { recontest.Golden(t, New()) }

func parse(t *testing.T, output string) recon.StateDelta {
	t.Helper()
	d, err := New().Parse(context.Background(), recon.Command{Line: "whatweb x"}, output)
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
	t.Run("a named host with its address", func(t *testing.T) {
		d := parse(t, "https://box.htb/login [200 OK] Apache[2.4.41], Cookies[sid,theme], HTTPServer[Ubuntu Linux][Apache/2.4.41 (Ubuntu)], IP[10.10.10.5], Title[Login, please]\n")
		if len(d.Hosts) != 1 || d.Hosts[0].IP != "10.10.10.5" || d.Hosts[0].Hostname != "box.htb" {
			t.Errorf("hosts = %+v", d.Hosts)
		}
		if len(d.Services) != 1 || d.Services[0].Name != "https" || d.Services[0].Port != 443 || d.Services[0].Version != "Apache/2.4.41 (Ubuntu)" {
			t.Errorf("services = %+v", d.Services)
		}
		if len(d.WebPaths) != 1 || d.WebPaths[0].Path != "/login" || *d.WebPaths[0].StatusCode != 200 {
			t.Errorf("paths = %+v", d.WebPaths)
		}
		if len(d.Notes) != 1 || !strings.Contains(d.Notes[0].Text, "Title[Login, please]") || !strings.Contains(d.Notes[0].Text, "Cookies[sid,theme]") || strings.Contains(d.Notes[0].Text, "IP[") {
			t.Errorf("notes = %+v", d.Notes)
		}
	})
	t.Run("a named host without an address is only a note", func(t *testing.T) {
		d := parse(t, "http://box.htb [200 OK] Title[Hi]\n")
		if len(d.Hosts)+len(d.Services) != 0 || len(d.Notes) != 1 {
			t.Errorf("delta = %+v", d)
		}
	})
	t.Run("an error line and other text are skipped", func(t *testing.T) {
		if d := parse(t, "ERROR Opening: http://x - Connection refused\nWhatWeb report\n"); !d.IsEmpty() {
			t.Errorf("delta = %+v", d)
		}
	})
	t.Run("plugins are split outside brackets", func(t *testing.T) {
		got := splitPlugins("A[1], B[x, y], C, D[[nested]], E[]")
		if len(got) != 5 || got[1].value != "x, y" || got[2].name != "C" || got[2].value != "" || got[3].value != "[nested]" {
			t.Errorf("plugins = %+v", got)
		}
		if got := splitPlugins("A[unclosed, B[2]"); len(got) == 0 {
			t.Error("unbalanced brackets lost everything")
		}
	})
}

func FuzzParse(f *testing.F) {
	f.Add("http://x [200 OK] A[1], B[x, y], IP[1.2.3.4]\n")
	f.Add("http://[::1]:99999 [200]\n")
	f.Fuzz(func(_ *testing.T, output string) {
		_, _ = New().Parse(context.Background(), recon.Command{Line: "whatweb x"}, output)
	})
}
