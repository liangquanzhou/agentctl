package validate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func configFixture() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "config")
}

// ── Missing files ────────────────────────────────────────────────────

func TestValidateMissingFiles(t *testing.T) {
	tmp := t.TempDir()

	ok, errs := ValidateConfig(tmp)
	if ok {
		t.Fatal("ValidateConfig should fail for empty dir, got ok=true")
	}

	if len(errs) == 0 {
		t.Fatal("ValidateConfig returned no errors for empty dir")
	}

	// Should report missing MCP files: servers.json, profiles.json, compat.json.
	foundMissing := map[string]bool{
		"servers.json":  false,
		"profiles.json": false,
		"compat.json":   false,
	}
	for _, errMsg := range errs {
		for filename := range foundMissing {
			if strings.Contains(errMsg, filename) && strings.Contains(errMsg, "missing file") {
				foundMissing[filename] = true
			}
		}
	}
	for filename, found := range foundMissing {
		if !found {
			t.Errorf("expected missing file error for %q, not found in: %v", filename, errs)
		}
	}
}

// ── Fixture config passes validation ─────────────────────────────────

func TestValidateFixtureOK(t *testing.T) {
	ok, errs := ValidateConfig(configFixture())
	if !ok {
		t.Errorf("ValidateConfig should pass for fixture config, got errors: %v", errs)
	}
	if len(errs) != 0 {
		t.Errorf("ValidateConfig returned errors for valid fixture: %v", errs)
	}
}

// ── legacy_enabled=false requires empty maps ─────────────────────────

func TestValidateLegacyFalseRequiresEmptyMaps(t *testing.T) {
	tmp := t.TempDir()
	mcpDir := filepath.Join(tmp, "mcp")
	os.MkdirAll(mcpDir, 0o755)

	// servers.json -- valid, with one server.
	writeJSON(t, filepath.Join(mcpDir, "servers.json"), map[string]any{
		"schema_version": "1.0.0",
		"generated_at":   "2026-02-15T00:00:00Z",
		"managed_by":     "agentctl",
		"servers": map[string]any{
			"test-server": map[string]any{
				"runtime": "node",
				"command": "/usr/bin/test",
			},
		},
	})

	// profiles.json -- references the server correctly.
	writeJSON(t, filepath.Join(mcpDir, "profiles.json"), map[string]any{
		"schema_version": "1.0.0",
		"generated_at":   "2026-02-15T00:00:00Z",
		"managed_by":     "agentctl",
		"agents": map[string]any{
			"claude-code": map[string]any{
				"servers": []any{"test-server"},
			},
		},
		"servers": map[string]any{},
	})

	// compat.json -- legacy_enabled=false BUT non-empty maps.
	writeJSON(t, filepath.Join(mcpDir, "compat.json"), map[string]any{
		"schema_version": "1.0.0",
		"generated_at":   "2026-02-15T00:00:00Z",
		"managed_by":     "agentctl",
		"legacy_enabled":  false,
		"profilesPerAgent_map": map[string]any{
			"claude-code": "default",
		},
		"disabled_map": map[string]any{},
	})

	ok, errs := ValidateConfig(tmp)
	if ok {
		t.Fatal("ValidateConfig should fail when legacy_enabled=false with non-empty maps")
	}

	foundLegacyErr := false
	for _, errMsg := range errs {
		if strings.Contains(errMsg, "legacy_enabled=false") {
			foundLegacyErr = true
			break
		}
	}
	if !foundLegacyErr {
		t.Errorf("expected legacy_enabled constraint error, got: %v", errs)
	}
}

// ── legacy_enabled=false with empty maps is OK ───────────────────────

func TestValidateLegacyFalseEmptyMapsOK(t *testing.T) {
	tmp := t.TempDir()
	mcpDir := filepath.Join(tmp, "mcp")
	os.MkdirAll(mcpDir, 0o755)

	writeJSON(t, filepath.Join(mcpDir, "servers.json"), map[string]any{
		"schema_version": "1.0.0",
		"generated_at":   "2026-02-15T00:00:00Z",
		"managed_by":     "agentctl",
		"servers":        map[string]any{},
	})

	writeJSON(t, filepath.Join(mcpDir, "profiles.json"), map[string]any{
		"schema_version": "1.0.0",
		"generated_at":   "2026-02-15T00:00:00Z",
		"managed_by":     "agentctl",
		"agents":         map[string]any{},
		"servers":        map[string]any{},
	})

	writeJSON(t, filepath.Join(mcpDir, "compat.json"), map[string]any{
		"schema_version":        "1.0.0",
		"generated_at":          "2026-02-15T00:00:00Z",
		"managed_by":            "agentctl",
		"legacy_enabled":        false,
		"profilesPerAgent_map":  map[string]any{},
		"disabled_map":          map[string]any{},
	})

	ok, errs := ValidateConfig(tmp)
	if !ok {
		t.Errorf("ValidateConfig should pass with legacy_enabled=false and empty maps, got errors: %v", errs)
	}
}

