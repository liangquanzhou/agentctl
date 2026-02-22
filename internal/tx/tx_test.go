package tx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── ExpandUser ───────────────────────────────────────────────────────

func TestExpandUser_Tilde(t *testing.T) {
	result := ExpandUser("~/some/file.md")
	home := HomeDir()
	want := filepath.Join(home, "some", "file.md")
	if result != want {
		t.Errorf("ExpandUser(~/some/file.md) = %q, want %q", result, want)
	}
}

func TestExpandUser_NoTilde(t *testing.T) {
	result := ExpandUser("/absolute/path")
	if result != "/absolute/path" {
		t.Errorf("ExpandUser(/absolute/path) = %q, want /absolute/path", result)
	}
}

func TestExpandUser_Empty(t *testing.T) {
	result := ExpandUser("")
	if result != "" {
		t.Errorf("ExpandUser('') = %q, want empty", result)
	}
}

// ── RejectSymlink ────────────────────────────────────────────────────

func TestRejectSymlink_RegularFile(t *testing.T) {
	tmp := t.TempDir()
	regular := filepath.Join(tmp, "regular.txt")
	os.WriteFile(regular, []byte("hello"), 0o644)

	if err := RejectSymlink(regular); err != nil {
		t.Errorf("RejectSymlink on regular file should pass, got: %v", err)
	}
}

func TestRejectSymlink_Symlink(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "target.txt")
	os.WriteFile(target, []byte("hello"), 0o644)
	link := filepath.Join(tmp, "link.txt")
	os.Symlink(target, link)

	err := RejectSymlink(link)
	if err == nil {
		t.Fatal("RejectSymlink on symlink should fail")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error should mention symlink, got: %v", err)
	}
}

func TestRejectSymlink_NonexistentPath(t *testing.T) {
	if err := RejectSymlink("/nonexistent/path/no-exist"); err != nil {
		t.Errorf("RejectSymlink on nonexistent path should pass, got: %v", err)
	}
}

// ── ParseEnvFile ─────────────────────────────────────────────────────

func TestParseEnvFile_BasicKV(t *testing.T) {
	tmp := t.TempDir()
	envFile := filepath.Join(tmp, "test.env")
	os.WriteFile(envFile, []byte("KEY1=value1\nKEY2=value2\n"), 0o644)

	result := ParseEnvFile(envFile)
	if result["KEY1"] != "value1" {
		t.Errorf("KEY1 = %q, want 'value1'", result["KEY1"])
	}
	if result["KEY2"] != "value2" {
		t.Errorf("KEY2 = %q, want 'value2'", result["KEY2"])
	}
}

func TestParseEnvFile_SkipsCommentsAndBlanks(t *testing.T) {
	tmp := t.TempDir()
	envFile := filepath.Join(tmp, "test.env")
	content := "# this is a comment\n\nKEY=val\n  \n# another comment\n"
	os.WriteFile(envFile, []byte(content), 0o644)

	result := ParseEnvFile(envFile)
	if len(result) != 1 {
		t.Errorf("expected 1 entry, got %d: %v", len(result), result)
	}
	if result["KEY"] != "val" {
		t.Errorf("KEY = %q, want 'val'", result["KEY"])
	}
}

func TestParseEnvFile_SupportsExportPrefix(t *testing.T) {
	tmp := t.TempDir()
	envFile := filepath.Join(tmp, "test.env")
	os.WriteFile(envFile, []byte("export MY_VAR=hello\n"), 0o644)

	result := ParseEnvFile(envFile)
	if result["MY_VAR"] != "hello" {
		t.Errorf("MY_VAR = %q, want 'hello'", result["MY_VAR"])
	}
}

func TestParseEnvFile_StripsQuotes(t *testing.T) {
	tmp := t.TempDir()
	envFile := filepath.Join(tmp, "test.env")
	content := `DOUBLE="double quoted"
SINGLE='single quoted'
NONE=unquoted
`
	os.WriteFile(envFile, []byte(content), 0o644)

	result := ParseEnvFile(envFile)
	if result["DOUBLE"] != "double quoted" {
		t.Errorf("DOUBLE = %q, want 'double quoted'", result["DOUBLE"])
	}
	if result["SINGLE"] != "single quoted" {
		t.Errorf("SINGLE = %q, want 'single quoted'", result["SINGLE"])
	}
	if result["NONE"] != "unquoted" {
		t.Errorf("NONE = %q, want 'unquoted'", result["NONE"])
	}
}

