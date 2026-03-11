# agentctl

多 AI 编程 Agent 的统一管控平面。agentctl 将 MCP Server、Rules、Hooks、Commands、Ignore、Skills 集中管理，然后按各 Agent 的原生配置格式分发到 Claude Code、Gemini CLI、Codex 等工具。像 `terraform apply` 一样管理你的 AI 工具链。

[English](README.md)

## 安装

```bash
brew tap liangquanzhou/tap
brew install agentctl
```

或从源码构建：

```bash
git clone https://github.com/liangquanzhou/agentctl.git
cd agentctl
make install   # 安装到 ~/.local/bin/
```

## 快速开始

```bash
# 校验配置
agentctl validate

# 预览变更（dry-run）
agentctl mcp plan

# 一键应用所有配置
agentctl apply

# 查看系统状态
agentctl status
```

## 命令一览

| 命令 | 说明 |
|------|------|
| `apply` | 一键应用所有子系统 |
| `status` | 查看所有子系统状态 |
| `validate` | 校验配置文件 |
| `doctor` | 健康检查（secrets、依赖等） |
| `rollback` | 回滚上次 apply |
| `mcp list\|add\|rm\|plan\|apply\|status` | MCP Server 管理 |
| `skills sync\|list\|status\|pull` | Skills 同步 |
| `rules\|hooks\|commands\|ignore` | 按类型管理内容 |
| `drift` | 检测 MCP 配置漂移 |
| `reconcile` | 修复 MCP 漂移 |
| `runs` | 查看 apply 历史 |

## 支持的 Agent

| Agent | 配置格式 | MCP 配置路径 |
|-------|---------|-------------|
| **Claude Code** | JSON | `~/.claude.json` |
| **Codex** | TOML | `~/.codex/config.toml` |
| **Gemini CLI** | JSON | `~/.gemini/settings.json` |
| **Antigravity** | JSON | `~/.gemini/antigravity/mcp_config.json` |
| **OpenCode** | JSON | `~/.config/opencode/opencode.json` |

可通过 `~/.config/agentctl/agents/` 下的 TOML 文件添加自定义 Agent。

## 能力矩阵

| 能力 | Claude Code | Codex | Gemini CLI | Antigravity | OpenCode |
|------|:-----------:|:-----:|:----------:|:-----------:|:--------:|
| **MCP** | ✅ | ✅ | ✅ | ✅ | ✅ |
| **Rules** | ✅ | ✅ | ✅ | ✅ 共享 | ✅ |
| **Hooks** | ✅ events | ✅ notify | ✅ events | ✅ 共享 | ❌ ¹ |
| **Commands** | ✅ | ❌ ² | ✅ .toml | ❌ ² | ✅ |
| **Ignore** | ✅ | ❌ ³ | ❌ ⁴ | ❌ ⁴ | ❌ ⁵ |
| **Skills** | ✅ | ✅ | ✅ | ✅ | ✅ |

> **注释：**
> 1. OpenCode 通过 JS/TS plugin 系统实现 hooks，非声明式 JSON，agentctl 暂不适配
> 2. 该 Agent 原生不支持 custom commands 概念（Gemini CLI 已支持，通过 `format = "toml"` 自动转换 `.md` → `.toml`）
> 3. Codex 的 `.codexignore` 仍处于实验阶段，[行为不稳定](https://github.com/openai/codex/issues/6530)
> 4. Gemini CLI 的 `.geminiignore` 仅支持项目级，[全局支持尚未确认](https://github.com/google-gemini/gemini-cli/issues/4925)
> 5. OpenCode 复用 `.gitignore`，无独立 ignore 文件
>
> "共享"指 Antigravity 与 Gemini CLI 共用配置文件（`~/.gemini/GEMINI.md`、`~/.gemini/settings.json`），自动继承 rules 和 hooks。

## 配置结构

默认配置目录：`~/.config/agentctl/`

```
~/.config/agentctl/
├── mcp.json          # MCP Server 注册表
├── rules/            # Rule 模板（CLAUDE.md 等）
├── hooks/            # Hook 配置
├── commands/         # 自定义命令
├── ignore/           # Ignore 规则
├── skills/           # Skill 源文件
└── secrets/          # 加密密钥（age）
```

## 工作原理

agentctl 从集中配置读取声明式定义，转换并写入各 Agent 的原生配置文件。工作流遵循 **plan → apply → verify** 循环：

1. **Plan** — `agentctl mcp plan` 预览变更，不做任何修改
2. **Apply** — `agentctl apply` 写入各 Agent 的目标配置文件
3. **Verify** — `agentctl status` 检测源配置与目标之间的漂移
4. **Rollback** — `agentctl rollback` 回滚到上一次状态

## 路线图

- [ ] `agentctl init` — 交互式初始化，检测已安装 agent 并生成初始配置
- [ ] OpenCode hooks 适配 — 生成 JS/TS plugin 文件以适配 OpenCode 的插件系统
- [ ] Gemini CLI 全局 ignore — 等待[上游支持](https://github.com/google-gemini/gemini-cli/issues/4925)

## License

MIT
