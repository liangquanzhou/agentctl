// Package cli provides the cobra CLI command tree for agentctl.
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"agentctl/internal/agents"

	"github.com/spf13/cobra"
)

// ── Color helpers (ANSI) ─────────────────────────────────────────────

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBold   = "\033[1m"
)

func green(s string) string  { return colorGreen + s + colorReset }
func red(s string) string    { return colorRed + s + colorReset }
func yellow(s string) string { return colorYellow + s + colorReset }
func bold(s string) string   { return colorBold + s + colorReset }

// ── Table helpers ────────────────────────────────────────────────────

// PrintTable prints a formatted table to stdout.
func PrintTable(title string, headers []string, rows [][]string) {
	if title != "" {
		fmt.Println(bold("── " + title + " ──"))
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, strings.Join(headers, "\t"))
	for _, row := range rows {
		fmt.Fprintln(w, strings.Join(row, "\t"))
	}
	w.Flush()
	fmt.Println()
}

// ── Path helpers ─────────────────────────────────────────────────────

func homeDir() string {
	home, _ := os.UserHomeDir()
	return home
}

// DefaultConfigDir returns the default config directory.
func DefaultConfigDir() string { return filepath.Join(homeDir(), ".config", "agentctl") }

// DefaultSecretsDir returns the default secrets directory.
func DefaultSecretsDir() string { return filepath.Join(homeDir(), ".config", "agentctl", "secrets") }

// DefaultStateDir returns the default state directory.
func DefaultStateDir() string { return filepath.Join(homeDir(), ".config", "agentctl", "state") }

// DefaultLegacySource returns the default legacy config path.
func DefaultLegacySource() string { return filepath.Join(homeDir(), ".config", "mcp", "config.json") }

// DefaultSkillsSource returns the default skills source directory.
func DefaultSkillsSource() string { return filepath.Join(homeDir(), ".config", "agentctl", "skills") }

// DefaultSkillsTargets returns the default skills target directories.
func DefaultSkillsTargets() map[string]string {
	return agents.BuildSkillsTargets(agents.LoadAgentRegistry(""))
}

// AgentDisplayOrder returns agents sorted by display order.
func AgentDisplayOrder() []string {
	return agents.BuildDisplayOrder(agents.LoadAgentRegistry(""))
}

// ── JSON output ──────────────────────────────────────────────────────

// PrintJSON prints data as indented JSON to stdout.
func PrintJSON(data any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(data)
}

// ShortenPath replaces the home directory prefix with ~.
func ShortenPath(path string) string {
	home := homeDir()
	if strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

// ── Skills flag helpers ──────────────────────────────────────────────

// skillsTargetKeys returns the stable set of keys for skills dispatch:
// one key per agent that has a non-empty SkillsTarget. The key is the first
// alias if present, otherwise the canonical name. Deduplicated by path so
// canonical names with matching aliases don't double-sync.
func skillsTargetKeys() []string {
	registry := agents.LoadAgentRegistry("")
	seen := make(map[string]bool)
	var keys []string
	// Preserve display_order for stable flag/output ordering.
	for _, name := range agents.BuildDisplayOrder(registry) {
		defn := registry[name]
		if defn.SkillsTarget == "" {
			continue
		}
		key := defn.Name
		if len(defn.Aliases) > 0 {
			key = defn.Aliases[0]
		}
		if seen[defn.SkillsTarget] {
			continue
		}
		seen[defn.SkillsTarget] = true
		keys = append(keys, key)
	}
	return keys
}

func addSkillsFlags(cmd *cobra.Command) {
	cmd.Flags().String("source-dir", DefaultSkillsSource(), "Skills source dir")
	defaults := DefaultSkillsTargets()
	for _, key := range skillsTargetKeys() {
		flagName := key + "-dir"
		// Guard against duplicate flag registration (e.g. when keys collide).
		if cmd.Flags().Lookup(flagName) != nil {
			continue
		}
		cmd.Flags().String(flagName, defaults[key], key+" skills dir")
	}
}

func getSkillsSource(cmd *cobra.Command) string {
	s, _ := cmd.Flags().GetString("source-dir")
	return s
}

func getSkillsTargets(cmd *cobra.Command) map[string]string {
	targets := make(map[string]string)
	for _, key := range skillsTargetKeys() {
		val, _ := cmd.Flags().GetString(key + "-dir")
		if val != "" {
			targets[key] = val
		}
	}
	return targets
}
