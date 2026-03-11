package content

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── RulesList ────────────────────────────────────────────────────────

func TestRulesList_Empty(t *testing.T) {
	tmp := t.TempDir()
	data, err := RulesList(tmp)
	if err != nil {
		t.Fatalf("RulesList failed: %v", err)
	}
	agents, _ := data["agents"].(map[string]any)
	if len(agents) != 0 {
		t.Errorf("expected 0 agents, got %d", len(agents))
	}
}

func TestRulesList_WithConfig(t *testing.T) {
	tmp := t.TempDir()
	rulesDir := filepath.Join(tmp, "rules")
	os.MkdirAll(rulesDir, 0o755)
	writeText(t, filepath.Join(rulesDir, "shared.md"), "# Shared")
	writeText(t, filepath.Join(rulesDir, "claude.md"), "# Claude")

	htmp := homeTmpDir(t)
	target := filepath.Join(htmp, "CLAUDE.md")

	writeJSON(t, filepath.Join(rulesDir, "config.json"), map[string]any{
		"agents": map[string]any{
			"claude": map[string]any{
				"compose": []any{"shared.md", "claude.md"},
				"target":  target,
			},
		},
	})

	data, err := RulesList(tmp)
	if err != nil {
		t.Fatalf("RulesList failed: %v", err)
	}

	sf, _ := data["source_files"].([]string)
	if len(sf) != 2 {
		t.Errorf("expected 2 source files, got %d", len(sf))
	}

	agents, _ := data["agents"].(map[string]any)
	if len(agents) != 1 {
		t.Errorf("expected 1 agent, got %d", len(agents))
	}
	claudeCfg, _ := agents["claude"].(map[string]any)
	compose, _ := claudeCfg["compose"].([]string)
	if len(compose) != 2 {
		t.Errorf("expected 2 compose files, got %d", len(compose))
	}
}

// ── RulesAdd ─────────────────────────────────────────────────────────

func TestRulesAdd_NewAgent(t *testing.T) {
	tmp := t.TempDir()
	rulesDir := filepath.Join(tmp, "rules")
	os.MkdirAll(rulesDir, 0o755)
	writeText(t, filepath.Join(rulesDir, "shared.md"), "# Shared")

	htmp := homeTmpDir(t)
	target := filepath.Join(htmp, "RULES.md")

	data, err := RulesAdd(tmp, "shared.md", "test-agent", target, "\n---\n")
	if err != nil {
		t.Fatalf("RulesAdd failed: %v", err)
	}

	if data["op"] != "add" {
		t.Errorf("op = %v, want 'add'", data["op"])
	}
	if data["agent"] != "test-agent" {
		t.Errorf("agent = %v, want 'test-agent'", data["agent"])
	}
	if data["created_agent"] != true {
		t.Error("created_agent should be true")
	}
	compose, _ := data["compose"].([]string)
	if len(compose) != 1 || compose[0] != "shared.md" {
		t.Errorf("compose = %v, want [shared.md]", compose)
	}
}

func TestRulesAdd_ExistingAgent(t *testing.T) {
	tmp := t.TempDir()
	rulesDir := filepath.Join(tmp, "rules")
	os.MkdirAll(rulesDir, 0o755)
	writeText(t, filepath.Join(rulesDir, "a.md"), "# A")
	writeText(t, filepath.Join(rulesDir, "b.md"), "# B")

	htmp := homeTmpDir(t)
	target := filepath.Join(htmp, "RULES.md")

	writeJSON(t, filepath.Join(rulesDir, "config.json"), map[string]any{
		"agents": map[string]any{
			"claude": map[string]any{
				"compose": []any{"a.md"},
				"target":  target,
			},
		},
	})

	data, err := RulesAdd(tmp, "b.md", "claude", "", "")
	if err != nil {
		t.Fatalf("RulesAdd failed: %v", err)
	}
	if data["created_agent"] != false {
		t.Error("created_agent should be false")
	}
	compose, _ := data["compose"].([]string)
	if len(compose) != 2 {
		t.Errorf("compose should have 2 entries, got %d", len(compose))
	}
}

