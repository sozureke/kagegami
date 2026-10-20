# ADR-0001: Name, license, paths

Status: accepted · 2026-10-20

## Context

Three decisions are expensive to reverse after the first public release: the project name, the license, and where config and data live on disk. They are settled before any code ships.

## Decision

### Name

The project is called **kagegami**. The earlier working name, "Kage", is retired.

- Go module: `github.com/sozureke/kagegami`
- Binary: `kagegami`
- Environment variable prefix: `KAGEGAMI_`

Availability checks (GitHub, Homebrew, apt, PyPI/npm, domain) are still pending.

### License: MIT

- Kagegami runs locally in the user's terminal and is not a network service, so the closed-SaaS-fork risk that AGPL guards against is low.
- MIT is simpler for contributors, and pentest distributions (Kali, BlackArch) package it more readily than copyleft licenses.
- Changing the license later requires the consent of every contributor, so it is fixed before the first public commit.

### Paths

Without overrides, config and data are split following the XDG Base Directory spec:

| What                        | Path                                                                |
| --------------------------- | ------------------------------------------------------------------- |
| Config                      | `$XDG_CONFIG_HOME/kagegami/`, falling back to `~/.config/kagegami/` |
| Data (SQLite, raw output)   | `$XDG_DATA_HOME/kagegami/`, falling back to `~/.local/share/kagegami/` |

Precedence: `KAGEGAMI_HOME` > `XDG_CONFIG_HOME` / `XDG_DATA_HOME` > defaults.

`KAGEGAMI_HOME` is a single root that forces config and data into one directory. This makes the whole state portable and convenient for per-engagement setups. Layout inside it: `config.yaml`, `templates/`, `data/`, `run/`.

`KAGEGAMI_CONFIG` (M2.2) points at a specific config file and takes precedence over the location derived from `KAGEGAMI_HOME`.

Permissions: directories `0700`, files `0600`.

## Consequences

- Linux users get the layout they expect from XDG-aware tools; `KAGEGAMI_HOME` covers the "everything in one folder" case.
- Two lookup roots (config, data) must be resolved in `internal/config`, and every path-using package must go through that resolver rather than hardcoding paths.
- Renaming after release would break paths, env vars, the module path and package names, which is why it is decided now.
