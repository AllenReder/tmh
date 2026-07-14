#!/bin/sh
set -eu

REPO="AllenReder/tmh"
INSTALL_DIR="${TMH_INSTALL_DIR:-$HOME/.local/bin}"
DATA_HOME="${XDG_DATA_HOME:-$HOME/.local/share}"
SHELL_DIR="$DATA_HOME/tmh/shell"
ZSHRC="${ZDOTDIR:-$HOME}/.zshrc"

fail() {
  printf 'tmh installer: %s\n' "$*" >&2
  exit 1
}

command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v tar >/dev/null 2>&1 || fail "tar is required"

case "$(uname -s)" in
  Darwin) os="darwin" ;;
  Linux) os="linux" ;;
  *) fail "unsupported operating system: $(uname -s)" ;;
esac

case "$(uname -m)" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) fail "unsupported architecture: $(uname -m)" ;;
esac

archive="tmh_${os}_${arch}.tar.gz"
base_url="https://github.com/$REPO/releases/latest/download"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

printf 'Downloading %s...\n' "$archive"
curl -fsSL "$base_url/$archive" -o "$tmp_dir/$archive"
curl -fsSL "$base_url/checksums.txt" -o "$tmp_dir/checksums.txt"

expected="$(awk -v file="$archive" '$2 == file { print $1 }' "$tmp_dir/checksums.txt")"
[ -n "$expected" ] || fail "checksum for $archive was not found"
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$tmp_dir/$archive" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "$tmp_dir/$archive" | awk '{print $1}')"
else
  fail "sha256sum or shasum is required"
fi
[ "$actual" = "$expected" ] || fail "checksum verification failed"

tar -xzf "$tmp_dir/$archive" -C "$tmp_dir"
[ -x "$tmp_dir/tmh" ] || fail "release archive did not contain tmh"
[ -f "$tmp_dir/tmh.zsh" ] || fail "release archive did not contain tmh.zsh"

mkdir -p "$INSTALL_DIR" "$SHELL_DIR"
install -m 0755 "$tmp_dir/tmh" "$INSTALL_DIR/tmh"
ln -sf tmh "$INSTALL_DIR/tmha"
install -m 0644 "$tmp_dir/tmh.zsh" "$SHELL_DIR/tmh.zsh"

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) printf 'Note: add %s to PATH before using tmh.\n' "$INSTALL_DIR" >&2 ;;
esac

begin_marker="# >>> tmh shell integration >>>"
end_marker="# <<< tmh shell integration <<<"
source_line="[ -f \"$SHELL_DIR/tmh.zsh\" ] && source \"$SHELL_DIR/tmh.zsh\""

install_shell=0
case "${TMH_INSTALL_ZSH:-ask}" in
  1|yes|true) install_shell=1 ;;
  0|no|false) install_shell=0 ;;
  ask)
    if [ -r /dev/tty ]; then
      printf 'Add tmh Zsh integration to %s? [y/N] ' "$ZSHRC" > /dev/tty
      read answer < /dev/tty
      case "$answer" in y|Y|yes|YES) install_shell=1 ;; esac
    fi
    ;;
  *) fail "TMH_INSTALL_ZSH must be ask, 1, or 0" ;;
esac

if [ "$install_shell" -eq 1 ]; then
  if [ -f "$ZSHRC" ] && grep -F "$begin_marker" "$ZSHRC" >/dev/null 2>&1; then
    printf 'Zsh integration is already present in %s.\n' "$ZSHRC"
  else
    mkdir -p "$(dirname "$ZSHRC")"
    {
      printf '\n%s\n' "$begin_marker"
      printf '%s\n' "$source_line"
      printf '%s\n' "$end_marker"
    } >> "$ZSHRC"
    printf 'Added Zsh integration to %s.\n' "$ZSHRC"
  fi
else
  printf 'To enable command insertion later, add this line to your .zshrc:\n%s\n' "$source_line"
fi

printf 'Installed tmh and tmha in %s.\n' "$INSTALL_DIR"
