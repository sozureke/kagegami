# ADR-0007: Supported tmux versions and the capture self-check

Status: accepted · 2026-10-21

## Context

ADR-0006 builds command capture on one property of tmux: `pipe-pane` hands over the raw bytes of a pane, including OSC markers that tmux itself does not interpret. That was observed on tmux 3.8. Other versions (Kali, Debian, Ubuntu ship different ones) were not observed, and a version number is only an indirect sign of a behavior. ADR-0006 is accepted and is not edited; this ADR adds to it.

## Decision

### Verified versions

A version is **verified** when the behavior was actually observed on it, with a source that can be checked. The list lives in `pkg/tmux` (`VerifiedVersions`) and here:

| tmux | Observed on | Evidence |
| ---- | ----------- | -------- |
| 3.8  | the development machine (macOS 26.6.2, arm64, tmux from Homebrew), 2026-10-21 | the spike behind ADR-0006 and the integration tests in `pkg/tmux`, including `TestPipePaneCarriesRawOSCMarkers` |
| 3.4  | the GitHub Actions image `ubuntu-24.04` (version 20261012.601), 2026-10-21 | CI run 39582742565 of PR #29: `tmux -V` printed `tmux 3.4`, and the `pkg/tmux` tests ran with `KAGEGAMI_REQUIRE_TMUX=1` (so none could be skipped) and passed |

The macOS runner of GitHub Actions has no tmux, so the integration tests are skipped there, visibly. Every entry is added only after CI output or a recorded run shows the tests passing on that version; the Ubuntu job prints `tmux -V` so that the table is taken from a log and not from an assumption about the runner image.

Not verified yet: tmux 3.3a (Debian 12), 3.5a (current Kali) and any other version. They may well work; the next section is what decides.

### Policy: the self-check is the gate, the version is a warning

- A tmux version that is **not verified** produces a warning ("this tmux was not verified, capture is checked at start-up"), never a refusal.
- The **capture self-check** (`tmux.VerifyCapture`) decides whether capture may start. It tests the exact property on the exact tmux that will be used, whatever its version number. If it fails, capture is not started and the reason is shown.
- The check runs at every start of `kagegami watch` and in `kagegami doctor`. It costs a fraction of a second once per long-lived session, and a cache keyed on the version would add staleness without a benefit.

### How the self-check works

1. It starts a **throwaway private tmux server**: its own socket (`-S`) in a fresh short temporary directory, no configuration file (`-f /dev/null`), and an environment without `TMUX` and `TMUX_PANE`, so it can never touch the user's sessions even when run inside tmux.
2. A pane waits for a key, a pipe is opened on it, then the key is sent and the pane prints a marker (`ESC ] 7771 ; V ; <token> BEL`) into the pipe's file. Waiting for the key guarantees the pipe is open before the marker is printed.
3. The pipe file is **polled until a deadline** (5 seconds by default) and the check passes when the exact marker is there. Output reaches the pipe a little after it is printed; reading the file once would give a false failure in about 1% of runs (the same race that sank the first design in ADR-0006).
4. A failure is **repeated once, from scratch on a new server, with twice the deadline**, before it is believed. The deadline costs time only when the check fails.
5. The server and the directory are always removed, also when the context was cancelled.

The marker token is a fresh random value (128 bits) and is **not the nonce of a capture session**: the nonce must not appear anywhere but in the markers of a capture session (ADR-0006).

An altered marker (characters changed, `ESC` or `BEL` removed, other case) fails the check, and an unchanged marker passes; the tests cover both with a tmux stand-in whose pipe alters or delays the bytes.

### Limits of what the check proves

- The server runs with `-f /dev/null`, so it checks a plain tmux and not the user's configuration. Whether options or plug-ins in a configuration can change what `pipe-pane` delivers is not known and was not checked against tmux's source. A runtime safeguard covers the gap (M3.4): if the hooks are installed and not a single marker arrives for the first command in a watched pane, a warning is shown.
- The check proves delivery of one marker on an idle machine; under load markers can arrive later, but by construction in the same order (ADR-0006).
- It says nothing about bash or zsh; the hooks are checked separately (M3.3).

### The two operations that change panes

- `Client.KillPane` closes only panes the same process created with `SplitWindow` and never the pane the program runs in.
- `Client.SendText` types into a pane the user may be working in, while the product is read-only by default. Only tests and the future executor of the approve-run mode may call it; a test fails on any other call.

## Consequences

- Distributions that ship another tmux work out of the box when the self-check passes, and are told that their version is unverified.
- The list of verified versions grows with evidence (the CI log of another image, an acceptance run on Kali), not with guesses.
- `watch` and `doctor` need the `tmux` binary and a writable temporary directory, and say so clearly when they are missing.
