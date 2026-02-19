package agents

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

func fixturesDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "agents")
}

// ── Builtins ─────────────────────────────────────────────────────────

func TestBuiltinsContainAllFiveAgents(t *testing.T) {
	builtins := builtinAgents()

	expected := []string{"claude-code", "codex", "gemini-cli", "antigravity", "opencode"}
	for _, name := range expected {
		if _, ok := builtins[name]; !ok {
			t.Errorf("builtinAgents() missing expected agent %q", name)
		}
	}

	if len(builtins) != len(expected) {
		t.Errorf("builtinAgents() has %d agents, want %d", len(builtins), len(expected))
	}
}

// ── LoadAgentRegistry ────────────────────────────────────────────────

func TestLoadAgentRegistry_NoTOMLReturnsBuiltins(t *testing.T) {
	// Use a directory that does not exist to get pure builtins.
	registry := LoadAgentRegistry("/nonexistent/agents/dir")

	builtins := builtinAgents()
	if len(registry) != len(builtins) {
		t.Fatalf("registry has %d agents, want %d builtins", len(registry), len(builtins))
	}

	for name := range builtins {
		if _, ok := registry[name]; !ok {
			t.Errorf("registry missing builtin %q", name)
		}
	}
}

func TestLoadAgentRegistry_TOMLOverrideReplacesBuiltin(t *testing.T) {
	registry := LoadAgentRegistry(fixturesDir())

	codex, ok := registry["codex"]
	if !ok {
		t.Fatal("registry missing 'codex' after TOML override")
	}

	// override-codex.toml sets path = "~/.codex-custom/config.toml"
	if codex.MCPPath == "" {
		t.Fatal("codex MCPPath is empty after override")
	}

	// The path should be expanded from ~, containing "codex-custom".
	home := home()
	wantPath := filepath.Join(home, ".codex-custom", "config.toml")
	if codex.MCPPath != wantPath {
		t.Errorf("codex MCPPath = %q, want %q", codex.MCPPath, wantPath)
	}

	// Aliases should be ["cx"] from the override TOML.
	if len(codex.Aliases) != 1 || codex.Aliases[0] != "cx" {
		t.Errorf("codex Aliases = %v, want [cx]", codex.Aliases)
	}
}

func TestLoadAgentRegistry_TOMLAddsNewAgent(t *testing.T) {
	registry := LoadAgentRegistry(fixturesDir())

	cursor, ok := registry["cursor"]
	if !ok {
		t.Fatal("registry missing 'cursor' from cursor.toml")
	}

	if cursor.DisplayOrder != 10 {
		t.Errorf("cursor DisplayOrder = %d, want 10", cursor.DisplayOrder)
	}

	if len(cursor.Aliases) != 1 || cursor.Aliases[0] != "cur" {
		t.Errorf("cursor Aliases = %v, want [cur]", cursor.Aliases)
	}

	wantPath := filepath.Join(home(), ".cursor", "mcp.json")
	if cursor.MCPPath != wantPath {
		t.Errorf("cursor MCPPath = %q, want %q", cursor.MCPPath, wantPath)
	}

	wantSkills := filepath.Join(home(), ".cursor", "skills")
	if cursor.SkillsTarget != wantSkills {
		t.Errorf("cursor SkillsTarget = %q, want %q", cursor.SkillsTarget, wantSkills)
	}
}

func TestLoadAgentRegistry_NonOverriddenBuiltinsPreserved(t *testing.T) {
	registry := LoadAgentRegistry(fixturesDir())

	// claude-code, gemini-cli, antigravity, opencode should be unchanged builtins.
	builtins := builtinAgents()
	for _, name := range []string{"claude-code", "gemini-cli", "antigravity", "opencode"} {
		got, ok := registry[name]
		if !ok {
			t.Errorf("registry missing builtin %q", name)
			continue
		}
		want := builtins[name]
		if got.MCPPath != want.MCPPath {
			t.Errorf("%s MCPPath = %q, want %q", name, got.MCPPath, want.MCPPath)
		}
		if got.MCPFileType != want.MCPFileType {
			t.Errorf("%s MCPFileType = %q, want %q", name, got.MCPFileType, want.MCPFileType)
		}
		if got.DisplayOrder != want.DisplayOrder {
			t.Errorf("%s DisplayOrder = %d, want %d", name, got.DisplayOrder, want.DisplayOrder)
		}
	}
}

func TestLoadAgentRegistry_MalformedTOMLSkipped(t *testing.T) {
	tmp := t.TempDir()

	// Write a malformed TOML (missing required agent.name).
	malformedPath := filepath.Join(tmp, "bad.toml")
	os.WriteFile(malformedPath, []byte(`
[mcp]
path = "~/.bad/mcp.json"
`), 0o644)

	registry := LoadAgentRegistry(tmp)

	// Should fall back to builtins only.
	builtins := builtinAgents()
	if len(registry) != len(builtins) {
		t.Errorf("registry has %d agents, want %d builtins (malformed should be skipped)",
			len(registry), len(builtins))
	}

	// The malformed file should not have introduced any extra agent.
	for name := range registry {
		if _, ok := builtins[name]; !ok {
			t.Errorf("registry contains unexpected agent %q from malformed TOML", name)
		}
	}
}

// ── Builder helpers ──────────────────────────────────────────────────

