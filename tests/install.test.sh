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

fixtures="$tmp_dir/fixtures"
payload="$tmp_dir/payload"
fake_bin="$tmp_dir/fake-bin"
home="$tmp_dir/home"
archive="tmh_${os}_${arch}.tar.gz"
mkdir -p "$fixtures" "$payload" "$fake_bin" "$home"

(cd "$repo_root" && go build -o "$payload/tmh" ./cmd/tmh)
cp "$repo_root/shell/tmh.zsh" "$payload/tmh.zsh"
tar -czf "$fixtures/$archive" -C "$payload" tmh tmh.zsh
if command -v sha256sum >/dev/null 2>&1; then
  digest="$(sha256sum "$fixtures/$archive" | awk '{print $1}')"
else
  digest="$(shasum -a 256 "$fixtures/$archive" | awk '{print $1}')"
fi
printf '%s  %s\n' "$digest" "$archive" > "$fixtures/checksums.txt"

cat > "$fake_bin/curl" <<'FAKE_CURL'
#!/bin/sh
set -eu
url="$2"
output="$4"
cp "$TMH_INSTALL_FIXTURES/$(basename "$url")" "$output"
FAKE_CURL
chmod +x "$fake_bin/curl"

PATH="$fake_bin:$home/.local/bin:$PATH"
export PATH
export HOME="$home"
export XDG_DATA_HOME="$home/.local/share"
export TMH_INSTALL_DIR="$home/.local/bin"
export TMH_INSTALL_ZSH=1
export TMH_INSTALL_FIXTURES="$fixtures"

sh "$repo_root/install.sh" >/dev/null
sh "$repo_root/install.sh" >/dev/null

test -x "$home/.local/bin/tmh"
test -L "$home/.local/bin/tmha"
test -f "$home/.local/share/tmh/shell/tmh.zsh"
test -f "$home/.zshrc"
test "$(grep -c '^# >>> tmh shell integration >>>$' "$home/.zshrc")" -eq 1
test "$($home/.local/bin/tmh --version)" != ""
