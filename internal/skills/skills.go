// Package skills implements skill directory discovery, synchronization, status
// checking, and pull-back operations across multiple agent targets.
//
// A "skill" is any directory containing a SKILL.md (or skill.md) marker file.
// The source of truth lives in a central skills directory; SkillsSync copies
// skills out to each agent's target directory, while SkillsPull copies skills
// from a target back to source.
package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"agentctl/internal/agents"
	"agentctl/internal/tx"
)

// ── Constants ────────────────────────────────────────────────────────

// skillMarkers are the filenames that identify a directory as a skill.
var skillMarkers = []string{"SKILL.md", "skill.md"}

// ── Public entry points ──────────────────────────────────────────────

// DefaultSkillsSource returns the default central skills source directory.
func DefaultSkillsSource() string {
	return filepath.Join(tx.HomeDir(), ".config", "agentctl", "skills")
}

// DefaultSkillsTargets returns the skill target directories for all
// registered agents (keyed by agent alias or name).
func DefaultSkillsTargets() map[string]string {
	registry := agents.LoadAgentRegistry("")
	return agents.BuildSkillsTargets(registry)
}

// ── SkillsList ───────────────────────────────────────────────────────

// SkillsList enumerates all skills found under sourceDir.
// Returns {"source_dir": string, "count": int, "skills": []string}.
func SkillsList(sourceDir string) map[string]any {
	skills := discoverSkills(sourceDir)

	names := make([]string, 0, len(skills))
	for name := range skills {
		names = append(names, name)
	}
	sort.Strings(names)

	return map[string]any{
		"source_dir": sourceDir,
		"count":      len(names),
		"skills":     names,
	}
}

// ── SkillsStatus ─────────────────────────────────────────────────────

// SkillsStatus compares source skills against each target and reports
// shared, local-only, missing, and drifted skills.
//
// Returns:
//
//	{
//	  "source_dir":    string,
//	  "source_count":  int,
//	  "targets":       []map[string]any,
//	  "unsynced_total": int,
//	}
func SkillsStatus(sourceDir string, targets map[string]string, cfg ...*SkillsConfig) map[string]any {
	allSrcSkills := discoverSkills(sourceDir)
	allSrcHashes := make(map[string]string, len(allSrcSkills))
	for name, dir := range allSrcSkills {
		allSrcHashes[name] = hashDir(dir)
	}

	var skillsCfg *SkillsConfig
	if len(cfg) > 0 {
		skillsCfg = cfg[0]
	}

	unsyncedTotal := 0
	var targetResults []map[string]any

	sortedTargets := sortedKeys(targets)
	for _, tgtName := range sortedTargets {
		tgtDir := targets[tgtName]

		// Per-agent filtering
		srcSkills := skillsCfg.FilteredSkills(tgtName, allSrcSkills)
		srcHashes := make(map[string]string, len(srcSkills))
		for name := range srcSkills {
			srcHashes[name] = allSrcHashes[name]
		}
		tgtSkills := discoverSkills(tgtDir)
		tgtHashes := make(map[string]string, len(tgtSkills))
		for name, dir := range tgtSkills {
			tgtHashes[name] = hashDir(dir)
		}

		var shared, localOnly, missing, drift []string

		// Skills present in both source and target.
		for name, srcHash := range srcHashes {
			if tgtHash, ok := tgtHashes[name]; ok {
				shared = append(shared, name)
				if srcHash != tgtHash {
					drift = append(drift, name)
				}
			} else {
				missing = append(missing, name)
			}
		}

		// Skills present only in target.
		for name := range tgtHashes {
			if _, ok := srcHashes[name]; !ok {
				localOnly = append(localOnly, name)
			}
		}

		sort.Strings(shared)
		sort.Strings(localOnly)
		sort.Strings(missing)
		sort.Strings(drift)

		unsynced := len(missing) + len(drift)
		unsyncedTotal += unsynced

		targetResults = append(targetResults, map[string]any{
			"target":     tgtName,
			"path":       tgtDir,
			"exists":     dirExists(tgtDir),
			"shared":     len(shared),
			"local":      len(localOnly),
			"missing":    len(missing),
			"drift":      len(drift),
			"drift_list": drift,
			"unsynced":   unsynced,
			"status":     syncStatus(unsynced),
		})
	}

	return map[string]any{
		"source_dir":     sourceDir,
		"source_count":   len(allSrcSkills),
		"targets":        targetResults,
		"unsynced_total": unsyncedTotal,
	}
}

