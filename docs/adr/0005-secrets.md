# ADR-0005: Handling of secrets

Status: accepted · 2026-10-21

## Context

Kagegami handles other people's credentials (passwords, hashes, keys found during an engagement) and, from v0.2, the user's own API keys for cloud model providers. Leaking either is the main risk of a tool like this. A leak rarely happens through a design decision; it happens through a debug log line, a printed struct or a pasted config file. The protections therefore have to be mechanical, not a matter of care.

## Decision

### Where secrets live

Credentials found during an engagement are stored in plaintext in the case database (`<data>/cases/<name>/kagegami.db`), because the tool has to use them. The protection is the filesystem: the database and its `-wal`, `-shm` and backup files are mode `0600`, the case and config directories `0700`. The README recommends disk encryption. Raw command output (`raw/<id>.txt`) is stored as the tool printed it and may contain credentials too; it has the same permissions.

### A secret has a type

Every field that carries a secret has the type `secret.String`. It prints as `•••` through `fmt`, JSON, text marshaling and `slog`, cannot be converted to a string or compared with one by accident, and is never decoded from JSON. The plaintext is available only through the explicit `Reveal()` method.

`secret.String` fields must be exported: `fmt` prints unexported fields through reflection and ignores their methods. A test in the store checks this for every type it defines.

### `Reveal()` is allow-listed

`Reveal()` may be called only from files named in an allowlist in `internal/secret/reveal_test.go`. A test parses every non-test Go file of the module and fails if `Reveal` is used anywhere else, and also if an allowlisted file no longer uses it, so the list stays honest. Today the only entry is `internal/store/credential.go`, which reveals the value once, when binding it to the database insert. Showing a secret in a new place (for example an explicit `--include-secrets` flag on `kagegami map`) means adding that file to the list, deliberately and visibly in the review.

### What may reveal a secret

Only two things: writing it to the case database, and showing it to the user because the user explicitly asked (an `--include-secrets` flag). Logs, reports, error messages and everything sent to a model never contain the plaintext. Reports mask secrets by default. Masking of credentials, flags and hosts before anything is sent to a cloud provider is a separate mechanism (`internal/mask`, v0.2).

### The configuration holds no secrets

`config.yaml` never contains an API key. Cloud provider settings will name an environment variable (`api_key_env: ANTHROPIC_API_KEY`) and the key is read from the environment at run time. A config file can then be committed, pasted into an issue or sent to a friend without containing anything to leak. The format is fixed now, before cloud providers exist, so that no user ever has a real key in a config file that has to be migrated later.

### Logging has a second layer

Typing is not enough on its own: `slog.String("password", plain)` bypasses `secret.String`. The logger (M2.8) therefore also redacts the value of any attribute whose key looks like a secret (`password`, `secret`, `token`, `api_key`, ...), whatever its type.

## Known limits

- `fmt` cannot be fully controlled by a type: an unexported field holding a `secret.String`, or a verb that does not fit a pointer inside a container (such as `%s` on `[]*T`), prints the value. Both are documented in the `secret` package and covered by tests; `go vet` reports the second.
- Plaintext secrets exist in process memory and in the database file; anyone with access to the user's account can read them.
- Raw output of tools is kept as is.

## Consequences

- New code that handles a secret uses `secret.String` and never `string`; a new `Reveal()` call needs an allowlist entry.
- Tests for output paths (CLI output, logs, reports) use a canary secret and assert it never appears.
