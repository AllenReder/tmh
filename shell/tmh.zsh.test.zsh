#!/usr/bin/env zsh
set -eu

repo_root="${0:A:h:h}"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

cat > "$tmp_dir/tmh" <<'FAKE'
#!/bin/sh
case "$*" in
  "config show"|"help"|"version"|"--help"|"--version"|"--agent help"|"--agent version"|"--agent --help"|"--agent --version")
    printf 'delegated:%s\n' "$*"
    exit 0
    ;;
esac
printf 'fake explanation\n' >&2
printf 'echo generated\n'
FAKE
chmod +x "$tmp_dir/tmh"
PATH="$tmp_dir:$PATH"

source "$repo_root/shell/tmh.zsh"

[[ "$(whence -w tmh)" == "tmh: function" ]]
[[ "$(whence -w tmha)" == "tmha: function" ]]
[[ "$(tmh config show)" == "delegated:config show" ]]
[[ "$(tmha config show)" == "delegated:config show" ]]
[[ "$(tmh help)" == "delegated:help" ]]
[[ "$(tmh version)" == "delegated:version" ]]
[[ "$(tmha --help)" == "delegated:--agent --help" ]]

tmh generate something
read -z queued
[[ "$queued" == "echo generated" ]]

tmha inspect something
read -z queued_agent
[[ "$queued_agent" == "echo generated" ]]