// ── SkillsSync ───────────────────────────────────────────────────────

// SkillsSync copies new or updated skills from source to each target,
// removes stale managed skills, and updates the managed state file.
// H4: Acquires exclusive lock on managed.json + target trees.
// M4: Returns errors via the result map "errors" key and as a second return value.
func SkillsSync(sourceDir string, targets map[string]string, stateDir string, dryRun bool, cfg ...*SkillsConfig) map[string]any {
	// Note: $HOME validation is enforced at config load time in agents.go.
	// Runtime checks here focus on symlink rejection before destructive ops.

	// H4: Acquire exclusive lock
	skillsStateDir := filepath.Join(stateDir, "skills")
	if err := os.MkdirAll(skillsStateDir, 0o755); err != nil {
		return map[string]any{
			"dry_run":      dryRun,
			"source_dir":   sourceDir,
			"source_count": 0,
			"targets":      []map[string]any{},
			"actions":      0,
			"errors":       []string{fmt.Sprintf("create skills state dir: %v", err)},
		}
	}
	lockPath := filepath.Join(skillsStateDir, ".lock")
	lock, lockErr := tx.AcquireLock(lockPath, 30)
	if lockErr != nil {
		return map[string]any{
			"dry_run":      dryRun,
			"source_dir":   sourceDir,
			"source_count": 0,
			"targets":      []map[string]any{},
			"actions":      0,
			"errors":       []string{fmt.Sprintf("acquire lock: %v", lockErr)},
		}
	}
	defer tx.ReleaseLock(lock)

	allSrcSkills := discoverSkills(sourceDir)
	allSrcHashes := make(map[string]string, len(allSrcSkills))
	for name, dir := range allSrcSkills {
		allSrcHashes[name] = hashDir(dir)
	}

	var skillsCfg *SkillsConfig
	if len(cfg) > 0 {
		skillsCfg = cfg[0]
	}

	managed := loadManagedState(stateDir)
	totalActions := 0

	var targetResults []map[string]any
	var syncErrors []string

	sortedTargets := sortedKeys(targets)
	for _, tgtName := range sortedTargets {
		tgtDir := targets[tgtName]
		pluginPrepared := false
		if isClaudeOrgPluginSkillsDir(tgtDir) {
			prepared, err := prepareClaudeOrgPlugin(tgtDir, dryRun)
			if err != nil {
				syncErrors = append(syncErrors, fmt.Sprintf("%s: prepare Claude org plugin: %v", tgtName, err))
				continue
			}
			pluginPrepared = prepared
		}
		if isClaudeDesktopSkillsPluginDir(tgtDir) {
			prepared, err := prepareClaudeDesktopSkillsPlugin(tgtDir, dryRun)
			if err != nil {
				syncErrors = append(syncErrors, fmt.Sprintf("%s: prepare Claude desktop skills plugin: %v", tgtName, err))
				continue
			}
			pluginPrepared = prepared
		}
		tgtSkills := discoverSkills(tgtDir)

		// Per-agent filtering
		srcSkills := skillsCfg.FilteredSkills(tgtName, allSrcSkills)
		srcHashes := make(map[string]string, len(srcSkills))
		for name := range srcSkills {
			srcHashes[name] = allSrcHashes[name]
		}
		srcNames := sortedKeys(srcSkills)

		var copied, updated, removed []string
		prevManaged := managed[tgtName]

		// Copy or update source skills into target.
		for _, name := range srcNames {
			srcDir := srcSkills[name]
			dstDir := filepath.Join(tgtDir, name)

			if existingDir, exists := tgtSkills[name]; exists {
				// Already present — check for drift.
				existingHash := hashDir(existingDir)
				if srcHashes[name] == existingHash {
					continue // up to date
				}
				if err := replaceTree(srcDir, dstDir, dryRun); err != nil {
					syncErrors = append(syncErrors, fmt.Sprintf("%s/%s: %v", tgtName, name, err))
					continue
				}
				updated = append(updated, name)
			} else {
				// New skill.
				if err := replaceTree(srcDir, dstDir, dryRun); err != nil {
					syncErrors = append(syncErrors, fmt.Sprintf("%s/%s: %v", tgtName, name, err))
					continue
				}
				copied = append(copied, name)
			}
		}

		// Remove stale managed skills (previously synced but no longer in source).
		var stillManaged []string
		for _, name := range prevManaged {
			if _, inSource := srcSkills[name]; inSource {
				stillManaged = append(stillManaged, name)
				continue
			}
			// Skill was managed but is no longer in source — remove it.
			staleDir := filepath.Join(tgtDir, name)
			if _, err := os.Stat(staleDir); err == nil {
				if !dryRun {
					// Reject symlink before destructive removal
					if symErr := tx.RejectSymlink(staleDir); symErr != nil {
						syncErrors = append(syncErrors, fmt.Sprintf("%s/%s: %v", tgtName, name, symErr))
						continue
					}
					os.RemoveAll(staleDir)
				}
				removed = append(removed, name)
			}
		}

		// Update managed state: all source skills + any that survived.
		newManaged := make(map[string]bool)
		for _, name := range srcNames {
			newManaged[name] = true
		}
		for _, name := range stillManaged {
			newManaged[name] = true
		}
		managedList := sortedBoolKeys(newManaged)
		managed[tgtName] = managedList

		actions := len(copied) + len(updated) + len(removed)
		pluginPermissionsChanged := false
		if isClaudeOrgPluginSkillsDir(tgtDir) {
			permissionsChanged, err := normalizeClaudeOrgPluginPermissions(tgtDir, dryRun)
			if err != nil {
				syncErrors = append(syncErrors, fmt.Sprintf("%s: normalize Claude org plugin permissions: %v", tgtName, err))
			}
			pluginPermissionsChanged = permissionsChanged
		}
		if isClaudeDesktopSkillsPluginDir(tgtDir) {
			changed, err := updateClaudeDesktopSkillsManifest(tgtDir, srcSkills, prevManaged, dryRun)
			if err != nil {
				syncErrors = append(syncErrors, fmt.Sprintf("%s: update Claude desktop skills manifest: %v", tgtName, err))
			}
			pluginPermissionsChanged = pluginPermissionsChanged || changed
		}
		if isClaudeOrgPluginSkillsDir(tgtDir) && (actions > 0 || pluginPrepared || pluginPermissionsChanged) {
			if err := bumpClaudeOrgPluginVersion(tgtDir, dryRun); err != nil {
				syncErrors = append(syncErrors, fmt.Sprintf("%s: bump Claude org plugin version: %v", tgtName, err))
			}
		}
		totalActions += actions

		// Re-discover target skills after sync to compute unsynced count.
		postTgtSkills := discoverSkills(tgtDir)
		postTgtHashes := make(map[string]string, len(postTgtSkills))
		for name, dir := range postTgtSkills {
			postTgtHashes[name] = hashDir(dir)
		}
		postUnsynced := 0
		for name, srcHash := range srcHashes {
			if tgtHash, ok := postTgtHashes[name]; !ok || srcHash != tgtHash {
				postUnsynced++
			}
		}
		unchanged := len(srcSkills) - len(copied) - len(updated)

		targetResults = append(targetResults, map[string]any{
			"target":    tgtName,
			"path":      tgtDir,
			"created":   len(copied),
			"updated":   len(updated),
			"removed":   len(removed),
			"unchanged": unchanged,
			"unsynced":  postUnsynced,
			"status":    syncStatus(postUnsynced),
			"actions":   actions,
		})
	}

	if !dryRun {
		if err := saveManagedState(stateDir, managed); err != nil {
			syncErrors = append(syncErrors, fmt.Sprintf("save state: %v", err))
		}
	}

	result := map[string]any{
		"dry_run":      dryRun,
		"source_dir":   sourceDir,
		"source_count": len(allSrcSkills),
		"targets":      targetResults,
		"actions":      totalActions,
	}
	if len(syncErrors) > 0 {
		result["errors"] = syncErrors
	}
	return result
}

