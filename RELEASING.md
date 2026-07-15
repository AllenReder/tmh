# Releasing tmh

GitHub Releases are the source of truth for every tmh binary. A successful
GitHub Release is followed by npm publication and an update to the official
Homebrew tap. The downstream packages must contain the exact binaries from the
published GitHub archives.

Release automation is implemented by:

- `.github/workflows/release.yml` for GitHub assets.
- `.github/workflows/publish-packages.yml` for npm and Homebrew.
- `.goreleaser.yaml` for the supported binary matrix and archives.

## Prerequisites

- Release permission for `AllenReder/tmh`.
- A configured `release` GitHub Environment.
- `git`, `gh`, Go 1.25.12, Node.js 22 or newer, npm, Ruby, Zsh, and
  GoReleaser 2.15.2 available locally.
- A clean, up-to-date `main` branch with required CI checks passing.
- The `HOMEBREW_TAP_DEPLOY_KEY` Environment secret configured as a write-enabled
  deploy key for `AllenReder/homebrew-tap`.
- npm Trusted Publishing configured for `@allenreder/tmh`, workflow
  `publish-packages.yml`, Environment `release`, and the `npm publish` action.

Authenticate and confirm repository access before starting:

```sh
gh auth status
npm whoami
git remote -v
```

## Versioning

All channels use the same stable Semantic Version:

```text
Git tag and GitHub Release: vMAJOR.MINOR.PATCH
npm package and Homebrew Formula: MAJOR.MINOR.PATCH
```

Published tags and npm versions are immutable. Never move, replace, or reuse a
released version.

## Prepare the release

Set and validate the version:

```sh
version=v0.1.1
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

Confirm that the version is unused in every channel:

```sh
! gh release view "$version" >/dev/null 2>&1
! npm view "@allenreder/tmh@${version#v}" version >/dev/null 2>&1
```

Run all deterministic checks and build a release snapshot:

```sh
make check
goreleaser release --snapshot --clean
```

Verify the snapshot assets and package assembly:

```sh
test -f dist/checksums.txt
test -f dist/tmh_darwin_amd64.tar.gz
test -f dist/tmh_darwin_arm64.tar.gz
test -f dist/tmh_linux_amd64.tar.gz
test -f dist/tmh_linux_arm64.tar.gz
scripts/prepare-release-packages.sh "$version" dist dist/packages
test -f "dist/packages/allenreder-tmh-${version#v}.tgz"
test -f dist/packages/tmh.rb
```

## Publish

Verify the worktree once more, then create and push an annotated tag:

```sh
test -z "$(git status --porcelain)"
git tag -a "$version" -m "Release $version"
git push origin "$version"
```

The `Release` workflow validates the tag, repeats all checks, publishes the
GitHub Release, and verifies the five release assets. When it completes, the
`Publish packages` workflow:

1. Downloads and verifies the GitHub Release assets.
2. Builds the npm tarball and Homebrew Formula from those assets.
3. Installs both packages on macOS and Linux.
4. Publishes npm through OIDC Trusted Publishing.
5. Updates `AllenReder/homebrew-tap` using its repository-scoped deploy key.
6. Verifies registry integrity and the published Formula contents.

Monitor both workflows:

```sh
gh run list --workflow release.yml --branch "$version" --limit 1
gh run list --workflow publish-packages.yml --limit 5
```

Inspect failed steps with:

```sh
gh run view RUN_ID --log-failed
```

## Verify the release

Inspect the GitHub Release and download all assets:

```sh
gh release view "$version" --json tagName,url,isDraft,isPrerelease,assets
verify_dir="$(mktemp -d)"
gh release download "$version" --dir "$verify_dir"
```

Verify checksums:

```sh
(
  cd "$verify_dir"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum --check checksums.txt
  else
    shasum -a 256 --check checksums.txt
  fi
)
```

Verify the standalone installer:

```sh
install_root="$(mktemp -d)"
TMH_INSTALL_DIR="$install_root/bin" TMH_INSTALL_ZSH=0 sh install.sh
test "$("$install_root/bin/tmh" --version)" = "${version#v}"
test -L "$install_root/bin/tmha"
```

Verify npm in an isolated prefix:

```sh
test "$(npm view "@allenreder/tmh@${version#v}" version)" = "${version#v}"
npm_root="$(mktemp -d)"
npm install --ignore-scripts --no-audit --no-fund \
  --prefix "$npm_root" "@allenreder/tmh@${version#v}"
test "$("$npm_root/node_modules/.bin/tmh" --version)" = "${version#v}"
test "$("$npm_root/node_modules/.bin/tmha" --version)" = "${version#v}"
```

Verify Homebrew:

```sh
brew update
brew install AllenReder/tap/tmh
brew test AllenReder/tap/tmh
test "$(tmh --version)" = "${version#v}"
test "$(tmha --version)" = "${version#v}"
```

## npm authentication

The npm package is published only through OIDC Trusted Publishing. The trusted
publisher is restricted to `AllenReder/tmh`, workflow
`publish-packages.yml`, Environment `release`, and the `npm publish` action.

Do not add an npm token, `NODE_AUTH_TOKEN`, or an npm credential secret to the
repository or the `release` Environment. If the trusted publisher configuration
must be replaced, update it in npm before changing the workflow and verify the
next real release through its provenance attestation.

## Failure recovery

- Before a tag is pushed, delete only the local tag, fix the problem, and
  repeat all checks.
- After a tag is pushed, never move or overwrite it.
- A transient npm, Homebrew, or network failure can be retried with the manual
  `Publish packages` workflow for the same version. It verifies existing
  content before skipping any publication.
- If npm already contains the version with different integrity, stop. npm
  versions cannot be overwritten; investigate and publish a fixed patch
  version.
- A Formula publishing error may be corrected in the tap only when it still
  references the unchanged, verified GitHub Release assets.
- Any source or binary change requires a new patch release.
