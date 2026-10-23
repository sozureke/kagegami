package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sozureke/kagegami/internal/secret"
)

const (
	plainSecret = "hunter2-Sup3r!"
	validSHA    = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

func leaksSecret(s string) bool { return strings.Contains(s, plainSecret) }

// --- Credentials ------------------------------------------------------------------------

func TestUpsertCredentialDeduplicates(t *testing.T) {
	_, r, clock := newRepo(t)

	a, err := r.UpsertCredential(bg, Credential{Username: "root", Secret: secret.New("toor"), WorksOn: "ssh"})
	if err != nil {
		t.Fatal(err)
	}
	*clock = time.UnixMilli(2_000)
	b, err := r.UpsertCredential(bg, Credential{Username: "root", Secret: secret.New("toor")})
	if err != nil {
		t.Fatal(err)
	}

	if a.ID != b.ID {
		t.Errorf("the same credential gave ids %d and %d, want one row", a.ID, b.ID)
	}
	if b.WorksOn != "ssh" {
		t.Errorf("WorksOn = %q, want ssh kept when the repeat has none", b.WorksOn)
	}
	if !b.CreatedAt.Equal(time.UnixMilli(1_000)) {
		t.Errorf("CreatedAt = %v, want it unchanged at 1000", b.CreatedAt)
	}
	if got := b.Secret.Reveal(); got != "toor" {
		t.Errorf("Secret = %q, want toor", got)
	}

	for name, other := range map[string]Credential{
		"another secret":   {Username: "root", Secret: secret.New("other")},
		"another username": {Username: "admin", Secret: secret.New("toor")},
		"another kind":     {Username: "root", Secret: secret.New("toor"), Kind: "hash"},
	} {
		c, err := r.UpsertCredential(bg, other)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if c.ID == a.ID {
			t.Errorf("%s must be a different credential", name)
		}
	}
	if list, _ := r.ListCredentials(bg); len(list) != 4 {
		t.Errorf("%d credentials, want 4", len(list))
	}
}

func TestUpsertCredentialMergeRules(t *testing.T) {
	s, r, _ := newRepo(t)
	for _, id := range []int64{1, 2} {
		mustExec(t, s, "INSERT INTO output_blob (id, cmd, started_at) VALUES (?, 'x', 1)", id)
	}
	base := Credential{Username: "root", Secret: secret.New("toor")}

	first := base
	first.WorksOn, first.SourceBlobID = "ssh", ptr(int64(1))
	if _, err := r.UpsertCredential(bg, first); err != nil {
		t.Fatal(err)
	}

	got, err := r.UpsertCredential(bg, base)
	if err != nil {
		t.Fatal(err)
	}
	if got.WorksOn != "ssh" || got.SourceBlobID == nil || *got.SourceBlobID != 1 {
		t.Errorf("a repeat without details erased them: works_on=%q source=%v", got.WorksOn, got.SourceBlobID)
	}

	again := base
	again.WorksOn, again.SourceBlobID = "ftp", ptr(int64(2))
	got, err = r.UpsertCredential(bg, again)
	if err != nil {
		t.Fatal(err)
	}
	if got.WorksOn != "ftp" || *got.SourceBlobID != 2 {
		t.Errorf("newer details were not applied: works_on=%q source=%d", got.WorksOn, *got.SourceBlobID)
	}
}

func TestUpsertCredentialKinds(t *testing.T) {
	_, r, _ := newRepo(t)

	c, err := r.UpsertCredential(bg, Credential{Username: "u", Secret: secret.New("p")})
	if err != nil || c.Kind != "password" {
		t.Errorf("default kind = %q, %v; want password", c.Kind, err)
	}
	for _, kind := range []string{"password", "hash", "key", "token", "other"} {
		if _, err := r.UpsertCredential(bg, Credential{Username: "u", Secret: secret.New("s-" + kind), Kind: kind}); err != nil {
			t.Errorf("kind %q rejected: %v", kind, err)
		}
	}
	for _, kind := range []string{"Password", "pwd", "passwd", " password", "hash ", "secret"} {
		_, err := r.UpsertCredential(bg, Credential{Username: "u", Secret: secret.New("x"), Kind: kind})
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("kind %q err = %v, want ErrInvalid", kind, err)
		}
	}
}

