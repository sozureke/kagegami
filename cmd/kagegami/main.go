package main

import (
	"fmt"
	"io"
	"os"
)

// Set at build time via -ldflags "-X main.version=... -X main.commit=... -X main.date=...".
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes the CLI and returns the process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "version" {
		_, _ = fmt.Fprintf(stdout, "kagegami %s (commit %s, built %s)\n", version, commit, date)
		return 0
	}

	_, _ = fmt.Fprintln(stderr, "Kagegami — pentest assistant. Not yet implemented.")
	return 1
}
