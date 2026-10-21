package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite" // the driver, to sabotage the database from a second connection

	"github.com/sozureke/kagegami/internal/recon"
	"github.com/sozureke/kagegami/internal/recon/nmap"
	"github.com/sozureke/kagegami/internal/store"
	"github.com/sozureke/kagegami/internal/terminal"
)

var bg = context.Background()

func openStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "case.db")
	st, err := store.Open(bg, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st, path
}

func ptr(n int) *int { return &n }

// snapshot is the whole map, for comparing two states of it.
type snapshot struct {
	hosts    []store.Host
	services map[int64][]store.Service
	paths    map[int64][]store.WebPath
	findings []store.Finding
	journal  []store.JournalEntry
}

func take(t *testing.T, st *store.Store) snapshot {
	t.Helper()
	r := st.Repo()
	s := snapshot{services: map[int64][]store.Service{}, paths: map[int64][]store.WebPath{}}
	var err error
	if s.hosts, err = r.ListHosts(bg); err != nil {
		t.Fatal(err)
	}
	for _, h := range s.hosts {
		svcs, err := r.ListServices(bg, h.ID)
		if err != nil {
			t.Fatal(err)
		}
		s.services[h.ID] = svcs
		for _, sv := range svcs {
			if s.paths[sv.ID], err = r.ListWebPaths(bg, sv.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if s.findings, err = r.ListFindings(bg); err != nil {
		t.Fatal(err)
	}
	if s.journal, err = r.ListJournal(bg); err != nil {
		t.Fatal(err)
	}
	return s
}

func (s snapshot) counts() (hosts, services, paths, findings, journal int) {
	for _, l := range s.services {
		services += len(l)
	}
	for _, l := range s.paths {
		paths += len(l)
	}
	return len(s.hosts), services, paths, len(s.findings), len(s.journal)
}

func TestApplyBuildsTheMapFromRealNmapOutput(t *testing.T) {
	raw, err := os.ReadFile("../recon/nmap/testdata/http.raw")
	if err != nil {
		t.Fatal(err)
	}
	delta, err := nmap.New().Parse(bg, recon.Command{Line: "nmap x"}, terminal.CleanString(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	st, _ := openStore(t)

	added, err := Apply(bg, st, delta, nil)
	if err != nil {
		t.Fatal(err)
	}
	if added.Hosts != 1 || added.Services != 1 || added.WebPaths != 5 || added.Notes != 2 || added.Findings != 0 || added.Total() != 9 {
		t.Errorf("added = %+v", added)
	}
	before := take(t, st)

	again, err := Apply(bg, st, delta, nil)
	if err != nil {
		t.Fatal(err)
	}
	if again.Total() != 0 {
		t.Errorf("a repeated scan added %+v: it must refresh, not duplicate", again)
	}
	after := take(t, st)
	h, s, p, f, j := after.counts()
	bh, bs, bp, bf, bj := before.counts()
	if h != bh || s != bs || p != bp || f != bf || j != bj {
		t.Errorf("counts changed from (%d %d %d %d %d) to (%d %d %d %d %d)", bh, bs, bp, bf, bj, h, s, p, f, j)
	}
	if after.hosts[0].IP != "127.0.0.1" || after.hosts[0].Hostname != "localhost" || after.hosts[0].Status != "up" {
		t.Errorf("host = %+v", after.hosts[0])
	}
}

func TestApplyRefreshesWhatIsKnown(t *testing.T) {
	st, _ := openStore(t)
	first := recon.StateDelta{
		Hosts:    []recon.HostDelta{{IP: "10.0.0.1", Hostname: "box.htb", Status: "up"}},
		Services: []recon.ServiceDelta{{HostIP: "10.0.0.1", Port: 22, Proto: "tcp", State: "open", Name: "ssh", Version: "OpenSSH 7.6"}},
	}
	if _, err := Apply(bg, st, first, nil); err != nil {
		t.Fatal(err)
	}
	second := recon.StateDelta{
		Hosts:    []recon.HostDelta{{IP: "10.0.0.1", OS: "Linux", Status: "down"}},
		Services: []recon.ServiceDelta{{HostIP: "10.0.0.1", Port: 22, Proto: "tcp", State: "closed", Version: "OpenSSH 8.9"}},
	}
	added, err := Apply(bg, st, second, nil)
	if err != nil || added.Total() != 0 {
		t.Fatalf("added %+v, %v", added, err)
	}
	s := take(t, st)
	h := s.hosts[0]
	if h.Hostname != "box.htb" || h.OS != "Linux" || h.Status != "down" {
		t.Errorf("host = %+v: an empty field must not erase what is known, a new value must replace it", h)
	}
	svc := s.services[h.ID][0]
	if svc.Name != "ssh" || svc.Version != "OpenSSH 8.9" || svc.State != "closed" {
		t.Errorf("service = %+v", svc)
	}
}

func TestApplyKeepsTheStateWhenThereIsNoNews(t *testing.T) {
	st, _ := openStore(t)
	if _, err := Apply(bg, st, recon.StateDelta{Services: []recon.ServiceDelta{{HostIP: "10.0.0.1", Port: 80, Proto: "tcp", State: "closed"}}}, nil); err != nil {
		t.Fatal(err)
	}
	// a delta with a version but no state (a model may do this), and a path found on the service
	d := recon.StateDelta{
		Services: []recon.ServiceDelta{{HostIP: "10.0.0.1", Port: 80, Proto: "tcp", Version: "nginx"}},
		WebPaths: []recon.WebPathDelta{{HostIP: "10.0.0.1", Port: 80, Proto: "tcp", Path: "/x"}},
	}
	if _, err := Apply(bg, st, d, nil); err != nil {
		t.Fatal(err)
	}
	s := take(t, st)
	svc := s.services[s.hosts[0].ID][0]
	if svc.State != "closed" || svc.Version != "nginx" {
		t.Errorf("service = %+v: state must stay closed", svc)
	}
}

func TestApplyCreatesWhatAPathBelongsTo(t *testing.T) {
	st, _ := openStore(t)
	d := recon.StateDelta{WebPaths: []recon.WebPathDelta{
		{HostIP: "10.0.0.5", Port: 8080, Proto: "tcp", Path: "/admin", StatusCode: ptr(301), Note: "redirect"},
		{HostIP: "10.0.0.5", Port: 8080, Proto: "tcp", Path: "/login", StatusCode: ptr(200)},
	}}
	added, err := Apply(bg, st, d, nil)
	if err != nil {
		t.Fatal(err)
	}
	if added.Hosts != 1 || added.Services != 1 || added.WebPaths != 2 {
		t.Errorf("added = %+v", added)
	}
	s := take(t, st)
	svc := s.services[s.hosts[0].ID][0]
	if svc.Port != 8080 || svc.State != "open" || len(s.paths[svc.ID]) != 2 || *s.paths[svc.ID][0].StatusCode != 301 {
		t.Errorf("service %+v, paths %+v", svc, s.paths[svc.ID])
	}
}

func TestApplyDoesNotRepeatFindingsAndNotes(t *testing.T) {
	st, _ := openStore(t)
	d := recon.StateDelta{
		Findings: []recon.FindingDelta{{Severity: "high", Title: "Anonymous FTP", Description: "x"}, {Severity: "high", Title: "Anonymous FTP"}, {Severity: "low", Title: "Anonymous FTP"}},
		Notes:    []recon.NoteDelta{{Text: "robots.txt lists /secret"}, {Text: "robots.txt lists /secret"}},
	}
	added, err := Apply(bg, st, d, nil)
	if err != nil {
		t.Fatal(err)
	}
	if added.Findings != 2 || added.Notes != 1 {
		t.Errorf("added = %+v: the same finding (severity and title) and the same note count once", added)
	}
	if again, _ := Apply(bg, st, d, nil); again.Total() != 0 {
		t.Errorf("a repeat added %+v", again)
	}
	s := take(t, st)
	if len(s.journal) != 1 || s.journal[0].Author != "ai" {
		t.Errorf("journal = %+v", s.journal)
	}
}

func TestApplyRecordsTheSource(t *testing.T) {
	st, _ := openStore(t)
	blob, err := st.Repo().CreateBlob(bg, "nmap x")
	if err != nil {
		t.Fatal(err)
	}
	d := recon.StateDelta{
		Hosts:    []recon.HostDelta{{IP: "10.0.0.1"}},
		Services: []recon.ServiceDelta{{HostIP: "10.0.0.1", Port: 22, Proto: "tcp"}},
		WebPaths: []recon.WebPathDelta{{HostIP: "10.0.0.1", Port: 80, Proto: "tcp", Path: "/a"}},
		Findings: []recon.FindingDelta{{Severity: "info", Title: "f"}},
		Notes:    []recon.NoteDelta{{Text: "n"}},
	}
	if _, err := Apply(bg, st, d, &blob.ID); err != nil {
		t.Fatal(err)
	}
	s := take(t, st)
	h := s.hosts[0]
	svcs := s.services[h.ID]
	check := func(what string, got *int64) {
		if got == nil || *got != blob.ID {
			t.Errorf("%s does not point to the command it came from", what)
		}
	}
	check("host", h.SourceBlobID)
	check("service 22", svcs[0].SourceBlobID)
	check("implicit service 80", svcs[1].SourceBlobID)
	check("path", s.paths[svcs[1].ID][0].SourceBlobID)
	check("finding", s.findings[0].SourceBlobID)
	check("note", s.journal[0].SourceBlobID)
}

func TestApplyRejectsAnInvalidDeltaWholly(t *testing.T) {
	st, _ := openStore(t)
	d := recon.StateDelta{
		Hosts:    []recon.HostDelta{{IP: "10.0.0.1"}},
		Services: []recon.ServiceDelta{{HostIP: "10.0.0.1", Port: 70000, Proto: "tcp"}},
	}
	if _, err := Apply(bg, st, d, nil); !errors.Is(err, recon.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	if h, _, _, _, _ := take(t, st).counts(); h != 0 {
		t.Error("the valid part of an invalid delta was stored")
	}
}

func TestApplyIsAllOrNothing(t *testing.T) {
	st, path := openStore(t)
	// make the last kind of write fail: a trigger on the table of paths
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(bg, `CREATE TRIGGER no_paths BEFORE INSERT ON web_path BEGIN SELECT RAISE(ABORT, 'no paths today'); END`); err != nil {
		t.Fatal(err)
	}

	d := recon.StateDelta{
		Hosts:    []recon.HostDelta{{IP: "10.0.0.1"}},
		Services: []recon.ServiceDelta{{HostIP: "10.0.0.1", Port: 22, Proto: "tcp"}},
		WebPaths: []recon.WebPathDelta{{HostIP: "10.0.0.1", Port: 80, Proto: "tcp", Path: "/a"}},
		Findings: []recon.FindingDelta{{Severity: "info", Title: "f"}},
	}
	if _, err := Apply(bg, st, d, nil); err == nil {
		t.Fatal("Apply succeeded although a write failed")
	}
	if h, s, p, f, j := take(t, st).counts(); h+s+p+f+j != 0 {
		t.Errorf("part of the delta stayed: %d hosts, %d services, %d paths, %d findings, %d notes", h, s, p, f, j)
	}
}

func TestApplyAnEmptyDelta(t *testing.T) {
	st, _ := openStore(t)
	added, err := Apply(bg, st, recon.StateDelta{}, nil)
	if err != nil || added.Total() != 0 {
		t.Errorf("added %+v, %v", added, err)
	}
}

func TestApplyNormalizesBeforeStoring(t *testing.T) {
	st, _ := openStore(t)
	d := recon.StateDelta{Hosts: []recon.HostDelta{{IP: "::ffff:10.0.0.1", Hostname: " BOX "}}, Services: []recon.ServiceDelta{{HostIP: "10.0.0.1", Port: 22, Proto: "TCP"}}}
	added, err := Apply(bg, st, d, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := take(t, st)
	if len(s.hosts) != 1 || s.hosts[0].IP != "10.0.0.1" || s.hosts[0].Hostname != "box" || added.Hosts != 1 || added.Services != 1 {
		t.Errorf("hosts %+v, added %+v: one host, written in its canonical form", s.hosts, added)
	}
}

func TestApplyDoesNotDowngradeAServiceName(t *testing.T) {
	st, _ := openStore(t)
	versioned := recon.StateDelta{Services: []recon.ServiceDelta{{HostIP: "10.0.0.1", Port: 8000, Proto: "tcp", State: "open", Name: "http", Version: "SimpleHTTPServer 0.6"}}}
	guessed := recon.StateDelta{Services: []recon.ServiceDelta{{HostIP: "10.0.0.1", Port: 8000, Proto: "tcp", State: "open", Name: "http-alt"}}}
	for _, d := range []recon.StateDelta{versioned, guessed} {
		if _, err := Apply(bg, st, d, nil); err != nil {
			t.Fatal(err)
		}
	}
	s := take(t, st)
	svc := s.services[s.hosts[0].ID][0]
	if svc.Name != "http" || svc.Version != "SimpleHTTPServer 0.6" {
		t.Errorf("service = %+v: a scan without a version must not replace what a version scan found", svc)
	}
	// but a scan that brings a version of its own does replace the name
	newer := recon.StateDelta{Services: []recon.ServiceDelta{{HostIP: "10.0.0.1", Port: 8000, Proto: "tcp", Name: "https", Version: "nginx"}}}
	if _, err := Apply(bg, st, newer, nil); err != nil {
		t.Fatal(err)
	}
	if svc := take(t, st).services[s.hosts[0].ID][0]; svc.Name != "https" || svc.Version != "nginx" {
		t.Errorf("service = %+v", svc)
	}
	// and a name is taken when nothing was known
	fresh := recon.StateDelta{Services: []recon.ServiceDelta{{HostIP: "10.0.0.2", Port: 22, Proto: "tcp", Name: "ssh"}}}
	if _, err := Apply(bg, st, fresh, nil); err != nil {
		t.Fatal(err)
	}
}

func TestApplyPlacesPathsFoundOnAHostKnownByName(t *testing.T) {
	st, _ := openStore(t)
	if _, err := Apply(bg, st, recon.StateDelta{Hosts: []recon.HostDelta{{IP: "10.10.10.5", Hostname: "box.htb"}}}, nil); err != nil {
		t.Fatal(err)
	}
	d := recon.StateDelta{WebPaths: []recon.WebPathDelta{
		{Host: "Box.HTB", Port: 80, Proto: "tcp", Path: "/admin", StatusCode: ptr(301)},
		{Host: "other.htb", Port: 8080, Proto: "tcp", Path: "/a", StatusCode: ptr(200)},
		{Host: "other.htb", Port: 8080, Proto: "tcp", Path: "/b"},
	}}
	added, err := Apply(bg, st, d, nil)
	if err != nil {
		t.Fatal(err)
	}
	if added.WebPaths != 1 || added.Services != 1 || added.Hosts != 0 || added.Notes != 1 {
		t.Errorf("added = %+v", added)
	}
	s := take(t, st)
	if len(s.hosts) != 1 || s.paths[s.services[s.hosts[0].ID][0].ID][0].Path != "/admin" {
		t.Errorf("the path was not placed on the known host: %+v", s.paths)
	}
	if len(s.journal) != 1 || s.journal[0].Text != "paths found on other.htb:8080/tcp (address not known): /a (200), /b" {
		t.Errorf("journal = %+v", s.journal)
	}
}
