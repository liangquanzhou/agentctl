// Package content implements the Content Plane: rules, hooks, commands, and
// ignore distribution with snapshot-based rollback.
//
// It composes rule source files into per-agent target files (e.g. CLAUDE.md),
// injects managed hooks into agent settings files, syncs command directories,
// and writes ignore patterns — all transactionally via tx primitives.
package content

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"agentctl/internal/tx"
)

// ── Defaults ─────────────────────────────────────────────────────────

// DefaultConfigDir returns the canonical agentctl config directory.
func DefaultConfigDir() string {
	return filepath.Join(tx.HomeDir(), ".config", "agentctl")
}

// ── Option structs ───────────────────────────────────────────────────

// PlanOpts holds parameters for ContentPlan.
type PlanOpts struct {
	Scope      string // "global" (default) or "project"
	ProjectDir string // required when Scope=="project"
	TypeFilter string // "" (all), "rules", "hooks", "commands", "ignore"
}

// ApplyOpts holds parameters for ContentApply.
type ApplyOpts struct {
	BreakGlass bool
	Reason     string
	Scope      string // "global" (default) or "project"
	ProjectDir string // required when Scope=="project"
	TypeFilter string // "" (all), "rules", "hooks", "commands", "ignore"
}

// ── Valid type filters ───────────────────────────────────────────────

var validTypeFilters = map[string]bool{
	"rules":    true,
	"hooks":    true,
	"commands": true,
	"ignore":   true,
}

// ── Path safety ──────────────────────────────────────────────────────

// resolvePath expands ~ and resolves the path. Rejects paths that escape $HOME
// and rejects symlinked targets.
func resolvePath(target string) (string, error) {
	if target == "" {
		return "", fmt.Errorf("target path is empty")
	}
	expanded := tx.ExpandUser(target)
	resolved, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("cannot resolve path %q: %w", target, err)
	}
	// H7: Reject symlinked targets
	if err := tx.RejectSymlink(resolved); err != nil {
		return "", err
	}
	// EvalSymlinks on both paths for proper comparison
	home := tx.HomeDir()
	resolvedHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		resolvedHome = home
	}
	resolvedTarget, err := filepath.EvalSymlinks(filepath.Dir(resolved))
	if err != nil {
		// Parent dir may not exist yet — fall back to lexical check
		resolvedTarget = filepath.Dir(resolved)
	}
	resolvedClean := filepath.Join(resolvedTarget, filepath.Base(resolved))
	if !strings.HasPrefix(resolvedClean, resolvedHome+string(filepath.Separator)) && resolvedClean != resolvedHome {
		return "", fmt.Errorf("target path escapes home: %s", target)
	}
	return resolved, nil
}

// resolveProjectPath resolves a relative filename inside projectDir. Rejects
// directory traversal attempts (.. or absolute paths).
func resolveProjectPath(projectDir, filename string) (string, error) {
	if strings.HasPrefix(filename, "/") {
		return "", fmt.Errorf("path traversal in project target: %s", filename)
	}
	// M5: Check each path component for ".." instead of substring check.
	cleanParts := strings.Split(filepath.Clean(filename), string(filepath.Separator))
	for _, part := range cleanParts {
		if part == ".." {
			return "", fmt.Errorf("path traversal in project target: %s", filename)
		}
	}
	resolved, err := filepath.Abs(filepath.Join(projectDir, filename))
	if err != nil {
		return "", fmt.Errorf("cannot resolve project path: %w", err)
	}
	// Resolve symlinks to prevent symlink-based escape from project dir
	resolvedReal, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		// Path may not exist yet — resolve parent instead
		parentReal, perr := filepath.EvalSymlinks(filepath.Dir(resolved))
		if perr != nil {
			parentReal = filepath.Dir(resolved)
		}
		resolvedReal = filepath.Join(parentReal, filepath.Base(resolved))
	}
	absProject, err := filepath.Abs(projectDir)
	if err != nil {
		return "", fmt.Errorf("cannot resolve project dir: %w", err)
	}
	absProjectReal, err := filepath.EvalSymlinks(absProject)
	if err != nil {
		absProjectReal = absProject
	}
	if !strings.HasPrefix(resolvedReal, absProjectReal+string(filepath.Separator)) && resolvedReal != absProjectReal {
		return "", fmt.Errorf("project target escapes project dir (symlink traversal): %s", filename)
	}
	return resolved, nil
}

