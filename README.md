<div align="center">

# tmh — tell me how

Turn natural language into a terminal command you can review before running.

[![CI](https://github.com/AllenReder/tmh/actions/workflows/ci.yml/badge.svg)](https://github.com/AllenReder/tmh/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/AllenReder/tmh)](https://github.com/AllenReder/tmh/releases)
[![npm](https://img.shields.io/npm/v/%40allenreder%2Ftmh)](https://www.npmjs.com/package/@allenreder/tmh)
[![License](https://img.shields.io/github/license/AllenReder/tmh)](LICENSE)

[English](README.md) · [简体中文](README.zh-CN.md)

</div>

```text
$ tmh find the ten largest files in this directory
Explanation: Finds regular files and sorts them by size.

find . -type f -exec du -h {} + | sort -hr | head -n 10
```

The explanation is written to stderr. The single generated command is written
to stdout and is never executed by tmh.

## Highlights

- Generates one editable Zsh, Bash, or Fish command for review.
- Supports a direct generate mode and a bounded context-aware Agent mode.
- Keeps command-execution tools completely absent unless explicitly enabled
  for one Agent invocation.
- Validates output with the same target shell used for generation and warns
  about risky commands.
- Supports OpenAI-compatible Chat Completions endpoints.
- Keeps the final command on stdout and explanations, warnings, and audit
  events on stderr.

## Install

Homebrew is the recommended installation method on macOS and Linux:

```sh
brew install AllenReder/tap/tmh
```

If you already use Node.js 22 or newer:

```sh
npm install -g @allenreder/tmh
```

You can also use the standalone installer:

```sh
curl -fsSL https://raw.githubusercontent.com/AllenReder/tmh/main/install.sh | sh
```

To install a specific stable version:

```sh
curl -fsSL https://raw.githubusercontent.com/AllenReder/tmh/main/install.sh | TMH_VERSION=v0.2.0 sh
```

The standalone installer installs only `tmh` in `~/.local/bin`. It asks before
adding a managed Zsh, Bash, or Fish integration block. Control this behavior
non-interactively with
`TMH_INSTALL_SHELL=ask|none|auto|zsh|bash|fish`:

```sh
curl -fsSL https://raw.githubusercontent.com/AllenReder/tmh/main/install.sh |
  TMH_INSTALL_SHELL=fish sh
```

Homebrew and npm do not modify shell startup files.

### Shell integration

Add the matching line to your startup file:

```sh
# Zsh: ~/.zshrc
eval "$(tmh shell init zsh)"

# Bash: ~/.bashrc, or ~/.bash_profile on macOS
eval "$(tmh shell init bash)"

# Fish: ~/.config/fish/conf.d/tmh.fish
tmh shell init fish | source
```

The integration is embedded in the `tmh` binary; no separate shell script is
installed.

- `Ctrl-X Ctrl-G` replaces the current input buffer with a generated command.
- `Ctrl-X Ctrl-A` does the same through Agent mode.
- Existing bindings are preserved by default. Pass `--force-bind` to replace
  them or `--no-bind` to register the functions and widgets without bindings.
- Zsh also preserves the classic behavior where a normal `tmh ...` invocation
  places the generated command into the next prompt with `print -z`.
- Bash 4 or newer supports the readline widgets. macOS Bash 3.2 still supports
  generation, target-shell validation, and stdout output, but not buffer
  replacement widgets.

Without shell integration, every mode continues to print the command to
stdout.

Build from source:

```sh
git clone https://github.com/AllenReder/tmh.git
cd tmh
make build
```

## Configure

The standalone installer automatically creates `~/.config/tmh/config.toml`, or
`$XDG_CONFIG_HOME/tmh/config.toml`, when it is missing. Existing configuration
files and symlinks are never overwritten. For Homebrew, npm, or source installs,
create it manually from this template:

```toml
base_url = "https://api.openai.com/v1"
model = "your-model-name"
shell = "auto"
generate_timeout_seconds = 30
agent_timeout_seconds = 90
```

Set `TMH_API_KEY` when authentication is required. `OPENAI_API_KEY` is used as
a fallback.

```sh
tmh config show
tmh config test
```

Shell selection uses this precedence:

1. `--shell auto|zsh|bash|fish`
2. `TMH_SHELL`
3. `shell` in the config file
4. automatic detection from `$SHELL`

An unrecognized shell is an error; tmh never silently falls back to Zsh.
`tmh config show` reports the setting source and the concrete shell resolved
from `auto`. The selected executable must resolve from a standard system or
package-manager location, or from a supported per-user location such as
`~/.local/bin`, `~/bin`, asdf, mise, or a Nix profile. Arbitrary `PATH`
directories and links escaping those locations are rejected.

The v0.2 configuration is intentionally breaking. The legacy
`tmh_timeout_seconds` and `tmha_timeout_seconds` fields are rejected as unknown
fields.

## Use

### Generate

```sh
tmh find files modified today
tmh show which process is using port 8080
printf '%s' 'sort disk usage from largest to smallest' | tmh
```

`tmh <request>` is the shortest form. `tmh generate <request>` is the explicit
form and is useful when the request begins with a reserved subcommand:

```sh
tmh generate config files under this directory
```

### Agent

Agent mode can list directories and read bounded ordinary text files when
local context is needed:

```sh
tmh agent start this project
tmh agent --allow-path /var/log generate a command to inspect these logs
```

Access is limited to the current directory unless another path is explicitly
granted. The v0.1 `tmha` executable and `tmh --agent` flag were removed; use
`tmh agent`.

### Inspection command tool

The `run_command` tool is not sent to the model by default. Enable it for one
Agent invocation only:

```sh
tmh agent --exec=inspection determine which files changed and suggest the relevant test command
```

Inspection mode may automatically run only policy-approved, read-only
`git status`, `git diff`, `git log`, `git show`, `git rev-parse`,
`git ls-files`, and `rg` operations. Requests are structured as a program plus
an argument array and are executed directly without a shell. Unknown programs,
subcommands, flags, scripts, pipelines, redirections, interpreters, network
access, and mutation are denied before process launch.

`--allow-path` extends the readable roots for both file tools and inspection
commands. The final generated command is still returned for review and is
never executed.

### Common flags

| Flag | Purpose |
| --- | --- |
| `--shell auto|zsh|bash|fish` | Select the generated command language. |
| `--allow-path PATH` | In Agent mode, add a readable root; repeatable. |
| `--exec=inspection` | In Agent mode, expose the sandboxed read-only command tool for this invocation. |
| `--base-url URL` | Override the configured endpoint. |
| `--model MODEL` | Override the configured model. |
| `--timeout DURATION` | Override the request timeout. |
| `--debug` | Print redacted diagnostics to stderr. |

## Safety

- The final generated command is never executed by tmh.
- File and command tools are scoped, budgeted, and treat their results as
  untrusted data rather than instructions.
- Sensitive paths, binary files, oversized reads, symlink escapes, and paths
  outside the allowed roots are blocked.
- Inspection execution is opt-in per invocation, limited to allowlisted
  `git`/`rg` forms, and has no stdin or TTY.
- Child environments omit API keys, credentials, proxy settings, authentication
  sockets, dynamic-loader settings, pagers, and user Git configuration.
- Command output is bounded, drained to avoid deadlocks, stripped of terminal
  control sequences, normalized to UTF-8, and redacted before it is sent to
  the model.
- Model turns, tool calls, command count, execution time, and output bytes all
  have fixed limits.
- Model output must be one valid physical line in the resolved target shell.
  Risk warnings remain advisory because the user decides whether to run it.

### Inspection sandbox and threat model

Inspection execution fails closed if its platform sandbox cannot be proven:

- On Linux it requires Landlock ABI v3 or newer, including truncate mediation,
  plus seccomp-BPF network syscall denial. Kernels without that support may
  still use normal generate and file-only Agent modes, but cannot use
  `--exec=inspection`.
- On macOS it uses the operating system's deprecated `/usr/bin/sandbox-exec`
  Seatbelt interface with a deny-default, read-only, no-network profile.
  If the executable is absent or its canary fails, inspection execution is
  unavailable.

This boundary is designed to prevent model mistakes and common prompt
injection from turning inspection into mutation or data exfiltration. It is
not a defense against a malicious local process running concurrently with the
same user privileges. Review generated commands and protect the host account
accordingly.

Requests, basic runtime context, and the Agent context selected by tools are
sent to the configured model endpoint. Do not include secrets.

## Support

- macOS and Linux on amd64 and arm64.
- Zsh, Bash 3.2 or newer, and Fish 3.6 or newer.
- Bash buffer widgets require Bash 4 or newer.
- Inspection execution additionally requires a working platform sandbox as
  described above.
- Windows, PowerShell, and terminal-emulator-specific adapters are not
  supported.

## Develop

```sh
make build
make test
make check
```

`make check` covers Go tests and static analysis, Zsh/Bash/Fish integration
syntax, installer and release checks, package verification, vulnerability
scanning, and workflow validation. CI installs Fish on both macOS and Linux so
the Fish checks do not take the optional local-skip path.

Issues and focused pull requests are welcome. Report security vulnerabilities
through a [private GitHub advisory](https://github.com/AllenReder/tmh/security/advisories/new).

## License

[MIT](LICENSE). See [Third-Party Notices](THIRD_PARTY_NOTICES.md) for bundled
dependencies.
