package secret_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// revealAllowlist names the only files (relative to the module root, slash separated)
// that may use secret.String's Reveal method. Adding an entry is how a new place that
// shows a plaintext secret becomes visible in a review: see ADR-0005.
var revealAllowlist = []string{
	"internal/store/credential.go", // binds the plaintext to the database insert
}

// moduleRoot returns the directory that holds go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

// findReveals returns, per file (relative to root, slash separated), the positions where
// a selector named Reveal is used. Calls and method values both count: s.Reveal() and
// f := s.Reveal. Test files, comments and strings are ignored.
func findReveals(root string) (map[string][]string, error) {
	found := map[string][]string{}
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		ast.Inspect(file, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "Reveal" {
				found[rel] = append(found[rel], fset.Position(sel.Sel.Pos()).String())
			}
			return true
		})
		return nil
	})
	return found, err
}

func TestRevealIsCalledOnlyFromAllowedFiles(t *testing.T) {
	found, err := findReveals(moduleRoot(t))
	if err != nil {
		t.Fatal(err)
	}

	var files []string
	for file := range found {
		files = append(files, file)
	}
	sort.Strings(files)

	for _, file := range files {
		if !slices.Contains(revealAllowlist, file) {
			t.Errorf("Reveal is used in %s (%v), which is not in the allowlist.\n"+
				"A plaintext secret must only be revealed where it is really needed. If this is intended, "+
				"add the file to revealAllowlist in this test and explain why in the review (ADR-0005).",
				file, found[file])
		}
	}
}

// An entry that no longer uses Reveal is stale, and a stale list would silently permit
// a future use, so every entry must still be needed.
func TestEveryAllowlistEntryIsStillUsed(t *testing.T) {
	found, err := findReveals(moduleRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range revealAllowlist {
		if len(found[file]) == 0 {
			t.Errorf("%s is in the allowlist but does not use Reveal: remove the entry", file)
		}
	}
	if slices.ContainsFunc(revealAllowlist, func(f string) bool { return strings.HasSuffix(f, "_test.go") }) {
		t.Error("test files are not scanned, so they must not be listed")
	}
}

// The scan has to catch what it is meant to catch, and ignore what it should.
func TestTheRevealScanItself(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write("a/call.go", "package a\nfunc f(s interface{ Reveal() string }) string { return s.Reveal() }\n")
	write("a/value.go", "package a\nfunc g(s interface{ Reveal() string }) func() string { return s.Reveal }\n")
	write("a/nested/deep.go", "package nested\nfunc h(x struct{ S interface{ Reveal() string } }) string { return x.S.Reveal() }\n")
	write("a/comment.go", "package a\n// s.Reveal() is mentioned only in a comment\nvar s = \"s.Reveal()\"\n")
	write("a/call_test.go", "package a\nfunc t(s interface{ Reveal() string }) string { return s.Reveal() }\n")
	write("vendor/x/x.go", "package x\nfunc f(s interface{ Reveal() string }) string { return s.Reveal() }\n")
	write("a/other.go", "package a\nfunc k(s interface{ Show() string }) string { return s.Show() }\n")

	found, err := findReveals(root)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"a/call.go", "a/value.go", "a/nested/deep.go"} {
		if len(found[want]) == 0 {
			t.Errorf("the scan missed Reveal in %s", want)
		}
	}
	for _, ignored := range []string{"a/comment.go", "a/call_test.go", "vendor/x/x.go", "a/other.go"} {
		if len(found[ignored]) != 0 {
			t.Errorf("the scan wrongly reported %s: %v", ignored, found[ignored])
		}
	}
}

func TestTheRealTreeIsActuallyScanned(t *testing.T) {
	found, err := findReveals(moduleRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(found) == 0 {
		t.Fatal("the scan found no Reveal at all: it is not looking at the module")
	}
	// The declaration of Reveal itself is a method declaration, not a selector, so it is
	// correctly not reported; the one real use must be.
	if len(found["internal/store/credential.go"]) == 0 {
		t.Error("the scan did not see the use in store/credential.go: it is not reaching the source files")
	}
}
