package engine

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"agentctl/internal/tx"
)

// ── List runs / Show run ────────────────────────────────────────────

// ListRuns reads all run manifests, newest first.
func ListRuns(stateDir string) []map[string]any {
	runsDir := filepath.Join(stateDir, "runs")
	if _, err := os.Stat(runsDir); os.IsNotExist(err) {
		return nil
	}

	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return nil
	}

	// Collect JSON files, sorted by name descending (newest first)
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			files = append(files, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(files)))

	var rows []map[string]any
	for _, name := range files {
		data, err := tx.ReadJSON(filepath.Join(runsDir, name))
		if err != nil {
			continue
		}
		rows = append(rows, data)
	}

	return rows
}

// ShowRun reads a single run manifest by its ID.
func ShowRun(stateDir, runID string) (map[string]any, error) {
	if err := validateRunID(runID); err != nil {
		return nil, err
	}
	return tx.ReadJSON(filepath.Join(stateDir, "runs", runID+".json"))
}
