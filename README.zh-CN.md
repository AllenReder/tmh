<div align="center">

# tmh — tell me how

将自然语言转换为一条可执行的终端命令。

[![CI](https://github.com/AllenReder/tmh/actions/workflows/ci.yml/badge.svg)](https://github.com/AllenReder/tmh/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/AllenReder/tmh)](https://github.com/AllenReder/tmh/releases)
[![npm](https://img.shields.io/npm/v/%40allenreder%2Ftmh)](https://www.npmjs.com/package/@allenreder/tmh)
[![License](https://img.shields.io/github/license/AllenReder/tmh)](LICENSE)

[English](README.md) · [简体中文](README.zh-CN.md)

</div>

```text
$ tmh 找出当前目录下最大的十个文件
Explanation: 查找普通文件并按大小排序。

# 命令进入下一条输入缓冲区，此时尚未执行：
find . -type f -exec du -h {} + | sort -hr | head -n 10
```

## 特点

- 生成一条可编辑命令，永不自动执行。
- 支持 OpenAI-compatible Chat Completions 接口。
- 提供可选的只读 Agent 模式补充文件上下文。
- 在本地校验模型输出，并对高风险命令给出警告。
- stdout 只输出命令，解释和错误输出到 stderr。

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

安装器会把 `tmh` 和 `tmha` 放到 `~/.local/bin`，并在修改
`~/.zshrc` 启用 Zsh 集成前询问用户。

Homebrew 和 npm 不会修改 Shell 启动文件。要启用 Zsh 命令插入功能，
请根据安装方式把对应一行加入 `~/.zshrc`：

```zsh
# Homebrew
source "$(brew --prefix)/share/tmh/tmh.zsh"

# npm
source "$(npm root -g)/@allenreder/tmh/shell/tmh.zsh"
```

不启用可选的 Zsh 集成时，`tmh` 会把生成的命令输出到 stdout，仍然
不会自动执行命令。

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
tmh_timeout_seconds = 30
tmha_timeout_seconds = 90
```

接口需要认证时设置 `TMH_API_KEY`，也可以使用 `OPENAI_API_KEY`。

```sh
tmh config show
tmh config test
```

## 使用

### 普通模式

```zsh
tmh 找出今天修改过的文件
tmh 查看哪个进程占用了 8080 端口
printf '%s' '按占用空间从大到小排序' | tmh
```

### Agent 模式

当命令依赖本地文件时，Agent 可以列出目录并读取普通文本文件：

```zsh
tmh --agent 启动当前项目
tmha 运行这个仓库文档中定义的测试命令
tmha --allow-path /var/log 生成检查这些日志的命令
```

`tmha` 等价于 `tmh --agent`。默认只能访问当前目录，额外路径必须
显式授权。

### 常用参数

| 参数 | 作用 |
| --- | --- |
| `--agent` | 启用只读文件上下文。 |
| `--allow-path PATH` | 为 Agent 增加一个允许路径。 |
| `--base-url URL` | 临时覆盖接口地址。 |
| `--model MODEL` | 临时覆盖模型。 |
| `--timeout DURATION` | 临时覆盖请求超时。 |
| `--debug` | 输出脱敏诊断信息。 |

## 安全

- 生成的命令永远不会自动执行。
- Agent 不能执行程序、写入文件或访问网络。
- 敏感、二进制、超大和越界文件会被拒绝。
- 模型输出必须是一条通过校验的 Zsh 命令。
- 默认不持久化提示词和工具结果。

请求、基础运行环境和 Agent 文件结果会发送到所配置的模型接口。
请勿输入秘密信息，并始终检查生成的命令。

## 当前支持

macOS、Linux · amd64、arm64 · Zsh · OpenAI-compatible Chat Completions

## 开发

```sh
make build
make test
make check
```

欢迎提交范围明确的 Issue 和 Pull Request。安全漏洞请通过
[GitHub 私有安全公告](https://github.com/AllenReder/tmh/security/advisories/new)报告。

## 许可证

[MIT](LICENSE)。捆绑依赖的许可证信息见
[第三方声明](THIRD_PARTY_NOTICES.md)。
