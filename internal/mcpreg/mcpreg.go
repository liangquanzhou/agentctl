// Package mcpreg implements the MCP server registry commands: list, add, remove.
//
// The registry is split across three JSON files under <configDir>/mcp/:
//
//   - servers.json   -- server definitions (name -> spec)
//   - profiles.json  -- per-agent server assignments and per-server overrides
//   - compat.json    -- legacy compatibility layer (profilesPerAgent_map, disabled_map)
//
// All mutations are written back atomically via tx.WriteJSONAtomic.
package mcpreg

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"agentctl/internal/agents"
	"agentctl/internal/tx"
	"agentctl/internal/validate"
)

// registryFiles enumerates the three MCP registry files that form the triplet.
var registryFiles = [3]string{"servers.json", "profiles.json", "compat.json"}

// ---------------------------------------------------------------------------
// Cached singleton agent registry
// ---------------------------------------------------------------------------

var (
	regOnce  sync.Once
	regCache map[string]agents.AgentDefinition
)

func registry() map[string]agents.AgentDefinition {
	regOnce.Do(func() {
		regCache = agents.LoadAgentRegistry("")
	})
	return regCache
}

// ResetRegistryCache clears the cached registry. Intended for testing only.
func ResetRegistryCache() {
	regOnce = sync.Once{}
	regCache = nil
}

// ---------------------------------------------------------------------------
// Option types
// ---------------------------------------------------------------------------

// AddOpts carries options for MCPAdd.
type AddOpts struct {
	Agent     string   // target agent (mutually exclusive with AllAgents)
	AllAgents bool     // assign to every known agent
	ToList    bool     // add server definition only, no agent assignment
	Runtime   string   // server runtime type (default "custom")
	Command   string   // executable command
	Args      []string // command arguments
	EnvRef    []string // environment variable references
}

// RmOpts carries options for MCPRm.
type RmOpts struct {
	Agent     string // target agent (mutually exclusive with AllAgents)
	AllAgents bool   // remove from every known agent
	FromList  bool   // also remove the server definition itself
}

// ---------------------------------------------------------------------------
// Public commands
// ---------------------------------------------------------------------------

// MCPList returns a summary of all registered MCP servers and their per-agent
// enablement status.
//
// The returned map has the shape:
//
//	{
//	  "config_dir": "<path>",
//	  "agents":     []string{...},
//	  "servers":    []map[string]any{ {name, runtime, command, enabled, disabled_for}, ... }
//	}
func MCPList(configDir string) (map[string]any, error) {
	servers, profiles, compat, err := loadRegistryTriplet(configDir)
	if err != nil {
		return nil, err
	}

	serverSpecs := getMapOrEmpty(servers, "servers")
	agentList := allAgents(profiles, compat)

	// Sort server names for deterministic output.
	sortedNames := sortedKeys(serverSpecs)

	rows := make([]any, 0, len(sortedNames))
	for _, name := range sortedNames {
		specRaw := serverSpecs[name]
		spec, _ := specRaw.(map[string]any)
		if spec == nil {
			spec = make(map[string]any)
		}

		enabled := make(map[string]any, len(agentList))
		for _, agent := range agentList {
			effective := effectiveAgentServers(agent, profiles, compat)
			enabled[agent] = containsStr(effective, name)
		}

		disabledFor := readDisabledFor(profiles, name)

		rows = append(rows, map[string]any{
			"name":         name,
			"runtime":      tx.GetString(spec, "runtime", ""),
			"command":      tx.GetString(spec, "command", ""),
			"enabled":      enabled,
			"disabled_for": disabledFor,
		})
	}

	return map[string]any{
		"config_dir": configDir,
		"agents":     agentList,
		"servers":    rows,
	}, nil
}

