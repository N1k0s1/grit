#!/bin/sh
# Installs grit on macOS or Linux: downloads the release binary for this
# machine, checks it against the release's checksums.txt, then runs
# `grit install` to start it at login.
#
#   curl -fsSL https://raw.githubusercontent.com/N1k0s1/grit/main/scripts/install.sh | sh
#
# GRIT_VERSION=v0.1.0 picks a release (default: the latest).
# GRIT_BASE_URL overrides where releases come from (a mirror, or a test).
# GRIT_NO_INSTALL=1 downloads and verifies only, and leaves the binary in
# the current folder.
set -eu

REPO="N1k0s1/grit"
VERSION="${GRIT_VERSION:-latest}"

fail() {
  echo "grit install: $1" >&2
  exit 1
}

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) fail "unsupported system $(uname -s). On Windows use install.ps1." ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) fail "unsupported processor $(uname -m)" ;;
esac

if [ "$VERSION" = latest ]; then
  base="https://github.com/$REPO/releases/latest/download"
else
  base="https://github.com/$REPO/releases/download/$VERSION"
fi
base="${GRIT_BASE_URL:-$base}"
name="grit-$os-$arch"

fetch() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL -o "$2" "$1"
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O "$2" "$1"
  else
    fail "needs curl or wget"
  fi
}

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    fail "needs sha256sum or shasum to check the download"
  fi
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Downloading $name ($VERSION)"
fetch "$base/$name" "$tmp/grit" || fail "could not download $base/$name"
fetch "$base/checksums.txt" "$tmp/checksums.txt" || fail "could not download checksums.txt"

want=$(awk -v n="$name" '$2 == n { print $1 }' "$tmp/checksums.txt")
[ -n "$want" ] || fail "checksums.txt has no entry for $name"
got=$(sha256 "$tmp/grit")
[ "$got" = "$want" ] || fail "checksum mismatch for $name: expected $want, got $got. Not installing."
echo "Checksum ok ($got)"
chmod +x "$tmp/grit"

if [ "${GRIT_NO_INSTALL:-}" = 1 ]; then
  mv "$tmp/grit" ./grit
  echo "Verified binary saved as ./grit (GRIT_NO_INSTALL=1, nothing installed)."
  exit 0
fi
"$tmp/grit" install
