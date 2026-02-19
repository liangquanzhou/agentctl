// Package validate provides config validation for agentctl.
package validate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agentctl/internal/tx"
)

// MCP schema files that must exist.
var mcpFiles = []string{"servers.json", "profiles.json", "compat.json"}

// ValidateConfig validates the concept-based config directory layout.
// Returns (ok, errors).
func ValidateConfig(configDir string) (bool, []string) {
	var errors []string
	loaded := make(map[string]map[string]any)
	mcpDir := filepath.Join(configDir, "mcp")

	// ── MCP files ────────────────────────────────────────────────────
	for _, filename := range mcpFiles {
		path := filepath.Join(mcpDir, filename)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			errors = append(errors, fmt.Sprintf("missing file: %s", path))
			continue
		}
		data, err := loadJSON(path)
		if err != nil {
			errors = append(errors, fmt.Sprintf("invalid json in %s: %v", path, err))
			continue
		}
		// Basic schema validation
		if schemaErr := validateMCPSchema(filename, data); schemaErr != "" {
			errors = append(errors, fmt.Sprintf("schema error in %s: %s", path, schemaErr))
			continue
		}
		loaded[filename] = data
	}

	// ── Optional split config files ──────────────────────────────────
	optionalConfigs := []struct {
		path string
		key  string
	}{
		{filepath.Join(configDir, "rules", "config.json"), "rules/config.json"},
		{filepath.Join(configDir, "hooks", "config.json"), "hooks/config.json"},
		{filepath.Join(configDir, "commands", "config.json"), "commands/config.json"},
		{filepath.Join(configDir, "ignore.json"), "ignore.json"},
	}
	for _, cfg := range optionalConfigs {
		if _, err := os.Stat(cfg.path); os.IsNotExist(err) {
			continue
		}
		data, err := loadJSON(cfg.path)
		if err != nil {
			errors = append(errors, fmt.Sprintf("invalid json in %s: %v", cfg.path, err))
			continue
		}
		loaded[cfg.key] = data
	}

	// ── Cross-file checks (MCP) ─────────────────────────────────────
	if len(errors) == 0 {
		if servers, ok := loaded["servers.json"]; ok {
			if profiles, ok := loaded["profiles.json"]; ok {
				if compat, ok := loaded["compat.json"]; ok {
					errors = append(errors, crossCheckMCP(servers, profiles, compat)...)
				}
			}
		}
	}

	// ── Cross-file checks (content) ─────────────────────────────────
	home := tx.HomeDir()
	seenTargets := make(map[string]bool)

	if rulesCfg, ok := loaded["rules/config.json"]; ok {
		agents := tx.GetMap(rulesCfg, "agents")
		for agent, val := range agents {
			ruleCfg, ok := val.(map[string]any)
			if !ok {
				continue
			}
			target := tx.GetString(ruleCfg, "target", "")
			resolved := resolveForValidation(target)
			if !strings.HasPrefix(resolved, home) {
				errors = append(errors, fmt.Sprintf("rules/config.json agent '%s': target escapes home: %s", agent, target))
			}
			if seenTargets[resolved] {
				errors = append(errors, fmt.Sprintf("rules/config.json: duplicate target %s", target))
			}
			seenTargets[resolved] = true
		}
	}

	if hooksCfg, ok := loaded["hooks/config.json"]; ok {
		agents := tx.GetMap(hooksCfg, "agents")
		for agent, val := range agents {
			hookCfg, ok := val.(map[string]any)
			if !ok {
				continue
			}
			target := tx.GetString(hookCfg, "target", "")
			resolved := resolveForValidation(target)
			if !strings.HasPrefix(resolved, home) {
				errors = append(errors, fmt.Sprintf("hooks/config.json agent '%s': target escapes home: %s", agent, target))
			}
			hooksKey := resolved + "#hooks"
			if seenTargets[hooksKey] {
				errors = append(errors, fmt.Sprintf("hooks/config.json: duplicate hooks target %s", target))
			}
			seenTargets[hooksKey] = true
		}
	}

	return len(errors) == 0, errors
}

func loadJSON(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func resolveForValidation(path string) string {
	expanded := tx.ExpandUser(path)
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return expanded
	}
	return abs
}

func validateMCPSchema(filename string, data map[string]any) string {
	// Check required meta fields
	for _, field := range []string{"schema_version", "generated_at", "managed_by"} {
		if _, ok := data[field]; !ok {
			return fmt.Sprintf("missing required field: %s", field)
		}
	}

	switch filename {
	case "servers.json":
		if servers, ok := data["servers"]; ok {
			if _, ok := servers.(map[string]any); !ok {
				return "servers must be an object"
			}
		}
	case "profiles.json":
		if agents, ok := data["agents"]; ok {
			if _, ok := agents.(map[string]any); !ok {
				return "agents must be an object"
			}
		}
	case "compat.json":
		// legacy_enabled should be bool
		if le, ok := data["legacy_enabled"]; ok {
			if _, ok := le.(bool); !ok {
				return "legacy_enabled must be a boolean"
			}
		}
	}
	return ""
}

func crossCheckMCP(servers, profiles, compat map[string]any) []string {
	var errors []string

	serverSpecs := tx.GetMap(servers, "servers")
	if serverSpecs == nil {
		serverSpecs = make(map[string]any)
	}
	serverNames := make(map[string]bool)
	for name := range serverSpecs {
		serverNames[name] = true
	}

	// Check profiles reference valid servers
	profilesAgents := tx.GetMap(profiles, "agents")
	for agent, val := range profilesAgents {
		profile, ok := val.(map[string]any)
		if !ok {
			continue
		}
		for _, serverName := range tx.GetStringSlice(profile, "servers") {
			if !serverNames[serverName] {
				errors = append(errors, fmt.Sprintf(
					"profiles.json references unknown server '%s' in agent '%s'",
					serverName, agent,
				))
			}
		}
	}

	// Check compat constraints
	legacyEnabled := tx.GetBool(compat, "legacy_enabled", true)
	if !legacyEnabled {
		pam := tx.GetMap(compat, "profilesPerAgent_map")
		dm := tx.GetMap(compat, "disabled_map")
		if len(pam) > 0 || len(dm) > 0 {
			errors = append(errors, "compat.json invalid: legacy_enabled=false requires empty profilesPerAgent_map and disabled_map")
		}
	}

	return errors
}
