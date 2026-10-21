package hook

import (
	"errors"
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	got, err := Render("zsh", "/home/u/.local/share/kagegami/run")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, runDirPlaceholder) {
		t.Error("the placeholder was left in the script")
	}
	if !strings.Contains(got, "__kg_dir='/home/u/.local/share/kagegami/run' ") {
		t.Errorf("the run directory is not in the script, quoted:\n%s", got[:400])
	}
}

func TestRenderRejects(t *testing.T) {
	t.Run("unknown shell", func(t *testing.T) {
		for _, shell := range []string{"", "fish", "ZSH", "zsh ", "bash"} {
			_, err := Render(shell, "/run")
			if !errors.Is(err, ErrUnknownShell) {
				t.Errorf("Render(%q) error = %v, want ErrUnknownShell", shell, err)
			}
		}
	})
	t.Run("bad run directory", func(t *testing.T) {
		for _, dir := range []string{"", "relative/run", "./run", "/a\nb", "/a\rb", "/a\x00b"} {
			_, err := Render("zsh", dir)
			if !errors.Is(err, ErrBadRunDir) {
				t.Errorf("Render(zsh, %q) error = %v, want ErrBadRunDir", dir, err)
			}
		}
	})
}

func TestQuote(t *testing.T) {
	tests := map[string]string{
		"/a":     `'/a'`,
		"/a b":   `'/a b'`,
		"/it's":  `'/it'\''s'`,
		"/$x`y`": "'/$x`y`'",
		`/a\b`:   `'/a\b'`,
		"/''":    `'/'\'''\'''`,
	}
	for in, want := range tests {
		got, err := quote(in)
		if err != nil || got != want {
			t.Errorf("quote(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestShellsAreRenderable(t *testing.T) {
	for _, shell := range Shells() {
		if _, err := Render(shell, "/run"); err != nil {
			t.Errorf("%s is listed as supported but does not render: %v", shell, err)
		}
	}
}
