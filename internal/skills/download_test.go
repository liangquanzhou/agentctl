package skills

import (
	"os"
	"path/filepath"
	"testing"
)

// ── normalizeGitURL ─────────────────────────────────────────────────

func TestNormalizeGitURL_FullHTTPS(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://github.com/user/repo", "https://github.com/user/repo.git"},
		{"https://github.com/user/repo.git", "https://github.com/user/repo.git"},
		{"https://git.xiaojukeji.com/team/skills", "https://git.xiaojukeji.com/team/skills.git"},
		{"https://gitlab.com/org/repo.git", "https://gitlab.com/org/repo.git"},
		{"http://internal.host/group/repo", "http://internal.host/group/repo.git"},
	}
	for _, tt := range tests {
		got := normalizeGitURL(tt.input)
		if got != tt.want {
			t.Errorf("normalizeGitURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNormalizeGitURL_NoScheme(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"github.com/user/repo", "https://github.com/user/repo.git"},
		{"gitlab.com/org/repo", "https://gitlab.com/org/repo.git"},
		{"git.xiaojukeji.com/team/skills", "https://git.xiaojukeji.com/team/skills.git"},
	}
	for _, tt := range tests {
		got := normalizeGitURL(tt.input)
		if got != tt.want {
			t.Errorf("normalizeGitURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNormalizeGitURL_Shorthand(t *testing.T) {
	got := normalizeGitURL("user/repo")
	want := "https://github.com/user/repo.git"
	if got != want {
		t.Errorf("normalizeGitURL(user/repo) = %q, want %q", got, want)
	}
}

func TestNormalizeGitURL_SSH(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"git@github.com:user/repo", "git@github.com:user/repo.git"},
		{"git@git.xiaojukeji.com:team/skills", "git@git.xiaojukeji.com:team/skills.git"},
		{"git@gitlab.com:org/repo.git", "git@gitlab.com:org/repo.git"},
	}
	for _, tt := range tests {
		got := normalizeGitURL(tt.input)
		if got != tt.want {
			t.Errorf("normalizeGitURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNormalizeGitURL_Invalid(t *testing.T) {
	invalids := []string{
		"",
		"just-a-word",
		"/absolute/path",
	}
	for _, input := range invalids {
		got := normalizeGitURL(input)
		if got != "" {
			t.Errorf("normalizeGitURL(%q) = %q, want empty", input, got)
		}
	}
}

func TestNormalizeGitURL_TrimsWhitespace(t *testing.T) {
	got := normalizeGitURL("  user/repo  ")
	want := "https://github.com/user/repo.git"
	if got != want {
		t.Errorf("normalizeGitURL with whitespace = %q, want %q", got, want)
	}
}

// ── ParseSkillMD ─────────────────────────────────────────────────────

func TestParseSkillMD_WithFrontMatter(t *testing.T) {
	dir := t.TempDir()
	content := `---
name: my-cool-skill
description: A cool skill for testing
---
# My Cool Skill

This is a test skill.
`
	path := filepath.Join(dir, "SKILL.md")
	os.WriteFile(path, []byte(content), 0o644)

	meta, err := ParseSkillMD(path)
	if err != nil {
		t.Fatalf("ParseSkillMD failed: %v", err)
	}
	if meta.Name != "my-cool-skill" {
		t.Errorf("name = %q, want 'my-cool-skill'", meta.Name)
	}
	if meta.Description != "A cool skill for testing" {
		t.Errorf("description = %q, want 'A cool skill for testing'", meta.Description)
	}
}

func TestParseSkillMD_WithQuotedValues(t *testing.T) {
	dir := t.TempDir()
	content := `---
name: "quoted-skill"
description: 'single quoted desc'
---
# Skill
`
	path := filepath.Join(dir, "SKILL.md")
	os.WriteFile(path, []byte(content), 0o644)

	meta, err := ParseSkillMD(path)
	if err != nil {
		t.Fatalf("ParseSkillMD failed: %v", err)
	}
	if meta.Name != "quoted-skill" {
		t.Errorf("name = %q, want 'quoted-skill'", meta.Name)
	}
	if meta.Description != "single quoted desc" {
		t.Errorf("description = %q, want 'single quoted desc'", meta.Description)
	}
}

func TestParseSkillMD_NoFrontMatter(t *testing.T) {
	dir := t.TempDir()
	content := "# Just a markdown file\n\nNo front matter here."
	path := filepath.Join(dir, "SKILL.md")
	os.WriteFile(path, []byte(content), 0o644)

	meta, err := ParseSkillMD(path)
	if err != nil {
		t.Fatalf("ParseSkillMD failed: %v", err)
	}
	if meta.Name != "" {
		t.Errorf("name should be empty, got %q", meta.Name)
	}
	if meta.Description != "" {
		t.Errorf("description should be empty, got %q", meta.Description)
	}
}

func TestParseSkillMD_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	os.WriteFile(path, []byte(""), 0o644)

	meta, err := ParseSkillMD(path)
	if err != nil {
		t.Fatalf("ParseSkillMD failed: %v", err)
	}
	if meta.Name != "" {
		t.Errorf("name should be empty, got %q", meta.Name)
	}
}

func TestParseSkillMD_NonexistentFile(t *testing.T) {
	_, err := ParseSkillMD("/nonexistent/SKILL.md")
	if err == nil {
		t.Error("ParseSkillMD should fail for nonexistent file")
	}
}

// ── findSkillDirs ────────────────────────────────────────────────────

func TestFindSkillDirs_RootSkill(t *testing.T) {
	root := t.TempDir()
	// Create a SKILL.md at root
	os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("---\nname: root-skill\n---\n# Root"), 0o644)

	found := findSkillDirs(root)
	if len(found) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(found))
	}
	if found[0].name != "root-skill" {
		t.Errorf("name = %q, want 'root-skill'", found[0].name)
	}
	if found[0].relPath != "." {
		t.Errorf("relPath = %q, want '.'", found[0].relPath)
	}
}

func TestFindSkillDirs_SubdirectorySkills(t *testing.T) {
	root := t.TempDir()
	// Create two skill subdirectories
	createSkill(t, root, "alpha", "---\nname: alpha\n---\n# Alpha", nil)
	createSkill(t, root, "beta", "---\nname: beta\n---\n# Beta", nil)
	// Create a non-skill directory
	os.MkdirAll(filepath.Join(root, "docs"), 0o755)
	os.WriteFile(filepath.Join(root, "docs", "README.md"), []byte("# Docs"), 0o644)

	found := findSkillDirs(root)
	if len(found) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(found))
	}

	names := make(map[string]bool)
	for _, c := range found {
		names[c.name] = true
	}
	if !names["alpha"] {
		t.Error("missing skill 'alpha'")
	}
	if !names["beta"] {
		t.Error("missing skill 'beta'")
	}
}

