# kagegami

A pentest recon assistant that lives in a tmux pane next to your working terminal.

> **Status: pre-alpha.** Nothing works yet. The interface, config format and database schema will change without notice.

Kagegami watches the output of the commands you run (read-only), keeps a map of the target and an engagement journal, suggests next steps, and builds a work pipeline from the recon you have collected. A local model (Ollama) is the default; cloud providers are optional and only ever see masked data.

## Design principles

- **Event-driven.** A finished command in your shell triggers work, not a model call. You run commands, the agent reacts to results.
- **Lazy, two-tier model.** A cheap pass extracts state from each interesting command; an expensive pass runs only on request. When idle, the model is silent.
- **Raw output stays on disk.** The model sees compact state and slices on demand, never a 10,000-line dump.
- **Suggest by default.** Running commands on your behalf is an opt-in mode.
- **Local by default.** Cloud providers get masked credentials, flags and targets.

## Requirements

- Linux or macOS
- tmux, zsh or bash
- [Ollama](https://ollama.com) for the default local mode

## Tuning the opening animation

`kagegami watch` opens with an animated ASCII splash: the art appears in rings that spread from a
centre, pulses run over it, it glitches for a moment and dissolves. `--no-anim` (or
`KAGEGAMI_NO_ANIM=1`) skips it. The art is `internal/splash/assets/art.txt`; replace the file to
change it. To try settings, run the demo:

```sh
go run ./cmd/splash-demo                          # the defaults
go run ./cmd/splash-demo -glitch 80 -color cyan -loop
go run ./cmd/splash-demo -config splash.yaml      # settings from a file
go run ./cmd/splash-demo -art my-art.txt          # another art, without rebuilding
```

Any key stops it. A flag beats the file, and the file beats the defaults. In the file the names are
the ones in the first column (`center_x: 30`). The same names with a dash are the flags
(`-center-x 30`).

| parameter | what it does | example |
|---|---|---|
| `speed` (1.8) | time multiplier of the appearance and the pulses; higher is faster | `-speed 3` is brisk, `-speed 0.8` calm |
| `fps` (30) | frames per second; higher is smoother and costs CPU while it plays | `-fps 60` |
| `rings` (6) | rings the art appears in, from the centre out; more is a finer ripple | `-rings 12`; `-rings 1` appears in one sweep |
| `pulse` (5) | pause between two rings, in frames | `-pulse 0` appears together, `-pulse 10` spreads out |
| `fade` (12) | frames a character takes to appear; 1 is a hard cut | `-fade 4` snappy, `-fade 30` soft |
| `jitter` (2) | random spread of each character's start, in frames; makes a ring's edge ragged | `-jitter 0` clean line |
| `center_x`, `center_y` (50, 50) | centre of the rings and pulses, % of the art's size | `-center-x 0 -center-y 0` starts at the top left |
| `pulse_freq` (35) | how often a pulse starts after the art has appeared, 0-100 | `-pulse-freq 0` none |
| `pulse_speed` (40) | how fast a pulse travels | `-pulse-speed 80` |
| `pulse_width` (18) | thickness of a pulse, in tenths of a cell | `-pulse-width 40` thick ring |
| `glitch` (55) | amount of glitching, 0-100: shifted blocks and rows, noise, rebuilt patches, flicker; 0 turns it all off | `-glitch 90` wild, `-glitch 0` clean |
| `rgb_split` (50) | how often a glitching row shows red and cyan ghosts, 0-100 | `-rgb-split 0` none |
| `scanlines` (true) | darken every second row | `-scanlines=false` |
| `vignette` (true) | darken towards the edges | `-vignette=false` |
| `cell_aspect` (0.5) | width / height of a terminal cell; raise it if rings look like upright ovals | `-cell-aspect 0.45` |
| `fade_out_sec` (1.2) | seconds the art takes to dissolve | `-fade-out-sec 2` |
| `hold_sec` (2) | seconds the finished art glitches and pulses before dissolving | `-hold-sec 0.5` |
| `total_max_sec` (6) | longest the whole animation may last; the hold is cut first, then the dissolve starts early | `-total-max-sec 3` |
| `loop` (false) | play again when it ends, until a key is pressed | `-loop` |
| `color` (`#39ff14`) | hex colour, or `green`, `white`, `magenta`, `cyan` | `-color "#ff8800"`, `-color magenta` |
| `mode` (`fade`) | `fade` grows a character through denser glyphs; `decode` shows random characters until it settles | `-mode decode` |
| `seed` (0) | a non-zero seed repeats the same frames; 0 is random each time | `-seed 42` |

Colours need a terminal that shows 24 bits. If yours does not, the animation falls back to 256
colours and says so once. Inside tmux, add this to `~/.tmux.conf` and restart tmux:

```
set -as terminal-features ',*:RGB'
```

## Authorized use only

Kagegami is for security testing of systems you own or have explicit written permission to test. You are responsible for staying within scope and the law.

Many certification exams restrict or forbid AI assistance (OSCP, for example). Check the current rules of your exam before using Kagegami in it.

## Documentation

- [Architecture decisions](docs/adr/)

## License

[MIT](LICENSE)