// ── SkillsPull ────────────────────────────────────────────────────────

// SkillsPull copies skills from a target directory back into the source.
// New skills are created; existing ones are updated only if overwrite is true.
func SkillsPull(sourceDir, targetName, targetDir string, dryRun, overwrite bool) (map[string]any, error) {
	// Note: $HOME validation is enforced at config load time in agents.go.

	if _, err := os.Stat(targetDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("target directory does not exist: %s", targetDir)
	}

	tgtSkills := discoverSkills(targetDir)
	srcSkills := discoverSkills(sourceDir)

	var created, updated, skipped []string

	sortedTgt := sortedKeys(tgtSkills)
	for _, name := range sortedTgt {
		tgtDir := tgtSkills[name]
		dstDir := filepath.Join(sourceDir, name)

		if _, exists := srcSkills[name]; exists {
			// Already in source.
			if !overwrite {
				skipped = append(skipped, name)
				continue
			}
			srcHash := hashDir(srcSkills[name])
			tgtHash := hashDir(tgtDir)
			if srcHash == tgtHash {
				skipped = append(skipped, name)
				continue
			}
			if err := replaceTree(tgtDir, dstDir, dryRun); err != nil {
				return nil, fmt.Errorf("replace skill %s: %w", name, err)
			}
			updated = append(updated, name)
		} else {
			// New skill from target.
			if err := replaceTree(tgtDir, dstDir, dryRun); err != nil {
				return nil, fmt.Errorf("copy skill %s: %w", name, err)
			}
			created = append(created, name)
		}
	}

	return map[string]any{
		"dry_run":    dryRun,
		"target":     targetName,
		"target_dir": targetDir,
		"source_dir": sourceDir,
		"created":    len(created),
		"updated":    len(updated),
		"skipped":    len(skipped),
	}, nil
}

