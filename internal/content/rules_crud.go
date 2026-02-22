package content

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agentctl/internal/tx"
)

// RulesList returns rule source files and per-agent compose mappings.
func RulesList(configDir string) (map[string]any, error) {
	cfg, err := loadRulesConfig(configDir)
	if err != nil {
		return nil, err
	}

	rulesDir := filepath.Join(configDir, "rules")
	var sourceFiles []string
	if entries, err := os.ReadDir(rulesDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == "config.json" {
				continue
			}
			sourceFiles = append(sourceFiles, e.Name())
		}
	}

	agents := make(map[string]any)
	for _, name := range sortedMapKeys(tx.GetMap(cfg, "agents")) {
		agentCfgRaw := tx.GetMap(cfg, "agents")[name]
		agentCfg, ok := agentCfgRaw.(map[string]any)
		if !ok {
			continue
		}
		agents[name] = map[string]any{
			"compose":   tx.GetStringSlice(agentCfg, "compose"),
			"target":    tx.GetString(agentCfg, "target", ""),
			"separator": tx.GetString(agentCfg, "separator", "\n\n"),
		}
	}

	return map[string]any{
		"source_files": sourceFiles,
		"agents":       agents,
	}, nil
}

// RulesAdd adds a source filename to an agent's compose list.
// When the agent does not exist, target is required to create the entry.
func RulesAdd(configDir, filename, agent string, target, separator string) (map[string]any, error) {
	cfg, err := loadRulesConfig(configDir)
	if err != nil {
		return nil, err
	}

	rulesDir := filepath.Join(configDir, "rules")

	// Validate filename safety
	if strings.HasPrefix(filename, "/") {
		return nil, fmt.Errorf("invalid filename (absolute path): %s", filename)
	}
	cleanParts := strings.Split(filepath.Clean(filename), string(filepath.Separator))
	for _, part := range cleanParts {
		if part == ".." {
			return nil, fmt.Errorf("invalid filename (path traversal): %s", filename)
		}
	}

	resolvedDir, _ := filepath.Abs(rulesDir)
	resolvedDirReal, err := filepath.EvalSymlinks(resolvedDir)
	if err != nil {
		resolvedDirReal = resolvedDir
	}
	resolved, _ := filepath.Abs(filepath.Join(resolvedDir, filename))
	resolvedReal, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("source file not found: %s", filename)
		}
		return nil, fmt.Errorf("cannot resolve path: %w", err)
	}
	if !strings.HasPrefix(resolvedReal, resolvedDirReal+string(filepath.Separator)) && resolvedReal != resolvedDirReal {
		return nil, fmt.Errorf("filename escapes rules dir: %s", filename)
	}
	if _, err := os.Stat(resolvedReal); os.IsNotExist(err) {
		return nil, fmt.Errorf("source file not found: %s", filename)
	}

	agents := tx.GetMap(cfg, "agents")
	if agents == nil {
		agents = make(map[string]any)
		cfg["agents"] = agents
	}

	createdAgent := false
	if _, exists := agents[agent]; !exists {
		if target == "" {
			return nil, fmt.Errorf("agent %q does not exist; --target is required to create it", agent)
		}
		entry := map[string]any{
			"compose": []any{},
			"target":  target,
		}
		if separator != "" {
			entry["separator"] = separator
		}
		agents[agent] = entry
		createdAgent = true
	}

	agentCfg, ok := agents[agent].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("agent %q: invalid config", agent)
	}

	// Check for duplicate
	compose := tx.GetStringSlice(agentCfg, "compose")
	for _, f := range compose {
		if f == filename {
			return nil, fmt.Errorf("filename already in compose list: %s", filename)
		}
	}

	// Append
	composeAny, _ := agentCfg["compose"].([]any)
	composeAny = append(composeAny, filename)
	agentCfg["compose"] = composeAny

	// Re-validate
	if err := validateRulesConfig(cfg); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	configPath := filepath.Join(rulesDir, "config.json")
	if err := tx.WriteJSONAtomic(configPath, cfg); err != nil {
		return nil, fmt.Errorf("write config: %w", err)
	}

	// Build result compose list
	resultCompose := tx.GetStringSlice(agentCfg, "compose")

	return map[string]any{
		"op":            "add",
		"agent":         agent,
		"filename":      filename,
		"created_agent": createdAgent,
		"compose":       resultCompose,
	}, nil
}

// RulesRm removes a source filename from an agent's compose list.
// If the compose list becomes empty, the agent entry is deleted.
func RulesRm(configDir, filename, agent string) (map[string]any, error) {
	cfg, err := loadRulesConfig(configDir)
	if err != nil {
		return nil, err
	}

	agents := tx.GetMap(cfg, "agents")
	if agents == nil {
		return nil, fmt.Errorf("agent not found: %s", agent)
	}

	agentCfgRaw, exists := agents[agent]
	if !exists {
		return nil, fmt.Errorf("agent not found: %s", agent)
	}
	agentCfg, ok := agentCfgRaw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("agent %q: invalid config", agent)
	}

	compose := tx.GetStringSlice(agentCfg, "compose")
	found := false
	var newCompose []any
	for _, f := range compose {
		if f == filename {
			found = true
			continue
		}
		newCompose = append(newCompose, f)
	}
	if !found {
		return nil, fmt.Errorf("filename not in compose list: %s", filename)
	}

	removedAgent := len(newCompose) == 0
	if removedAgent {
		delete(agents, agent)
	} else {
		agentCfg["compose"] = newCompose
	}

	configPath := filepath.Join(configDir, "rules", "config.json")
	if err := tx.WriteJSONAtomic(configPath, cfg); err != nil {
		return nil, fmt.Errorf("write config: %w", err)
	}

	return map[string]any{
		"op":            "rm",
		"agent":         agent,
		"filename":      filename,
		"removed_agent": removedAgent,
	}, nil
}
