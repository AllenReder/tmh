# tmh Zsh integration: keep model output reviewable by placing it in BUFFER.

_tmh_should_delegate() {
  (( $# > 0 )) || return 1
  case "$1" in
    config|help|version|-h|--help|--version)
      return 0
      ;;
  esac
  return 1
}

tmh() {
  if _tmh_should_delegate "$@"; then
    command tmh "$@"
    return $?
  fi

  local generated rc
  generated="$(command tmh "$@")"
  rc=$?
  (( rc == 0 )) || return "$rc"
  [[ -n "$generated" ]] || return 1
  print -z -- "$generated"
}

tmha() {
  if _tmh_should_delegate "$@"; then
    if [[ "${1:-}" == "config" ]]; then
      command tmh "$@"
    else
      command tmh --agent "$@"
    fi
    return $?
  fi

  local generated rc
  generated="$(command tmh --agent "$@")"
  rc=$?
  (( rc == 0 )) || return "$rc"
  [[ -n "$generated" ]] || return 1
  print -z -- "$generated"
}
