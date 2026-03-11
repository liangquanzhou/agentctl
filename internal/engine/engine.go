// Package engine implements the MCP control plane for agentctl.
//
// It provides plan, apply, rollback, list_runs, show_run, stageb_check,
// migrate_init, and migrate_finalize_legacy operations that manage MCP
// server configurations across multiple coding agents.
package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"agentctl/internal/agents"
	"agentctl/internal/tx"
	"agentctl/internal/validate"
)

// runIDRegex is the strict format for run IDs: YYYYMMDD-HHMMSS-<8hex>.
var runIDRegex = regexp.MustCompile(`^\d{8}-\d{6}-[0-9a-f]{8}$`)

// RegistryFiles lists the MCP config files that comprise the registry.
var RegistryFiles = []string{
	"servers.json",
	"profiles.json",
	"compat.json",
}

// ── Agent specs cache ───────────────────────────────────────────────

var (
	specsOnce  sync.Once
	specsCache map[string]agents.AgentSpec
)

func getAgentSpecs() map[string]agents.AgentSpec {
	specsOnce.Do(func() {
		specsCache = agents.BuildAgentSpecs(agents.LoadAgentRegistry(""))
	})
	return specsCache
}

// ResetAgentSpecsCache clears the cached agent specs (intended for testing).
func ResetAgentSpecsCache() {
	specsOnce = sync.Once{}
	specsCache = nil
}

// ── Registry loading ────────────────────────────────────────────────

func loadRegistry(configDir string) (map[string]map[string]any, error) {
	mcpDir := filepath.Join(configDir, "mcp")
	registry := make(map[string]map[string]any)
	for _, filename := range RegistryFiles {
		data, err := tx.ReadJSON(filepath.Join(mcpDir, filename))
		if err != nil {
			return nil, fmt.Errorf("loading %s: %w", filename, err)
		}
		registry[filename] = data
	}
	return registry, nil
}

// ── Env loading ─────────────────────────────────────────────────────

// loadEnvValues delegates to tx.LoadEnvValues.
func loadEnvValues(secretsDir string) map[string]string {
	return tx.LoadEnvValues(secretsDir)
}

// ── Server resolution ───────────────────────────────────────────────

// agentServerList resolves which servers an agent gets based on profiles,
// compat legacy overrides, and disabled_for filtering.
func agentServerList(agent string, registry map[string]map[string]any) []string {
	profilesAgents := tx.GetMap(registry["profiles.json"], "agents")
	compat := registry["compat.json"]

	agentProfile := tx.GetMap(profilesAgents, agent)
	servers := tx.GetStringSlice(agentProfile, "servers")

	// Legacy compat override
	if tx.GetBool(compat, "legacy_enabled", true) {
		pam := tx.GetMap(compat, "profilesPerAgent_map")
		if mapServers := tx.GetStringSlice(pam, agent); mapServers != nil {
			servers = mapServers
		}
	}

	// Apply disabled_for overrides
	profilesServers := tx.GetMap(registry["profiles.json"], "servers")
	var filtered []string
	for _, name := range servers {
		serverOverride := tx.GetMap(profilesServers, name)
		disabledAgents := tx.GetStringSlice(serverOverride, "disabled_for")
		if !stringSliceContains(disabledAgents, agent) {
			filtered = append(filtered, name)
		}
	}

	return filtered
}

