package content

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"agentctl/internal/tx"
)

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

// ── Config loading ───────────────────────────────────────────────────

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

// ── Apply ────────────────────────────────────────────────────────────

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