func TestRulesAdd_DuplicateReject(t *testing.T) {
	tmp := t.TempDir()
	rulesDir := filepath.Join(tmp, "rules")
	os.MkdirAll(rulesDir, 0o755)
	writeText(t, filepath.Join(rulesDir, "a.md"), "# A")

	htmp := homeTmpDir(t)
	target := filepath.Join(htmp, "RULES.md")

	writeJSON(t, filepath.Join(rulesDir, "config.json"), map[string]any{
		"agents": map[string]any{
			"claude": map[string]any{
				"compose": []any{"a.md"},
				"target":  target,
			},
		},
	})

	_, err := RulesAdd(tmp, "a.md", "claude", "", "")
	if err == nil {
		t.Fatal("should reject duplicate filename")
	}
	if !strings.Contains(err.Error(), "already in compose list") {
		t.Errorf("error should mention 'already in compose list', got: %v", err)
	}
}

func TestRulesAdd_MissingFile(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "rules"), 0o755)

	_, err := RulesAdd(tmp, "nonexistent.md", "agent", "~/target", "")
	if err == nil {
		t.Fatal("should reject missing source file")
	}
	if !strings.Contains(err.Error(), "source file not found") {
		t.Errorf("error should mention 'source file not found', got: %v", err)
	}
}

func TestRulesAdd_NewAgentRequiresTarget(t *testing.T) {
	tmp := t.TempDir()
	rulesDir := filepath.Join(tmp, "rules")
	os.MkdirAll(rulesDir, 0o755)
	writeText(t, filepath.Join(rulesDir, "a.md"), "# A")

	_, err := RulesAdd(tmp, "a.md", "new-agent", "", "")
	if err == nil {
		t.Fatal("should require --target for new agent")
	}
	if !strings.Contains(err.Error(), "--target is required") {
		t.Errorf("error should mention '--target is required', got: %v", err)
	}
}

func TestRulesAdd_PathTraversal(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "rules"), 0o755)

	_, err := RulesAdd(tmp, "../escape.md", "agent", "~/target", "")
	if err == nil {
		t.Fatal("should reject path traversal")
	}
	if !strings.Contains(err.Error(), "path traversal") {
		t.Errorf("error should mention 'path traversal', got: %v", err)
	}
}

// ── RulesRm ──────────────────────────────────────────────────────────

func TestRulesRm_RemovesFile(t *testing.T) {
	tmp := t.TempDir()
	rulesDir := filepath.Join(tmp, "rules")
	os.MkdirAll(rulesDir, 0o755)

	htmp := homeTmpDir(t)
	target := filepath.Join(htmp, "RULES.md")

	writeJSON(t, filepath.Join(rulesDir, "config.json"), map[string]any{
		"agents": map[string]any{
			"claude": map[string]any{
				"compose": []any{"a.md", "b.md"},
				"target":  target,
			},
		},
	})

	data, err := RulesRm(tmp, "a.md", "claude")
	if err != nil {
		t.Fatalf("RulesRm failed: %v", err)
	}
	if data["removed_agent"] != false {
		t.Error("agent should not be removed when compose still has entries")
	}
}

func TestRulesRm_RemovesAgent(t *testing.T) {
	tmp := t.TempDir()
	rulesDir := filepath.Join(tmp, "rules")
	os.MkdirAll(rulesDir, 0o755)

	htmp := homeTmpDir(t)
	target := filepath.Join(htmp, "RULES.md")

	writeJSON(t, filepath.Join(rulesDir, "config.json"), map[string]any{
		"agents": map[string]any{
			"claude": map[string]any{
				"compose": []any{"a.md"},
				"target":  target,
			},
		},
	})

	data, err := RulesRm(tmp, "a.md", "claude")
	if err != nil {
		t.Fatalf("RulesRm failed: %v", err)
	}
	if data["removed_agent"] != true {
		t.Error("agent should be removed when compose becomes empty")
	}
}

