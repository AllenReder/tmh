# Releasing tmh

This document describes the release process for project maintainers. Release
automation is implemented by `.github/workflows/release.yml` and
`.goreleaser.yaml`.

## Prerequisites

- Release permission for the GitHub repository.
- A configured `release` GitHub Environment.
- `git`, `gh`, Go 1.25.12, Zsh, and GoReleaser 2.15.2 available locally.
- A clean, up-to-date `main` branch.
- All required CI and acceptance checks passing.

Authenticate and confirm repository access before starting:

```sh
gh auth status
git remote -v
```

## Versioning

Releases use stable Semantic Versioning tags:

```text
vMAJOR.MINOR.PATCH
```

Examples: `v0.1.0`, `v0.1.1`, `v1.0.0`.

The Release workflow rejects tags that do not match this format. Published
tags are immutable: do not move, replace, or reuse a released version.

## Prepare the release

Set the version to be released:

```sh
version=v0.1.0
printf '%s\n' "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'
```

Update and verify `main`:

```sh
git switch main
git fetch origin --tags
git pull --ff-only origin main
test -z "$(git status --porcelain)"
test "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)"
test -z "$(git tag --list "$version")"
```

Confirm that the version has not already been published:

```sh
if gh release view "$version" >/dev/null 2>&1; then
  echo "release already exists: $version" >&2
  exit 1
fi
```

Run the deterministic checks and build a release snapshot:

```sh
make check
goreleaser release --snapshot --clean
```

Confirm that the snapshot contains the expected archives and checksum file:

```sh
test -f dist/checksums.txt
test -f dist/tmh_darwin_amd64.tar.gz
test -f dist/tmh_darwin_arm64.tar.gz
test -f dist/tmh_linux_amd64.tar.gz
test -f dist/tmh_linux_arm64.tar.gz
tar -tzf dist/tmh_darwin_arm64.tar.gz
```

## Publish

Verify the working tree once more, then create and push an annotated tag:

```sh
test -z "$(git status --porcelain)"
git tag -a "$version" -m "Release $version"
git push origin "$version"
```

Pushing the tag starts the Release workflow. It will:

1. Validate the tag and confirm that its commit is on `main`.
2. Run tests, Vet, shell checks, the installer test, and a release snapshot.
3. Publish the GitHub Release through GoReleaser.
4. Download the published assets and verify their checksums.

## Monitor the workflow

Find and watch the workflow run:

```sh
gh run list --workflow release.yml --branch "$version" --limit 1
run_id="$(gh run list --workflow release.yml --branch "$version" --limit 1 --json databaseId --jq '.[0].databaseId')"
test -n "$run_id"
gh run watch "$run_id" --exit-status
```

If it fails, inspect the failed steps:

```sh
gh run view "$run_id" --log-failed
```

## Verify the release

Inspect the Release and its assets:

```sh
gh release view "$version" --json tagName,url,isDraft,isPrerelease,assets
```

Download all assets and verify their checksums:

```sh
verify_dir="$(mktemp -d)"
gh release download "$version" --dir "$verify_dir"
(
  cd "$verify_dir"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum --check checksums.txt
  else
    shasum -a 256 --check checksums.txt
  fi
)
```

Run an installation smoke test in a temporary directory:

```sh
install_root="$(mktemp -d)"
TMH_INSTALL_DIR="$install_root/bin" TMH_INSTALL_ZSH=0 sh install.sh
test "$("$install_root/bin/tmh" --version)" = "${version#v}"
test -L "$install_root/bin/tmha"
```

## Failure recovery

- Before a tag is pushed, delete the local tag with `git tag -d "$version"`,
  fix the problem, and repeat the checks.
- After a tag is pushed, do not move, overwrite, or automatically delete it.
- If a published release requires code changes, create a new patch release.
- Deleting a remote tag or GitHub Release is an exceptional maintenance action
  and must be reviewed separately.
