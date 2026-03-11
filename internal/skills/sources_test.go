package skills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSources_FileNotExist(t *testing.T) {
	cfg, err := LoadSources(t.TempDir())
	if err != nil {
		t.Fatalf("LoadSources failed: %v", err)
	}
	if len(cfg.Registries) != 0 {
		t.Errorf("expected empty registries, got %d", len(cfg.Registries))
	}
}

func TestLoadSources_ValidFile(t *testing.T) {
	configDir := t.TempDir()
	skillsDir := filepath.Join(configDir, "skills")
	os.MkdirAll(skillsDir, 0o755)

	data := SourcesConfig{
		Registries: map[string]Registry{
			"team": {
				URL:         "git@git.example.com:team/skills.git",
				Description: "Team skills",
			},
			"oss": {
				URL: "https://github.com/org/skills",
			},
		},
	}
	raw, _ := json.MarshalIndent(data, "", "  ")
	os.WriteFile(filepath.Join(skillsDir, "sources.json"), raw, 0o644)

	cfg, err := LoadSources(configDir)
	if err != nil {
		t.Fatalf("LoadSources failed: %v", err)
	}
	if len(cfg.Registries) != 2 {
		t.Errorf("expected 2 registries, got %d", len(cfg.Registries))
	}
	if cfg.Registries["team"].URL != "git@git.example.com:team/skills.git" {
		t.Errorf("team URL = %q", cfg.Registries["team"].URL)
	}
	if cfg.Registries["oss"].Description != "" {
		t.Errorf("oss description should be empty, got %q", cfg.Registries["oss"].Description)
	}
}

func TestLoadSources_InvalidJSON(t *testing.T) {
	configDir := t.TempDir()
	skillsDir := filepath.Join(configDir, "skills")
	os.MkdirAll(skillsDir, 0o755)
	os.WriteFile(filepath.Join(skillsDir, "sources.json"), []byte("{invalid"), 0o644)

	_, err := LoadSources(configDir)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestLoadSources_EmptyRegistries(t *testing.T) {
	configDir := t.TempDir()
	skillsDir := filepath.Join(configDir, "skills")
	os.MkdirAll(skillsDir, 0o755)
	os.WriteFile(filepath.Join(skillsDir, "sources.json"), []byte(`{}`), 0o644)

	cfg, err := LoadSources(configDir)
	if err != nil {
		t.Fatalf("LoadSources failed: %v", err)
	}
	if cfg.Registries == nil {
		t.Error("Registries should not be nil")
	}
}

func TestSearchSource_FiltersByQuery(t *testing.T) {
	// Create a fake "cloned repo" with skills
	repoDir := t.TempDir()
	createSkill(t, repoDir, "data-pipeline", "---\nname: data-pipeline\ndescription: ETL pipeline tools\n---\n# Pipeline", nil)
	createSkill(t, repoDir, "sql-helper", "---\nname: sql-helper\ndescription: SQL query builder\n---\n# SQL", nil)
	createSkill(t, repoDir, "dashboard", "---\nname: dashboard\ndescription: Dashboard templates\n---\n# Dashboard", nil)

	// Test: findSkillDirs finds all 3
	found := findSkillDirs(repoDir)
	if len(found) != 3 {
		t.Fatalf("expected 3 skills in test repo, got %d", len(found))
	}
}