func TestRulesRm_AgentNotFound(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "rules"), 0o755)
	writeJSON(t, filepath.Join(tmp, "rules", "config.json"), map[string]any{
		"agents": map[string]any{},
	})

	_, err := RulesRm(tmp, "a.md", "nonexistent")
	if err == nil {
		t.Fatal("should reject nonexistent agent")
	}
	if !strings.Contains(err.Error(), "agent not found") {
		t.Errorf("error should mention 'agent not found', got: %v", err)
	}
}

func TestRulesRm_FileNotFound(t *testing.T) {
	tmp := t.TempDir()
	rulesDir := filepath.Join(tmp, "rules")
	os.MkdirAll(rulesDir, 0o755)

	htmp := homeTmpDir(t)
	target := filepath.Join(htmp, "RULES.md")

	writeJSON(t, filepath.Join(rulesDir, "config.json"), map[string]any{
		"agents": map[string]any{
			"claude": map[string]any{
				"compose": []any{"a.md"},
				"target":  target,
			},
		},
	})

	_, err := RulesRm(tmp, "b.md", "claude")
	if err == nil {
		t.Fatal("should reject filename not in compose")
	}
	if !strings.Contains(err.Error(), "not in compose list") {
		t.Errorf("error should mention 'not in compose list', got: %v", err)
	}
}

// ── HooksList ────────────────────────────────────────────────────────

func TestHooksList_Empty(t *testing.T) {
	tmp := t.TempDir()
	data, err := HooksList(tmp)
	if err != nil {
		t.Fatalf("HooksList failed: %v", err)
	}
	agents, _ := data["agents"].(map[string]any)
	if len(agents) != 0 {
		t.Errorf("expected 0 agents, got %d", len(agents))
	}
}

func TestHooksList_WithConfig(t *testing.T) {
	tmp := t.TempDir()
	hooksDir := filepath.Join(tmp, "hooks")
	os.MkdirAll(hooksDir, 0o755)

	writeJSON(t, filepath.Join(hooksDir, "config.json"), map[string]any{
		"agents": map[string]any{
			"claude": map[string]any{
				"target": "~/.claude/settings.json",
				"format": "claude_hooks",
				"events": map[string]any{
					"SessionEnd": []any{
						map[string]any{"type": "command", "command": "test"},
					},
				},
			},
		},
	})

	data, err := HooksList(tmp)
	if err != nil {
		t.Fatalf("HooksList failed: %v", err)
	}
	agents, _ := data["agents"].(map[string]any)
	if len(agents) != 1 {
		t.Errorf("expected 1 agent, got %d", len(agents))
	}
}

// ── HooksAdd ─────────────────────────────────────────────────────────

func TestHooksAdd_NewClaudeAgent(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "hooks"), 0o755)

	data, err := HooksAdd(tmp, "claude", HooksAddOpts{
		Target:  "~/.claude/settings.json",
		Format:  "claude_hooks",
		Event:   "SessionEnd",
		Command: "kg-memory-mcp hooks run {{agent}}",
	})
	if err != nil {
		t.Fatalf("HooksAdd failed: %v", err)
	}
	if data["created_agent"] != true {
		t.Error("created_agent should be true")
	}
	if data["format"] != "claude_hooks" {
		t.Errorf("format = %v, want 'claude_hooks'", data["format"])
	}
}

func TestHooksAdd_CodexNotify(t *testing.T) {
	tmp := t.TempDir()
	hooksDir := filepath.Join(tmp, "hooks")
	os.MkdirAll(hooksDir, 0o755)

	writeJSON(t, filepath.Join(hooksDir, "config.json"), map[string]any{
		"agents": map[string]any{
			"codex": map[string]any{
				"target": "~/.codex/config.toml",
				"format": "codex_notify",
				"notify": []any{"existing-cmd"},
			},
		},
	})

	data, err := HooksAdd(tmp, "codex", HooksAddOpts{
		Notify: "new-cmd",
	})
	if err != nil {
		t.Fatalf("HooksAdd failed: %v", err)
	}
	if data["created_agent"] != false {
		t.Error("created_agent should be false")
	}
}