// MCPAdd either adds a new server definition to the registry (--to-list) or
// assigns an existing server to one or more agents.
func MCPAdd(configDir, name string, opts AddOpts) (map[string]any, error) {
	servers, profiles, compat, err := loadRegistryTriplet(configDir)
	if err != nil {
		return nil, err
	}

	mcpDir := filepath.Join(configDir, "mcp")
	serverSpecs := ensureMap(servers, "servers")
	profilesServers := ensureMap(profiles, "servers")

	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("server name cannot be empty")
	}

	// Default runtime.
	runtime := opts.Runtime
	if runtime == "" {
		runtime = "custom"
	}

	// ── Add to list (definition only) ───────────────────────────────
	if opts.ToList {
		if _, exists := serverSpecs[name]; exists {
			return nil, fmt.Errorf("server already exists in list: %s", name)
		}
		if opts.Command == "" {
			return nil, fmt.Errorf("--to-list requires --command")
		}

		serverSpecs[name] = map[string]any{
			"runtime":      runtime,
			"command":      opts.Command,
			"args":         toAnySlice(opts.Args),
			"envRef":       toAnySlice(sortedUniqueStrings(opts.EnvRef)),
			"install":      map[string]any{},
			"healthcheck":  map[string]any{"type": "skip"},
			"capabilities": []any{},
		}
		touchMeta(servers)

		if err := tx.WriteJSONAtomic(filepath.Join(mcpDir, "servers.json"), servers); err != nil {
			return nil, fmt.Errorf("write servers.json: %w", err)
		}
		return map[string]any{
			"op":      "add-to-list",
			"server":  name,
			"changed": true,
		}, nil
	}

	// ── Assign existing server to agent(s) ──────────────────────────
	if _, exists := serverSpecs[name]; !exists {
		return nil, fmt.Errorf("server not found in list: %s (hint: use --to-list first)", name)
	}

	targets, err := resolveTargets(profiles, compat, opts.Agent, opts.AllAgents)
	if err != nil {
		return nil, err
	}

	changedAgents := make([]string, 0)
	for _, target := range targets {
		current := rawAgentServers(target, profiles, compat)
		if !containsStr(current, name) {
			current = append(current, name)
			changedAgents = append(changedAgents, target)
		}
		setAgentServers(target, profiles, compat, current)

		// Remove from disabled_for if present.
		node := ensureNestedMap(profilesServers, name)
		disabledRaw, ok := node["disabled_for"]
		if ok {
			if disabled, ok := disabledRaw.([]any); ok {
				filtered := make([]any, 0, len(disabled))
				for _, item := range disabled {
					if s, ok := item.(string); ok && s == target {
						continue
					}
					filtered = append(filtered, item)
				}
				node["disabled_for"] = filtered
			}
		}
	}

	touchMeta(profiles)
	touchMeta(compat)

	if err := tx.WriteJSONAtomic(filepath.Join(mcpDir, "profiles.json"), profiles); err != nil {
		return nil, fmt.Errorf("write profiles.json: %w", err)
	}
	if err := tx.WriteJSONAtomic(filepath.Join(mcpDir, "compat.json"), compat); err != nil {
		return nil, fmt.Errorf("write compat.json: %w", err)
	}

	return map[string]any{
		"op":             "add",
		"server":         name,
		"targets":        targets,
		"changed_agents": changedAgents,
	}, nil
}

// MCPRm removes a server from one or more agents. With --from-list it also
// deletes the server definition itself.
func MCPRm(configDir, name string, opts RmOpts) (map[string]any, error) {
	servers, profiles, compat, err := loadRegistryTriplet(configDir)
	if err != nil {
		return nil, err
	}

	mcpDir := filepath.Join(configDir, "mcp")
	serverSpecs := ensureMap(servers, "servers")
	profilesServers := ensureMap(profiles, "servers")
	compatDisabled := ensureMap(compat, "disabled_map")

	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("server name cannot be empty")
	}

	targets, err := resolveTargets(profiles, compat, opts.Agent, opts.AllAgents)
	if err != nil {
		return nil, err
	}

	changedAgents := make([]string, 0)
	for _, target := range targets {
		current := rawAgentServers(target, profiles, compat)
		updated := removeStr(current, name)
		if len(updated) != len(current) {
			changedAgents = append(changedAgents, target)
		}
		setAgentServers(target, profiles, compat, updated)
	}

	// When removing from all agents, mark as globally disabled.
	if opts.AllAgents {
		node := ensureNestedMap(profilesServers, name)
		node["disabled_for"] = toAnySlice(sortedStrings(targets))

		compatDisabled[name] = map[string]any{
			"reason":  "manual disable",
			"date":    tx.TodayISO(),
			"scope":   "global",
			"restore": fmt.Sprintf("agentctl mcp add %s --all", name),
		}
	}

	// When removing from list, delete all traces.
	if opts.FromList {
		delete(serverSpecs, name)
		delete(profilesServers, name)
		delete(compatDisabled, name)
	}

	touchMeta(servers)
	touchMeta(profiles)
	touchMeta(compat)

	if err := tx.WriteJSONAtomic(filepath.Join(mcpDir, "servers.json"), servers); err != nil {
		return nil, fmt.Errorf("write servers.json: %w", err)
	}
	if err := tx.WriteJSONAtomic(filepath.Join(mcpDir, "profiles.json"), profiles); err != nil {
		return nil, fmt.Errorf("write profiles.json: %w", err)
	}
	if err := tx.WriteJSONAtomic(filepath.Join(mcpDir, "compat.json"), compat); err != nil {
		return nil, fmt.Errorf("write compat.json: %w", err)
	}

	op := "rm"
	if opts.FromList {
		op = "rm-from-list"
	}
	return map[string]any{
		"op":             op,
		"server":         name,
		"targets":        targets,
		"changed_agents": changedAgents,
		"from_list":      opts.FromList,
	}, nil
}

