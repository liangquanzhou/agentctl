package engine

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"

	"agentctl/internal/tx"
)

// shallowCopyMap creates a shallow copy of a map.
func shallowCopyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// MarshalJSON is a convenience for producing JSON bytes from any value.
func MarshalJSON(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

// normalize produces a canonical JSON string for comparison.
func normalize(obj any) string {
	return tx.Normalize(obj)
}

// generateRunID produces a run ID in the format: YYYYMMDD-HHMMSS-<8hex>.
func generateRunID() string {
	now := time.Now().UTC()
	prefix := now.Format("20060102-150405")
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// Fallback to time-based hex if crypto/rand fails
		b = []byte{byte(now.UnixNano() >> 24), byte(now.UnixNano() >> 16),
			byte(now.UnixNano() >> 8), byte(now.UnixNano())}
	}
	return prefix + "-" + hex.EncodeToString(b)
}