func TestHooksAdd_CodexRejectsEventCommand(t *testing.T) {
	tmp := t.TempDir()
	hooksDir := filepath.Join(tmp, "hooks")
	os.MkdirAll(hooksDir, 0o755)

	writeJSON(t, filepath.Join(hooksDir, "config.json"), map[string]any{
		"agents": map[string]any{
			"codex": map[string]any{
				"target": "~/.codex/config.toml",
				"format": "codex_notify",
				"notify": []any{"existing-cmd"},
			},
		},
	})

	_, err := HooksAdd(tmp, "codex", HooksAddOpts{
		Event:   "SessionEnd",
		Command: "test",
	})
	if err == nil {
		t.Fatal("should reject --event/--command for codex_notify")
	}
	if !strings.Contains(err.Error(), "codex_notify format uses --notify") {
		t.Errorf("error mismatch: %v", err)
	}
}

func TestHooksAdd_NewAgentRequiresTargetFormat(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "hooks"), 0o755)

	_, err := HooksAdd(tmp, "new-agent", HooksAddOpts{
		Event:   "SessionEnd",
		Command: "test",
	})
	if err == nil {
		t.Fatal("should require --target and --format for new agent")
	}
	if !strings.Contains(err.Error(), "--target and --format are required") {
		t.Errorf("error mismatch: %v", err)
	}
}

func TestHooksAdd_DuplicateNotifyReject(t *testing.T) {
	tmp := t.TempDir()
	hooksDir := filepath.Join(tmp, "hooks")
	os.MkdirAll(hooksDir, 0o755)

	writeJSON(t, filepath.Join(hooksDir, "config.json"), map[string]any{
		"agents": map[string]any{
			"codex": map[string]any{
				"target": "~/.codex/config.toml",
				"format": "codex_notify",
				"notify": []any{"existing-cmd"},
			},
		},
	})

	_, err := HooksAdd(tmp, "codex", HooksAddOpts{Notify: "existing-cmd"})
	if err == nil {
		t.Fatal("should reject duplicate notify value")
	}
	if !strings.Contains(err.Error(), "notify value already exists") {
		t.Errorf("error mismatch: %v", err)
	}
}

// ── HooksRm ──────────────────────────────────────────────────────────

func TestHooksRm_RemoveAgent(t *testing.T) {
	tmp := t.TempDir()
	hooksDir := filepath.Join(tmp, "hooks")
	os.MkdirAll(hooksDir, 0o755)

	writeJSON(t, filepath.Join(hooksDir, "config.json"), map[string]any{
		"agents": map[string]any{
			"claude": map[string]any{
				"target": "~/.claude/settings.json",
				"format": "claude_hooks",
				"events": map[string]any{
					"SessionEnd": []any{
						map[string]any{"type": "command", "command": "test"},
					},
				},
			},
		},
	})

	data, err := HooksRm(tmp, "claude", HooksRmOpts{})
	if err != nil {
		t.Fatalf("HooksRm failed: %v", err)
	}
	if data["removed_agent"] != true {
		t.Error("removed_agent should be true")
	}
}

func TestHooksRm_RemoveEvent(t *testing.T) {
	tmp := t.TempDir()
	hooksDir := filepath.Join(tmp, "hooks")
	os.MkdirAll(hooksDir, 0o755)

	writeJSON(t, filepath.Join(hooksDir, "config.json"), map[string]any{
		"agents": map[string]any{
			"claude": map[string]any{
				"target": "~/.claude/settings.json",
				"format": "claude_hooks",
				"events": map[string]any{
					"SessionEnd": []any{
						map[string]any{"type": "command", "command": "test1"},
					},
					"SessionStart": []any{
						map[string]any{"type": "command", "command": "test2"},
					},
				},
			},
		},
	})

	data, err := HooksRm(tmp, "claude", HooksRmOpts{Event: "SessionEnd"})
	if err != nil {
		t.Fatalf("HooksRm failed: %v", err)
	}
	if data["removed_agent"] != false {
		t.Error("agent should not be removed when other events remain")
	}
}

