# ADR-0002: Go version and dependencies

Status: accepted · 2026-10-20

## Context

The initial stack was chosen from memory, without checking current versions. Versions below were verified against the Go module proxy on 2026-10-20.

## Decision

### Go version: 1.27

`go.mod` declares `go 1.27.3`, the latest stable patch release at the time of writing. Pinning the patch matters: the standard library of go1.27.0 had known vulnerabilities (reported by `govulncheck`) fixed in later patches. See the freshness policy below. CI reads the version from `go.mod`; contributors with an older local Go get the right toolchain through `GOTOOLCHAIN=auto`.

### Dependencies

| Purpose          | Module                                      | Version | Notes                                                             |
| ---------------- | ------------------------------------------- | ------- | ----------------------------------------------------------------- |
| TUI framework    | `charm.land/bubbletea/v2`                   | v2.1.0  | New v2 import path; starting on v2 avoids a later migration       |
| TUI styling      | `charm.land/lipgloss/v2`                    | v2.0.6  | Must match the Bubble Tea major version                           |
| TUI components   | `charm.land/bubbles/v2`                     | v2.2.1  | Same                                                              |
| TUI tests        | `github.com/charmbracelet/x/exp/teatest/v2` | pseudo  | Experimental, no tagged release; pinned to a commit, test-only    |
| CLI              | `github.com/spf13/cobra`                    | v1.10.2 |                                                                   |
| SQLite           | `modernc.org/sqlite`                        | v1.60.1 | Pure Go, keeps `CGO_ENABLED=0` builds                             |
| YAML             | `go.yaml.in/yaml/v3`                        | v3.0.5  | Maintained fork; `gopkg.in/yaml.v3` has had no release since 2022 |
| Linter (tooling) | `github.com/golangci/golangci-lint/v2`      | v2.15.0 | Installed by CI, not a module dependency                          |

The Ollama client is a small hand-written HTTP client in `pkg/llm`, not the `ollama` module: that module pulls in the whole server, and we only need `/api/chat`.

Dependencies are added to `go.mod` by the milestone that first imports them, not up front.

### Freshness policy

All versions must be the latest stable release: the Go toolchain, module dependencies, GitHub Actions and tooling such as golangci-lint.

- Versions are re-verified against the Go module proxy (`go list -m <module>@latest`) when a dependency is first added, and bumped to latest if newer.
- Dependabot (`gomod` and `github-actions`, weekly) keeps them current afterwards; update PRs are reviewed and merged promptly rather than left to accumulate.
- GitHub Actions stay pinned by commit SHA, but the SHA must belong to the latest release tag.
- Exceptions (a latest version that is broken or incompatible) are allowed only with a note in the PR and, if lasting, a follow-up ADR.
- `govulncheck` in CI backs this up: a known-vulnerable dependency fails the build.

## Consequences

- Contributors need Go 1.27+ (or auto-toolchain download).
- teatest is the one unstable dependency; if it breaks, TUI snapshot tests fall back to calling `Update`/`View` directly.
- Dependabot keeps versions current; major bumps (Charm v3, etc.) need a new ADR.
