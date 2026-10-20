package main

import (
	"os"

	"github.com/sozureke/kagegami/internal/cli"
)

// Set at build time via -ldflags "-X main.version=... -X main.commit=... -X main.date=...".
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	info := cli.BuildInfo{Version: version, Commit: commit, Date: date}
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr, info))
}
