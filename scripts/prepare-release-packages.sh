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
. "$repo_dir/scripts/release-lib.sh"
source_dir="${TMH_RELEASE_SOURCE_DIR:-$repo_dir}"

version="$(release_normalize_version "$version")"

[ -d "$assets_dir" ] || { printf 'release assets directory not found: %s\n' "$assets_dir" >&2; exit 1; }
[ -d "$source_dir/npm" ] || { printf 'release source directory not found: %s\n' "$source_dir" >&2; exit 1; }
[ -n "$output_dir" ] && [ "$output_dir" != "/" ] || { printf 'unsafe output directory\n' >&2; exit 2; }

assets_dir="$(CDPATH= cd -- "$assets_dir" && pwd)"
source_dir="$(CDPATH= cd -- "$source_dir" && pwd)"
output_parent="$(dirname "$output_dir")"
output_name="$(basename "$output_dir")"
mkdir -p "$output_parent"
output_parent="$(CDPATH= cd -- "$output_parent" && pwd)"
output_dir="$output_parent/$output_name"
[ "$output_dir" != "$repo_dir" ] && [ "$output_dir" != "$source_dir" ] && [ "$output_dir" != "$assets_dir" ] || {
  printf 'unsafe output directory: %s\n' "$output_dir" >&2
  exit 2
}

release_require_commands install node npm ruby tar
if [ "${TMH_VERIFY_SNAPSHOT:-0}" = "1" ]; then
  "$repo_dir/scripts/verify-release-assets.sh" "$assets_dir" >/dev/null
else
  "$repo_dir/scripts/verify-release-assets.sh" "$assets_dir" "$version" >/dev/null
fi

rm -rf -- "$output_dir"
mkdir -p "$output_dir/npm-package/bin" "$output_dir/npm-package/lib"

install -m 0755 "$source_dir/npm/bin/tmh.mjs" "$output_dir/npm-package/bin/tmh.mjs"
install -m 0644 "$source_dir/npm/lib/launcher.mjs" "$output_dir/npm-package/lib/launcher.mjs"
install -m 0644 "$source_dir/npm/package.json" "$output_dir/npm-package/package.json"
install -m 0644 "$source_dir/README.md" "$output_dir/npm-package/README.md"
install -m 0644 "$source_dir/README.zh-CN.md" "$output_dir/npm-package/README.zh-CN.md"
install -m 0644 "$source_dir/LICENSE" "$output_dir/npm-package/LICENSE"
install -m 0644 "$source_dir/THIRD_PARTY_NOTICES.md" "$output_dir/npm-package/THIRD_PARTY_NOTICES.md"

for platform in $(release_platforms); do
  os="${platform%_*}"
  arch="${platform#*_}"
  archive="$(release_archive_name "$platform")"
  extract_dir="$output_dir/extract-$platform"
  mkdir -p "$extract_dir" "$output_dir/npm-package/vendor/$os-$arch"
  tar -xzf "$assets_dir/$archive" -C "$extract_dir"
  [ -x "$extract_dir/tmh" ] || { printf 'archive does not contain executable tmh: %s\n' "$platform" >&2; exit 1; }
  install -m 0755 "$extract_dir/tmh" "$output_dir/npm-package/vendor/$os-$arch/tmh"
done

bare_version="${version#v}"
package_archive="allenreder-tmh-$bare_version.tgz"
node - "$output_dir/npm-package/package.json" "$bare_version" <<'EOF'
import fs from "node:fs";

const [file, version] = process.argv.slice(2);
const pkg = JSON.parse(fs.readFileSync(file, "utf8"));
pkg.version = version;
fs.writeFileSync(file, `${JSON.stringify(pkg, null, 2)}\n`);
EOF

(
  cd "$output_dir/npm-package"
  npm pack --ignore-scripts --pack-destination "$output_dir" >/dev/null
)
[ -f "$output_dir/$package_archive" ] || { printf 'npm package was not created: %s\n' "$package_archive" >&2; exit 1; }

"$repo_dir/scripts/render-homebrew-formula.sh" "$version" "$assets_dir/checksums.txt" > "$output_dir/tmh.rb"
ruby -c "$output_dir/tmh.rb" >/dev/null

printf '%s\n' "$output_dir/$package_archive"
printf '%s\n' "$output_dir/tmh.rb"
