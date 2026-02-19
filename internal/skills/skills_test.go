package skills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ── helpers ──────────────────────────────────────────────────────────

// createSkill creates a skill directory with a SKILL.md marker and optional extra files.
func createSkill(t *testing.T, root, name, markerContent string, extraFiles map[string]string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create skill dir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(markerContent), 0o644); err != nil {
		t.Fatalf("failed to write SKILL.md for %s: %v", name, err)
	}
	for fname, content := range extraFiles {
		fpath := filepath.Join(dir, fname)
		if err := os.MkdirAll(filepath.Dir(fpath), 0o755); err != nil {
			t.Fatalf("failed to create dir for %s: %v", fpath, err)
		}
		if err := os.WriteFile(fpath, []byte(content), 0o644); err != nil {
			t.Fatalf("failed to write %s: %v", fpath, err)
		}
	}
}

// writeManagedState writes a managed.json file for testing.
func writeManagedState(t *testing.T, stateDir string, data map[string][]string) {
	t.Helper()
	dir := filepath.Join(stateDir, "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create state dir: %v", err)
	}
	obj := make(map[string]any, len(data))
	for k, v := range data {
		obj[k] = v
	}
	encoded, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal managed state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "managed.json"), encoded, 0o644); err != nil {
		t.Fatalf("failed to write managed.json: %v", err)
	}
}

// readManagedState reads the managed.json state for verification.
func readManagedState(t *testing.T, stateDir string) map[string][]string {
	t.Helper()
	path := filepath.Join(stateDir, "skills", "managed.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read managed.json: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to parse managed.json: %v", err)
	}
	result := make(map[string][]string)
	for k, v := range raw {
		arr, ok := v.([]any)
		if !ok {
			continue
		}
		names := make([]string, 0, len(arr))
		for _, item := range arr {
			if s, ok := item.(string); ok {
				names = append(names, s)
			}
		}
		result[k] = names
	}
	return result
}

// ── SkillsStatus ─────────────────────────────────────────────────────

func TestSkillsStatus_DetectsMissingAndDrift(t *testing.T) {
	srcDir := t.TempDir()
	tgtDir := t.TempDir()

	// Source has skills: alpha, beta, gamma.
	createSkill(t, srcDir, "alpha", "# Alpha skill", nil)
	createSkill(t, srcDir, "beta", "# Beta skill", map[string]string{
		"handler.py": "def handle(): pass",
	})
	createSkill(t, srcDir, "gamma", "# Gamma skill", nil)

	// Target has alpha (identical), beta (drifted), but NOT gamma.
	createSkill(t, tgtDir, "alpha", "# Alpha skill", nil)
	createSkill(t, tgtDir, "beta", "# Beta skill", map[string]string{
		"handler.py": "def handle(): return True  # drifted",
	})

	// Target also has a local-only skill "delta".
	createSkill(t, tgtDir, "delta", "# Delta local skill", nil)

	targets := map[string]string{"test-agent": tgtDir}
	result := SkillsStatus(srcDir, targets)

	// Verify source count.
	if result["source_count"] != 3 {
		t.Errorf("source_count = %v, want 3", result["source_count"])
	}

	targetResults, ok := result["targets"].([]map[string]any)
	if !ok || len(targetResults) == 0 {
		t.Fatal("targets is empty or not the expected type")
	}

	tr := targetResults[0]
	if tr["target"] != "test-agent" {
		t.Errorf("target name = %v, want 'test-agent'", tr["target"])
	}

	// shared: alpha and beta are in both.
	if tr["shared"] != 2 {
		t.Errorf("shared = %v, want 2", tr["shared"])
	}

	// missing: gamma is in source but not target.
	if tr["missing"] != 1 {
		t.Errorf("missing = %v, want 1 (gamma)", tr["missing"])
	}

	// drift: beta has different content.
	if tr["drift"] != 1 {
		t.Errorf("drift = %v, want 1 (beta)", tr["drift"])
	}

	driftList, _ := tr["drift_list"].([]string)
	if len(driftList) != 1 || driftList[0] != "beta" {
		t.Errorf("drift_list = %v, want [beta]", driftList)
	}

	// local_only: delta is only in target.
	if tr["local"] != 1 {
		t.Errorf("local_only = %v, want 1 (delta)", tr["local"])
	}

	// unsynced = missing + drift = 2.
	if tr["unsynced"] != 2 {
		t.Errorf("unsynced = %v, want 2", tr["unsynced"])
	}

	if result["unsynced_total"] != 2 {
		t.Errorf("unsynced_total = %v, want 2", result["unsynced_total"])
	}
}