// ── Claude org plugin support ────────────────────────────────────────

func isClaudeOrgPluginSkillsDir(path string) bool {
	clean := filepath.Clean(path)
	if filepath.Base(clean) != "skills" {
		return false
	}
	parts := strings.Split(clean, string(filepath.Separator))
	for i, part := range parts {
		if part == "org-plugins" && i+2 < len(parts) && parts[len(parts)-1] == "skills" {
			return true
		}
	}
	return false
}

func claudeOrgPluginRoot(skillsDir string) string {
	return filepath.Dir(filepath.Clean(skillsDir))
}

func prepareClaudeOrgPlugin(skillsDir string, dryRun bool) (bool, error) {
	if dryRun {
		return false, nil
	}
	root := claudeOrgPluginRoot(skillsDir)
	pluginName := filepath.Base(root)

	pluginJSON := filepath.Join(root, ".claude-plugin", "plugin.json")
	versionJSON := filepath.Join(root, "version.json")
	changed := false

	if err := os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0o755); err != nil {
		return false, err
	}
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return false, err
	}

	manifest := map[string]any{
		"name":        pluginName,
		"version":     "1.0.0",
		"description": "Agentctl-managed skills for Claude Cowork on 3P",
	}
	if normalizeFileJSON(pluginJSON) != normalizeJSON(manifest) {
		if err := tx.WriteJSONAtomic(pluginJSON, manifest); err != nil {
			return false, err
		}
		if err := os.Chmod(pluginJSON, 0o644); err != nil {
			return false, err
		}
		changed = true
	}

	if _, err := os.Stat(versionJSON); os.IsNotExist(err) {
		if err := tx.WriteJSONAtomic(versionJSON, map[string]any{"version": tx.UTCNowISO()}); err != nil {
			return false, err
		}
		if err := os.Chmod(versionJSON, 0o644); err != nil {
			return false, err
		}
		changed = true
	}

	return changed, nil
}

