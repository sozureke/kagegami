package engagement

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sozureke/kagegami/internal/store"
)

var bg = context.Background()

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(bg, filepath.Join(t.TempDir(), "case.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func sp(s string) *string { return &s }

func TestFirstUpdateCreatesTheEngagement(t *testing.T) {
	st := openStore(t)
	res, err := Update(bg, st, "htb-box", Changes{Target: sp("10.10.10.5"), Goal: sp("user and root flag")})
	if err != nil {
		t.Fatal(err)
	}
	e := res.Engagement
	if e.Name != "htb-box" || e.Target != "10.10.10.5" || e.Goal != "user and root flag" || e.Phase != "recon" {
		t.Errorf("engagement = %+v", e)
	}
	if !res.ScopeFromTarget || e.Scope != "10.10.10.5" {
		t.Errorf("the scope should be the target: %+v", res)
	}
	got, err := Get(bg, st)
	if err != nil || got != e {
		t.Errorf("Get = %+v, %v", got, err)
	}
	if _, err := Get(bg, openStore(t)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a case with nothing set: %v", err)
	}
}

func TestWhatIsNotGivenStays(t *testing.T) {
	st := openStore(t)
	if _, err := Update(bg, st, "c", Changes{Target: sp("10.0.0.5"), Goal: sp("g"), ROE: sp("no DoS"), Phase: sp("web")}); err != nil {
		t.Fatal(err)
	}
	res, err := Update(bg, st, "c", Changes{Goal: sp("new goal")})
	if err != nil {
		t.Fatal(err)
	}
	e := res.Engagement
	if e.Target != "10.0.0.5" || e.Goal != "new goal" || e.ROE != "no DoS" || e.Phase != "web" || e.Scope != "10.0.0.5" || res.ScopeFromTarget {
		t.Errorf("engagement = %+v, from target %v", e, res.ScopeFromTarget)
	}
}

func TestScopeIsReplacedAndAddedTo(t *testing.T) {
	st := openStore(t)
	two := []string{"10.0.0.0/24", "10.0.1.5-10.0.1.9"}
	res, err := Update(bg, st, "c", Changes{Target: sp("10.0.0.5"), Scope: &two})
	if err != nil || res.Engagement.Scope != "10.0.0.0/24, 10.0.1.5-10.0.1.9" || res.ScopeFromTarget {
		t.Fatalf("%+v, %v", res, err)
	}
	res, err = Update(bg, st, "c", Changes{AddScope: []string{"192.168.1.50", "10.0.0.0/24"}})
	if err != nil || res.Engagement.Scope != "10.0.0.0/24, 10.0.1.5-10.0.1.9, 192.168.1.50" {
		t.Fatalf("add: %+v, %v", res, err)
	}
	only := []string{"172.16.0.1"}
	res, _ = Update(bg, st, "c", Changes{Scope: &only})
	if res.Engagement.Scope != "172.16.0.1" {
		t.Errorf("replace: %q", res.Engagement.Scope)
	}
	// emptying the scope falls back to the target, which is an address
	none := []string{}
	res, _ = Update(bg, st, "c", Changes{Scope: &none})
	if res.Engagement.Scope != "10.0.0.5" || !res.ScopeFromTarget {
		t.Errorf("emptied: %+v", res)
	}
}

func TestATargetThatIsAName(t *testing.T) {
	st := openStore(t)
	res, err := Update(bg, st, "c", Changes{Target: sp("box.htb"), Goal: sp("g")})
	if err != nil || res.Engagement.Scope != "" || res.ScopeFromTarget {
		t.Errorf("a name is no scope: %+v, %v", res, err)
	}
	if got := Scope(res.Engagement); !got.IsEmpty() {
		t.Error("the scope should be empty")
	}
	res, _ = Update(bg, st, "c", Changes{AddScope: []string{"10.10.10.5"}})
	if !Scope(res.Engagement).Contains("10.10.10.5") || Scope(res.Engagement).Contains("10.10.10.6") {
		t.Errorf("scope = %q", res.Engagement.Scope)
	}
}

func TestInvalidInputChangesNothing(t *testing.T) {
	st := openStore(t)
	if _, err := Update(bg, st, "c", Changes{Target: sp("10.0.0.5"), Goal: sp("keep")}); err != nil {
		t.Fatal(err)
	}
	bad := map[string]Changes{
		"empty target":                {Target: sp("  ")},
		"a long target":               {Target: sp(strings.Repeat("x", 300))},
		"a control char":              {Goal: sp("a\x1b[31mb")},
		"a newline in target":         {Target: sp("a\nb")},
		"a long goal":                 {Goal: sp(strings.Repeat("x", 600))},
		"a long roe":                  {ROE: sp(strings.Repeat("x", 5000))},
		"a bad phase":                 {Phase: sp("Web Phase")},
		"an empty phase":              {Phase: sp("")},
		"a bad scope":                 {Scope: &[]string{"nope"}},
		"everything":                  {AddScope: []string{"0.0.0.0/0"}},
		"a good goal and a bad scope": {Goal: sp("spoiled"), AddScope: []string{"nope"}},
	}
	for name, c := range bad {
		if _, err := Update(bg, st, "c", c); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	got, _ := Get(bg, st)
	if got.Goal != "keep" || got.Target != "10.0.0.5" || got.Scope != "10.0.0.5" {
		t.Errorf("rejected input changed the engagement: %+v", got)
	}
	// a goal may have lines
	if res, err := Update(bg, st, "c", Changes{Goal: sp("first\nsecond")}); err != nil || res.Engagement.Goal != "first\nsecond" {
		t.Errorf("%+v, %v", res, err)
	}
}

func TestAScopeStoredInABadFormIsNeverTrusted(t *testing.T) {
	st := openStore(t)
	if _, err := st.Repo().SaveEngagement(bg, store.Engagement{Name: "c", Scope: "garbage ; 10.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	e, _ := Get(bg, st)
	if !Scope(e).IsEmpty() {
		t.Error("a scope that cannot be read must be empty, not partly used")
	}
	res, err := Update(bg, st, "c", Changes{Goal: sp("g")})
	if err != nil || res.Engagement.Scope != "" {
		t.Errorf("%+v, %v", res, err)
	}
}
