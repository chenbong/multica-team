#!/usr/bin/env bash
# multica-team installer — installs the `multica` CLI (which hosts the agent
# daemon) built by this repository's own CI.
#
#   curl -fsSL https://raw.githubusercontent.com/chenbong/multica-team/main/scripts/install.sh | bash
#
# Binaries come from this repository's GitHub Releases, which the "Build daemon"
# workflow publishes for every `v*.*.*` tag: one archive per platform named
# `multica_<os>_<arch>.tar.gz` (zip for Windows) plus a `checksums.txt`.
# Upstream's own installer is not used on purpose — this deployment ships the
# overlay build (skill scopes, runtime profiles, model catalog fix, …).
#
# Environment overrides:
#   MULTICA_VERSION        release tag to install (default: the latest release)
#   MULTICA_BIN_DIR        where to put the binary (default: /usr/local/bin, else ~/.local/bin)
#   MULTICA_DOWNLOAD_BASE  base URL of the releases (default: this repository)
#   GITHUB_TOKEN           bearer token for downloading from a private repository
set -euo pipefail

REPO_WEB_URL="${MULTICA_REPO_WEB_URL:-https://github.com/chenbong/multica-team}"
REPO_RAW_URL="${MULTICA_RAW_BASE:-https://raw.githubusercontent.com/chenbong/multica-team/main}"

BOLD='' GREEN='' YELLOW='' RED='' CYAN='' RESET=''
if [ -t 1 ] || [ -t 2 ]; then
  BOLD='\033[1m' GREEN='\033[0;32m' YELLOW='\033[0;33m' RED='\033[0;31m' CYAN='\033[0;36m' RESET='\033[0m'
fi

info() { printf "${BOLD}${CYAN}==> %s${RESET}\n" "$*"; }
ok()   { printf "${BOLD}${GREEN}✓ %s${RESET}\n" "$*"; }
warn() { printf "${BOLD}${YELLOW}⚠ %s${RESET}\n" "$*" >&2; }
fail() { printf "${BOLD}${RED}✗ %s${RESET}\n" "$*" >&2; exit 1; }

command_exists() { command -v "$1" >/dev/null 2>&1; }

# Persist a PATH entry in the user's shell profiles, the way upstream's
# installer does: only append when the directory is not mentioned yet, and only
# touch profiles that already exist (never create or rewrite one).
add_to_path() {
  local dir="$1"
  local line="export PATH=\"$dir:\$PATH\""
  for rc in "$HOME/.bashrc" "$HOME/.zshrc"; do
    [ -f "$rc" ] || continue
    grep -qF "$dir" "$rc" && continue
    if printf '\n# Added by multica-team installer\n%s\n' "$line" >>"$rc" 2>/dev/null; then
      ok "Added ${dir} to PATH in ${rc}"
    else
      warn "Could not update ${rc}; add ${dir} to PATH manually."
    fi
  done
}

# curl wrapper: adds the bearer token when one is provided, so a private
# repository can be installed from without putting the token in the URL.
fetch() {
  if [ -n "${GITHUB_TOKEN:-}" ]; then
    curl -fsSL -H "Authorization: Bearer ${GITHUB_TOKEN}" "$@"
  else
    curl -fsSL "$@"
  fi
}

usage() {
  cat <<EOF
multica-team installer

Usage: install.sh [--version <tag>] [--dir <path>] [--help]

Environment: MULTICA_VERSION, MULTICA_BIN_DIR, GITHUB_TOKEN
EOF
}

VERSION="${MULTICA_VERSION:-}"
BIN_DIR="${MULTICA_BIN_DIR:-}"
while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="${2:-}"; shift 2 ;;
    --dir) BIN_DIR="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) fail "Unknown argument: $1 (try --help)" ;;
  esac
done

# ---------------------------------------------------------------------------
# Platform
# ---------------------------------------------------------------------------
case "$(uname -s)" in
  Darwin) OS="darwin" ;;
  Linux) OS="linux" ;;
  MINGW*|MSYS*|CYGWIN*)
    fail "On Windows download multica_windows_amd64.zip from ${REPO_WEB_URL}/releases and unzip it." ;;
  *) fail "Unsupported operating system: $(uname -s)" ;;
esac

case "$(uname -m)" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) fail "Unsupported architecture: $(uname -m)" ;;
esac

info "Platform: ${OS}/${ARCH}"
command_exists curl || fail "curl is required"
command_exists tar || fail "tar is required"