func TestUpsertCredentialNeedsAUsernameOrASecret(t *testing.T) {
	_, r, _ := newRepo(t)

	if _, err := r.UpsertCredential(bg, Credential{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty credential err = %v, want ErrInvalid", err)
	}
	if _, err := r.UpsertCredential(bg, Credential{Username: "admin"}); err != nil {
		t.Errorf("a username alone is a valid finding: %v", err)
	}
	if _, err := r.UpsertCredential(bg, Credential{Secret: secret.New("deadbeef"), Kind: "hash"}); err != nil {
		t.Errorf("a secret alone is a valid finding: %v", err)
	}
}

func TestCredentialSecretRoundTrip(t *testing.T) {
	_, r, _ := newRepo(t)

	secrets := []string{plainSecret, "pässwörd ✓", `"quoted" 'single' ; DROP TABLE credential; --`, "line1\nline2", strings.Repeat("x", 10_000), " leading and trailing "}
	for _, plain := range secrets {
		c, err := r.UpsertCredential(bg, Credential{Username: "u", Secret: secret.New(plain)})
		if err != nil {
			t.Fatalf("%q: %v", plain, err)
		}
		if got := c.Secret.Reveal(); got != plain {
			t.Errorf("secret changed on the way in: %q -> %q", plain, got)
		}
	}
	list, err := r.ListCredentials(bg)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range list {
		got = append(got, c.Secret.Reveal())
	}
	if !slices.Equal(got, secrets) {
		t.Error("ListCredentials did not return the stored secrets in order")
	}
}

func TestListCredentialsOrderAndEmpty(t *testing.T) {
	_, r, _ := newRepo(t)
	if list, err := r.ListCredentials(bg); err != nil || len(list) != 0 {
		t.Fatalf("empty store: %d, %v", len(list), err)
	}
	for _, u := range []string{"zed", "amy", "bob"} {
		if _, err := r.UpsertCredential(bg, Credential{Username: u, Secret: secret.New("p")}); err != nil {
			t.Fatal(err)
		}
	}
	list, _ := r.ListCredentials(bg)
	var got []string
	for _, c := range list {
		got = append(got, c.Username)
	}
	if want := []string{"zed", "amy", "bob"}; !slices.Equal(got, want) {
		t.Errorf("order = %v, want the order they were found in %v", got, want)
	}
}

// --- The secret must stay out of every output -------------------------------------------

func TestCredentialNeverPrintsItsSecret(t *testing.T) {
	_, r, _ := newRepo(t)
	c, err := r.UpsertCredential(bg, Credential{Username: "root", Secret: secret.New(plainSecret), WorksOn: "ssh"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := r.ListCredentials(bg)
	if err != nil {
		t.Fatal(err)
	}

	values := map[string]any{
		"credential":          c,
		"pointer":             &c,
		"listed credentials":  list,
		"pointer slice":       []*Credential{&c},
		"map":                 map[string]Credential{"k": c},
		"struct around it":    struct{ C Credential }{c},
		"interface":           any(c),
		"error built from it": fmt.Errorf("could not use %v", c),
	}
	for name, v := range values {
		for _, verb := range []string{"%v", "%+v", "%#v"} {
			if out := fmt.Sprintf(verb, v); leaksSecret(out) {
				t.Errorf("%s %s printed the secret: %s", name, verb, out)
			}
		}
		if js, err := json.Marshal(v); err != nil || leaksSecret(string(js)) {
			t.Errorf("%s json: %s, %v", name, js, err)
		}
	}

	for name, h := range map[string]func(*bytes.Buffer) slog.Handler{
		"text": func(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) },
		"json": func(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) },
	} {
		var buf bytes.Buffer
		slog.New(h(&buf)).Info("credential", "cred", c, "all", list, slog.Group("g", "cred", &c))
		if leaksSecret(buf.String()) {
			t.Errorf("slog %s printed the secret: %s", name, buf.String())
		}
	}
}

func TestErrorsDoNotContainTheSecret(t *testing.T) {
	_, r, _ := newRepo(t)

	_, err := r.UpsertCredential(bg, Credential{Username: "u", Secret: secret.New(plainSecret), Kind: "bogus"})
	if err == nil {
		t.Fatal("expected an error for the bad kind")
	}
	if leaksSecret(err.Error()) {
		t.Errorf("validation error contains the secret: %v", err)
	}
}

// The secret is stored in the database as is (a local file with restricted permissions,
// see the storage ADR); this test records that decision so a change is a conscious one.
func TestSecretIsStoredAsPlaintext(t *testing.T) {
	s, r, _ := newRepo(t)
	if _, err := r.UpsertCredential(bg, Credential{Username: "u", Secret: secret.New(plainSecret)}); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := s.db.QueryRowContext(bg, "SELECT secret FROM credential").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != plainSecret {
		t.Errorf("stored secret = %q, want the plaintext", stored)
	}
}

// fmt prints unexported fields through reflection and ignores their methods, so a
// secret.String held in an unexported field would leak. Check every type in the package.
func unexportedSecretFields(t reflect.Type, path string, seen map[reflect.Type]bool) []string {
	secretType := reflect.TypeOf(secret.String{})
	if seen[t] {
		return nil
	}
	seen[t] = true

	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array:
		return unexportedSecretFields(t.Elem(), path, seen)
	case reflect.Map:
		return append(unexportedSecretFields(t.Key(), path+"[key]", seen), unexportedSecretFields(t.Elem(), path+"[value]", seen)...)
	case reflect.Struct:
		if t == secretType {
			return nil
		}
		var found []string
		for i := range t.NumField() {
			f := t.Field(i)
			fieldPath := path + "." + f.Name
			if f.Type == secretType && !f.IsExported() {
				found = append(found, fieldPath)
			}
			found = append(found, unexportedSecretFields(f.Type, fieldPath, seen)...)
		}
		return found
	}
	return nil
}

