package engine

import (
	"strings"
	"time"
)

// ── Stage B check ───────────────────────────────────────────────────

// StageBResult holds the verdict of a stage B readiness check.
type StageBResult struct {
	WindowDays     int    `json:"window_days"`
	RecentRuns     int    `json:"recent_runs"`
	DistinctDays   int    `json:"distinct_days"`
	CriticalFailed int    `json:"critical_failed"`
	Verdict        string `json:"verdict"`
}

// ToMap converts StageBResult to a generic map for JSON serialization.
func (sr *StageBResult) ToMap() map[string]any {
	return map[string]any{
		"window_days":     sr.WindowDays,
		"recent_runs":     sr.RecentRuns,
		"distinct_days":   sr.DistinctDays,
		"critical_failed": sr.CriticalFailed,
		"verdict":         sr.Verdict,
	}
}

// StageBCheck checks if stage B criteria are met: no critical failures and
// runs on at least windowDays distinct days within the window.
func StageBCheck(stateDir string, windowDays int) (*StageBResult, error) {
	if windowDays <= 0 {
		windowDays = 3
	}

	now := time.Now().UTC()
	cutoff := now.Add(-time.Duration(windowDays) * 24 * time.Hour)

	runs := ListRuns(stateDir)
	var recent []map[string]any

	for _, run := range runs {
		ts, ok := run["timestamp"].(string)
		if !ok {
			continue
		}
		parsed, err := parseISO8601(ts)
		if err != nil {
			continue
		}
		if !parsed.Before(cutoff) {
			recent = append(recent, run)
		}
	}

	criticalFailed := 0
	for _, r := range recent {
		result, _ := r["result"].(string)
		severity, _ := r["severity"].(string)
		if result == "failed" && severity == "critical" {
			criticalFailed++
		}
	}

	dates := make(map[string]bool)
	for _, r := range recent {
		ts, ok := r["timestamp"].(string)
		if !ok {
			continue
		}
		parsed, err := parseISO8601(ts)
		if err != nil {
			continue
		}
		dates[parsed.Format("2006-01-02")] = true
	}

	verdict := "FAIL"
	if criticalFailed == 0 && len(dates) >= windowDays {
		verdict = "PASS"
	}

	return &StageBResult{
		WindowDays:     windowDays,
		RecentRuns:     len(recent),
		DistinctDays:   len(dates),
		CriticalFailed: criticalFailed,
		Verdict:        verdict,
	}, nil
}

// parseISO8601 parses a timestamp like "2025-01-15T12:00:00Z" or with +00:00 offset.
func parseISO8601(s string) (time.Time, error) {
	// Replace trailing Z with +00:00 for consistent parsing
	normalized := strings.Replace(s, "Z", "+00:00", 1)
	return time.Parse("2006-01-02T15:04:05-07:00", normalized)
}
