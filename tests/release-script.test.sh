#!/bin/sh
set -eu

source_repo="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

fake_bin="$tmp_dir/fake-bin"
mkdir -p "$fake_bin"

for workflow in "$source_repo/.github/workflows/ci.yml" "$source_repo/.github/workflows/release.yml"; do
  if ! grep -Eq 'apt-get install -y([^#]*[[:space:]])ripgrep([[:space:]]|$)' "$workflow"; then
    printf '%s\n' "Linux workflow does not install ripgrep: $workflow" >&2
    exit 1
  fi
done
if ! grep -Eq 'brew install([^#]*[[:space:]])ripgrep([[:space:]]|$)' "$source_repo/.github/workflows/ci.yml"; then
  printf '%s\n' 'macOS CI workflow does not install ripgrep' >&2
  exit 1
fi

cat > "$fake_bin/gh" <<'FAKE_GH'
#!/bin/sh
set -eu
case "${1:-} ${2:-}" in
  'auth status') exit 0 ;;
  'repo view')
    case "$*" in
      *nameWithOwner*) printf '%s\n' 'AllenReder/tmh' ;;
      *viewerPermission*) printf '%s\n' 'ADMIN' ;;
      *defaultBranchRef*) printf '%s\n' 'main' ;;
      *) exit 1 ;;
    esac
    ;;
  'secret list') printf '%s\n' 'HOMEBREW_TAP_DEPLOY_KEY' ;;
  api*)
    case "$*" in
      *'/environments/release'*) printf '%s\n' '{}' ;;
      *'/releases/tags/'*)
        if [ "${TMH_TEST_GITHUB_RELEASE_EXISTS:-0}" = '1' ]; then
          printf '%s\n' '{}'
        else
          printf '%s\n' 'gh: Not Found (HTTP 404)' >&2
          exit 1
        fi
        ;;
      *) exit 1 ;;
    esac
    ;;
  *) exit 1 ;;
esac
FAKE_GH

cat > "$fake_bin/npm" <<'FAKE_NPM'
#!/bin/sh
set -eu
if [ "${1:-}" = 'view' ]; then
  if [ "${TMH_TEST_NPM_VERSION_EXISTS:-0}" = '1' ]; then
    printf '%s\n' '1.2.3'
    exit 0
  fi
  printf '%s\n' 'npm error code E404' >&2
  exit 1
fi
exit 1
FAKE_NPM

cat > "$fake_bin/make" <<'FAKE_MAKE'
#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$TMH_TEST_MAKE_LOG"
exit 99
FAKE_MAKE

cat > "$fake_bin/goreleaser" <<'FAKE_GORELEASER'
#!/bin/sh
exit 99
FAKE_GORELEASER
chmod +x "$fake_bin/gh" "$fake_bin/npm" "$fake_bin/make" "$fake_bin/goreleaser"

create_repository() {
  case_name="$1"
  case_root="$tmp_dir/$case_name"
  remote="$case_root/remote.git"
  repo="$case_root/repo"
  mkdir -p "$case_root"
  git init --bare -q "$remote"
  git init -q -b main "$repo"
  git -C "$repo" config user.name 'Release Test'
  git -C "$repo" config user.email 'release-test@example.com'
  mkdir -p "$repo/scripts" "$repo/.github/workflows"
  cp "$source_repo/scripts/release-lib.sh" "$repo/scripts/release-lib.sh"
  printf '%s\n' 'module github.com/AllenReder/tmh' > "$repo/go.mod"
  printf '%s\n' 'name: Publish packages' > "$repo/.github/workflows/publish-packages.yml"
  git -C "$repo" add .
  git -C "$repo" commit -q -m 'test fixture'
  git -C "$repo" remote add origin 'git@github.com:AllenReder/tmh.git'
  git -C "$repo" config "url.file://$remote.insteadOf" 'git@github.com:AllenReder/tmh.git'
  git -C "$repo" push -q -u origin main
  printf '%s\n%s\n' "$repo" "$remote"
}

run_conflict_case() {
  case_name="$1"
  conflict="$2"
  paths="$(create_repository "$case_name")"
  repo="$(printf '%s\n' "$paths" | sed -n '1p')"
  remote="$(printf '%s\n' "$paths" | sed -n '2p')"
  case "$conflict" in
    local-tag)
      git -C "$repo" tag v1.2.3
      ;;
    remote-tag)
      git -C "$repo" tag v1.2.3
      git -C "$repo" push -q origin v1.2.3
      git -C "$repo" tag -d v1.2.3 >/dev/null
      ;;
  esac

  before_refs="$(git --git-dir="$remote" show-ref || true)"
  make_log="$tmp_dir/$case_name/make.log"
  export TMH_TEST_MAKE_LOG="$make_log"
  TMH_TEST_GITHUB_RELEASE_EXISTS=0
  TMH_TEST_NPM_VERSION_EXISTS=0
  case "$conflict" in
    github-release) TMH_TEST_GITHUB_RELEASE_EXISTS=1 ;;
    npm-version) TMH_TEST_NPM_VERSION_EXISTS=1 ;;
  esac
  export TMH_TEST_GITHUB_RELEASE_EXISTS TMH_TEST_NPM_VERSION_EXISTS

  if PATH="$fake_bin:$PATH" TMH_REPO_DIR="$repo" "$source_repo/scripts/release.sh" v1.2.3 >"$tmp_dir/$case_name/stdout" 2>"$tmp_dir/$case_name/stderr"; then
    printf 'release preflight accepted %s\n' "$conflict" >&2
    exit 1
  fi
  after_refs="$(git --git-dir="$remote" show-ref || true)"
  [ "$after_refs" = "$before_refs" ] || {
    printf 'release preflight changed remote refs for %s\n' "$conflict" >&2
    exit 1
  }
  test ! -e "$make_log"
}

if "$source_repo/scripts/release.sh" v1.2.3-rc1 >/dev/null 2>&1; then
  printf 'release script accepted a prerelease version\n' >&2
  exit 1
fi

run_conflict_case local-tag local-tag
run_conflict_case remote-tag remote-tag
run_conflict_case github-release github-release
run_conflict_case npm-version npm-version

printf 'Release script preflight tests passed.\n'
