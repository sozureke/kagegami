package store

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"
)

// newRepo opens a store whose clock the test controls.
func newRepo(t *testing.T) (*Store, *Repo, *time.Time) {
	t.Helper()
	s, _ := openTemp(t)
	clock := time.UnixMilli(1_000)
	s.now = func() time.Time { return clock }
	return s, s.Repo(), &clock
}

func ptr[T any](v T) *T { return &v }

func mustHost(t *testing.T, r *Repo, h Host) Host {
	t.Helper()
	got, err := r.UpsertHost(bg, h)
	if err != nil {
		t.Fatalf("UpsertHost(%+v): %v", h, err)
	}
	return got
}

func mustService(t *testing.T, r *Repo, s Service) Service {
	t.Helper()
	got, err := r.UpsertService(bg, s)
	if err != nil {
		t.Fatalf("UpsertService(%+v): %v", s, err)
	}
	return got
}

// --- Hosts ------------------------------------------------------------------------------

func TestUpsertHostInsertsWithDefaults(t *testing.T) {
	_, r, _ := newRepo(t)

	h := mustHost(t, r, Host{IP: "10.0.0.1"})
	if h.ID == 0 {
		t.Error("ID was not returned")
	}
	if h.Status != "unknown" {
		t.Errorf("Status = %q, want the default \"unknown\"", h.Status)
	}
	if h.Hostname != "" || h.OS != "" {
		t.Errorf("Hostname/OS = %q/%q, want empty", h.Hostname, h.OS)
	}
	if !h.FirstSeen.Equal(time.UnixMilli(1_000)) || !h.LastSeen.Equal(time.UnixMilli(1_000)) {
		t.Errorf("FirstSeen/LastSeen = %v/%v, want both at the clock time", h.FirstSeen, h.LastSeen)
	}
	if h.SourceBlobID != nil {
		t.Errorf("SourceBlobID = %v, want nil", *h.SourceBlobID)
	}
}

func TestUpsertHostRepeatedScanUpdatesInsteadOfDuplicating(t *testing.T) {
	_, r, clock := newRepo(t)

	first := mustHost(t, r, Host{IP: "10.0.0.1", Hostname: "web01", OS: "Linux", Status: "up"})
	*clock = time.UnixMilli(2_000)
	second := mustHost(t, r, Host{IP: "10.0.0.1"})

	if second.ID != first.ID {
		t.Errorf("ID changed from %d to %d: a repeated scan must update the same row", first.ID, second.ID)
	}
	hosts, err := r.ListHosts(bg)
	if err != nil || len(hosts) != 1 {
		t.Fatalf("ListHosts = %d hosts, %v; want exactly 1", len(hosts), err)
	}
	if !second.FirstSeen.Equal(time.UnixMilli(1_000)) {
		t.Errorf("FirstSeen = %v, want it unchanged at 1000", second.FirstSeen)
	}
	if !second.LastSeen.Equal(time.UnixMilli(2_000)) {
		t.Errorf("LastSeen = %v, want it refreshed to 2000", second.LastSeen)
	}
}

func TestUpsertHostEmptyValuesNeverEraseKnownOnes(t *testing.T) {
	_, r, _ := newRepo(t)
	mustHost(t, r, Host{IP: "10.0.0.1", Hostname: "web01", OS: "Linux", Status: "up"})

	got := mustHost(t, r, Host{IP: "10.0.0.1"}) // a scan that learned nothing new
	if got.Hostname != "web01" {
		t.Errorf("Hostname = %q, want web01 kept", got.Hostname)
	}
	if got.OS != "Linux" {
		t.Errorf("OS = %q, want Linux kept", got.OS)
	}
	// Regression guard: an empty status is stored as "unknown" on insert, and that
	// default must not overwrite a known status on update.
	if got.Status != "up" {
		t.Errorf("Status = %q, want up kept (the unknown default must not win)", got.Status)
	}
}