func bumpClaudeOrgPluginVersion(skillsDir string, dryRun bool) error {
	if dryRun {
		return nil
	}
	path := filepath.Join(claudeOrgPluginRoot(skillsDir), "version.json")
	if err := tx.WriteJSONAtomic(path, map[string]any{
		"version": tx.UTCNowISO(),
	}); err != nil {
		return err
	}
	return os.Chmod(path, 0o644)
}

func normalizeClaudeOrgPluginPermissions(skillsDir string, dryRun bool) (bool, error) {
	if dryRun {
		return false, nil
	}
	root := claudeOrgPluginRoot(skillsDir)
	changed := false
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		want := fs.FileMode(0o644)
		if d.IsDir() {
			want = 0o755
		}
		if info.Mode().Perm() == want {
			return nil
		}
		if err := os.Chmod(path, want); err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return changed, nil
}

// ── Claude Desktop/Cowork skills-plugin support ─────────────────────

func isClaudeDesktopSkillsPluginDir(path string) bool {
	clean := filepath.Clean(path)
	if filepath.Base(clean) != "skills" {
		return false
	}
	parts := strings.Split(clean, string(filepath.Separator))
	for i, part := range parts {
		if part == "local-agent-mode-sessions" && i+3 < len(parts) && parts[i+1] == "skills-plugin" && parts[len(parts)-1] == "skills" {
			return true
		}
	}
	return false
}

func claudeDesktopSkillsPluginRoot(skillsDir string) string {
	return filepath.Dir(filepath.Clean(skillsDir))
}

func prepareClaudeDesktopSkillsPlugin(skillsDir string, dryRun bool) (bool, error) {
	if dryRun {
		return false, nil
	}
	root := claudeDesktopSkillsPluginRoot(skillsDir)
	pluginJSON := filepath.Join(root, ".claude-plugin", "plugin.json")
	changed := false

	if err := os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0o700); err != nil {
		return false, err
	}
	if err := os.MkdirAll(skillsDir, 0o700); err != nil {
		return false, err
	}

	manifest := map[string]any{
		"name":        "anthropic-skills",
		"version":     "1.0.0",
		"description": "Anthropic-managed skills for Claude Desktop",
	}
	if normalizeFileJSON(pluginJSON) != normalizeJSON(manifest) {
		if err := tx.WriteJSONAtomic(pluginJSON, manifest); err != nil {
			return false, err
		}
		changed = true
	}
	return changed, nil
}

