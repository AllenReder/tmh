#!/bin/sh

release_fail() {
  printf 'tmh release: %s\n' "$*" >&2
  exit 1
}

release_normalize_version() {
  release_version_input="${1:-}"
  case "$release_version_input" in
    v*) release_version="$release_version_input" ;;
    *) release_version="v$release_version_input" ;;
  esac
  if ! printf '%s\n' "$release_version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
    release_fail "version must use vMAJOR.MINOR.PATCH"
  fi
  printf '%s\n' "$release_version"
}

release_bare_version() {
  release_version="$(release_normalize_version "$1")"
  printf '%s\n' "${release_version#v}"
}

release_asset_names() {
  printf '%s\n' checksums.txt
  release_archive_names
}

release_platforms() {
  printf '%s\n' \
    darwin_amd64 \
    darwin_arm64 \
    linux_amd64 \
    linux_arm64
}

release_archive_names() {
  for release_platform in $(release_platforms); do
    release_archive_name "$release_platform"
  done
}

release_archive_name() {
  release_requested_platform="$1"
  release_platform_found=0
  for release_known_platform in $(release_platforms); do
    if [ "$release_requested_platform" = "$release_known_platform" ]; then
      release_platform_found=1
      break
    fi
  done
  [ "$release_platform_found" -eq 1 ] || release_fail "unsupported release platform: $release_requested_platform"
  printf 'tmh_%s.tar.gz\n' "$release_requested_platform"
}

release_require_commands() {
  for release_command_name in "$@"; do
    command -v "$release_command_name" >/dev/null 2>&1 || release_fail "$release_command_name is required"
  done
}

release_verify_checksums() {
  release_assets_dir="$1"
  [ -f "$release_assets_dir/checksums.txt" ] || release_fail "missing release asset: checksums.txt"
  for release_archive in $(release_archive_names); do
    release_expected_sha256="$(release_checksum_for "$release_assets_dir/checksums.txt" "$release_archive")"
    release_actual_sha256="$(release_sha256_file "$release_assets_dir/$release_archive")"
    [ "$release_actual_sha256" = "$release_expected_sha256" ] || release_fail "checksum verification failed: $release_archive"
  done
  release_checksum_entries="$(awk 'NF { count++ } END { print count + 0 }' "$release_assets_dir/checksums.txt")"
  [ "$release_checksum_entries" -eq 4 ] || release_fail "checksums.txt must contain exactly four archive checksums"
}

release_checksum_for() {
  release_checksums_file="$1"
  release_checksum_archive="$2"
  [ -f "$release_checksums_file" ] || release_fail "checksums file not found: $release_checksums_file"
  release_checksum_matches="$(awk -v archive="$release_checksum_archive" '$2 == archive { print $1 }' "$release_checksums_file")"
  release_checksum_count="$(printf '%s\n' "$release_checksum_matches" | awk 'NF { count++ } END { print count + 0 }')"
  [ "$release_checksum_count" -eq 1 ] || release_fail "expected exactly one checksum for $release_checksum_archive"
  if ! printf '%s\n' "$release_checksum_matches" | grep -Eq '^[0-9a-fA-F]{64}$'; then
    release_fail "invalid SHA-256 checksum for $release_checksum_archive"
  fi
  printf '%s\n' "$release_checksum_matches"
}

release_sha256_file() {
  release_sha256_path="$1"
  [ -f "$release_sha256_path" ] || release_fail "file not found for SHA-256: $release_sha256_path"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$release_sha256_path" | awk '{ print $1 }'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$release_sha256_path" | awk '{ print $1 }'
  else
    release_fail "sha256sum or shasum is required"
  fi
}

release_sha512_integrity() {
  release_package="$1"
  printf 'sha512-%s\n' "$(openssl dgst -sha512 -binary "$release_package" | openssl base64 -A)"
}

release_native_platform() {
  case "$(uname -s)" in
    Darwin) release_os="darwin" ;;
    Linux) release_os="linux" ;;
    *) release_fail "unsupported verification operating system: $(uname -s)" ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64) release_arch="amd64" ;;
    arm64|aarch64) release_arch="arm64" ;;
    *) release_fail "unsupported verification architecture: $(uname -m)" ;;
  esac
  printf '%s_%s\n' "$release_os" "$release_arch"
}

release_native_archive_name() {
  release_archive_name "$(release_native_platform)"
}
