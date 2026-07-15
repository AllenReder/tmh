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
cp "$repo_root/shell/tmh.zsh" "$payload/tmh.zsh"

create_fixtures() {
  fixture_dir="$1"
  checksum_mode="${2:-valid}"
  mkdir -p "$fixture_dir"
  tar -czf "$fixture_dir/$archive" -C "$payload" tmh tmh.zsh
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
  case_root="$tmp_dir/$case_name"
  mkdir -p "$case_root/home"
  export HOME="$case_root/home"
  export XDG_DATA_HOME="$case_root/home/.local/share"
  export TMH_INSTALL_DIR="$case_root/home/.local/bin"
  export TMH_INSTALL_ZSH=1
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
  sh "$repo_root/install.sh" >/dev/null
}

original_path="$PATH"
valid_fixtures="$tmp_dir/fixtures-valid"
bad_checksum_fixtures="$tmp_dir/fixtures-bad-checksum"
create_fixtures "$valid_fixtures"
create_fixtures "$bad_checksum_fixtures" invalid

run_install latest "$valid_fixtures" '<latest>'
run_install latest "$valid_fixtures" '<latest>'
grep -Fq "/releases/latest/download/$archive" "$tmp_dir/latest/curl.log"
test -x "$tmp_dir/latest/home/.local/bin/tmh"
test -L "$tmp_dir/latest/home/.local/bin/tmha"
test -f "$tmp_dir/latest/home/.local/share/tmh/shell/tmh.zsh"
test "$(grep -c '^# >>> tmh shell integration >>>$' "$tmp_dir/latest/home/.zshrc")" -eq 1

run_install pinned-bare "$valid_fixtures" '1.2.3'
grep -Fq "/releases/download/v1.2.3/$archive" "$tmp_dir/pinned-bare/curl.log"
test "$("$tmp_dir/pinned-bare/home/.local/bin/tmh" --version)" = '1.2.3'

run_install pinned-v "$valid_fixtures" 'v1.2.3'
grep -Fq "/releases/download/v1.2.3/checksums.txt" "$tmp_dir/pinned-v/curl.log"
test "$("$tmp_dir/pinned-v/home/.local/bin/tmha" --version)" = '1.2.3'

if run_install invalid-version "$valid_fixtures" 'v1.2.3-rc1' >/dev/null 2>&1; then
  printf 'installer accepted an invalid stable version\n' >&2
  exit 1
fi
test ! -e "$tmp_dir/invalid-version/curl.log"

if run_install bad-checksum "$bad_checksum_fixtures" 'v1.2.3' >/dev/null 2>&1; then
  printf 'installer accepted a checksum mismatch\n' >&2
  exit 1
fi

if run_install version-mismatch "$valid_fixtures" 'v1.2.4' >/dev/null 2>&1; then
  printf 'installer accepted a mismatched binary version\n' >&2
  exit 1
fi
test ! -e "$tmp_dir/version-mismatch/home/.local/bin/tmh"

printf 'Installer tests passed.\n'