// ---------------------------------------------------------------------------
// Internal: triplet loading & validation
// ---------------------------------------------------------------------------

// loadRegistryTriplet validates the config directory and loads the three MCP
// registry JSON files (servers, profiles, compat).
func loadRegistryTriplet(configDir string) (servers, profiles, compat map[string]any, err error) {
	ok, errors := validate.ValidateConfig(configDir)
	if !ok {
		return nil, nil, nil, fmt.Errorf("registry invalid: %s", strings.Join(errors, " | "))
	}

	mcpDir := filepath.Join(configDir, "mcp")
	servers, err = tx.ReadJSON(filepath.Join(mcpDir, "servers.json"))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read servers.json: %w", err)
	}
	profiles, err = tx.ReadJSON(filepath.Join(mcpDir, "profiles.json"))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read profiles.json: %w", err)
	}
	compat, err = tx.ReadJSON(filepath.Join(mcpDir, "compat.json"))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read compat.json: %w", err)
	}
	return servers, profiles, compat, nil
}

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

// ---------------------------------------------------------------------------
// Internal: metadata
// ---------------------------------------------------------------------------

// touchMeta updates the generated_at timestamp on a registry payload.
func touchMeta(payload map[string]any) {
	payload["generated_at"] = tx.UTCNowISO()
}

// ---------------------------------------------------------------------------
// Internal: map helpers
// ---------------------------------------------------------------------------

// getMapOrEmpty retrieves a sub-map from m[key], returning an empty map if
// the key is missing or not a map.
func getMapOrEmpty(m map[string]any, key string) map[string]any {
	v := tx.GetMap(m, key)
	if v == nil {
		return make(map[string]any)
	}
	return v
}

// ensureMap ensures m[key] is a map[string]any. If missing or wrong type, it
// creates one in place and returns it.
func ensureMap(m map[string]any, key string) map[string]any {
	v, ok := m[key]
	if ok {
		if sub, ok := v.(map[string]any); ok {
			return sub
		}
	}
	sub := make(map[string]any)
	m[key] = sub
	return sub
}

// ensureNestedMap ensures m[key] is a map[string]any (like ensureMap, identical
// semantics, provided for clarity when dealing with second-level nesting).
func ensureNestedMap(m map[string]any, key string) map[string]any {
	return ensureMap(m, key)
}

// ---------------------------------------------------------------------------
// Internal: slice / string helpers
// ---------------------------------------------------------------------------

// sortedKeys returns the keys of a map, sorted alphabetically.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// containsStr reports whether slice contains s.
func containsStr(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// removeStr returns a new slice with all occurrences of s removed.
func removeStr(slice []string, s string) []string {
	result := make([]string, 0, len(slice))
	for _, item := range slice {
		if item != s {
			result = append(result, item)
		}
	}
	return result
}

// dedup removes duplicate strings while preserving order.
func dedup(slice []string) []string {
	seen := make(map[string]struct{}, len(slice))
	result := make([]string, 0, len(slice))
	for _, s := range slice {
		if _, exists := seen[s]; exists {
			continue
		}
		seen[s] = struct{}{}
		result = append(result, s)
	}
	return result
}

// toAnySlice converts a []string to []any for JSON serialization compatibility.
func toAnySlice(ss []string) []any {
	if ss == nil {
		ss = []string{}
	}
	result := make([]any, len(ss))
	for i, s := range ss {
		result[i] = s
	}
	return result
}

// sortedUniqueStrings returns a sorted, deduplicated copy of a string slice.
func sortedUniqueStrings(ss []string) []string {
	if len(ss) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(ss))
	result := make([]string, 0, len(ss))
	for _, s := range ss {
		if _, exists := seen[s]; exists {
			continue
		}
		seen[s] = struct{}{}
		result = append(result, s)
	}
	sort.Strings(result)
	return result
}

// sortedStrings returns a sorted copy of a string slice.
func sortedStrings(ss []string) []string {
	out := make([]string, len(ss))
	copy(out, ss)
	sort.Strings(out)
	return out
}
