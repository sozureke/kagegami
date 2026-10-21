# ADR-0006: How command output is captured

Status: accepted · 2026-10-21

## Context

Kagegami reacts when a command finishes in the user's shell, so it must reliably learn "command X ended with exit code N, and this is its output". The shell can report command boundaries through hooks (`preexec` and `precmd` in zsh), and tmux can hand over everything a pane prints (`pipe-pane`). The open question was how to join the two.

A spike (M3.1) tried two designs on macOS with tmux 3.8 and zsh 5.9, using a private tmux server so the user's sessions were never touched.

### Result of the spike

**A. Events on a separate channel.** The hook writes "command started" and "command ended" to a file, and the output arrives separately through `pipe-pane`. Two channels are not synchronized. In 200 runs of `seq 1 20000`, 2 runs (1%) reported the end event while the output was still on its way: 15 KB and 23 KB of the tail were missing from the pipe at that moment. Output would be cut silently.

**B. Markers inside the stream.** The hook prints an invisible OSC sequence into the terminal itself, so the markers travel through the same pty as the output. In 200 runs, all 200 commands produced a segment of exactly the same size between the start and end markers, with zero variation, and the user's screen was not disturbed.

Design B was then checked on 20 situations: exit codes (including 130 after Ctrl-C), `false`, `true; false`, pipelines, loops, quotes, Unicode and emoji, `\r` progress output, NUL and invalid UTF-8 bytes, stderr, a 1.3 MB output compared byte for byte, a 3000-character command line, a command that reads the keyboard, and a full-screen program (`less`). 19 passed; the one failure was a wrong expectation about the exact bytes of `clear` in tmux, not a design problem.

## Decision

### Markers in the stream

The shell hook prints two markers into the terminal:

```
ESC ] 7771 ; C ; kg=<nonce> ; id=<n> ; cmd=<command text> BEL     before the command runs
ESC ] 7771 ; D ; kg=<nonce> ; id=<n> ; rc=<exit code>      BEL     when it has finished
```

Kagegami reads the raw pane stream (`pipe-pane` delivers what the application wrote, before tmux interprets it) and cuts it at these markers. The bytes between a `C` and its `D` are the output of that command. Everything outside them (the prompt, the echo of the typed line, redraws) is ignored.

The correctness of this design does not depend on load. The order "output, then end marker" is guaranteed by the stream itself, not by timing. A slow or busy machine can delay a marker but cannot bring back the boundary race that broke design A, so the acceptance run on Kali is a check of the environment (bash 5, tmux version), not a worry that capture might fall apart.

### The nonce and the threat model

Every marker carries a secret nonce, a random value known only to the shell hook and to kagegami and generated before the capture starts. The parser accepts only markers with the correct nonce.

Threat addressed: output of a command is untrusted data (a banner, an HTTP response, a file from the target) and could contain bytes that look like markers. In the spike, forged markers with a wrong or missing nonce created no phantom commands, and their bytes stayed inside the output of the real command. Without the nonce, a banner could close a command early or invent a fake one.

Requirements that follow:

- One nonce per capture session is enough.
- The nonce must never appear in anything a target could read or replay: not in logs, not in reports, not in the output of a command. This rules out passing it through the environment, because `env`, `printenv` or `set` would print it into the captured output (the spike used an environment variable for convenience only). The delivery mechanism is decided in M3.3 under this constraint; the intended direction is a private file (`0600`) that the hook reads with shell built-ins.
- The nonce protects against bytes produced by the target. It does not protect against a malicious local program running with the permissions of the user's shell, which can read the shell's memory or environment; that is a deeper compromise and out of scope.

### The marker code

The private OSC number 7771 is used instead of the standard 133 (semantic prompts) so that terminals with shell integration (iTerm2, VS Code, which also uses 633, iTerm2 also 1337) do not interpret our markers and draw prompt decorations. There is no central registry of OSC codes, so 7771 cannot be proven to be free, but it is obscure enough for the risk to be low. The raw stream carries the markers; they are not shown on the screen.

### The hook uses no external processes

The hook runs on every command the user types, so its cost is the cost of the user's shell. With shell built-ins only, it takes about 20 microseconds per command. The first version, which encoded the command with `base64` and `tr`, took about 3.2 milliseconds, 160 times more. Control bytes in the command text are replaced with spaces instead of being encoded. In bash, a `DEBUG` trap fires for every simple command in a pipeline or compound command, so the bash hook must guard against printing a start marker for every stage of `a | b | c`; this is real work in M3.3, not a port of the zsh hook.

### zsh: `PROMPT_SP` is switched off during capture

Before `precmd` runs, zsh prints its own end-of-line indicator (an inverse-video `%`, a line of spaces and `\r \r`), and it ends up at the end of each command's output. With `unsetopt PROMPT_SP` in the capture session the segments are exactly the command output (`hello\r\n`, `abc`, and empty for `false`). This is a visible change of the user's shell: the `%` that marks an unterminated line disappears. It is a deliberate trade-off for clean data. The option must be restored when capture ends, so closing the panel leaves no side effect, and the README documents it.

### Full-screen programs and background jobs

- A program that switches to the alternate screen (`ESC [ ? 1049 h` appears in its segment) is recognized and skipped, marked as out of scope for the MVP: its "output" is screen redraws, not text to parse. Interactive sessions come in v0.3.
- Output of a background job (`cmd &`) lands in the segment of whichever command is running when it appears; the stream does not say who wrote it. This is a known limitation of the same kind as the interactive-session one.
- Keys typed while a command reads the keyboard are echoed into the stream and therefore appear in that command's segment.

## Not verified yet

- **bash.** Only bash 3.2 exists on the development Mac; Kali uses 5.x. The bash hook (`DEBUG` trap, `PROMPT_COMMAND`) needs bash 5 and its own spike in M3.3.
- **Load.** The spike ran on an idle Mac. Under load the markers can arrive later but, by construction, in the same order.
- **tmux versions.** Only tmux 3.8 was tried. The minimum supported version is decided in M3.2.
- **How kagegami consumes the stream** (a FIFO, a socket, or a process started by `pipe-pane`) is decided in M3.4.

## Consequences

- Capture does not need a second channel between the shell and kagegami, and the race of design A cannot occur.
- `internal/terminal` parses the stream with a state machine keyed on the nonce; output is cleaned (ANSI, `\r`) after it has been cut out.
- The hooks must be tiny, free of external processes, silent when kagegami is absent, and must not slow down the prompt.
- The acceptance checklist on Kali gains items for bash and for the restoration of `PROMPT_SP`.
