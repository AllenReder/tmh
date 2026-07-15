#!/bin/sh
set -eu

repo_dir="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
. "$repo_dir/scripts/release-lib.sh"

if [ "$#" -ne 3 ]; then
  printf 'usage: %s <version> <npm-package.tgz> <formula.rb>\n' "$0" >&2
  exit 2
fi

version="$(release_normalize_version "$1")"
bare_version="${version#v}"
package="$2"
formula="$3"
[ -f "$package" ] || release_fail "npm package not found: $package"
[ -f "$formula" ] || release_fail "Homebrew Formula not found: $formula"
release_require_commands curl npm openssl grep cmp

expected_integrity="$(release_sha512_integrity "$package")"
registry_attempts="${TMH_REGISTRY_ATTEMPTS:-30}"
tap_attempts="${TMH_TAP_ATTEMPTS:-12}"
retry_delay="${TMH_RETRY_DELAY_SECONDS:-20}"
attempt=1
while [ "$attempt" -le "$registry_attempts" ]; do
  published_version="$(npm view "@allenreder/tmh@$bare_version" version 2>/dev/null || true)"
  published_integrity="$(npm view "@allenreder/tmh@$bare_version" dist.integrity 2>/dev/null || true)"
  if [ -n "$published_integrity" ] && [ "$published_integrity" != "$expected_integrity" ]; then
    release_fail "npm package integrity differs from the verified release package"
  fi
  if [ "$published_version" = "$bare_version" ] && [ "$published_integrity" = "$expected_integrity" ]; then
    break
  fi
  [ "$attempt" -lt "$registry_attempts" ] || release_fail "npm registry did not expose the expected package"
  printf 'Waiting for npm registry propagation (attempt %s/%s)...\n' "$attempt" "$registry_attempts"
  sleep "$retry_delay"
  attempt=$((attempt + 1))
done

attestation_url="$(npm view "@allenreder/tmh@$bare_version" dist.attestations.url 2>/dev/null || true)"
[ -n "$attestation_url" ] || release_fail "npm provenance attestation is missing"
attestation_file="$(mktemp)"
published_formula="$(mktemp)"
trap 'rm -f "$attestation_file" "$published_formula"' EXIT HUP INT TERM
curl -fsSL "$attestation_url" -o "$attestation_file"
grep -Fq 'https://slsa.dev/provenance/v1' "$attestation_file" || release_fail "npm SLSA provenance attestation is missing"

"$repo_dir/scripts/verify-npm-package.sh" "$version" "$package" >/dev/null

formula_url="${TMH_HOMEBREW_FORMULA_URL:-https://raw.githubusercontent.com/AllenReder/homebrew-tap/main/Formula/tmh.rb}"
attempt=1
while [ "$attempt" -le "$tap_attempts" ]; do
  if curl -fsSL "$formula_url" -o "$published_formula" && cmp -s "$formula" "$published_formula"; then
    printf 'npm provenance: %s\n' "$attestation_url"
    printf 'Homebrew Formula: %s\n' "$formula_url"
    exit 0
  fi
  [ "$attempt" -lt "$tap_attempts" ] || release_fail "published Homebrew Formula does not match the verified Formula"
  printf 'Waiting for Homebrew tap propagation (attempt %s/%s)...\n' "$attempt" "$tap_attempts"
  sleep "$retry_delay"
  attempt=$((attempt + 1))
done

release_fail "published package verification failed"
