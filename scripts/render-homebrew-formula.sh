#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
  printf 'usage: %s <vMAJOR.MINOR.PATCH> <checksums.txt>\n' "$0" >&2
  exit 2
fi

version="$1"
checksums="$2"

if ! printf '%s\n' "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
  printf 'invalid release version: %s\n' "$version" >&2
  exit 2
fi

[ -f "$checksums" ] || { printf 'checksums file not found: %s\n' "$checksums" >&2; exit 1; }

checksum() {
  archive="$1"
  value="$(awk -v archive="$archive" '$2 == archive { print $1 }' "$checksums")"
  [ -n "$value" ] || { printf 'checksum not found for %s\n' "$archive" >&2; exit 1; }
  printf '%s' "$value"
}

darwin_amd64="$(checksum tmh_darwin_amd64.tar.gz)"
darwin_arm64="$(checksum tmh_darwin_arm64.tar.gz)"
linux_amd64="$(checksum tmh_linux_amd64.tar.gz)"
linux_arm64="$(checksum tmh_linux_arm64.tar.gz)"
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
      url "$release_url/tmh_darwin_arm64.tar.gz"
      sha256 "$darwin_arm64"
    else
      url "$release_url/tmh_darwin_amd64.tar.gz"
      sha256 "$darwin_amd64"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "$release_url/tmh_linux_arm64.tar.gz"
      sha256 "$linux_arm64"
    else
      url "$release_url/tmh_linux_amd64.tar.gz"
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
