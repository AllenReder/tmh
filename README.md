<div align="center">

# tmh — tell me how

Turn natural language into a terminal command you can review before running.

[![CI](https://github.com/AllenReder/tmh/actions/workflows/ci.yml/badge.svg)](https://github.com/AllenReder/tmh/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/AllenReder/tmh)](https://github.com/AllenReder/tmh/releases)
[![npm](https://img.shields.io/npm/v/tmh)](https://www.npmjs.com/package/tmh)
[![License](https://img.shields.io/github/license/AllenReder/tmh)](LICENSE)

[English](README.md) · [简体中文](README.zh-CN.md)

</div>

```text
$ tmh find the ten largest files in this directory
Explanation: Finds regular files and sorts them by size.

# Inserted into the next prompt, not executed:
find . -type f -exec du -h {} + | sort -hr | head -n 10
```

## Highlights

- Generates one editable command and never executes it.
- Supports OpenAI-compatible Chat Completions endpoints.
- Offers an optional read-only Agent mode for file context.
- Validates output locally and warns about risky commands.
- Keeps command output on stdout and explanations on stderr.

## Install

Homebrew is the recommended installation method on macOS and Linux:

```sh
brew install AllenReder/tap/tmh
```

If you already use Node.js 22 or newer:

```sh
npm install -g tmh
```

You can also use the standalone installer:

```sh
curl -fsSL https://raw.githubusercontent.com/AllenReder/tmh/main/install.sh | sh
```

The installer places `tmh` and `tmha` in `~/.local/bin` and asks before
enabling Zsh integration in `~/.zshrc`.

Homebrew and npm do not modify shell startup files. To enable Zsh command
insertion, add the line for your installation method to `~/.zshrc`:

```zsh
# Homebrew
source "$(brew --prefix)/share/tmh/tmh.zsh"

# npm
source "$(npm root -g)/tmh/shell/tmh.zsh"
```

Without the optional Zsh integration, `tmh` prints the generated command to
stdout and still never executes it automatically.

Build from source:

```sh
git clone https://github.com/AllenReder/tmh.git
cd tmh
make build
```

## Configure

Create `~/.config/tmh/config.toml`, or use `$XDG_CONFIG_HOME/tmh/config.toml`:

```toml
base_url = "https://api.openai.com/v1"
model = "your-model-name"
tmh_timeout_seconds = 30
tmha_timeout_seconds = 90
```

Set `TMH_API_KEY` when authentication is required. `OPENAI_API_KEY` is used as
a fallback.

```sh
tmh config show
tmh config test
```

## Use

### Direct mode

```zsh
tmh find files modified today
tmh show which process is using port 8080
printf '%s' 'sort disk usage from largest to smallest' | tmh
```

### Agent mode

Agent mode may list directories and read ordinary text files when the command
depends on local context:

```zsh
tmh --agent start this project
tmha run the test command documented by this repository
tmha --allow-path /var/log generate a command to inspect these logs
```

`tmha` is equivalent to `tmh --agent`. Access is limited to the current
directory unless another path is explicitly granted.

### Common flags

| Flag | Purpose |
| --- | --- |
| `--agent` | Enable read-only file context. |
| `--allow-path PATH` | Grant Agent mode an additional path. |
| `--base-url URL` | Override the endpoint. |
| `--model MODEL` | Override the model. |
| `--timeout DURATION` | Override the request timeout. |
| `--debug` | Print redacted diagnostics. |

## Safety

- Generated commands are never executed automatically.
- Agent mode cannot run programs, write files, or access the network.
- Sensitive, binary, oversized, and out-of-scope files are blocked.
- Model output must be a valid single-line Zsh command.
- Prompts and tool results are not persisted by default.

Requests, basic runtime context, and Agent file results are sent to the
configured model. Do not include secrets, and always review generated commands.

## Support

macOS and Linux · amd64 and arm64 · Zsh · OpenAI-compatible Chat Completions

## Develop

```sh
make build
make test
make check
```

Issues and focused pull requests are welcome. Report security vulnerabilities
through a [private GitHub advisory](https://github.com/AllenReder/tmh/security/advisories/new).

## License

[MIT](LICENSE). See [Third-Party Notices](THIRD_PARTY_NOTICES.md) for bundled
dependencies.
