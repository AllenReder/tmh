# Releasing tmh

GitHub Releases are the source of truth for every tmh binary. npm and the
official Homebrew tap distribute the exact binaries from the same verified
GitHub Release assets.

## Stable interfaces

- `make check` runs the complete source, distribution, security, workflow, and
  release-configuration checks.
- `make release-check VERSION=vMAJOR.MINOR.PATCH` builds and verifies a local
  snapshot, npm package, and Homebrew Formula. It does not push, tag, publish,
  or use release credentials.
- `scripts/release.sh vMAJOR.MINOR.PATCH` performs the complete stable release
  after all prerequisites are configured.

All release versions are immutable. Git tags and GitHub Releases use
`vMAJOR.MINOR.PATCH`; npm and Homebrew use `MAJOR.MINOR.PATCH`. Prereleases are
not accepted.

## Prerequisites

- A clean, synchronized `main` branch for `AllenReder/tmh`.
- GitHub CLI authentication with administrator access to the repository.
- The `release` GitHub Environment.
- `HOMEBREW_TAP_DEPLOY_KEY` in that Environment, restricted to write access for
  `AllenReder/homebrew-tap`.
- npm Trusted Publishing for `@allenreder/tmh`, restricted to repository
  `AllenReder/tmh`, workflow `publish-packages.yml`, Environment `release`, and
  the `npm publish` action.
- No npm token, `NODE_AUTH_TOKEN`, or token fallback in the repository or
  release Environment.
- Local `git`, `gh`, Go, Node.js 22 or newer, npm, Ruby, Zsh, Bash, Fish 3.6
  or newer, GoReleaser, OpenSSL, curl, tar, and ripgrep. Fish is required for
  a release even though a developer's local `make check` can skip Fish-only
  syntax checks when it is absent.

Confirm access before starting:

```sh
gh auth status
git remote -v
```

## Local validation

Run the same deterministic interfaces used by CI and the Release workflow:

```sh
make check
make release-check VERSION=v0.2.0
```

The snapshot command verifies all four archives, `checksums.txt`, archive
contents and permissions, the assembled npm package and its single `tmh`
launcher, and the generated Homebrew Formula. It also verifies that legacy
`tmha` launchers and standalone shell-integration files are absent. Shell
integration is rendered from the binary through `tmh shell init`. Snapshot
binaries keep GoReleaser's snapshot version and are not required to report the
future release version.

For the v0.2 breaking interface, confirm that validation covers all of the
following before tagging:

- `tmh <request>`, `tmh generate`, and `tmh agent` work while `tmha` and
  `tmh --agent` are rejected.
- Zsh, Bash, and Fish generation use their matching local syntax validators;
  Bash 3.2 takes the documented no-widget fallback.
- `tmh shell init zsh|bash|fish` renders the embedded integration, default
  bindings preserve existing keys, and `--no-bind`/`--force-bind` behave as
  documented.
- `run_command` is absent by default and appears only for
  `tmh agent --exec=inspection`.
- Linux inspection fails closed without Landlock ABI v3 and seccomp-BPF;
  macOS inspection fails closed without a successful deprecated
  `sandbox-exec` Seatbelt canary.
- The example configuration uses `shell`, `generate_timeout_seconds`, and
  `agent_timeout_seconds`; the two v0.1 timeout fields remain rejected.

## Publish

Run the tracked release entry point from the repository:

```sh
scripts/release.sh v0.2.0
```

Before creating a tag, the script verifies the repository, permissions,
Environment, Homebrew key, npm OIDC-only configuration, and that the version is
unused locally, on the remote, in GitHub Releases, and in npm. It then runs both
local validation interfaces, pushes `main`, and waits for CI on the exact
commit.

Only after CI succeeds does it create and push the annotated tag. The `Release`
workflow publishes the GitHub assets, and `Publish packages` publishes or
verifies npm and Homebrew. The script waits for both workflows and performs
post-publication asset, installer, npm integrity, provenance, and Formula
verification.

## Workflow responsibilities

- `.github/workflows/ci.yml` prepares the supported toolchain, including Fish,
  and runs `make check` on macOS and Linux. This exercises both platform
  sandbox implementations and all three shell integrations.
- `.github/workflows/release.yml` independently runs `make check` and
  `make release-check` with Fish installed, publishes the GitHub Release
  through GoReleaser, and verifies the downloaded assets.
- `.github/workflows/publish-packages.yml` assembles packages from the target
  tag and verified Release assets, tests npm and Homebrew on macOS and Linux,
  publishes through scoped credentials, and verifies the public channels.

The package workflow can be safely retried for an existing version. Existing
npm content and Formula content must match exactly before a publish step is
skipped.

## Failure recovery

- Before the tag is pushed, fix the failure and rerun the release command. No
  release version has been consumed.
- After the tag is pushed, never move, replace, or delete it to reuse the
  version.
- If GitHub Release creation fails because of source or binary behavior, fix
  forward with a new patch version.
- For a transient npm, Homebrew, or network failure, inspect the failed run and
  retry only the downstream package workflow for the same immutable Release:

  ```sh
  gh workflow run publish-packages.yml --repo AllenReder/tmh --ref main -f version=v0.2.0
  ```

- If npm already contains the version with different integrity, stop. npm
  versions cannot be overwritten.
- A Formula may be corrected only when it continues to reference the unchanged,
  verified GitHub Release assets.
- Any source or binary change requires a new patch version.

Inspect workflow failures with:

```sh
gh run view RUN_ID --repo AllenReder/tmh --log-failed
```
