# ADR-0008: Shell hooks and the capture nonce

Status: accepted · 2026-10-21

## Context

ADR-0006 decided that command boundaries are marked inside the terminal stream by the user's shell, with a secret nonce in every marker. It left three things open: how the hook gets into the user's shell, how the hook learns the nonce without leaking it, and what the hook does to the shell while Kagegami is not watching. This ADR decides them for zsh. The bash hook needs its own spike (the `DEBUG` trap fires for every stage of a pipeline) and comes with its own ADR or an amendment to this one.

## Decision

### The user loads the hook, Kagegami does not inject it

The user adds one line to the shell's startup file:

```
eval "$(kagegami hook zsh)"
```

`kagegami hook <shell>` only prints the script. Kagegami does not type it into the pane (typing into a pane belongs to the executor, ADR-0007) and does not edit `.zshrc`. `doctor` and the runtime check of M3.4 report when markers do not arrive.

### The nonce is a file, read by the hook before every command

While Kagegami watches a pane, it keeps the nonce in `<run dir>/<pane number>.nonce` (for pane `%7`, `7.nonce`): 32 lowercase hex characters (128 bits from `crypto/rand`), file mode `0600`, directory mode `0700`, created exclusively and removed when the watch ends. The run directory is `$KAGEGAMI_HOME/run`, or `<data dir>/run` without `KAGEGAMI_HOME`; `kagegami hook` writes it into the script, quoted, so the hook does no path logic.

The hook reads the file with built-ins before each command and again before the prompt:

- **No file, or content that is not exactly 32 lowercase hex characters: the hook does nothing.** It prints no marker and changes no option. A pane that Kagegami does not watch costs the user one failed file test per command.
- **The nonce is never kept in a variable between commands.** It lives in a local variable of the hook function and in `REPLY`, which is cleared before the function returns. `set`, `typeset`, `export`, `env` and `print ${(k)parameters}` therefore cannot print it. A test runs all of them and searches the whole stream for the nonce.
- The nonce does not travel through the environment, arguments or the marker's `cmd` text.

The lifecycle follows from the file. Starting `watch` creates it, ending `watch` removes it, and the hook notices on the next command. If the file disappears while a command runs, no end marker is printed for that command; the parser (M3.4) closes an unfinished command when the session ends.

### Marker text

The command text in a start marker has every control character replaced by a space (a newline, ESC or BEL in it would end the marker early or start another sequence). The text is the last field of the marker and may contain `;` and `=`, so a parser splits the first fields and takes the rest as the command.

### `PROMPT_SP` follows the watch

While the nonce file is valid, the hook runs `unsetopt promptsp`, because zsh prints its end-of-line indicator before the end marker and it would end up in the command's output (ADR-0006). It remembers whether the user had the option on, and when the file disappears it sets the option back only if the user had it. The switch is made both before a command and before the prompt, so the first command after the watch started is clean as well. The user-visible effect while watching is that the `%` marking an unterminated line is gone; the README documents it.

### The hook coexists with the user's own hooks

The functions are registered so that the user's own hooks keep running, and in an order that keeps their output out of the command's output: the start marker goes last in `preexec_functions` (right before the command), the end marker goes first in `precmd_functions`. Loading the hook twice registers it once and does not restart the command counter. Outside tmux (`$TMUX_PANE` empty) it does nothing. It uses built-ins only (ADR-0006).

One case cannot be ordered: zsh runs a function named `precmd` before every entry of `precmd_functions`, so whatever such a legacy function prints lands at the end of the command's output. It is a known limit; output is cleaned of terminal escape sequences afterwards, and plain text printed by a `precmd` function is rare.

## Consequences

- One manual step for the user, in exchange for no code that edits their dotfiles or types into their terminal.
- The cost of an idle hook is a file test and, while watching, one `read` per command.
- A target cannot learn the nonce from the user's shell by dumping variables or the environment. A program running as the same local user can still read the file (ADR-0006 puts that out of scope).
- A different `KAGEGAMI_HOME` between loading the hook and starting `watch` makes the hook look in the wrong directory and stay silent; the M3.4 check ("hook loaded, no marker after the first command") reports it.
- Supporting another shell means a new script, its own tests against that real shell, and an entry in `hook.Shells()`.