func TestNoStoreTypeHoldsASecretInAnUnexportedField(t *testing.T) {
	for _, v := range []any{
		Host{}, Service{}, WebPath{}, Engagement{}, Credential{}, Finding{}, JournalEntry{}, OutputBlob{},
	} {
		typ := reflect.TypeOf(v)
		if found := unexportedSecretFields(typ, typ.Name(), map[reflect.Type]bool{}); len(found) > 0 {
			t.Errorf("%v: secret.String in an unexported field would be printed in plaintext by fmt: keep it exported", found)
		}
	}
	// And the secret is where we expect it.
	if f, ok := reflect.TypeOf(Credential{}).FieldByName("Secret"); !ok || f.Type != reflect.TypeOf(secret.String{}) {
		t.Error("Credential.Secret must be a secret.String")
	}
}

// The check above must itself catch the mistake it is meant to prevent.
func TestUnexportedSecretCheckerWorks(t *testing.T) {
	type bad struct {
		Name   string
		secret secret.String
	}
	type nested struct{ Inner []*bad }
	type good struct{ Secret secret.String }
	_ = bad{}.secret // the field is only ever read through reflection; keep the linter satisfied

	for name, typ := range map[string]reflect.Type{"direct": reflect.TypeOf(bad{}), "nested": reflect.TypeOf(nested{})} {
		if len(unexportedSecretFields(typ, name, map[reflect.Type]bool{})) == 0 {
			t.Errorf("%s: the checker missed an unexported secret field", name)
		}
	}
	if found := unexportedSecretFields(reflect.TypeOf(good{}), "good", map[reflect.Type]bool{}); len(found) != 0 {
		t.Errorf("the checker flagged an exported field: %v", found)
	}
}

func TestCredentialSchemaConstraints(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{"unknown kind", "INSERT INTO credential (username, secret, kind, created_at) VALUES ('u', 's', 'weird', 1)"},
		{"no username and no secret", "INSERT INTO credential (username, secret, created_at) VALUES ('', '', 1)"},
		{"duplicate credential", "INSERT INTO credential (username, secret, kind, created_at) VALUES ('root', 'toor', 'password', 1)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := openTemp(t)
			mustExec(t, s, "INSERT INTO credential (username, secret, kind, created_at) VALUES ('root', 'toor', 'password', 1)")
			if _, err := s.db.ExecContext(bg, tt.query); err == nil {
				t.Errorf("expected a constraint error for: %s", tt.query)
			}
		})
	}
}

