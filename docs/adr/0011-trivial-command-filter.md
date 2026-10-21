# ADR-0011: Which finished commands are not worth a model call

Status: accepted · 2026-10-21

## Context

Every finished command costs a model call and some of the user's attention. Most commands in a shell are trivia (`cd`, `ls`, `clear`), mistyped, or silent. The configuration has an ignore list (`capture.ignore`); the open question from the plan was what else to drop, in particular failures with a short message.

## Decision

`terminal.Filter` decides about each finished command, and gives one reason when it drops one. The rule is deliberately narrow: **drop a command only when it cannot tell Kagegami anything.** The reasons, in the order they are checked:

1. **Unfinished** (no end marker): neither the exit status nor the output can be trusted.
2. **Full-screen program** (it switched to the alternate screen): its output is screen redraws (ADR-0006).
3. **Exit status 126 or 127** (not executable, not found): almost always a typo.
4. **Ignored command**: every simple command of the line is on the ignore list.
5. **No output**: nothing to learn.

Everything else is passed on, including:

- **A failure with a short message.** `curl: (7) Failed to connect`, `Connection refused` and `Permission denied` are findings (a closed port, a filtered host), not noise. Only the shell's own "cannot run this" statuses are dropped.
- A command interrupted with Ctrl-C (status 130) when it printed something: a long scan stopped halfway is still data.

### The ignore list

- An entry is a command name (`ls`) or a command with leading arguments (`git status`). Matching is by words, case-sensitive, on the base name of the command, so `/bin/ls` matches `ls`.
- Variable assignments (`LANG=C ls`) and the wrappers `sudo`, `time`, `command` and `builtin` are looked through, but only without options, because an option can change what runs (`sudo -u x ls`).
- A line with several commands (`;`, `&&`, `||`, `|`, `&`, newline) is ignored only if **every** part is on the list: `cd /tmp && nmap x` is processed.
- A line the filter cannot read with certainty is **never** ignored: command and process substitution, subshells, groups, unbalanced quotes, a trailing backslash. When in doubt, process it.
- Quoting is understood (`ls "a;b"` is one command), and the word splitter is checked against a real shell in the tests.

## Consequences

- Fewer model calls on trivia, with a bias towards sending too much rather than too little. A wrong "process" costs a call; a wrong "ignore" loses information.
- The user keeps control through `capture.ignore`, with entries as precise as `git status`.
- The filter does not look at what the command printed beyond its size, so it cannot be fooled by output.
- Dropped commands are still recorded (blob and exit status); only the reasoning step is skipped.