func updateClaudeDesktopSkillsManifest(skillsDir string, srcSkills map[string]string, prevManaged []string, dryRun bool) (bool, error) {
	root := claudeDesktopSkillsPluginRoot(skillsDir)
	manifestPath := filepath.Join(root, "manifest.json")

	current := map[string]any{}
	if data, err := os.ReadFile(manifestPath); err == nil {
		_ = json.Unmarshal(data, &current)
	}

	managedNow := make(map[string]bool, len(srcSkills))
	for name := range srcSkills {
		managedNow[name] = true
	}
	previouslyManaged := make(map[string]bool, len(prevManaged))
	for _, name := range prevManaged {
		previouslyManaged[name] = true
	}

	var preserved []any
	if raw, ok := current["skills"].([]any); ok {
		for _, item := range raw {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			name, _ := entry["name"].(string)
			if name == "" {
				continue
			}
			if managedNow[name] {
				continue
			}
			if isAgentctlManifestEntry(entry) || previouslyManaged[name] {
				continue
			}
			preserved = append(preserved, entry)
		}
	}

	for _, name := range sortedKeys(srcSkills) {
		preserved = append(preserved, map[string]any{
			"skillId":         name,
			"name":            name,
			"description":     readSkillDescription(srcSkills[name]),
			"creatorType":     "user",
			"syncManaged":     false,
			"agentctlManaged": true,
			"updatedAt":       tx.UTCNowISO(),
			"enabled":         true,
		})
	}

	next := map[string]any{
		"lastUpdated": time.Now().UnixMilli(),
		"skills":      preserved,
	}
	if normalizeFileJSON(manifestPath) == normalizeJSON(next) {
		return false, nil
	}
	if dryRun {
		return true, nil
	}
	if err := tx.WriteJSONAtomic(manifestPath, next); err != nil {
		return false, err
	}
	return true, nil
}

func isAgentctlManifestEntry(entry map[string]any) bool {
	v, ok := entry["agentctlManaged"].(bool)
	return ok && v
}

func readSkillDescription(skillDir string) string {
	data, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		data, err = os.ReadFile(filepath.Join(skillDir, "skill.md"))
		if err != nil {
			return ""
		}
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "---" {
			break
		}
		if strings.HasPrefix(line, "description:") {
			value := strings.TrimSpace(strings.TrimPrefix(line, "description:"))
			return strings.Trim(value, `"'`)
		}
	}
	return ""
}

func normalizeFileJSON(path string) string {
	data, err := tx.ReadJSON(path)
	if err != nil {
		return ""
	}
	return normalizeJSON(data)
}

func normalizeJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

// ── Internal helpers ─────────────────────────────────────────────────

// validateSkillName rejects skill names that could cause path traversal.
func validateSkillName(name string) error {
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("invalid skill name: %q", name)
	}
	if filepath.Base(name) != name || name != filepath.Clean(name) {
		return fmt.Errorf("invalid skill name: %q", name)
	}
	return nil
}

// discoverSkills walks root looking for directories that contain a skill
// marker file. Returns map[skillName]dirPath.
func discoverSkills(root string) map[string]string {
	skills := make(map[string]string)

	if _, err := os.Stat(root); os.IsNotExist(err) {
		return skills
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return skills
	}

	for _, entry := range entries {
		name := entry.Name()
		if err := validateSkillName(name); err != nil {
			continue
		}

		dirPath := filepath.Join(root, name)
		checkPath := dirPath // path used for isSkillDir check

		if entry.Type()&os.ModeSymlink != 0 {
			// Symlink: resolve target, must be a directory under $HOME
			resolved, err := filepath.EvalSymlinks(dirPath)
			if err != nil {
				continue
			}
			info, err := os.Stat(resolved)
			if err != nil || !info.IsDir() {
				continue
			}
			if err := tx.IsUnderHome(resolved); err != nil {
				continue
			}
			checkPath = resolved // use resolved path to avoid TOCTOU
		} else if !entry.IsDir() {
			continue
		}

		if isSkillDir(checkPath) {
			skills[name] = dirPath
		}
	}

	return skills
}

