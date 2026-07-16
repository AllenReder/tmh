#!/usr/bin/env bash
set -euo pipefail

repo="AllenReder/tmh"
repo_dir="${TMH_REPO_DIR:-$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)}"
release_workflow="release.yml"
packages_workflow="publish-packages.yml"
ci_workflow="ci.yml"

. "$repo_dir/scripts/release-lib.sh"

log() {
  printf '\n==> %s\n' "$*"
}

wait_for_run() {
  local workflow="$1"
  local branch="$2"
  local sha="$3"
  local attempts=0
  local run_id=""

  while [[ "$attempts" -lt 60 ]]; do
    run_id="$(gh run list \
      --repo "$repo" \
      --workflow "$workflow" \
      --branch "$branch" \
      --limit 20 \
      --json databaseId,headSha \
      --jq "[.[] | select(.headSha == \"$sha\")][0].databaseId // \"\"" 2>/dev/null || true)"
    if [[ -n "$run_id" ]]; then
      printf '%s\n' "$run_id"
      return 0
    fi
    attempts=$((attempts + 1))
    sleep 5
  done
  return 1
}

wait_for_package_run() {
  local version="$1"
  local attempts=0
  local run_id=""

  while [[ "$attempts" -lt 120 ]]; do
    run_id="$(gh run list \
      --repo "$repo" \
      --workflow "$packages_workflow" \
      --limit 30 \
      --json databaseId,displayTitle \
      --jq "[.[] | select(.displayTitle == \"Publish packages $version\")][0].databaseId // \"\"" 2>/dev/null || true)"
    if [[ -n "$run_id" ]]; then
      printf '%s\n' "$run_id"
      return 0
    fi
    attempts=$((attempts + 1))
    sleep 5
  done
  return 1
}

watch_run() {
  local run_id="$1"
  local label="$2"
  local attempts=0
  local state status conclusion

  while [[ "$attempts" -lt 240 ]]; do
    if state="$(gh run view "$run_id" \
      --repo "$repo" \
      --json status,conclusion \
      --jq '.status + "\t" + (.conclusion // "")' 2>/dev/null)"; then
      status="${state%%$'\t'*}"
      conclusion="${state#*$'\t'}"
      if [[ "$status" == "completed" ]]; then
        [[ "$conclusion" == "success" ]]
        return $?
      fi
    fi

    attempts=$((attempts + 1))
    if (( attempts % 6 == 0 )); then
      printf 'Waiting for %s run %s...\n' "$label" "$run_id"
    fi
    sleep 5
  done
  return 1
}

ensure_github_release_unused() {
  local version="$1"
  local output status
  set +e
  output="$(gh api "repos/$repo/releases/tags/$version" 2>&1)"
  status=$?
  set -e
  if [[ "$status" -eq 0 ]]; then
    release_fail "GitHub Release already exists: $version"
  fi
  grep -Fq 'HTTP 404' <<<"$output" || release_fail "unable to verify whether GitHub Release $version exists"
}

ensure_npm_version_unused() {
  local version="$1"
  local bare_version="${version#v}"
  local output status
  set +e
  output="$(npm view "@allenreder/tmh@$bare_version" version 2>&1)"
  status=$?
  set -e
  if [[ "$status" -eq 0 ]]; then
    release_fail "npm version already exists: @allenreder/tmh@$bare_version"
  fi
  if ! grep -Eiq 'E404|404 Not Found|is not in this registry|No match found' <<<"$output"; then
    release_fail "unable to verify whether npm version @allenreder/tmh@$bare_version exists"
  fi
}

ensure_version_unused() {
  local version="$1"
  local remote_tag
  if git tag --list "$version" | grep -q .; then
    release_fail "local tag already exists: $version"
  fi
  remote_tag="$(git ls-remote --tags origin "refs/tags/$version")" || release_fail "unable to query remote tags"
  [[ -z "$remote_tag" ]] || release_fail "remote tag already exists: $version"
  ensure_github_release_unused "$version"
  ensure_npm_version_unused "$version"
}

if [[ "$#" -ne 1 ]]; then
  release_fail "usage: release.sh <vMAJOR.MINOR.PATCH>"
fi
version="$(release_normalize_version "$1")"

release_require_commands git gh go make zsh goreleaser tar rg node npm ruby openssl curl
[ -d "$repo_dir/.git" ] || release_fail "repository not found: $repo_dir"
cd "$repo_dir"

[[ "$(git rev-parse --show-toplevel)" == "$repo_dir" ]] || release_fail "unexpected repository root"
grep -Fxq 'module github.com/AllenReder/tmh' go.mod || release_fail "unexpected Go module"
git config --get remote.origin.url | grep -Eq 'github\.com[:/]AllenReder/tmh(\.git)?$' || release_fail "origin is not $repo"
gh auth status >/dev/null
gh repo view "$repo" --json nameWithOwner --jq .nameWithOwner | grep -Fxq "$repo" || release_fail "GitHub repository is unavailable"
viewer_permission="$(gh repo view "$repo" --json viewerPermission --jq '.viewerPermission // ""')"
[[ "$viewer_permission" == "ADMIN" ]] || release_fail "GitHub ADMIN permission is required for release preflight checks"
[[ "$(git branch --show-current)" == "main" ]] || release_fail "release must run from main"
[[ -z "$(git status --porcelain)" ]] || release_fail "working tree is not clean; commit pending changes first"

log "Synchronizing main"
git fetch origin --tags
git pull --ff-only origin main
[[ "$(git rev-parse HEAD)" == "$(git rev-parse origin/main)" ]] || release_fail "local main does not match origin/main"

default_branch="$(gh repo view "$repo" --json defaultBranchRef --jq '.defaultBranchRef.name // ""')"
[[ "$default_branch" == "main" ]] || release_fail "the GitHub default branch must be main"
gh api "repos/$repo/environments/release" >/dev/null 2>&1 || release_fail "the release GitHub Environment is not configured"
release_secrets="$(gh secret list --repo "$repo" --env release --json name --jq '.[].name')"
grep -Fxq 'HOMEBREW_TAP_DEPLOY_KEY' <<<"$release_secrets" || release_fail "HOMEBREW_TAP_DEPLOY_KEY is not configured in the release Environment"
if grep -Fxq 'NPM_BOOTSTRAP_TOKEN' <<<"$release_secrets"; then
  release_fail "NPM_BOOTSTRAP_TOKEN must be removed; npm releases use OIDC Trusted Publishing"
fi
if rg -n 'NPM_BOOTSTRAP_TOKEN|NODE_AUTH_TOKEN' .github/workflows/publish-packages.yml >/dev/null; then
  release_fail "publish-packages.yml contains an npm token fallback"
fi

ensure_version_unused "$version"

log "Running source checks"
make check

log "Building and verifying the release snapshot"
make release-check VERSION="$version"
[[ -z "$(git status --porcelain)" ]] || release_fail "working tree changed during validation"

log "Pushing main"
git push -u origin main
head_sha="$(git rev-parse HEAD)"

log "Waiting for CI on main"
ci_run_id="$(wait_for_run "$ci_workflow" main "$head_sha")" || release_fail "CI workflow run was not found"
if ! watch_run "$ci_run_id" "CI"; then
  gh run view "$ci_run_id" --repo "$repo" --log-failed || true
  release_fail "CI failed; no release tag was created"
fi

[[ "$(git rev-parse HEAD)" == "$(git rev-parse origin/main)" ]] || release_fail "local main no longer matches origin/main"
[[ -z "$(git status --porcelain)" ]] || release_fail "working tree is no longer clean"
ensure_version_unused "$version"

log "Creating and pushing $version"
git tag -a "$version" -m "Release $version"
git push origin "$version"

log "Waiting for the Release workflow"
release_run_id="$(wait_for_run "$release_workflow" "$version" "$head_sha")" || release_fail "Release workflow run was not found; the pushed tag must not be moved"
if ! watch_run "$release_run_id" "Release"; then
  gh run view "$release_run_id" --repo "$repo" --log-failed || true
  release_fail "Release run $release_run_id failed after tag push; keep $version immutable, rerun only infrastructure failures, and use a new patch version for source or binary fixes"
fi

log "Waiting for the package publishing workflow"
packages_run_id="$(wait_for_package_run "$version")" || release_fail "package publishing workflow was not found; the pushed tag must not be moved"
if ! watch_run "$packages_run_id" "package publishing"; then
  gh run view "$packages_run_id" --repo "$repo" --log-failed || true
  release_fail "package run $packages_run_id failed; keep $version immutable, inspect it, then retry with: gh workflow run publish-packages.yml --repo $repo --ref main -f version=$version"
fi

release_state="$(gh release view "$version" --repo "$repo" --json isDraft,isPrerelease --jq '[.isDraft,.isPrerelease] | @tsv')"
[[ "$release_state" == $'false\tfalse' ]] || release_fail "published Release is draft or prerelease"

verify_dir="$(mktemp -d)"
install_root="$(mktemp -d)"
package_dir="$(mktemp -d)"
trap 'rm -rf "$verify_dir" "$install_root" "$package_dir"' EXIT

log "Downloading and verifying published assets"
gh release download "$version" --repo "$repo" --dir "$verify_dir"
scripts/verify-release-assets.sh "$verify_dir" "$version"

log "Running the version-pinned installation smoke test"
mkdir -p "$install_root/home"
HOME="$install_root/home" XDG_DATA_HOME="$install_root/home/.local/share" \
  TMH_VERSION="$version" TMH_INSTALL_DIR="$install_root/bin" TMH_INSTALL_SHELL=none \
  sh install.sh >/dev/null
[[ "$("$install_root/bin/tmh" --version)" == "${version#v}" ]] || release_fail "installed version verification failed"
[[ ! -e "$install_root/bin/tmha" ]] || release_fail "installer created legacy tmha"

log "Verifying npm and Homebrew publication"
scripts/prepare-release-packages.sh "$version" "$verify_dir" "$package_dir" >/dev/null
npm_package="$package_dir/allenreder-tmh-${version#v}.tgz"
formula="$package_dir/tmh.rb"
published_summary="$(scripts/verify-published-packages.sh "$version" "$npm_package" "$formula")"

release_url="$(gh release view "$version" --repo "$repo" --json url --jq .url)"
printf '\nRelease completed successfully.\n'
printf 'Version: %s\n' "$version"
printf 'URL: %s\n' "$release_url"
printf 'Commit: %s\n' "$head_sha"
printf 'Verified assets: 5\n'
printf 'Installed version: %s\n' "${version#v}"
printf 'npm package: @allenreder/tmh@%s\n' "${version#v}"
printf '%s\n' "$published_summary"
git status --short --branch