func TestFindSkillDirs_SkipsHiddenDirs(t *testing.T) {
	root := t.TempDir()
	// Create a hidden directory with SKILL.md
	hiddenDir := filepath.Join(root, ".hidden")
	os.MkdirAll(hiddenDir, 0o755)
	os.WriteFile(filepath.Join(hiddenDir, "SKILL.md"), []byte("# Hidden"), 0o644)
	// Create a normal skill
	createSkill(t, root, "visible", "# Visible", nil)

	found := findSkillDirs(root)
	if len(found) != 1 {
		t.Fatalf("expected 1 skill (hidden skipped), got %d", len(found))
	}
	if found[0].name != "visible" {
		t.Errorf("name = %q, want 'visible'", found[0].name)
	}
}

func TestFindSkillDirs_NoSkills(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "README.md"), []byte("# No skills here"), 0o644)

	found := findSkillDirs(root)
	if len(found) != 0 {
		t.Errorf("expected 0 skills, got %d", len(found))
	}
}

func TestFindSkillDirs_NameFromDirectory(t *testing.T) {
	root := t.TempDir()
	// Skill without front matter name -> should use directory name
	createSkill(t, root, "my-dir-skill", "# No front matter name", nil)

	found := findSkillDirs(root)
	if len(found) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(found))
	}
	if found[0].name != "my-dir-skill" {
		t.Errorf("name = %q, want 'my-dir-skill'", found[0].name)
	}
}

// ── resolveSkillName ─────────────────────────────────────────────────

