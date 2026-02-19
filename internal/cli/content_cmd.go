package cli

import (
	"fmt"
	"os"

	"agentctl/internal/content"

	"github.com/spf13/cobra"
)

// ── content (deprecated group) ───────────────────────────────────────

func newContentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "content",
		Short: "Content plane commands (deprecated: use rules/hooks/commands/ignore)",
	}
	cmd.AddCommand(newContentPlanCmd())
	cmd.AddCommand(newContentApplyCmd())
	cmd.AddCommand(newContentStatusCmd())
	return cmd
}

func newContentPlanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Preview content changes",
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			output, _ := cmd.Flags().GetString("output")
			scope, _ := cmd.Flags().GetString("scope")
			projectDir, _ := cmd.Flags().GetString("project-dir")

			if scope == "project" && projectDir == "" {
				cwd, _ := os.Getwd()
				projectDir = cwd
			}

			data, err := content.ContentPlan(configDir, content.PlanOpts{
				Scope:      scope,
				ProjectDir: projectDir,
			})
			if err != nil {
				fmt.Println(red("content plan failed") + ": " + err.Error())
				os.Exit(1)
			}

			if output == "json" {
				PrintJSON(data)
				return nil
			}
			printContentPlanTable(data)
			return nil
		},
	}
	cmd.Flags().String("output", "text", "Output format: text|json")
	cmd.Flags().String("scope", "global", "Scope: global|project")
	cmd.Flags().String("project-dir", "", "Project directory for project scope")
	return cmd
}

func newContentApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply content changes",
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			stateDir, _ := cmd.Flags().GetString("state-dir")
			breakGlass, _ := cmd.Flags().GetBool("break-glass")
			reason, _ := cmd.Flags().GetString("reason")
			scope, _ := cmd.Flags().GetString("scope")
			projectDir, _ := cmd.Flags().GetString("project-dir")

			if scope == "project" && projectDir == "" {
				cwd, _ := os.Getwd()
				projectDir = cwd
			}

			manifest, err := content.ContentApply(configDir, stateDir, content.ApplyOpts{
				BreakGlass: breakGlass,
				Reason:     reason,
				Scope:      scope,
				ProjectDir: projectDir,
			})
			if err != nil {
				fmt.Println(red("content apply failed") + ": " + err.Error())
				os.Exit(1)
			}

			fmt.Printf("%s run_id=%s result=%s changed=%d\n",
				green("content apply finished"),
				manifest["run_id"], manifest["result"], countMapChangedFiles(manifest))
			return nil
		},
	}
	cmd.Flags().Bool("break-glass", false, "Emergency override")
	cmd.Flags().String("reason", "", "Reason for break-glass")
	cmd.Flags().String("scope", "global", "Scope: global|project")
	cmd.Flags().String("project-dir", "", "Project directory for project scope")
	return cmd
}

func newContentStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Check content drift",
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			data, err := content.ContentPlan(configDir, content.PlanOpts{})
			if err != nil {
				fmt.Println(red("content status failed") + ": " + err.Error())
				os.Exit(1)
			}
			printContentPlanTable(data)
			if changed := countMapChangedItems(data); changed > 0 {
				fmt.Printf("%s: %d item(s)\n", yellow("Drift detected"), changed)
				os.Exit(1)
			}
			fmt.Println(green("Healthy") + ": no content drift")
			return nil
		},
	}
}

// ── Content type sub-commands (rules/hooks/commands/ignore) ──────────

func newContentTypeCmd(typeName string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   typeName,
		Short: "Manage " + typeName,
	}

	// plan
	planCmd := &cobra.Command{
		Use:   "plan",
		Short: "Preview " + typeName + " changes",
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			output, _ := cmd.Flags().GetString("output")

			data, err := content.ContentPlan(configDir, content.PlanOpts{TypeFilter: typeName})
			if err != nil {
				fmt.Println(red(typeName+" plan failed") + ": " + err.Error())
				os.Exit(1)
			}

			if output == "json" {
				PrintJSON(data)
				return nil
			}
			printContentPlanTable(data)
			return nil
		},
	}
	planCmd.Flags().String("output", "text", "Output format: text|json")

	// apply
	applyCmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply " + typeName,
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			stateDir, _ := cmd.Flags().GetString("state-dir")
			breakGlass, _ := cmd.Flags().GetBool("break-glass")
			reason, _ := cmd.Flags().GetString("reason")

			manifest, err := content.ContentApply(configDir, stateDir, content.ApplyOpts{
				BreakGlass: breakGlass,
				Reason:     reason,
				TypeFilter: typeName,
			})
			if err != nil {
				fmt.Println(red(typeName+" apply failed") + ": " + err.Error())
				os.Exit(1)
			}

			fmt.Printf("%s run_id=%s result=%s changed=%d\n",
				green(typeName+" apply finished"),
				manifest["run_id"], manifest["result"], countMapChangedFiles(manifest))
			return nil
		},
	}
	applyCmd.Flags().Bool("break-glass", false, "Emergency override")
	applyCmd.Flags().String("reason", "", "Reason for break-glass")

	// status
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Check " + typeName + " drift",
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			data, err := content.ContentPlan(configDir, content.PlanOpts{TypeFilter: typeName})
			if err != nil {
				fmt.Println(red(typeName+" status failed") + ": " + err.Error())
				os.Exit(1)
			}
			printContentPlanTable(data)
			if changed := countMapChangedItems(data); changed > 0 {
				fmt.Printf("%s: %d %s item(s)\n", yellow("Drift detected"), changed, typeName)
				os.Exit(1)
			}
			fmt.Printf("%s: no %s drift\n", green("Healthy"), typeName)
			return nil
		},
	}

	cmd.AddCommand(planCmd, applyCmd, statusCmd)
	return cmd
}
