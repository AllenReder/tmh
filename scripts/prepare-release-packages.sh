#!/bin/sh
set -eu

if [ "$#" -ne 3 ]; then
  printf 'usage: %s <vMAJOR.MINOR.PATCH> <release-assets-dir> <output-dir>\n' "$0" >&2
  exit 2
fi

version="$1"
assets_dir="$2"
output_dir="$3"
repo_dir="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"

if ! printf '%s\n' "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
  printf 'invalid release version: %s\n' "$version" >&2
  exit 2
fi

[ -d "$assets_dir" ] || { printf 'release assets directory not found: %s\n' "$assets_dir" >&2; exit 1; }
[ -n "$output_dir" ] && [ "$output_dir" != "/" ] || { printf 'unsafe output directory\n' >&2; exit 2; }

assets_dir="$(CDPATH= cd -- "$assets_dir" && pwd)"
output_parent="$(dirname "$output_dir")"
output_name="$(basename "$output_dir")"
mkdir -p "$output_parent"
output_parent="$(CDPATH= cd -- "$output_parent" && pwd)"
output_dir="$output_parent/$output_name"

for command_name in awk grep install node npm tar; do
  command -v "$command_name" >/dev/null 2>&1 || { printf '%s is required\n' "$command_name" >&2; exit 1; }
done

for asset in \
  checksums.txt \
  tmh_darwin_amd64.tar.gz \
  tmh_darwin_arm64.tar.gz \
  tmh_linux_amd64.tar.gz \
  tmh_linux_arm64.tar.gz; do
  [ -f "$assets_dir/$asset" ] || { printf 'missing release asset: %s\n' "$asset" >&2; exit 1; }
done

(
  cd "$assets_dir"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum --check checksums.txt
  else
    shasum -a 256 --check checksums.txt
  fi
)

rm -rf -- "$output_dir"
mkdir -p "$output_dir/npm-package/bin" "$output_dir/npm-package/lib" "$output_dir/npm-package/shell"

install -m 0755 "$repo_dir/npm/bin/tmh.mjs" "$output_dir/npm-package/bin/tmh.mjs"
install -m 0755 "$repo_dir/npm/bin/tmha.mjs" "$output_dir/npm-package/bin/tmha.mjs"
install -m 0644 "$repo_dir/npm/lib/launcher.mjs" "$output_dir/npm-package/lib/launcher.mjs"
install -m 0644 "$repo_dir/npm/package.json" "$output_dir/npm-package/package.json"
install -m 0644 "$repo_dir/README.md" "$output_dir/npm-package/README.md"
install -m 0644 "$repo_dir/README.zh-CN.md" "$output_dir/npm-package/README.zh-CN.md"
install -m 0644 "$repo_dir/LICENSE" "$output_dir/npm-package/LICENSE"
install -m 0644 "$repo_dir/THIRD_PARTY_NOTICES.md" "$output_dir/npm-package/THIRD_PARTY_NOTICES.md"
install -m 0644 "$repo_dir/shell/tmh.zsh" "$output_dir/npm-package/shell/tmh.zsh"

for platform in darwin_amd64 darwin_arm64 linux_amd64 linux_arm64; do
  os="${platform%_*}"
  arch="${platform#*_}"
  extract_dir="$output_dir/extract-$platform"
  mkdir -p "$extract_dir" "$output_dir/npm-package/vendor/$os-$arch"
  tar -xzf "$assets_dir/tmh_${platform}.tar.gz" -C "$extract_dir"
  [ -x "$extract_dir/tmh" ] || { printf 'archive does not contain executable tmh: %s\n' "$platform" >&2; exit 1; }
  install -m 0755 "$extract_dir/tmh" "$output_dir/npm-package/vendor/$os-$arch/tmh"
done

bare_version="${version#v}"
node - "$output_dir/npm-package/package.json" "$bare_version" <<'EOF'
import fs from "node:fs";

const [file, version] = process.argv.slice(2);
const pkg = JSON.parse(fs.readFileSync(file, "utf8"));
pkg.version = version;
fs.writeFileSync(file, `${JSON.stringify(pkg, null, 2)}\n`);
EOF

(
  cd "$output_dir/npm-package"
  npm pack --pack-destination "$output_dir" >/dev/null
)

"$repo_dir/scripts/render-homebrew-formula.sh" "$version" "$assets_dir/checksums.txt" > "$output_dir/tmh.rb"
ruby -c "$output_dir/tmh.rb" >/dev/null

printf '%s\n' "$output_dir/tmh-$bare_version.tgz"
printf '%s\n' "$output_dir/tmh.rb"
