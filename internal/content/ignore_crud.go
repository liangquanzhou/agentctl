package content

import (
	"fmt"
	"path/filepath"

	"agentctl/internal/tx"
)

// IgnoreList returns ignore patterns and per-agent target paths.
func IgnoreList(configDir string) (map[string]any, error) {
	cfg, err := loadIgnoreConfig(configDir)
	if err != nil {
		return nil, err
	}

	patternsRaw, _ := cfg["patterns"].([]any)
	patterns := make([]string, 0, len(patternsRaw))
	for _, p := range patternsRaw {
		if s, ok := p.(string); ok {
			patterns = append(patterns, s)
		}
	}

	agents := make(map[string]any)
	agentsCfg := tx.GetMap(cfg, "agents")
	for _, name := range sortedMapKeys(agentsCfg) {
		ignCfgRaw := agentsCfg[name]
		ignCfg, ok := ignCfgRaw.(map[string]any)
		if !ok {
			continue
		}
		agents[name] = map[string]any{
			"target": tx.GetString(ignCfg, "target", ""),
		}
	}

	return map[string]any{
		"patterns": patterns,
		"agents":   agents,
	}, nil
}

// IgnoreAdd adds an ignore pattern or agent target.
// Exactly one of pattern or (agent+target) must be provided.
func IgnoreAdd(configDir string, opts IgnoreAddOpts) (map[string]any, error) {
	if opts.Pattern != "" && opts.Agent != "" {
		return nil, fmt.Errorf("pattern and --agent are mutually exclusive")
	}
	if opts.Pattern == "" && opts.Agent == "" {
		return nil, fmt.Errorf("either pattern or --agent+--target is required")
	}

	cfg, err := loadIgnoreConfig(configDir)
	if err != nil {
		return nil, err
	}

	configPath := filepath.Join(configDir, "ignore.json")

	if opts.Pattern != "" {
		patternsRaw, _ := cfg["patterns"].([]any)
		for _, p := range patternsRaw {
			if s, ok := p.(string); ok && s == opts.Pattern {
				return nil, fmt.Errorf("pattern already exists: %s", opts.Pattern)
			}
		}
		patternsRaw = append(patternsRaw, opts.Pattern)
		cfg["patterns"] = patternsRaw

		if err := tx.WriteJSONAtomic(configPath, cfg); err != nil {
			return nil, fmt.Errorf("write config: %w", err)
		}
		return map[string]any{
			"op":      "add-pattern",
			"pattern": opts.Pattern,
		}, nil
	}

	// Agent path
	if opts.Target == "" {
		return nil, fmt.Errorf("--target is required with --agent")
	}

	agents := tx.GetMap(cfg, "agents")
	if agents == nil {
		agents = make(map[string]any)
		cfg["agents"] = agents
	}

	if _, exists := agents[opts.Agent]; exists {
		return nil, fmt.Errorf("agent already registered: %s", opts.Agent)
	}

	agents[opts.Agent] = map[string]any{"target": opts.Target}

	if err := tx.WriteJSONAtomic(configPath, cfg); err != nil {
		return nil, fmt.Errorf("write config: %w", err)
	}
	return map[string]any{
		"op":     "add-agent",
		"agent":  opts.Agent,
		"target": opts.Target,
	}, nil
}

// IgnoreAddOpts holds parameters for IgnoreAdd.
type IgnoreAddOpts struct {
	Pattern string
	Agent   string
	Target  string
}

// IgnoreRm removes an ignore pattern or agent.
// Exactly one of pattern or agent must be provided.
func IgnoreRm(configDir string, opts IgnoreRmOpts) (map[string]any, error) {
	if opts.Pattern != "" && opts.Agent != "" {
		return nil, fmt.Errorf("pattern and --agent are mutually exclusive")
	}
	if opts.Pattern == "" && opts.Agent == "" {
		return nil, fmt.Errorf("either pattern or --agent is required")
	}

	cfg, err := loadIgnoreConfig(configDir)
	if err != nil {
		return nil, err
	}

	configPath := filepath.Join(configDir, "ignore.json")

	if opts.Pattern != "" {
		patternsRaw, _ := cfg["patterns"].([]any)
		found := false
		var newPatterns []any
		for _, p := range patternsRaw {
			if s, ok := p.(string); ok && s == opts.Pattern {
				found = true
				continue
			}
			newPatterns = append(newPatterns, p)
		}
		if !found {
			return nil, fmt.Errorf("pattern not found: %s", opts.Pattern)
		}
		if newPatterns == nil {
			newPatterns = []any{}
		}
		cfg["patterns"] = newPatterns

		if err := tx.WriteJSONAtomic(configPath, cfg); err != nil {
			return nil, fmt.Errorf("write config: %w", err)
		}
		return map[string]any{
			"op":      "rm-pattern",
			"pattern": opts.Pattern,
		}, nil
	}

	// Agent removal
	agents := tx.GetMap(cfg, "agents")
	if agents == nil {
		return nil, fmt.Errorf("agent not found: %s", opts.Agent)
	}
	if _, exists := agents[opts.Agent]; !exists {
		return nil, fmt.Errorf("agent not found: %s", opts.Agent)
	}

	delete(agents, opts.Agent)

	if err := tx.WriteJSONAtomic(configPath, cfg); err != nil {
		return nil, fmt.Errorf("write config: %w", err)
	}
	return map[string]any{
		"op":    "rm-agent",
		"agent": opts.Agent,
	}, nil
}

// IgnoreRmOpts holds parameters for IgnoreRm.
type IgnoreRmOpts struct {
	Pattern string
	Agent   string
}
