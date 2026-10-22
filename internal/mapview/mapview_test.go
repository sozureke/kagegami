package mapview

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sozureke/kagegami/internal/pipeline"
	"github.com/sozureke/kagegami/internal/recon"
	"github.com/sozureke/kagegami/internal/recon/ffuf"
	"github.com/sozureke/kagegami/internal/recon/gobuster"
	"github.com/sozureke/kagegami/internal/recon/nmap"
	"github.com/sozureke/kagegami/internal/recon/whatweb"
	"github.com/sozureke/kagegami/internal/secret"
	"github.com/sozureke/kagegami/internal/store"
	"github.com/sozureke/kagegami/internal/terminal"
)

var (
	update = flag.Bool("update", false, "rewrite the golden files")
	bg     = context.Background()
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(bg, filepath.Join(t.TempDir(), "case.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// feed runs recorded output through the parsers and into the map, as Kagegami does.
func feed(t *testing.T, st *store.Store, cmdLine, file string) {
	t.Helper()
	raw, err := os.ReadFile(file) //nolint:gosec // a test fixture
	if err != nil {
		t.Fatal(err)
	}
	reg := recon.NewRegistry(nmap.New(), ffuf.New(), gobuster.New(), whatweb.New())
	res := reg.Run(bg, recon.Command{Line: cmdLine}, terminal.CleanString(string(raw)))
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if _, err := pipeline.Apply(bg, st, res.Delta, nil); err != nil {
		t.Fatal(err)
	}
}

func realMap(t *testing.T) *store.Store {
	t.Helper()
	st := openStore(t)
	feed(t, st, "nmap -sV -sC -p 22,80,8000 127.0.0.1 127.0.0.2", "../recon/nmap/testdata/multi.raw")
	feed(t, st, "nmap -p 8000 --script http-enum 127.0.0.1", "../recon/nmap/testdata/http.raw")
	feed(t, st, "whatweb http://127.0.0.1:8000", "../recon/whatweb/testdata/whatweb-basic.raw")
	feed(t, st, "ffuf -u http://127.0.0.1:8000/FUZZ -w w", "../recon/ffuf/testdata/ffuf-basic.raw")
	feed(t, st, "gobuster dir -u http://127.0.0.1:8000 -w w -x html,txt", "../recon/gobuster/testdata/gobuster-x.raw")
	return st
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path) //nolint:gosec // a test fixture
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Errorf("%s differs:\n%s", name, got)
	}
}

func render(t *testing.T, st *store.Store, o Options) string {
	t.Helper()
	v, err := Load(bg, st)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := Text(&b, v, o); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestTextOfARealMap(t *testing.T) {
	st := realMap(t)
	golden(t, "map.txt", render(t, st, Options{}))
	golden(t, "map-all.txt", render(t, st, Options{All: true}))
}

func TestFoldingHidesClosedPortsAndExplainsIt(t *testing.T) {
	st := realMap(t)
	short, long := render(t, st, Options{}), render(t, st, Options{All: true})
	if strings.Contains(short, "closed") && !strings.Contains(short, "hidden") {
		t.Errorf("closed ports are shown without --all:\n%s", short)
	}
	if !strings.Contains(short, "closed or filtered ports hidden (--all shows them)") {
		t.Errorf("the fold is not announced:\n%s", short)
	}
	if !strings.Contains(long, "80/tcp") || strings.Contains(long, "hidden") {
		t.Errorf("--all must show the closed ports:\n%s", long)
	}
}

func TestEmptyMap(t *testing.T) {
	st := openStore(t)
	if got := render(t, st, Options{}); got != "The map is empty.\n" {
		t.Errorf("got %q", got)
	}
}

func TestCredentialsAreNeverShown(t *testing.T) {
	st := openStore(t)
	const plain = "hunter2-very-secret"
	if _, err := st.Repo().UpsertCredential(bg, store.Credential{Username: "admin", Secret: secret.New(plain), Kind: "password", WorksOn: "10.0.0.5:22/tcp"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Repo().UpsertCredential(bg, store.Credential{Username: "svc", Secret: secret.New("aad3b435:31d6cfe0"), Kind: "hash"}); err != nil {
		t.Fatal(err)
	}
	v, err := Load(bg, st)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range []Options{{}, {All: true}} {
		var b bytes.Buffer
		if err := Text(&b, v, o); err != nil {
			t.Fatal(err)
		}
		text := b.String()
		if strings.Contains(text, plain) || strings.Contains(text, "31d6cfe0") {
			t.Fatalf("a secret reached the text output:\n%s", text)
		}
		if !strings.Contains(text, "admin") || strings.Count(text, secret.Redacted) != 2 || !strings.Contains(text, "10.0.0.5:22/tcp") {
			t.Errorf("credentials are not shown as masks:\n%s", text)
		}
	}
	var b bytes.Buffer
	if err := JSON(&b, v); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), plain) || strings.Contains(b.String(), "31d6cfe0") {
		t.Fatalf("a secret reached the JSON output:\n%s", b.String())
	}
	var decoded View
	if err := json.Unmarshal(b.Bytes(), &decoded); err == nil {
		t.Error("the JSON of a map decoded back into secrets: secret.String must not be decodable")
	}
}

func TestJSONOfARealMap(t *testing.T) {
	st := realMap(t)
	v, err := Load(bg, st)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := JSON(&b, v); err != nil {
		t.Fatal(err)
	}
	var generic map[string]any
	if err := json.Unmarshal(b.Bytes(), &generic); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"hosts", "findings", "credentials", "notes"} {
		if _, ok := generic[key].([]any); !ok {
			t.Errorf("%q must be a list, even when it is empty", key)
		}
	}
	hosts := generic["hosts"].([]any)
	if len(hosts) != 2 {
		t.Fatalf("%d hosts", len(hosts))
	}
	// the JSON is complete: the closed ports that the text folds are there
	if !strings.Contains(b.String(), `"state": "closed"`) {
		t.Error("the JSON leaves out closed ports")
	}
}

// What a target sends must not be able to act on the terminal of the person reading the map.
func TestHostileTextIsCleaned(t *testing.T) {
	st := openStore(t)
	evil := "x\x1b]0;pwned\x07\x1b[2J\x1b[31m\u202eevil\u2066\r\n\x00\u200b\u0085end"
	d := recon.StateDelta{
		Hosts:    []recon.HostDelta{{IP: "10.0.0.1", Hostname: "box", OS: evil}},
		Services: []recon.ServiceDelta{{HostIP: "10.0.0.1", Port: 80, Proto: "tcp", State: "open", Name: "http", Version: evil}},
		WebPaths: []recon.WebPathDelta{{HostIP: "10.0.0.1", Port: 80, Proto: "tcp", Path: "/a" + evil[:6], Note: evil}},
		Findings: []recon.FindingDelta{{Severity: "low", Title: evil}},
		Notes:    []recon.NoteDelta{{Text: evil}},
	}
	if _, err := pipeline.Apply(bg, st, d, nil); err != nil {
		t.Fatal(err)
	}
	for _, o := range []Options{{}, {All: true}} {
		out := render(t, st, o)
		for _, r := range out {
			if r != '\n' && (r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || (r >= 0x200b && r <= 0x200f) || (r >= 0x202a && r <= 0x202e) || (r >= 0x2060 && r <= 0x2069) || r == 0xfeff) {
				t.Fatalf("the output holds the character %U:\n%q", r, out)
			}
		}
		if !strings.Contains(out, "evil") {
			t.Error("the readable part of the text was lost")
		}
	}
}

func TestLongListsAreFolded(t *testing.T) {
	st := openStore(t)
	var d recon.StateDelta
	for i := range 40 {
		d.WebPaths = append(d.WebPaths, recon.WebPathDelta{HostIP: "10.0.0.1", Port: 80, Proto: "tcp", Path: fmt.Sprintf("/p%02d", i)})
		d.Notes = append(d.Notes, recon.NoteDelta{Text: fmt.Sprintf("note %02d %s", i, strings.Repeat("long ", 40))})
	}
	if _, err := pipeline.Apply(bg, st, d, nil); err != nil {
		t.Fatal(err)
	}
	short := render(t, st, Options{})
	if !strings.Contains(short, "/p14") || strings.Contains(short, "/p15") || !strings.Contains(short, "... 25 more paths (--all shows them)") {
		t.Errorf("paths are not folded as promised:\n%s", short)
	}
	if !strings.Contains(short, "Notes (the last 5 of 40, --all shows them)") || !strings.Contains(short, "note 39") || strings.Contains(short, "note 34") || !strings.Contains(short, "…") {
		t.Errorf("notes are not folded as promised:\n%s", short)
	}
	long := render(t, st, Options{All: true})
	if !strings.Contains(long, "/p39") || !strings.Contains(long, "note 00") || strings.Contains(long, "more paths") {
		t.Error("--all must show everything")
	}
}

func TestLoadIsStable(t *testing.T) {
	st := realMap(t)
	a, b := render(t, st, Options{All: true}), render(t, st, Options{All: true})
	if a != b {
		t.Error("two reads of the same map differ")
	}
}

func TestStatsShowWhatTheModelsUsed(t *testing.T) {
	st := openStore(t)
	for _, u := range []store.Usage{
		{Tier: "tier1", Model: "ollama/llama3.1:8b", PromptTokens: 700, CompletionTokens: 90, OK: true},
		{Tier: "tier1", Model: "ollama/llama3.1:8b", PromptTokens: 300, CompletionTokens: 0, OK: false},
		{Tier: "tier2", Model: "ollama/llama3.1:8b", PromptTokens: 2000, CompletionTokens: 400, OK: true},
	} {
		if err := st.Repo().AddUsage(bg, u); err != nil {
			t.Fatal(err)
		}
	}
	v, err := Load(bg, st)
	if err != nil {
		t.Fatal(err)
	}
	var plain, stats bytes.Buffer
	if err := Text(&plain, v, Options{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain.String(), "tokens") {
		t.Errorf("usage is shown without --stats:\n%s", plain.String())
	}
	if err := Text(&stats, v, Options{Stats: true}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Model use on this case",
		"tier1  ollama/llama3.1:8b",
		"2 calls, 1 without a usable answer: 1000 prompt + 90 answer = 1090 tokens",
		"tier2  ollama/llama3.1:8b",
		"1 calls: 2000 prompt + 400 answer = 2400 tokens",
	} {
		if !strings.Contains(stats.String(), want) {
			t.Errorf("%q is missing from:\n%s", want, stats.String())
		}
	}
	var js bytes.Buffer
	if err := JSON(&js, v); err != nil {
		t.Fatal(err)
	}
	var back struct {
		Usage []Usage `json:"usage"`
	}
	if err := json.Unmarshal(js.Bytes(), &back); err != nil || len(back.Usage) != 2 || back.Usage[0].PromptTokens != 1000 || back.Usage[0].Failed != 1 {
		t.Errorf("json usage = %+v, %v", back.Usage, err)
	}
}

func TestStatsOfACaseWithoutModelsAndOfAnEmptyMap(t *testing.T) {
	st := openStore(t)
	if got := render(t, st, Options{Stats: true}); got != "The map is empty.\n\nModel use on this case\n  no model has been used\n" {
		t.Errorf("got %q", got)
	}
	v, _ := Load(bg, st)
	var js bytes.Buffer
	_ = JSON(&js, v)
	if !strings.Contains(js.String(), `"usage": []`) {
		t.Errorf("an empty list of usage must be a list:\n%s", js.String())
	}
}

func TestSourceBlobIDIsCarriedButNeverShown(t *testing.T) {
	st := openStore(t)
	blob, err := st.Repo().CreateBlob(bg, "nmap x")
	if err != nil {
		t.Fatal(err)
	}
	d := recon.StateDelta{
		Hosts:    []recon.HostDelta{{IP: "10.0.0.1"}},
		Services: []recon.ServiceDelta{{HostIP: "10.0.0.1", Port: 22, Proto: "tcp"}},
		WebPaths: []recon.WebPathDelta{{HostIP: "10.0.0.1", Port: 80, Proto: "tcp", Path: "/a"}},
		Findings: []recon.FindingDelta{{Severity: "info", Title: "f"}},
	}
	if _, err := pipeline.Apply(bg, st, d, &blob.ID); err != nil {
		t.Fatal(err)
	}
	v, err := Load(bg, st)
	if err != nil {
		t.Fatal(err)
	}
	h := v.Hosts[0]
	if h.SourceBlobID == nil || *h.SourceBlobID != blob.ID {
		t.Errorf("host SourceBlobID = %v, want %d", h.SourceBlobID, blob.ID)
	}
	for _, svc := range h.Services {
		if svc.SourceBlobID == nil || *svc.SourceBlobID != blob.ID {
			t.Errorf("service %d SourceBlobID = %v", svc.Port, svc.SourceBlobID)
		}
		for _, p := range svc.Paths {
			if p.SourceBlobID == nil || *p.SourceBlobID != blob.ID {
				t.Errorf("path %q SourceBlobID = %v", p.Path, p.SourceBlobID)
			}
		}
	}
	if v.Findings[0].SourceBlobID == nil || *v.Findings[0].SourceBlobID != blob.ID {
		t.Errorf("finding SourceBlobID = %v", v.Findings[0].SourceBlobID)
	}

	var jsBuf bytes.Buffer
	if err := JSON(&jsBuf, v); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(jsBuf.String(), "source_blob") || strings.Contains(jsBuf.String(), "SourceBlobID") {
		t.Errorf("the source blob leaked into the JSON a person sees:\n%s", jsBuf.String())
	}
	var textBuf bytes.Buffer
	if err := Text(&textBuf, v, Options{All: true}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(textBuf.String(), "blob") {
		t.Errorf("the source blob leaked into the text a person sees:\n%s", textBuf.String())
	}
}
