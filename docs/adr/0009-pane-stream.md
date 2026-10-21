# ADR-0009: How the pane stream reaches Kagegami

Status: accepted · 2026-10-21

## Context

ADR-0006 left open how Kagegami consumes the raw pane stream: a named pipe, a socket, or a process started by `pipe-pane`. ADR-0008 settled how the hook learns the nonce. This ADR decides how the stream is delivered, in what order things start and stop, and what Kagegami does when the setup does not work.

## Decision

### A named pipe in the run directory

`tmux pipe-pane` is told to run `cat > <run dir>/<pane number>.fifo`, and Kagegami reads the other end. A plain file would grow with everything the pane ever prints; a socket would need a tool that connects to it (`nc -U`) on every machine. `cat` and a named pipe exist everywhere.

- The run directory must be a real directory owned by the user with no access for group or others (mode `0700`); otherwise Kagegami refuses to start (`ErrUnsafeDir`). The nonce file lives there too.
- A pipe file or link left over from a run that was killed is removed first and never followed.
- Kagegami opens the pipe for reading **and** writing. Opening does not wait for a writer, and the stream does not end when `cat` ends, so a pane that is restarted does not end the watch by accident.

Two consequences follow, and both are handled explicitly:

- **The stream never ends on its own, so the end of the pane is detected by asking tmux** whether the pane still exists, once a second. When it is gone, the watch ends with `ErrPaneGone` and cleans up.
- **Closing the pipe does not wake a reader that waits for data.** Kagegami first writes one byte into the pipe through a second, non-blocking descriptor, and what is read after closing began is dropped.

### Start and stop order

Start: check that the pane exists and is not piped already, check the run directory, create the pipe file, run `pipe-pane`, and only then write the nonce file. Writing the nonce last means the hook cannot mark a command before the stream is being read.

Stop: remove the nonce file first (the hook stops marking), then stop the pipe, then remove the pipe file. An open command is reported as aborted. If any step fails, the steps already done are undone, and nothing is left behind. If the process is killed, the nonce file and the pipe stay; the next watch of the pane replaces them.

### A pane that already pipes elsewhere is refused

`pipe-pane` without `-o` replaces an existing pipe. Kagegami checks `#{pane_pipe}` first and refuses (`ErrPaneBusy`) rather than silently cutting a pipe that belongs to the user or to another program.

### Recording failures never stop the capture

The output of each command goes to a `Recorder` supplied by the caller (the store and the raw writer plug in there). If recording fails (disk full, cannot create the file), the command is still reported, with the error on the event, and the capture goes on. A command that prints a lot must not be disturbed by Kagegami.

### The hook may be missing

If a good deal of output (16 KiB by default) passes through a watched pane and not one marker has been seen, Kagegami reports once that the shell hook is probably not loaded in that pane. It is a warning, not an error: the output of a pane without the hook is useless to Kagegami, but the user may simply not have run a command yet.

## Consequences

- No second channel, no polling of files, no growth of a raw stream on disk.
- A pane can be watched, stopped and watched again; a watch ended by closing the pane or cancelling the context leaves the run directory empty.
- Reading what is already in the pipe when the watch starts is possible because the pipe is created before `pipe-pane` runs and held open by Kagegami.
- The tests run the whole path (private tmux, real zsh, real named pipe), including a second watch of the same pane and the kill of the pane under watch.
- Not covered: a pane whose shell is not zsh (bash comes with its own hook), and a user configuration that changes what `pipe-pane` delivers (ADR-0007 states the same limit).
