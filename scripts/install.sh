#!/bin/sh
# Install kagegami from a GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/sozureke/kagegami/main/scripts/install.sh | sh
#
# Environment:
#   KAGEGAMI_VERSION      release tag to install, e.g. v0.1.0 (default: latest stable release)
#   KAGEGAMI_INSTALL_DIR  target directory (default: $HOME/.local/bin)
#
# The archive is verified against the release's checksums.txt (sha256). If cosign
# is installed, the signature of checksums.txt is verified too.
set -eu

REPO="sozureke/kagegami"
BASE_URL="${KAGEGAMI_RELEASE_BASE:-https://github.com/${REPO}/releases/download}"
INSTALL_DIR="${KAGEGAMI_INSTALL_DIR:-$HOME/.local/bin}"

info() { printf '%s\n' "$*"; }
fail() { printf 'error: %s\n' "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || fail "required tool not found: $1"; }

need curl
need tar
need uname

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) fail "unsupported OS: $(uname -s) (supported: Linux, macOS)" ;;
esac

case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) fail "unsupported architecture: $(uname -m) (supported: amd64, arm64)" ;;
esac

if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then
  sha256() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
  fail "required tool not found: sha256sum or shasum"
fi

tag="${KAGEGAMI_VERSION:-}"
if [ -z "$tag" ]; then
  tag=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" |
    sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1) || true
  [ -n "$tag" ] || fail "no stable release found; set KAGEGAMI_VERSION to a tag, e.g. v0.1.0-rc.1"
fi
case "$tag" in
  v*) ;;
  *) tag="v${tag}" ;;
esac
version="${tag#v}"

archive="kagegami_${version}_${os}_${arch}.tar.gz"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

info "Installing kagegami ${tag} (${os}/${arch})"
curl -fsSL -o "${tmp}/${archive}" "${BASE_URL}/${tag}/${archive}" || fail "download failed: ${BASE_URL}/${tag}/${archive}"
curl -fsSL -o "${tmp}/checksums.txt" "${BASE_URL}/${tag}/checksums.txt" || fail "download failed: checksums.txt"

expected=$(grep " ${archive}\$" "${tmp}/checksums.txt" | cut -d' ' -f1)
[ -n "$expected" ] || fail "${archive} not listed in checksums.txt"
actual=$(sha256 "${tmp}/${archive}")
[ "$expected" = "$actual" ] || fail "checksum mismatch for ${archive} (expected ${expected}, got ${actual})"
info "Checksum OK"

if command -v cosign >/dev/null 2>&1; then
  curl -fsSL -o "${tmp}/checksums.txt.sigstore.json" "${BASE_URL}/${tag}/checksums.txt.sigstore.json" ||
    fail "download failed: checksums.txt.sigstore.json"
  cosign verify-blob \
    --bundle "${tmp}/checksums.txt.sigstore.json" \
    --certificate-identity-regexp "^https://github.com/${REPO}/\\.github/workflows/release\\.yml@refs/tags/v" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    "${tmp}/checksums.txt" >/dev/null 2>&1 || fail "signature verification failed"
  info "Signature OK"
else
  info "cosign not found; skipping signature verification (see docs/adr/0003-release-process.md)"
fi

tar -xzf "${tmp}/${archive}" -C "$tmp" kagegami
mkdir -p "$INSTALL_DIR"
install -m 0755 "${tmp}/kagegami" "${INSTALL_DIR}/kagegami"
info "Installed ${INSTALL_DIR}/kagegami"

case ":${PATH}:" in
  *":${INSTALL_DIR}:"*) ;;
  *) info "Note: ${INSTALL_DIR} is not in your PATH." ;;
esac
