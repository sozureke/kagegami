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

## Authorized use only

Kagegami is for security testing of systems you own or have explicit written permission to test. You are responsible for staying within scope and the law.

Many certification exams restrict or forbid AI assistance (OSCP, for example). Check the current rules of your exam before using Kagegami in it.

## Documentation

- [Architecture decisions](docs/adr/)

## License

[MIT](LICENSE)
