package skills

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ── Sources config ──────────────────────────────────────────────────

// SourcesConfig holds the private skill registries configuration.
type SourcesConfig struct {
	Registries map[string]Registry `json:"registries"`
}

// Registry represents a private skill source (a git repo containing SKILL.md files).
type Registry struct {
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

// LoadSources reads skills/sources.json from configDir.
// Returns empty config if file doesn't exist.
func LoadSources(configDir string) (*SourcesConfig, error) {
	path := filepath.Join(configDir, "skills", "sources.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &SourcesConfig{Registries: map[string]Registry{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read sources.json: %w", err)
	}

	var cfg SourcesConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse sources.json: %w", err)
	}
	if cfg.Registries == nil {
		cfg.Registries = map[string]Registry{}
	}
	return &cfg, nil
}

// ── Search from source ──────────────────────────────────────────────

// SourceSearchResult represents a skill found in a private registry.
type SourceSearchResult struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	RelPath     string `json:"rel_path"`
	Registry    string `json:"registry"`
}

// SearchSource clones a registry repo and searches for skills matching query.
// Query matches against skill name and description (case-insensitive substring).
func SearchSource(registry Registry, registryName string, query string) ([]SourceSearchResult, error) {
	// Clone to temp dir
	tmpDir, err := os.MkdirTemp("", "agentctl-source-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	url := normalizeGitURL(registry.URL)
	if url == "" {
		return nil, fmt.Errorf("invalid registry URL: %q", registry.URL)
	}

	cloneDir := filepath.Join(tmpDir, "repo")
	cmd := exec.Command("git", "clone", "--depth", "1", "--quiet", url, cloneDir)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git clone %s: %w", url, err)
	}

	// Find all skills
	candidates := findSkillDirs(cloneDir)

	// Filter by query
	q := strings.ToLower(query)
	var results []SourceSearchResult
	for _, c := range candidates {
		meta, _ := parseSkillMDInDir(c.dir)
		desc := ""
		if meta != nil {
			desc = meta.Description
		}

		if q == "" ||
			strings.Contains(strings.ToLower(c.name), q) ||
			strings.Contains(strings.ToLower(desc), q) {
			results = append(results, SourceSearchResult{
				Name:        c.name,
				Description: desc,
				RelPath:     c.relPath,
				Registry:    registryName,
			})
		}
	}

	return results, nil
}
