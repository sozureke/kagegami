package cli

import (
	"strings"
	"testing"

	"github.com/sozureke/kagegami/internal/config"
	"github.com/sozureke/kagegami/internal/setup"
)

func TestCaseNameForDir(t *testing.T) {
	for dir, want := range map[string]string{
		"/home/me/htb-box":              "htb-box",
		"/home/me/My Project (2)":       "My-Project--2-",
		"/home/me/.hidden":              "hidden",
		"/":                             "case",
		"/x/" + strings.Repeat("a", 90): strings.Repeat("a", 64),
	} {
		got := caseNameForDir(dir)
		if got != want {
			t.Errorf("caseNameForDir(%q) = %q, want %q", dir, got, want)
		}
		if err := config.ValidateCaseName(got); err != nil {
			t.Errorf("caseNameForDir(%q) = %q is not a valid case name: %v", dir, got, err)
		}
	}
}

func TestCreatedNoticeSaysOnlyWhatWasCreated(t *testing.T) {
	home := t.TempDir()
	p, err := config.NewPaths(config.Env{Getenv: func(k string) string {
		if k == "KAGEGAMI_HOME" {
			return home
		}
		return ""
	}, Home: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	first, err := setup.Init(p, setup.Options{Case: "box"})
	if err != nil {
		t.Fatal(err)
	}
	got := createdNotice(p, "box", "/work/box", first)
	if !strings.Contains(got, "created your config") || !strings.Contains(got, `created the case "box" for /work/box`) {
		t.Errorf("first run notice = %q", got)
	}
	again, err := setup.Init(p, setup.Options{Case: "box"})
	if err != nil {
		t.Fatal(err)
	}
	if got := createdNotice(p, "box", "/work/box", again); got != "" {
		t.Errorf("nothing was created, yet the notice says %q", got)
	}
}