func TestHooksRm_RemoveCommand(t *testing.T) {
	tmp := t.TempDir()
	hooksDir := filepath.Join(tmp, "hooks")
	os.MkdirAll(hooksDir, 0o755)

	writeJSON(t, filepath.Join(hooksDir, "config.json"), map[string]any{
		"agents": map[string]any{
			"claude": map[string]any{
				"target": "~/.claude/settings.json",
				"format": "claude_hooks",
				"events": map[string]any{
					"SessionEnd": []any{
						map[string]any{"type": "command", "command": "cmd1"},
						map[string]any{"type": "command", "command": "cmd2"},
					},
				},
			},
		},
	})

	data, err := HooksRm(tmp, "claude", HooksRmOpts{
		Event:   "SessionEnd",
		Command: "cmd1",
	})
	if err != nil {
		t.Fatalf("HooksRm failed: %v", err)
	}
	if data["removed_agent"] != false {
		t.Error("agent should not be removed")
	}
}

func TestHooksRm_AgentNotFound(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "hooks"), 0o755)
	writeJSON(t, filepath.Join(tmp, "hooks", "config.json"), map[string]any{
		"agents": map[string]any{},
	})

	_, err := HooksRm(tmp, "nonexistent", HooksRmOpts{})
	if err == nil {
		t.Fatal("should reject nonexistent agent")
	}
	if !strings.Contains(err.Error(), "agent not found") {
		t.Errorf("error mismatch: %v", err)
	}
}

func TestHooksRm_CodexNotifyRemoval(t *testing.T) {
	tmp := t.TempDir()
	hooksDir := filepath.Join(tmp, "hooks")
	os.MkdirAll(hooksDir, 0o755)

	writeJSON(t, filepath.Join(hooksDir, "config.json"), map[string]any{
		"agents": map[string]any{
			"codex": map[string]any{
				"target": "~/.codex/config.toml",
				"format": "codex_notify",
				"notify": []any{"cmd1", "cmd2"},
			},
		},
	})

	data, err := HooksRm(tmp, "codex", HooksRmOpts{Notify: "cmd1"})
	if err != nil {
		t.Fatalf("HooksRm failed: %v", err)
	}
	if data["removed_agent"] != false {
		t.Error("agent should not be removed when notify still has entries")
	}
}

func TestHooksRm_CodexNotifyLastRemovesAgent(t *testing.T) {
	tmp := t.TempDir()
	hooksDir := filepath.Join(tmp, "hooks")
	os.MkdirAll(hooksDir, 0o755)

	writeJSON(t, filepath.Join(hooksDir, "config.json"), map[string]any{
		"agents": map[string]any{
			"codex": map[string]any{
				"target": "~/.codex/config.toml",
				"format": "codex_notify",
				"notify": []any{"only-cmd"},
			},
		},
	})

	data, err := HooksRm(tmp, "codex", HooksRmOpts{Notify: "only-cmd"})
	if err != nil {
		t.Fatalf("HooksRm failed: %v", err)
	}
	if data["removed_agent"] != true {
		t.Error("agent should be removed when notify becomes empty")
	}
}

// ── CommandsList ─────────────────────────────────────────────────────

func TestCommandsList_Empty(t *testing.T) {
	tmp := t.TempDir()
	data, err := CommandsList(tmp)
	if err != nil {
		t.Fatalf("CommandsList failed: %v", err)
	}
	agents, _ := data["agents"].(map[string]any)
	if len(agents) != 0 {
		t.Errorf("expected 0 agents, got %d", len(agents))
	}
}