func stringSliceContains(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// serverEnv resolves environment variables for a server spec.
func serverEnv(serverSpec map[string]any, envValues map[string]string) map[string]string {
	out := make(map[string]string)

	// Copy static env
	if envMap := tx.GetMap(serverSpec, "env"); envMap != nil {
		for k, v := range envMap {
			if s, ok := v.(string); ok {
				out[k] = s
			}
		}
	}

	// Resolve envRef
	for _, key := range tx.GetStringSlice(serverSpec, "envRef") {
		if val, ok := envValues[key]; ok {
			out[key] = val
		}
	}

	return out
}

// ── Desired state builders ──────────────────────────────────────────

// buildDesiredMCPServers builds the desired MCP config for standard agents
// (claude_json, json, codex_toml).
func buildDesiredMCPServers(agent string, registry map[string]map[string]any, envValues map[string]string) map[string]any {
	serversFile := tx.GetMap(registry["servers.json"], "servers")
	desired := make(map[string]any)

	for _, serverName := range agentServerList(agent, registry) {
		spec := tx.GetMap(serversFile, serverName)
		if spec == nil {
			continue
		}
		entry := map[string]any{
			"command": tx.GetString(spec, "command", ""),
			"args":    getArgsSlice(spec),
			"env":     serverEnv(spec, envValues),
		}
		desired[serverName] = entry
	}

	return desired
}

// buildDesiredOpencode builds the desired MCP config for the opencode agent.
func buildDesiredOpencode(agent string, registry map[string]map[string]any, envValues map[string]string) map[string]any {
	serversFile := tx.GetMap(registry["servers.json"], "servers")
	desired := make(map[string]any)

	for _, serverName := range agentServerList(agent, registry) {
		spec := tx.GetMap(serversFile, serverName)
		if spec == nil {
			continue
		}

		cmd := tx.GetString(spec, "command", "")
		args := getArgsSlice(spec)
		command := make([]any, 0, 1+len(args))
		command = append(command, cmd)
		for _, a := range args {
			command = append(command, a)
		}

		item := map[string]any{
			"type":    "local",
			"command": command,
			"enabled": true,
		}
		env := serverEnv(spec, envValues)
		if len(env) > 0 {
			item["environment"] = env
		}
		desired[serverName] = item
	}

	return desired
}

// getArgsSlice extracts args as []any from a server spec, defaulting to empty slice.
func getArgsSlice(spec map[string]any) []any {
	val, ok := spec["args"]
	if !ok || val == nil {
		return []any{}
	}
	arr, ok := val.([]any)
	if !ok {
		return []any{}
	}
	return arr
}

// ── Current state reading ───────────────────────────────────────────

// currentSubtree reads the current MCP config subtree from an agent's file.
// Returns (subtree, fileExists, fullDocument, error).
// When the file doesn't exist, returns empty maps with nil error.
// When the file exists but cannot be read/parsed, returns nil maps with the error.
func currentSubtree(agent string, spec agents.AgentSpec) (map[string]any, bool, map[string]any, error) {
	if _, err := os.Stat(spec.Path); os.IsNotExist(err) {
		return map[string]any{}, false, map[string]any{}, nil
	}

	switch spec.FileType {
	case "claude_json", "json", "opencode_json":
		full, err := tx.ReadJSON(spec.Path)
		if err != nil {
			return nil, false, nil, fmt.Errorf("read %s for %s: %w", spec.Path, agent, err)
		}
		var key string
		if spec.FileType == "opencode_json" {
			key = "mcp"
		} else {
			key = "mcpServers"
		}
		subtree := tx.GetMap(full, key)
		if subtree == nil {
			subtree = map[string]any{}
		}
		return subtree, true, full, nil

	case "openclaw_json":
		full, err := tx.ReadJSON(spec.Path)
		if err != nil {
			return nil, false, nil, fmt.Errorf("read %s for %s: %w", spec.Path, agent, err)
		}
		mcpMap := tx.GetMap(full, "mcp")
		if mcpMap == nil {
			return map[string]any{}, true, full, nil
		}
		subtree := tx.GetMap(mcpMap, "servers")
		if subtree == nil {
			subtree = map[string]any{}
		}
		return subtree, true, full, nil

	case "codex_toml":
		full, err := tx.ReadTOML(spec.Path)
		if err != nil {
			return nil, false, nil, fmt.Errorf("read %s for %s: %w", spec.Path, agent, err)
		}
		subtree := tx.GetMap(full, "mcp_servers")
		if subtree == nil {
			subtree = map[string]any{}
		}
		return subtree, true, full, nil
	}

	return map[string]any{}, false, map[string]any{}, nil
}

// ── Render + write ──────────────────────────────────────────────────

// renderUpdated merges the desired MCP servers into the full document.
func renderUpdated(agent string, spec agents.AgentSpec, full map[string]any, desired map[string]any) map[string]any {
	updated := shallowCopyMap(full)

	switch spec.FileType {
	case "claude_json":
		updated["mcpServers"] = desired
		// Clear per-project mcpServers
		if projects, ok := updated["projects"]; ok {
			if projMap, ok := projects.(map[string]any); ok {
				for _, proj := range projMap {
					if p, ok := proj.(map[string]any); ok {
						if _, has := p["mcpServers"]; has {
							p["mcpServers"] = map[string]any{}
						}
					}
				}
			}
		}
		return updated

	case "json":
		updated["mcpServers"] = desired
		return updated

	case "opencode_json":
		updated["mcp"] = desired
		return updated

	case "openclaw_json":
		mcpMap := tx.GetMap(updated, "mcp")
		if mcpMap == nil {
			mcpMap = map[string]any{}
		}
		updatedMCP := shallowCopyMap(mcpMap)
		updatedMCP["servers"] = desired
		updated["mcp"] = updatedMCP
		return updated

	case "codex_toml":
		updated["mcp_servers"] = desired
		return updated
	}

	return updated
}

// writeTarget writes the rendered config to disk using the appropriate format.
func writeTarget(path string, spec agents.AgentSpec, data map[string]any) error {
	switch spec.FileType {
	case "claude_json", "json", "opencode_json", "openclaw_json":
		return tx.WriteJSONAtomic(path, data)
	case "codex_toml":
		return tx.WriteTOMLAtomic(path, data)
	default:
		return fmt.Errorf("unsupported file type: %s", spec.FileType)
	}
}

// ── Plan ────────────────────────────────────────────────────────────

// PlanResult holds the output of a plan operation.
type PlanResult struct {
	GeneratedAt string      `json:"generated_at"`
	ConfigDir   string      `json:"config_dir"`
	SecretsDir  string      `json:"secrets_dir"`
	Agents      []PlanAgent `json:"agents"`
}

// PlanAgent describes the plan for a single agent.
type PlanAgent struct {
	Agent        string         `json:"agent"`
	Path         string         `json:"path"`
	Exists       bool           `json:"exists"`
	Changed      bool           `json:"changed"`
	CurrentCount int            `json:"current_count"`
	DesiredCount int            `json:"desired_count"`
	Desired      map[string]any `json:"desired"`
	Current      map[string]any `json:"-"` // current MCP subtree, not serialized
	FullCurrent  map[string]any `json:"-"` // full document, not serialized to JSON
}

// planInternal computes the desired vs current diff for all agents.
func planInternal(configDir, secretsDir string) (*PlanResult, error) {
	ok, errors := validate.ValidateConfig(configDir)
	if !ok {
		return nil, fmt.Errorf("registry invalid: %s", strings.Join(errors, " | "))
	}

	registry, err := loadRegistry(configDir)
	if err != nil {
		return nil, err
	}
	envValues := loadEnvValues(secretsDir)

	profilesAgents := tx.GetMap(registry["profiles.json"], "agents")
	compat := registry["compat.json"]
	agentSpecs := getAgentSpecs()

	var resultAgents []PlanAgent

	for agentName, spec := range agentSpecs {
		// Security: validate spec.Path is under $HOME before any read/write
		if err := tx.IsUnderHome(spec.Path); err != nil {
			return nil, fmt.Errorf("agent %s: %w", agentName, err)
		}

		// Check if agent is configured in profiles or compat
		_, inProfiles := profilesAgents[agentName]
		inCompat := false
		if tx.GetBool(compat, "legacy_enabled", true) {
			pam := tx.GetMap(compat, "profilesPerAgent_map")
			if pam != nil {
				_, inCompat = pam[agentName]
			}
		}

		if !inProfiles && !inCompat {
			continue
		}

		var desired map[string]any
		if spec.FileType == "opencode_json" {
			desired = buildDesiredOpencode(agentName, registry, envValues)
		} else {
			desired = buildDesiredMCPServers(agentName, registry, envValues)
		}

		current, exists, full, err := currentSubtree(agentName, spec)
		if err != nil {
			return nil, fmt.Errorf("current state for %s: %w", agentName, err)
		}
		changed := normalize(current) != normalize(desired)

		resultAgents = append(resultAgents, PlanAgent{
			Agent:        agentName,
			Path:         spec.Path,
			Exists:       exists,
			Changed:      changed,
			CurrentCount: len(current),
			DesiredCount: len(desired),
			Desired:      desired,
			Current:      current,
			FullCurrent:  full,
		})
	}

	// Sort agents by name for deterministic output
	sort.Slice(resultAgents, func(i, j int) bool {
		return resultAgents[i].Agent < resultAgents[j].Agent
	})

	return &PlanResult{
		GeneratedAt: tx.UTCNowISO(),
		ConfigDir:   configDir,
		SecretsDir:  secretsDir,
		Agents:      resultAgents,
	}, nil
}

// Plan compares desired vs current MCP config for all agents.
func Plan(configDir, secretsDir string) (*PlanResult, error) {
	return planInternal(configDir, secretsDir)
}

// ToMap converts PlanResult to a generic map for JSON serialization.
func (pr *PlanResult) ToMap() map[string]any {
	agentList := make([]any, 0, len(pr.Agents))
	for _, a := range pr.Agents {
		agentList = append(agentList, map[string]any{
			"agent":         a.Agent,
			"path":          a.Path,
			"exists":        a.Exists,
			"changed":       a.Changed,
			"current_count": a.CurrentCount,
			"desired_count": a.DesiredCount,
			"desired":       a.Desired,
		})
	}
	return map[string]any{
		"generated_at": pr.GeneratedAt,
		"config_dir":   pr.ConfigDir,
		"secrets_dir":  pr.SecretsDir,
		"agents":       agentList,
	}
}

// ── Apply ───────────────────────────────────────────────────────────

// ChangedFile records a single file modification during apply.
type ChangedFile struct {
	Agent     string `json:"agent"`
	Path      string `json:"path"`
	PreExists bool   `json:"pre_exists"`
	Snapshot  string `json:"snapshot,omitempty"`
	PreHash   string `json:"pre_hash,omitempty"`
	PostHash  string `json:"post_hash,omitempty"`
}

// ApplyResult holds the output of an apply operation.
type ApplyResult struct {
	RunID        string        `json:"run_id"`
	Timestamp    string        `json:"timestamp"`
	Actor        string        `json:"actor"`
	Command      string        `json:"command"`
	Result       string        `json:"result"`
	Severity     string        `json:"severity"`
	Reason       string        `json:"reason,omitempty"`
	BreakGlass   bool          `json:"break_glass"`
	ChangedFiles []ChangedFile `json:"changed_files"`
	DurationMs   int64         `json:"duration_ms"`
	Error        string        `json:"error,omitempty"`
}

// ToMap converts ApplyResult to a generic map for JSON serialization.
func (ar *ApplyResult) ToMap() map[string]any {
	files := make([]any, 0, len(ar.ChangedFiles))
	for _, f := range ar.ChangedFiles {
		entry := map[string]any{
			"agent":      f.Agent,
			"path":       f.Path,
			"pre_exists": f.PreExists,
			"snapshot":   f.Snapshot,
			"pre_hash":   f.PreHash,
			"post_hash":  f.PostHash,
		}
		files = append(files, entry)
	}
	m := map[string]any{
		"run_id":        ar.RunID,
		"timestamp":     ar.Timestamp,
		"actor":         ar.Actor,
		"command":       ar.Command,
		"result":        ar.Result,
		"severity":      ar.Severity,
		"reason":        ar.Reason,
		"break_glass":   ar.BreakGlass,
		"changed_files": files,
		"duration_ms":   ar.DurationMs,
	}
	if ar.Error != "" {
		m["error"] = ar.Error
	}
	return m
}

// snapshotOne creates a snapshot of a file before modification.
func snapshotOne(path, snapshotDir, key string) (bool, string) {
	snapPath := filepath.Join(snapshotDir, key+".snapshot")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false, ""
	}
	if err := tx.EnsureParent(snapPath); err != nil {
		return false, ""
	}
	if err := tx.CopyFile(path, snapPath); err != nil {
		return false, ""
	}
	return true, snapPath
}

