package skills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"agentctl/internal/agents"
)

// SkillsConfig controls per-agent skill filtering.
// Agents not listed (or with ["*"]) receive all skills.
type SkillsConfig struct {
	Agents map[string]AgentSkillsSpec `json:"agents"`
}

// AgentSkillsSpec defines which skills an agent should receive.
type AgentSkillsSpec struct {
	Skills []string `json:"skills"` // ["*"] = all; explicit list = only those
}

// LoadSkillsConfig reads the skills config from configDir/skills/config.json.
// Returns nil (meaning "sync all") if the file doesn't exist or is invalid.
func LoadSkillsConfig(configDir string) *SkillsConfig {
	path := filepath.Join(configDir, "skills", "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}

	agentsRaw, ok := raw["agents"]
	if !ok {
		return nil
	}
	agentsMap, ok := agentsRaw.(map[string]any)
	if !ok {
		return nil
	}

	cfg := &SkillsConfig{
		Agents: make(map[string]AgentSkillsSpec),
	}
	for name, val := range agentsMap {
		specMap, ok := val.(map[string]any)
		if !ok {
			continue
		}
		skillsRaw, ok := specMap["skills"]
		if !ok {
			continue
		}
		skillsArr, ok := skillsRaw.([]any)
		if !ok {
			continue
		}
		var skills []string
		for _, item := range skillsArr {
			if s, ok := item.(string); ok {
				skills = append(skills, s)
			}
		}
		cfg.Agents[name] = AgentSkillsSpec{Skills: skills}
	}

	return cfg
}

// FilteredSkills returns the subset of allSkills that agentName should receive.
// Returns allSkills unchanged if: config is nil, agent is not in config,
// agent has no skills list, or list contains "*".
func (c *SkillsConfig) FilteredSkills(agentName string, allSkills map[string]string) map[string]string {
	if c == nil {
		return allSkills
	}
	spec, ok := c.lookupAgentSpec(agentName)
	if !ok || len(spec.Skills) == 0 {
		return allSkills
	}
	for _, s := range spec.Skills {
		if s == "*" {
			return allSkills
		}
	}

	allowed := make(map[string]bool, len(spec.Skills))
	for _, s := range spec.Skills {
		allowed[s] = true
	}
	filtered := make(map[string]string)
	for name, dir := range allSkills {
		if allowed[name] {
			filtered[name] = dir
		}
	}
	return filtered
}

func (c *SkillsConfig) lookupAgentSpec(agentName string) (AgentSkillsSpec, bool) {
	if c == nil {
		return AgentSkillsSpec{}, false
	}
	if spec, ok := c.Agents[agentName]; ok {
		return spec, true
	}

	registry := agents.LoadAgentRegistry("")
	aliasMap := agents.BuildAliasMap(registry)
	canonical, ok := aliasMap[strings.ToLower(agentName)]
	if !ok {
		return AgentSkillsSpec{}, false
	}
	if spec, ok := c.Agents[canonical]; ok {
		return spec, true
	}

	if defn, ok := registry[canonical]; ok {
		for _, alias := range defn.Aliases {
			if spec, ok := c.Agents[alias]; ok {
				return spec, true
			}
		}
	}
	return AgentSkillsSpec{}, false
}