func TestUpsertHostNewValuesReplaceOldOnes(t *testing.T) {
	_, r, _ := newRepo(t)
	mustHost(t, r, Host{IP: "10.0.0.1", Hostname: "web01", OS: "Linux", Status: "up"})

	got := mustHost(t, r, Host{IP: "10.0.0.1", Hostname: "web02", OS: "Ubuntu 22.04", Status: "down"})
	if got.Hostname != "web02" || got.OS != "Ubuntu 22.04" || got.Status != "down" {
		t.Errorf("got %q/%q/%q, want the newer non-empty values", got.Hostname, got.OS, got.Status)
	}
}

func TestUpsertHostSourceBlobFollowsTheLatestScan(t *testing.T) {
	s, r, _ := newRepo(t)
	for _, id := range []int64{1, 2} {
		mustExec(t, s, "INSERT INTO output_blob (id, cmd, started_at) VALUES (?, 'nmap', 1)", id)
	}

	h := mustHost(t, r, Host{IP: "10.0.0.1", SourceBlobID: ptr(int64(1))})
	if h.SourceBlobID == nil || *h.SourceBlobID != 1 {
		t.Fatalf("SourceBlobID = %v, want 1", h.SourceBlobID)
	}
	h = mustHost(t, r, Host{IP: "10.0.0.1", SourceBlobID: ptr(int64(2))})
	if *h.SourceBlobID != 2 {
		t.Errorf("SourceBlobID = %d, want 2 (the latest evidence)", *h.SourceBlobID)
	}
	h = mustHost(t, r, Host{IP: "10.0.0.1"})
	if h.SourceBlobID == nil || *h.SourceBlobID != 2 {
		t.Errorf("SourceBlobID = %v, want 2 kept when a scan has no source", h.SourceBlobID)
	}
}

func TestUpsertHostNormalizesAndValidatesTheIP(t *testing.T) {
	_, r, _ := newRepo(t)

	// Different spellings of the same address are one host.
	a := mustHost(t, r, Host{IP: "2001:0db8:0000:0000:0000:0000:0000:0001"})
	b := mustHost(t, r, Host{IP: "2001:db8::1"})
	if a.ID != b.ID || b.IP != "2001:db8::1" {
		t.Errorf("IPv6 spellings produced %d (%s) and %d (%s), want one host with the short form", a.ID, a.IP, b.ID, b.IP)
	}

	for _, bad := range []string{"", "nope", "10.0.0", "10.0.0.256", "10.0.0.1/24", "10.0.0.1 ", "example.com"} {
		if _, err := r.UpsertHost(bg, Host{IP: bad}); !errors.Is(err, ErrInvalid) {
			t.Errorf("UpsertHost(%q) err = %v, want ErrInvalid", bad, err)
		}
	}
	if hosts, _ := r.ListHosts(bg); len(hosts) != 1 {
		t.Errorf("rejected input still created hosts: %d in total, want 1", len(hosts))
	}
}

func TestGetHostByIP(t *testing.T) {
	_, r, _ := newRepo(t)
	want := mustHost(t, r, Host{IP: "10.0.0.1", Hostname: "web01"})

	got, err := r.GetHostByIP(bg, "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("GetHostByIP = %+v, want %+v", got, want)
	}

	if _, err := r.GetHostByIP(bg, "9.9.9.9"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing host err = %v, want ErrNotFound", err)
	}
	if _, err := r.GetHostByIP(bg, "not an ip"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad ip err = %v, want ErrInvalid", err)
	}
}