func TestParseEnvFile_NonexistentFile(t *testing.T) {
	result := ParseEnvFile("/nonexistent/file.env")
	if result != nil {
		t.Errorf("nonexistent file should return nil, got: %v", result)
	}
}

// ── GetStringSlice ───────────────────────────────────────────────────

func TestGetStringSlice_ValidSlice(t *testing.T) {
	m := map[string]any{
		"items": []any{"a", "b", "c"},
	}
	result := GetStringSlice(m, "items")
	if len(result) != 3 || result[0] != "a" || result[1] != "b" || result[2] != "c" {
		t.Errorf("GetStringSlice = %v, want [a b c]", result)
	}
}

func TestGetStringSlice_MissingKey(t *testing.T) {
	m := map[string]any{}
	result := GetStringSlice(m, "missing")
	if result != nil {
		t.Errorf("missing key should return nil, got: %v", result)
	}
}

func TestGetStringSlice_NonArrayValue(t *testing.T) {
	m := map[string]any{"items": "not-an-array"}
	result := GetStringSlice(m, "items")
	if result != nil {
		t.Errorf("non-array value should return nil, got: %v", result)
	}
}

// ── GetMap ───────────────────────────────────────────────────────────

func TestGetMap_ValidMap(t *testing.T) {
	m := map[string]any{
		"nested": map[string]any{"key": "val"},
	}
	result := GetMap(m, "nested")
	if result == nil || result["key"] != "val" {
		t.Errorf("GetMap = %v, want {key: val}", result)
	}
}

func TestGetMap_MissingKey(t *testing.T) {
	m := map[string]any{}
	result := GetMap(m, "missing")
	if result != nil {
		t.Errorf("missing key should return nil, got: %v", result)
	}
}

func TestGetMap_NonMapValue(t *testing.T) {
	m := map[string]any{"nested": "not-a-map"}
	result := GetMap(m, "nested")
	if result != nil {
		t.Errorf("non-map value should return nil, got: %v", result)
	}
}

// ── GetString ────────────────────────────────────────────────────────

func TestGetString_Present(t *testing.T) {
	m := map[string]any{"name": "alice"}
	result := GetString(m, "name", "default")
	if result != "alice" {
		t.Errorf("GetString = %q, want 'alice'", result)
	}
}

func TestGetString_MissingReturnsDefault(t *testing.T) {
	m := map[string]any{}
	result := GetString(m, "name", "default")
	if result != "default" {
		t.Errorf("GetString = %q, want 'default'", result)
	}
}

func TestGetString_NonStringReturnsDefault(t *testing.T) {
	m := map[string]any{"name": 42}
	result := GetString(m, "name", "default")
	if result != "default" {
		t.Errorf("GetString with non-string should return default, got %q", result)
	}
}

// ── GetBool ──────────────────────────────────────────────────────────

func TestGetBool_Present(t *testing.T) {
	m := map[string]any{"enabled": true}
	if !GetBool(m, "enabled", false) {
		t.Error("GetBool should return true")
	}
}

func TestGetBool_MissingReturnsDefault(t *testing.T) {
	m := map[string]any{}
	if GetBool(m, "enabled", true) != true {
		t.Error("GetBool missing key should return default true")
	}
	if GetBool(m, "enabled", false) != false {
		t.Error("GetBool missing key should return default false")
	}
}

func TestGetBool_NonBoolReturnsDefault(t *testing.T) {
	m := map[string]any{"enabled": "yes"}
	if GetBool(m, "enabled", false) != false {
		t.Error("GetBool with non-bool should return default false")
	}
}

// ── IsUnderHome ──────────────────────────────────────────────────────

func TestIsUnderHome_ValidSubdir(t *testing.T) {
	home := HomeDir()
	path := filepath.Join(home, "subdir", "file.txt")
	if err := IsUnderHome(path); err != nil {
		t.Errorf("path under home should pass: %v", err)
	}
}

func TestIsUnderHome_RejectsEscape(t *testing.T) {
	err := IsUnderHome("/etc/passwd")
	if err == nil {
		t.Error("path outside home should fail")
	}
	if !strings.Contains(err.Error(), "escapes home") {
		t.Errorf("error should mention 'escapes home', got: %v", err)
	}
}

// ── Normalize ────────────────────────────────────────────────────────

