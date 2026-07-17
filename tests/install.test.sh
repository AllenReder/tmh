#!/bin/sh
set -eu

repo_root="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

case "$(uname -s)" in
  Darwin) os="darwin" ;;
  Linux) os="linux" ;;
  *) printf 'unsupported test OS\n' >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) printf 'unsupported test architecture\n' >&2; exit 1 ;;
esac

archive="tmh_${os}_${arch}.tar.gz"
payload="$tmp_dir/payload"
fake_bin="$tmp_dir/fake-bin"
mkdir -p "$payload" "$fake_bin"

(cd "$repo_root" && go build -ldflags '-X github.com/AllenReder/tmh/internal/cli.Version=1.2.3' -o "$payload/tmh" ./cmd/tmh)
install -m 0644 "$repo_root/examples/config.toml" "$payload/config.example.toml"

create_fixtures() {
  fixture_dir="$1"
  checksum_mode="${2:-valid}"
  content_mode="${3:-complete}"
  mkdir -p "$fixture_dir"
  if [ "$content_mode" = "missing-config" ]; then
    tar -czf "$fixture_dir/$archive" -C "$payload" tmh
  else
    tar -czf "$fixture_dir/$archive" -C "$payload" tmh config.example.toml
  fi
  if [ "$checksum_mode" = "invalid" ]; then
    digest="0000000000000000000000000000000000000000000000000000000000000000"
  elif command -v sha256sum >/dev/null 2>&1; then
    digest="$(sha256sum "$fixture_dir/$archive" | awk '{print $1}')"
  else
    digest="$(shasum -a 256 "$fixture_dir/$archive" | awk '{print $1}')"
  fi
  printf '%s  %s\n' "$digest" "$archive" > "$fixture_dir/checksums.txt"
}

cat > "$fake_bin/curl" <<'FAKE_CURL'
#!/bin/sh
set -eu
url=""
output=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) output="$2"; shift 2 ;;
    -*) shift ;;
    *) url="$1"; shift ;;
  esac
done
[ -n "$url" ] && [ -n "$output" ]
printf '%s\n' "$url" >> "$TMH_CURL_LOG"
cp "$TMH_INSTALL_FIXTURES/$(basename "$url")" "$output"
FAKE_CURL
chmod +x "$fake_bin/curl"

run_install() {
  case_name="$1"
  fixtures="$2"
  requested_version="$3"
  install_shell="${4:-zsh}"
  install_runner="${5:-normal}"
  case_root="$tmp_dir/$case_name"
  mkdir -p "$case_root/home"
  export HOME="$case_root/home"
  export XDG_CONFIG_HOME="$case_root/home/.config"
  export XDG_DATA_HOME="$case_root/home/.local/share"
  export TMH_INSTALL_DIR="$case_root/home/.local/bin"
  export SHELL="/bin/zsh"
  if [ "$install_shell" = '<default>' ]; then
    unset TMH_INSTALL_SHELL
  else
    export TMH_INSTALL_SHELL="$install_shell"
  fi
  export TMH_INSTALL_FIXTURES="$fixtures"
  export TMH_CURL_LOG="$case_root/curl.log"
  PATH="$fake_bin:$TMH_INSTALL_DIR:$original_path"
  export PATH
  if [ "$requested_version" = "<latest>" ]; then
    unset TMH_VERSION
  else
    TMH_VERSION="$requested_version"
    export TMH_VERSION
  fi
  if [ "$install_runner" = no-tty ]; then
    "$tmp_dir/run-no-tty" sh "$repo_root/install.sh" >/dev/null
  else
    sh "$repo_root/install.sh" >/dev/null
  fi
}

original_path="$PATH"
valid_fixtures="$tmp_dir/fixtures-valid"
bad_checksum_fixtures="$tmp_dir/fixtures-bad-checksum"
missing_config_fixtures="$tmp_dir/fixtures-missing-config"
create_fixtures "$valid_fixtures"
create_fixtures "$bad_checksum_fixtures" invalid
create_fixtures "$missing_config_fixtures" valid missing-config

cat > "$tmp_dir/run-no-tty.go" <<'EOF'
package main

import (
	"os"
	"os/exec"
	"syscall"
)