// isSkillDir checks whether dir contains one of the skill marker files.
func isSkillDir(dir string) bool {
	for _, marker := range skillMarkers {
		markerPath := filepath.Join(dir, marker)
		if info, err := os.Stat(markerPath); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

// hashFile returns the SHA-256 hex digest of a single file.
func hashFile(path string) string {
	digest, err := tx.SHA256File(path)
	if err != nil {
		return ""
	}
	return digest
}

// hashDir computes a composite SHA-256 over an entire directory tree.
// It sorts all file paths, then hashes the concatenation of each relative
// path and its file hash. Symlinks are skipped to prevent following them
// to unexpected locations.
func hashDir(dir string) string {
	// Resolve symlinks at root so WalkDir sees the real directory.
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}

	type entry struct {
		rel  string
		hash string
	}

	var entries []entry

	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil // skip symlinks
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return nil
		}
		// Normalize separators for cross-platform consistency.
		rel = filepath.ToSlash(rel)
		entries = append(entries, entry{rel: rel, hash: hashFile(path)})
		return nil
	})

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].rel < entries[j].rel
	})

	h := sha256.New()
	for _, e := range entries {
		h.Write([]byte(e.rel))
		h.Write([]byte(e.hash))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// replaceTree removes dst (if it exists) and copies the entire src tree into dst.
// If dryRun is true, no filesystem changes are made. Symlinks are skipped to
// prevent following them to unexpected locations.
func replaceTree(src, dst string, dryRun bool) error {
	if dryRun {
		return nil
	}

	// Resolve symlinks at source so WalkDir sees the real directory.
	resolvedSrc, err := filepath.EvalSymlinks(src)
	if err != nil {
		return fmt.Errorf("resolve source %s: %w", src, err)
	}
	src = resolvedSrc

	// Reject symlinked destination to prevent redirect attacks.
	if err := tx.RejectSymlink(dst); err != nil {
		return fmt.Errorf("symlink check %s: %w", dst, err)
	}

	// Remove existing destination.
	if err := os.RemoveAll(dst); err != nil {
		return fmt.Errorf("remove %s: %w", dst, err)
	}

	// Copy tree.
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip symlinks to prevent following them to unexpected locations.
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}

		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		return copyFile(path, target)
	})
}

// copyFile copies a single file from src to dst atomically (temp+rename),
// preserving permissions.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	// Write to temp file in same directory, then rename for atomicity.
	tmpFile, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()

	if _, err := io.Copy(tmpFile, in); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, info.Mode()); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

// ── Managed state persistence ────────────────────────────────────────

// managedStatePath returns the path to the managed skills state file.
func managedStatePath(stateDir string) string {
	return filepath.Join(stateDir, "skills", "managed.json")
}

// loadManagedState reads the managed skills state from disk.
// Returns map[targetName][]skillName.
func loadManagedState(stateDir string) map[string][]string {
	path := managedStatePath(stateDir)

	data, err := os.ReadFile(path)
	if err != nil {
		return make(map[string][]string)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return make(map[string][]string)
	}

	result := make(map[string][]string)
	for key, val := range raw {
		arr, ok := val.([]any)
		if !ok {
			continue
		}
		names := make([]string, 0, len(arr))
		for _, item := range arr {
			if s, ok := item.(string); ok {
				// Sanitise names from state to prevent path-traversal deletions
				if validateSkillName(s) != nil {
					continue
				}
				names = append(names, s)
			}
		}
		result[key] = names
	}

	return result
}

// saveManagedState persists the managed skills state to disk atomically.
func saveManagedState(stateDir string, data map[string][]string) error {
	path := managedStatePath(stateDir)

	// Convert to map[string]any for WriteJSONAtomic.
	obj := make(map[string]any, len(data))
	for k, v := range data {
		obj[k] = v
	}

	return tx.WriteJSONAtomic(path, obj)
}

// ── Utility ──────────────────────────────────────────────────────────

// dirExists returns true if path exists and is a directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// syncStatus returns "synced" when unsynced==0, otherwise "drift".
func syncStatus(unsynced int) string {
	if unsynced == 0 {
		return "synced"
	}
	return "drift"
}

// sortedKeys returns the keys of a map sorted alphabetically.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sortedBoolKeys returns the true-valued keys of a bool map, sorted.
func sortedBoolKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k, v := range m {
		if v {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// ── Exported aliases for testing ─────────────────────────────────────

// DiscoverSkills is an exported wrapper around discoverSkills for testing.
func DiscoverSkills(root string) map[string]string {
	return discoverSkills(root)
}

// HashDir is an exported wrapper around hashDir for testing.
func HashDir(dir string) string {
	return hashDir(dir)
}