func TestListHostsKeepsFirstSeenOrder(t *testing.T) {
	_, r, _ := newRepo(t)
	if hosts, err := r.ListHosts(bg); err != nil || len(hosts) != 0 {
		t.Fatalf("empty store: %d hosts, %v", len(hosts), err)
	}

	for _, ip := range []string{"10.0.0.9", "10.0.0.1", "10.0.0.5"} {
		mustHost(t, r, Host{IP: ip})
	}
	mustHost(t, r, Host{IP: "10.0.0.9"}) // seen again: must not move to the end

	hosts, err := r.ListHosts(bg)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, h := range hosts {
		got = append(got, h.IP)
	}
	if want := []string{"10.0.0.9", "10.0.0.1", "10.0.0.5"}; !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// --- Services ---------------------------------------------------------------------------

func TestUpsertServiceDeduplicatesByHostPortProto(t *testing.T) {
	_, r, _ := newRepo(t)
	h := mustHost(t, r, Host{IP: "10.0.0.1"})

	a := mustService(t, r, Service{HostID: h.ID, Port: 80, Proto: "tcp", Name: "http"})
	b := mustService(t, r, Service{HostID: h.ID, Port: 80, Proto: "tcp"})
	udp := mustService(t, r, Service{HostID: h.ID, Port: 80, Proto: "udp"})

	if a.ID != b.ID {
		t.Errorf("same host, port and protocol gave ids %d and %d, want one row", a.ID, b.ID)
	}
	if udp.ID == a.ID {
		t.Error("tcp/80 and udp/80 must be different services")
	}
	services, err := r.ListServices(bg, h.ID)
	if err != nil || len(services) != 2 {
		t.Fatalf("ListServices = %d, %v; want 2", len(services), err)
	}
}

func TestUpsertServiceMergeRules(t *testing.T) {
	_, r, _ := newRepo(t)
	h := mustHost(t, r, Host{IP: "10.0.0.1"})

	first := mustService(t, r, Service{HostID: h.ID, Port: 80, Proto: "tcp", Name: "http", Version: "nginx 1.18"})
	if first.State != "open" {
		t.Errorf("State = %q, want the default \"open\"", first.State)
	}

	got := mustService(t, r, Service{HostID: h.ID, Port: 80, Proto: "tcp"})
	if got.Name != "http" || got.Version != "nginx 1.18" {
		t.Errorf("Name/Version = %q/%q, want them kept when the new scan has none", got.Name, got.Version)
	}

	got = mustService(t, r, Service{HostID: h.ID, Port: 80, Proto: "tcp", State: "closed"})
	if got.State != "closed" {
		t.Errorf("State = %q, want closed: state follows the latest scan", got.State)
	}
	got = mustService(t, r, Service{HostID: h.ID, Port: 80, Proto: "tcp", Version: "nginx 1.24"})
	if got.Version != "nginx 1.24" {
		t.Errorf("Version = %q, want the newer non-empty value", got.Version)
	}
	if got.State != "open" {
		t.Errorf("State = %q, want open: a scan without a state means open again", got.State)
	}
}

func TestUpsertServiceValidation(t *testing.T) {
	_, r, _ := newRepo(t)
	h := mustHost(t, r, Host{IP: "10.0.0.1"})

	tests := []struct {
		name string
		svc  Service
	}{
		{"port zero", Service{HostID: h.ID, Port: 0, Proto: "tcp"}},
		{"negative port", Service{HostID: h.ID, Port: -1, Proto: "tcp"}},
		{"port too large", Service{HostID: h.ID, Port: 65536, Proto: "tcp"}},
		{"empty protocol", Service{HostID: h.ID, Port: 80}},
		{"unknown protocol", Service{HostID: h.ID, Port: 80, Proto: "icmp"}},
		{"upper case protocol", Service{HostID: h.ID, Port: 80, Proto: "TCP"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := r.UpsertService(bg, tt.svc); !errors.Is(err, ErrInvalid) {
				t.Errorf("err = %v, want ErrInvalid", err)
			}
		})
	}

	for _, port := range []int{1, 65535} { // the edges are valid
		if _, err := r.UpsertService(bg, Service{HostID: h.ID, Port: port, Proto: "tcp"}); err != nil {
			t.Errorf("port %d: %v", port, err)
		}
	}
}

func TestUpsertServiceNeedsAnExistingHost(t *testing.T) {
	_, r, _ := newRepo(t)
	if _, err := r.UpsertService(bg, Service{HostID: 999, Port: 80, Proto: "tcp"}); err == nil {
		t.Error("expected a foreign key error for an unknown host")
	}
}

func TestListServicesOrderedByPortAndScopedToTheHost(t *testing.T) {
	_, r, _ := newRepo(t)
	h1 := mustHost(t, r, Host{IP: "10.0.0.1"})
	h2 := mustHost(t, r, Host{IP: "10.0.0.2"})
	for _, p := range []int{443, 22, 80} {
		mustService(t, r, Service{HostID: h1.ID, Port: p, Proto: "tcp"})
	}
	mustService(t, r, Service{HostID: h1.ID, Port: 22, Proto: "udp"})
	mustService(t, r, Service{HostID: h2.ID, Port: 8080, Proto: "tcp"})

	services, err := r.ListServices(bg, h1.ID)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range services {
		got = append(got, s.Proto+"/"+strconv.Itoa(s.Port))
	}
	if want := []string{"tcp/22", "udp/22", "tcp/80", "tcp/443"}; !slices.Equal(got, want) {
		t.Errorf("services = %v, want %v", got, want)
	}

	if none, err := r.ListServices(bg, 999); err != nil || len(none) != 0 {
		t.Errorf("unknown host: %d services, %v; want none", len(none), err)
	}
}

// --- Web paths --------------------------------------------------------------------------

func TestUpsertWebPathDeduplicatesPerService(t *testing.T) {
	_, r, _ := newRepo(t)
	h := mustHost(t, r, Host{IP: "10.0.0.1"})
	s80 := mustService(t, r, Service{HostID: h.ID, Port: 80, Proto: "tcp"})
	s8080 := mustService(t, r, Service{HostID: h.ID, Port: 8080, Proto: "tcp"})

	a, err := r.UpsertWebPath(bg, WebPath{ServiceID: s80.ID, Path: "/admin", StatusCode: ptr(200)})
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.UpsertWebPath(bg, WebPath{ServiceID: s80.ID, Path: "/admin", Note: "login form"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := r.UpsertWebPath(bg, WebPath{ServiceID: s8080.ID, Path: "/admin"})
	if err != nil {
		t.Fatal(err)
	}

	if a.ID != b.ID {
		t.Errorf("the same path on the same service gave ids %d and %d", a.ID, b.ID)
	}
	if c.ID == a.ID {
		t.Error("/admin on port 8080 must be separate from /admin on port 80")
	}
	if b.StatusCode == nil || *b.StatusCode != 200 {
		t.Errorf("StatusCode = %v, want 200 kept when the new scan has none", b.StatusCode)
	}
	if b.Note != "login form" {
		t.Errorf("Note = %q, want the new note", b.Note)
	}
	if c.StatusCode != nil {
		t.Errorf("StatusCode = %v, want nil when never provided", *c.StatusCode)
	}
}

func TestUpsertWebPathMergeRules(t *testing.T) {
	_, r, _ := newRepo(t)
	h := mustHost(t, r, Host{IP: "10.0.0.1"})
	s := mustService(t, r, Service{HostID: h.ID, Port: 80, Proto: "tcp"})

	if _, err := r.UpsertWebPath(bg, WebPath{ServiceID: s.ID, Path: "/x", StatusCode: ptr(403), Note: "forbidden"}); err != nil {
		t.Fatal(err)
	}
	got, err := r.UpsertWebPath(bg, WebPath{ServiceID: s.ID, Path: "/x"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Note != "forbidden" || got.StatusCode == nil || *got.StatusCode != 403 {
		t.Errorf("got note %q status %v, want both kept", got.Note, got.StatusCode)
	}
	got, err = r.UpsertWebPath(bg, WebPath{ServiceID: s.ID, Path: "/x", StatusCode: ptr(200)})
	if err != nil {
		t.Fatal(err)
	}
	if *got.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want the newer value 200", *got.StatusCode)
	}
}

func TestUpsertWebPathValidation(t *testing.T) {
	_, r, _ := newRepo(t)
	h := mustHost(t, r, Host{IP: "10.0.0.1"})
	s := mustService(t, r, Service{HostID: h.ID, Port: 80, Proto: "tcp"})

	for _, bad := range []string{"", "admin", "http://x/", "../x"} {
		if _, err := r.UpsertWebPath(bg, WebPath{ServiceID: s.ID, Path: bad}); !errors.Is(err, ErrInvalid) {
			t.Errorf("path %q err = %v, want ErrInvalid", bad, err)
		}
	}
	if _, err := r.UpsertWebPath(bg, WebPath{ServiceID: 999, Path: "/x"}); err == nil {
		t.Error("expected a foreign key error for an unknown service")
	}
}

func TestListWebPathsSortedAndScoped(t *testing.T) {
	_, r, _ := newRepo(t)
	h := mustHost(t, r, Host{IP: "10.0.0.1"})
	s1 := mustService(t, r, Service{HostID: h.ID, Port: 80, Proto: "tcp"})
	s2 := mustService(t, r, Service{HostID: h.ID, Port: 8080, Proto: "tcp"})
	for _, p := range []string{"/z", "/a", "/m"} {
		if _, err := r.UpsertWebPath(bg, WebPath{ServiceID: s1.ID, Path: p}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.UpsertWebPath(bg, WebPath{ServiceID: s2.ID, Path: "/other"}); err != nil {
		t.Fatal(err)
	}

	paths, err := r.ListWebPaths(bg, s1.ID)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range paths {
		got = append(got, p.Path)
	}
	if want := []string{"/a", "/m", "/z"}; !slices.Equal(got, want) {
		t.Errorf("paths = %v, want %v", got, want)
	}
}

// --- Engagement -------------------------------------------------------------------------

func TestEngagementLifecycle(t *testing.T) {
	s, r, clock := newRepo(t)

	if _, err := r.GetEngagement(bg); !errors.Is(err, ErrNotFound) {
		t.Fatalf("before saving: err = %v, want ErrNotFound", err)
	}

	created, err := r.SaveEngagement(bg, Engagement{Name: "box", Target: "10.0.0.1", Difficulty: "Medium"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Phase != "recon" {
		t.Errorf("Phase = %q, want the default \"recon\"", created.Phase)
	}
	if !created.CreatedAt.Equal(time.UnixMilli(1_000)) {
		t.Errorf("CreatedAt = %v, want 1000", created.CreatedAt)
	}

	*clock = time.UnixMilli(5_000)
	updated, err := r.SaveEngagement(bg, Engagement{Name: "box renamed", Target: "10.0.0.2", Phase: "web", Goal: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "box renamed" || updated.Target != "10.0.0.2" || updated.Phase != "web" || updated.Goal != "root" {
		t.Errorf("update was not applied: %+v", updated)
	}
	if updated.Difficulty != "" {
		t.Errorf("Difficulty = %q, want it replaced by the empty value (a save writes all fields)", updated.Difficulty)
	}
	if !updated.CreatedAt.Equal(time.UnixMilli(1_000)) {
		t.Errorf("CreatedAt = %v, want it unchanged at 1000", updated.CreatedAt)
	}

	got, err := r.GetEngagement(bg)
	if err != nil || got != updated {
		t.Errorf("GetEngagement = %+v, %v; want %+v", got, err, updated)
	}
	if n := count(t, s, "SELECT count(*) FROM engagement"); n != 1 {
		t.Errorf("engagement rows = %d, want exactly 1", n)
	}
}

func TestSaveEngagementRequiresAName(t *testing.T) {
	_, r, _ := newRepo(t)
	if _, err := r.SaveEngagement(bg, Engagement{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("err = %v, want ErrInvalid", err)
	}
	if _, err := r.GetEngagement(bg); !errors.Is(err, ErrNotFound) {
		t.Error("a rejected save must not create the row")
	}
}

// --- Transactions -----------------------------------------------------------------------

func hostCount(t *testing.T, r *Repo) int {
	t.Helper()
	hosts, err := r.ListHosts(bg)
	if err != nil {
		t.Fatal(err)
	}
	return len(hosts)
}

func TestInTxCommits(t *testing.T) {
	s, r, _ := newRepo(t)

	err := s.InTx(bg, func(tx *Repo) error {
		h, err := tx.UpsertHost(bg, Host{IP: "10.0.0.1"})
		if err != nil {
			return err
		}
		_, err = tx.UpsertService(bg, Service{HostID: h.ID, Port: 80, Proto: "tcp"})
		return err
	})
	if err != nil {
		t.Fatalf("InTx: %v", err)
	}
	if hostCount(t, r) != 1 {
		t.Error("the committed host is missing")
	}
}

func TestInTxRollsBackOnError(t *testing.T) {
	s, r, _ := newRepo(t)
	boom := errors.New("boom")

	err := s.InTx(bg, func(tx *Repo) error {
		h, err := tx.UpsertHost(bg, Host{IP: "10.0.0.1"})
		if err != nil {
			return err
		}
		if _, err := tx.UpsertService(bg, Service{HostID: h.ID, Port: 80, Proto: "tcp"}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the function's own error", err)
	}
	if n := hostCount(t, r); n != 0 {
		t.Errorf("%d hosts survived the rollback", n)
	}
}

func TestInTxRollsBackWhenAStatementFails(t *testing.T) {
	s, r, _ := newRepo(t)

	err := s.InTx(bg, func(tx *Repo) error {
		if _, err := tx.UpsertHost(bg, Host{IP: "10.0.0.1"}); err != nil {
			return err
		}
		_, err := tx.UpsertService(bg, Service{HostID: 999, Port: 80, Proto: "tcp"}) // foreign key violation
		return err
	})
	if err == nil {
		t.Fatal("expected the foreign key error")
	}
	if n := hostCount(t, r); n != 0 {
		t.Errorf("%d hosts survived although a later statement failed", n)
	}
}

func TestInTxRollsBackOnPanicAndRethrows(t *testing.T) {
	s, r, _ := newRepo(t)

	func() {
		defer func() {
			if p := recover(); p != "kaboom" {
				t.Errorf("recovered %v, want the original panic value", p)
			}
		}()
		_ = s.InTx(bg, func(tx *Repo) error {
			if _, err := tx.UpsertHost(bg, Host{IP: "10.0.0.1"}); err != nil {
				return err
			}
			panic("kaboom")
		})
		t.Error("InTx returned normally after a panic")
	}()

	if n := hostCount(t, r); n != 0 {
		t.Errorf("%d hosts survived the panic", n)
	}
	// The connection must be usable again: the transaction was released.
	if _, err := r.UpsertHost(bg, Host{IP: "10.0.0.2"}); err != nil {
		t.Errorf("store is unusable after a panic in InTx: %v", err)
	}
}

func TestInTxIsUsableAfterARollback(t *testing.T) {
	s, r, _ := newRepo(t)
	_ = s.InTx(bg, func(*Repo) error { return errors.New("fail") })

	if _, err := r.UpsertHost(bg, Host{IP: "10.0.0.1"}); err != nil {
		t.Fatalf("store is unusable after a rolled back transaction: %v", err)
	}
	if err := s.InTx(bg, func(tx *Repo) error {
		_, err := tx.UpsertHost(bg, Host{IP: "10.0.0.2"})
		return err
	}); err != nil {
		t.Fatalf("second transaction: %v", err)
	}
	if hostCount(t, r) != 2 {
		t.Error("expected both hosts")
	}
}

func TestInTxSeesItsOwnWritesBeforeCommit(t *testing.T) {
	s, _, _ := newRepo(t)
	err := s.InTx(bg, func(tx *Repo) error {
		if _, err := tx.UpsertHost(bg, Host{IP: "10.0.0.1"}); err != nil {
			return err
		}
		hosts, err := tx.ListHosts(bg)
		if err != nil {
			return err
		}
		if len(hosts) != 1 {
			t.Errorf("inside the transaction: %d hosts, want 1", len(hosts))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestInTxHonoursCancelledContext(t *testing.T) {
	s, _, _ := newRepo(t)
	ctx, cancel := context.WithCancel(bg)
	cancel()

	err := s.InTx(ctx, func(*Repo) error {
		t.Error("the function must not run with a cancelled context")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestUsage(t *testing.T) {
	st, _ := openTemp(t)
	r := st.Repo()
	for _, u := range []Usage{
		{Tier: "tier1", Model: "ollama/llama3.1:8b", PromptTokens: 100, CompletionTokens: 20, OK: true},
		{Tier: "tier1", Model: "ollama/llama3.1:8b", PromptTokens: 50, CompletionTokens: 0, OK: false},
		{Tier: "tier2", Model: "ollama/llama3.1:8b", PromptTokens: 1000, CompletionTokens: 300, OK: true},
		{Tier: "tier1", Model: "ollama/other:7b", PromptTokens: 5, CompletionTokens: 5, OK: true},
	} {
		if err := r.AddUsage(context.Background(), u); err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.UsageTotals(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []UsageTotal{
		{Tier: "tier1", Model: "ollama/llama3.1:8b", Calls: 2, Failed: 1, PromptTokens: 150, CompletionTokens: 20},
		{Tier: "tier1", Model: "ollama/other:7b", Calls: 1, PromptTokens: 5, CompletionTokens: 5},
		{Tier: "tier2", Model: "ollama/llama3.1:8b", Calls: 1, PromptTokens: 1000, CompletionTokens: 300},
	}
	if !slices.Equal(got, want) {
		t.Errorf("totals = %+v\nwant     %+v", got, want)
	}
	fresh, _ := openTemp(t)
	if none, err := fresh.Repo().UsageTotals(context.Background()); err != nil || len(none) != 0 {
		t.Errorf("an unused case: %v, %v", none, err)
	}
}

func TestUsageRefusesNonsense(t *testing.T) {
	st, _ := openTemp(t)
	for name, u := range map[string]Usage{
		"unknown tier":    {Tier: "tier3", Model: "m"},
		"empty tier":      {Model: "m"},
		"no model":        {Tier: "tier1"},
		"negative prompt": {Tier: "tier1", Model: "m", PromptTokens: -1},
		"negative answer": {Tier: "tier1", Model: "m", CompletionTokens: -1},
	} {
		if err := st.Repo().AddUsage(context.Background(), u); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if totals, _ := st.Repo().UsageTotals(context.Background()); len(totals) != 0 {
		t.Errorf("rejected usage was stored: %+v", totals)
	}
}

func TestPipelineRun(t *testing.T) {
	st, _ := openTemp(t)
	r := st.Repo()

	run1, err := r.SavePipelineRun(context.Background(), "ollama/llama3.1:8b", "a first summary", `[{"priority":1}]`)
	if err != nil {
		t.Fatal(err)
	}
	if run1.ID == 0 || run1.Model != "ollama/llama3.1:8b" || run1.Summary != "a first summary" || run1.Steps != `[{"priority":1}]` || run1.CreatedAt.IsZero() {
		t.Errorf("run1 = %+v", run1)
	}
	run2, err := r.SavePipelineRun(context.Background(), "ollama/llama3.1:8b", "a second summary", `[{"priority":1},{"priority":2}]`)
	if err != nil {
		t.Fatal(err)
	}
	if run2.ID == run1.ID {
		t.Error("two runs got the same id")
	}

	all, err := r.ListPipelineRuns(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].ID != run1.ID || all[1].ID != run2.ID {
		t.Errorf("all = %+v, want run1 then run2", all)
	}

	got, err := r.GetPipelineRun(context.Background(), run1.ID)
	if err != nil || got != run1 {
		t.Errorf("GetPipelineRun(%d) = %+v, %v, want %+v", run1.ID, got, err, run1)
	}
	if _, err := r.GetPipelineRun(context.Background(), 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("a missing run: err = %v", err)
	}
}

func TestPipelineRunRefusesBlankFields(t *testing.T) {
	st, _ := openTemp(t)
	r := st.Repo()
	for name, args := range map[string][3]string{
		"blank model":   {"", "s", "[]"},
		"blank summary": {"m", "", "[]"},
		"blank steps":   {"m", "s", ""},
	} {
		if _, err := r.SavePipelineRun(context.Background(), args[0], args[1], args[2]); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if all, _ := r.ListPipelineRuns(context.Background()); len(all) != 0 {
		t.Errorf("rejected input was stored: %+v", all)
	}
}