func TestNormalize_IdenticalInput(t *testing.T) {
	a := Normalize(map[string]any{"b": 2, "a": 1})
	b := Normalize(map[string]any{"a": 1, "b": 2})
	if a != b {
		t.Errorf("Normalize should produce identical output for same data: %q vs %q", a, b)
	}
}

func TestNormalize_DifferentInput(t *testing.T) {
	a := Normalize(map[string]any{"x": 1})
	b := Normalize(map[string]any{"x": 2})
	if a == b {
		t.Error("Normalize should produce different output for different data")
	}
}

// ── SHA256Bytes ──────────────────────────────────────────────────────

func TestSHA256Bytes_Deterministic(t *testing.T) {
	h1 := SHA256Bytes([]byte("hello world"))
	h2 := SHA256Bytes([]byte("hello world"))
	if h1 != h2 {
		t.Errorf("SHA256Bytes should be deterministic: %q vs %q", h1, h2)
	}
	if len(h1) != 64 {
		t.Errorf("SHA256 hex should be 64 chars, got %d", len(h1))
	}
}

func TestSHA256Bytes_DifferentInput(t *testing.T) {
	h1 := SHA256Bytes([]byte("hello"))
	h2 := SHA256Bytes([]byte("world"))
	if h1 == h2 {
		t.Error("different inputs should produce different hashes")
	}
}

// ── ReadJSON ─────────────────────────────────────────────────────────

func TestReadJSON_Valid(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.json")
	os.WriteFile(path, []byte(`{"key": "val"}`), 0o644)

	data, err := ReadJSON(path)
	if err != nil {
		t.Fatalf("ReadJSON failed: %v", err)
	}
	if data["key"] != "val" {
		t.Errorf("ReadJSON key = %v, want 'val'", data["key"])
	}
}

func TestReadJSON_InvalidJSON(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "bad.json")
	os.WriteFile(path, []byte(`{broken json`), 0o644)

	_, err := ReadJSON(path)
	if err == nil {
		t.Fatal("ReadJSON should fail on invalid JSON")
	}
}

func TestReadJSON_ArrayRoot(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "arr.json")
	os.WriteFile(path, []byte(`[]`), 0o644)

	_, err := ReadJSON(path)
	if err == nil {
		t.Fatal("ReadJSON should fail on array root (expected object)")
	}
}

func TestReadJSON_Nonexistent(t *testing.T) {
	_, err := ReadJSON("/nonexistent/file.json")
	if err == nil {
		t.Fatal("ReadJSON should fail on nonexistent file")
	}
}

// ── WriteJSONAtomic / ReadJSON roundtrip ─────────────────────────────

func TestWriteJSONAtomic_Roundtrip(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "sub", "data.json")

	original := map[string]any{
		"name": "test",
		"count": float64(42),
	}

	if err := WriteJSONAtomic(path, original); err != nil {
		t.Fatalf("WriteJSONAtomic failed: %v", err)
	}

	loaded, err := ReadJSON(path)
	if err != nil {
		t.Fatalf("ReadJSON failed: %v", err)
	}
	if loaded["name"] != "test" {
		t.Errorf("name = %v, want 'test'", loaded["name"])
	}
	if loaded["count"] != float64(42) {
		t.Errorf("count = %v, want 42", loaded["count"])
	}
}

// ── WriteTextAtomic ──────────────────────────────────────────────────

func TestWriteTextAtomic_CreatesParent(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "nested", "deep", "file.txt")

	if err := WriteTextAtomic(path, "hello world"); err != nil {
		t.Fatalf("WriteTextAtomic failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back failed: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("content = %q, want 'hello world'", string(data))
	}
}

// ── SnapshotKey ──────────────────────────────────────────────────────

func TestSnapshotKey_Deterministic(t *testing.T) {
	k1 := SnapshotKey("/some/path/file.json")
	k2 := SnapshotKey("/some/path/file.json")
	if k1 != k2 {
		t.Errorf("SnapshotKey should be deterministic: %q vs %q", k1, k2)
	}
	if len(k1) != 12 {
		t.Errorf("SnapshotKey should be 12 chars, got %d", len(k1))
	}
}

func TestSnapshotKey_DifferentPaths(t *testing.T) {
	k1 := SnapshotKey("/path/a.json")
	k2 := SnapshotKey("/path/b.json")
	if k1 == k2 {
		t.Error("different paths should produce different keys")
	}
}