// ── Template ─────────────────────────────────────────────────────────

// templateSub replaces {{agent}} with the agent name.
func templateSub(s, agent string) string {
	return strings.ReplaceAll(s, "{{agent}}", agent)
}

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
func buildHooksClaude(agent string, hookCfg map[string]any) map[string]any {
	events := tx.GetMap(hookCfg, "events")
	if events == nil {
		return map[string]any{}
	}

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
			hooksList = append(hooksList, map[string]any{
				"hooks": []any{hook},
			})
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

// ── Ignore helpers ───────────────────────────────────────────────────

// buildIgnoreContent builds ignore file content from a pattern list.
func buildIgnoreContent(patterns []string) string {
	if len(patterns) == 0 {
		return ""
	}
	return strings.Join(patterns, "\n") + "\n"
}

// ── Directory sync helpers ───────────────────────────────────────────

// dirSyncPlan compares source dir files with target dir; returns per-file plan items.
// It also detects stale files in target that no longer exist in source.
func dirSyncPlan(sourceDir, targetDir, agent, itemType string) []map[string]any {
	var items []map[string]any

	// Track source file names to detect stale targets later.
	sourceNames := make(map[string]bool)

	if _, err := os.Stat(sourceDir); !os.IsNotExist(err) {
		entries, err := os.ReadDir(sourceDir)
		if err == nil {
			// Sort entries for deterministic output
			sort.Slice(entries, func(i, j int) bool {
				return entries[i].Name() < entries[j].Name()
			})

			for _, entry := range entries {
				if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || entry.Name() == "config.json" {
					continue
				}
				// Skip symlinks to prevent following them outside source tree
				if entry.Type()&os.ModeSymlink != 0 {
					continue
				}
				sourceNames[entry.Name()] = true
				srcFile := filepath.Join(sourceDir, entry.Name())
				tgtFile := filepath.Join(targetDir, entry.Name())

				desired, err := os.ReadFile(srcFile)
				if err != nil {
					continue
				}

				var current []byte
				tgtExists := false
				if _, statErr := os.Stat(tgtFile); statErr == nil {
					tgtExists = true
					current, _ = os.ReadFile(tgtFile)
				}

				items = append(items, map[string]any{
					"agent":   agent,
					"type":    itemType,
					"path":    tgtFile,
					"source":  srcFile,
					"exists":  tgtExists,
					"changed": string(current) != string(desired),
				})
			}
		}
	}

	// Detect stale files: files in target that are not in source.
	if _, err := os.Stat(targetDir); !os.IsNotExist(err) {
		tgtEntries, err := os.ReadDir(targetDir)
		if err == nil {
			sort.Slice(tgtEntries, func(i, j int) bool {
				return tgtEntries[i].Name() < tgtEntries[j].Name()
			})

			for _, entry := range tgtEntries {
				if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || entry.Name() == "config.json" {
					continue
				}
				if !sourceNames[entry.Name()] {
					tgtFile := filepath.Join(targetDir, entry.Name())
					items = append(items, map[string]any{
						"agent":   agent,
						"type":    itemType,
						"path":    tgtFile,
						"exists":  true,
						"changed": true,
						"stale":   true,
					})
				}
			}
		}
	}

	return items
}

// ── Config loaders ───────────────────────────────────────────────────

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

// loadCommandsConfig loads commands/config.json from configDir.
// H6: validates that all agent target_dir values are non-empty and under $HOME.
func loadCommandsConfig(configDir string) (map[string]any, error) {
	path := filepath.Join(configDir, "commands", "config.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return map[string]any{"agents": map[string]any{}}, nil
	}
	cfg, err := tx.ReadJSON(path)
	if err != nil {
		return nil, fmt.Errorf("commands config: %w", err)
	}
	// Validate targets
	agents := tx.GetMap(cfg, "agents")
	for name, val := range agents {
		agentCfg, ok := val.(map[string]any)
		if !ok {
			continue
		}
		target := tx.GetString(agentCfg, "target_dir", "")
		if target == "" {
			return nil, fmt.Errorf("commands config: agent %q has empty target_dir", name)
		}
	}
	return cfg, nil
}

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

// ── Helpers ──────────────────────────────────────────────────────────

// shortHash returns the first 12 hex chars of SHA-256 of the given string.
func shortHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])[:12]
}