func TestCommandsList_WithConfig(t *testing.T) {
	tmp := t.TempDir()
	cmdsDir := filepath.Join(tmp, "commands")
	os.MkdirAll(cmdsDir, 0o755)
	writeText(t, filepath.Join(cmdsDir, "greet.md"), "# Greet")

	htmp := homeTmpDir(t)
	targetDir := filepath.Join(htmp, "commands")

	writeJSON(t, filepath.Join(cmdsDir, "config.json"), map[string]any{
		"agents": map[string]any{
			"claude": map[string]any{"target_dir": targetDir},
		},
	})

	data, err := CommandsList(tmp)
	if err != nil {
		t.Fatalf("CommandsList failed: %v", err)
	}
	sf, _ := data["source_files"].([]string)
	if len(sf) != 1 {
		t.Errorf("expected 1 source file, got %d", len(sf))
	}
	agents, _ := data["agents"].(map[string]any)
	if len(agents) != 1 {
		t.Errorf("expected 1 agent, got %d", len(agents))
	}
}

// ── CommandsAdd ──────────────────────────────────────────────────────

func TestCommandsAdd_NewAgent(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "commands"), 0o755)

	htmp := homeTmpDir(t)
	targetDir := filepath.Join(htmp, "commands")

	data, err := CommandsAdd(tmp, "claude", targetDir, "")
	if err != nil {
		t.Fatalf("CommandsAdd failed: %v", err)
	}
	if data["op"] != "add" {
		t.Errorf("op = %v, want 'add'", data["op"])
	}
	if data["agent"] != "claude" {
		t.Errorf("agent = %v, want 'claude'", data["agent"])
	}
}

func TestCommandsAdd_DuplicateReject(t *testing.T) {
	tmp := t.TempDir()
	cmdsDir := filepath.Join(tmp, "commands")
	os.MkdirAll(cmdsDir, 0o755)

	htmp := homeTmpDir(t)
	targetDir := filepath.Join(htmp, "commands")

	writeJSON(t, filepath.Join(cmdsDir, "config.json"), map[string]any{
		"agents": map[string]any{
			"claude": map[string]any{"target_dir": targetDir},
		},
	})

	_, err := CommandsAdd(tmp, "claude", targetDir, "")
	if err == nil {
		t.Fatal("should reject duplicate agent")
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("error mismatch: %v", err)
	}
}

// ── CommandsRm ───────────────────────────────────────────────────────

func TestCommandsRm_ExistingAgent(t *testing.T) {
	tmp := t.TempDir()
	cmdsDir := filepath.Join(tmp, "commands")
	os.MkdirAll(cmdsDir, 0o755)

	htmp := homeTmpDir(t)
	targetDir := filepath.Join(htmp, "commands")

	writeJSON(t, filepath.Join(cmdsDir, "config.json"), map[string]any{
		"agents": map[string]any{
			"claude": map[string]any{"target_dir": targetDir},
		},
	})

	data, err := CommandsRm(tmp, "claude")
	if err != nil {
		t.Fatalf("CommandsRm failed: %v", err)
	}
	if data["op"] != "rm" {
		t.Errorf("op = %v, want 'rm'", data["op"])
	}
}

func TestCommandsRm_AgentNotFound(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "commands"), 0o755)
	writeJSON(t, filepath.Join(tmp, "commands", "config.json"), map[string]any{
		"agents": map[string]any{},
	})

	_, err := CommandsRm(tmp, "nonexistent")
	if err == nil {
		t.Fatal("should reject nonexistent agent")
	}
	if !strings.Contains(err.Error(), "agent not found") {
		t.Errorf("error mismatch: %v", err)
	}
}

// ── IgnoreList ───────────────────────────────────────────────────────

func TestIgnoreList_Empty(t *testing.T) {
	tmp := t.TempDir()
	data, err := IgnoreList(tmp)
	if err != nil {
		t.Fatalf("IgnoreList failed: %v", err)
	}
	patterns, _ := data["patterns"].([]string)
	if len(patterns) != 0 {
		t.Errorf("expected 0 patterns, got %d", len(patterns))
	}
}

