// Package engine implements the MCP control plane for agentctl.
//
// It provides plan, apply, rollback, list_runs, show_run, stageb_check,
// migrate_init, and migrate_finalize_legacy operations that manage MCP
// server configurations across multiple coding agents.
package engine

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"agentctl/internal/agents"
	"agentctl/internal/tx"
	"agentctl/internal/validate"
)

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

// loadEnvValues reads .env files from secretsDir and merges with shell env.
// Shell environment takes priority over .env file values.
func loadEnvValues(secretsDir string) map[string]string {
	values := make(map[string]string)

	info, err := os.Stat(secretsDir)
	if err != nil || !info.IsDir() {
		return values
	}

	// Read *.env files sorted by name
	entries, err := os.ReadDir(secretsDir)
	if err != nil {
		return values
	}

	var envFiles []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".env") {
			envFiles = append(envFiles, filepath.Join(secretsDir, entry.Name()))
		}
	}
	sort.Strings(envFiles)

	for _, path := range envFiles {
		parsed := parseEnvFile(path)
		for k, v := range parsed {
			values[k] = v
		}
	}

	// Shell environment has higher priority
	for _, kv := range os.Environ() {
		idx := strings.IndexByte(kv, '=')
		if idx < 0 {
			continue
		}
		values[kv[:idx]] = kv[idx+1:]
	}

	return values
}

// parseEnvFile is a simple .env parser: key=value lines, # comments, empty lines ignored.
func parseEnvFile(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	result := make(map[string]string)
	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])

		// Strip surrounding quotes if present
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') ||
				(val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}

		if key != "" {
			result[key] = val
		}
	}
	return result
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

	case "codex_toml":
		updated["mcp_servers"] = desired
		return updated
	}

	return updated
}

// writeTarget writes the rendered config to disk using the appropriate format.
func writeTarget(path string, spec agents.AgentSpec, data map[string]any) error {
	switch spec.FileType {
	case "claude_json", "json", "opencode_json":
		return tx.WriteJSONAtomic(path, data)
	case "codex_toml":
		return tx.WriteTOMLAtomic(path, data)
	default:
		return fmt.Errorf("unsupported file type: %s", spec.FileType)
	}
}

// ── Normalization ───────────────────────────────────────────────────

// normalize produces a canonical JSON string for comparison.
func normalize(obj any) string {
	return tx.Normalize(obj)
}

// ── Run ID ──────────────────────────────────────────────────────────

// generateRunID produces a run ID in the format: YYYYMMDD-HHMMSS-<8hex>.
func generateRunID() string {
	now := time.Now().UTC()
	prefix := now.Format("20060102-150405")
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// Fallback to time-based hex if crypto/rand fails
		b = []byte{byte(now.UnixNano() >> 24), byte(now.UnixNano() >> 16),
			byte(now.UnixNano() >> 8), byte(now.UnixNano())}
	}
	return prefix + "-" + hex.EncodeToString(b)
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
	FullCurrent  map[string]any `json:"-"` // internal, not serialized to JSON
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

// ── Rollback ────────────────────────────────────────────────────────

// RollbackFile records a single file restoration during rollback.
type RollbackFile struct {
	Agent        string `json:"agent"`
	Path         string `json:"path"`
	RestoredFrom string `json:"restored_from,omitempty"`
}

// RollbackResult holds the output of a rollback operation.
type RollbackResult struct {
	RunID        string         `json:"run_id"`
	Timestamp    string         `json:"timestamp"`
	Actor        string         `json:"actor"`
	Command      string         `json:"command"`
	SourceRunID  string         `json:"source_run_id"`
	Result       string         `json:"result"`
	Severity     string         `json:"severity"`
	ChangedFiles []RollbackFile `json:"changed_files"`
	Error        string         `json:"error,omitempty"`
}