// ── Invalid JSON triggers error ──────────────────────────────────────

func TestValidateInvalidJSON(t *testing.T) {
	tmp := t.TempDir()
	mcpDir := filepath.Join(tmp, "mcp")
	os.MkdirAll(mcpDir, 0o755)

	// Write valid servers.json and profiles.json, but broken compat.json.
	writeJSON(t, filepath.Join(mcpDir, "servers.json"), map[string]any{
		"schema_version": "1.0.0",
		"generated_at":   "2026-02-15T00:00:00Z",
		"managed_by":     "agentctl",
		"servers":        map[string]any{},
	})

	writeJSON(t, filepath.Join(mcpDir, "profiles.json"), map[string]any{
		"schema_version": "1.0.0",
		"generated_at":   "2026-02-15T00:00:00Z",
		"managed_by":     "agentctl",
		"agents":         map[string]any{},
		"servers":        map[string]any{},
	})

	// Write invalid JSON to compat.json.
	os.WriteFile(filepath.Join(mcpDir, "compat.json"), []byte(`{broken json`), 0o644)

	ok, errs := ValidateConfig(tmp)
	if ok {
		t.Fatal("ValidateConfig should fail with invalid JSON, got ok=true")
	}

	foundJSONErr := false
	for _, errMsg := range errs {
		if strings.Contains(errMsg, "invalid json") {
			foundJSONErr = true
			break
		}
	}
	if !foundJSONErr {
		t.Errorf("expected 'invalid json' error, got: %v", errs)
	}
}

// ── Missing schema_version triggers error ────────────────────────────

func TestValidateMissingSchemaVersion(t *testing.T) {
	tmp := t.TempDir()
	mcpDir := filepath.Join(tmp, "mcp")
	os.MkdirAll(mcpDir, 0o755)

	// servers.json missing schema_version.
	writeJSON(t, filepath.Join(mcpDir, "servers.json"), map[string]any{
		"generated_at": "2026-02-15T00:00:00Z",
		"managed_by":   "agentctl",
		"servers":      map[string]any{},
	})

	writeJSON(t, filepath.Join(mcpDir, "profiles.json"), map[string]any{
		"schema_version": "1.0.0",
		"generated_at":   "2026-02-15T00:00:00Z",
		"managed_by":     "agentctl",
		"agents":         map[string]any{},
		"servers":        map[string]any{},
	})

	writeJSON(t, filepath.Join(mcpDir, "compat.json"), map[string]any{
		"schema_version":        "1.0.0",
		"generated_at":          "2026-02-15T00:00:00Z",
		"managed_by":            "agentctl",
		"legacy_enabled":        true,
		"profilesPerAgent_map":  map[string]any{},
		"disabled_map":          map[string]any{},
	})

	ok, errs := ValidateConfig(tmp)
	if ok {
		t.Fatal("ValidateConfig should fail with missing schema_version")
	}

	foundSchemaErr := false
	for _, errMsg := range errs {
		if strings.Contains(errMsg, "schema_version") {
			foundSchemaErr = true
			break
		}
	}
	if !foundSchemaErr {
		t.Errorf("expected schema_version error, got: %v", errs)
	}
}

// ── Unknown server reference triggers error ──────────────────────────

