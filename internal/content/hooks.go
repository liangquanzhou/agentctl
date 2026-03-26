package content

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agentctl/internal/tx"
)

// ── Template ─────────────────────────────────────────────────────────

// templateSub replaces {{agent}} with the agent name.
func templateSub(s, agent string) string {
	return strings.ReplaceAll(s, "{{agent}}", agent)
}

// ── Hooks adapters ───────────────────────────────────────────────────

// hookAdapter describes how a hook format maps to its file injection.
type hookAdapter struct {
	InjectKey string
	FileType  string // "json" or "toml"
}

// hooksAdapters maps format name to adapter metadata.
var hooksAdapters = map[string]hookAdapter{
	"claude_hooks": {InjectKey: "hooks", FileType: "json"},
	"gemini_hooks": {InjectKey: "hooks", FileType: "json"},
	"codex_notify": {InjectKey: "notify", FileType: "toml"},
}

// buildHooksClaude builds the Claude Code / Gemini hooks structure from config.
// Returns a map[string]any suitable for JSON injection.
// For gemini_hooks format, also passes through "matcher" field.
func buildHooksClaude(agent string, hookCfg map[string]any) map[string]any {
	events := tx.GetMap(hookCfg, "events")
	if events == nil {
		return map[string]any{}
	}

	format := tx.GetString(hookCfg, "format", "")

	result := make(map[string]any)
	for eventName, entriesRaw := range events {
		entries, ok := entriesRaw.([]any)
		if !ok {
			continue
		}
		hooksList := make([]any, 0, len(entries))
		for _, entryRaw := range entries {
			entry, ok := entryRaw.(map[string]any)
			if !ok {
				continue
			}
			hook := map[string]any{
				"type":    tx.GetString(entry, "type", "command"),
				"command": templateSub(tx.GetString(entry, "command", ""), agent),
			}
			if timeout, ok := entry["timeout"]; ok {
				hook["timeout"] = timeout
			}
			wrapper := map[string]any{
				"hooks": []any{hook},
			}
			// Gemini CLI requires "matcher" on each hook group
			if format == "gemini_hooks" {
				matcher := tx.GetString(entry, "matcher", "*")
				wrapper["matcher"] = matcher
			}
			hooksList = append(hooksList, wrapper)
		}
		result[eventName] = hooksList
	}
	return result
}

// buildHooksCodex builds the Codex notify array from config.
func buildHooksCodex(agent string, hookCfg map[string]any) []string {
	notifyRaw := tx.GetStringSlice(hookCfg, "notify")
	if notifyRaw == nil {
		return []string{}
	}
	result := make([]string, len(notifyRaw))
	for i, part := range notifyRaw {
		result[i] = templateSub(part, agent)
	}
	return result
}

// ── Config loading ───────────────────────────────────────────────────

// loadHooksConfig loads hooks/config.json from configDir.
func loadHooksConfig(configDir string) (map[string]any, error) {
	path := filepath.Join(configDir, "hooks", "config.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return map[string]any{"agents": map[string]any{}}, nil
	}
	cfg, err := tx.ReadJSON(path)
	if err != nil {
		return nil, fmt.Errorf("hooks config: %w", err)
	}
	if err := validateHooksConfig(cfg); err != nil {
		return nil, fmt.Errorf("hooks config validation: %w", err)
	}
	return cfg, nil
}

// ── Config validation ────────────────────────────────────────────────

// validateHooksConfig validates the structure of hooks config, including
// format-specific constraints (events vs notify).
func validateHooksConfig(cfg map[string]any) error {
	agents := tx.GetMap(cfg, "agents")
	if agents == nil {
		return nil
	}
	for name, val := range agents {
		agentCfg, ok := val.(map[string]any)
		if !ok {
			return fmt.Errorf("agent %q: expected object", name)
		}
		target := tx.GetString(agentCfg, "target", "")
		if target == "" {
			return fmt.Errorf("agent %q: missing 'target'", name)
		}
		format := tx.GetString(agentCfg, "format", "")
		if _, ok := hooksAdapters[format]; !ok {
			return fmt.Errorf("agent %q: unknown hooks format: %s", name, format)
		}
		// Model constraint: codex_notify requires notify, others require events
		if format == "codex_notify" {
			events := tx.GetMap(agentCfg, "events")
			if events != nil && len(events) > 0 {
				return fmt.Errorf("agent %q: codex_notify format must use 'notify', not 'events'", name)
			}
			notify := tx.GetStringSlice(agentCfg, "notify")
			if len(notify) == 0 {
				return fmt.Errorf("agent %q: codex_notify format requires 'notify' list", name)
			}
		} else {
			notify := tx.GetStringSlice(agentCfg, "notify")
			if len(notify) > 0 {
				return fmt.Errorf("agent %q: %s format must use 'events', not 'notify'", name, format)
			}
		}
	}
	return nil
}

// ── Apply ────────────────────────────────────────────────────────────

// applyHookChange injects hooks into the target settings file (JSON or TOML).
func applyHookChange(hooksCfg map[string]any, agent, path string) error {
	agents := tx.GetMap(hooksCfg, "agents")
	hookCfgRaw, ok := agents[agent]
	if !ok {
		return fmt.Errorf("agent %q not found in hooks config", agent)
	}
	hookCfg, ok := hookCfgRaw.(map[string]any)
	if !ok {
		return fmt.Errorf("agent %q: invalid hooks config", agent)
	}

	format := tx.GetString(hookCfg, "format", "")
	adapter, ok := hooksAdapters[format]
	if !ok {
		return fmt.Errorf("unknown hooks format: %s", format)
	}

	// Read existing file, preserving all other fields
	var full map[string]any
	if fileExists(path) {
		var readErr error
		if adapter.FileType == "toml" {
			full, readErr = tx.ReadTOML(path)
		} else {
			full, readErr = tx.ReadJSON(path)
		}
		if readErr != nil {
			return fmt.Errorf("reading %s: %w", path, readErr)
		}
	} else {
		full = map[string]any{}
	}

	// Build and inject desired hooks
	if format == "codex_notify" {
		notifySlice := buildHooksCodex(agent, hookCfg)
		// Convert []string to []any for TOML serialization compatibility
		anySlice := make([]any, len(notifySlice))
		for i, s := range notifySlice {
			anySlice[i] = s
		}
		full[adapter.InjectKey] = anySlice
	} else {
		full[adapter.InjectKey] = buildHooksClaude(agent, hookCfg)
	}

	// Write back
	if adapter.FileType == "toml" {
		return tx.WriteTOMLAtomic(path, full)
	}
	return tx.WriteJSONAtomic(path, full)
}