// ToMap converts RollbackResult to a generic map for JSON serialization.
func (rr *RollbackResult) ToMap() map[string]any {
	files := make([]any, 0, len(rr.ChangedFiles))
	for _, f := range rr.ChangedFiles {
		files = append(files, map[string]any{
			"agent":         f.Agent,
			"path":          f.Path,
			"restored_from": f.RestoredFrom,
		})
	}
	m := map[string]any{
		"run_id":         rr.RunID,
		"timestamp":      rr.Timestamp,
		"actor":          rr.Actor,
		"command":        rr.Command,
		"source_run_id":  rr.SourceRunID,
		"result":         rr.Result,
		"severity":       rr.Severity,
		"changed_files":  files,
	}
	if rr.Error != "" {
		m["error"] = rr.Error
	}
	return m
}

// Rollback restores agent configs from a previous run's snapshots.
// If agent is non-empty, only that agent's files are rolled back.
func Rollback(stateDir, runID, agent, actor string) (*RollbackResult, error) {
	if err := tx.EnsureStateDirs(stateDir); err != nil {
		return nil, fmt.Errorf("ensure state dirs: %w", err)
	}

	lockPath := filepath.Join(stateDir, "locks", "apply.lock")
	lock, err := tx.AcquireLock(lockPath, 30)
	if err != nil {
		return nil, fmt.Errorf("acquire lock: %w", err)
	}
	defer tx.ReleaseLock(lock)

	srcManifest, err := tx.ReadJSON(filepath.Join(stateDir, "runs", runID+".json"))
	if err != nil {
		return nil, fmt.Errorf("read source manifest: %w", err)
	}

	changes := getChangedFiles(srcManifest)

	if actor == "" {
		actor = os.Getenv("USER")
		if actor == "" {
			actor = "unknown"
		}
	}

	rollbackID := generateRunID()
	result := &RollbackResult{
		RunID:        rollbackID,
		Timestamp:    tx.UTCNowISO(),
		Actor:        actor,
		Command:      "rollback",
		SourceRunID:  runID,
		Result:       "rolled_back",
		Severity:     "warning",
		ChangedFiles: []RollbackFile{},
	}

	defer func() {
		manifestPath := filepath.Join(stateDir, "runs", rollbackID+".json")
		_ = tx.WriteJSONAtomic(manifestPath, result.ToMap())
	}()

	var rollbackErrors []string
	for _, meta := range changes {
		metaAgent, _ := meta["agent"].(string)
		if agent != "" && metaAgent != agent {
			continue
		}

		path, _ := meta["path"].(string)
		preExists, _ := meta["pre_exists"].(bool)
		snapshot, _ := meta["snapshot"].(string)

		if preExists && snapshot != "" {
			if err := tx.EnsureParent(path); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Sprintf("ensure parent %s: %v", path, err))
			}
			if err := tx.CopyFile(snapshot, path); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Sprintf("restore %s: %v", path, err))
			}
		} else {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				rollbackErrors = append(rollbackErrors, fmt.Sprintf("remove %s: %v", path, err))
			}
		}

		result.ChangedFiles = append(result.ChangedFiles, RollbackFile{
			Agent:        metaAgent,
			Path:         path,
			RestoredFrom: snapshot,
		})
	}

	if len(rollbackErrors) > 0 {
		result.Result = "partial_rollback"
		result.Error = "rollback errors: " + strings.Join(rollbackErrors, "; ")
	}

	return result, nil
}

// getChangedFiles extracts the changed_files array from a manifest map.
func getChangedFiles(manifest map[string]any) []map[string]any {
	val, ok := manifest["changed_files"]
	if !ok {
		return nil
	}
	arr, ok := val.([]any)
	if !ok {
		return nil
	}
	result := make([]map[string]any, 0, len(arr))
	for _, item := range arr {
		if m, ok := item.(map[string]any); ok {
			result = append(result, m)
		}
	}
	return result
}

// ── List runs / Show run ────────────────────────────────────────────

