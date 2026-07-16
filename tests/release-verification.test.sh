#!/bin/sh
set -eu

repo_dir="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
. "$repo_dir/scripts/release-lib.sh"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

version="v1.2.3"
assets_dir="$tmp_dir/assets"
fixture_dir="$tmp_dir/fixture"
mkdir -p "$assets_dir" "$fixture_dir"

cat > "$tmp_dir/fixture.go" <<'EOF'
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "version":
			fmt.Println("1.2.3")
			return
		case "--help", "help":
			fmt.Println("usage")
			return
		case "--tmh-invalid-option":
			os.Exit(2)
		}
	}
}
EOF
go build -o "$fixture_dir/tmh" "$tmp_dir/fixture.go"
install -m 0644 "$repo_dir/LICENSE" "$fixture_dir/LICENSE"
install -m 0644 "$repo_dir/THIRD_PARTY_NOTICES.md" "$fixture_dir/THIRD_PARTY_NOTICES.md"
install -m 0644 "$repo_dir/README.md" "$fixture_dir/README.md"
install -m 0644 "$repo_dir/README.zh-CN.md" "$fixture_dir/README.zh-CN.md"
install -m 0644 "$repo_dir/examples/config.toml" "$fixture_dir/config.example.toml"

write_checksums() {
  checksum_dir="$1"
  : > "$checksum_dir/checksums.txt"
  for checksum_platform in darwin_amd64 darwin_arm64 linux_amd64 linux_arm64; do
    checksum_archive="tmh_${checksum_platform}.tar.gz"
    checksum_value="$(release_sha256_file "$checksum_dir/$checksum_archive")"
    printf '%s  %s\n' "$checksum_value" "$checksum_archive" >> "$checksum_dir/checksums.txt"
  done
}

write_archives() {
  archive_dir="$1"
  archive_fixture="$2"
  mkdir -p "$archive_dir"
  for archive_platform in darwin_amd64 darwin_arm64 linux_amd64 linux_arm64; do
    tar -czf "$archive_dir/tmh_${archive_platform}.tar.gz" -C "$archive_fixture" .
  done
  write_checksums "$archive_dir"
}