func TestResolveSkillName_FromMeta(t *testing.T) {
	meta := &SkillMeta{Name: "from-meta"}
	got := resolveSkillName(meta, "fallback-dir")
	if got != "from-meta" {
		t.Errorf("resolveSkillName = %q, want 'from-meta'", got)
	}
}

func TestResolveSkillName_FallbackToDir(t *testing.T) {
	meta := &SkillMeta{Name: ""}
	got := resolveSkillName(meta, "fallback-dir")
	if got != "fallback-dir" {
		t.Errorf("resolveSkillName = %q, want 'fallback-dir'", got)
	}
}

func TestResolveSkillName_NilMeta(t *testing.T) {
	got := resolveSkillName(nil, "fallback-dir")
	if got != "fallback-dir" {
		t.Errorf("resolveSkillName(nil) = %q, want 'fallback-dir'", got)
	}
}

func TestResolveSkillName_InvalidMetaName(t *testing.T) {
	meta := &SkillMeta{Name: "../evil"}
	got := resolveSkillName(meta, "safe-dir")
	if got != "safe-dir" {
		t.Errorf("resolveSkillName with invalid name = %q, want 'safe-dir'", got)
	}
}

// ── Remove ───────────────────────────────────────────────────────────

func TestRemove_DeletesSkill(t *testing.T) {
	configDir := t.TempDir()
	skillsDir := filepath.Join(configDir, "skills")
	createSkill(t, skillsDir, "to-delete", "# Delete me", nil)

	// Verify it exists
	if _, err := os.Stat(filepath.Join(skillsDir, "to-delete")); err != nil {
		t.Fatal("skill should exist before removal")
	}

	err := Remove("to-delete", configDir)
	if err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	// Verify it's gone
	if _, err := os.Stat(filepath.Join(skillsDir, "to-delete")); !os.IsNotExist(err) {
		t.Error("skill should not exist after removal")
	}
}

func TestRemove_NonexistentSkill(t *testing.T) {
	configDir := t.TempDir()
	os.MkdirAll(filepath.Join(configDir, "skills"), 0o755)

	err := Remove("nonexistent", configDir)
	if err == nil {
		t.Error("Remove should fail for nonexistent skill")
	}
}

func TestRemove_RejectsInvalidName(t *testing.T) {
	configDir := t.TempDir()

	err := Remove("../escape", configDir)
	if err == nil {
		t.Error("Remove should reject path traversal name")
	}

	err = Remove("", configDir)
	if err == nil {
		t.Error("Remove should reject empty name")
	}
}

// ── parseFrontMatterLine ─────────────────────────────────────────────

func TestParseFrontMatterLine(t *testing.T) {
	tests := []struct {
		line     string
		wantName string
		wantDesc string
	}{
		{"name: test-skill", "test-skill", ""},
		{"description: A test skill", "", "A test skill"},
		{"name:   spaced  ", "spaced", ""},
		{"name: \"quoted\"", "quoted", ""},
		{"description: 'single'", "", "single"},
		{"unknown: value", "", ""},
		{"no-colon-here", "", ""},
	}

	for _, tt := range tests {
		meta := &SkillMeta{}
		parseFrontMatterLine(tt.line, meta)
		if meta.Name != tt.wantName {
			t.Errorf("parseFrontMatterLine(%q): name = %q, want %q", tt.line, meta.Name, tt.wantName)
		}
		if meta.Description != tt.wantDesc {
			t.Errorf("parseFrontMatterLine(%q): desc = %q, want %q", tt.line, meta.Description, tt.wantDesc)
		}
	}
}

// ── candidateNames ───────────────────────────────────────────────────

func TestCandidateNames(t *testing.T) {
	candidates := []skillCandidate{
		{name: "alpha"},
		{name: "beta"},
		{name: "gamma"},
	}
	got := candidateNames(candidates)
	want := "alpha, beta, gamma"
	if got != want {
		t.Errorf("candidateNames = %q, want %q", got, want)
	}
}

func TestCandidateNames_Empty(t *testing.T) {
	got := candidateNames(nil)
	if got != "" {
		t.Errorf("candidateNames(nil) = %q, want empty", got)
	}
}

// ── installSkill ─────────────────────────────────────────────────────

