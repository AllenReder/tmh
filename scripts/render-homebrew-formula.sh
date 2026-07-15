#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
  printf 'usage: %s <vMAJOR.MINOR.PATCH> <checksums.txt>\n' "$0" >&2
  exit 2
fi

version="$1"
checksums="$2"
repo_dir="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
. "$repo_dir/scripts/release-lib.sh"

version="$(release_normalize_version "$version")"

[ -f "$checksums" ] || { printf 'checksums file not found: %s\n' "$checksums" >&2; exit 1; }

checksum() {
  archive="$(release_archive_name "$1")"
  release_checksum_for "$checksums" "$archive"
}

darwin_amd64_archive="$(release_archive_name darwin_amd64)"
darwin_arm64_archive="$(release_archive_name darwin_arm64)"
linux_amd64_archive="$(release_archive_name linux_amd64)"
linux_arm64_archive="$(release_archive_name linux_arm64)"
darwin_amd64="$(checksum darwin_amd64)"
darwin_arm64="$(checksum darwin_arm64)"
linux_amd64="$(checksum linux_amd64)"
linux_arm64="$(checksum linux_arm64)"
bare_version="${version#v}"
release_url="https://github.com/AllenReder/tmh/releases/download/$version"

cat <<EOF
class Tmh < Formula
  desc "Turn natural language into reviewable terminal commands"
  homepage "https://github.com/AllenReder/tmh"
  version "$bare_version"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "$release_url/$darwin_arm64_archive"
      sha256 "$darwin_arm64"
    else
      url "$release_url/$darwin_amd64_archive"
      sha256 "$darwin_amd64"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "$release_url/$linux_arm64_archive"
      sha256 "$linux_arm64"
    else
      url "$release_url/$linux_amd64_archive"
      sha256 "$linux_amd64"
    end
  end

  def install
    bin.install "tmh"
    bin.install_symlink "tmh" => "tmha"
    pkgshare.install "tmh.zsh", "LICENSE", "THIRD_PARTY_NOTICES.md", "README.md", "README.zh-CN.md"
  end

  def caveats
    <<~EOS
      To enable Zsh command insertion, add this line to ~/.zshrc:
        source "#{pkgshare}/tmh.zsh"
    EOS
  end

  test do
    assert_equal version.to_s, shell_output("#{bin}/tmh --version").strip
    assert_equal version.to_s, shell_output("#{bin}/tmha --version").strip
  end
end
EOF
