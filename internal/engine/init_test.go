package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInit_CreatesExpectedStructure(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "agentctl")

	result, err := Init(InitConfig{
		ConfigDir:      configDir,
		SelectedAgents: []string{"claude-code", "gemini-cli"},
		Force:          false,
		DryRun:         false,
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Check directories were created
	expectedDirs := []string{
		"mcp", "rules", "hooks", "commands", "agents", "skills", "secrets",
		filepath.Join("state", "runs"),
		filepath.Join("state", "snapshots"),
		filepath.Join("state", "locks"),
	}
	for _, dir := range expectedDirs {
		fullPath := filepath.Join(configDir, dir)
		info, err := os.Stat(fullPath)
		if err != nil {
			t.Errorf("expected directory %s to exist: %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("expected %s to be a directory", dir)
		}
	}

	// Check files were created
	expectedFiles := []string{
		filepath.Join("mcp", "servers.json"),
		filepath.Join("mcp", "profiles.json"),
		filepath.Join("mcp", "compat.json"),
		filepath.Join("rules", "config.json"),
		filepath.Join("rules", "shared.md"),
		filepath.Join("hooks", "config.json"),
		filepath.Join("commands", "config.json"),
		filepath.Join("skills", "config.json"),
	}
	for _, file := range expectedFiles {
		fullPath := filepath.Join(configDir, file)
		if _, err := os.Stat(fullPath); err != nil {
			t.Errorf("expected file %s to exist: %v", file, err)
		}
	}

	if result.ConfigDir != configDir {
		t.Errorf("expected ConfigDir=%s, got %s", configDir, result.ConfigDir)
	}
	if len(result.CreatedDirs) == 0 {
		t.Error("expected CreatedDirs to be non-empty")
	}
	if len(result.CreatedFiles) == 0 {
		t.Error("expected CreatedFiles to be non-empty")
	}
}

func TestInit_GeneratesValidConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "agentctl")

	_, err := Init(InitConfig{
		ConfigDir:      configDir,
		SelectedAgents: []string{"claude-code"},
		Force:          false,
		DryRun:         false,
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Validate all JSON files are parseable
	jsonFiles := []string{
		filepath.Join("mcp", "servers.json"),
		filepath.Join("mcp", "profiles.json"),
		filepath.Join("mcp", "compat.json"),
		filepath.Join("rules", "config.json"),
		filepath.Join("hooks", "config.json"),
		filepath.Join("commands", "config.json"),
		filepath.Join("skills", "config.json"),
	}

	for _, file := range jsonFiles {
		fullPath := filepath.Join(configDir, file)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			t.Errorf("failed to read %s: %v", file, err)
			continue
		}
		var parsed map[string]any
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Errorf("invalid JSON in %s: %v", file, err)
			continue
		}
		// Check common fields
		if v, ok := parsed["schema_version"]; !ok || v != "1.0.0" {
			t.Errorf("%s: expected schema_version=1.0.0, got %v", file, v)
		}
		if v, ok := parsed["managed_by"]; !ok || v != "agentctl" {
			t.Errorf("%s: expected managed_by=agentctl, got %v", file, v)
		}
	}

	// Validate shared.md exists and has content
	sharedMd, err := os.ReadFile(filepath.Join(configDir, "rules", "shared.md"))
	if err != nil {
		t.Fatalf("failed to read shared.md: %v", err)
	}
	if len(sharedMd) == 0 {
		t.Error("shared.md should not be empty")
	}
}

