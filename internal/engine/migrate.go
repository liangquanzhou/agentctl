package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"agentctl/internal/tx"
)

// ── Migration ───────────────────────────────────────────────────────

// InferRuntime guesses the runtime from a server command string.
func InferRuntime(command string) string {
	c := strings.ToLower(command)
	if strings.Contains(c, "homebrew") || strings.Contains(c, "/opt/homebrew") {
		return "brew"
	}
	if strings.Contains(c, "uv") || strings.Contains(c, ".local/bin") {
		return "uv"
	}
	if strings.Contains(c, "bun") || strings.Contains(c, "node") || strings.Contains(c, "npx") {
		return "bun"
	}
	return "custom"
}

// MigrateInitResult holds the output of migrate_init.
type MigrateInitResult struct {
	DryRun    bool   `json:"dry_run"`
	ConfigDir string `json:"config_dir"`
	Servers   int    `json:"servers"`
	Agents    int    `json:"agents"`
}

// MigrateInit migrates from a legacy single-file config to the split registry format.
func MigrateInit(sourceConfig, configDir string, dryRun bool) (*MigrateInitResult, error) {
	source, err := tx.ReadJSON(sourceConfig)
	if err != nil {
		return nil, fmt.Errorf("read source config: %w", err)
	}

	now := tx.UTCNowISO()

	sourceMCPServers := tx.GetMap(source, "mcpServers")
	if sourceMCPServers == nil {
		sourceMCPServers = map[string]any{}
	}
	sourceProfiles := tx.GetMap(source, "profilesPerAgent")
	if sourceProfiles == nil {
		sourceProfiles = map[string]any{}
	}
	sourceDisabled := tx.GetMap(source, "disabled")
	if sourceDisabled == nil {
		sourceDisabled = map[string]any{}
	}

	// Build servers.json
	serversMap := make(map[string]any)
	for name, cfg := range sourceMCPServers {
		cfgMap, ok := cfg.(map[string]any)
		if !ok {
			continue
		}
		cmd := tx.GetString(cfgMap, "command", "")
		args := getArgsSlice(cfgMap)

		// Extract env keys sorted
		envMap := tx.GetMap(cfgMap, "env")
		var envRef []any
		if envMap != nil {
			keys := make([]string, 0, len(envMap))
			for k := range envMap {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			envRef = make([]any, len(keys))
			for i, k := range keys {
				envRef[i] = k
			}
		} else {
			envRef = []any{}
		}

		serversMap[name] = map[string]any{
			"runtime":      InferRuntime(cmd),
			"command":      cmd,
			"args":         args,
			"envRef":       envRef,
			"install":      map[string]any{},
			"healthcheck":  map[string]any{"type": "skip"},
			"capabilities": []any{},
		}
	}

	serversJSON := map[string]any{
		"schema_version": "1.0.0",
		"generated_at":   now,
		"managed_by":     "agentctl",
		"servers":        serversMap,
	}

	// Build profiles.json
	profilesAgents := make(map[string]any)
	for agent, servers := range sourceProfiles {
		if serverList, ok := servers.([]any); ok {
			profilesAgents[agent] = map[string]any{
				"servers":          serverList,
				"overrides":        map[string]any{},
				"post_apply_hooks": []any{},
			}
		}
	}

	profilesJSON := map[string]any{
		"schema_version": "1.0.0",
		"generated_at":   now,
		"managed_by":     "agentctl",
		"agents":         profilesAgents,
		"servers":        map[string]any{},
	}

	// Build compat.json
	compatJSON := map[string]any{
		"schema_version":       "1.0.0",
		"generated_at":         now,
		"managed_by":           "agentctl",
		"legacy_enabled":       true,
		"profilesPerAgent_map": sourceProfiles,
		"disabled_map":         sourceDisabled,
	}

	mcpFiles := map[string]map[string]any{
		"servers.json":  serversJSON,
		"profiles.json": profilesJSON,
		"compat.json":   compatJSON,
	}

	if !dryRun {
		mcpDir := filepath.Join(configDir, "mcp")
		if err := os.MkdirAll(mcpDir, 0o755); err != nil {
			return nil, fmt.Errorf("create mcp dir: %w", err)
		}
		for filename, payload := range mcpFiles {
			if err := tx.WriteJSONAtomic(filepath.Join(mcpDir, filename), payload); err != nil {
				return nil, fmt.Errorf("write %s: %w", filename, err)
			}
		}
	}

	return &MigrateInitResult{
		DryRun:    dryRun,
		ConfigDir: configDir,
		Servers:   len(serversMap),
		Agents:    len(profilesAgents),
	}, nil
}

// MigrateFinalizeResult holds the output of migrate_finalize_legacy.
type MigrateFinalizeResult struct {
	DryRun              bool           `json:"dry_run"`
	LegacyEnabled       bool           `json:"legacy_enabled"`
	ProfilesPerAgentMap map[string]any `json:"profilesPerAgent_map"`
	DisabledMap         map[string]any `json:"disabled_map"`
}

// MigrateFinalizeLegacy disables legacy compat mode and clears the compat maps.
func MigrateFinalizeLegacy(configDir string, dryRun bool) (*MigrateFinalizeResult, error) {
	compatPath := filepath.Join(configDir, "mcp", "compat.json")
	compat, err := tx.ReadJSON(compatPath)
	if err != nil {
		return nil, fmt.Errorf("read compat.json: %w", err)
	}

	newCompat := shallowCopyMap(compat)
	newCompat["legacy_enabled"] = false
	newCompat["profilesPerAgent_map"] = map[string]any{}
	newCompat["disabled_map"] = map[string]any{}
	newCompat["generated_at"] = tx.UTCNowISO()

	if !dryRun {
		if err := tx.WriteJSONAtomic(compatPath, newCompat); err != nil {
			return nil, fmt.Errorf("write compat.json: %w", err)
		}
	}

	return &MigrateFinalizeResult{
		DryRun:              dryRun,
		LegacyEnabled:       false,
		ProfilesPerAgentMap: map[string]any{},
		DisabledMap:         map[string]any{},
	}, nil
}