copy_assets() {
  destination="$1"
  mkdir -p "$destination"
  cp "$assets_dir"/* "$destination/"
}

expect_failure() {
  failure_name="$1"
  shift
  if "$@" >"$tmp_dir/$failure_name.out" 2>"$tmp_dir/$failure_name.err"; then
    printf '%s unexpectedly succeeded\n' "$failure_name" >&2
    exit 1
  fi
}

write_archives "$assets_dir" "$fixture_dir"
"$repo_dir/scripts/verify-release-assets.sh" "$assets_dir" "$version" >/dev/null
expect_failure invalid-version "$repo_dir/scripts/verify-release-assets.sh" "$assets_dir" v1.2.3-rc1
expect_failure version-mismatch "$repo_dir/scripts/verify-release-assets.sh" "$assets_dir" v1.2.4

missing_dir="$tmp_dir/missing"
copy_assets "$missing_dir"
rm "$missing_dir/tmh_linux_arm64.tar.gz"
expect_failure missing-asset "$repo_dir/scripts/verify-release-assets.sh" "$missing_dir"

checksum_dir="$tmp_dir/checksum"
copy_assets "$checksum_dir"
printf 'corruption' >> "$checksum_dir/tmh_linux_amd64.tar.gz"
expect_failure checksum-mismatch "$repo_dir/scripts/verify-release-assets.sh" "$checksum_dir"

missing_content_fixture="$tmp_dir/missing-content-fixture"
cp -R "$fixture_dir" "$missing_content_fixture"
rm "$missing_content_fixture/config.example.toml"
missing_content_dir="$tmp_dir/missing-content"
write_archives "$missing_content_dir" "$missing_content_fixture"
expect_failure missing-archive-file "$repo_dir/scripts/verify-release-assets.sh" "$missing_content_dir"

permission_fixture="$tmp_dir/permission-fixture"
cp -R "$fixture_dir" "$permission_fixture"
chmod 0644 "$permission_fixture/tmh"
permission_dir="$tmp_dir/permission"
write_archives "$permission_dir" "$permission_fixture"
expect_failure executable-permission "$repo_dir/scripts/verify-release-assets.sh" "$permission_dir"

legacy_alias_fixture="$tmp_dir/legacy-alias-fixture"
cp -R "$fixture_dir" "$legacy_alias_fixture"
ln -s tmh "$legacy_alias_fixture/tmha"
legacy_alias_dir="$tmp_dir/legacy-alias"
write_archives "$legacy_alias_dir" "$legacy_alias_fixture"
expect_failure legacy-tmha "$repo_dir/scripts/verify-release-assets.sh" "$legacy_alias_dir"

legacy_shell_fixture="$tmp_dir/legacy-shell-fixture"
cp -R "$fixture_dir" "$legacy_shell_fixture"
mkdir -p "$legacy_shell_fixture/shell"
printf '%s\n' '# legacy standalone integration' > "$legacy_shell_fixture/shell/tmh.zsh"
legacy_shell_dir="$tmp_dir/legacy-shell"
write_archives "$legacy_shell_dir" "$legacy_shell_fixture"
expect_failure legacy-tmh-zsh "$repo_dir/scripts/verify-release-assets.sh" "$legacy_shell_dir"

package_dir="$tmp_dir/package"
"$repo_dir/scripts/prepare-release-packages.sh" "$version" "$assets_dir" "$package_dir" >/dev/null
package="$package_dir/allenreder-tmh-1.2.3.tgz"
formula="$package_dir/tmh.rb"
"$repo_dir/scripts/verify-npm-package.sh" "$version" "$package" >/dev/null
expect_failure npm-manifest-version "$repo_dir/scripts/verify-npm-package.sh" v1.2.4 "$package"

legacy_npm_root="$tmp_dir/legacy-npm-root"
mkdir -p "$legacy_npm_root"
tar -xzf "$package" -C "$legacy_npm_root"
mkdir -p "$legacy_npm_root/package/shell"
printf '%s\n' '# legacy standalone integration' > "$legacy_npm_root/package/shell/tmh.zsh"
legacy_npm_package="$tmp_dir/legacy-npm.tgz"
tar -czf "$legacy_npm_package" -C "$legacy_npm_root" package
expect_failure legacy-npm-shell "$repo_dir/scripts/verify-npm-package.sh" "$version" "$legacy_npm_package"

real_npm="$(command -v npm)"
fake_bin="$tmp_dir/fake-bin"
mkdir -p "$fake_bin"
cat > "$fake_bin/npm" <<'FAKE_NPM'
#!/bin/sh
set -eu
if [ "${1:-}" != "view" ]; then
  exec "$TMH_REAL_NPM" "$@"
fi
field="${3:-}"
case "$field" in
  version)
    count="$(cat "$TMH_FAKE_REGISTRY_COUNT" 2>/dev/null || printf '0')"
    count=$((count + 1))
    printf '%s\n' "$count" > "$TMH_FAKE_REGISTRY_COUNT"
    [ "$count" -gt "${TMH_FAKE_REGISTRY_DELAY:-0}" ] || exit 0
    printf '%s\n' "$TMH_FAKE_VERSION"
    ;;
  dist.integrity)
    count="$(cat "$TMH_FAKE_REGISTRY_COUNT" 2>/dev/null || printf '0')"
    [ "$count" -gt "${TMH_FAKE_REGISTRY_DELAY:-0}" ] || exit 0
    printf '%s\n' "$TMH_FAKE_INTEGRITY"
    ;;
  dist.attestations.url)
    printf '%s\n' 'https://example.test/attestation'
    ;;
  *) exit 1 ;;
esac
FAKE_NPM

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
case "$url" in
  *attestation)
    printf '%s\n' '{"predicateType":"https://slsa.dev/provenance/v1"}' > "$output"
    ;;
  *)
    count="$(cat "$TMH_FAKE_TAP_COUNT" 2>/dev/null || printf '0')"
    count=$((count + 1))
    printf '%s\n' "$count" > "$TMH_FAKE_TAP_COUNT"
    [ "$count" -gt "${TMH_FAKE_TAP_DELAY:-0}" ] || exit 22
    cp "$TMH_FAKE_FORMULA_SOURCE" "$output"
    ;;
esac
FAKE_CURL
chmod +x "$fake_bin/npm" "$fake_bin/curl"

expected_integrity="$(release_sha512_integrity "$package")"
export TMH_REAL_NPM="$real_npm"
export TMH_FAKE_VERSION='1.2.3'
export TMH_FAKE_INTEGRITY="$expected_integrity"
export TMH_FAKE_FORMULA_SOURCE="$formula"
export TMH_FAKE_REGISTRY_COUNT="$tmp_dir/registry-count"
export TMH_FAKE_TAP_COUNT="$tmp_dir/tap-count"
export TMH_FAKE_REGISTRY_DELAY=1
export TMH_FAKE_TAP_DELAY=1
PATH="$fake_bin:$PATH"
export PATH

TMH_REGISTRY_ATTEMPTS=3 TMH_TAP_ATTEMPTS=3 TMH_RETRY_DELAY_SECONDS=0 \
  "$repo_dir/scripts/verify-published-packages.sh" "$version" "$package" "$formula" >/dev/null

rm -f "$TMH_FAKE_REGISTRY_COUNT" "$TMH_FAKE_TAP_COUNT"
TMH_FAKE_REGISTRY_DELAY=0
TMH_FAKE_TAP_DELAY=0
TMH_FAKE_INTEGRITY='sha512-different'
export TMH_FAKE_REGISTRY_DELAY TMH_FAKE_TAP_DELAY TMH_FAKE_INTEGRITY
expect_failure published-integrity env TMH_REGISTRY_ATTEMPTS=1 TMH_TAP_ATTEMPTS=1 TMH_RETRY_DELAY_SECONDS=0 \
  "$repo_dir/scripts/verify-published-packages.sh" "$version" "$package" "$formula"

TMH_FAKE_INTEGRITY="$expected_integrity"
TMH_FAKE_REGISTRY_DELAY=4
export TMH_FAKE_INTEGRITY TMH_FAKE_REGISTRY_DELAY
rm -f "$TMH_FAKE_REGISTRY_COUNT"
expect_failure registry-timeout env TMH_REGISTRY_ATTEMPTS=2 TMH_TAP_ATTEMPTS=1 TMH_RETRY_DELAY_SECONDS=0 \
  "$repo_dir/scripts/verify-published-packages.sh" "$version" "$package" "$formula"

TMH_FAKE_REGISTRY_DELAY=0
export TMH_FAKE_REGISTRY_DELAY
printf 'different formula\n' > "$tmp_dir/different.rb"
TMH_FAKE_FORMULA_SOURCE="$tmp_dir/different.rb"
export TMH_FAKE_FORMULA_SOURCE
rm -f "$TMH_FAKE_REGISTRY_COUNT" "$TMH_FAKE_TAP_COUNT"
expect_failure formula-mismatch env TMH_REGISTRY_ATTEMPTS=1 TMH_TAP_ATTEMPTS=2 TMH_RETRY_DELAY_SECONDS=0 \
  "$repo_dir/scripts/verify-published-packages.sh" "$version" "$package" "$formula"

printf 'Release verification tests passed.\n'
