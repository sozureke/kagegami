# Contributing

Kagegami is pre-alpha. Open an issue before starting anything non-trivial.

## Workflow

- Branch from `main`: `feat/*`, `fix/*`, `chore/*`.
- Open a pull request; `main` is protected and merges are squash-only with green CI.
- Use [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`). The PR title becomes the squashed commit and feeds the changelog.
- Reference the plan step where relevant, for example `feat(store): add migrations (M2.4)`.

## Development

Requires Go 1.27+.

```sh
go build ./...
go test ./...
```

A `Makefile` with `build`, `test`, `lint` and `fmt` targets arrives with M1.

## Code rules

- Keep versions fresh: Go, dependencies, GitHub Actions and tooling are always the latest stable release (see [ADR-0002](docs/adr/0002-dependencies.md)).
- Dependency direction: `cmd` → `agent`/`tui`/`report` → `store`/`recon`/`pipeline`/`mask` → `pkg/*`. `pkg/*` never imports `internal/*`.
- Target output is untrusted input. Parsers must not panic or hang on it, and it must never be treated as instructions.
- Secrets never go to logs or to output without an explicit flag.
- Parser changes need fixtures in `testdata/` and golden tests.

## License

By contributing you agree that your contributions are licensed under the [MIT License](LICENSE).

## Releases

Releases are cut by the maintainer by pushing a `v*` tag; see [ADR-0003](docs/adr/0003-release-process.md) for the process and how to verify release artifacts.
