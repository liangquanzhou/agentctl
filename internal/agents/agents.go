// Package agents provides the agent registry with builtin definitions and TOML override support.
package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"agentctl/internal/tx"
)

// AgentDefinition describes one agent's configuration layout.
type AgentDefinition struct {
	Name           string
	Aliases        []string
	DisplayOrder   int
	MCPFileType    string // claude_json | json | codex_toml | opencode_json | openclaw_json
	MCPPath        string
	MCPInjectKey   string
	MCPManagedKeys []string
	HooksFormat    string // claude_hooks | gemini_hooks | codex_notify | "" (none)
	SkillsTarget   string // empty string means no skills
	BinaryNames    []string // executable names for which-style detection
}

// AgentSpec is an engine-compatible agent specification.
type AgentSpec struct {
	Key      string
	FileType string
	Path     string
}

// ── Built-in defaults ─────────────────────────────────────────────────

func home() string { return tx.HomeDir() }

func builtinAgents() map[string]AgentDefinition {
	h := home()
	return map[string]AgentDefinition{
		"claude-code": {
			Name:           "claude-code",
			Aliases:        []string{"claude"},
			DisplayOrder:   1,
			MCPFileType:    "claude_json",
			MCPPath:        filepath.Join(h, ".claude.json"),
			MCPInjectKey:   "mcpServers",
			MCPManagedKeys: []string{"mcpServers", "projects.*.mcpServers"},
			HooksFormat:    "claude_hooks",
			SkillsTarget:   filepath.Join(h, ".claude", "skills"),
			BinaryNames:    []string{"claude"},
		},
		"codex": {
			Name:           "codex",
			Aliases:        []string{},
			DisplayOrder:   2,
			MCPFileType:    "codex_toml",
			MCPPath:        filepath.Join(h, ".codex", "config.toml"),
			MCPInjectKey:   "mcp_servers",
			MCPManagedKeys: []string{"mcp_servers"},
			HooksFormat:    "codex_notify",
			SkillsTarget:   filepath.Join(h, ".codex", "skills"),
			BinaryNames:    []string{"codex"},
		},
		"gemini-cli": {
			Name:           "gemini-cli",
			Aliases:        []string{"gemini"},
			DisplayOrder:   3,
			MCPFileType:    "json",
			MCPPath:        filepath.Join(h, ".gemini", "settings.json"),
			MCPInjectKey:   "mcpServers",
			MCPManagedKeys: []string{"mcpServers"},
			HooksFormat:    "gemini_hooks",
			SkillsTarget:   filepath.Join(h, ".gemini", "skills"),
			BinaryNames:    []string{"gemini"},
		},
		"antigravity": {
			Name:           "antigravity",
			Aliases:        []string{},
			DisplayOrder:   4,
			MCPFileType:    "json",
			MCPPath:        filepath.Join(h, ".gemini", "antigravity", "mcp_config.json"),
			MCPInjectKey:   "mcpServers",
			MCPManagedKeys: []string{"mcpServers"},
			HooksFormat:    "",
			SkillsTarget:   filepath.Join(h, ".gemini", "antigravity", "skills"),
			BinaryNames:    []string{"antigravity"},
		},
		"opencode": {
			Name:           "opencode",
			Aliases:        []string{},
			DisplayOrder:   5,
			MCPFileType:    "opencode_json",
			MCPPath:        filepath.Join(h, ".config", "opencode", "opencode.json"),
			MCPInjectKey:   "mcp",
			MCPManagedKeys: []string{"mcp"},
			HooksFormat:    "",
			SkillsTarget:   filepath.Join(h, ".config", "opencode", "skills"),
			BinaryNames:    []string{"opencode"},
		},
		"openclaw": {
			Name:           "openclaw",
			Aliases:        []string{},
			DisplayOrder:   6,
			MCPFileType:    "openclaw_json",
			MCPPath:        filepath.Join(h, ".openclaw", "openclaw.json"),
			MCPInjectKey:   "mcp.servers",
			MCPManagedKeys: []string{"mcp.servers"},
			HooksFormat:    "",
			SkillsTarget:   filepath.Join(h, ".openclaw", "skills"),
			BinaryNames:    []string{"openclaw"},
		},
		"trae-cn": {
			Name:           "trae-cn",
			Aliases:        []string{},
			DisplayOrder:   7,
			MCPFileType:    "json",
			MCPPath:        filepath.Join(h, "Library", "Application Support", "Trae CN", "User", "mcp.json"),
			MCPInjectKey:   "mcpServers",
			MCPManagedKeys: []string{"mcpServers"},
			HooksFormat:    "",
			SkillsTarget:   filepath.Join(h, ".trae-cn", "skills"),
			BinaryNames:    []string{},
		},
	}
}

// DefaultAgentsDir returns the default TOML agents directory.
func DefaultAgentsDir() string {
	return filepath.Join(home(), ".config", "agentctl", "agents")
}

