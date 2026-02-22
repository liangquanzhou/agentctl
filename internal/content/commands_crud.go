package content

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agentctl/internal/tx"
)

// CommandsList returns command source files and per-agent target directories.
func CommandsList(configDir string) (map[string]any, error) {
	cfg, err := loadCommandsConfig(configDir)
	if err != nil {
		return nil, err
	}

	commandsDir := filepath.Join(configDir, "commands")
	var sourceFiles []string
	if entries, err := os.ReadDir(commandsDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == "config.json" {
				continue
			}
			sourceFiles = append(sourceFiles, e.Name())
		}
	}

	agents := make(map[string]any)
	agentsCfg := tx.GetMap(cfg, "agents")
	for _, name := range sortedMapKeys(agentsCfg) {
		cmdCfgRaw := agentsCfg[name]
		cmdCfg, ok := cmdCfgRaw.(map[string]any)
		if !ok {
			continue
		}
		agents[name] = map[string]any{
			"target_dir": tx.GetString(cmdCfg, "target_dir", ""),
		}
	}

	return map[string]any{
		"source_files": sourceFiles,
		"agents":       agents,
	}, nil
}

// CommandsAdd registers an agent's command sync target directory.
func CommandsAdd(configDir, agent, targetDir string) (map[string]any, error) {
	cfg, err := loadCommandsConfig(configDir)
	if err != nil {
		return nil, err
	}

	agents := tx.GetMap(cfg, "agents")
	if agents == nil {
		agents = make(map[string]any)
		cfg["agents"] = agents
	}

	if _, exists := agents[agent]; exists {
		return nil, fmt.Errorf("agent already registered: %s", agent)
	}

	// Validate path safety
	if _, err := resolvePath(targetDir); err != nil {
		return nil, fmt.Errorf("invalid target_dir: %w", err)
	}

	agents[agent] = map[string]any{"target_dir": targetDir}

	configPath := filepath.Join(configDir, "commands", "config.json")
	if err := tx.WriteJSONAtomic(configPath, cfg); err != nil {
		return nil, fmt.Errorf("write config: %w", err)
	}

	return map[string]any{
		"op":         "add",
		"agent":      agent,
		"target_dir": targetDir,
	}, nil
}

// CommandsRm removes an agent from commands config.
func CommandsRm(configDir, agent string) (map[string]any, error) {
	cfg, err := loadCommandsConfig(configDir)
	if err != nil {
		return nil, err
	}

	agents := tx.GetMap(cfg, "agents")
	if agents == nil {
		return nil, fmt.Errorf("agent not found: %s", agent)
	}

	if _, exists := agents[agent]; !exists {
		return nil, fmt.Errorf("agent not found: %s", agent)
	}

	delete(agents, agent)

	configPath := filepath.Join(configDir, "commands", "config.json")
	if err := tx.WriteJSONAtomic(configPath, cfg); err != nil {
		return nil, fmt.Errorf("write config: %w", err)
	}

	return map[string]any{
		"op":    "rm",
		"agent": agent,
	}, nil
}