// --- Findings ---------------------------------------------------------------------------

func TestAddFinding(t *testing.T) {
	s, r, _ := newRepo(t)
	mustExec(t, s, "INSERT INTO output_blob (id, cmd, started_at) VALUES (1, 'ffuf', 1)")

	f, err := r.AddFinding(bg, Finding{Severity: "high", Title: "  SQL injection  ", Description: "  keep my spacing\n", SourceBlobID: ptr(int64(1))})
	if err != nil {
		t.Fatal(err)
	}
	if f.ID == 0 || f.Severity != "high" {
		t.Errorf("unexpected finding %+v", f)
	}
	if f.Title != "SQL injection" {
		t.Errorf("Title = %q, want it trimmed", f.Title)
	}
	if f.Description != "  keep my spacing\n" {
		t.Errorf("Description = %q, want it stored exactly as given", f.Description)
	}
	if f.SourceBlobID == nil || *f.SourceBlobID != 1 {
		t.Errorf("SourceBlobID = %v, want 1", f.SourceBlobID)
	}
	if !f.CreatedAt.Equal(time.UnixMilli(1_000)) {
		t.Errorf("CreatedAt = %v, want the clock time", f.CreatedAt)
	}
}

func TestAddFindingValidation(t *testing.T) {
	_, r, _ := newRepo(t)

	for _, sev := range []string{"info", "low", "medium", "high", "critical"} {
		if _, err := r.AddFinding(bg, Finding{Severity: sev, Title: "t"}); err != nil {
			t.Errorf("severity %q rejected: %v", sev, err)
		}
	}
	for _, sev := range []string{"", "urgent", "High", "CRITICAL", "informational", " low"} {
		if _, err := r.AddFinding(bg, Finding{Severity: sev, Title: "t"}); !errors.Is(err, ErrInvalid) {
			t.Errorf("severity %q err = %v, want ErrInvalid", sev, err)
		}
	}
	for _, title := range []string{"", " ", "\t\n"} {
		if _, err := r.AddFinding(bg, Finding{Severity: "low", Title: title}); !errors.Is(err, ErrInvalid) {
			t.Errorf("title %q err = %v, want ErrInvalid", title, err)
		}
	}
	list, _ := r.ListFindings(bg)
	if len(list) != 5 {
		t.Errorf("%d findings stored, want only the 5 valid ones", len(list))
	}
}

func TestFindingsAreRecordsNotState(t *testing.T) {
	_, r, _ := newRepo(t)
	in := Finding{Severity: "medium", Title: "Outdated nginx"}

	a, _ := r.AddFinding(bg, in)
	b, _ := r.AddFinding(bg, in)
	if a.ID == b.ID {
		t.Error("adding the same finding twice must create two records")
	}
	list, err := r.ListFindings(bg)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListFindings = %d, %v; want 2", len(list), err)
	}
	if list[0].ID != a.ID || list[1].ID != b.ID {
		t.Error("findings are not in the order they were recorded")
	}
}

func TestListFindingsEmpty(t *testing.T) {
	_, r, _ := newRepo(t)
	if list, err := r.ListFindings(bg); err != nil || len(list) != 0 {
		t.Errorf("empty store: %d, %v", len(list), err)
	}
}

// --- Journal ----------------------------------------------------------------------------

func TestAddJournal(t *testing.T) {
	_, r, clock := newRepo(t)

	*clock = time.UnixMilli(5_000)
	e, err := r.AddJournal(bg, JournalEntry{Author: "human", Text: "  tried ssh with root\n"})
	if err != nil {
		t.Fatal(err)
	}
	if e.ID == 0 || e.Author != "human" {
		t.Errorf("unexpected entry %+v", e)
	}
	if e.Text != "  tried ssh with root\n" {
		t.Errorf("Text = %q, want it stored exactly as written", e.Text)
	}
	if !e.Time.Equal(time.UnixMilli(5_000)) {
		t.Errorf("Time = %v, want the clock time (the caller cannot choose it)", e.Time)
	}
	if ai, err := r.AddJournal(bg, JournalEntry{Author: "ai", Text: "port 22 is open"}); err != nil || ai.Author != "ai" {
		t.Errorf("ai entry: %+v, %v", ai, err)
	}
}

