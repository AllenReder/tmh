#!/bin/sh
set -eu

REPO="AllenReder/tmh"
INSTALL_DIR="${TMH_INSTALL_DIR:-$HOME/.local/bin}"
DATA_HOME="${XDG_DATA_HOME:-$HOME/.local/share}"

fail() {
  printf 'tmh installer: %s\n' "$*" >&2
  exit 1
}

command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v tar >/dev/null 2>&1 || fail "tar is required"
command -v awk >/dev/null 2>&1 || fail "awk is required"
command -v readlink >/dev/null 2>&1 || fail "readlink is required"

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
requested_version="${TMH_VERSION:-}"
expected_version=""
if [ -n "$requested_version" ]; then
  case "$requested_version" in
    v*) version="$requested_version" ;;
    *) version="v$requested_version" ;;
  esac
  if ! printf '%s\n' "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
    fail "TMH_VERSION must use vMAJOR.MINOR.PATCH"
  fi
  expected_version="${version#v}"
  base_url="https://github.com/$REPO/releases/download/$version"
else
  base_url="https://github.com/$REPO/releases/latest/download"
fi

tmp_dir="$(mktemp -d)"
startup_stage=""
trap 'rm -rf "$tmp_dir"; [ -z "${startup_stage:-}" ] || rm -f "$startup_stage"' EXIT HUP INT TERM

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
if [ -n "$expected_version" ]; then
  actual_version="$("$tmp_dir/tmh" --version)"
  [ "$actual_version" = "$expected_version" ] || fail "downloaded tmh version is $actual_version, expected $expected_version"
fi

mkdir -p "$INSTALL_DIR"
install -m 0755 "$tmp_dir/tmh" "$INSTALL_DIR/tmh"
if [ -L "$INSTALL_DIR/tmha" ]; then
  legacy_target="$(readlink "$INSTALL_DIR/tmha" 2>/dev/null || true)"
  case "$legacy_target" in
    tmh|"$INSTALL_DIR/tmh") rm -f "$INSTALL_DIR/tmha" ;;
  esac
fi
legacy_shell_file="$DATA_HOME/tmh/shell/tmh.zsh"
if [ -f "$legacy_shell_file" ] || [ -L "$legacy_shell_file" ]; then
  rm -f "$legacy_shell_file"
  rmdir "$DATA_HOME/tmh/shell" "$DATA_HOME/tmh" 2>/dev/null || true
fi
if [ -n "$expected_version" ]; then
  installed_version="$("$INSTALL_DIR/tmh" --version)"
  [ "$installed_version" = "$expected_version" ] || fail "installed tmh version is $installed_version, expected $expected_version"
fi

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) printf 'Note: add %s to PATH before using tmh.\n' "$INSTALL_DIR" >&2 ;;
esac

detect_shell() {
  shell_name="$(basename "${SHELL:-}")"
  shell_name="${shell_name#-}"
  case "$shell_name" in
    zsh|bash|fish) printf '%s\n' "$shell_name" ;;
    *) return 1 ;;
  esac
}

has_controlling_tty() {
  (: </dev/tty) 2>/dev/null
}

selected_shell=""
case "${TMH_INSTALL_SHELL:-ask}" in
  none) ;;
  zsh|bash|fish) selected_shell="${TMH_INSTALL_SHELL}" ;;
  auto)
    selected_shell="$(detect_shell)" || fail "TMH_INSTALL_SHELL=auto could not detect zsh, bash, or fish from SHELL"
    ;;
  ask)
    if detected_shell="$(detect_shell 2>/dev/null)" && has_controlling_tty; then
      printf 'Enable tmh %s integration? [y/N] ' "$detected_shell" > /dev/tty
      answer=""
      if IFS= read -r answer < /dev/tty; then
        case "$answer" in y|Y|yes|YES) selected_shell="$detected_shell" ;; esac
      fi
    fi
    ;;
  *) fail "TMH_INSTALL_SHELL must be ask, none, auto, zsh, bash, or fish" ;;
