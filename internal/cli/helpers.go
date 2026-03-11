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

func addSkillsFlags(cmd *cobra.Command) {
	cmd.Flags().String("source-dir", DefaultSkillsSource(), "Skills source dir")
	defaults := DefaultSkillsTargets()
	cmd.Flags().String("claude-dir", defaults["claude"], "Claude skills dir")
	cmd.Flags().String("codex-dir", defaults["codex"], "Codex skills dir")
	cmd.Flags().String("gemini-dir", defaults["gemini"], "Gemini skills dir")
	cmd.Flags().String("antigravity-dir", defaults["antigravity"], "Antigravity skills dir")
	cmd.Flags().String("opencode-dir", defaults["opencode"], "OpenCode skills dir")
	cmd.Flags().String("openclaw-dir", defaults["openclaw"], "OpenClaw skills dir")
}

func getSkillsSource(cmd *cobra.Command) string {
	s, _ := cmd.Flags().GetString("source-dir")
	return s
}

func getSkillsTargets(cmd *cobra.Command) map[string]string {
	targets := make(map[string]string)
	for _, name := range []string{"claude", "codex", "gemini", "antigravity", "opencode", "openclaw"} {
		val, _ := cmd.Flags().GetString(name + "-dir")
		if val != "" {
			targets[name] = val
		}
	}
	return targets
}