func TestAddJournalIgnoresACallerSuppliedTime(t *testing.T) {
	_, r, _ := newRepo(t)
	e, err := r.AddJournal(bg, JournalEntry{Author: "human", Text: "x", Time: time.UnixMilli(999_999)})
	if err != nil {
		t.Fatal(err)
	}
	if !e.Time.Equal(time.UnixMilli(1_000)) {
		t.Errorf("Time = %v, want the store clock, not the caller's value", e.Time)
	}
}

func TestAddJournalValidation(t *testing.T) {
	_, r, _ := newRepo(t)

	for _, author := range []string{"", "bot", "Human", "AI", "system"} {
		if _, err := r.AddJournal(bg, JournalEntry{Author: author, Text: "x"}); !errors.Is(err, ErrInvalid) {
			t.Errorf("author %q err = %v, want ErrInvalid", author, err)
		}
	}
	for _, text := range []string{"", " ", "\n\t "} {
		if _, err := r.AddJournal(bg, JournalEntry{Author: "human", Text: text}); !errors.Is(err, ErrInvalid) {
			t.Errorf("text %q err = %v, want ErrInvalid", text, err)
		}
	}
	if list, _ := r.ListJournal(bg); len(list) != 0 {
		t.Errorf("rejected input still wrote %d entries", len(list))
	}
}