func TestValidateUnknownServerReference(t *testing.T) {
	tmp := t.TempDir()
	mcpDir := filepath.Join(tmp, "mcp")
	os.MkdirAll(mcpDir, 0o755)

	writeJSON(t, filepath.Join(mcpDir, "servers.json"), map[string]any{
		"schema_version": "1.0.0",
		"generated_at":   "2026-02-15T00:00:00Z",
		"managed_by":     "agentctl",
		"servers":        map[string]any{},
	})

	// profiles.json references "nonexistent" server.
	writeJSON(t, filepath.Join(mcpDir, "profiles.json"), map[string]any{
		"schema_version": "1.0.0",
		"generated_at":   "2026-02-15T00:00:00Z",
		"managed_by":     "agentctl",
		"agents": map[string]any{
			"claude-code": map[string]any{
				"servers": []any{"nonexistent"},
			},
		},
		"servers": map[string]any{},
	})

	writeJSON(t, filepath.Join(mcpDir, "compat.json"), map[string]any{
		"schema_version":        "1.0.0",
		"generated_at":          "2026-02-15T00:00:00Z",
		"managed_by":            "agentctl",
		"legacy_enabled":        true,
		"profilesPerAgent_map":  map[string]any{},
		"disabled_map":          map[string]any{},
	})

	ok, errs := ValidateConfig(tmp)
	if ok {
		t.Fatal("ValidateConfig should fail with unknown server reference")
	}

	foundRef := false
	for _, errMsg := range errs {
		if strings.Contains(errMsg, "unknown server") && strings.Contains(errMsg, "nonexistent") {
			foundRef = true
			break
		}
	}
	if !foundRef {
		t.Errorf("expected unknown server reference error, got: %v", errs)
	}
}

// ── Optional config schema validation ─────────────────────────────────

func TestValidateRulesSchema_MissingAgents(t *testing.T) {
	errs := validateRulesSchema(map[string]any{})
	if len(errs) == 0 {
		t.Fatal("should report missing 'agents' key")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e, "agents") {
			found = true
		}
	}
	if !found {
		t.Errorf("should mention 'agents', got: %v", errs)
	}
}

func TestValidateRulesSchema_MissingComposeAndTarget(t *testing.T) {
	errs := validateRulesSchema(map[string]any{
		"agents": map[string]any{
			"test-agent": map[string]any{},
		},
	})
	if len(errs) < 2 {
		t.Fatalf("should report missing compose and target, got %d errors: %v", len(errs), errs)
	}
	foundCompose, foundTarget := false, false
	for _, e := range errs {
		if strings.Contains(e, "compose") {
			foundCompose = true
		}
		if strings.Contains(e, "target") {
			foundTarget = true
		}
	}
	if !foundCompose {
		t.Error("should mention missing 'compose'")
	}
	if !foundTarget {
		t.Error("should mention missing 'target'")
	}
}

func TestValidateRulesSchema_ValidConfig(t *testing.T) {
	errs := validateRulesSchema(map[string]any{
		"agents": map[string]any{
			"test-agent": map[string]any{
				"compose": []any{"a.md"},
				"target":  "~/test",
			},
		},
	})
	if len(errs) != 0 {
		t.Errorf("valid config should produce no errors, got: %v", errs)
	}
}

func TestValidateHooksSchema_MissingAgents(t *testing.T) {
	errs := validateHooksSchema(map[string]any{})
	if len(errs) == 0 {
		t.Fatal("should report missing 'agents' key")
	}
}

func TestValidateHooksSchema_InvalidFormat(t *testing.T) {
	errs := validateHooksSchema(map[string]any{
		"agents": map[string]any{
			"test-agent": map[string]any{
				"target": "~/test",
				"format": "invalid_format",
			},
		},
	})
	if len(errs) == 0 {
		t.Fatal("should reject unknown format")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e, "format must be one of") {
			found = true
		}
	}
	if !found {
		t.Errorf("should mention valid format options, got: %v", errs)
	}
}

func TestValidateHooksSchema_ValidClaudeHooks(t *testing.T) {
	errs := validateHooksSchema(map[string]any{
		"agents": map[string]any{
			"claude-code": map[string]any{
				"target": "~/test",
				"format": "claude_hooks",
			},
		},
	})
	if len(errs) != 0 {
		t.Errorf("valid claude_hooks config should pass, got: %v", errs)
	}
}

func TestValidateCommandsSchema_MissingAgents(t *testing.T) {
	errs := validateCommandsSchema(map[string]any{})
	if len(errs) == 0 {
		t.Fatal("should report missing 'agents' key")
	}
}

