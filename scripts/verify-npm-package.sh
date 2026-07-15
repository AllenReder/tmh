#!/bin/sh
set -eu

repo_dir="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
. "$repo_dir/scripts/release-lib.sh"

if [ "$#" -ne 2 ]; then
  printf 'usage: %s <version> <npm-package.tgz>\n' "$0" >&2
  exit 2
fi

version="$(release_normalize_version "$1")"
bare_version="${version#v}"
package="$2"
[ -f "$package" ] || release_fail "npm package not found: $package"
package="$(CDPATH= cd -- "$(dirname "$package")" && pwd)/$(basename "$package")"
release_require_commands node npm

install_root="$(mktemp -d)"
trap 'rm -rf "$install_root"' EXIT HUP INT TERM
npm install --ignore-scripts --no-audit --no-fund --prefix "$install_root" "$package" >/dev/null

tmh_bin="$install_root/node_modules/.bin/tmh"
tmha_bin="$install_root/node_modules/.bin/tmha"
package_root="$install_root/node_modules/@allenreder/tmh"
installed_package_version="$(node -p 'require(process.argv[1]).version' "$package_root/package.json")"
[ "$installed_package_version" = "$bare_version" ] || release_fail "npm package manifest version is $installed_package_version, expected $bare_version"
tmh_version="$($tmh_bin --version)"
tmha_version="$($tmha_bin --version)"
[ -n "$tmh_version" ] || release_fail "npm tmh returned an empty version"
[ "$tmha_version" = "$tmh_version" ] || release_fail "npm tmh and tmha versions differ"
if [ "${TMH_VERIFY_SNAPSHOT:-0}" != "1" ]; then
  [ "$tmh_version" = "$bare_version" ] || release_fail "npm tmh version is $tmh_version, expected $bare_version"
fi
$tmh_bin --help >/dev/null 2>&1
$tmha_bin --help >/dev/null 2>&1

set +e
$tmh_bin --tmh-invalid-option >/dev/null 2>&1
invalid_status="$?"
set -e
[ "$invalid_status" -eq 2 ] || release_fail "npm launcher did not preserve the invalid argument exit status"

printf 'Verified npm package %s.\n' "$package"