// ListRuns reads all run manifests, newest first.
func ListRuns(stateDir string) []map[string]any {
	runsDir := filepath.Join(stateDir, "runs")
	if _, err := os.Stat(runsDir); os.IsNotExist(err) {
		return nil
	}

	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return nil
	}

	// Collect JSON files, sorted by name descending (newest first)
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			files = append(files, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(files)))

	var rows []map[string]any
	for _, name := range files {
		data, err := tx.ReadJSON(filepath.Join(runsDir, name))
		if err != nil {
			continue
		}
		rows = append(rows, data)
	}

	return rows
}

// ShowRun reads a single run manifest by its ID.
func ShowRun(stateDir, runID string) (map[string]any, error) {
	return tx.ReadJSON(filepath.Join(stateDir, "runs", runID+".json"))
}

// ── Stage B check ───────────────────────────────────────────────────

// StageBResult holds the verdict of a stage B readiness check.
type StageBResult struct {
	WindowDays     int    `json:"window_days"`
	RecentRuns     int    `json:"recent_runs"`
	DistinctDays   int    `json:"distinct_days"`
	CriticalFailed int    `json:"critical_failed"`
	Verdict        string `json:"verdict"`
}

// ToMap converts StageBResult to a generic map for JSON serialization.
func (sr *StageBResult) ToMap() map[string]any {
	return map[string]any{
		"window_days":     sr.WindowDays,
		"recent_runs":     sr.RecentRuns,
		"distinct_days":   sr.DistinctDays,
		"critical_failed": sr.CriticalFailed,
		"verdict":         sr.Verdict,
	}
}

// StageBCheck checks if stage B criteria are met: no critical failures and
// runs on at least windowDays distinct days within the window.
func StageBCheck(stateDir string, windowDays int) (*StageBResult, error) {
	if windowDays <= 0 {
		windowDays = 3
	}

	now := time.Now().UTC()
	cutoff := now.Add(-time.Duration(windowDays) * 24 * time.Hour)

	runs := ListRuns(stateDir)
	var recent []map[string]any

	for _, run := range runs {
		ts, ok := run["timestamp"].(string)
		if !ok {
			continue
		}
		parsed, err := parseISO8601(ts)
		if err != nil {
			continue
		}
		if !parsed.Before(cutoff) {
			recent = append(recent, run)
		}
	}

	criticalFailed := 0
	for _, r := range recent {
		result, _ := r["result"].(string)
		severity, _ := r["severity"].(string)
		if result == "failed" && severity == "critical" {
			criticalFailed++
		}
	}

	dates := make(map[string]bool)
	for _, r := range recent {
		ts, ok := r["timestamp"].(string)
		if !ok {
			continue
		}
		parsed, err := parseISO8601(ts)
		if err != nil {
			continue
		}
		dates[parsed.Format("2006-01-02")] = true
	}

	verdict := "FAIL"
	if criticalFailed == 0 && len(dates) >= windowDays {
		verdict = "PASS"
	}

	return &StageBResult{
		WindowDays:     windowDays,
		RecentRuns:     len(recent),
		DistinctDays:   len(dates),
		CriticalFailed: criticalFailed,
		Verdict:        verdict,
	}, nil
}

// parseISO8601 parses a timestamp like "2025-01-15T12:00:00Z" or with +00:00 offset.
func parseISO8601(s string) (time.Time, error) {
	// Replace trailing Z with +00:00 for consistent parsing
	normalized := strings.Replace(s, "Z", "+00:00", 1)
	return time.Parse("2006-01-02T15:04:05-07:00", normalized)
}

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
			"runtime":     InferRuntime(cmd),
			"command":     cmd,
			"args":        args,
			"envRef":      envRef,
			"install":     map[string]any{},
			"healthcheck": map[string]any{"type": "skip"},
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

// ── Helpers ─────────────────────────────────────────────────────────

// shallowCopyMap creates a shallow copy of a map.
func shallowCopyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// MarshalJSON is a convenience for producing JSON bytes from any value.
func MarshalJSON(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}
