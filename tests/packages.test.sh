#!/bin/sh
set -eu

repo_dir="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

version="v1.2.3"
assets_dir="$tmp_dir/assets"
fixture_dir="$tmp_dir/fixture"
output_dir="$tmp_dir/output"
second_output_dir="$tmp_dir/output-second"
install_dir="$tmp_dir/install"
mkdir -p "$assets_dir" "$fixture_dir"

if "$repo_dir/scripts/render-homebrew-formula.sh" v1.2.3-rc1 "$tmp_dir/missing-checksums" >/dev/null 2>&1; then
  printf 'prerelease version unexpectedly passed validation\n' >&2
  exit 1
fi

cat > "$tmp_dir/fixture.go" <<'EOF'
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Println("1.2.3")
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--help" {
		fmt.Println("usage")
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--tmh-invalid-option" {
		os.Exit(2)
	}
	if len(os.Args) > 1 && os.Args[1] == "exit7" {
		os.Exit(7)
	}
	fmt.Printf("command=%s args=%s\n", filepath.Base(os.Args[0]), strings.Join(os.Args[1:], ","))
}
EOF

go build -o "$fixture_dir/tmh" "$tmp_dir/fixture.go"
install -m 0644 "$repo_dir/LICENSE" "$fixture_dir/LICENSE"
install -m 0644 "$repo_dir/THIRD_PARTY_NOTICES.md" "$fixture_dir/THIRD_PARTY_NOTICES.md"
install -m 0644 "$repo_dir/README.md" "$fixture_dir/README.md"
install -m 0644 "$repo_dir/README.zh-CN.md" "$fixture_dir/README.zh-CN.md"
install -m 0644 "$repo_dir/shell/tmh.zsh" "$fixture_dir/tmh.zsh"
install -m 0644 "$repo_dir/examples/config.toml" "$fixture_dir/config.example.toml"

for platform in darwin_amd64 darwin_arm64 linux_amd64 linux_arm64; do
  tar -czf "$assets_dir/tmh_${platform}.tar.gz" -C "$fixture_dir" .
done

(
  cd "$assets_dir"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum tmh_*.tar.gz > checksums.txt
  else
    shasum -a 256 tmh_*.tar.gz > checksums.txt
  fi
)

"$repo_dir/scripts/prepare-release-packages.sh" "$version" "$assets_dir" "$output_dir" >/dev/null
(
  cd "$tmp_dir"
  "$repo_dir/scripts/prepare-release-packages.sh" "$version" assets "$(basename "$second_output_dir")" >/dev/null
)

test -f "$output_dir/allenreder-tmh-1.2.3.tgz"
test -f "$output_dir/tmh.rb"
cmp -s "$output_dir/allenreder-tmh-1.2.3.tgz" "$second_output_dir/allenreder-tmh-1.2.3.tgz"
cmp -s "$output_dir/tmh.rb" "$second_output_dir/tmh.rb"
ruby -c "$output_dir/tmh.rb" >/dev/null

for archive in tmh_darwin_amd64.tar.gz tmh_darwin_arm64.tar.gz tmh_linux_amd64.tar.gz tmh_linux_arm64.tar.gz; do
  hash="$(awk -v archive="$archive" '$2 == archive { print $1 }' "$assets_dir/checksums.txt")"
  grep -Fq "$hash" "$output_dir/tmh.rb"
  grep -Fq "https://github.com/AllenReder/tmh/releases/download/$version/$archive" "$output_dir/tmh.rb"
done

tar -tzf "$output_dir/allenreder-tmh-1.2.3.tgz" | LC_ALL=C sort > "$tmp_dir/package-files.actual"
cat > "$tmp_dir/package-files.expected" <<'EOF'
package/LICENSE
package/README.md
package/README.zh-CN.md
package/THIRD_PARTY_NOTICES.md
package/bin/tmh.mjs
package/bin/tmha.mjs
package/lib/launcher.mjs
package/package.json
package/shell/tmh.zsh
package/vendor/darwin-amd64/tmh
package/vendor/darwin-arm64/tmh
package/vendor/linux-amd64/tmh
package/vendor/linux-arm64/tmh
EOF
LC_ALL=C sort -o "$tmp_dir/package-files.expected" "$tmp_dir/package-files.expected"
diff -u "$tmp_dir/package-files.expected" "$tmp_dir/package-files.actual"

npm install --ignore-scripts --no-audit --no-fund --prefix "$install_dir" "$output_dir/allenreder-tmh-1.2.3.tgz" >/dev/null
test "$("$install_dir/node_modules/.bin/tmh" --version)" = "1.2.3"
test "$("$install_dir/node_modules/.bin/tmha" --version)" = "1.2.3"
test "$("$install_dir/node_modules/.bin/tmh" one two)" = "command=tmh args=one,two"
test "$("$install_dir/node_modules/.bin/tmha" one two)" = "command=tmha args=one,two"

set +e
"$install_dir/node_modules/.bin/tmh" exit7
status="$?"
set -e
test "$status" -eq 7

printf 'Package distribution tests passed.\n'