func TestListJournalOrderedByTimeThenID(t *testing.T) {
	_, r, clock := newRepo(t)
	if list, err := r.ListJournal(bg); err != nil || len(list) != 0 {
		t.Fatalf("empty store: %d, %v", len(list), err)
	}

	add := func(ms int64, text string) {
		*clock = time.UnixMilli(ms)
		if _, err := r.AddJournal(bg, JournalEntry{Author: "human", Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	add(3_000, "third")
	add(1_000, "first")
	add(1_000, "second") // same millisecond: insertion order decides
	add(2_000, "between")

	list, err := r.ListJournal(bg)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range list {
		got = append(got, e.Text)
	}
	if want := []string{"first", "second", "between", "third"}; !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestJournalSourceBlob(t *testing.T) {
	s, r, _ := newRepo(t)
	mustExec(t, s, "INSERT INTO output_blob (id, cmd, started_at) VALUES (1, 'nmap', 1)")

	e, err := r.AddJournal(bg, JournalEntry{Author: "ai", Text: "found it", SourceBlobID: ptr(int64(1))})
	if err != nil || e.SourceBlobID == nil || *e.SourceBlobID != 1 {
		t.Errorf("source blob = %v, %v; want 1", e.SourceBlobID, err)
	}
	if _, err := r.AddJournal(bg, JournalEntry{Author: "ai", Text: "x", SourceBlobID: ptr(int64(99))}); err == nil {
		t.Error("expected a foreign key error for an unknown blob")
	}
}

// --- Output blobs -----------------------------------------------------------------------

func TestBlobLifecycle(t *testing.T) {
	_, r, clock := newRepo(t)

	b, err := r.CreateBlob(bg, "nmap -sV 10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if b.ID == 0 || b.Cmd != "nmap -sV 10.0.0.1" {
		t.Errorf("unexpected blob %+v", b)
	}
	if !b.StartedAt.Equal(time.UnixMilli(1_000)) {
		t.Errorf("StartedAt = %v, want the clock time", b.StartedAt)
	}
	if b.ExitCode != nil || b.FinishedAt != nil || b.Size != 0 || b.SHA256 != "" {
		t.Errorf("a running command must have no exit code, finish time, size or hash: %+v", b)
	}

	*clock = time.UnixMilli(4_000)
	done, err := r.FinishBlob(bg, b.ID, 2, 12_345, validSHA)
	if err != nil {
		t.Fatal(err)
	}
	if done.ID != b.ID || done.ExitCode == nil || *done.ExitCode != 2 {
		t.Errorf("exit code = %v, want 2", done.ExitCode)
	}
	if done.Size != 12_345 || done.SHA256 != validSHA {
		t.Errorf("size/sha = %d/%s", done.Size, done.SHA256)
	}
	if done.FinishedAt == nil || !done.FinishedAt.Equal(time.UnixMilli(4_000)) {
		t.Errorf("FinishedAt = %v, want 4000", done.FinishedAt)
	}
	if !done.StartedAt.Equal(time.UnixMilli(1_000)) {
		t.Errorf("StartedAt = %v, want it unchanged", done.StartedAt)
	}

	got, err := r.GetBlob(bg, b.ID)
	if err != nil || !reflect.DeepEqual(got, done) {
		t.Errorf("GetBlob = %+v, %v; want %+v", got, err, done)
	}
}

func TestFinishBlobExitCodes(t *testing.T) {
	_, r, _ := newRepo(t)
	for _, code := range []int{0, 1, 127, 130, -1, 255} {
		b, _ := r.CreateBlob(bg, "cmd")
		got, err := r.FinishBlob(bg, b.ID, code, 0, validSHA)
		if err != nil || got.ExitCode == nil || *got.ExitCode != code {
			t.Errorf("exit code %d: stored %v, err %v", code, got.ExitCode, err)
		}
	}
}

func TestFinishBlobOnlyOnce(t *testing.T) {
	_, r, _ := newRepo(t)
	b, _ := r.CreateBlob(bg, "cmd")
	if _, err := r.FinishBlob(bg, b.ID, 0, 10, validSHA); err != nil {
		t.Fatal(err)
	}

	_, err := r.FinishBlob(bg, b.ID, 1, 99, validSHA)
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "already finished") {
		t.Fatalf("second finish err = %v, want ErrInvalid (already finished)", err)
	}
	got, _ := r.GetBlob(bg, b.ID)
	if *got.ExitCode != 0 || got.Size != 10 {
		t.Errorf("the first result was overwritten: exit %d size %d", *got.ExitCode, got.Size)
	}
}

func TestFinishBlobMissing(t *testing.T) {
	_, r, _ := newRepo(t)
	if _, err := r.FinishBlob(bg, 999, 0, 1, validSHA); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound (not ErrInvalid: the input was fine)", err)
	}
	if _, err := r.GetBlob(bg, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetBlob err = %v, want ErrNotFound", err)
	}
}

func TestFinishBlobValidation(t *testing.T) {
	_, r, _ := newRepo(t)
	b, _ := r.CreateBlob(bg, "cmd")

	tests := []struct {
		name string
		size int64
		sha  string
	}{
		{"negative size", -1, validSHA},
		{"empty hash", 1, ""},
		{"short hash", 1, validSHA[:63]},
		{"long hash", 1, validSHA + "0"},
		{"upper case hash", 1, strings.ToUpper(validSHA)},
		{"non-hex hash", 1, strings.Repeat("g", 64)},
		{"hash with a space", 1, validSHA[:63] + " "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := r.FinishBlob(bg, b.ID, 0, tt.size, tt.sha); !errors.Is(err, ErrInvalid) {
				t.Errorf("err = %v, want ErrInvalid", err)
			}
			got, _ := r.GetBlob(bg, b.ID)
			if got.FinishedAt != nil {
				t.Error("a rejected FinishBlob must leave the blob running")
			}
		})
	}

	if _, err := r.FinishBlob(bg, b.ID, 0, 0, validSHA); err != nil {
		t.Errorf("an empty output (size 0) is valid: %v", err)
	}
}

func TestCreateBlobValidation(t *testing.T) {
	_, r, _ := newRepo(t)
	for _, cmd := range []string{"", " ", "\n\t"} {
		if _, err := r.CreateBlob(bg, cmd); !errors.Is(err, ErrInvalid) {
			t.Errorf("command %q err = %v, want ErrInvalid", cmd, err)
		}
	}
}

func TestBlobsAreIndependent(t *testing.T) {
	_, r, _ := newRepo(t)
	a, _ := r.CreateBlob(bg, "first")
	b, _ := r.CreateBlob(bg, "second")
	if a.ID == b.ID || b.ID <= a.ID {
		t.Errorf("ids %d and %d: want increasing, distinct ids", a.ID, b.ID)
	}
	if _, err := r.FinishBlob(bg, a.ID, 0, 1, validSHA); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.GetBlob(bg, b.ID); got.FinishedAt != nil {
		t.Error("finishing one blob finished another")
	}
}

// --- Transactions across the new repositories -------------------------------------------

func TestInTxAppliesEvidenceAllOrNothing(t *testing.T) {
	s, r, _ := newRepo(t)

	apply := func(tx *Repo, fail bool) error {
		b, err := tx.CreateBlob(bg, "nmap -sV 10.0.0.1")
		if err != nil {
			return err
		}
		h, err := tx.UpsertHost(bg, Host{IP: "10.0.0.1", SourceBlobID: &b.ID})
		if err != nil {
			return err
		}
		if _, err := tx.UpsertService(bg, Service{HostID: h.ID, Port: 22, Proto: "tcp", SourceBlobID: &b.ID}); err != nil {
			return err
		}
		if _, err := tx.UpsertCredential(bg, Credential{Username: "root", Secret: secret.New("toor"), SourceBlobID: &b.ID}); err != nil {
			return err
		}
		if _, err := tx.AddFinding(bg, Finding{Severity: "low", Title: "ssh exposed", SourceBlobID: &b.ID}); err != nil {
			return err
		}
		if _, err := tx.AddJournal(bg, JournalEntry{Author: "ai", Text: "scanned", SourceBlobID: &b.ID}); err != nil {
			return err
		}
		if _, err := tx.FinishBlob(bg, b.ID, 0, 10, validSHA); err != nil {
			return err
		}
		if fail {
			return errors.New("boom")
		}
		return nil
	}

	total := func() int {
		n := 0
		for _, table := range []string{"output_blob", "host", "service", "credential", "finding", "journal_entry"} {
			n += count(t, s, "SELECT count(*) FROM "+table)
		}
		return n
	}

	if err := s.InTx(bg, func(tx *Repo) error { return apply(tx, true) }); err == nil {
		t.Fatal("expected the transaction to fail")
	}
	if n := total(); n != 0 {
		t.Errorf("%d rows survived a rolled back transaction", n)
	}

	if err := s.InTx(bg, func(tx *Repo) error { return apply(tx, false) }); err != nil {
		t.Fatal(err)
	}
	if n := total(); n != 6 {
		t.Errorf("%d rows after a committed transaction, want 6 (one in each table)", n)
	}
	if hosts, _ := r.ListHosts(bg); len(hosts) != 1 || hosts[0].SourceBlobID == nil {
		t.Errorf("host did not keep its evidence link: %+v", hosts)
	}
}

func TestListRecentBlobsGivesTheLatestFinishedOnesOldestFirst(t *testing.T) {
	_, r, _ := newRepo(t)
	var ids []int64
	for i, cmd := range []string{"nmap a", "ffuf b", "curl c", "dig d"} {
		b, err := r.CreateBlob(bg, cmd)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.FinishBlob(bg, b.ID, i, int64(i), strings.Repeat("a", 64)); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, b.ID)
	}
	if _, err := r.CreateBlob(bg, "still running"); err != nil {
		t.Fatal(err)
	}

	got, err := r.ListRecentBlobs(bg, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Cmd != "ffuf b" || got[1].Cmd != "curl c" || got[2].Cmd != "dig d" || got[2].ID != ids[3] {
		t.Errorf("recent = %+v: the last three finished commands, oldest first, and not the one still running", got)
	}
	if all, _ := r.ListRecentBlobs(bg, 100); len(all) != 4 {
		t.Errorf("%d commands, want 4", len(all))
	}
	if _, err := r.ListRecentBlobs(bg, 0); err == nil {
		t.Error("a count of 0 must be refused")
	}
}