func TestValidateCommandsSchema_MissingTargetDir(t *testing.T) {
	errs := validateCommandsSchema(map[string]any{
		"agents": map[string]any{
			"test-agent": map[string]any{},
		},
	})
	if len(errs) == 0 {
		t.Fatal("should report missing 'target_dir'")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e, "target_dir") {
			found = true
		}
	}
	if !found {
		t.Errorf("should mention 'target_dir', got: %v", errs)
	}
}

func TestValidateIgnoreSchema_MissingPatternsAndAgents(t *testing.T) {
	errs := validateIgnoreSchema(map[string]any{})
	if len(errs) < 2 {
		t.Fatalf("should report missing patterns and agents, got %d errors: %v", len(errs), errs)
	}
}

func TestValidateIgnoreSchema_ValidConfig(t *testing.T) {
	errs := validateIgnoreSchema(map[string]any{
		"patterns": []any{"node_modules", ".env"},
		"agents": map[string]any{
			"test-agent": map[string]any{
				"target": "~/test",
			},
		},
	})
	if len(errs) != 0 {
		t.Errorf("valid ignore config should pass, got: %v", errs)
	}
}

// ── Target escapes home detection ─────────────────────────────────────

func TestValidateConfig_DetectsRulesTargetEscapingHome(t *testing.T) {
	tmp := t.TempDir()
	mcpDir := filepath.Join(tmp, "mcp")
	os.MkdirAll(mcpDir, 0o755)

	// Set up valid MCP files
	writeMCPFiles(t, mcpDir)

	// Rules config with target escaping home
	rulesDir := filepath.Join(tmp, "rules")
	os.MkdirAll(rulesDir, 0o755)
	writeJSON(t, filepath.Join(rulesDir, "config.json"), map[string]any{
		"agents": map[string]any{
			"evil-agent": map[string]any{
				"compose": []any{"a.md"},
				"target":  "/etc/evil-rules.md",
			},
		},
	})

	ok, errs := ValidateConfig(tmp)
	if ok {
		t.Fatal("should fail when rules target escapes home")
	}
	foundEscape := false
	for _, e := range errs {
		if strings.Contains(e, "escapes home") {
			foundEscape = true
		}
	}
	if !foundEscape {
		t.Errorf("should mention 'escapes home', got: %v", errs)
	}
}

func TestValidateConfig_DetectsHooksTargetEscapingHome(t *testing.T) {
	tmp := t.TempDir()
	mcpDir := filepath.Join(tmp, "mcp")
	os.MkdirAll(mcpDir, 0o755)

	writeMCPFiles(t, mcpDir)

	hooksDir := filepath.Join(tmp, "hooks")
	os.MkdirAll(hooksDir, 0o755)
	writeJSON(t, filepath.Join(hooksDir, "config.json"), map[string]any{
		"agents": map[string]any{
			"evil-agent": map[string]any{
				"target": "/tmp/evil-hooks.json",
				"format": "claude_hooks",
			},
		},
	})

	ok, errs := ValidateConfig(tmp)
	if ok {
		t.Fatal("should fail when hooks target escapes home")
	}
	foundEscape := false
	for _, e := range errs {
		if strings.Contains(e, "escapes home") {
			foundEscape = true
		}
	}
	if !foundEscape {
		t.Errorf("should mention 'escapes home', got: %v", errs)
	}
}

func TestValidateConfig_DetectsCommandsTargetEscapingHome(t *testing.T) {
	tmp := t.TempDir()
	mcpDir := filepath.Join(tmp, "mcp")
	os.MkdirAll(mcpDir, 0o755)

	writeMCPFiles(t, mcpDir)

	cmdsDir := filepath.Join(tmp, "commands")
	os.MkdirAll(cmdsDir, 0o755)
	writeJSON(t, filepath.Join(cmdsDir, "config.json"), map[string]any{
		"agents": map[string]any{
			"evil-agent": map[string]any{
				"target_dir": "/tmp/evil-commands",
			},
		},
	})

	ok, errs := ValidateConfig(tmp)
	if ok {
		t.Fatal("should fail when commands target_dir escapes home")
	}
	foundEscape := false
	for _, e := range errs {
		if strings.Contains(e, "escapes home") {
			foundEscape = true
		}
	}
	if !foundEscape {
		t.Errorf("should mention 'escapes home', got: %v", errs)
	}
}

