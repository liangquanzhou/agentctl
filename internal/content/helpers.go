package content

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"agentctl/internal/tx"
)

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
