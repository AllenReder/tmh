#!/bin/sh
set -eu

repo_dir="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
. "$repo_dir/scripts/release-lib.sh"

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
  printf 'usage: %s <release-assets-dir> [expected-version]\n' "$0" >&2
  exit 2
fi

assets_dir="$1"
expected_version="${2:-}"
[ -d "$assets_dir" ] || release_fail "release assets directory not found: $assets_dir"
assets_dir="$(CDPATH= cd -- "$assets_dir" && pwd)"
release_require_commands awk grep tar

if [ -n "$expected_version" ]; then
  expected_version="$(release_normalize_version "$expected_version")"
  expected_bare="${expected_version#v}"
fi

for asset in $(release_asset_names); do
  [ -f "$assets_dir/$asset" ] || release_fail "missing release asset: $asset"
done
release_verify_checksums "$assets_dir"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

for archive in $(release_archive_names); do
  archive_dir="$tmp_dir/${archive%.tar.gz}"
  mkdir -p "$archive_dir"
  if tar -tzf "$assets_dir/$archive" | grep -Eq '(^|/)(tmha|tmh\.zsh)$'; then
    release_fail "$archive contains a legacy tmha or standalone tmh.zsh artifact"
  fi
  tar -xzf "$assets_dir/$archive" -C "$archive_dir"
  [ -x "$archive_dir/tmh" ] || release_fail "$archive does not contain executable tmh"
  for packaged_file in LICENSE THIRD_PARTY_NOTICES.md README.md README.zh-CN.md config.example.toml; do
    [ -f "$archive_dir/$packaged_file" ] || release_fail "$archive does not contain $packaged_file"
  done
done

if [ -n "$expected_version" ]; then
  native_archive="$(release_native_archive_name)"
  native_binary="$tmp_dir/${native_archive%.tar.gz}/tmh"
  actual_version="$($native_binary --version)"
  [ "$actual_version" = "$expected_bare" ] || release_fail "native archive version is $actual_version, expected $expected_bare"
fi

printf 'Verified release assets in %s.\n' "$assets_dir"
