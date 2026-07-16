<div align="center">

# tmh — tell me how

将自然语言转换为一条可检查、再决定是否运行的终端命令。

[![CI](https://github.com/AllenReder/tmh/actions/workflows/ci.yml/badge.svg)](https://github.com/AllenReder/tmh/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/AllenReder/tmh)](https://github.com/AllenReder/tmh/releases)
[![npm](https://img.shields.io/npm/v/%40allenreder%2Ftmh)](https://www.npmjs.com/package/@allenreder/tmh)
[![License](https://img.shields.io/github/license/AllenReder/tmh)](LICENSE)

[English](README.md) · [简体中文](README.zh-CN.md)

</div>

```text
$ tmh 找出当前目录下最大的十个文件
Explanation: 查找普通文件并按大小排序。

find . -type f -exec du -h {} + | sort -hr | head -n 10
```

解释写入 stderr，stdout 只包含最终生成的一条命令；tmh 不会执行这条
最终命令。

## 特点

- 生成一条可编辑、可检查的 Zsh、Bash 或 Fish 命令。
- 同时提供快速生成模式和有明确边界的上下文 Agent 模式。
- 除非用户对本次 Agent 调用显式授权，否则模型完全看不到命令执行
  Tool。
- 使用与生成目标相同的 Shell 在本地校验语法，并提示高风险行为。
- 支持 OpenAI-compatible Chat Completions 接口。
- stdout 只输出最终命令，解释、警告和审计事件写入 stderr。

## 安装

macOS 和 Linux 推荐使用 Homebrew：

```sh
brew install AllenReder/tap/tmh
```

如果已经安装 Node.js 22 或更高版本，也可以使用 npm：

```sh
npm install -g @allenreder/tmh
```

也可以使用独立安装脚本：

```sh
curl -fsSL https://raw.githubusercontent.com/AllenReder/tmh/main/install.sh | sh
```

安装指定的稳定版本：

```sh
curl -fsSL https://raw.githubusercontent.com/AllenReder/tmh/main/install.sh | TMH_VERSION=v0.2.0 sh
```

独立安装器只会把 `tmh` 安装到 `~/.local/bin`。写入受管理的 Zsh、
Bash 或 Fish 集成配置前，默认会先询问用户。非交互场景可使用
`TMH_INSTALL_SHELL=ask|none|auto|zsh|bash|fish`：

```sh
curl -fsSL https://raw.githubusercontent.com/AllenReder/tmh/main/install.sh |
  TMH_INSTALL_SHELL=fish sh
```

Homebrew 和 npm 不会修改 Shell 启动文件。

### Shell 集成

在对应的启动文件中加入：

```sh
# Zsh：~/.zshrc
eval "$(tmh shell init zsh)"

# Bash：~/.bashrc；macOS 通常为 ~/.bash_profile
eval "$(tmh shell init bash)"

# Fish：~/.config/fish/conf.d/tmh.fish
tmh shell init fish | source
```

集成脚本已嵌入 `tmh` 二进制，不再安装独立 Shell 脚本。

- `Ctrl-X Ctrl-G`：把当前输入缓冲区替换为普通模式生成的命令。
- `Ctrl-X Ctrl-A`：通过 Agent 模式完成相同操作。
- 默认保留已有快捷键；`--force-bind` 会显式覆盖，
  `--no-bind` 只注册函数和 Widget，不绑定快捷键。
- Zsh 继续支持普通 `tmh ...` 回车后通过 `print -z` 把结果放入下一条
  提示符。
- Bash 4 及以上支持 readline Widget。macOS 自带的 Bash 3.2 仍支持
  命令生成、目标 Shell 校验和 stdout 输出，但不支持替换输入缓冲区。

不启用 Shell 集成时，所有模式仍会把最终命令输出到 stdout。

从源码构建：

```sh
git clone https://github.com/AllenReder/tmh.git
cd tmh
make build
```

## 配置

创建 `~/.config/tmh/config.toml`，或者使用
`$XDG_CONFIG_HOME/tmh/config.toml`：

```toml
base_url = "https://api.openai.com/v1"
model = "your-model-name"
shell = "auto"
generate_timeout_seconds = 30
agent_timeout_seconds = 90
```

接口需要认证时设置 `TMH_API_KEY`；未设置时会使用
`OPENAI_API_KEY`。

```sh
tmh config show
tmh config test
```

目标 Shell 的选择优先级为：

1. `--shell auto|zsh|bash|fish`
2. `TMH_SHELL`
3. 配置文件中的 `shell`
4. 根据 `$SHELL` 自动检测

无法识别时会明确报错，不会静默回退到 Zsh。`tmh config show` 会显示
配置来源，以及 `auto` 最终解析出的具体 Shell。Shell 可执行文件必须来自
标准系统或包管理器路径，或 `~/.local/bin`、`~/bin`、asdf、mise、Nix
profile 等受支持的用户路径；任意 `PATH` 目录及逃逸这些目录的符号链接会被
拒绝。

v0.2 的配置是有意设计的破坏性变更。旧字段
`tmh_timeout_seconds` 和 `tmha_timeout_seconds` 会作为未知字段被拒绝。

## 使用

### 生成命令

```sh
tmh 找出今天修改过的文件
tmh 查看哪个进程占用了 8080 端口
printf '%s' '按占用空间从大到小排序' | tmh
```

`tmh <请求>` 是最短入口。请求以保留子命令开头时，使用显式入口
`tmh generate <请求>`：

```sh
tmh generate 找出当前目录下的 config 文件
```

### Agent 模式

命令依赖本地上下文时，Agent 可以列出目录并在预算范围内读取普通
文本文件：

```sh
tmh agent 启动当前项目
tmh agent --allow-path /var/log 生成检查这些日志的命令
```

默认只能访问当前目录；额外路径必须显式授权。v0.1 的 `tmha` 可执行
文件和 `tmh --agent` 参数已经删除，请统一使用 `tmh agent`。

### Inspection 命令 Tool

默认不会向模型提供 `run_command`。仅对当前这次 Agent 调用启用：

```sh
tmh agent --exec=inspection 判断哪些文件发生了变化，并建议相关测试命令
```

Inspection 模式只能自动运行通过策略检查的只读
`git status`、`git diff`、`git log`、`git show`、`git rev-parse`、
`git ls-files` 和 `rg` 操作。请求使用“程序 + 参数数组”的结构化形式，
直接执行，不经过 Shell。未知程序、子命令、参数、脚本、管道、重定向、
解释器、网络访问和任何修改行为都会在启动进程前被拒绝。

`--allow-path` 会同时扩展文件 Tool 和 Inspection 命令的可读根目录。
Agent 最终生成的命令仍只会返回给用户检查，不会执行。

### 常用参数

| 参数 | 作用 |
| --- | --- |
| `--shell auto|zsh|bash|fish` | 选择最终命令使用的 Shell。 |
| `--allow-path PATH` | 仅在 Agent 模式增加可读根目录，可重复使用。 |
| `--exec=inspection` | 仅在 Agent 模式为本次调用提供沙箱化的只读命令 Tool。 |
| `--base-url URL` | 临时覆盖接口地址。 |
| `--model MODEL` | 临时覆盖模型。 |
| `--timeout DURATION` | 临时覆盖请求超时。 |
| `--debug` | 向 stderr 输出脱敏诊断信息。 |

## 安全

- tmh 永远不会执行最终生成的命令。
- 文件和命令 Tool 都受路径与预算约束，并把结果视为不可信数据，而非
  新指令。
- 敏感路径、二进制文件、超大读取、符号链接越界和授权根目录外路径
  会被拒绝。
- Inspection 必须逐次显式启用，只允许白名单内的 `git`/`rg` 形式，
  不提供 stdin 或 TTY。
- 子进程环境会移除 API Key、凭据、代理设置、认证 socket、动态加载
  设置、pager 和用户 Git 配置。
- 命令输出有大小限制；即使截断仍会持续 drain 以避免死锁，随后清理
  终端控制序列、修复 UTF-8 并脱敏，再发送给模型。
- 模型轮次、Tool 次数、命令次数、执行时间和输出总量均有固定上限。
- 模型输出必须是目标 Shell 中一条合法的物理单行命令。风险提示是
  辅助信息，是否运行始终由用户决定。

### Inspection 沙箱与威胁模型

无法证明平台沙箱有效时，Inspection 会 fail closed：

- Linux 要求 Landlock ABI v3 或更高版本，以覆盖 truncate 等写入行为，
  并使用 seccomp-BPF 拒绝网络相关系统调用。不满足要求时，普通生成和
  仅文件 Tool 的 Agent 模式仍可使用，但 `--exec=inspection` 不可用。
- macOS 使用操作系统中已被标记为 deprecated 的
  `/usr/bin/sandbox-exec` Seatbelt 接口，应用 deny-default、只读且
  无网络的 profile。该程序缺失或 canary 验证失败时，Inspection 不会
  降级运行。

这个边界用于阻止模型误操作和常见 Prompt Injection 把“检查”升级为
修改或数据外传；它不用于抵抗以同一用户权限并发运行的恶意本地进程。
请保护主机账户，并始终检查最终生成的命令。

请求、基础运行环境以及 Tool 选取的 Agent 上下文会发送给配置的模型
接口，请勿输入秘密信息。

## 当前支持

- macOS、Linux；amd64、arm64。
- Zsh、Bash 3.2 及以上、Fish 3.6 及以上。
- Bash 输入缓冲区 Widget 要求 Bash 4 及以上。
- Inspection 还要求上述平台沙箱可正常使用。
- 暂不支持 Windows、PowerShell 和终端模拟器专用适配器。

## 开发

```sh
make build
make test
make check
```

`make check` 覆盖 Go 测试与静态检查、Zsh/Bash/Fish 集成脚本语法、
安装与发布流程、包验证、漏洞扫描和 workflow 校验。CI 会在 macOS 和
Linux 上安装 Fish，确保 Fish 检查不会走本地可选跳过分支。

欢迎提交范围明确的 Issue 和 Pull Request。安全漏洞请通过
[GitHub 私有安全公告](https://github.com/AllenReder/tmh/security/advisories/new)报告。

## 许可证

[MIT](LICENSE)。捆绑依赖的许可证信息见
[第三方声明](THIRD_PARTY_NOTICES.md)。