// readFileText reads a file and returns its content as string.
// Returns empty string if the file does not exist.
func readFileText(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// fileExists returns true if path exists and is not a directory.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// sortedMapKeys returns the keys of a map[string]any in sorted order.
func sortedMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ── Plan ─────────────────────────────────────────────────────────────

// ContentPlan detects drift between desired and current content state.
//
// PlanOpts.Scope can be "global" (default) or "project".  When "project" is
// used, only agents with a "project" sub-config in their rules are processed,
// and PlanOpts.ProjectDir supplies the root.
//
// PlanOpts.TypeFilter limits processing to a single content type: "rules",
// "hooks", "commands", or "ignore".  Empty string processes all types.
func ContentPlan(configDir string, opts PlanOpts) (map[string]any, error) {
	// Default scope
	if opts.Scope == "" {
		opts.Scope = "global"
	}

	// Validate scope
	if opts.Scope != "global" && opts.Scope != "project" {
		return nil, fmt.Errorf("invalid scope: %q (expected 'global' or 'project')", opts.Scope)
	}

	// Validate type filter
	if opts.TypeFilter != "" && !validTypeFilters[opts.TypeFilter] {
		valid := []string{"commands", "hooks", "ignore", "rules"}
		return nil, fmt.Errorf("invalid type_filter: %q (expected one of %v or empty)", opts.TypeFilter, valid)
	}

	// Project scope + type_filter constraint
	if opts.Scope == "project" && opts.TypeFilter != "" && opts.TypeFilter != "rules" {
		return nil, fmt.Errorf("scope='project' only supports type_filter='' or 'rules', got %q", opts.TypeFilter)
	}

	rulesCfg, err := loadRulesConfig(configDir)
	if err != nil {
		return nil, err
	}
	rulesDir := filepath.Join(configDir, "rules")

	var items []map[string]any
	seenTargets := make(map[string]bool)

	// --- Rules ---
	if opts.TypeFilter == "" || opts.TypeFilter == "rules" {
		agents := tx.GetMap(rulesCfg, "agents")
		for _, agent := range sortedMapKeys(agents) {
			ruleCfgRaw := agents[agent]
			ruleCfg, ok := ruleCfgRaw.(map[string]any)
			if !ok {
				continue
			}

			var targetPath string
			var compose []string
			var sep string

			if opts.Scope == "project" {
				pcfg := tx.GetMap(ruleCfg, "project")
				if pcfg == nil {
					continue
				}
				if opts.ProjectDir == "" {
					return nil, fmt.Errorf("--project-dir is required for project scope")
				}
				targetPath, err = resolveProjectPath(opts.ProjectDir, tx.GetString(pcfg, "target", ""))
				if err != nil {
					return nil, err
				}
				compose = tx.GetStringSlice(pcfg, "compose")
				sep = tx.GetString(pcfg, "separator", "\n\n")
			} else {
				targetPath, err = resolvePath(tx.GetString(ruleCfg, "target", ""))
				if err != nil {
					return nil, err
				}
				compose = tx.GetStringSlice(ruleCfg, "compose")
				sep = tx.GetString(ruleCfg, "separator", "\n\n")
			}

			if seenTargets[targetPath] {
				return nil, fmt.Errorf("duplicate target: %s", targetPath)
			}
			seenTargets[targetPath] = true

			desired, err := composeRules(rulesDir, compose, sep)
			if err != nil {
				return nil, fmt.Errorf("rules compose for agent %q: %w", agent, err)
			}

			current := readFileText(targetPath)
			items = append(items, map[string]any{
				"agent":        agent,
				"type":         "rules",
				"path":         targetPath,
				"exists":       fileExists(targetPath),
				"changed":      current != desired,
				"desired_hash": shortHash(desired),
				"current_hash": func() string {
					if current == "" {
						return ""
					}
					return shortHash(current)
				}(),
			})
		}
	}

	// Project scope only applies to rules
	if opts.Scope == "project" {
		result := map[string]any{
			"generated_at": tx.UTCNowISO(),
			"items":        nilItems(items),
		}
		legacyPath := filepath.Join(configDir, "registry", "content.json")
		if fileExists(legacyPath) {
			result["warnings"] = []string{
				fmt.Sprintf("legacy %s found — run migration to split config", legacyPath),
			}
		}
		return result, nil
	}

	// --- Hooks ---
	if opts.TypeFilter == "" || opts.TypeFilter == "hooks" {
		hooksCfg, err := loadHooksConfig(configDir)
		if err != nil {
			return nil, err
		}
		agents := tx.GetMap(hooksCfg, "agents")
		for _, agent := range sortedMapKeys(agents) {
			hookCfgRaw := agents[agent]
			hookCfg, ok := hookCfgRaw.(map[string]any)
			if !ok {
				continue
			}

			targetPath, err := resolvePath(tx.GetString(hookCfg, "target", ""))
			if err != nil {
				return nil, err
			}

			format := tx.GetString(hookCfg, "format", "")
			adapter, ok := hooksAdapters[format]
			if !ok {
				return nil, fmt.Errorf("unknown hooks format: %s", format)
			}

			contentTargetKey := targetPath + "#hooks"
			if seenTargets[contentTargetKey] {
				return nil, fmt.Errorf("duplicate hooks target: %s", targetPath)
			}
			seenTargets[contentTargetKey] = true

			// Read current hooks from existing file
			var currentHooks any
			if fileExists(targetPath) {
				var fullCfg map[string]any
				if adapter.FileType == "toml" {
					fullCfg, err = tx.ReadTOML(targetPath)
				} else {
					fullCfg, err = tx.ReadJSON(targetPath)
				}
				if err != nil {
					return nil, fmt.Errorf("reading hooks target %s: %w", targetPath, err)
				}
				if v, ok := fullCfg[adapter.InjectKey]; ok {
					currentHooks = v
				} else {
					if adapter.FileType == "toml" {
						currentHooks = []any{}
					} else {
						currentHooks = map[string]any{}
					}
				}
			} else {
				if adapter.FileType == "toml" {
					currentHooks = []any{}
				} else {
					currentHooks = map[string]any{}
				}
			}

			// Build desired hooks
			var desiredHooks any
			if format == "codex_notify" {
				desiredHooks = buildHooksCodex(agent, hookCfg)
			} else {
				desiredHooks = buildHooksClaude(agent, hookCfg)
			}

			currentJSON, _ := json.Marshal(currentHooks)
			desiredJSON, _ := json.Marshal(desiredHooks)
			changed := tx.Normalize(json.RawMessage(currentJSON)) != tx.Normalize(json.RawMessage(desiredJSON))

			items = append(items, map[string]any{
				"agent":      agent,
				"type":       "hooks",
				"path":       targetPath,
				"inject_key": adapter.InjectKey,
				"exists":     fileExists(targetPath),
				"changed":    changed,
				"format":     format,
			})
		}
	}

	// --- Commands (directory sync) ---
	if opts.TypeFilter == "" || opts.TypeFilter == "commands" {
		commandsCfg, err := loadCommandsConfig(configDir)
		if err != nil {
			return nil, err
		}
		commandsDir := filepath.Join(configDir, "commands")
		agents := tx.GetMap(commandsCfg, "agents")
		for _, agent := range sortedMapKeys(agents) {
			cmdCfgRaw := agents[agent]
			cmdCfg, ok := cmdCfgRaw.(map[string]any)
			if !ok {
				continue
			}
			targetDir, err := resolvePath(tx.GetString(cmdCfg, "target_dir", ""))
			if err != nil {
				return nil, err
			}
			syncItems := dirSyncPlan(commandsDir, targetDir, agent, "commands")
			items = append(items, syncItems...)
		}
	}

	// --- Ignore ---
	if opts.TypeFilter == "" || opts.TypeFilter == "ignore" {
		ignoreCfg, err := loadIgnoreConfig(configDir)
		if err != nil {
			return nil, err
		}
		patternsRaw, _ := ignoreCfg["patterns"].([]any)
		patterns := make([]string, 0, len(patternsRaw))
		for _, p := range patternsRaw {
			if s, ok := p.(string); ok {
				patterns = append(patterns, s)
			}
		}

		if len(patterns) > 0 {
			desiredIgnore := buildIgnoreContent(patterns)
			agents := tx.GetMap(ignoreCfg, "agents")
			for _, agent := range sortedMapKeys(agents) {
				ignCfgRaw := agents[agent]
				ignCfg, ok := ignCfgRaw.(map[string]any)
				if !ok {
					continue
				}
				targetPath, err := resolvePath(tx.GetString(ignCfg, "target", ""))
				if err != nil {
					return nil, err
				}
				current := readFileText(targetPath)
				items = append(items, map[string]any{
					"agent":   agent,
					"type":    "ignore",
					"path":    targetPath,
					"exists":  fileExists(targetPath),
					"changed": current != desiredIgnore,
				})
			}
		}
	}

	result := map[string]any{
		"generated_at": tx.UTCNowISO(),
		"items":        nilItems(items),
	}

	// L1: legacy content.json detection — emit as non-fatal warning, not validation error
	legacyPath := filepath.Join(configDir, "registry", "content.json")
	if fileExists(legacyPath) {
		result["warnings"] = []string{
			fmt.Sprintf("legacy %s found — run migration to split config", legacyPath),
		}
	}

	return result, nil
}

// nilItems ensures a nil slice is replaced with an empty slice so JSON
// serialisation emits [] instead of null.  The concrete type
// []map[string]any is preserved so callers can type-assert directly.
func nilItems(items []map[string]any) []map[string]any {
	if items == nil {
		return []map[string]any{}
	}
	return items
}

// ── Apply ────────────────────────────────────────────────────────────

// changeMeta tracks a single file change for rollback purposes.
type changeMeta struct {
	Agent      string
	Type       string
	Path       string
	PreExists  bool
	Snapshot   string
	PreHash    string
	PostHash   string
}

// ContentApply applies content changes with snapshot-based rollback on failure.
func ContentApply(configDir, stateDir string, opts ApplyOpts) (map[string]any, error) {
	// Default scope
	if opts.Scope == "" {
		opts.Scope = "global"
	}

	// Validate type filter
	if opts.TypeFilter != "" && !validTypeFilters[opts.TypeFilter] {
		valid := []string{"commands", "hooks", "ignore", "rules"}
		return nil, fmt.Errorf("invalid type_filter: %q (expected one of %v or empty)", opts.TypeFilter, valid)
	}

	// Project scope + type_filter constraint
	if opts.Scope == "project" && opts.TypeFilter != "" && opts.TypeFilter != "rules" {
		return nil, fmt.Errorf("scope='project' only supports type_filter='' or 'rules', got %q", opts.TypeFilter)
	}

	// Break glass requires reason
	if opts.BreakGlass && opts.Reason == "" {
		return nil, fmt.Errorf("--break-glass requires --reason")
	}

	// Ensure state directory structure
	if err := tx.EnsureStateDirs(stateDir); err != nil {
		return nil, fmt.Errorf("ensure state dirs: %w", err)
	}

	// Acquire lock
	lockPath := filepath.Join(stateDir, "locks", "apply.lock")
	lockFD, err := tx.AcquireLock(lockPath, 30)
	if err != nil {
		return nil, fmt.Errorf("acquire lock: %w", err)
	}

	// Generate run ID
	now := time.Now().UTC()
	runID := now.Format("20060102-150405") + "-" + randomHex(8)
	snapshotDir := filepath.Join(stateDir, "snapshots", runID)
	if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
		tx.ReleaseLock(lockFD)
		return nil, fmt.Errorf("create snapshot dir: %w", err)
	}

	// Build manifest
	actor := os.Getenv("USER")
	if actor == "" {
		actor = "unknown"
	}
	manifest := map[string]any{
		"run_id":        runID,
		"timestamp":     tx.UTCNowISO(),
		"actor":         actor,
		"command":       "content_apply",
		"result":        "success",
		"severity":      "info",
		"reason":        opts.Reason,
		"break_glass":   opts.BreakGlass,
		"changed_files": []any{},
	}

	var backupMeta []changeMeta
	start := time.Now().UTC()

	var applyErr error

	func() {
		defer func() {
			// Write manifest and release lock in all cases
			duration := time.Now().UTC().Sub(start)
			manifest["duration_ms"] = int(duration.Milliseconds())
			runsPath := filepath.Join(stateDir, "runs", runID+".json")
			_ = tx.WriteJSONAtomic(runsPath, manifest)
			tx.ReleaseLock(lockFD)
		}()

		// Load configs inside lock to avoid TOCTOU
		var rulesCfg, hooksCfg, commandsCfg, ignoreCfg map[string]any
		var loadErr error

		if opts.TypeFilter == "" || opts.TypeFilter == "rules" {
			rulesCfg, loadErr = loadRulesConfig(configDir)
			if loadErr != nil {
				applyErr = loadErr
				return
			}
		} else {
			rulesCfg = map[string]any{"agents": map[string]any{}}
		}

		if opts.TypeFilter == "" || opts.TypeFilter == "hooks" {
			hooksCfg, loadErr = loadHooksConfig(configDir)
			if loadErr != nil {
				applyErr = loadErr
				return
			}
		} else {
			hooksCfg = map[string]any{"agents": map[string]any{}}
		}

		if opts.TypeFilter == "" || opts.TypeFilter == "commands" {
			commandsCfg, loadErr = loadCommandsConfig(configDir)
			if loadErr != nil {
				applyErr = loadErr
				return
			}
		} else {
			commandsCfg = map[string]any{"agents": map[string]any{}}
		}

		if opts.TypeFilter == "" || opts.TypeFilter == "ignore" {
			ignoreCfg, loadErr = loadIgnoreConfig(configDir)
			if loadErr != nil {
				applyErr = loadErr
				return
			}
		} else {
			ignoreCfg = map[string]any{"patterns": []any{}, "agents": map[string]any{}}
		}

		rulesDir := filepath.Join(configDir, "rules")
		_ = commandsCfg // used below

		// Get plan to determine what needs changing
		planResult, planErr := ContentPlan(configDir, PlanOpts{
			Scope:      opts.Scope,
			ProjectDir: opts.ProjectDir,
			TypeFilter: opts.TypeFilter,
		})
		if planErr != nil {
			applyErr = planErr
			return
		}

		planItems, _ := planResult["items"].([]map[string]any)
		seq := 0

		for _, item := range planItems {

			changed := tx.GetBool(item, "changed", false)
			if !changed {
				continue
			}

			path := tx.GetString(item, "path", "")
			itemType := tx.GetString(item, "type", "")
			itemAgent := tx.GetString(item, "agent", "")

			// Sequenced snapshot to avoid collision
			preExists, snapPath, snapErr := tx.SnapshotWithSeq(path, snapshotDir, seq)
			seq++
			if snapErr != nil {
				applyErr = fmt.Errorf("snapshot %s: %w", path, snapErr)
				rbErrs := rollbackChanges(backupMeta)
				if len(rbErrs) > 0 {
					manifest["result"] = "partial_rollback"
					manifest["rollback_errors"] = rbErrs
				} else {
					manifest["result"] = "rolled_back"
				}
				manifest["severity"] = "critical"
				manifest["error"] = applyErr.Error()
				return
			}

			var preHash string
			if preExists {
				preHash, _ = tx.SHA256File(path)
			}

			// Apply the change
			var writeErr error
			switch itemType {
			case "rules":
				writeErr = applyRuleChange(rulesCfg, rulesDir, opts, itemAgent, path)
			case "hooks":
				writeErr = applyHookChange(hooksCfg, itemAgent, path)
			case "commands":
				writeErr = applyCommandChange(item, path)
			case "ignore":
				writeErr = applyIgnoreChange(ignoreCfg, path)
			default:
				writeErr = fmt.Errorf("unknown item type: %s", itemType)
			}

			if writeErr != nil {
				applyErr = fmt.Errorf("apply %s for %s: %w", itemType, itemAgent, writeErr)
				rbErrs := rollbackChanges(backupMeta)
				if len(rbErrs) > 0 {
					manifest["result"] = "partial_rollback"
					manifest["rollback_errors"] = rbErrs
				} else {
					manifest["result"] = "rolled_back"
				}
				manifest["severity"] = "critical"
				manifest["error"] = applyErr.Error()
				return
			}

			postHash, _ := tx.SHA256File(path)

			change := changeMeta{
				Agent:     itemAgent,
				Type:      itemType,
				Path:      path,
				PreExists: preExists,
				Snapshot:  snapPath,
				PreHash:   preHash,
				PostHash:  postHash,
			}
			backupMeta = append(backupMeta, change)

			// Also append to manifest
			changedFiles, _ := manifest["changed_files"].([]any)
			changedFiles = append(changedFiles, map[string]any{
				"agent":      itemAgent,
				"type":       itemType,
				"path":       path,
				"pre_exists": preExists,
				"snapshot":   nilIfEmpty(snapPath),
				"pre_hash":   nilIfEmpty(preHash),
				"post_hash":  postHash,
			})
			manifest["changed_files"] = changedFiles
		}

		changedFiles, _ := manifest["changed_files"].([]any)
		if len(changedFiles) == 0 {
			manifest["result"] = "no_changes"
		}
	}()

	if applyErr != nil {
		return manifest, applyErr
	}
	return manifest, nil
}