// Apply applies the planned MCP config changes to all agents.
// It creates snapshots before writing, rolls back on failure, and records a run manifest.
func Apply(configDir, secretsDir, stateDir string, breakGlass bool, reason, actor string) (*ApplyResult, error) {
	if breakGlass && reason == "" {
		return nil, fmt.Errorf("--break-glass requires --reason")
	}

	if err := tx.EnsureStateDirs(stateDir); err != nil {
		return nil, fmt.Errorf("ensure state dirs: %w", err)
	}

	lockPath := filepath.Join(stateDir, "locks", "apply.lock")
	lock, err := tx.AcquireLock(lockPath, 30)
	if err != nil {
		return nil, fmt.Errorf("acquire lock: %w", err)
	}
	defer tx.ReleaseLock(lock)

	runID := generateRunID()
	snapshotDir := filepath.Join(stateDir, "snapshots", runID)
	if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
		return nil, fmt.Errorf("create snapshot dir: %w", err)
	}

	if actor == "" {
		actor = os.Getenv("USER")
		if actor == "" {
			actor = "unknown"
		}
	}

	result := &ApplyResult{
		RunID:        runID,
		Timestamp:    tx.UTCNowISO(),
		Actor:        actor,
		Command:      "apply",
		Result:       "success",
		Severity:     "info",
		Reason:       reason,
		BreakGlass:   breakGlass,
		ChangedFiles: []ChangedFile{},
	}

	start := time.Now()
	var backupMeta []ChangedFile
	var applyErr error

	func() {
		defer func() {
			if r := recover(); r != nil {
				applyErr = fmt.Errorf("panic: %v", r)
			}
		}()

		planResult, err := planInternal(configDir, secretsDir)
		if err != nil {
			applyErr = err
			return
		}

		agentSpecs := getAgentSpecs()

		for _, item := range planResult.Agents {
			if !item.Changed {
				continue
			}

			spec := agentSpecs[item.Agent]
			fullCurrent := item.FullCurrent
			if fullCurrent == nil {
				fullCurrent = map[string]any{}
			}

			preExists, snapshot := snapshotOne(item.Path, snapshotDir, item.Agent)
			var preHash string
			if preExists {
				preHash, _ = tx.SHA256File(item.Path)
			}

			updated := renderUpdated(item.Agent, spec, fullCurrent, item.Desired)
			if err := writeTarget(item.Path, spec, updated); err != nil {
				applyErr = fmt.Errorf("write %s: %w", item.Path, err)
				return
			}

			postHash, _ := tx.SHA256File(item.Path)

			change := ChangedFile{
				Agent:     item.Agent,
				Path:      item.Path,
				PreExists: preExists,
				Snapshot:  snapshot,
				PreHash:   preHash,
				PostHash:  postHash,
			}
			result.ChangedFiles = append(result.ChangedFiles, change)
			backupMeta = append(backupMeta, change)
		}

		if len(result.ChangedFiles) == 0 {
			result.Result = "dry_run"
			result.Severity = "info"
		}
	}()

	duration := time.Since(start)
	result.DurationMs = duration.Milliseconds()

	if applyErr != nil {
		// Rollback files that were changed in this run
		var rollbackErrors []string
		for i := len(backupMeta) - 1; i >= 0; i-- {
			meta := backupMeta[i]
			if meta.PreExists && meta.Snapshot != "" {
				if err := tx.EnsureParent(meta.Path); err != nil {
					rollbackErrors = append(rollbackErrors, fmt.Sprintf("ensure parent %s: %v", meta.Path, err))
				}
				if err := tx.CopyFile(meta.Snapshot, meta.Path); err != nil {
					rollbackErrors = append(rollbackErrors, fmt.Sprintf("restore %s: %v", meta.Path, err))
				}
			} else {
				if err := os.Remove(meta.Path); err != nil && !os.IsNotExist(err) {
					rollbackErrors = append(rollbackErrors, fmt.Sprintf("remove %s: %v", meta.Path, err))
				}
			}
		}

		if len(rollbackErrors) > 0 {
			result.Result = "partial_rollback"
			result.Error = applyErr.Error() + "; rollback errors: " + strings.Join(rollbackErrors, "; ")
		} else {
			result.Result = "rolled_back"
			result.Error = applyErr.Error()
		}
		result.Severity = "critical"

		// Write manifest even on failure
		manifestPath := filepath.Join(stateDir, "runs", runID+".json")
		_ = tx.WriteJSONAtomic(manifestPath, result.ToMap())

		return result, applyErr
	}

	// Write run manifest
	manifestPath := filepath.Join(stateDir, "runs", runID+".json")
	if err := tx.WriteJSONAtomic(manifestPath, result.ToMap()); err != nil {
		return result, fmt.Errorf("write manifest: %w", err)
	}

	return result, nil
}
