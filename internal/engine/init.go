package engine

import (
	"fmt"
	"os"
	"path/filepath"

	"agentctl/internal/tx"
)

// InitConfig holds parameters for the Init operation.
type InitConfig struct {
	ConfigDir      string
	SelectedAgents []string
	Force          bool
	DryRun         bool
}

// InitResult holds the output of Init.
type InitResult struct {
	ConfigDir      string   `json:"config_dir"`
	CreatedFiles   []string `json:"created_files"`
	CreatedDirs    []string `json:"created_dirs"`
	SelectedAgents []string `json:"selected_agents"`
}

// rulesTarget maps agent name to the global rules target file path.
var rulesTarget = map[string]string{
	"claude-code": "~/.claude/CLAUDE.md",
	"codex":       "~/.codex/instructions.md",
	"gemini-cli":  "~/.gemini/GEMINI.md",
	"opencode":    "~/.config/opencode/AGENTS.md",
}

// commandsTarget maps agent name to the commands directory + format.
type commandsSpec struct {
	TargetDir string
	Format    string
}

var commandsTargets = map[string]commandsSpec{
	"claude-code": {TargetDir: "~/.claude/commands", Format: "md"},
	"gemini-cli":  {TargetDir: "~/.gemini/commands", Format: "toml"},
	"opencode":    {TargetDir: "~/.config/opencode/commands", Format: "md"},
}

// Init creates the initial agentctl configuration directory structure and
// generates template configuration files for the selected agents.
func Init(cfg InitConfig) (*InitResult, error) {
	if cfg.ConfigDir == "" {
		return nil, fmt.Errorf("config_dir is required")
	}

	// Check if already initialized (mcp/servers.json exists)
	serversPath := filepath.Join(cfg.ConfigDir, "mcp", "servers.json")
	if _, err := os.Stat(serversPath); err == nil && !cfg.Force {
		return nil, fmt.Errorf("config already exists at %s (use --force to overwrite)", cfg.ConfigDir)
	}

	now := tx.UTCNowISO()

	result := &InitResult{
		ConfigDir:      cfg.ConfigDir,
		SelectedAgents: cfg.SelectedAgents,
	}

	// Define directory structure
	dirs := []string{
		filepath.Join(cfg.ConfigDir, "mcp"),
		filepath.Join(cfg.ConfigDir, "rules"),
		filepath.Join(cfg.ConfigDir, "hooks"),
		filepath.Join(cfg.ConfigDir, "commands"),
		filepath.Join(cfg.ConfigDir, "agents"),
		filepath.Join(cfg.ConfigDir, "skills"),
		filepath.Join(cfg.ConfigDir, "secrets"),
		filepath.Join(cfg.ConfigDir, "state", "runs"),
		filepath.Join(cfg.ConfigDir, "state", "snapshots"),
		filepath.Join(cfg.ConfigDir, "state", "locks"),
	}

	// Build file templates
	files := make(map[string]map[string]any)

	// mcp/servers.json
	files[filepath.Join(cfg.ConfigDir, "mcp", "servers.json")] = map[string]any{
		"schema_version": "1.0.0",
		"generated_at":   now,
		"managed_by":     "agentctl",
		"servers":        map[string]any{},
	}

	// mcp/profiles.json
	profilesAgents := make(map[string]any)
	for _, agent := range cfg.SelectedAgents {
		profilesAgents[agent] = map[string]any{
			"servers":          []any{},
			"overrides":        map[string]any{},
			"post_apply_hooks": []any{},
		}
	}
	files[filepath.Join(cfg.ConfigDir, "mcp", "profiles.json")] = map[string]any{
		"schema_version": "1.0.0",
		"generated_at":   now,
		"managed_by":     "agentctl",
		"agents":         profilesAgents,
		"servers":        map[string]any{},
	}

	// mcp/compat.json
	files[filepath.Join(cfg.ConfigDir, "mcp", "compat.json")] = map[string]any{
		"schema_version":       "1.0.0",
		"generated_at":         now,
		"managed_by":           "agentctl",
		"legacy_enabled":       false,
		"profilesPerAgent_map": map[string]any{},
		"disabled_map":         map[string]any{},
	}

	// rules/config.json
	rulesAgents := make(map[string]any)
	for _, agent := range cfg.SelectedAgents {
		if target, ok := rulesTarget[agent]; ok {
			rulesAgents[agent] = map[string]any{
				"target":    target,
				"compose":   []any{"shared.md"},
				"separator": "\n\n",
			}
		}
	}
	files[filepath.Join(cfg.ConfigDir, "rules", "config.json")] = map[string]any{
		"schema_version": "1.0.0",
		"generated_at":   now,
		"managed_by":     "agentctl",
		"agents":         rulesAgents,
	}

	// hooks/config.json
	files[filepath.Join(cfg.ConfigDir, "hooks", "config.json")] = map[string]any{
		"schema_version": "1.0.0",
		"generated_at":   now,
		"managed_by":     "agentctl",
		"agents":         map[string]any{},
	}

	// commands/config.json
	commandsAgents := make(map[string]any)
	for _, agent := range cfg.SelectedAgents {
		if spec, ok := commandsTargets[agent]; ok {
			commandsAgents[agent] = map[string]any{
				"target_dir": spec.TargetDir,
				"format":     spec.Format,
			}
		}
	}
	files[filepath.Join(cfg.ConfigDir, "commands", "config.json")] = map[string]any{
		"schema_version": "1.0.0",
		"generated_at":   now,
		"managed_by":     "agentctl",
		"agents":         commandsAgents,
	}

	// skills/config.json
	files[filepath.Join(cfg.ConfigDir, "skills", "config.json")] = map[string]any{
		"schema_version": "1.0.0",
		"generated_at":   now,
		"managed_by":     "agentctl",
		"agents":         map[string]any{},
	}

	// Text file: rules/shared.md
	sharedMdPath := filepath.Join(cfg.ConfigDir, "rules", "shared.md")

	if cfg.DryRun {
		// Collect what would be created without writing
		result.CreatedDirs = dirs
		for path := range files {
			result.CreatedFiles = append(result.CreatedFiles, path)
		}
		result.CreatedFiles = append(result.CreatedFiles, sharedMdPath)
		return result, nil
	}

	// Create directories
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create directory %s: %w", dir, err)
		}
		result.CreatedDirs = append(result.CreatedDirs, dir)
	}

	// Write JSON files
	for path, data := range files {
		if err := tx.WriteJSONAtomic(path, data); err != nil {
			return nil, fmt.Errorf("write %s: %w", path, err)
		}
		result.CreatedFiles = append(result.CreatedFiles, path)
	}

	// Write rules/shared.md placeholder
	if err := tx.WriteTextAtomic(sharedMdPath, "# Shared Rules\n\n<!-- Add your shared rules here -->\n"); err != nil {
		return nil, fmt.Errorf("write %s: %w", sharedMdPath, err)
	}
	result.CreatedFiles = append(result.CreatedFiles, sharedMdPath)

	return result, nil
}