func main() {
	command := exec.Command(os.Args[1], os.Args[2:]...)
	command.Env = os.Environ()
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Run(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			os.Exit(exitError.ExitCode())
		}
		os.Exit(1)
	}
}
EOF
go build -o "$tmp_dir/run-no-tty" "$tmp_dir/run-no-tty.go"

run_install ask-no-tty "$valid_fixtures" '<latest>' '<default>' no-tty
test -x "$tmp_dir/ask-no-tty/home/.local/bin/tmh"
test ! -e "$tmp_dir/ask-no-tty/home/.zshrc"
cmp -s "$repo_root/examples/config.toml" "$tmp_dir/ask-no-tty/home/.config/tmh/config.toml"
if stat -f '%Lp' "$tmp_dir/ask-no-tty/home/.config/tmh/config.toml" >/dev/null 2>&1; then
  config_mode="$(stat -f '%Lp' "$tmp_dir/ask-no-tty/home/.config/tmh/config.toml")"
else
  config_mode="$(stat -c '%a' "$tmp_dir/ask-no-tty/home/.config/tmh/config.toml")"
fi
test "$config_mode" = 600

mkdir -p "$tmp_dir/preserve-config/home/.config/tmh"
printf '%s\n' 'model = "keep-existing"' > "$tmp_dir/preserve-config/home/.config/tmh/config.toml"
cp "$tmp_dir/preserve-config/home/.config/tmh/config.toml" "$tmp_dir/preserve-config/original-config.toml"
run_install preserve-config "$valid_fixtures" '<latest>' none
cmp -s "$tmp_dir/preserve-config/original-config.toml" "$tmp_dir/preserve-config/home/.config/tmh/config.toml"

mkdir -p "$tmp_dir/preserve-config-symlink/home/.config/tmh" "$tmp_dir/preserve-config-symlink/home/dotfiles"
printf '%s\n' 'model = "keep-symlink-target"' > "$tmp_dir/preserve-config-symlink/home/dotfiles/tmh.toml"
ln -s "$tmp_dir/preserve-config-symlink/home/dotfiles/tmh.toml" "$tmp_dir/preserve-config-symlink/home/.config/tmh/config.toml"
run_install preserve-config-symlink "$valid_fixtures" '<latest>' none
test -L "$tmp_dir/preserve-config-symlink/home/.config/tmh/config.toml"
grep -Fq 'keep-symlink-target' "$tmp_dir/preserve-config-symlink/home/dotfiles/tmh.toml"

run_install latest "$valid_fixtures" '<latest>' zsh
cat > "$tmp_dir/latest/home/.zshrc" <<'EOF'
before legacy block
# >>> tmh shell integration >>>
[ -f "$HOME/.local/share/tmh/shell/tmh.zsh" ] && source "$HOME/.local/share/tmh/shell/tmh.zsh"
# <<< tmh shell integration <<<
after legacy block
EOF
mkdir -p "$tmp_dir/latest/home/.local/share/tmh/shell"
printf '%s\n' '# legacy standalone integration' > "$tmp_dir/latest/home/.local/share/tmh/shell/tmh.zsh"
ln -s tmh "$tmp_dir/latest/home/.local/bin/tmha"
run_install latest "$valid_fixtures" '<latest>' zsh
grep -Fq "/releases/latest/download/$archive" "$tmp_dir/latest/curl.log"
test -x "$tmp_dir/latest/home/.local/bin/tmh"
test ! -e "$tmp_dir/latest/home/.local/bin/tmha"
test ! -e "$tmp_dir/latest/home/.local/share/tmh/shell/tmh.zsh"
test "$(grep -c '^# >>> tmh shell integration >>>$' "$tmp_dir/latest/home/.zshrc")" -eq 1
grep -Fq 'shell init zsh' "$tmp_dir/latest/home/.zshrc"
grep -Fq 'before legacy block' "$tmp_dir/latest/home/.zshrc"
grep -Fq 'after legacy block' "$tmp_dir/latest/home/.zshrc"
if grep -Fq 'tmh.zsh' "$tmp_dir/latest/home/.zshrc"; then
  printf 'installer kept the legacy static Zsh marker body\n' >&2
  exit 1