func TestSkillsStatus_EmptyTargets(t *testing.T) {
	srcDir := t.TempDir()
	tgtDir := t.TempDir()

	createSkill(t, srcDir, "alpha", "# Alpha", nil)

	targets := map[string]string{"empty-agent": tgtDir}
	result := SkillsStatus(srcDir, targets)

	targetResults := result["targets"].([]map[string]any)
	tr := targetResults[0]

	if tr["shared"] != 0 {
		t.Errorf("shared = %v, want 0", tr["shared"])
	}
	if tr["missing"] != 1 {
		t.Errorf("missing = %v, want 1", tr["missing"])
	}
}

// ── SkillsSync ───────────────────────────────────────────────────────

func TestSkillsSync_CopiesAndRemovesStale(t *testing.T) {
	srcDir := t.TempDir()
	tgtDir := t.TempDir()
	stateDir := t.TempDir()

	// Source has skills: alpha, beta.
	createSkill(t, srcDir, "alpha", "# Alpha skill", map[string]string{
		"main.py": "print('alpha')",
	})
	createSkill(t, srcDir, "beta", "# Beta skill", nil)

	// Target is empty.
	targets := map[string]string{"agent1": tgtDir}

	// First sync: should copy both skills.
	result := SkillsSync(srcDir, targets, stateDir, false)

	if result["actions"] != 2 {
		t.Errorf("first sync actions = %v, want 2", result["actions"])
	}

	targetResults := result["targets"].([]map[string]any)
	tr := targetResults[0]

	if tr["created"] != 2 {
		t.Errorf("first sync created = %v, want 2", tr["created"])
	}

	// Verify files were actually copied.
	if _, err := os.Stat(filepath.Join(tgtDir, "alpha", "SKILL.md")); err != nil {
		t.Error("alpha/SKILL.md not copied to target")
	}
	if _, err := os.Stat(filepath.Join(tgtDir, "alpha", "main.py")); err != nil {
		t.Error("alpha/main.py not copied to target")
	}
	if _, err := os.Stat(filepath.Join(tgtDir, "beta", "SKILL.md")); err != nil {
		t.Error("beta/SKILL.md not copied to target")
	}

	// Verify managed state was written.
	state := readManagedState(t, stateDir)
	agent1Managed := state["agent1"]
	if len(agent1Managed) != 2 {
		t.Errorf("managed state has %d skills, want 2", len(agent1Managed))
	}

	// Now remove beta from source and re-sync. Beta should be removed from target.
	os.RemoveAll(filepath.Join(srcDir, "beta"))

	result2 := SkillsSync(srcDir, targets, stateDir, false)

	targetResults2 := result2["targets"].([]map[string]any)
	tr2 := targetResults2[0]

	if tr2["removed"] != 1 {
		t.Errorf("second sync removed = %v, want 1", tr2["removed"])
	}

	// Verify beta was actually removed from target.
	if _, err := os.Stat(filepath.Join(tgtDir, "beta")); !os.IsNotExist(err) {
		t.Error("beta should have been removed from target after source deletion")
	}

	// alpha should still exist.
	if _, err := os.Stat(filepath.Join(tgtDir, "alpha", "SKILL.md")); err != nil {
		t.Error("alpha should still exist after sync")
	}
}

func TestSkillsSync_UpdatesDrifted(t *testing.T) {
	srcDir := t.TempDir()
	tgtDir := t.TempDir()
	stateDir := t.TempDir()

	targets := map[string]string{"agent1": tgtDir}

	// Create identical skill in source and target.
	createSkill(t, srcDir, "alpha", "# Alpha v1", nil)
	createSkill(t, tgtDir, "alpha", "# Alpha v1", nil)

	// First sync: no changes needed since content is identical.
	result := SkillsSync(srcDir, targets, stateDir, false)
	if result["actions"] != 0 {
		t.Errorf("sync with identical content: actions = %v, want 0", result["actions"])
	}

	// Now modify source (making it v2).
	os.WriteFile(filepath.Join(srcDir, "alpha", "SKILL.md"), []byte("# Alpha v2"), 0o644)

	// Second sync: should update the drifted skill.
	result2 := SkillsSync(srcDir, targets, stateDir, false)

	targetResults := result2["targets"].([]map[string]any)
	tr := targetResults[0]

	if tr["updated"] != 1 {
		t.Errorf("updated = %v, want 1", tr["updated"])
	}

	// Verify target has the new content.
	content, err := os.ReadFile(filepath.Join(tgtDir, "alpha", "SKILL.md"))
	if err != nil {
		t.Fatalf("failed to read target SKILL.md: %v", err)
	}
	if string(content) != "# Alpha v2" {
		t.Errorf("target content = %q, want '# Alpha v2'", string(content))
	}
}

