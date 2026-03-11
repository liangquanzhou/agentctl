package cli

import (
	"fmt"
	"os"

	"agentctl/internal/skills"

	"github.com/spf13/cobra"
)

func newSkillsAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <source>",
		Short: "Download and install a skill from GitHub",
		Long: `Download a skill from a GitHub repository and install it into the
agentctl skills source directory. After installation, automatically
runs skills sync to distribute to all agent targets.

Source can be:
  - Full URL:    https://github.com/user/repo
  - Short URL:   github.com/user/repo
  - Shorthand:   user/repo
  - SSH URL:     git@github.com:user/repo`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			stateDir, _ := cmd.Flags().GetString("state-dir")
			all, _ := cmd.Flags().GetBool("all")
			noSync, _ := cmd.Flags().GetBool("no-sync")
			output, _ := cmd.Flags().GetString("output")

			source := args[0]

			fmt.Fprintf(os.Stderr, "Downloading skill from %s ...\n", source)

			installed, err := skills.Download(source, configDir, all)
			if err != nil {
				fmt.Println(red("skills add failed") + ": " + err.Error())
				os.Exit(1)
			}

			if output == "json" {
				result := map[string]any{
					"source":    source,
					"installed": skillMetasToMaps(installed),
					"synced":    false,
				}
				if !noSync {
					syncData := runSyncAfterAdd(cmd, stateDir)
					result["synced"] = true
					result["sync_actions"] = syncData["actions"]
				}
				PrintJSON(result)
				return nil
			}

			// Text output
			fmt.Printf("%s installed %d skill(s):\n", green("OK"), len(installed))
			for _, m := range installed {
				desc := ""
				if m.Description != "" {
					desc = " - " + m.Description
				}
				fmt.Printf("  %s%s\n", bold(m.Name), desc)
				fmt.Printf("    %s\n", ShortenPath(m.LocalPath))
			}

			if !noSync {
				fmt.Println()
				syncData := runSyncAfterAdd(cmd, stateDir)
				fmt.Printf("%s actions=%v\n", green("skills sync"), syncData["actions"])
			}

			return nil
		},
	}
	cmd.Flags().Bool("all", false, "Install all skills from a multi-skill repo")
	cmd.Flags().Bool("no-sync", false, "Download only, do not sync to agents")
	cmd.Flags().String("output", "text", "Output format: text|json")
	addSkillsFlags(cmd)
	return cmd
}

func newSkillsSearchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search for skills",
		Long: `Search for skills using the 'skills' CLI tool if installed,
otherwise shows instructions for manual search.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := skills.Search(args[0])
			if err != nil {
				fmt.Println(red("skills search failed") + ": " + err.Error())
				os.Exit(1)
			}
			fmt.Println(result)
			return nil
		},
	}
	return cmd
}

func newSkillsRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a skill and sync",
		Long:  `Remove a skill from the agentctl skills source directory and re-sync all agents.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			stateDir, _ := cmd.Flags().GetString("state-dir")
			noSync, _ := cmd.Flags().GetBool("no-sync")
			output, _ := cmd.Flags().GetString("output")

			name := args[0]

			if err := skills.Remove(name, configDir); err != nil {
				fmt.Println(red("skills remove failed") + ": " + err.Error())
				os.Exit(1)
			}

			if output == "json" {
				result := map[string]any{
					"removed": name,
					"synced":  false,
				}
				if !noSync {
					syncData := runSyncAfterAdd(cmd, stateDir)
					result["synced"] = true
					result["sync_actions"] = syncData["actions"]
				}
				PrintJSON(result)
				return nil
			}

			fmt.Printf("%s removed skill %s\n", yellow("OK"), bold(name))

			if !noSync {
				syncData := runSyncAfterAdd(cmd, stateDir)
				fmt.Printf("%s actions=%v\n", green("skills sync"), syncData["actions"])
			}

			return nil
		},
	}
	cmd.Flags().Bool("no-sync", false, "Remove only, do not sync to agents")
	cmd.Flags().String("output", "text", "Output format: text|json")
	addSkillsFlags(cmd)
	return cmd
}

// ── Helpers ──────────────────────────────────────────────────────────

// runSyncAfterAdd performs a skills sync using the command's flags.
func runSyncAfterAdd(cmd *cobra.Command, stateDir string) map[string]any {
	sourceDir := getSkillsSource(cmd)
	targets := getSkillsTargets(cmd)
	return skills.SkillsSync(sourceDir, targets, stateDir, false)
}

// skillMetasToMaps converts a slice of SkillMeta to a slice of maps for JSON output.
func skillMetasToMaps(metas []skills.SkillMeta) []map[string]any {
	result := make([]map[string]any, len(metas))
	for i, m := range metas {
		result[i] = map[string]any{
			"name":        m.Name,
			"description": m.Description,
			"source_url":  m.SourceURL,
			"local_path":  m.LocalPath,
		}
	}
	return result
}
