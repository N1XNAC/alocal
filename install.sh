#!/bin/sh
# Alocal installer. Usage:
#   curl -fsSL https://raw.githubusercontent.com/OWNER/REPO/main/install.sh | ALOCAL_REPO=OWNER/REPO sh
#   ./install.sh                       # from a source checkout (builds from source)
set -eu

BIN="alocal"
VERSION="${ALOCAL_VERSION:-latest}"
INSTALL_DIR="${ALOCAL_INSTALL_DIR:-$HOME/.local/bin}"
REPO="${ALOCAL_REPO:-}"
BASE_URL="${ALOCAL_BASE_URL:-}"

say() { printf '%s\n' "$*"; }
die() { printf 'alocal installer: %s\n' "$*" >&2; exit 1; }

json_escape() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

fetch() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$2" "$1"
  else
    die "curl or wget is required to download $1"
  fi
}

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

# From a cloned repository: build from source with Go 1.22+.
if [ -f ./go.mod ] && [ -d ./cmd/alocal ]; then
  command -v go >/dev/null 2>&1 || die "Go 1.22+ is required to build from source"
  say "Building Alocal from source..."
  (cd . && go build -trimpath -ldflags='-s -w' -o "$tmp/$BIN" ./cmd/alocal)
else
  # Otherwise download a static release binary for this OS/architecture.
  os="$(uname -s)"
  case "$os" in
    Linux)   os=linux ;;
    Darwin)  os=darwin ;;
    MINGW*|MSYS*|CYGWIN*) os=windows ;;
    *) die "unsupported operating system: $os" ;;
  esac

  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) die "unsupported architecture: $arch" ;;
  esac

  asset="$BIN-$os-$arch"
  [ "$os" = windows ] && asset="$asset.exe"

  if [ -n "$BASE_URL" ]; then
    base="$BASE_URL"
  else
    [ -n "$REPO" ] || die "set ALOCAL_REPO=OWNER/REPO (your GitHub repository)"
    if [ "$VERSION" = latest ]; then
      base="https://github.com/$REPO/releases/latest/download"
    else
      base="https://github.com/$REPO/releases/download/$VERSION"
    fi
  fi

  say "Downloading $asset ($VERSION) from $REPO..."
  fetch "$base/$asset" "$tmp/$asset" || die "download failed: check the repo, version, and asset name"

  # Verify the published SHA-256 checksum before installing.
  fetch "$base/checksums.txt" "$tmp/checksums.txt" || die "checksums.txt not found; refusing to install an unverified binary"
  expected="$(awk -v a="$asset" '{ n=$2; sub(/^.*\//, "", n); sub(/^\*/, "", n); if (n == a) { print $1; exit } }' "$tmp/checksums.txt")"
  [ -n "$expected" ] || die "no checksum for $asset"
  if command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "$tmp/$asset" | awk '{print $1}')"
  elif command -v shasum >/dev/null 2>&1; then
    actual="$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')"
  else
    die "sha256sum or shasum is required to verify the download"
  fi
  [ "$expected" = "$actual" ] || die "checksum mismatch: expected $expected, got $actual"
  say "Checksum verified."
  mv "$tmp/$asset" "$tmp/$BIN"
fi

mkdir -p "$INSTALL_DIR"
if command -v install >/dev/null 2>&1; then
  install -m 0755 "$tmp/$BIN" "$INSTALL_DIR/$BIN"
else
  cp "$tmp/$BIN" "$INSTALL_DIR/$BIN"
  chmod 0755 "$INSTALL_DIR/$BIN"
fi

say "Installed $INSTALL_DIR/$BIN"

# Remember where `alocal update` should fetch new releases.
if [ -n "$REPO" ] || [ -n "$BASE_URL" ]; then
  config_dir="${ALOCAL_CONFIG_DIR:-${XDG_CONFIG_HOME:-$HOME/.config}/alocal}"
  mkdir -p "$config_dir"
  printf '{"repo":"%s","base_url":"%s"}\n' "$(json_escape "$REPO")" "$(json_escape "$BASE_URL")" > "$config_dir/update.json"
  say "Saved update source to $config_dir/update.json"
fi

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) say "Add $INSTALL_DIR to your PATH, then run: alocal" ;;
esac
"$INSTALL_DIR/$BIN" --version || true