func TestSkillsSync_DryRunMakesNoChanges(t *testing.T) {
	srcDir := t.TempDir()
	tgtDir := t.TempDir()
	stateDir := t.TempDir()

	createSkill(t, srcDir, "alpha", "# Alpha skill", nil)

	targets := map[string]string{"agent1": tgtDir}

	result := SkillsSync(srcDir, targets, stateDir, true)

	if result["dry_run"] != true {
		t.Error("dry_run flag should be true")
	}

	// No files should have been created in target.
	entries, _ := os.ReadDir(tgtDir)
	if len(entries) != 0 {
		t.Errorf("dry run should not create files, found %d entries in target", len(entries))
	}

	// No managed state file should have been created.
	if _, err := os.Stat(filepath.Join(stateDir, "skills", "managed.json")); !os.IsNotExist(err) {
		t.Error("dry run should not create managed.json")
	}
}

func TestSkillsSync_MultipleTargets(t *testing.T) {
	srcDir := t.TempDir()
	tgt1 := t.TempDir()
	tgt2 := t.TempDir()
	stateDir := t.TempDir()

	createSkill(t, srcDir, "alpha", "# Alpha skill", nil)

	targets := map[string]string{
		"agent1": tgt1,
		"agent2": tgt2,
	}

	result := SkillsSync(srcDir, targets, stateDir, false)

	if result["actions"] != 2 {
		t.Errorf("total actions = %v, want 2 (one copy per target)", result["actions"])
	}

	// Verify both targets received the skill.
	for name, dir := range targets {
		if _, err := os.Stat(filepath.Join(dir, "alpha", "SKILL.md")); err != nil {
			t.Errorf("alpha/SKILL.md not copied to target %q (%s)", name, dir)
		}
	}
}

// ── SkillsPull ────────────────────────────────────────────────────────

func TestSkillsPull_RespectsOverwrite(t *testing.T) {
	srcDir := t.TempDir()
	tgtDir := t.TempDir()

	// Source has alpha v1.
	createSkill(t, srcDir, "alpha", "# Alpha v1", nil)

	// Target has alpha v2 (different content) and beta (new).
	createSkill(t, tgtDir, "alpha", "# Alpha v2", nil)
	createSkill(t, tgtDir, "beta", "# Beta from target", nil)

	// Pull WITHOUT overwrite: alpha should be skipped, beta should be created.
	result, err := SkillsPull(srcDir, "test-agent", tgtDir, false, false)
	if err != nil {
		t.Fatalf("SkillsPull failed: %v", err)
	}

	if result["created"] != 1 {
		t.Errorf("created = %v, want 1", result["created"])
	}
	if result["skipped"] != 1 {
		t.Errorf("skipped = %v, want 1", result["skipped"])
	}

	// Source alpha should still be v1.
	content, err := os.ReadFile(filepath.Join(srcDir, "alpha", "SKILL.md"))
	if err != nil {
		t.Fatalf("failed to read source alpha: %v", err)
	}
	if string(content) != "# Alpha v1" {
		t.Errorf("source alpha content = %q, want '# Alpha v1'", string(content))
	}

	// Source beta should exist now.
	content, err = os.ReadFile(filepath.Join(srcDir, "beta", "SKILL.md"))
	if err != nil {
		t.Fatalf("failed to read source beta: %v", err)
	}
	if string(content) != "# Beta from target" {
		t.Errorf("source beta content = %q, want '# Beta from target'", string(content))
	}
}

func TestSkillsPull_WithOverwrite(t *testing.T) {
	srcDir := t.TempDir()
	tgtDir := t.TempDir()

	// Source has alpha v1.
	createSkill(t, srcDir, "alpha", "# Alpha v1", nil)

	// Target has alpha v2.
	createSkill(t, tgtDir, "alpha", "# Alpha v2", nil)

	// Pull WITH overwrite: alpha should be updated.
	result, err := SkillsPull(srcDir, "test-agent", tgtDir, false, true)
	if err != nil {
		t.Fatalf("SkillsPull failed: %v", err)
	}

	if result["updated"] != 1 {
		t.Errorf("updated = %v, want 1", result["updated"])
	}

	// Source alpha should now be v2.
	content, err := os.ReadFile(filepath.Join(srcDir, "alpha", "SKILL.md"))
	if err != nil {
		t.Fatalf("failed to read source alpha: %v", err)
	}
	if string(content) != "# Alpha v2" {
		t.Errorf("source alpha content = %q, want '# Alpha v2'", string(content))
	}
}

func TestSkillsPull_OverwriteSkipsIdentical(t *testing.T) {
	srcDir := t.TempDir()
	tgtDir := t.TempDir()

	// Source and target have identical alpha.
	createSkill(t, srcDir, "alpha", "# Alpha same", nil)
	createSkill(t, tgtDir, "alpha", "# Alpha same", nil)

	result, err := SkillsPull(srcDir, "test-agent", tgtDir, false, true)
	if err != nil {
		t.Fatalf("SkillsPull failed: %v", err)
	}

	if result["skipped"] != 1 {
		t.Errorf("identical skills should be skipped even with overwrite, got skipped=%v", result["skipped"])
	}
}

