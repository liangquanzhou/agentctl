package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestRootCommandStructure verifies the root command has all expected subcommands.
func TestRootCommandStructure(t *testing.T) {
	root := NewRootCmd("test")

	expected := []string{
		"init", "validate", "apply", "status", "drift", "reconcile",
		"rollback", "doctor", "runs", "migrate", "stageb",
		"mcp", "skills", "agents", "content",
		"rules", "hooks", "commands", "ignore",
	}

	cmdMap := make(map[string]bool)
	for _, sub := range root.Commands() {
		cmdMap[sub.Name()] = true
	}

	for _, name := range expected {
		t.Run(name, func(t *testing.T) {
			if !cmdMap[name] {
				t.Errorf("expected subcommand %q not found on root", name)
			}
		})
	}
}

// TestRootPersistentFlags verifies root has the expected persistent flags.
func TestRootPersistentFlags(t *testing.T) {
	root := NewRootCmd("test")

	flags := []string{"config-dir", "secrets-dir", "state-dir"}
	for _, name := range flags {
		t.Run(name, func(t *testing.T) {
			f := root.PersistentFlags().Lookup(name)
			if f == nil {
				t.Errorf("persistent flag %q not found", name)
			}
		})
	}
}

// TestMCPSubcommands verifies mcp has all expected subcommands.
func TestMCPSubcommands(t *testing.T) {
	root := NewRootCmd("test")

	var mcpCmd *cobra.Command
	for _, sub := range root.Commands() {
		if sub.Name() == "mcp" {
			mcpCmd = sub
			break
		}
	}
	if mcpCmd == nil {
		t.Fatal("mcp command not found")
	}

	expected := []string{"plan", "status", "apply", "list", "add", "rm", "check"}

	cmdMap := make(map[string]bool)
	for _, sub := range mcpCmd.Commands() {
		cmdMap[sub.Name()] = true
	}

	for _, name := range expected {
		t.Run(name, func(t *testing.T) {
			if !cmdMap[name] {
				t.Errorf("expected mcp subcommand %q not found", name)
			}
		})
	}
}

// TestSkillsSubcommands verifies skills has all expected subcommands.
func TestSkillsSubcommands(t *testing.T) {
	root := NewRootCmd("test")

	var skillsCmd *cobra.Command
	for _, sub := range root.Commands() {
		if sub.Name() == "skills" {
			skillsCmd = sub
			break
		}
	}
	if skillsCmd == nil {
		t.Fatal("skills command not found")
	}

	expected := []string{"list", "status", "sync", "apply", "pull", "add", "search", "remove"}

	cmdMap := make(map[string]bool)
	for _, sub := range skillsCmd.Commands() {
		cmdMap[sub.Name()] = true
	}

	for _, name := range expected {
		t.Run(name, func(t *testing.T) {
			if !cmdMap[name] {
				t.Errorf("expected skills subcommand %q not found", name)
			}
		})
	}
}

// TestContentTypeSubcommands verifies rules/hooks/commands/ignore each have
// the expected subcommands: plan, apply, status, list, add, rm.
func TestContentTypeSubcommands(t *testing.T) {
	root := NewRootCmd("test")

	contentTypes := []string{"rules", "hooks", "commands", "ignore"}
	expectedSubs := []string{"plan", "apply", "status", "list", "add", "rm"}

	for _, typeName := range contentTypes {
		t.Run(typeName, func(t *testing.T) {
			var typeCmd *cobra.Command
			for _, sub := range root.Commands() {
				if sub.Name() == typeName {
					typeCmd = sub
					break
				}
			}
			if typeCmd == nil {
				t.Fatalf("%s command not found", typeName)
			}

			cmdMap := make(map[string]bool)
			for _, sub := range typeCmd.Commands() {
				cmdMap[sub.Name()] = true
			}

			for _, name := range expectedSubs {
				t.Run(name, func(t *testing.T) {
					if !cmdMap[name] {
						t.Errorf("expected %s subcommand %q not found", typeName, name)
					}
				})
			}
		})
	}
}

// TestApplyFlags verifies the apply command has the expected flags.
func TestApplyFlags(t *testing.T) {
	root := NewRootCmd("test")

	var applyCmd *cobra.Command
	for _, sub := range root.Commands() {
		if sub.Name() == "apply" {
			applyCmd = sub
			break
		}
	}
	if applyCmd == nil {
		t.Fatal("apply command not found")
	}

	expected := []string{
		"break-glass", "reason", "best-effort",
		"source-dir",
		"claude-dir", "codex-dir", "gemini-dir",
		"antigravity-dir", "opencode-dir", "openclaw-dir", "trae-cn-dir",
	}

	for _, name := range expected {
		t.Run(name, func(t *testing.T) {
			f := applyCmd.Flags().Lookup(name)
			if f == nil {
				t.Errorf("expected apply flag %q not found", name)
			}
		})
	}
}