func TestValidateConfig_DetectsIgnoreTargetEscapingHome(t *testing.T) {
	tmp := t.TempDir()
	mcpDir := filepath.Join(tmp, "mcp")
	os.MkdirAll(mcpDir, 0o755)

	writeMCPFiles(t, mcpDir)

	writeJSON(t, filepath.Join(tmp, "ignore.json"), map[string]any{
		"patterns": []any{"node_modules"},
		"agents": map[string]any{
			"evil-agent": map[string]any{
				"target": "/tmp/evil-ignore",
			},
		},
	})

	ok, errs := ValidateConfig(tmp)
	if ok {
		t.Fatal("should fail when ignore target escapes home")
	}
	foundEscape := false
	for _, e := range errs {
		if strings.Contains(e, "escapes home") {
			foundEscape = true
		}
	}
	if !foundEscape {
		t.Errorf("should mention 'escapes home', got: %v", errs)
	}
}

// ── Duplicate target detection ────────────────────────────────────────

func TestValidateConfig_DetectsDuplicateRulesTarget(t *testing.T) {
	tmp := t.TempDir()
	mcpDir := filepath.Join(tmp, "mcp")
	os.MkdirAll(mcpDir, 0o755)
	writeMCPFiles(t, mcpDir)

	rulesDir := filepath.Join(tmp, "rules")
	os.MkdirAll(rulesDir, 0o755)
	writeJSON(t, filepath.Join(rulesDir, "config.json"), map[string]any{
		"agents": map[string]any{
			"agent-a": map[string]any{
				"compose": []any{"a.md"},
				"target":  "~/shared-rules.md",
			},
			"agent-b": map[string]any{
				"compose": []any{"a.md"},
				"target":  "~/shared-rules.md",
			},
		},
	})

	ok, errs := ValidateConfig(tmp)
	if ok {
		t.Fatal("should fail with duplicate rules targets")
	}
	foundDup := false
	for _, e := range errs {
		if strings.Contains(e, "duplicate target") {
			foundDup = true
		}
	}
	if !foundDup {
		t.Errorf("should mention 'duplicate target', got: %v", errs)
	}
}

// ── Optional config invalid JSON ──────────────────────────────────────

func TestValidateConfig_InvalidOptionalConfigJSON(t *testing.T) {
	tmp := t.TempDir()
	mcpDir := filepath.Join(tmp, "mcp")
	os.MkdirAll(mcpDir, 0o755)
	writeMCPFiles(t, mcpDir)

	// Write broken JSON for rules config
	rulesDir := filepath.Join(tmp, "rules")
	os.MkdirAll(rulesDir, 0o755)
	os.WriteFile(filepath.Join(rulesDir, "config.json"), []byte("{broken json"), 0o644)

	ok, errs := ValidateConfig(tmp)
	if ok {
		t.Fatal("should fail with invalid optional config JSON")
	}
	foundJSON := false
	for _, e := range errs {
		if strings.Contains(e, "invalid json") {
			foundJSON = true
		}
	}
	if !foundJSON {
		t.Errorf("should mention 'invalid json', got: %v", errs)
	}
}

// ── helpers ──────────────────────────────────────────────────────────

// writeMCPFiles creates valid MCP config files (servers, profiles, compat).
func writeMCPFiles(t *testing.T, mcpDir string) {
	t.Helper()
	writeJSON(t, filepath.Join(mcpDir, "servers.json"), map[string]any{
		"schema_version": "1.0.0",
		"generated_at":   "2026-02-15T00:00:00Z",
		"managed_by":     "agentctl",
		"servers":        map[string]any{},
	})
	writeJSON(t, filepath.Join(mcpDir, "profiles.json"), map[string]any{
		"schema_version": "1.0.0",
		"generated_at":   "2026-02-15T00:00:00Z",
		"managed_by":     "agentctl",
		"agents":         map[string]any{},
		"servers":        map[string]any{},
	})
	writeJSON(t, filepath.Join(mcpDir, "compat.json"), map[string]any{
		"schema_version":       "1.0.0",
		"generated_at":         "2026-02-15T00:00:00Z",
		"managed_by":           "agentctl",
		"legacy_enabled":       true,
		"profilesPerAgent_map": map[string]any{},
		"disabled_map":         map[string]any{},
	})
}

func writeJSON(t *testing.T, path string, data map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("failed to create dir for %s: %v", path, err)
	}
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal json for %s: %v", path, err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}