func TestSkillsPull_DryRunMakesNoChanges(t *testing.T) {
	srcDir := t.TempDir()
	tgtDir := t.TempDir()

	createSkill(t, tgtDir, "beta", "# Beta from target", nil)

	result, err := SkillsPull(srcDir, "test-agent", tgtDir, true, false)
	if err != nil {
		t.Fatalf("SkillsPull failed: %v", err)
	}

	if result["dry_run"] != true {
		t.Error("dry_run flag should be true")
	}

	// Source should still be empty (no beta created).
	entries, _ := os.ReadDir(srcDir)
	skills := 0
	for _, e := range entries {
		if e.IsDir() {
			skills++
		}
	}
	if skills != 0 {
		t.Errorf("dry run should not create skills in source, found %d dirs", skills)
	}
}

func TestSkillsPull_NonexistentTargetReturnsError(t *testing.T) {
	srcDir := t.TempDir()
	_, err := SkillsPull(srcDir, "missing", "/nonexistent/path/target", false, false)
	if err == nil {
		t.Error("SkillsPull should return error for nonexistent target directory")
	}
}

// ── DiscoverSkills / HashDir ─────────────────────────────────────────

func TestDiscoverSkills_FindsMarkerDirs(t *testing.T) {
	root := t.TempDir()

	// Create two skills (one with SKILL.md, one with skill.md).
	createSkill(t, root, "alpha", "# Alpha", nil)

	betaDir := filepath.Join(root, "beta")
	os.MkdirAll(betaDir, 0o755)
	os.WriteFile(filepath.Join(betaDir, "skill.md"), []byte("# Beta lowercase"), 0o644)

	// Create a non-skill directory (no marker).
	noSkillDir := filepath.Join(root, "not-a-skill")
	os.MkdirAll(noSkillDir, 0o755)
	os.WriteFile(filepath.Join(noSkillDir, "README.md"), []byte("# Not a skill"), 0o644)

	skills := DiscoverSkills(root)

	if len(skills) != 2 {
		t.Fatalf("DiscoverSkills found %d skills, want 2", len(skills))
	}

	if _, ok := skills["alpha"]; !ok {
		t.Error("DiscoverSkills missing 'alpha'")
	}
	if _, ok := skills["beta"]; !ok {
		t.Error("DiscoverSkills missing 'beta'")
	}
	if _, ok := skills["not-a-skill"]; ok {
		t.Error("DiscoverSkills should not include 'not-a-skill'")
	}
}

func TestDiscoverSkills_EmptyDir(t *testing.T) {
	root := t.TempDir()
	skills := DiscoverSkills(root)
	if len(skills) != 0 {
		t.Errorf("DiscoverSkills on empty dir found %d skills, want 0", len(skills))
	}
}

func TestDiscoverSkills_NonexistentDir(t *testing.T) {
	skills := DiscoverSkills("/nonexistent/path")
	if len(skills) != 0 {
		t.Errorf("DiscoverSkills on nonexistent dir found %d skills, want 0", len(skills))
	}
}

func TestHashDir_IdenticalContentSameHash(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	// Write identical files to both directories.
	for _, dir := range []string{dir1, dir2} {
		os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# Test"), 0o644)
		os.WriteFile(filepath.Join(dir, "main.py"), []byte("pass"), 0o644)
	}

	h1 := HashDir(dir1)
	h2 := HashDir(dir2)

	if h1 != h2 {
		t.Errorf("identical directories have different hashes: %q vs %q", h1, h2)
	}
}

func TestHashDir_DifferentContentDifferentHash(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	os.WriteFile(filepath.Join(dir1, "SKILL.md"), []byte("# Version 1"), 0o644)
	os.WriteFile(filepath.Join(dir2, "SKILL.md"), []byte("# Version 2"), 0o644)

	h1 := HashDir(dir1)
	h2 := HashDir(dir2)

	if h1 == h2 {
		t.Error("directories with different content should have different hashes")
	}
}

// ── SkillsList ───────────────────────────────────────────────────────

func TestSkillsList(t *testing.T) {
	root := t.TempDir()

	createSkill(t, root, "alpha", "# Alpha", nil)
	createSkill(t, root, "beta", "# Beta", nil)

	result := SkillsList(root)

	if result["count"] != 2 {
		t.Errorf("SkillsList count = %v, want 2", result["count"])
	}

	names, ok := result["skills"].([]string)
	if !ok {
		t.Fatal("skills is not []string")
	}
	if len(names) != 2 || names[0] != "alpha" || names[1] != "beta" {
		t.Errorf("skills = %v, want [alpha beta]", names)
	}
}