// TestRollbackFlags verifies the rollback command has the expected flags,
// and that "run" is marked as required.
func TestRollbackFlags(t *testing.T) {
	root := NewRootCmd("test")

	var rollbackCmd *cobra.Command
	for _, sub := range root.Commands() {
		if sub.Name() == "rollback" {
			rollbackCmd = sub
			break
		}
	}
	if rollbackCmd == nil {
		t.Fatal("rollback command not found")
	}

	t.Run("run", func(t *testing.T) {
		f := rollbackCmd.Flags().Lookup("run")
		if f == nil {
			t.Fatal("expected rollback flag \"run\" not found")
		}
		// Verify it is required by checking annotations
		if ann := f.Annotations; ann == nil {
			t.Error("expected \"run\" flag to have annotations (required)")
		} else if _, ok := ann["cobra_annotation_bash_completion_one_required_flag"]; !ok {
			t.Error("expected \"run\" flag to be marked as required")
		}
	})

	t.Run("agent", func(t *testing.T) {
		f := rollbackCmd.Flags().Lookup("agent")
		if f == nil {
			t.Error("expected rollback flag \"agent\" not found")
		}
	})
}

// TestHelperFunctions tests ShortenPath and color helpers.
func TestHelperFunctions(t *testing.T) {
	t.Run("ShortenPath/home_prefix", func(t *testing.T) {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skip("cannot determine home dir")
		}
		input := filepath.Join(home, ".config", "agentctl")
		got := ShortenPath(input)
		expected := "~/.config/agentctl"
		if got != expected {
			t.Errorf("ShortenPath(%q) = %q, want %q", input, got, expected)
		}
	})

	t.Run("ShortenPath/no_home_prefix", func(t *testing.T) {
		input := "/tmp/something"
		got := ShortenPath(input)
		if got != input {
			t.Errorf("ShortenPath(%q) = %q, want unchanged", input, got)
		}
	})

	t.Run("green", func(t *testing.T) {
		result := green("OK")
		if !strings.Contains(result, "OK") {
			t.Error("green() should contain the input string")
		}
		if !strings.HasPrefix(result, colorGreen) {
			t.Error("green() should start with green ANSI code")
		}
		if !strings.HasSuffix(result, colorReset) {
			t.Error("green() should end with reset ANSI code")
		}
	})

	t.Run("red", func(t *testing.T) {
		result := red("FAIL")
		if !strings.Contains(result, "FAIL") {
			t.Error("red() should contain the input string")
		}
		if !strings.HasPrefix(result, colorRed) {
			t.Error("red() should start with red ANSI code")
		}
		if !strings.HasSuffix(result, colorReset) {
			t.Error("red() should end with reset ANSI code")
		}
	})

	t.Run("yellow", func(t *testing.T) {
		result := yellow("WARN")
		if !strings.Contains(result, "WARN") {
			t.Error("yellow() should contain the input string")
		}
		if !strings.HasPrefix(result, colorYellow) {
			t.Error("yellow() should start with yellow ANSI code")
		}
		if !strings.HasSuffix(result, colorReset) {
			t.Error("yellow() should end with reset ANSI code")
		}
	})

	t.Run("bold", func(t *testing.T) {
		result := bold("Title")
		if !strings.Contains(result, "Title") {
			t.Error("bold() should contain the input string")
		}
		if !strings.HasPrefix(result, colorBold) {
			t.Error("bold() should start with bold ANSI code")
		}
		if !strings.HasSuffix(result, colorReset) {
			t.Error("bold() should end with reset ANSI code")
		}
	})
}

// TestDefaultPaths verifies the default path functions return sane values.
func TestDefaultPaths(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir")
	}

	t.Run("DefaultConfigDir", func(t *testing.T) {
		got := DefaultConfigDir()
		want := filepath.Join(home, ".config", "agentctl")
		if got != want {
			t.Errorf("DefaultConfigDir() = %q, want %q", got, want)
		}
	})

	t.Run("DefaultSecretsDir", func(t *testing.T) {
		got := DefaultSecretsDir()
		want := filepath.Join(home, ".config", "agentctl", "secrets")
		if got != want {
			t.Errorf("DefaultSecretsDir() = %q, want %q", got, want)
		}
	})

	t.Run("DefaultStateDir", func(t *testing.T) {
		got := DefaultStateDir()
		want := filepath.Join(home, ".config", "agentctl", "state")
		if got != want {
			t.Errorf("DefaultStateDir() = %q, want %q", got, want)
		}
	})
}