# ---------------------------------------------------------------------------
# Release
# ---------------------------------------------------------------------------
if [ -z "$VERSION" ]; then
  info "Resolving the latest release of ${REPO_WEB_URL}..."
  location="$(curl -sI ${GITHUB_TOKEN:+-H "Authorization: Bearer ${GITHUB_TOKEN}"} "${REPO_WEB_URL}/releases/latest" \
    | tr -d '\r' | awk 'tolower($1) == "location:" { print $2 }' | tail -1)"
  VERSION="$(printf '%s' "${location##*/}")"
  [ -n "$VERSION" ] || fail "Could not determine the latest release. Set MULTICA_VERSION or check network access."
fi

ASSET="multica_${OS}_${ARCH}.tar.gz"
BASE="${MULTICA_DOWNLOAD_BASE:-${REPO_WEB_URL}/releases/download/${VERSION}}"
URL="${BASE}/${ASSET}"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

info "Downloading ${URL}"
fetch "$URL" -o "$TMP_DIR/$ASSET" || fail "Failed to download ${ASSET} for ${VERSION}."

# Verify the published checksum when the release ships one.
if fetch "${BASE}/checksums.txt" -o "$TMP_DIR/checksums.txt" 2>/dev/null; then
  expected="$(awk -v asset="$ASSET" '$2 == asset { print $1 }' "$TMP_DIR/checksums.txt" | head -1)"
  if [ -n "$expected" ]; then
    if command_exists sha256sum; then actual="$(sha256sum "$TMP_DIR/$ASSET" | awk '{ print $1 }')"
    elif command_exists shasum; then actual="$(shasum -a 256 "$TMP_DIR/$ASSET" | awk '{ print $1 }')"
    else actual=""; warn "No sha256 tool found; skipping checksum verification."
    fi
    [ -z "$actual" ] || [ "$actual" = "$expected" ] || fail "Checksum mismatch for ${ASSET}."
    [ -z "$actual" ] || ok "Checksum verified"
  fi
else
  warn "checksums.txt not available for ${VERSION}; skipping verification."
fi

tar -xzf "$TMP_DIR/$ASSET" -C "$TMP_DIR" multica || fail "Archive did not contain the multica binary."
chmod +x "$TMP_DIR/multica"

# ---------------------------------------------------------------------------
# Install
# ---------------------------------------------------------------------------
# Pick a directory that actually accepts the binary. `mkdir -p` on an existing
# directory succeeds even when it is not writable, so writability is tested on
# the directory itself and sudo is validated before it is used.
install_binary() {
  local dir="$1"
  shift
  "$@" install -m 0755 "$TMP_DIR/multica" "$dir/multica"
}

fallback_dir() { printf '%s' "$HOME/.local/bin"; }

if [ -z "$BIN_DIR" ]; then
  if [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
    BIN_DIR="/usr/local/bin"
  elif command_exists sudo && [ -e /dev/tty ] && sudo -v 2>/dev/null; then
    BIN_DIR="/usr/local/bin"
    SUDO_NEEDED="yes"
  else
    BIN_DIR="$(fallback_dir)"
  fi
fi

mkdir -p "$BIN_DIR" 2>/dev/null || true

if [ "${SUDO_NEEDED:-}" = "yes" ] && [ -w "$BIN_DIR" ]; then
  SUDO_NEEDED=""
fi

if [ "${SUDO_NEEDED:-}" = "yes" ]; then
  info "Installing to ${BIN_DIR} (sudo)"
  install_binary "$BIN_DIR" sudo || BIN_DIR="$(fallback_dir)"
elif [ -w "$BIN_DIR" ]; then
  install_binary "$BIN_DIR"
elif command_exists sudo && [ -e /dev/tty ] && sudo -v 2>/dev/null; then
  info "Installing to ${BIN_DIR} (sudo)"
  install_binary "$BIN_DIR" sudo || BIN_DIR="$(fallback_dir)"
else
  warn "${BIN_DIR} is not writable; installing to $(fallback_dir) instead."
  warn "For a system-wide install: export MULTICA_BIN_DIR=/usr/local/bin and re-run with sudo."
  BIN_DIR="$(fallback_dir)"
fi

mkdir -p "$BIN_DIR"
[ -x "$BIN_DIR/multica" ] || install_binary "$BIN_DIR"

ok "Installed $("$BIN_DIR/multica" version 2>/dev/null | head -1 || echo "multica $VERSION") to ${BIN_DIR}/multica"

case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *)
    export PATH="$BIN_DIR:$PATH"
    add_to_path "$BIN_DIR"
    warn "${BIN_DIR} was not in PATH; added it to your shell profile (open a new shell or source the file)."
    ;;
esac

printf '\nNext steps:\n'
printf '  1. Configure the daemon for this server (the web UI prints the exact command under "Add a computer").\n'
printf '  2. Start it in the background:  multica --profile <name> daemon start\n'
printf '  Docs: %s\n' "${REPO_RAW_URL}/README.md"
