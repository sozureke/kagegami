<p align="center"><img src="resources/logo.gif" alt="Kagegami" width="420"></p>

<h1 align="center">KAGEGAMI</h1>

A pentest recon assistant that lives in a tmux pane next to your working terminal.

---

Kagegami watches the output of the commands you run (read-only), keeps a map of the target and an engagement journal, suggests next steps, and builds a work pipeline from the recon you've collected. A local model (Ollama) is the default; cloud providers are optional and only ever see masked data.

## Design principles

- **Event-driven.** A finished command in your shell triggers work, not a model call. You run commands, the agent reacts to results.
- **Lazy, two-tier model.** A cheap pass extracts state from each interesting command; an expensive pass runs only on request. When idle, the model is silent.
- **Raw output stays on disk.** The model sees compact state and slices on demand, never a 10,000-line dump.
- **Suggest by default.** Running commands on your behalf is an opt-in mode.
- **Local by default.** Cloud providers get masked credentials, flags and targets.

## Requirements

- Linux or macOS
- tmux and zsh (bash support is planned)
- [Ollama](https://ollama.com) for the default local mode

## Authorized use only

Kagegami is for security testing of systems you own or have explicit written permission to test. You are responsible for staying within scope and the law.

Many certification exams restrict or forbid AI assistance (OSCP, for example). Check the current rules of your exam before using Kagegami in it.

## Documentation

Architecture decisions (ADRs) record what was decided and why. They are in [docs/adr](docs/adr/).

| ADR                                                                              | Decision                                                                                                           |
| -------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------ |
| [0001 Name, license, paths](docs/adr/0001-name-license-paths.md)                 | The name kagegami, the MIT license, and where config and data live (XDG directories, `KAGEGAMI_HOME`)              |
| [0002 Go version and dependencies](docs/adr/0002-dependencies.md)                | Go 1.27, Charm v2 for the panel, cobra, pure-Go SQLite, and how versions are pinned                                |
| [0003 Release process](docs/adr/0003-release-process.md)                         | The maintainer tags releases by hand; building, signing and publishing are automated                               |
| [0004 Per-case storage layout](docs/adr/0004-storage-layout.md)                  | One directory and one SQLite database per engagement, with raw command output next to it                           |
| [0005 Handling of secrets](docs/adr/0005-secrets.md)                             | Secrets have their own type that prints as `•••`, and only allow-listed code may reveal one                        |
| [0006 How command output is captured](docs/adr/0006-command-capture.md)          | Shell hooks print markers with a secret nonce into the pane stream, and Kagegami cuts the output at them           |
| [0007 Supported tmux versions](docs/adr/0007-tmux-versions-and-capture-check.md) | Versions are verified only when observed; a self-check at start, not the version number, decides                   |
| [0008 Shell hooks and the capture nonce](docs/adr/0008-shell-hooks.md)           | You load the hook yourself with one line; it reads the nonce from a private file, never from the environment       |
| [0009 How the pane stream reaches Kagegami](docs/adr/0009-pane-stream.md)        | A named pipe in a private run directory, with a set order for starting and stopping                                |
| [0010 Cloud models and refusals](docs/adr/0010-cloud-models-and-refusals.md)     | Three connectors at most, no subscriptions, the cloud off by default and masked, and refusals shown, never retried |
| [0011 Trivial command filter](docs/adr/0011-trivial-command-filter.md)           | A finished command is dropped only when it cannot tell Kagegami anything                                           |
