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
	Agent     string
	Type      string
	Path      string
	PreExists bool
	Snapshot  string
	PreHash   string
	PostHash  string
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
		_ = ignoreCfg   // used below

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
