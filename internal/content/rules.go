package content

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agentctl/internal/tx"
)

// ── Rules composition ────────────────────────────────────────────────

// composeRules reads and concatenates rule source files from rulesDir.
func composeRules(rulesDir string, compose []string, sep string) (string, error) {
	resolvedDir, err := filepath.Abs(rulesDir)
	if err != nil {
		return "", fmt.Errorf("cannot resolve rules dir: %w", err)
	}
	// Resolve symlinks on the rules dir itself for proper boundary check
	resolvedDirReal, err := filepath.EvalSymlinks(resolvedDir)
	if err != nil {
		resolvedDirReal = resolvedDir
	}

	parts := make([]string, 0, len(compose))
	for _, filename := range compose {
		// M5: Check each path component for ".." instead of substring check.
		// This allows filenames like "v1..md" while rejecting actual traversal.
		if strings.HasPrefix(filename, "/") {
			return "", fmt.Errorf("invalid compose filename (absolute path): %s", filename)
		}
		cleanParts := strings.Split(filepath.Clean(filename), string(filepath.Separator))
		for _, part := range cleanParts {
			if part == ".." {
				return "", fmt.Errorf("invalid compose filename (path traversal): %s", filename)
			}
		}
		path, err := filepath.Abs(filepath.Join(resolvedDir, filename))
		if err != nil {
			return "", fmt.Errorf("cannot resolve compose path: %w", err)
		}
		// Resolve symlinks on the compose path to prevent symlink-based traversal
		// (e.g., rules/slink -> /etc, compose entry "slink/hosts")
		pathReal, err := filepath.EvalSymlinks(path)
		if err != nil {
			if os.IsNotExist(err) {
				return "", fmt.Errorf("rule source not found: %s", path)
			}
			return "", fmt.Errorf("cannot resolve compose symlinks: %w", err)
		}
		if !strings.HasPrefix(pathReal, resolvedDirReal+string(filepath.Separator)) && pathReal != resolvedDirReal {
			return "", fmt.Errorf("compose path escapes rules dir (symlink traversal): %s", filename)
		}
		data, err := os.ReadFile(pathReal)
		if err != nil {
			if os.IsNotExist(err) {
				return "", fmt.Errorf("rule source not found: %s", path)
			}
			return "", err
		}
		parts = append(parts, strings.TrimRight(string(data), "\n\r "))
	}
	return strings.Join(parts, sep) + "\n", nil
}

// ── Config loading ───────────────────────────────────────────────────

// loadRulesConfig loads rules/config.json from configDir.
func loadRulesConfig(configDir string) (map[string]any, error) {
	path := filepath.Join(configDir, "rules", "config.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return map[string]any{"agents": map[string]any{}}, nil
	}
	cfg, err := tx.ReadJSON(path)
	if err != nil {
		return nil, fmt.Errorf("rules config: %w", err)
	}
	if err := validateRulesConfig(cfg); err != nil {
		return nil, fmt.Errorf("rules config validation: %w", err)
	}
	return cfg, nil
}

// ── Config validation ────────────────────────────────────────────────

// validateRulesConfig validates the structure of rules config.
func validateRulesConfig(cfg map[string]any) error {
	agents := tx.GetMap(cfg, "agents")
	if agents == nil {
		return nil
	}
	for name, val := range agents {
		agentCfg, ok := val.(map[string]any)
		if !ok {
			return fmt.Errorf("agent %q: expected object", name)
		}
		if tx.GetString(agentCfg, "target", "") == "" {
			return fmt.Errorf("agent %q: missing 'target'", name)
		}
		if tx.GetStringSlice(agentCfg, "compose") == nil {
			return fmt.Errorf("agent %q: missing 'compose'", name)
		}
	}
	return nil
}

// ── Apply ────────────────────────────────────────────────────────────

// applyRuleChange writes the composed rules to the target path.
func applyRuleChange(rulesCfg map[string]any, rulesDir string, opts ApplyOpts, agent, path string) error {
	agents := tx.GetMap(rulesCfg, "agents")
	ruleCfgRaw, ok := agents[agent]
	if !ok {
		return fmt.Errorf("agent %q not found in rules config", agent)
	}
	ruleCfg, ok := ruleCfgRaw.(map[string]any)
	if !ok {
		return fmt.Errorf("agent %q: invalid rules config", agent)
	}

	var compose []string
	var sep string

	if opts.Scope == "project" {
		pcfg := tx.GetMap(ruleCfg, "project")
		if pcfg == nil {
			return fmt.Errorf("agent %q: no project config", agent)
		}
		compose = tx.GetStringSlice(pcfg, "compose")
		sep = tx.GetString(pcfg, "separator", "\n\n")
	} else {
		compose = tx.GetStringSlice(ruleCfg, "compose")
		sep = tx.GetString(ruleCfg, "separator", "\n\n")
	}

	desired, err := composeRules(rulesDir, compose, sep)
	if err != nil {
		return err
	}
	return tx.WriteTextAtomic(path, desired)
}
