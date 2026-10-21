package tmux

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

// sendTextAllowlist names the directories (relative to the module root, slash separated,
// ending in a slash) whose non-test code may call SendText. SendText types into a pane
// the user works in, while the product only reads by default; the only planned user is
// the executor of the approve-run mode. Adding an entry is a conscious, reviewed decision.
var sendTextAllowlist = []string{
	"internal/executor/",
}

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

// findSendText returns, per non-test file (relative to root, slash separated), where a
// selector named SendText is used. Calls and method values both count.
func findSendText(root string) (map[string][]string, error) {
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
			if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "SendText" {
				found[rel] = append(found[rel], fset.Position(sel.Sel.Pos()).String())
			}
			return true
		})
		return nil
	})
	return found, err
}

func allowed(file string) bool {
	return slices.ContainsFunc(sendTextAllowlist, func(prefix string) bool { return strings.HasPrefix(file, prefix) })
}

func TestSendTextIsOnlyUsedWhereItIsAllowed(t *testing.T) {
	found, err := findSendText(moduleRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for f := range found {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		if !allowed(f) {
			t.Errorf("SendText is used in %s (%v), which is not allowed to type into a pane.\n"+
				"Kagegami reads panes and does not write to them; only the executor of approve-run may. "+
				"If this is intended, add the directory to sendTextAllowlist and explain it in the review.", f, found[f])
		}
	}
}

func TestSendTextScanItself(t *testing.T) {
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
	write("a/call.go", "package a\nfunc f(x interface{ SendText() }) { x.SendText() }\n")
	write("a/value.go", "package a\nfunc g(x interface{ SendText() }) func() { return x.SendText }\n")
	write("internal/executor/run.go", "package executor\nfunc h(x interface{ SendText() }) { x.SendText() }\n")
	write("a/comment.go", "package a\n// x.SendText() only in a comment\nvar s = \"x.SendText()\"\n")
	write("a/call_test.go", "package a\nfunc t(x interface{ SendText() }) { x.SendText() }\n")
	write("vendor/v/v.go", "package v\nfunc f(x interface{ SendText() }) { x.SendText() }\n")
	write("a/other.go", "package a\nfunc k(x interface{ SendKeys() }) { x.SendKeys() }\n")

	found, err := findSendText(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"a/call.go", "a/value.go", "internal/executor/run.go"} {
		if len(found[want]) == 0 {
			t.Errorf("the scan missed SendText in %s", want)
		}
	}
	for _, ignored := range []string{"a/comment.go", "a/call_test.go", "vendor/v/v.go", "a/other.go"} {
		if len(found[ignored]) != 0 {
			t.Errorf("the scan wrongly reported %s", ignored)
		}
	}
	if !allowed("internal/executor/run.go") || allowed("internal/cli/watch.go") || allowed("internal/executorx/run.go") {
		t.Error("the allowlist matches the wrong paths")
	}
}

func TestSendTextScanReadsTheRealTree(t *testing.T) {
	// The declaration is not a selector, so it is not reported; scanning must at least
	// succeed and see a Go file that does use selectors.
	found, err := findSendText(moduleRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	_ = found
	if _, err := os.Stat(filepath.Join(moduleRoot(t), "pkg", "tmux", "client.go")); err != nil {
		t.Errorf("the module layout is not what the scan assumes: %v", err)
	}
}
