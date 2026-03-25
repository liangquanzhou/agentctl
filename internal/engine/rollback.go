package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agentctl/internal/tx"
)

// ── Run ID validation ────────────────────────────────────────────────

// validateRunID ensures a run ID matches the strict format: YYYYMMDD-HHMMSS-<8hex>.
func validateRunID(runID string) error {
	if !runIDRegex.MatchString(runID) {
		return fmt.Errorf("invalid run_id: %q (must match YYYYMMDD-HHMMSS-<8hex>)", runID)
	}
	return nil
}

// ── Rollback types ──────────────────────────────────────────────────

// RollbackFile records a single file restoration during rollback.
type RollbackFile struct {
	Agent        string `json:"agent"`
	Path         string `json:"path"`
	RestoredFrom string `json:"restored_from,omitempty"`
}

// RollbackResult holds the output of a rollback operation.
type RollbackResult struct {
	RunID        string         `json:"run_id"`
	Timestamp    string         `json:"timestamp"`
	Actor        string         `json:"actor"`
	Command      string         `json:"command"`
	SourceRunID  string         `json:"source_run_id"`
	Result       string         `json:"result"`
	Severity     string         `json:"severity"`
	ChangedFiles []RollbackFile `json:"changed_files"`
	Error        string         `json:"error,omitempty"`
}

// ToMap converts RollbackResult to a generic map for JSON serialization.
func (rr *RollbackResult) ToMap() map[string]any {
	files := make([]any, 0, len(rr.ChangedFiles))
	for _, f := range rr.ChangedFiles {
		files = append(files, map[string]any{
			"agent":         f.Agent,
			"path":          f.Path,
			"restored_from": f.RestoredFrom,
		})
	}
	m := map[string]any{
		"run_id":        rr.RunID,
		"timestamp":     rr.Timestamp,
		"actor":         rr.Actor,
		"command":       rr.Command,
		"source_run_id": rr.SourceRunID,
		"result":        rr.Result,
		"severity":      rr.Severity,
		"changed_files": files,
	}
	if rr.Error != "" {
		m["error"] = rr.Error
	}
	return m
}

// ── Rollback ────────────────────────────────────────────────────────

// Rollback restores agent configs from a previous run's snapshots.
// If agent is non-empty, only that agent's files are rolled back.
func Rollback(stateDir, runID, agent, actor string) (*RollbackResult, error) {
	if err := validateRunID(runID); err != nil {
		return nil, err
	}

	if err := tx.EnsureStateDirs(stateDir); err != nil {
		return nil, fmt.Errorf("ensure state dirs: %w", err)
	}

	lockPath := filepath.Join(stateDir, "locks", "apply.lock")
	lock, err := tx.AcquireLock(lockPath, 30)
	if err != nil {
		return nil, fmt.Errorf("acquire lock: %w", err)
	}
	defer tx.ReleaseLock(lock)

	srcManifest, err := tx.ReadJSON(filepath.Join(stateDir, "runs", runID+".json"))
	if err != nil {
		return nil, fmt.Errorf("read source manifest: %w", err)
	}

	changes := getChangedFiles(srcManifest)

	if actor == "" {
		actor = os.Getenv("USER")
		if actor == "" {
			actor = "unknown"
		}
	}

	rollbackID := generateRunID()
	result := &RollbackResult{
		RunID:        rollbackID,
		Timestamp:    tx.UTCNowISO(),
		Actor:        actor,
		Command:      "rollback",
		SourceRunID:  runID,
		Result:       "rolled_back",
		Severity:     "warning",
		ChangedFiles: []RollbackFile{},
	}

	defer func() {
		manifestPath := filepath.Join(stateDir, "runs", rollbackID+".json")
		if wErr := tx.WriteJSONAtomic(manifestPath, result.ToMap()); wErr != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to write rollback manifest: %v\n", wErr)
		}
	}()

	// Validate snapshot paths are under the expected snapshots directory.
	expectedPrefix := filepath.Join(stateDir, "snapshots") + string(filepath.Separator)

	var rollbackErrors []string
	for _, meta := range changes {
		metaAgent, _ := meta["agent"].(string)
		if agent != "" && metaAgent != agent {
			continue
		}

		path, _ := meta["path"].(string)
		preExists, _ := meta["pre_exists"].(bool)
		snapshot, _ := meta["snapshot"].(string)

		// Security (C1): validate target path from manifest is under $HOME
		if err := tx.IsUnderHome(path); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Sprintf("target path escapes home: %s", path))
			continue
		}

		// Security: ensure snapshot path is under state_dir/snapshots/
		if snapshot != "" && !strings.HasPrefix(filepath.Clean(snapshot), expectedPrefix) {
			rollbackErrors = append(rollbackErrors, fmt.Sprintf("snapshot path outside expected dir: %s", snapshot))
			continue
		}

		// Security: validate snapshot path is also under $HOME
		if snapshot != "" {
			if err := tx.IsUnderHome(snapshot); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Sprintf("snapshot path escapes home: %s", snapshot))
				continue
			}
		}

		if preExists && snapshot != "" {
			if err := tx.EnsureParent(path); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Sprintf("ensure parent %s: %v", path, err))
			}
			if err := tx.CopyFile(snapshot, path); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Sprintf("restore %s: %v", path, err))
			}
		} else {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				rollbackErrors = append(rollbackErrors, fmt.Sprintf("remove %s: %v", path, err))
			}
		}

		result.ChangedFiles = append(result.ChangedFiles, RollbackFile{
			Agent:        metaAgent,
			Path:         path,
			RestoredFrom: snapshot,
		})
	}

	if len(rollbackErrors) > 0 {
		result.Result = "partial_rollback"
		result.Error = "rollback errors: " + strings.Join(rollbackErrors, "; ")
	}

	return result, nil
}

// getChangedFiles extracts the changed_files array from a manifest map.
func getChangedFiles(manifest map[string]any) []map[string]any {
	val, ok := manifest["changed_files"]
	if !ok {
		return nil
	}
	arr, ok := val.([]any)
	if !ok {
		return nil
	}
	result := make([]map[string]any, 0, len(arr))
	for _, item := range arr {
		if m, ok := item.(map[string]any); ok {
			result = append(result, m)
		}
	}
	return result
}
