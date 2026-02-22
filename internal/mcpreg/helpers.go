package mcpreg

import (
	"sort"

	"agentctl/internal/tx"
)

// ---------------------------------------------------------------------------
// Internal: map helpers
// ---------------------------------------------------------------------------

// getMapOrEmpty retrieves a sub-map from m[key], returning an empty map if
// the key is missing or not a map.
func getMapOrEmpty(m map[string]any, key string) map[string]any {
	v := tx.GetMap(m, key)
	if v == nil {
		return make(map[string]any)
	}
	return v
}

// ensureMap ensures m[key] is a map[string]any. If missing or wrong type, it
// creates one in place and returns it.
func ensureMap(m map[string]any, key string) map[string]any {
	v, ok := m[key]
	if ok {
		if sub, ok := v.(map[string]any); ok {
			return sub
		}
	}
	sub := make(map[string]any)
	m[key] = sub
	return sub
}

// ensureNestedMap ensures m[key] is a map[string]any (like ensureMap, identical
// semantics, provided for clarity when dealing with second-level nesting).
func ensureNestedMap(m map[string]any, key string) map[string]any {
	return ensureMap(m, key)
}

// ---------------------------------------------------------------------------
// Internal: slice / string helpers
// ---------------------------------------------------------------------------

// sortedKeys returns the keys of a map, sorted alphabetically.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// containsStr reports whether slice contains s.
func containsStr(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// removeStr returns a new slice with all occurrences of s removed.
func removeStr(slice []string, s string) []string {
	result := make([]string, 0, len(slice))
	for _, item := range slice {
		if item != s {
			result = append(result, item)
		}
	}
	return result
}

// dedup removes duplicate strings while preserving order.
func dedup(slice []string) []string {
	seen := make(map[string]struct{}, len(slice))
	result := make([]string, 0, len(slice))
	for _, s := range slice {
		if _, exists := seen[s]; exists {
			continue
		}
		seen[s] = struct{}{}
		result = append(result, s)
	}
	return result
}

// toAnySlice converts a []string to []any for JSON serialization compatibility.
func toAnySlice(ss []string) []any {
	if ss == nil {
		ss = []string{}
	}
	result := make([]any, len(ss))
	for i, s := range ss {
		result[i] = s
	}
	return result
}

// sortedUniqueStrings returns a sorted, deduplicated copy of a string slice.
func sortedUniqueStrings(ss []string) []string {
	if len(ss) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(ss))
	result := make([]string, 0, len(ss))
	for _, s := range ss {
		if _, exists := seen[s]; exists {
			continue
		}
		seen[s] = struct{}{}
		result = append(result, s)
	}
	sort.Strings(result)
	return result
}

// sortedStrings returns a sorted copy of a string slice.
func sortedStrings(ss []string) []string {
	out := make([]string, len(ss))
	copy(out, ss)
	sort.Strings(out)
	return out
}
