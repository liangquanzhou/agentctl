package content

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentctl/internal/tx"
)

// ── helpers ──────────────────────────────────────────────────────────

func writeJSON(t *testing.T, path string, data map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("failed to create dir for %s: %v", path, err)
	}
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal json for %s: %v", path, err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}

func writeText(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("failed to create dir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}

// homeTmpDir creates a temporary directory under $HOME for tests that need
// paths passing the "under home" validation. Cleaned up automatically.
func homeTmpDir(t *testing.T) string {
	t.Helper()
	home := tx.HomeDir()
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	dir := filepath.Join(home, fmt.Sprintf(".agentctl-test-%d", rng.Intn(999999999)))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create home tmp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// setupRulesEnv creates a minimal config dir for rules plan/apply testing.
// Target paths are under $HOME so they pass path validation.
func setupRulesEnv(t *testing.T) (configDir, targetPath, stateDir string) {
	t.Helper()
	tmp := t.TempDir()
	htmp := homeTmpDir(t)

	configDir = filepath.Join(tmp, "config")
	rulesDir := filepath.Join(configDir, "rules")
	os.MkdirAll(rulesDir, 0o755)
	writeText(t, filepath.Join(rulesDir, "a.md"), "# Section A")
	writeText(t, filepath.Join(rulesDir, "b.md"), "# Section B")

	targetPath = filepath.Join(htmp, "target", "RULES.md")
	os.MkdirAll(filepath.Dir(targetPath), 0o755)

	writeJSON(t, filepath.Join(rulesDir, "config.json"), map[string]any{
		"agents": map[string]any{
			"test-agent": map[string]any{
				"compose":   []any{"a.md", "b.md"},
				"target":    targetPath,
				"separator": "\n\n",
			},
		},
	})

	stateDir = filepath.Join(tmp, "state")
	return configDir, targetPath, stateDir
}

// setupFullPlanEnv creates config dir with rules, commands, and ignore.
// All targets are under $HOME.
func setupFullPlanEnv(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	htmp := homeTmpDir(t)

	configDir := filepath.Join(tmp, "config")
	rulesDir := filepath.Join(configDir, "rules")
	cmdsDir := filepath.Join(configDir, "commands")
	os.MkdirAll(rulesDir, 0o755)
	os.MkdirAll(cmdsDir, 0o755)

	writeText(t, filepath.Join(rulesDir, "a.md"), "# Section A")
	writeText(t, filepath.Join(cmdsDir, "greet.md"), "# Greet skill")

	rulesTarget := filepath.Join(htmp, "agent", "RULES.md")
	os.MkdirAll(filepath.Dir(rulesTarget), 0o755)
	cmdsTarget := filepath.Join(htmp, "agent", "commands")
	os.MkdirAll(cmdsTarget, 0o755)
	ignoreTarget := filepath.Join(htmp, "agent", ".agentignore")

	writeJSON(t, filepath.Join(rulesDir, "config.json"), map[string]any{
		"agents": map[string]any{
			"test-agent": map[string]any{
				"compose": []any{"a.md"},
				"target":  rulesTarget,
			},
		},
	})

	writeJSON(t, filepath.Join(cmdsDir, "config.json"), map[string]any{
		"agents": map[string]any{
			"test-agent": map[string]any{"target_dir": cmdsTarget},
		},
	})

	writeJSON(t, filepath.Join(configDir, "ignore.json"), map[string]any{
		"patterns": []any{"node_modules", ".env"},
		"agents": map[string]any{
			"test-agent": map[string]any{"target": ignoreTarget},
		},
	})

	return configDir
}

// ── templateSub ──────────────────────────────────────────────────────

func TestTemplateSub_ReplacesAgent(t *testing.T) {
	result := templateSub("hooks run {{agent}}", "claude-code")
	if result != "hooks run claude-code" {
		t.Errorf("templateSub = %q, want 'hooks run claude-code'", result)
	}
}

func TestTemplateSub_NoPlaceholder(t *testing.T) {
	result := templateSub("plain command", "agent")
	if result != "plain command" {
		t.Errorf("templateSub = %q, want 'plain command'", result)
	}
}

func TestTemplateSub_MultipleReplacements(t *testing.T) {
	result := templateSub("{{agent}}-{{agent}}", "test")
	if result != "test-test" {
		t.Errorf("templateSub = %q, want 'test-test'", result)
	}
}

// ── composeRules ─────────────────────────────────────────────────────

func TestComposeRules_MultipleFiles(t *testing.T) {
	tmp := t.TempDir()
	writeText(t, filepath.Join(tmp, "shared.md"), "# Shared Rules")
	writeText(t, filepath.Join(tmp, "claude.md"), "# Claude Rules")

	result, err := composeRules(tmp, []string{"shared.md", "claude.md"}, "\n\n")
	if err != nil {
		t.Fatalf("composeRules failed: %v", err)
	}
	if !strings.Contains(result, "# Shared Rules") {
		t.Error("result should contain '# Shared Rules'")
	}
	if !strings.Contains(result, "# Claude Rules") {
		t.Error("result should contain '# Claude Rules'")
	}
	if !strings.Contains(result, "\n\n") {
		t.Error("result should contain separator")
	}
	if !strings.HasSuffix(result, "\n") {
		t.Error("result should end with newline")
	}
}

func TestComposeRules_CustomSeparator(t *testing.T) {
	tmp := t.TempDir()
	writeText(t, filepath.Join(tmp, "a.md"), "Part A")
	writeText(t, filepath.Join(tmp, "b.md"), "Part B")

	result, err := composeRules(tmp, []string{"a.md", "b.md"}, "\n---\n")
	if err != nil {
		t.Fatalf("composeRules failed: %v", err)
	}
	if !strings.Contains(result, "\n---\n") {
		t.Errorf("result should contain custom separator, got: %q", result)
	}
}

func TestComposeRules_MissingFile(t *testing.T) {
	tmp := t.TempDir()
	writeText(t, filepath.Join(tmp, "exists.md"), "ok")

	_, err := composeRules(tmp, []string{"nonexistent.md"}, "\n\n")
	if err == nil {
		t.Fatal("composeRules should fail for missing file")
	}
	if !strings.Contains(err.Error(), "rule source not found") {
		t.Errorf("error should mention 'rule source not found', got: %v", err)
	}
}

func TestComposeRules_RejectsPathTraversal(t *testing.T) {
	tmp := t.TempDir()
	_, err := composeRules(tmp, []string{"../secret.md"}, "\n\n")
	if err == nil {
		t.Fatal("composeRules should reject path traversal")
	}
	if !strings.Contains(err.Error(), "path traversal") {
		t.Errorf("error should mention 'path traversal', got: %v", err)
	}
}

func TestComposeRules_RejectsAbsolutePath(t *testing.T) {
	tmp := t.TempDir()
	_, err := composeRules(tmp, []string{"/etc/passwd"}, "\n\n")
	if err == nil {
		t.Fatal("composeRules should reject absolute path")
	}
	if !strings.Contains(err.Error(), "absolute path") {
		t.Errorf("error should mention 'absolute path', got: %v", err)
	}
}

func TestComposeRules_AllowsDoubleDotInFilename(t *testing.T) {
	tmp := t.TempDir()
	writeText(t, filepath.Join(tmp, "v1..md"), "# Version 1")

	result, err := composeRules(tmp, []string{"v1..md"}, "\n\n")
	if err != nil {
		t.Fatalf("double dot in filename should be allowed: %v", err)
	}
	if !strings.Contains(result, "# Version 1") {
		t.Error("result should contain content of v1..md")
	}
}

// ── buildHooksClaude ─────────────────────────────────────────────────

func TestBuildHooksClaude_Basic(t *testing.T) {
	hookCfg := map[string]any{
		"events": map[string]any{
			"SessionEnd": []any{
				map[string]any{"command": "run-hook {{agent}}", "timeout": float64(60)},
				map[string]any{"command": "another-hook"},
			},
		},
	}
	result := buildHooksClaude("claude-code", hookCfg)

	hooksList, ok := result["SessionEnd"].([]any)
	if !ok || len(hooksList) != 2 {
		t.Fatalf("SessionEnd hooks count = %d, want 2", len(hooksList))
	}

	first, _ := hooksList[0].(map[string]any)
	hooks, _ := first["hooks"].([]any)
	hook, _ := hooks[0].(map[string]any)
	if hook["command"] != "run-hook claude-code" {
		t.Errorf("first command = %v, want 'run-hook claude-code'", hook["command"])
	}
	if hook["timeout"] != float64(60) {
		t.Errorf("first timeout = %v, want 60", hook["timeout"])
	}

	second, _ := hooksList[1].(map[string]any)
	hooks2, _ := second["hooks"].([]any)
	hook2, _ := hooks2[0].(map[string]any)
	if hook2["command"] != "another-hook" {
		t.Errorf("second command = %v, want 'another-hook'", hook2["command"])
	}
	if _, hasTimeout := hook2["timeout"]; hasTimeout {
		t.Error("second hook should not have timeout")
	}
}

func TestBuildHooksClaude_TemplateSubstitution(t *testing.T) {
	hookCfg := map[string]any{
		"events": map[string]any{
			"SessionEnd": []any{
				map[string]any{"command": "kg-memory-mcp hooks run {{agent}}"},
			},
		},
	}
	result := buildHooksClaude("gemini-cli", hookCfg)
	hooksList := result["SessionEnd"].([]any)
	first := hooksList[0].(map[string]any)
	hooks := first["hooks"].([]any)
	hook := hooks[0].(map[string]any)
	if hook["command"] != "kg-memory-mcp hooks run gemini-cli" {
		t.Errorf("command = %v, want 'kg-memory-mcp hooks run gemini-cli'", hook["command"])
	}
}

func TestBuildHooksClaude_EmptyEvents(t *testing.T) {
	hookCfg := map[string]any{}
	result := buildHooksClaude("claude-code", hookCfg)
	if len(result) != 0 {
		t.Errorf("empty events should produce empty result, got: %v", result)
	}
}

// ── buildHooksCodex ──────────────────────────────────────────────────

func TestBuildHooksCodex_TemplateSubstitution(t *testing.T) {
	hookCfg := map[string]any{
		"notify": []any{"kg-memory-mcp", "hooks", "run", "{{agent}}"},
	}
	result := buildHooksCodex("codex", hookCfg)
	expected := []string{"kg-memory-mcp", "hooks", "run", "codex"}
	if len(result) != len(expected) {
		t.Fatalf("len = %d, want %d", len(result), len(expected))
	}
	for i, v := range expected {
		if result[i] != v {
			t.Errorf("result[%d] = %q, want %q", i, result[i], v)
		}
	}
}

func TestBuildHooksCodex_EmptyNotify(t *testing.T) {
	hookCfg := map[string]any{}
	result := buildHooksCodex("codex", hookCfg)
	if len(result) != 0 {
		t.Errorf("empty notify should produce empty result, got: %v", result)
	}
}

// ── buildIgnoreContent ───────────────────────────────────────────────

func TestBuildIgnoreContent_MultiplePatterns(t *testing.T) {
	result := buildIgnoreContent([]string{"node_modules", ".env", "*.log"})
	if result != "node_modules\n.env\n*.log\n" {
		t.Errorf("buildIgnoreContent = %q, want 'node_modules\\n.env\\n*.log\\n'", result)
	}
}

func TestBuildIgnoreContent_Empty(t *testing.T) {
	result := buildIgnoreContent([]string{})
	if result != "" {
		t.Errorf("buildIgnoreContent([]) = %q, want empty", result)
	}
}

// ── resolveProjectPath ───────────────────────────────────────────────

func TestResolveProjectPath_Normal(t *testing.T) {
	tmp := t.TempDir()
	result, err := resolveProjectPath(tmp, "CLAUDE.md")
	if err != nil {
		t.Fatalf("resolveProjectPath failed: %v", err)
	}
	expected, _ := filepath.Abs(filepath.Join(tmp, "CLAUDE.md"))
	if result != expected {
		t.Errorf("resolveProjectPath = %q, want %q", result, expected)
	}
}

func TestResolveProjectPath_RejectsDotDot(t *testing.T) {
	tmp := t.TempDir()
	_, err := resolveProjectPath(tmp, "../escape.md")
	if err == nil {
		t.Fatal("should reject '..' traversal")
	}
	if !strings.Contains(err.Error(), "path traversal") {
		t.Errorf("error should mention 'path traversal', got: %v", err)
	}
}

func TestResolveProjectPath_RejectsAbsolute(t *testing.T) {
	tmp := t.TempDir()
	_, err := resolveProjectPath(tmp, "/etc/passwd")
	if err == nil {
		t.Fatal("should reject absolute path")
	}
	if !strings.Contains(err.Error(), "path traversal") {
		t.Errorf("error should mention 'path traversal', got: %v", err)
	}
}

func TestResolveProjectPath_AllowsDoubleDotInName(t *testing.T) {
	tmp := t.TempDir()
	result, err := resolveProjectPath(tmp, "foo..bar.md")
	if err != nil {
		t.Fatalf("double dot in filename should be allowed: %v", err)
	}
	expected, _ := filepath.Abs(filepath.Join(tmp, "foo..bar.md"))
	if result != expected {
		t.Errorf("resolveProjectPath = %q, want %q", result, expected)
	}
}

// ── resolvePath ──────────────────────────────────────────────────────

func TestResolvePath_ExpandsTilde(t *testing.T) {
	result, err := resolvePath("~/some/file.md")
	if err != nil {
		t.Fatalf("resolvePath failed: %v", err)
	}
	if strings.Contains(result, "~") {
		t.Errorf("result should not contain tilde: %q", result)
	}
	if !strings.Contains(result, "some/file.md") {
		t.Errorf("result should contain path: %q", result)
	}
}

func TestResolvePath_RejectsEscape(t *testing.T) {
	_, err := resolvePath("/etc/passwd")
	if err == nil {
		t.Fatal("should reject path escaping home")
	}
	if !strings.Contains(err.Error(), "escapes home") {
		t.Errorf("error should mention 'escapes home', got: %v", err)
	}
}

func TestResolvePath_RejectsEmpty(t *testing.T) {
	_, err := resolvePath("")
	if err == nil {
		t.Fatal("should reject empty path")
	}
}

// ── dirSyncPlan ──────────────────────────────────────────────────────

func TestDirSyncPlan_DetectsNewFiles(t *testing.T) {
	src := t.TempDir()
	tgt := t.TempDir()

	writeText(t, filepath.Join(src, "skill-a.md"), "# Skill A")
	writeText(t, filepath.Join(src, "skill-b.md"), "# Skill B")

	items := dirSyncPlan(src, tgt, "test-agent", "commands", "")
	if len(items) != 2 {
		t.Fatalf("dirSyncPlan returned %d items, want 2", len(items))
	}
	for _, item := range items {
		if item["changed"] != true {
			t.Error("new files should be marked as changed")
		}
		if item["exists"] != false {
			t.Error("new files should not exist in target")
		}
	}
}

func TestDirSyncPlan_DetectsNoChange(t *testing.T) {
	src := t.TempDir()
	tgt := t.TempDir()

	writeText(t, filepath.Join(src, "skill-a.md"), "# Skill A")
	writeText(t, filepath.Join(tgt, "skill-a.md"), "# Skill A") // identical

	items := dirSyncPlan(src, tgt, "test-agent", "commands", "")
	if len(items) != 1 {
		t.Fatalf("dirSyncPlan returned %d items, want 1", len(items))
	}
	if items[0]["changed"] != false {
		t.Error("identical files should not be marked as changed")
	}
}

func TestDirSyncPlan_SkipsHiddenAndDirs(t *testing.T) {
	src := t.TempDir()
	tgt := t.TempDir()

	writeText(t, filepath.Join(src, ".hidden"), "hidden")
	os.MkdirAll(filepath.Join(src, "subdir"), 0o755)
	writeText(t, filepath.Join(src, "visible.md"), "# Visible")

	items := dirSyncPlan(src, tgt, "test-agent", "commands", "")
	if len(items) != 1 {
		t.Fatalf("dirSyncPlan returned %d items, want 1 (only visible.md)", len(items))
	}
	if !strings.Contains(items[0]["path"].(string), "visible.md") {
		t.Errorf("expected visible.md in plan, got: %s", items[0]["path"])
	}
}

func TestDirSyncPlan_DetectsStaleFiles(t *testing.T) {
	src := t.TempDir()
	tgt := t.TempDir()

	writeText(t, filepath.Join(src, "keep.md"), "# keep")
	writeText(t, filepath.Join(tgt, "keep.md"), "# keep")
	writeText(t, filepath.Join(tgt, "orphan.md"), "# orphan")

	items := dirSyncPlan(src, tgt, "test-agent", "commands", "")

	staleItems := make([]map[string]any, 0)
	for _, item := range items {
		if stale, ok := item["stale"].(bool); ok && stale {
			staleItems = append(staleItems, item)
		}
	}
	if len(staleItems) != 1 {
		t.Fatalf("expected 1 stale item, got %d", len(staleItems))
	}
	if !strings.Contains(staleItems[0]["path"].(string), "orphan.md") {
		t.Errorf("stale item should be orphan.md, got: %s", staleItems[0]["path"])
	}
}

func TestDirSyncPlan_StaleWhenSourceMissing(t *testing.T) {
	tgt := t.TempDir()
	writeText(t, filepath.Join(tgt, "orphan.md"), "# orphan")

	// Source dir does not exist
	items := dirSyncPlan("/nonexistent/source", tgt, "test-agent", "commands", "")

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if stale, ok := items[0]["stale"].(bool); !ok || !stale {
		t.Error("orphan should be marked as stale")
	}
}

func TestDirSyncPlan_SkipsConfigJSON(t *testing.T) {
	src := t.TempDir()
	tgt := t.TempDir()

	writeText(t, filepath.Join(src, "config.json"), `{"agents":{}}`)
	writeText(t, filepath.Join(src, "real.md"), "# Real")

	items := dirSyncPlan(src, tgt, "test-agent", "commands", "")
	if len(items) != 1 {
		t.Fatalf("expected 1 item (config.json should be skipped), got %d", len(items))
	}
}

// ── parseFrontMatter ─────────────────────────────────────────────────

func TestParseFrontMatter_WithDescription(t *testing.T) {
	md := "---\ndescription: Gemini 调用\n---\nPrompt body here\n"
	desc, body := parseFrontMatter(md)
	if desc != "Gemini 调用" {
		t.Errorf("description = %q, want 'Gemini 调用'", desc)
	}
	if body != "Prompt body here\n" {
		t.Errorf("body = %q, want 'Prompt body here\\n'", body)
	}
}

func TestParseFrontMatter_NoFrontMatter(t *testing.T) {
	md := "# Just a heading\nSome content\n"
	desc, body := parseFrontMatter(md)
	if desc != "" {
		t.Errorf("description should be empty, got %q", desc)
	}
	if body != md {
		t.Errorf("body should be the entire content")
	}
}

func TestParseFrontMatter_IncompleteDelimiter(t *testing.T) {
	md := "---\ndescription: test\nno closing delimiter"
	desc, body := parseFrontMatter(md)
	if desc != "" {
		t.Errorf("incomplete front matter should return empty desc, got %q", desc)
	}
	if body != md {
		t.Errorf("body should be entire content for incomplete front matter")
	}
}

// ── convertMdToToml ─────────────────────────────────────────────────

func TestConvertMdToToml_WithFrontMatter(t *testing.T) {
	md := "---\ndescription: Gemini 调用\n---\n使用 Gemini CLI 处理：\n$ARGUMENTS\n"
	result := convertMdToToml(md)

	if !strings.Contains(result, `description = "Gemini 调用"`) {
		t.Errorf("should contain description, got:\n%s", result)
	}
	if !strings.Contains(result, "{{args}}") {
		t.Error("should replace $ARGUMENTS with {{args}}")
	}
	if strings.Contains(result, "$ARGUMENTS") {
		t.Error("should not contain $ARGUMENTS after conversion")
	}
	if !strings.HasPrefix(result, "description = ") {
		t.Error("should start with description")
	}
	if !strings.Contains(result, "prompt = '''") {
		t.Error("should contain TOML multi-line literal string")
	}
}

func TestConvertMdToToml_NoFrontMatter(t *testing.T) {
	md := "# Just a prompt\n$ARGUMENTS\n"
	result := convertMdToToml(md)

	if !strings.Contains(result, `description = ""`) {
		t.Errorf("empty front matter should yield empty description, got:\n%s", result)
	}
	if !strings.Contains(result, "# Just a prompt") {
		t.Error("body should be preserved")
	}
}

// ── targetFileName ──────────────────────────────────────────────────

func TestTargetFileName_MdFormat(t *testing.T) {
	if name := targetFileName("gemini.md", ""); name != "gemini.md" {
		t.Errorf("empty format should keep name, got %q", name)
	}
	if name := targetFileName("gemini.md", "md"); name != "gemini.md" {
		t.Errorf("md format should keep name, got %q", name)
	}
}

func TestTargetFileName_TomlFormat(t *testing.T) {
	if name := targetFileName("gemini.md", "toml"); name != "gemini.toml" {
		t.Errorf("toml format should convert extension, got %q", name)
	}
}

func TestTargetFileName_NonMdSource(t *testing.T) {
	if name := targetFileName("readme.txt", "toml"); name != "readme.txt" {
		t.Errorf("non-.md files should keep name even with toml format, got %q", name)
	}
}

// ── dirSyncPlan with format ─────────────────────────────────────────

func TestDirSyncPlan_TomlFormat_ConvertsExtension(t *testing.T) {
	src := t.TempDir()
	tgt := t.TempDir()

	writeText(t, filepath.Join(src, "greet.md"), "---\ndescription: Greet\n---\nHello $ARGUMENTS\n")

	items := dirSyncPlan(src, tgt, "gemini-cli", "commands", "toml")
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if !strings.HasSuffix(items[0]["path"].(string), "greet.toml") {
		t.Errorf("target should be .toml, got %s", items[0]["path"])
	}
	if items[0]["format"] != "toml" {
		t.Errorf("format should be stored in item, got %v", items[0]["format"])
	}
}

func TestDirSyncPlan_TomlFormat_DetectsNoChange(t *testing.T) {
	src := t.TempDir()
	tgt := t.TempDir()

	md := "---\ndescription: Greet\n---\nHello {{args}}\n"
	writeText(t, filepath.Join(src, "greet.md"), md)
	// Write the expected converted content
	converted := convertMdToToml(md)
	writeText(t, filepath.Join(tgt, "greet.toml"), converted)

	items := dirSyncPlan(src, tgt, "gemini-cli", "commands", "toml")
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0]["changed"] != false {
		t.Error("matching converted content should not be marked as changed")
	}
}

func TestDirSyncPlan_TomlFormat_StaleOldMdInTarget(t *testing.T) {
	src := t.TempDir()
	tgt := t.TempDir()

	writeText(t, filepath.Join(src, "greet.md"), "---\ndescription: Greet\n---\nHello\n")
	// Old .md file left in target from before format switch
	writeText(t, filepath.Join(tgt, "greet.md"), "# old md file")

	items := dirSyncPlan(src, tgt, "gemini-cli", "commands", "toml")

	var staleCount, newCount int
	for _, item := range items {
		if stale, ok := item["stale"].(bool); ok && stale {
			staleCount++
			if !strings.HasSuffix(item["path"].(string), "greet.md") {
				t.Errorf("stale item should be old .md, got %s", item["path"])
			}
		} else {
			newCount++
			if !strings.HasSuffix(item["path"].(string), "greet.toml") {
				t.Errorf("new item should be .toml, got %s", item["path"])
			}
		}
	}
	if staleCount != 1 {
		t.Errorf("expected 1 stale item (old .md), got %d", staleCount)
	}
	if newCount != 1 {
		t.Errorf("expected 1 new item (.toml), got %d", newCount)
	}
}

// ── ContentPlan ──────────────────────────────────────────────────────

func TestContentPlan_DetectsNew(t *testing.T) {
	configDir, _, _ := setupRulesEnv(t)

	result, err := ContentPlan(configDir, PlanOpts{})
	if err != nil {
		t.Fatalf("ContentPlan failed: %v", err)
	}

	items, _ := result["items"].([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0]["agent"] != "test-agent" {
		t.Errorf("agent = %v, want 'test-agent'", items[0]["agent"])
	}
	if items[0]["type"] != "rules" {
		t.Errorf("type = %v, want 'rules'", items[0]["type"])
	}
	if items[0]["changed"] != true {
		t.Error("new file should be marked as changed")
	}
	if items[0]["exists"] != false {
		t.Error("new file should not exist yet")
	}
}

func TestContentPlan_DetectsNoChange(t *testing.T) {
	configDir, targetPath, _ := setupRulesEnv(t)

	// Write the expected composed content to target
	rulesDir := filepath.Join(configDir, "rules")
	composed, _ := composeRules(rulesDir, []string{"a.md", "b.md"}, "\n\n")
	writeText(t, targetPath, composed)

	result, err := ContentPlan(configDir, PlanOpts{})
	if err != nil {
		t.Fatalf("ContentPlan failed: %v", err)
	}

	items, _ := result["items"].([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0]["changed"] != false {
		t.Error("matching content should not be marked as changed")
	}
}

func TestContentPlan_RejectsDuplicateTarget(t *testing.T) {
	tmp := t.TempDir()
	htmp := homeTmpDir(t)
	configDir := filepath.Join(tmp, "config")
	rulesDir := filepath.Join(configDir, "rules")
	os.MkdirAll(rulesDir, 0o755)
	writeText(t, filepath.Join(rulesDir, "a.md"), "# A")

	targetPath := filepath.Join(htmp, "target", "RULES.md")
	os.MkdirAll(filepath.Dir(targetPath), 0o755)

	writeJSON(t, filepath.Join(rulesDir, "config.json"), map[string]any{
		"agents": map[string]any{
			"agent-alpha": map[string]any{
				"compose": []any{"a.md"},
				"target":  targetPath,
			},
			"agent-beta": map[string]any{
				"compose": []any{"a.md"},
				"target":  targetPath,
			},
		},
	})

	_, err := ContentPlan(configDir, PlanOpts{})
	if err == nil {
		t.Fatal("should reject duplicate target")
	}
	if !strings.Contains(err.Error(), "duplicate target") {
		t.Errorf("error should mention 'duplicate target', got: %v", err)
	}
}

func TestContentPlan_InvalidScope(t *testing.T) {
	configDir, _, _ := setupRulesEnv(t)

	_, err := ContentPlan(configDir, PlanOpts{Scope: "projcet"})
	if err == nil {
		t.Fatal("should reject invalid scope")
	}
	if !strings.Contains(err.Error(), "invalid scope") {
		t.Errorf("error should mention 'invalid scope', got: %v", err)
	}
}

func TestContentPlan_InvalidTypeFilter(t *testing.T) {
	configDir, _, _ := setupRulesEnv(t)

	_, err := ContentPlan(configDir, PlanOpts{TypeFilter: "hodks"})
	if err == nil {
		t.Fatal("should reject invalid type filter")
	}
	if !strings.Contains(err.Error(), "invalid type_filter") {
		t.Errorf("error should mention 'invalid type_filter', got: %v", err)
	}
}

func TestContentPlan_TypeFilterRules(t *testing.T) {
	configDir := setupFullPlanEnv(t)

	result, err := ContentPlan(configDir, PlanOpts{TypeFilter: "rules"})
	if err != nil {
		t.Fatalf("ContentPlan failed: %v", err)
	}

	items, _ := result["items"].([]map[string]any)
	for _, item := range items {
		if item["type"] != "rules" {
			t.Errorf("type_filter='rules' should only return rules, got: %s", item["type"])
		}
	}
}

func TestContentPlan_TypeFilterCommands(t *testing.T) {
	configDir := setupFullPlanEnv(t)

	result, err := ContentPlan(configDir, PlanOpts{TypeFilter: "commands"})
	if err != nil {
		t.Fatalf("ContentPlan failed: %v", err)
	}

	items, _ := result["items"].([]map[string]any)
	for _, item := range items {
		if item["type"] != "commands" {
			t.Errorf("type_filter='commands' should only return commands, got: %s", item["type"])
		}
	}
}

func TestContentPlan_TypeFilterIgnore(t *testing.T) {
	configDir := setupFullPlanEnv(t)

	result, err := ContentPlan(configDir, PlanOpts{TypeFilter: "ignore"})
	if err != nil {
		t.Fatalf("ContentPlan failed: %v", err)
	}

	items, _ := result["items"].([]map[string]any)
	for _, item := range items {
		if item["type"] != "ignore" {
			t.Errorf("type_filter='ignore' should only return ignore, got: %s", item["type"])
		}
	}
}

func TestContentPlan_ProjectScopeRejectsNonRulesFilter(t *testing.T) {
	configDir, _, _ := setupRulesEnv(t)

	_, err := ContentPlan(configDir, PlanOpts{
		Scope:      "project",
		ProjectDir: t.TempDir(),
		TypeFilter: "hooks",
	})
	if err == nil {
		t.Fatal("project scope with type_filter='hooks' should fail")
	}
	if !strings.Contains(err.Error(), "scope='project' only supports") {
		t.Errorf("error should mention constraint, got: %v", err)
	}
}

func TestContentPlan_IncludesAllTypes(t *testing.T) {
	configDir := setupFullPlanEnv(t)

	result, err := ContentPlan(configDir, PlanOpts{})
	if err != nil {
		t.Fatalf("ContentPlan failed: %v", err)
	}

	items, _ := result["items"].([]map[string]any)
	types := make(map[string]bool)
	for _, item := range items {
		types[item["type"].(string)] = true
	}

	for _, expected := range []string{"rules", "commands", "ignore"} {
		if !types[expected] {
			t.Errorf("plan should include type %q, types found: %v", expected, types)
		}
	}
}

// ── ContentApply ─────────────────────────────────────────────────────

func TestContentApply_CreatesRules(t *testing.T) {
	configDir, targetPath, stateDir := setupRulesEnv(t)

	manifest, err := ContentApply(configDir, stateDir, ApplyOpts{})
	if err != nil {
		t.Fatalf("ContentApply failed: %v", err)
	}

	if manifest["result"] != "success" {
		t.Errorf("result = %v, want 'success'", manifest["result"])
	}

	changedFiles, _ := manifest["changed_files"].([]any)
	if len(changedFiles) != 1 {
		t.Fatalf("expected 1 changed file, got %d", len(changedFiles))
	}

	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("target file not created: %v", err)
	}
	if !strings.Contains(string(data), "# Section A") {
		t.Error("target should contain '# Section A'")
	}
	if !strings.Contains(string(data), "# Section B") {
		t.Error("target should contain '# Section B'")
	}
}

func TestContentApply_NoChanges(t *testing.T) {
	configDir, _, stateDir := setupRulesEnv(t)

	// First apply
	_, err := ContentApply(configDir, stateDir, ApplyOpts{})
	if err != nil {
		t.Fatalf("first apply failed: %v", err)
	}

	// Second apply — no changes
	manifest, err := ContentApply(configDir, stateDir, ApplyOpts{})
	if err != nil {
		t.Fatalf("second apply failed: %v", err)
	}
	if manifest["result"] != "no_changes" {
		t.Errorf("result = %v, want 'no_changes'", manifest["result"])
	}
	changedFiles, _ := manifest["changed_files"].([]any)
	if len(changedFiles) != 0 {
		t.Errorf("expected 0 changed files, got %d", len(changedFiles))
	}
}

func TestContentApply_InvalidTypeFilter(t *testing.T) {
	configDir, _, stateDir := setupRulesEnv(t)

	_, err := ContentApply(configDir, stateDir, ApplyOpts{TypeFilter: "rulez"})
	if err == nil {
		t.Fatal("should reject invalid type_filter")
	}
	if !strings.Contains(err.Error(), "invalid type_filter") {
		t.Errorf("error should mention 'invalid type_filter', got: %v", err)
	}
}

func TestContentApply_CreatesSnapshot(t *testing.T) {
	configDir, targetPath, stateDir := setupRulesEnv(t)

	// Write existing target with old content
	writeText(t, targetPath, "old content")

	manifest, err := ContentApply(configDir, stateDir, ApplyOpts{})
	if err != nil {
		t.Fatalf("ContentApply failed: %v", err)
	}

	changedFiles, _ := manifest["changed_files"].([]any)
	if len(changedFiles) != 1 {
		t.Fatalf("expected 1 changed file, got %d", len(changedFiles))
	}

	change, _ := changedFiles[0].(map[string]any)
	if change["pre_exists"] != true {
		t.Error("pre_exists should be true")
	}
	if change["snapshot"] == nil {
		t.Error("snapshot should not be nil")
	}

	// Verify snapshot file exists and has old content
	snapPath := change["snapshot"].(string)
	snapData, err := os.ReadFile(snapPath)
	if err != nil {
		t.Fatalf("snapshot file not found: %v", err)
	}
	if string(snapData) != "old content" {
		t.Errorf("snapshot content = %q, want 'old content'", string(snapData))
	}
}

func TestContentApply_RunManifestRecorded(t *testing.T) {
	configDir, _, stateDir := setupRulesEnv(t)

	manifest, err := ContentApply(configDir, stateDir, ApplyOpts{})
	if err != nil {
		t.Fatalf("ContentApply failed: %v", err)
	}

	runID := manifest["run_id"].(string)
	runFile := filepath.Join(stateDir, "runs", runID+".json")
	if _, err := os.Stat(runFile); os.IsNotExist(err) {
		t.Fatal("run manifest file should exist")
	}

	data, _ := os.ReadFile(runFile)
	var recorded map[string]any
	json.Unmarshal(data, &recorded)
	if recorded["command"] != "content_apply" {
		t.Errorf("recorded command = %v, want 'content_apply'", recorded["command"])
	}
}

func TestContentApply_BreakGlassRequiresReason(t *testing.T) {
	configDir, _, stateDir := setupRulesEnv(t)

	_, err := ContentApply(configDir, stateDir, ApplyOpts{BreakGlass: true})
	if err == nil {
		t.Fatal("break_glass without reason should fail")
	}
	if !strings.Contains(err.Error(), "--reason") {
		t.Errorf("error should mention '--reason', got: %v", err)
	}
}

// ── Hooks config validation ──────────────────────────────────────────

func TestValidateHooksConfig_CodexRejectsEvents(t *testing.T) {
	cfg := map[string]any{
		"agents": map[string]any{
			"codex": map[string]any{
				"target": "~/.codex/config.toml",
				"format": "codex_notify",
				"events": map[string]any{
					"SessionEnd": []any{map[string]any{"command": "test"}},
				},
			},
		},
	}
	err := validateHooksConfig(cfg)
	if err == nil {
		t.Fatal("codex_notify with events should fail")
	}
	if !strings.Contains(err.Error(), "codex_notify format must use 'notify'") {
		t.Errorf("error message mismatch: %v", err)
	}
}

func TestValidateHooksConfig_CodexRequiresNotify(t *testing.T) {
	cfg := map[string]any{
		"agents": map[string]any{
			"codex": map[string]any{
				"target": "~/.codex/config.toml",
				"format": "codex_notify",
			},
		},
	}
	err := validateHooksConfig(cfg)
	if err == nil {
		t.Fatal("codex_notify without notify should fail")
	}
	if !strings.Contains(err.Error(), "requires 'notify' list") {
		t.Errorf("error message mismatch: %v", err)
	}
}

func TestValidateHooksConfig_ClaudeRejectsNotify(t *testing.T) {
	cfg := map[string]any{
		"agents": map[string]any{
			"claude": map[string]any{
				"target": "~/.claude/settings.json",
				"format": "claude_hooks",
				"notify": []any{"some", "command"},
			},
		},
	}
	err := validateHooksConfig(cfg)
	if err == nil {
		t.Fatal("claude_hooks with notify should fail")
	}
	if !strings.Contains(err.Error(), "must use 'events', not 'notify'") {
		t.Errorf("error message mismatch: %v", err)
	}
}

func TestValidateHooksConfig_InvalidFormat(t *testing.T) {
	cfg := map[string]any{
		"agents": map[string]any{
			"test": map[string]any{
				"target": "~/.test",
				"format": "invalid_format",
			},
		},
	}
	err := validateHooksConfig(cfg)
	if err == nil {
		t.Fatal("invalid format should fail")
	}
	if !strings.Contains(err.Error(), "unknown hooks format") {
		t.Errorf("error message mismatch: %v", err)
	}
}

// ── Rules config validation ──────────────────────────────────────────

func TestValidateRulesConfig_MissingTarget(t *testing.T) {
	cfg := map[string]any{
		"agents": map[string]any{
			"test": map[string]any{
				"compose": []any{"a.md"},
			},
		},
	}
	err := validateRulesConfig(cfg)
	if err == nil {
		t.Fatal("missing target should fail")
	}
	if !strings.Contains(err.Error(), "missing 'target'") {
		t.Errorf("error message mismatch: %v", err)
	}
}

func TestValidateRulesConfig_MissingCompose(t *testing.T) {
	cfg := map[string]any{
		"agents": map[string]any{
			"test": map[string]any{
				"target": "~/test",
			},
		},
	}
	err := validateRulesConfig(cfg)
	if err == nil {
		t.Fatal("missing compose should fail")
	}
	if !strings.Contains(err.Error(), "missing 'compose'") {
		t.Errorf("error message mismatch: %v", err)
	}
}

// ── Legacy content.json detection ────────────────────────────────────

func TestContentPlan_DetectsLegacyContentJSON(t *testing.T) {
	configDir, _, _ := setupRulesEnv(t)

	// Create legacy content.json
	legacyDir := filepath.Join(configDir, "registry")
	os.MkdirAll(legacyDir, 0o755)
	writeText(t, filepath.Join(legacyDir, "content.json"), "{}")

	result, err := ContentPlan(configDir, PlanOpts{})
	if err != nil {
		t.Fatalf("ContentPlan failed: %v", err)
	}

	warnings, ok := result["warnings"].([]string)
	if !ok || len(warnings) == 0 {
		t.Fatal("should emit warnings for legacy content.json")
	}
	foundLegacy := false
	for _, w := range warnings {
		if strings.Contains(w, "legacy") {
			foundLegacy = true
			break
		}
	}
	if !foundLegacy {
		t.Errorf("warnings should mention 'legacy', got: %v", warnings)
	}
}

// ── shortHash ────────────────────────────────────────────────────────

func TestShortHash_Deterministic(t *testing.T) {
	h1 := shortHash("hello world")
	h2 := shortHash("hello world")
	if h1 != h2 {
		t.Errorf("shortHash should be deterministic: %q vs %q", h1, h2)
	}
	if len(h1) != 12 {
		t.Errorf("shortHash should be 12 chars, got %d", len(h1))
	}
}

func TestShortHash_DifferentInput(t *testing.T) {
	h1 := shortHash("hello")
	h2 := shortHash("world")
	if h1 == h2 {
		t.Error("different inputs should produce different hashes")
	}
}

// ── fileExists / readFileText ────────────────────────────────────────

func TestFileExists_ExistingFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.txt")
	os.WriteFile(path, []byte("test"), 0o644)
	if !fileExists(path) {
		t.Error("fileExists should return true for existing file")
	}
}

func TestFileExists_NonexistentFile(t *testing.T) {
	if fileExists("/nonexistent/path/file.txt") {
		t.Error("fileExists should return false for nonexistent file")
	}
}

func TestFileExists_Directory(t *testing.T) {
	tmp := t.TempDir()
	if fileExists(tmp) {
		t.Error("fileExists should return false for directory")
	}
}

func TestReadFileText_ExistingFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.txt")
	os.WriteFile(path, []byte("hello"), 0o644)
	result := readFileText(path)
	if result != "hello" {
		t.Errorf("readFileText = %q, want 'hello'", result)
	}
}

func TestReadFileText_NonexistentFile(t *testing.T) {
	result := readFileText("/nonexistent/path/file.txt")
	if result != "" {
		t.Errorf("nonexistent file should return empty, got: %q", result)
	}
}

// ── nilItems ─────────────────────────────────────────────────────────

func TestNilItems_ReturnsEmptySlice(t *testing.T) {
	result := nilItems(nil)
	if result == nil {
		t.Fatal("nilItems(nil) should return non-nil empty slice")
	}
	if len(result) != 0 {
		t.Errorf("nilItems(nil) should return empty slice, got len=%d", len(result))
	}
}

func TestNilItems_PassesThrough(t *testing.T) {
	items := []map[string]any{{"key": "val"}}
	result := nilItems(items)
	if len(result) != 1 || result[0]["key"] != "val" {
		t.Errorf("nilItems should pass through non-nil slice")
	}
}

// ── randomHex ────────────────────────────────────────────────────────

func TestRandomHex_CorrectLength(t *testing.T) {
	h := randomHex(8)
	if len(h) != 8 {
		t.Errorf("randomHex(8) should return 8 chars, got %d", len(h))
	}
}

func TestRandomHex_AllHexChars(t *testing.T) {
	h := randomHex(16)
	for _, c := range h {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("randomHex should only contain hex chars, got %c", c)
		}
	}
}

// ── nilIfEmpty ───────────────────────────────────────────────────────

func TestNilIfEmpty_ReturnsNilForEmpty(t *testing.T) {
	result := nilIfEmpty("")
	if result != nil {
		t.Errorf("nilIfEmpty('') should return nil, got %v", result)
	}
}

func TestNilIfEmpty_ReturnsValueForNonEmpty(t *testing.T) {
	result := nilIfEmpty("hello")
	if result != "hello" {
		t.Errorf("nilIfEmpty('hello') should return 'hello', got %v", result)
	}
}

// ── sortedMapKeys ────────────────────────────────────────────────────

func TestSortedMapKeys(t *testing.T) {
	m := map[string]any{"charlie": 1, "alpha": 2, "bravo": 3}
	keys := sortedMapKeys(m)
	if len(keys) != 3 || keys[0] != "alpha" || keys[1] != "bravo" || keys[2] != "charlie" {
		t.Errorf("sortedMapKeys = %v, want [alpha bravo charlie]", keys)
	}
}

func TestSortedMapKeys_Empty(t *testing.T) {
	m := map[string]any{}
	keys := sortedMapKeys(m)
	if len(keys) != 0 {
		t.Errorf("sortedMapKeys of empty map should be empty, got %v", keys)
	}
}
