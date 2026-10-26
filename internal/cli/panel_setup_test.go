package cli

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/sozureke/kagegami/internal/engagement"
	"github.com/sozureke/kagegami/internal/store"
	"github.com/sozureke/kagegami/internal/tui"
)

func TestAddressesInFindsAddressesNetworksAndRangesOnly(t *testing.T) {
	tests := []struct {
		text string
		want []string
	}{
		{"get the user flag on 10.10.10.5", []string{"10.10.10.5"}},
		{"scan 10.10.10.0/24, then (10.10.20.5).", []string{"10.10.10.0/24", "10.10.20.5"}},
		{"range 10.0.0.5-10.0.0.9 please", []string{"10.0.0.5-10.0.0.9"}},
		{"10.10.10.5 and again 10.10.10.5", []string{"10.10.10.5"}},
		{"htb.example.com is the box", nil},
		{"the 0.0.0.0/0 internet", nil}, // that is no scope
		{"version 1.2 of the thing", nil},
		{"", nil},
	}
	for _, tt := range tests {
		if got := addressesIn(tt.text); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("addressesIn(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

func newSetupBackend(t *testing.T) (*panelBackend, *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir()+"/case.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &panelBackend{st: st, name: "box"}, st
}

func TestTheFirstMessageSetsTargetScopeAndGoalFromItsAddresses(t *testing.T) {
	b, st := newSetupBackend(t)
	res, err := b.Onboard(context.Background(), "get the user flag on 10.10.10.5")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Handled || !res.Started || !strings.Contains(res.Summary, "target 10.10.10.5") {
		t.Errorf("res = %+v", res)
	}
	e, err := engagement.Get(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	if e.Target != "10.10.10.5" || e.Scope != "10.10.10.5" || e.Goal != "get the user flag on 10.10.10.5" {
		t.Errorf("engagement = %+v", e)
	}

	again, err := b.Onboard(context.Background(), "what is on 10.99.99.99?") // a question now, and it must not widen the scope
	if err != nil {
		t.Fatal(err)
	}
	if again.Handled {
		t.Error("once there is a scope a message is a question")
	}
	if e, _ := engagement.Get(context.Background(), st); e.Scope != "10.10.10.5" {
		t.Errorf("a question changed the scope to %q", e.Scope)
	}
}

func TestAMessageWithNoAddressDoesNotSetAScope(t *testing.T) {
	b, st := newSetupBackend(t)
	res, err := b.Onboard(context.Background(), "scan the htb box")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Handled || res.Started || res.Summary != noTargetYet {
		t.Errorf("res = %+v", res)
	}
	if e, err := engagement.Get(context.Background(), st); err == nil && e.Scope != "" {
		t.Errorf("scope = %q", e.Scope)
	}
}

func TestConfigureSetsTheScopeFromTheTargetOnce(t *testing.T) {
	b, _ := newSetupBackend(t)
	target := "10.10.10.5"
	res, err := b.Configure(context.Background(), tui.Settings{Target: &target})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Started || !strings.Contains(res.Summary, "scope 10.10.10.5") {
		t.Errorf("first target: %+v", res)
	}
	goal := "root"
	res, err = b.Configure(context.Background(), tui.Settings{Goal: &goal})
	if err != nil {
		t.Fatal(err)
	}
	if res.Started || !strings.Contains(res.Summary, "goal: root") {
		t.Errorf("goal: %+v", res)
	}
	shown, err := b.Configure(context.Background(), tui.Settings{})
	if err != nil || shown.Started || !strings.Contains(shown.Summary, "target 10.10.10.5") {
		t.Errorf("showing: %+v, %v", shown, err)
	}
	bad := []string{"example.com"}
	if _, err := b.Configure(context.Background(), tui.Settings{Scope: &bad}); err == nil {
		t.Error("a name is not a scope")
	}
}