fi

run_install bash-shell "$valid_fixtures" '<latest>' bash
if [ "$os" = "darwin" ]; then
  bash_startup="$tmp_dir/bash-shell/home/.bash_profile"
else
  bash_startup="$tmp_dir/bash-shell/home/.bashrc"
fi
grep -Fq 'shell init bash' "$bash_startup"

run_install fish-shell "$valid_fixtures" '<latest>' fish
grep -Fq 'shell init fish | source' "$tmp_dir/fish-shell/home/.config/fish/conf.d/tmh.fish"

run_install no-shell "$valid_fixtures" '<latest>' none
test ! -e "$tmp_dir/no-shell/home/.zshrc"

mkdir -p "$tmp_dir/symlink-shell/home/dotfiles"
printf '%s\n' 'existing symlink target' > "$tmp_dir/symlink-shell/home/dotfiles/zshrc"
chmod 0600 "$tmp_dir/symlink-shell/home/dotfiles/zshrc"
ln -s dotfiles/zshrc "$tmp_dir/symlink-shell/home/.zshrc"
run_install symlink-shell "$valid_fixtures" '<latest>' zsh
test -L "$tmp_dir/symlink-shell/home/.zshrc"
grep -Fq 'existing symlink target' "$tmp_dir/symlink-shell/home/dotfiles/zshrc"
grep -Fq 'shell init zsh' "$tmp_dir/symlink-shell/home/dotfiles/zshrc"
if stat -f '%Lp' "$tmp_dir/symlink-shell/home/dotfiles/zshrc" >/dev/null 2>&1; then
  startup_mode="$(stat -f '%Lp' "$tmp_dir/symlink-shell/home/dotfiles/zshrc")"
else
  startup_mode="$(stat -c '%a' "$tmp_dir/symlink-shell/home/dotfiles/zshrc")"
fi
test "$startup_mode" = 600

mkdir -p "$tmp_dir/malformed-marker/home"
cat > "$tmp_dir/malformed-marker/home/.zshrc" <<'EOF'
keep before malformed marker
# >>> tmh shell integration >>>
keep after malformed marker
EOF
cp "$tmp_dir/malformed-marker/home/.zshrc" "$tmp_dir/malformed-marker/original.zshrc"
if run_install malformed-marker "$valid_fixtures" '<latest>' zsh >/dev/null 2>&1; then
  printf 'installer accepted malformed managed markers\n' >&2
  exit 1
fi
cmp -s "$tmp_dir/malformed-marker/original.zshrc" "$tmp_dir/malformed-marker/home/.zshrc"

run_install pinned-bare "$valid_fixtures" '1.2.3' zsh
grep -Fq "/releases/download/v1.2.3/$archive" "$tmp_dir/pinned-bare/curl.log"
test "$("$tmp_dir/pinned-bare/home/.local/bin/tmh" --version)" = '1.2.3'

run_install pinned-v "$valid_fixtures" 'v1.2.3' zsh
grep -Fq "/releases/download/v1.2.3/checksums.txt" "$tmp_dir/pinned-v/curl.log"

if run_install invalid-version "$valid_fixtures" 'v1.2.3-rc1' none >/dev/null 2>&1; then
  printf 'installer accepted an invalid stable version\n' >&2
  exit 1
fi
test ! -e "$tmp_dir/invalid-version/curl.log"

if run_install bad-checksum "$bad_checksum_fixtures" 'v1.2.3' none >/dev/null 2>&1; then
  printf 'installer accepted a checksum mismatch\n' >&2
  exit 1
fi

if run_install missing-config "$missing_config_fixtures" 'v1.2.3' none >/dev/null 2>&1; then
  printf 'installer accepted an archive without config.example.toml\n' >&2
  exit 1
fi
test ! -e "$tmp_dir/missing-config/home/.local/bin/tmh"

if run_install version-mismatch "$valid_fixtures" 'v1.2.4' none >/dev/null 2>&1; then
  printf 'installer accepted a mismatched binary version\n' >&2
  exit 1
fi
test ! -e "$tmp_dir/version-mismatch/home/.local/bin/tmh"

printf 'Installer tests passed.\n'