func TestBuildAliasMap_IncludesSelfAndAliases(t *testing.T) {
	registry := LoadAgentRegistry(fixturesDir())
	aliases := BuildAliasMap(registry)

	// Each agent's canonical name maps to itself.
	for name := range registry {
		canonical, ok := aliases[name]
		if !ok {
			t.Errorf("BuildAliasMap missing self-mapping for %q", name)
			continue
		}
		if canonical != name {
			t.Errorf("BuildAliasMap[%q] = %q, want %q", name, canonical, name)
		}
	}

	// Check specific aliases.
	checks := map[string]string{
		"claude": "claude-code",   // builtin alias
		"gemini": "gemini-cli",    // builtin alias
		"cx":     "codex",         // override alias
		"cur":    "cursor",        // new agent alias
	}
	for alias, wantCanonical := range checks {
		got, ok := aliases[alias]
		if !ok {
			t.Errorf("BuildAliasMap missing alias %q", alias)
			continue
		}
		if got != wantCanonical {
			t.Errorf("BuildAliasMap[%q] = %q, want %q", alias, got, wantCanonical)
		}
	}
}

func TestBuildAgentSpecs_MatchesBuiltins(t *testing.T) {
	builtins := builtinAgents()
	specs := BuildAgentSpecs(builtins)

	if len(specs) != len(builtins) {
		t.Fatalf("BuildAgentSpecs returned %d specs, want %d", len(specs), len(builtins))
	}

	for name, defn := range builtins {
		spec, ok := specs[name]
		if !ok {
			t.Errorf("BuildAgentSpecs missing agent %q", name)
			continue
		}
		if spec.Key != name {
			t.Errorf("spec[%q].Key = %q, want %q", name, spec.Key, name)
		}
		if spec.FileType != defn.MCPFileType {
			t.Errorf("spec[%q].FileType = %q, want %q", name, spec.FileType, defn.MCPFileType)
		}
		if spec.Path != defn.MCPPath {
			t.Errorf("spec[%q].Path = %q, want %q", name, spec.Path, defn.MCPPath)
		}
	}
}

func TestBuildSkillsTargets_UsesAliasAsKey(t *testing.T) {
	registry := builtinAgents()
	targets := BuildSkillsTargets(registry)

	// claude-code has alias "claude", so key should be "claude".
	if _, ok := targets["claude"]; !ok {
		t.Error("BuildSkillsTargets missing alias-based key 'claude' for claude-code")
	}
	// Canonical name should also be present.
	if _, ok := targets["claude-code"]; !ok {
		t.Error("BuildSkillsTargets missing canonical key 'claude-code'")
	}

	// gemini-cli has alias "gemini".
	if _, ok := targets["gemini"]; !ok {
		t.Error("BuildSkillsTargets missing alias-based key 'gemini' for gemini-cli")
	}
	if _, ok := targets["gemini-cli"]; !ok {
		t.Error("BuildSkillsTargets missing canonical key 'gemini-cli'")
	}

	// codex has no aliases in builtins (empty slice), so key = "codex".
	if _, ok := targets["codex"]; !ok {
		t.Error("BuildSkillsTargets missing key 'codex'")
	}

	// All targets should be non-empty strings.
	for key, dir := range targets {
		if dir == "" {
			t.Errorf("BuildSkillsTargets[%q] is empty", key)
		}
	}
}

func TestBuildDisplayOrder_Sorted(t *testing.T) {
	registry := builtinAgents()
	order := BuildDisplayOrder(registry)

	if len(order) != len(registry) {
		t.Fatalf("BuildDisplayOrder returned %d items, want %d", len(order), len(registry))
	}

	// Builtins have display_order 1..5, so expected order:
	expected := []string{"claude-code", "codex", "gemini-cli", "antigravity", "opencode"}
	for i, name := range expected {
		if order[i] != name {
			t.Errorf("BuildDisplayOrder[%d] = %q, want %q", i, order[i], name)
		}
	}
}

func TestBuildDisplayOrder_WithOverride(t *testing.T) {
	registry := LoadAgentRegistry(fixturesDir())
	order := BuildDisplayOrder(registry)

	// cursor has display_order=10, should come last.
	last := order[len(order)-1]
	if last != "cursor" {
		t.Errorf("BuildDisplayOrder last = %q, want 'cursor' (display_order=10)", last)
	}

	// Verify the result is sorted by display order.
	for i := 1; i < len(order); i++ {
		prevOrder := registry[order[i-1]].DisplayOrder
		currOrder := registry[order[i]].DisplayOrder
		if prevOrder > currOrder {
			t.Errorf("BuildDisplayOrder not sorted: %q (order=%d) before %q (order=%d)",
				order[i-1], prevOrder, order[i], currOrder)
		}
		// If same display order, should be alphabetical.
		if prevOrder == currOrder && order[i-1] > order[i] {
			t.Errorf("BuildDisplayOrder tie-break not alphabetical: %q before %q",
				order[i-1], order[i])
		}
	}

	// Verify all agents are included.
	orderSet := make(map[string]bool)
	for _, name := range order {
		orderSet[name] = true
	}
	for name := range registry {
		if !orderSet[name] {
			t.Errorf("BuildDisplayOrder missing agent %q", name)
		}
	}
}

func TestBuildManagedKeys(t *testing.T) {
	registry := builtinAgents()
	keys := BuildManagedKeys(registry)

	// claude-code should have two managed keys.
	ccKeys := keys["claude-code"]
	sort.Strings(ccKeys)
	if len(ccKeys) != 2 {
		t.Fatalf("claude-code managed keys count = %d, want 2", len(ccKeys))
	}
	wantCC := []string{"mcpServers", "projects.*.mcpServers"}
	sort.Strings(wantCC)
	for i, k := range wantCC {
		if ccKeys[i] != k {
			t.Errorf("claude-code managed_keys[%d] = %q, want %q", i, ccKeys[i], k)
		}
	}

	// codex should have one managed key.
	cxKeys := keys["codex"]
	if len(cxKeys) != 1 || cxKeys[0] != "mcp_servers" {
		t.Errorf("codex managed_keys = %v, want [mcp_servers]", cxKeys)
	}
}
