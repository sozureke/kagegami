package hook

import (
	_ "embed" // the hook scripts are part of the binary
	"errors"
	"fmt"
	"strings"
)

//go:embed zsh.zsh
var zshScript string

// runDirPlaceholder is replaced by the quoted path of the run directory.
const runDirPlaceholder = "@RUN_DIR@"

// ErrUnknownShell is returned for a shell that has no hook.
var ErrUnknownShell = errors.New("no hook for this shell")

// ErrBadRunDir is returned for a run directory that cannot be put into a script safely.
var ErrBadRunDir = errors.New("invalid run directory")

// Shells lists the shells that have a hook.
func Shells() []string { return []string{"zsh"} }

// Render returns the hook script for shell with runDir, the directory that holds the nonce
// files, written into it. The script is meant to be passed to eval by the user's shell.
func Render(shell, runDir string) (string, error) {
	var script string
	switch shell {
	case "zsh":
		script = zshScript
	default:
		return "", fmt.Errorf("%w: %q (supported: %s)", ErrUnknownShell, shell, strings.Join(Shells(), ", "))
	}
	quoted, err := quote(runDir)
	if err != nil {
		return "", err
	}
	return strings.Replace(script, runDirPlaceholder, quoted, 1), nil
}

// quote makes path one word for the shell: inside single quotes nothing is special, except a
// single quote itself, which is written as '\”.
func quote(path string) (string, error) {
	if path == "" || path[0] != '/' {
		return "", fmt.Errorf("%w: %q is not an absolute path", ErrBadRunDir, path)
	}
	if strings.ContainsAny(path, "\x00\n\r") {
		return "", fmt.Errorf("%w: the path contains a control character", ErrBadRunDir)
	}
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'", nil
}