func TestIgnoreList_WithConfig(t *testing.T) {
	tmp := t.TempDir()
	writeJSON(t, filepath.Join(tmp, "ignore.json"), map[string]any{
		"patterns": []any{"node_modules", ".env"},
		"agents": map[string]any{
			"claude": map[string]any{"target": "~/.claude/.agentignore"},
		},
	})

	data, err := IgnoreList(tmp)
	if err != nil {
		t.Fatalf("IgnoreList failed: %v", err)
	}
	patterns, _ := data["patterns"].([]string)
	if len(patterns) != 2 {
		t.Errorf("expected 2 patterns, got %d", len(patterns))
	}
	agents, _ := data["agents"].(map[string]any)
	if len(agents) != 1 {
		t.Errorf("expected 1 agent, got %d", len(agents))
	}
}

// ── IgnoreAdd ────────────────────────────────────────────────────────

func TestIgnoreAdd_Pattern(t *testing.T) {
	tmp := t.TempDir()
	writeJSON(t, filepath.Join(tmp, "ignore.json"), map[string]any{
		"patterns": []any{"node_modules"},
		"agents":   map[string]any{},
	})

	data, err := IgnoreAdd(tmp, IgnoreAddOpts{Pattern: ".env"})
	if err != nil {
		t.Fatalf("IgnoreAdd failed: %v", err)
	}
	if data["op"] != "add-pattern" {
		t.Errorf("op = %v, want 'add-pattern'", data["op"])
	}
}

func TestIgnoreAdd_Agent(t *testing.T) {
	tmp := t.TempDir()
	writeJSON(t, filepath.Join(tmp, "ignore.json"), map[string]any{
		"patterns": []any{},
		"agents":   map[string]any{},
	})

	data, err := IgnoreAdd(tmp, IgnoreAddOpts{
		Agent:  "claude",
		Target: "~/.claude/.agentignore",
	})
	if err != nil {
		t.Fatalf("IgnoreAdd failed: %v", err)
	}
	if data["op"] != "add-agent" {
		t.Errorf("op = %v, want 'add-agent'", data["op"])
	}
}

func TestIgnoreAdd_DuplicatePatternReject(t *testing.T) {
	tmp := t.TempDir()
	writeJSON(t, filepath.Join(tmp, "ignore.json"), map[string]any{
		"patterns": []any{"node_modules"},
		"agents":   map[string]any{},
	})

	_, err := IgnoreAdd(tmp, IgnoreAddOpts{Pattern: "node_modules"})
	if err == nil {
		t.Fatal("should reject duplicate pattern")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error mismatch: %v", err)
	}
}

func TestIgnoreAdd_DuplicateAgentReject(t *testing.T) {
	tmp := t.TempDir()
	writeJSON(t, filepath.Join(tmp, "ignore.json"), map[string]any{
		"patterns": []any{},
		"agents": map[string]any{
			"claude": map[string]any{"target": "~/.claude/.agentignore"},
		},
	})

	_, err := IgnoreAdd(tmp, IgnoreAddOpts{
		Agent:  "claude",
		Target: "~/.claude/.agentignore",
	})
	if err == nil {
		t.Fatal("should reject duplicate agent")
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("error mismatch: %v", err)
	}
}

func TestIgnoreAdd_MutuallyExclusive(t *testing.T) {
	tmp := t.TempDir()
	writeJSON(t, filepath.Join(tmp, "ignore.json"), map[string]any{
		"patterns": []any{},
		"agents":   map[string]any{},
	})

	_, err := IgnoreAdd(tmp, IgnoreAddOpts{
		Pattern: "*.log",
		Agent:   "claude",
		Target:  "~/.claude/.agentignore",
	})
	if err == nil {
		t.Fatal("should reject pattern + agent together")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error mismatch: %v", err)
	}
}

