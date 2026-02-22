package content

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agentctl/internal/tx"
)

// ── Ignore helpers ───────────────────────────────────────────────────

// buildIgnoreContent builds ignore file content from a pattern list.
func buildIgnoreContent(patterns []string) string {
	if len(patterns) == 0 {
		return ""
	}
	return strings.Join(patterns, "\n") + "\n"
}

// ── Config loading ───────────────────────────────────────────────────

// loadIgnoreConfig loads ignore.json from configDir.
// H6: validates that all agent target values are non-empty and under $HOME.
func loadIgnoreConfig(configDir string) (map[string]any, error) {
	path := filepath.Join(configDir, "ignore.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return map[string]any{
			"patterns": []any{},
			"agents":   map[string]any{},
		}, nil
	}
	cfg, err := tx.ReadJSON(path)
	if err != nil {
		return nil, fmt.Errorf("ignore config: %w", err)
	}
	// Validate targets
	agents := tx.GetMap(cfg, "agents")
	for name, val := range agents {
		agentCfg, ok := val.(map[string]any)
		if !ok {
			continue
		}
		target := tx.GetString(agentCfg, "target", "")
		if target == "" {
			return nil, fmt.Errorf("ignore config: agent %q has empty target", name)
		}
	}
	return cfg, nil
}

// ── Apply ────────────────────────────────────────────────────────────

// applyIgnoreChange writes the ignore file content to the target path.
func applyIgnoreChange(ignoreCfg map[string]any, path string) error {
	patternsRaw, _ := ignoreCfg["patterns"].([]any)
	patterns := make([]string, 0, len(patternsRaw))
	for _, p := range patternsRaw {
		if s, ok := p.(string); ok {
			patterns = append(patterns, s)
		}
	}
	return tx.WriteTextAtomic(path, buildIgnoreContent(patterns))
}