// parseTOML parses a single agent TOML file into an AgentDefinition.
func parseTOML(path string) (AgentDefinition, error) {
	data, err := tx.ReadTOML(path)
	if err != nil {
		return AgentDefinition{}, err
	}

	agent := tx.GetMap(data, "agent")
	mcp := tx.GetMap(data, "mcp")
	hooks := tx.GetMap(data, "hooks")
	skills := tx.GetMap(data, "skills")

	name := tx.GetString(agent, "name", "")
	if name == "" {
		return AgentDefinition{}, &MissingFieldError{Field: "agent.name", Path: path}
	}

	mcpPath := tx.GetString(mcp, "path", "")
	if mcpPath == "" {
		return AgentDefinition{}, &MissingFieldError{Field: "mcp.path", Path: path}
	}

	displayOrder := 99
	if v, ok := agent["display_order"]; ok {
		switch n := v.(type) {
		case int64:
			displayOrder = int(n)
		case float64:
			displayOrder = int(n)
		}
	}

	var aliases []string
	if v, ok := agent["aliases"]; ok {
		if arr, ok := v.([]any); ok {
			for _, item := range arr {
				if s, ok := item.(string); ok {
					aliases = append(aliases, s)
				}
			}
		}
	}

	skillsDir := tx.GetString(skills, "target_dir", "")

	expandedMCPPath := tx.ExpandUser(mcpPath)
	if err := tx.IsUnderHome(expandedMCPPath); err != nil {
		return AgentDefinition{}, fmt.Errorf("agent %q: mcp.path %w", name, err)
	}

	var expandedSkillsTarget string
	if skillsDir != "" {
		expandedSkillsTarget = tx.ExpandUser(skillsDir)
		if err := tx.IsUnderHome(expandedSkillsTarget); err != nil {
			return AgentDefinition{}, fmt.Errorf("agent %q: skills.target_dir %w", name, err)
		}
	}

	return AgentDefinition{
		Name:           name,
		Aliases:        aliases,
		DisplayOrder:   displayOrder,
		MCPFileType:    tx.GetString(mcp, "file_type", "json"),
		MCPPath:        expandedMCPPath,
		MCPInjectKey:   tx.GetString(mcp, "inject_key", "mcpServers"),
		MCPManagedKeys: tx.GetStringSlice(mcp, "managed_keys"),
		HooksFormat:    tx.GetString(hooks, "format", ""),
		SkillsTarget:   expandedSkillsTarget,
	}, nil
}

// MissingFieldError indicates a required TOML field is missing.
type MissingFieldError struct {
	Field string
	Path  string
}

func (e *MissingFieldError) Error() string {
	return e.Field + " is required in " + e.Path
}

// LoadAgentRegistry loads the agent registry: TOML files override builtins.
func LoadAgentRegistry(agentsDir string) map[string]AgentDefinition {
	if agentsDir == "" {
		agentsDir = DefaultAgentsDir()
	}

	registry := builtinAgents()

	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return registry // dir doesn't exist or unreadable — use builtins
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".toml") {
			continue
		}
		tomlPath := filepath.Join(agentsDir, entry.Name())
		defn, err := parseTOML(tomlPath)
		if err != nil {
			continue // skip malformed files, keep builtin
		}
		registry[defn.Name] = defn
	}

	return registry
}

// ── Builder helpers ───────────────────────────────────────────────────

// BuildAliasMap builds alias -> canonical name mapping (lower-cased keys).
func BuildAliasMap(registry map[string]AgentDefinition) map[string]string {
	aliases := make(map[string]string)
	for _, defn := range registry {
		aliases[strings.ToLower(defn.Name)] = defn.Name
		for _, alias := range defn.Aliases {
			aliases[strings.ToLower(alias)] = defn.Name
		}
	}
	return aliases
}

// BuildAgentSpecs builds engine-compatible AgentSpec map.
func BuildAgentSpecs(registry map[string]AgentDefinition) map[string]AgentSpec {
	specs := make(map[string]AgentSpec)
	for _, defn := range registry {
		specs[defn.Name] = AgentSpec{
			Key:      defn.Name,
			FileType: defn.MCPFileType,
			Path:     defn.MCPPath,
		}
	}
	return specs
}

// BuildSkillsTargets builds skills-compatible targets map.
func BuildSkillsTargets(registry map[string]AgentDefinition) map[string]string {
	targets := make(map[string]string)
	for _, defn := range registry {
		if defn.SkillsTarget == "" {
			continue
		}
		// Primary key: first alias (backward compat) or agent name
		key := defn.Name
		if len(defn.Aliases) > 0 {
			key = defn.Aliases[0]
		}
		targets[key] = defn.SkillsTarget
		// Also register canonical name
		if defn.Name != key {
			targets[defn.Name] = defn.SkillsTarget
		}
	}
	return targets
}

// BuildDisplayOrder returns agent names sorted by display_order.
func BuildDisplayOrder(registry map[string]AgentDefinition) []string {
	type pair struct {
		name  string
		order int
	}
	var pairs []pair
	for _, defn := range registry {
		pairs = append(pairs, pair{defn.Name, defn.DisplayOrder})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].order != pairs[j].order {
			return pairs[i].order < pairs[j].order
		}
		return pairs[i].name < pairs[j].name
	})
	result := make([]string, len(pairs))
	for i, p := range pairs {
		result[i] = p.name
	}
	return result
}

// BuildManagedKeys builds agent -> managed_keys mapping.
func BuildManagedKeys(registry map[string]AgentDefinition) map[string][]string {
	keys := make(map[string][]string)
	for _, defn := range registry {
		keys[defn.Name] = append([]string{}, defn.MCPManagedKeys...)
	}
	return keys
}