esac

begin_marker="# >>> tmh shell integration >>>"
end_marker="# <<< tmh shell integration <<<"

resolve_startup_target() {
  startup_path="$1"
  startup_hops=0
  while [ -L "$startup_path" ]; do
    startup_link="$(readlink "$startup_path")" || fail "could not read startup-file symlink: $startup_path"
    case "$startup_link" in
      /*) startup_path="$startup_link" ;;
      *) startup_path="$(dirname "$startup_path")/$startup_link" ;;
    esac
    startup_hops=$((startup_hops + 1))
    [ "$startup_hops" -le 16 ] || fail "startup-file symlink chain is too deep: $1"
  done
  printf '%s\n' "$startup_path"
}

update_managed_file() {
  target="$1"
  line="$2"
  resolved_target="$(resolve_startup_target "$target")"
  mkdir -p "$(dirname "$resolved_target")"
  if [ -e "$resolved_target" ] && [ ! -f "$resolved_target" ]; then
    fail "startup file is not a regular file: $target"
  fi
  if [ -f "$resolved_target" ]; then
    if ! awk -v begin="$begin_marker" -v end="$end_marker" '
      $0 == begin {
        if (skipping) exit 42
        skipping = 1
        next
      }
      $0 == end {
        if (!skipping) exit 42
        skipping = 0
        next
      }
      !skipping { print }
      END { if (skipping) exit 42 }
    ' "$resolved_target" > "$tmp_dir/startup"; then
      fail "managed shell integration markers are malformed in $target"
    fi
  else
    : > "$tmp_dir/startup"
  fi
  {
    cat "$tmp_dir/startup"
    printf '\n%s\n%s\n%s\n' "$begin_marker" "$line" "$end_marker"
  } > "$tmp_dir/startup-new"
  startup_stage="$(mktemp "$(dirname "$resolved_target")/.tmh-startup.XXXXXX")" || fail "could not stage startup file: $target"
  if [ -e "$resolved_target" ]; then
    cp -p "$resolved_target" "$startup_stage"
  else
    chmod 0644 "$startup_stage"
  fi
  cat "$tmp_dir/startup-new" > "$startup_stage"
  mv -f "$startup_stage" "$resolved_target"
  startup_stage=""
}

if [ -n "$selected_shell" ]; then
  case "$INSTALL_DIR/tmh" in
    *'"'*|*'`'*|*'$'*|*'\\'*) fail "install path contains characters unsafe for shell startup files" ;;
  esac
  quoted_binary="\"$INSTALL_DIR/tmh\""
  case "$selected_shell" in
    zsh)
      startup_file="${ZDOTDIR:-$HOME}/.zshrc"
      init_line="eval \"\$(\"$INSTALL_DIR/tmh\" shell init zsh)\""
      ;;
    bash)
      if [ "$os" = "darwin" ]; then
        startup_file="$HOME/.bash_profile"
      else
        startup_file="$HOME/.bashrc"
      fi
      init_line="eval \"\$(\"$INSTALL_DIR/tmh\" shell init bash)\""
      ;;
    fish)
      startup_file="${XDG_CONFIG_HOME:-$HOME/.config}/fish/conf.d/tmh.fish"
      init_line="$quoted_binary shell init fish | source"
      ;;
  esac
  update_managed_file "$startup_file" "$init_line"
  printf 'Enabled tmh %s integration in %s.\n' "$selected_shell" "$startup_file"
else
  printf '%s\n' 'Shell integration was not enabled. Add one matching line later:'
  printf '  Zsh:  eval "$(tmh shell init zsh)"\n'
  printf '  Bash: eval "$(tmh shell init bash)"\n'
  printf '  Fish: tmh shell init fish | source\n'
fi

printf 'Installed tmh in %s.\n' "$INSTALL_DIR"
