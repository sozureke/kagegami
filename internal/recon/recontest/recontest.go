// Package recontest runs a parser over recorded output and compares the delta with a golden file.
package recontest

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sozureke/kagegami/internal/recon"
	"github.com/sozureke/kagegami/internal/terminal"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// Golden parses every testdata/*.raw (output as recorded, with its command in the .meta file
// next to it), cleans it as Kagegami does, and compares the validated delta with
// testdata/NAME.delta.json. Run with -update to write the golden files.
func Golden(t *testing.T, p recon.Parser) {
	t.Helper()
	files, err := filepath.Glob("testdata/*.raw")
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	for _, f := range files {
		base := strings.TrimSuffix(f, ".raw")
		t.Run(filepath.Base(base), func(t *testing.T) {
			raw, err := os.ReadFile(f) //nolint:gosec // a test fixture
			if err != nil {
				t.Fatal(err)
			}
			cmd := recon.Command{Line: commandOf(t, base+".meta")}
			if !p.Match(cmd) {
				t.Fatalf("%s does not match its own command %q", p.Name(), cmd.Line)
			}
			delta, err := p.Parse(context.Background(), cmd, terminal.CleanString(string(raw)))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			checked, err := delta.Validate()
			if err != nil {
				t.Fatalf("the delta is invalid: %v", err)
			}
			got, err := json.MarshalIndent(checked, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			golden := base + ".delta.json"
			if *update {
				//nolint:gosec // writes next to the fixture, in the test data directory
				if err := os.WriteFile(golden, append(got, '\n'), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden) //nolint:gosec // a test fixture
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(string(want)) != string(got) {
				t.Errorf("delta differs from %s:\n%s", golden, got)
			}
		})
	}
}

func commandOf(t *testing.T, meta string) string {
	t.Helper()
	f, err := os.Open(meta) //nolint:gosec // a test fixture
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line, ok := strings.CutPrefix(sc.Text(), "command: "); ok {
			return line
		}
	}
	t.Fatalf("%s has no command line", meta)
	return ""
}
