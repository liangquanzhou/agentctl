package mcpreg

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"agentctl/internal/agents"
	"agentctl/internal/tx"
)

// ---------------------------------------------------------------------------
// Internal: agent resolution
// ---------------------------------------------------------------------------

// allAgents collects every known agent from the builtin registry, profiles, and
// compat layer, returning them sorted alphabetically.
func allAgents(profiles, compat map[string]any) []string {
	seen := make(map[string]struct{})

	// Builtin registry.
	for name := range registry() {
		seen[name] = struct{}{}
	}

	// Profiles agents.
	if pa := tx.GetMap(profiles, "agents"); pa != nil {
		for name := range pa {
			seen[name] = struct{}{}
		}
	}

	// Compat profilesPerAgent_map.
	if cm := tx.GetMap(compat, "profilesPerAgent_map"); cm != nil {
		for name := range cm {
			seen[name] = struct{}{}
		}
	}

	result := make([]string, 0, len(seen))
	for name := range seen {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

// normalizeAgent resolves an agent alias to its canonical name.
func normalizeAgent(agent string) string {
	key := strings.TrimSpace(strings.ToLower(agent))
	aliasMap := agents.BuildAliasMap(registry())
	if canonical, ok := aliasMap[key]; ok {
		return canonical
	}
	return agent
}

// resolveTargets determines which agents a command should operate on.
func resolveTargets(profiles, compat map[string]any, agent string, allFlag bool) ([]string, error) {
	if agent != "" && allFlag {
		return nil, fmt.Errorf("--agent and --all are mutually exclusive")
	}
	if allFlag {
		return allAgents(profiles, compat), nil
	}
	selected := agent
	if selected != "" {
		selected = normalizeAgent(selected)
	} else {
		selected = os.Getenv("AGENTCTL_DEFAULT_AGENT")
		if selected == "" {
			selected = "codex"
		}
	}
	return []string{selected}, nil
}

// ---------------------------------------------------------------------------
// Internal: per-agent server management
// ---------------------------------------------------------------------------

// ensureAgentProfile ensures the agent key exists in profiles["agents"] with
// the correct sub-structure (servers, overrides, post_apply_hooks).
func ensureAgentProfile(profiles map[string]any, agent string) {
	agentsMap := ensureMap(profiles, "agents")

	existing, ok := agentsMap[agent]
	if !ok {
		agentsMap[agent] = map[string]any{
			"servers":          []any{},
			"overrides":        map[string]any{},
			"post_apply_hooks": []any{},
		}
		return
	}

	profile, ok := existing.(map[string]any)
	if !ok {
		agentsMap[agent] = map[string]any{
			"servers":          []any{},
			"overrides":        map[string]any{},
			"post_apply_hooks": []any{},
		}
		return
	}

	if _, ok := profile["servers"]; !ok {
		profile["servers"] = []any{}
	}
	if _, ok := profile["overrides"]; !ok {
		profile["overrides"] = map[string]any{}
	}
	if _, ok := profile["post_apply_hooks"]; !ok {
		profile["post_apply_hooks"] = []any{}
	}
}

// rawAgentServers returns the raw server list for an agent, accounting for the
// compat legacy_enabled layer which may override the profiles value.
func rawAgentServers(agent string, profiles, compat map[string]any) []string {
	ensureAgentProfile(profiles, agent)

	agentsMap := tx.GetMap(profiles, "agents")
	current := tx.GetMap(agentsMap, agent)
	servers := tx.GetStringSlice(current, "servers")
	if servers == nil {
		servers = []string{}
	}

	if tx.GetBool(compat, "legacy_enabled", true) {
		compatMap := tx.GetMap(compat, "profilesPerAgent_map")
		if compatMap != nil {
			if val, ok := compatMap[agent]; ok {
				if arr, ok := val.([]any); ok {
					servers = make([]string, 0, len(arr))
					for _, item := range arr {
						if s, ok := item.(string); ok {
							servers = append(servers, s)
						}
					}
				}
			}
		}
	}

	return servers
}

// setAgentServers writes the deduplicated server list for an agent into both
// profiles and (if legacy_enabled) compat.
func setAgentServers(agent string, profiles, compat map[string]any, servers []string) {
	normalized := dedup(servers)

	ensureAgentProfile(profiles, agent)
	agentsMap := tx.GetMap(profiles, "agents")
	profile := tx.GetMap(agentsMap, agent)
	profile["servers"] = toAnySlice(normalized)

	if tx.GetBool(compat, "legacy_enabled", true) {
		compatMap := ensureMap(compat, "profilesPerAgent_map")
		compatMap[agent] = toAnySlice(normalized)
	}
}

// effectiveAgentServers returns the server list for an agent after filtering
// out servers that have the agent in their disabled_for list.
func effectiveAgentServers(agent string, profiles, compat map[string]any) []string {
	servers := rawAgentServers(agent, profiles, compat)

	profilesServers := tx.GetMap(profiles, "servers")
	if profilesServers == nil {
		return servers
	}

	filtered := make([]string, 0, len(servers))
	for _, server := range servers {
		nodeRaw, ok := profilesServers[server]
		if !ok {
			filtered = append(filtered, server)
			continue
		}
		node, ok := nodeRaw.(map[string]any)
		if !ok {
			filtered = append(filtered, server)
			continue
		}
		disabledFor := tx.GetStringSlice(node, "disabled_for")
		if !containsStr(disabledFor, agent) {
			filtered = append(filtered, server)
		}
	}
	return filtered
}

// readDisabledFor extracts the sorted disabled_for list for a server from
// profiles["servers"][name]["disabled_for"].
func readDisabledFor(profiles map[string]any, serverName string) []string {
	profilesServers := tx.GetMap(profiles, "servers")
	if profilesServers == nil {
		return []string{}
	}
	nodeRaw, ok := profilesServers[serverName]
	if !ok {
		return []string{}
	}
	node, ok := nodeRaw.(map[string]any)
	if !ok {
		return []string{}
	}
	disabled := tx.GetStringSlice(node, "disabled_for")
	if disabled == nil {
		return []string{}
	}
	sort.Strings(disabled)
	return disabled
}
