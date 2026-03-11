package cli

import (
	"fmt"
	"os"

	"agentctl/internal/skills"

	"github.com/spf13/cobra"
)

func newSkillsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Skills sync commands",
	}
	cmd.AddCommand(newSkillsListCmd())
	cmd.AddCommand(newSkillsStatusCmd())
	cmd.AddCommand(newSkillsSyncCmd())
	cmd.AddCommand(newSkillsApplyCmd())
	cmd.AddCommand(newSkillsPullCmd())
	cmd.AddCommand(newSkillsAddCmd())
	cmd.AddCommand(newSkillsSearchCmd())
	cmd.AddCommand(newSkillsRemoveCmd())
	return cmd
}

func newSkillsListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List source skills",
		RunE: func(cmd *cobra.Command, args []string) error {
			sourceDir := getSkillsSource(cmd)
			data := skills.SkillsList(sourceDir)

			headers := []string{"Source", "Count"}
			rows := [][]string{{fmt.Sprint(data["source_dir"]), fmt.Sprintf("%v", data["count"])}}
			PrintTable("Skills Source", headers, rows)

			if names, ok := data["skills"].([]string); ok {
				for _, name := range names {
					fmt.Println("- " + name)
				}
			}
			return nil
		},
	}
	addSkillsFlags(cmd)
	return cmd
}

func newSkillsStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Check sync status",
		RunE: func(cmd *cobra.Command, args []string) error {
			sourceDir := getSkillsSource(cmd)
			targets := getSkillsTargets(cmd)
			output, _ := cmd.Flags().GetString("output")
			targetFilter, _ := cmd.Flags().GetString("target")

			if targetFilter != "" {
				targets = filterSkillsTargets(targets, targetFilter)
			}

			data := skills.SkillsStatus(sourceDir, targets)

			if output == "json" {
				PrintJSON(data)
			} else {
				headers := []string{"Target", "Status", "Shared", "Local", "Unsynced", "Path"}
				var rows [][]string
				if targetRows, ok := data["targets"].([]map[string]any); ok {
					for _, row := range targetRows {
						rows = append(rows, []string{
							fmt.Sprint(row["target"]),
							fmt.Sprint(row["status"]),
							fmt.Sprintf("%v", row["shared"]),
							fmt.Sprintf("%v", row["local"]),
							fmt.Sprintf("%v", row["unsynced"]),
							fmt.Sprint(row["path"]),
						})
					}
				}
				PrintTable("Skills Status", headers, rows)
				fmt.Printf("unsynced_total=%v\n", data["unsynced_total"])
			}

			if total, ok := data["unsynced_total"].(int); ok && total > 0 {
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().String("output", "text", "Output format: text|json")
	cmd.Flags().String("target", "", "Filter to specific target (claude|codex|gemini|antigravity|opencode)")
	addSkillsFlags(cmd)
	return cmd
}

func newSkillsSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync skills to targets",
		RunE:  runSkillsSync,
	}
	cmd.Flags().Bool("dry-run", false, "Dry run mode")
	cmd.Flags().String("output", "text", "Output format: text|json")
	cmd.Flags().String("target", "", "Filter to specific target (claude|codex|gemini|antigravity|opencode)")
	addSkillsFlags(cmd)
	return cmd
}

func runSkillsSync(cmd *cobra.Command, args []string) error {
	sourceDir := getSkillsSource(cmd)
	stateDir, _ := cmd.Flags().GetString("state-dir")
	targets := getSkillsTargets(cmd)
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	output, _ := cmd.Flags().GetString("output")
	targetFilter, _ := cmd.Flags().GetString("target")

	if targetFilter != "" {
		targets = filterSkillsTargets(targets, targetFilter)
	}

	data := skills.SkillsSync(sourceDir, targets, stateDir, dryRun)

	if output == "json" {
		PrintJSON(data)
		return nil
	}

	headers := []string{"Target", "Created", "Updated", "Removed", "Unchanged", "Unsynced", "Status"}
	var rows [][]string
	if targetRows, ok := data["targets"].([]map[string]any); ok {
		for _, row := range targetRows {
			rows = append(rows, []string{
				fmt.Sprint(row["target"]),
				fmt.Sprintf("%v", row["created"]),
				fmt.Sprintf("%v", row["updated"]),
				fmt.Sprintf("%v", row["removed"]),
				fmt.Sprintf("%v", row["unchanged"]),
				fmt.Sprintf("%v", row["unsynced"]),
				fmt.Sprint(row["status"]),
			})
		}
	}
	PrintTable("Skills Sync", headers, rows)
	fmt.Printf("actions=%v dry_run=%v\n", data["actions"], data["dry_run"])
	return nil
}

func newSkillsApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Alias for 'skills sync'",
		RunE:  runSkillsSync,
	}
	cmd.Flags().Bool("dry-run", false, "Dry run mode")
	cmd.Flags().String("output", "text", "Output format: text|json")
	cmd.Flags().String("target", "", "Filter to specific target (claude|codex|gemini|antigravity|opencode)")
	addSkillsFlags(cmd)
	return cmd
}

func newSkillsPullCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Pull skills from target",
		RunE: func(cmd *cobra.Command, args []string) error {
			sourceDir := getSkillsSource(cmd)
			target, _ := cmd.Flags().GetString("target")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			overwrite, _ := cmd.Flags().GetBool("overwrite")
			output, _ := cmd.Flags().GetString("output")

			targets := getSkillsTargets(cmd)
			targetDir, ok := targets[target]
			if !ok {
				fmt.Println(red("invalid target") + ": " + target)
				os.Exit(1)
			}

			data, err := skills.SkillsPull(sourceDir, target, targetDir, dryRun, overwrite)
			if err != nil {
				return err
			}

			if output == "json" {
				PrintJSON(data)
				return nil
			}

			headers := []string{"Target", "Created", "Updated", "Skipped"}
			rows := [][]string{{
				fmt.Sprint(data["target"]),
				fmt.Sprintf("%v", data["created"]),
				fmt.Sprintf("%v", data["updated"]),
				fmt.Sprintf("%v", data["skipped"]),
			}}
			PrintTable("Skills Pull", headers, rows)
			fmt.Printf("dry_run=%v overwrite=%v\n", data["dry_run"], overwrite)
			return nil
		},
	}
	cmd.Flags().String("target", "", "Target agent (claude|codex|gemini|antigravity|opencode)")
	cmd.MarkFlagRequired("target")
	cmd.Flags().Bool("dry-run", false, "Dry run mode")
	cmd.Flags().Bool("overwrite", false, "Overwrite existing skills")
	cmd.Flags().String("output", "text", "Output format: text|json")
	addSkillsFlags(cmd)
	return cmd
}

// filterSkillsTargets returns a subset of targets matching the given target name.
func filterSkillsTargets(targets map[string]string, target string) map[string]string {
	if dir, ok := targets[target]; ok {
		return map[string]string{target: dir}
	}
	return map[string]string{}
}