func TestInstallSkill_CopiesToDest(t *testing.T) {
	// Create source skill
	srcDir := t.TempDir()
	os.WriteFile(filepath.Join(srcDir, "SKILL.md"), []byte("---\ndescription: test desc\n---\n# Test"), 0o644)
	os.WriteFile(filepath.Join(srcDir, "handler.py"), []byte("print('hello')"), 0o644)

	skillsDir := t.TempDir()

	c := skillCandidate{
		name: "test-skill",
		dir:  srcDir,
	}

	meta, err := installSkill(c, skillsDir, "user/repo")
	if err != nil {
		t.Fatalf("installSkill failed: %v", err)
	}

	if meta.Name != "test-skill" {
		t.Errorf("name = %q, want 'test-skill'", meta.Name)
	}
	if meta.SourceURL != "user/repo" {
		t.Errorf("source = %q, want 'user/repo'", meta.SourceURL)
	}
	if meta.Description != "test desc" {
		t.Errorf("description = %q, want 'test desc'", meta.Description)
	}

	// Verify files were copied
	destDir := filepath.Join(skillsDir, "test-skill")
	if _, err := os.Stat(filepath.Join(destDir, "SKILL.md")); err != nil {
		t.Error("SKILL.md not copied")
	}
	if _, err := os.Stat(filepath.Join(destDir, "handler.py")); err != nil {
		t.Error("handler.py not copied")
	}
}

func TestInstallSkill_OverwritesExisting(t *testing.T) {
	srcDir := t.TempDir()
	os.WriteFile(filepath.Join(srcDir, "SKILL.md"), []byte("# v2"), 0o644)

	skillsDir := t.TempDir()
	// Create existing skill
	existingDir := filepath.Join(skillsDir, "my-skill")
	os.MkdirAll(existingDir, 0o755)
	os.WriteFile(filepath.Join(existingDir, "SKILL.md"), []byte("# v1"), 0o644)

	c := skillCandidate{name: "my-skill", dir: srcDir}
	_, err := installSkill(c, skillsDir, "user/repo")
	if err != nil {
		t.Fatalf("installSkill failed: %v", err)
	}

	// Content should be v2
	data, _ := os.ReadFile(filepath.Join(skillsDir, "my-skill", "SKILL.md"))
	if string(data) != "# v2" {
		t.Errorf("content = %q, want '# v2'", string(data))
	}
}

func TestInstallSkill_RemovesGitDir(t *testing.T) {
	srcDir := t.TempDir()
	os.WriteFile(filepath.Join(srcDir, "SKILL.md"), []byte("# Test"), 0o644)
	// Simulate a .git directory
	os.MkdirAll(filepath.Join(srcDir, ".git", "objects"), 0o755)
	os.WriteFile(filepath.Join(srcDir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)

	skillsDir := t.TempDir()
	c := skillCandidate{name: "git-skill", dir: srcDir}
	_, err := installSkill(c, skillsDir, "user/repo")
	if err != nil {
		t.Fatalf("installSkill failed: %v", err)
	}

	// .git should be removed
	gitDir := filepath.Join(skillsDir, "git-skill", ".git")
	if _, err := os.Stat(gitDir); !os.IsNotExist(err) {
		t.Error(".git directory should be removed after install")
	}
}

func TestInstallSkill_RejectsInvalidName(t *testing.T) {
	srcDir := t.TempDir()
	os.WriteFile(filepath.Join(srcDir, "SKILL.md"), []byte("# Test"), 0o644)

	skillsDir := t.TempDir()
	c := skillCandidate{name: "../evil", dir: srcDir}
	_, err := installSkill(c, skillsDir, "user/repo")
	if err == nil {
		t.Error("installSkill should reject invalid skill name")
	}
}

// ── Search ───────────────────────────────────────────────────────────

func TestSearch_FallbackWhenCLINotInstalled(t *testing.T) {
	// This test assumes the `skills` CLI is NOT installed in the test env.
	// If it is, the test is still valid — it just tests the installed path.
	result, err := Search("test-query")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if result == "" {
		t.Error("Search should return non-empty result")
	}
}

// ── Download (integration-level, no network) ─────────────────────────

func TestDownload_InvalidSource(t *testing.T) {
	configDir := t.TempDir()
	_, err := Download("not-a-valid-source", configDir, false)
	if err == nil {
		t.Error("Download should fail for invalid source")
	}
}