func TestInit_ProfilesMatchSelectedAgents(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "agentctl")

	agents := []string{"claude-code", "codex", "gemini-cli"}
	_, err := Init(InitConfig{
		ConfigDir:      configDir,
		SelectedAgents: agents,
		Force:          false,
		DryRun:         false,
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Check profiles.json has entries for all selected agents
	data, err := os.ReadFile(filepath.Join(configDir, "mcp", "profiles.json"))
	if err != nil {
		t.Fatalf("failed to read profiles.json: %v", err)
	}
	var profiles map[string]any
	if err := json.Unmarshal(data, &profiles); err != nil {
		t.Fatalf("invalid JSON in profiles.json: %v", err)
	}

	agentsMap, ok := profiles["agents"].(map[string]any)
	if !ok {
		t.Fatal("profiles.json should have 'agents' map")
	}

	for _, agent := range agents {
		if _, ok := agentsMap[agent]; !ok {
			t.Errorf("profiles.json should contain agent %q", agent)
		}
	}
	if len(agentsMap) != len(agents) {
		t.Errorf("profiles.json agents count: expected %d, got %d", len(agents), len(agentsMap))
	}

	// Check rules/config.json has entries for agents that have rules targets
	data, err = os.ReadFile(filepath.Join(configDir, "rules", "config.json"))
	if err != nil {
		t.Fatalf("failed to read rules/config.json: %v", err)
	}
	var rulesCfg map[string]any
	if err := json.Unmarshal(data, &rulesCfg); err != nil {
		t.Fatalf("invalid JSON in rules/config.json: %v", err)
	}

	rulesAgents, ok := rulesCfg["agents"].(map[string]any)
	if !ok {
		t.Fatal("rules/config.json should have 'agents' map")
	}

	// claude-code, codex, and gemini-cli all have rules targets
	for _, agent := range []string{"claude-code", "gemini-cli"} {
		if _, ok := rulesAgents[agent]; !ok {
			t.Errorf("rules/config.json should contain agent %q", agent)
		}
	}

	// Check commands/config.json
	data, err = os.ReadFile(filepath.Join(configDir, "commands", "config.json"))
	if err != nil {
		t.Fatalf("failed to read commands/config.json: %v", err)
	}
	var cmdsCfg map[string]any
	if err := json.Unmarshal(data, &cmdsCfg); err != nil {
		t.Fatalf("invalid JSON in commands/config.json: %v", err)
	}

	cmdsAgents, ok := cmdsCfg["agents"].(map[string]any)
	if !ok {
		t.Fatal("commands/config.json should have 'agents' map")
	}

	// claude-code and gemini-cli have commands targets, codex does not
	if _, ok := cmdsAgents["claude-code"]; !ok {
		t.Error("commands/config.json should contain claude-code")
	}
	if _, ok := cmdsAgents["gemini-cli"]; !ok {
		t.Error("commands/config.json should contain gemini-cli")
	}
}

func TestInit_RefusesOverwriteWithoutForce(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "agentctl")

	// First init
	_, err := Init(InitConfig{
		ConfigDir:      configDir,
		SelectedAgents: []string{"claude-code"},
		Force:          false,
		DryRun:         false,
	})
	if err != nil {
		t.Fatalf("first Init failed: %v", err)
	}

	// Second init without force should fail
	_, err = Init(InitConfig{
		ConfigDir:      configDir,
		SelectedAgents: []string{"claude-code"},
		Force:          false,
		DryRun:         false,
	})
	if err == nil {
		t.Fatal("expected error on second Init without --force")
	}
}

func TestInit_ForceOverwriteWorks(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "agentctl")

	// First init
	_, err := Init(InitConfig{
		ConfigDir:      configDir,
		SelectedAgents: []string{"claude-code"},
		Force:          false,
		DryRun:         false,
	})
	if err != nil {
		t.Fatalf("first Init failed: %v", err)
	}

	// Second init with force should succeed
	result, err := Init(InitConfig{
		ConfigDir:      configDir,
		SelectedAgents: []string{"claude-code", "gemini-cli"},
		Force:          true,
		DryRun:         false,
	})
	if err != nil {
		t.Fatalf("second Init with force failed: %v", err)
	}

	// Verify updated profiles
	data, err := os.ReadFile(filepath.Join(configDir, "mcp", "profiles.json"))
	if err != nil {
		t.Fatalf("failed to read profiles.json: %v", err)
	}
	var profiles map[string]any
	if err := json.Unmarshal(data, &profiles); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	agentsMap := profiles["agents"].(map[string]any)
	if len(agentsMap) != 2 {
		t.Errorf("expected 2 agents after force overwrite, got %d", len(agentsMap))
	}

	if len(result.SelectedAgents) != 2 {
		t.Errorf("expected 2 selected agents, got %d", len(result.SelectedAgents))
	}
}

func TestInit_DryRunDoesNotWrite(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "agentctl")

	result, err := Init(InitConfig{
		ConfigDir:      configDir,
		SelectedAgents: []string{"claude-code"},
		Force:          false,
		DryRun:         true,
	})
	if err != nil {
		t.Fatalf("Init dry-run failed: %v", err)
	}

	// Should have non-empty lists of what would be created
	if len(result.CreatedDirs) == 0 {
		t.Error("dry-run should report directories that would be created")
	}
	if len(result.CreatedFiles) == 0 {
		t.Error("dry-run should report files that would be created")
	}

	// But nothing should actually exist
	if _, err := os.Stat(configDir); !os.IsNotExist(err) {
		t.Errorf("dry-run should not create config directory, but it exists")
	}
}
