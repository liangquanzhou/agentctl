# agentctl

A unified control plane for managing multiple AI coding agents. agentctl keeps MCP servers, rules, hooks, commands, ignore patterns, and skills in one place, then distributes them to Claude Code, Gemini CLI, Codex, and more — each in its native config format. Think `terraform apply`, but for your AI toolchain.

[中文文档](README_zh.md)

## Installation

```bash
brew tap liangquanzhou/tap
brew install agentctl
```

Or build from source:

```bash
git clone https://github.com/liangquanzhou/agentctl.git
cd agentctl
make install   # installs to ~/.local/bin/
```

## Quick Start

```bash
# Validate your configuration
agentctl validate

# Preview what would change
agentctl mcp plan

# Apply all configurations (MCP + rules + hooks + commands + ignore + skills)
agentctl apply

# Check system status
agentctl status
```

## Commands

| Command | Description |
|---------|-------------|
| `apply` | Apply all subsystems at once |
| `status` | Show status of all subsystems |
| `validate` | Validate configuration files |
| `doctor` | Health check (secrets, dependencies) |
| `rollback` | Rollback last apply |
| `mcp list\|add\|rm\|plan\|apply\|status` | MCP server management |
| `skills sync\|list\|status\|pull` | Skills synchronization |
| `rules\|hooks\|commands\|ignore` | Content management per type |
| `drift` | Check MCP configuration drift |
| `reconcile` | Fix MCP drift |
| `runs` | View apply history |

## Supported Agents

| Agent | Config Format | MCP Config Path |
|-------|--------------|-----------------|
| **Claude Code** | JSON | `~/.claude.json` |
| **Codex** | TOML | `~/.codex/config.toml` |
| **Gemini CLI** | JSON | `~/.gemini/settings.json` |
| **Antigravity** | JSON | `~/.gemini/antigravity/mcp_config.json` |
| **OpenCode** | JSON | `~/.config/opencode/opencode.json` |

Custom agents can be added via TOML overrides in `~/.config/agentctl/agents/`.

## Configuration

Default config directory: `~/.config/agentctl/`

```
~/.config/agentctl/
├── mcp.json          # MCP server registry
├── rules/            # Rule templates (CLAUDE.md, etc.)
├── hooks/            # Hook configurations
├── commands/         # Custom commands
├── ignore/           # Ignore patterns
├── skills/           # Skill source files
└── secrets/          # Encrypted secrets (age)
```

## How It Works

agentctl reads a centralized configuration and distributes it to each AI agent's native config format. The workflow follows a **plan → apply → verify** cycle:

1. **Plan** — `agentctl mcp plan` previews changes without touching anything
2. **Apply** — `agentctl apply` writes configs to each agent's target files
3. **Verify** — `agentctl status` detects drift between source and targets
4. **Rollback** — `agentctl rollback` reverts to the previous state if needed

## License

MIT
