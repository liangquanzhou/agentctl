package content

import (
	"fmt"
	"path/filepath"

	"agentctl/internal/tx"
)

// HooksList returns per-agent hook configurations.
func HooksList(configDir string) (map[string]any, error) {
	cfg, err := loadHooksConfig(configDir)
	if err != nil {
		return nil, err
	}

	agents := make(map[string]any)
	agentsCfg := tx.GetMap(cfg, "agents")
	for _, name := range sortedMapKeys(agentsCfg) {
		hookCfgRaw := agentsCfg[name]
		hookCfg, ok := hookCfgRaw.(map[string]any)
		if !ok {
			continue
		}
		entry := map[string]any{
			"target": tx.GetString(hookCfg, "target", ""),
			"format": tx.GetString(hookCfg, "format", ""),
		}
		if events := tx.GetMap(hookCfg, "events"); events != nil {
			entry["events"] = events
		}
		if notify := tx.GetStringSlice(hookCfg, "notify"); notify != nil {
			entry["notify"] = notify
		}
		agents[name] = entry
	}

	return map[string]any{"agents": agents}, nil
}

// HooksAdd adds a hook entry to an agent.
// For claude/gemini formats: event + command are required.
// For codex format: notify is required.
// New agents require target + format.
func HooksAdd(configDir, agent string, opts HooksAddOpts) (map[string]any, error) {
	cfg, err := loadHooksConfig(configDir)
	if err != nil {
		return nil, err
	}

	agents := tx.GetMap(cfg, "agents")
	if agents == nil {
		agents = make(map[string]any)
		cfg["agents"] = agents
	}

	createdAgent := false
	if _, exists := agents[agent]; !exists {
		if opts.Target == "" || opts.Format == "" {
			return nil, fmt.Errorf("agent %q does not exist; --target and --format are required", agent)
		}
		newEntry := map[string]any{
			"target": opts.Target,
			"format": opts.Format,
		}
		if opts.Format == "codex_notify" {
			newEntry["notify"] = []any{}
		} else {
			newEntry["events"] = map[string]any{}
		}
		agents[agent] = newEntry
		createdAgent = true
	}

	agentCfg, ok := agents[agent].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("agent %q: invalid config", agent)
	}
	format := tx.GetString(agentCfg, "format", "")

	var detail map[string]any

	if format == "codex_notify" {
		if opts.Event != "" || opts.Command != "" {
			return nil, fmt.Errorf("codex_notify format uses --notify, not --event/--command")
		}
		if opts.Notify == "" {
			return nil, fmt.Errorf("--notify is required for codex_notify format")
		}
		notifyList := tx.GetStringSlice(agentCfg, "notify")
		for _, n := range notifyList {
			if n == opts.Notify {
				return nil, fmt.Errorf("notify value already exists: %s", opts.Notify)
			}
		}
		notifyAny, _ := agentCfg["notify"].([]any)
		notifyAny = append(notifyAny, opts.Notify)
		agentCfg["notify"] = notifyAny
		detail = map[string]any{"notify": opts.Notify}
	} else {
		if opts.Event == "" || opts.Command == "" {
			return nil, fmt.Errorf("%s format requires --event and --command", format)
		}
		events := tx.GetMap(agentCfg, "events")
		if events == nil {
			events = make(map[string]any)
			agentCfg["events"] = events
		}
		hookEntry := map[string]any{
			"type":    "command",
			"command": opts.Command,
		}
		if opts.Timeout > 0 {
			hookEntry["timeout"] = opts.Timeout
		}
		// Append to event's hook list
		existing, _ := events[opts.Event].([]any)
		existing = append(existing, hookEntry)
		events[opts.Event] = existing
		detail = map[string]any{"event": opts.Event, "command": opts.Command}
	}

	// Re-validate
	if err := validateHooksConfig(cfg); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	configPath := filepath.Join(configDir, "hooks", "config.json")
	if err := tx.WriteJSONAtomic(configPath, cfg); err != nil {
		return nil, fmt.Errorf("write config: %w", err)
	}

	return map[string]any{
		"op":            "add",
		"agent":         agent,
		"format":        format,
		"created_agent": createdAgent,
		"detail":        detail,
	}, nil
}

// HooksAddOpts holds parameters for HooksAdd.
type HooksAddOpts struct {
	Event   string
	Command string
	Timeout int
	Notify  string
	Target  string
	Format  string
}

// HooksRm removes hook entries from an agent.
// claude/gemini: event+command removes one entry; event alone removes
// the entire event; neither removes the whole agent.
// codex: notify removes one value; neither removes the whole agent.
func HooksRm(configDir, agent string, opts HooksRmOpts) (map[string]any, error) {
	cfg, err := loadHooksConfig(configDir)
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

	format := tx.GetString(agentCfg, "format", "")
	removedAgent := false

	if format == "codex_notify" {
		if opts.Notify != "" {
			notifyList := tx.GetStringSlice(agentCfg, "notify")
			found := false
			var newNotify []any
			for _, n := range notifyList {
				if n == opts.Notify {
					found = true
					continue
				}
				newNotify = append(newNotify, n)
			}
			if !found {
				return nil, fmt.Errorf("notify value not found: %s", opts.Notify)
			}
			if len(newNotify) == 0 {
				delete(agents, agent)
				removedAgent = true
			} else {
				agentCfg["notify"] = newNotify
			}
		} else {
			delete(agents, agent)
			removedAgent = true
		}
	} else {
		if opts.Event != "" && opts.Command != "" {
			events := tx.GetMap(agentCfg, "events")
			if events == nil {
				return nil, fmt.Errorf("event not found: %s", opts.Event)
			}
			entriesRaw, ok := events[opts.Event].([]any)
			if !ok {
				return nil, fmt.Errorf("event not found: %s", opts.Event)
			}
			var filtered []any
			found := false
			for _, entryRaw := range entriesRaw {
				entry, ok := entryRaw.(map[string]any)
				if !ok {
					filtered = append(filtered, entryRaw)
					continue
				}
				if tx.GetString(entry, "command", "") == opts.Command {
					found = true
					continue
				}
				filtered = append(filtered, entryRaw)
			}
			if !found {
				return nil, fmt.Errorf("command not found in event %q: %s", opts.Event, opts.Command)
			}
			if len(filtered) > 0 {
				events[opts.Event] = filtered
			} else {
				delete(events, opts.Event)
			}
		} else if opts.Event != "" {
			events := tx.GetMap(agentCfg, "events")
			if events == nil {
				return nil, fmt.Errorf("event not found: %s", opts.Event)
			}
			if _, ok := events[opts.Event]; !ok {
				return nil, fmt.Errorf("event not found: %s", opts.Event)
			}
			delete(events, opts.Event)
		} else {
			delete(agents, agent)
			removedAgent = true
		}
	}

	// Re-validate (skip if agent was removed)
	if !removedAgent {
		if err := validateHooksConfig(cfg); err != nil {
			return nil, fmt.Errorf("validation failed: %w", err)
		}
	}

	configPath := filepath.Join(configDir, "hooks", "config.json")
	if err := tx.WriteJSONAtomic(configPath, cfg); err != nil {
		return nil, fmt.Errorf("write config: %w", err)
	}

	return map[string]any{
		"op":            "rm",
		"agent":         agent,
		"removed_agent": removedAgent,
	}, nil
}

// HooksRmOpts holds parameters for HooksRm.
type HooksRmOpts struct {
	Event   string
	Command string
	Notify  string
}
