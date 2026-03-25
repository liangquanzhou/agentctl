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
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"agentctl/internal/agents"
	"agentctl/internal/tx"
	"agentctl/internal/validate"
)

// defaultLockTimeout is kept as a local alias for backward compatibility.
const defaultLockTimeout = tx.DefaultLockTimeout

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
// H3: Acquires exclusive lock before mutating registry files.
func MCPAdd(configDir, name string, opts AddOpts) (map[string]any, error) {
	// Acquire exclusive lock
	lockDir := filepath.Join(configDir, "state", "locks")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}
	lockPath := filepath.Join(lockDir, "mcp_registry.lock")
	lock, err := tx.AcquireLock(lockPath, tx.DefaultLockTimeout)
	if err != nil {
		return nil, fmt.Errorf("acquire lock: %w", err)
	}
	defer tx.ReleaseLock(lock)

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

	// M3: Write profiles and compat atomically with rollback on failure
	if err := writeRegistryTriplet(mcpDir, servers, profiles, compat); err != nil {
		return nil, err
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
// H3: Acquires exclusive lock before mutating registry files.
func MCPRm(configDir, name string, opts RmOpts) (map[string]any, error) {
	// Acquire exclusive lock
	lockDir := filepath.Join(configDir, "state", "locks")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}
	lockPath := filepath.Join(lockDir, "mcp_registry.lock")
	lock, err := tx.AcquireLock(lockPath, defaultLockTimeout)
	if err != nil {
		return nil, fmt.Errorf("acquire lock: %w", err)
	}
	defer tx.ReleaseLock(lock)

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

	// M3: Write all 3 files atomically with rollback on failure
	if err := writeRegistryTriplet(mcpDir, servers, profiles, compat); err != nil {
		return nil, err
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

// MCPCheck validates that each server's command is executable and that all
// envRef variables are resolvable from the secrets directory + environment.
//
// The returned map has the shape:
//
//	{
//	  "servers": [ { "name", "command", "command_ok", "envRef_missing" }, ... ],
//	  "all_ok": bool,
//	}
func MCPCheck(configDir, secretsDir string) (map[string]any, error) {
	servers, _, _, err := loadRegistryTriplet(configDir)
	if err != nil {
		return nil, err
	}

	envValues := tx.LoadEnvValues(secretsDir)
	serverSpecs := getMapOrEmpty(servers, "servers")
	sortedNames := sortedKeys(serverSpecs)

	allOK := true
	rows := make([]any, 0, len(sortedNames))

	for _, name := range sortedNames {
		specRaw := serverSpecs[name]
		spec, _ := specRaw.(map[string]any)
		if spec == nil {
			spec = make(map[string]any)
		}

		command := tx.GetString(spec, "command", "")

		// Check if command is executable.
		commandOK := false
		if command != "" {
			if filepath.IsAbs(command) {
				// Absolute path: check if file exists and is executable.
				info, err := os.Stat(command)
				if err == nil && !info.IsDir() {
					// Check if any execute bit is set.
					if info.Mode()&0o111 != 0 {
						commandOK = true
					}
				}
			} else {
				// Relative: use LookPath to find in PATH.
				if _, err := exec.LookPath(command); err == nil {
					commandOK = true
				}
			}
		}

		// Check envRef resolution.
		envRefSlice := tx.GetStringSlice(spec, "envRef")
		var missing []any
		for _, key := range envRefSlice {
			if _, ok := envValues[key]; !ok {
				missing = append(missing, key)
			}
		}
		if missing == nil {
			missing = []any{}
		}

		if !commandOK || len(missing) > 0 {
			allOK = false
		}

		rows = append(rows, map[string]any{
			"name":           name,
			"command":        command,
			"command_ok":     commandOK,
			"envRef_missing": missing,
		})
	}

	return map[string]any{
		"servers": rows,
		"all_ok":  allOK,
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
// Internal: transactional registry writes
// ---------------------------------------------------------------------------

// writeRegistryTriplet writes all 3 registry files atomically. If any write
// fails, previously written files are restored from their backups.
// M3: Ensures 3-file write is transactional.
func writeRegistryTriplet(mcpDir string, servers, profiles, compat map[string]any) error {
	files := []struct {
		name string
		data map[string]any
	}{
		{"servers.json", servers},
		{"profiles.json", profiles},
		{"compat.json", compat},
	}

	// Snapshot existing files before writing
	type backup struct {
		path    string
		existed bool
		content []byte
	}
	var backups []backup

	for _, f := range files {
		path := filepath.Join(mcpDir, f.name)
		var b backup
		b.path = path
		if data, err := os.ReadFile(path); err == nil {
			b.existed = true
			b.content = data
		}
		backups = append(backups, b)
	}

	// Write all files
	for i, f := range files {
		path := filepath.Join(mcpDir, f.name)
		if err := tx.WriteJSONAtomic(path, f.data); err != nil {
			// Rollback previously written files using atomic restore
			for j := i - 1; j >= 0; j-- {
				if backups[j].existed {
					// Use atomic write for rollback restore
					tx.WriteTextAtomic(backups[j].path, string(backups[j].content))
				} else {
					os.Remove(backups[j].path)
				}
			}
			return fmt.Errorf("write %s: %w", f.name, err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Internal: metadata
// ---------------------------------------------------------------------------

// touchMeta updates the generated_at timestamp on a registry payload.
func touchMeta(payload map[string]any) {
	payload["generated_at"] = tx.UTCNowISO()
}