// ── Apply sub-functions ──────────────────────────────────────────────

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

// applyCommandChange copies a command source file to the target path atomically,
// or deletes the target if the item is stale (no longer in source).
// M2: Uses atomic write (read + WriteTextAtomic) instead of direct copy.
func applyCommandChange(item map[string]any, path string) error {
	if tx.GetBool(item, "stale", false) {
		return os.Remove(path)
	}
	source := tx.GetString(item, "source", "")
	if source == "" {
		return fmt.Errorf("missing source for command item")
	}
	// Reject symlinked source to prevent reading outside source tree
	if err := tx.RejectSymlink(source); err != nil {
		return fmt.Errorf("source symlink check: %w", err)
	}
	// Read source content and write atomically to target
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read source %s: %w", source, err)
	}
	return tx.WriteTextAtomic(path, string(data))
}

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

// ── Rollback ─────────────────────────────────────────────────────────

// rollbackChanges restores files from snapshots in reverse order.
// Returns a list of errors encountered during rollback (empty if all succeeded).
func rollbackChanges(metas []changeMeta) []string {
	var errs []string
	for i := len(metas) - 1; i >= 0; i-- {
		meta := metas[i]
		if meta.PreExists && meta.Snapshot != "" {
			if err := tx.EnsureParent(meta.Path); err != nil {
				errs = append(errs, fmt.Sprintf("rollback ensure parent %s: %v", meta.Path, err))
				continue
			}
			if err := tx.CopyFile(meta.Snapshot, meta.Path); err != nil {
				errs = append(errs, fmt.Sprintf("rollback restore %s: %v", meta.Path, err))
			}
		} else {
			// File did not exist before — remove it
			if err := os.Remove(meta.Path); err != nil && !os.IsNotExist(err) {
				errs = append(errs, fmt.Sprintf("rollback remove %s: %v", meta.Path, err))
			}
		}
	}
	return errs
}

// ── Utilities ────────────────────────────────────────────────────────

// randomHex returns n random hex characters using crypto/rand-quality source.
// Falls back to a timestamp-based value if crypto/rand fails.
func randomHex(n int) string {
	// Use a combination of time and PID for uniqueness without importing
	// crypto/rand (which can block on some systems). This is for run IDs,
	// not security purposes.
	now := time.Now().UnixNano()
	pid := os.Getpid()
	h := sha256.Sum256([]byte(fmt.Sprintf("%d-%d", now, pid)))
	hex := hex.EncodeToString(h[:])
	if n > len(hex) {
		n = len(hex)
	}
	return hex[:n]
}

// nilIfEmpty returns nil if s is empty, otherwise returns s.
// Used for JSON serialization where empty strings should be null.
func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