func TestIgnoreAdd_NeitherProvided(t *testing.T) {
	tmp := t.TempDir()
	writeJSON(t, filepath.Join(tmp, "ignore.json"), map[string]any{
		"patterns": []any{},
		"agents":   map[string]any{},
	})

	_, err := IgnoreAdd(tmp, IgnoreAddOpts{})
	if err == nil {
		t.Fatal("should reject when neither pattern nor agent provided")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Errorf("error mismatch: %v", err)
	}
}

func TestIgnoreAdd_AgentWithoutTarget(t *testing.T) {
	tmp := t.TempDir()
	writeJSON(t, filepath.Join(tmp, "ignore.json"), map[string]any{
		"patterns": []any{},
		"agents":   map[string]any{},
	})

	_, err := IgnoreAdd(tmp, IgnoreAddOpts{Agent: "claude"})
	if err == nil {
		t.Fatal("should require --target with --agent")
	}
	if !strings.Contains(err.Error(), "--target is required") {
		t.Errorf("error mismatch: %v", err)
	}
}

// ── IgnoreRm ─────────────────────────────────────────────────────────

func TestIgnoreRm_Pattern(t *testing.T) {
	tmp := t.TempDir()
	writeJSON(t, filepath.Join(tmp, "ignore.json"), map[string]any{
		"patterns": []any{"node_modules", ".env"},
		"agents":   map[string]any{},
	})

	data, err := IgnoreRm(tmp, IgnoreRmOpts{Pattern: "node_modules"})
	if err != nil {
		t.Fatalf("IgnoreRm failed: %v", err)
	}
	if data["op"] != "rm-pattern" {
		t.Errorf("op = %v, want 'rm-pattern'", data["op"])
	}
}

func TestIgnoreRm_Agent(t *testing.T) {
	tmp := t.TempDir()
	writeJSON(t, filepath.Join(tmp, "ignore.json"), map[string]any{
		"patterns": []any{},
		"agents": map[string]any{
			"claude": map[string]any{"target": "~/.claude/.agentignore"},
		},
	})

	data, err := IgnoreRm(tmp, IgnoreRmOpts{Agent: "claude"})
	if err != nil {
		t.Fatalf("IgnoreRm failed: %v", err)
	}
	if data["op"] != "rm-agent" {
		t.Errorf("op = %v, want 'rm-agent'", data["op"])
	}
}

func TestIgnoreRm_PatternNotFound(t *testing.T) {
	tmp := t.TempDir()
	writeJSON(t, filepath.Join(tmp, "ignore.json"), map[string]any{
		"patterns": []any{"node_modules"},
		"agents":   map[string]any{},
	})

	_, err := IgnoreRm(tmp, IgnoreRmOpts{Pattern: "nonexistent"})
	if err == nil {
		t.Fatal("should reject nonexistent pattern")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error mismatch: %v", err)
	}
}

func TestIgnoreRm_AgentNotFound(t *testing.T) {
	tmp := t.TempDir()
	writeJSON(t, filepath.Join(tmp, "ignore.json"), map[string]any{
		"patterns": []any{},
		"agents":   map[string]any{},
	})

	_, err := IgnoreRm(tmp, IgnoreRmOpts{Agent: "nonexistent"})
	if err == nil {
		t.Fatal("should reject nonexistent agent")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error mismatch: %v", err)
	}
}

func TestIgnoreRm_MutuallyExclusive(t *testing.T) {
	tmp := t.TempDir()
	writeJSON(t, filepath.Join(tmp, "ignore.json"), map[string]any{
		"patterns": []any{"node_modules"},
		"agents": map[string]any{
			"claude": map[string]any{"target": "~/.claude/.agentignore"},
		},
	})

	_, err := IgnoreRm(tmp, IgnoreRmOpts{
		Pattern: "node_modules",
		Agent:   "claude",
	})
	if err == nil {
		t.Fatal("should reject pattern + agent together")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error mismatch: %v", err)
	}
}
