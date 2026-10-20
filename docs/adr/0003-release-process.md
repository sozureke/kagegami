# ADR-0003: Release process

Status: accepted · 2026-10-20

## Context

Releases are built, signed and published by `.github/workflows/release.yml` when a `v*` tag is pushed. We had to decide who creates the tag and the version number: a bot (release-please) or the maintainer.

## Decision

**Manual tagging.** The maintainer decides when to release and which version to use, creates an annotated tag on `main` and pushes it. Everything after that is automated.

Why not release-please:

- A tag created by a bot with the default `GITHUB_TOKEN` does not trigger other workflows, so `release.yml` would not run. Working around it needs a personal access token or a GitHub App, i.e. a long-lived secret, which goes against the keyless design of the pipeline.
- release-please creates the GitHub Release itself, which collides with GoReleaser doing the same and needs extra configuration.
- Release candidates (`-rc.N`, `-alpha.N`), which are central to the road to v0.1.0, are awkward to express there.

The decision is reversible: commit conventions are the same either way, so release-please can be adopted later if releases become frequent or the project gets more maintainers.

### Versioning

SemVer. Before v1.0, minor releases may break the config format, database schema and CLI; database migrations are provided from the first release anyway. Pre-release tags use `-alpha.N` and `-rc.N` and are published as GitHub pre-releases automatically.

### Release steps

1. `main` is green (CI, CodeQL) and `make vuln` is clean.
2. Create and push an annotated tag:
   ```sh
   git checkout main && git pull
   git tag -a v0.1.0-rc.1 -m "v0.1.0-rc.1"
   git push origin v0.1.0-rc.1
   ```
3. The release workflow builds the archives, `checksums.txt`, SPDX SBOMs, the cosign signature bundle and build provenance, and creates the GitHub Release with notes generated from Conventional Commits.
4. Verify the published artifacts (see below) and edit the release notes if needed.

### Verifying a release

```sh
# integrity
sha256sum -c --ignore-missing checksums.txt

# signature: made by this repository's release workflow
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github.com/sozureke/kagegami/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt

# provenance
gh attestation verify kagegami_<version>_<os>_<arch>.tar.gz --repo sozureke/kagegami
```

### Tag protection

Tags matching `v*` are protected by a repository ruleset: only repository admins can create them, and nobody can move or delete them. A tag is what gets signed and distributed, so it must not be silently replaced.

### Bad releases

A published release is never deleted and its tag is never moved. Fix forward with a patch release; mark the bad release as deprecated in its notes.

### Changelog

There is no `CHANGELOG.md` yet. The generated GitHub Release notes are the changelog until v0.1.0; a file can be added later.

## Consequences

- The maintainer picks version numbers by hand and must keep PR titles in Conventional Commit form, since they drive the generated notes.
- Releasing needs no secrets beyond the repository's built-in token.
- The first end-to-end test of the pipeline is the trial release `v0.0.1-alpha.1`.
