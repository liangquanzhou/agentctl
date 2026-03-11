package cli

import (
	"fmt"
	"os"
	"strings"

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
		Long: `Search for skills. By default searches the public skills.sh marketplace.
Use --source to search a private registry defined in skills/sources.json.
Use --source all to search all registered private sources.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			source, _ := cmd.Flags().GetString("source")
			query := args[0]

			// Private source search
			if source != "" {
				return searchPrivateSource(configDir, source, query)
			}

			// Public marketplace search (skills CLI)
			result, err := skills.Search(query)
			if err != nil {
				fmt.Println(red("skills search failed") + ": " + err.Error())
				os.Exit(1)
			}
			fmt.Println(result)
			return nil
		},
	}
	cmd.Flags().String("source", "", "Search a private registry (name from sources.json, or 'all')")
	return cmd
}

func searchPrivateSource(configDir, source, query string) error {
	cfg, err := skills.LoadSources(configDir)
	if err != nil {
		return err
	}
	if len(cfg.Registries) == 0 {
		fmt.Println(yellow("No registries configured") + " in skills/sources.json")
		return nil
	}

	// Determine which registries to search
	var toSearch map[string]skills.Registry
	if source == "all" {
		toSearch = cfg.Registries
	} else {
		reg, ok := cfg.Registries[source]
		if !ok {
			available := make([]string, 0, len(cfg.Registries))
			for name := range cfg.Registries {
				available = append(available, name)
			}
			return fmt.Errorf("registry %q not found (available: %s)", source, strings.Join(available, ", "))
		}
		toSearch = map[string]skills.Registry{source: reg}
	}

	totalResults := 0
	for name, reg := range toSearch {
		desc := ""
		if reg.Description != "" {
			desc = " (" + reg.Description + ")"
		}
		fmt.Printf("%s%s\n", bold(name), desc)

		results, err := skills.SearchSource(reg, name, query)
		if err != nil {
			fmt.Printf("  %s: %v\n\n", red("error"), err)
			continue
		}

		if len(results) == 0 {
			fmt.Println("  (no matching skills)")
		} else {
			for _, r := range results {
				desc := ""
				if r.Description != "" {
					desc = " - " + r.Description
				}
				fmt.Printf("  %s%s\n", green(r.Name), desc)
			}
		}
		fmt.Println()
		totalResults += len(results)
	}

	if totalResults > 0 {
		fmt.Println("Install with: agentctl skills add <registry-url>")
	}
	return nil
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
