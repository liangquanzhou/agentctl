package agents

import (
	"os"
	"os/exec"
	"path/filepath"
)

// ProbeStatus describes the detected state of an agent on the local machine.
type ProbeStatus struct {
	Name           string `json:"name"`
	Installed      bool   `json:"installed"`       // config path parent dir exists
	ConfigFound    bool   `json:"config_found"`    // MCP config file exists
	ConfigWritable bool   `json:"config_writable"` // MCP config file/dir is writable
	BinaryFound    bool   `json:"binary_found"`    // binary found in PATH
	BinaryPath     string `json:"binary_path,omitempty"` // resolved binary path
}

// ProbeAll checks every registered agent's local installation status.
func ProbeAll(registry map[string]AgentDefinition) []ProbeStatus {
	order := BuildDisplayOrder(registry)
	results := make([]ProbeStatus, 0, len(order))

	for _, name := range order {
		defn := registry[name]
		results = append(results, probeOne(defn))
	}
	return results
}

func probeOne(defn AgentDefinition) ProbeStatus {
	ps := ProbeStatus{Name: defn.Name}

	mcpPath := defn.MCPPath
	if mcpPath == "" {
		return ps
	}

	// Check if the parent directory exists (agent is "installed")
	parentDir := filepath.Dir(mcpPath)
	if info, err := os.Stat(parentDir); err == nil && info.IsDir() {
		ps.Installed = true
	}

	// Check if the config file itself exists
	if _, err := os.Stat(mcpPath); err == nil {
		ps.ConfigFound = true
	}

	// Check writability: if file exists check it; otherwise check parent dir
	if ps.ConfigFound {
		ps.ConfigWritable = isWritable(mcpPath)
	} else if ps.Installed {
		ps.ConfigWritable = isWritable(parentDir)
	}

	// Check if the binary is in PATH
	probeBinary(defn.BinaryNames, &ps)

	return ps
}

// probeBinary checks if any of the given binary names exist in PATH.
func probeBinary(names []string, ps *ProbeStatus) {
	for _, name := range names {
		path, err := exec.LookPath(name)
		if err == nil {
			ps.BinaryFound = true
			ps.BinaryPath = path
			return
		}
	}
}

func isWritable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	// Try to open for writing without truncating
	if info.IsDir() {
		// For dirs, try to create a temp file
		tmp := filepath.Join(path, ".agentctl_probe_tmp")
		f, err := os.Create(tmp)
		if err != nil {
			return false
		}
		f.Close()
		os.Remove(tmp)
		return true
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return false
	}
	f.Close()
	return true
}
