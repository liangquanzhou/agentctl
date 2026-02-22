package cli

import (
	"fmt"
	"os"
	"strings"

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
			changedOnly, _ := cmd.Flags().GetBool("changed-only")

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

			if changedOnly {
				data = filterContentChanged(data)
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
	cmd.Flags().Bool("changed-only", false, "Only show changed items")
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

			if breakGlass && strings.TrimSpace(reason) == "" {
				fmt.Println(red("--break-glass requires --reason"))
				os.Exit(1)
			}

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
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Check content drift",
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			changedOnly, _ := cmd.Flags().GetBool("changed-only")

			data, err := content.ContentPlan(configDir, content.PlanOpts{})
			if err != nil {
				fmt.Println(red("content status failed") + ": " + err.Error())
				os.Exit(1)
			}

			if changedOnly {
				data = filterContentChanged(data)
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
	cmd.Flags().Bool("changed-only", false, "Only show changed items")
	return cmd
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
			changedOnly, _ := cmd.Flags().GetBool("changed-only")
			agentFilter, _ := cmd.Flags().GetString("agent")
			scope, _ := cmd.Flags().GetString("scope")
			projectDir, _ := cmd.Flags().GetString("project-dir")

			if scope == "project" && projectDir == "" {
				cwd, _ := os.Getwd()
				projectDir = cwd
			}

			data, err := content.ContentPlan(configDir, content.PlanOpts{
				TypeFilter: typeName,
				Scope:      scope,
				ProjectDir: projectDir,
			})
			if err != nil {
				fmt.Println(red(typeName+" plan failed") + ": " + err.Error())
				os.Exit(1)
			}

			if agentFilter != "" {
				data = filterContentByAgent(data, agentFilter)
			}

			if changedOnly {
				data = filterContentChanged(data)
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
	planCmd.Flags().Bool("changed-only", false, "Only show changed items")
	planCmd.Flags().String("agent", "", "Filter to specific agent")
	planCmd.Flags().String("scope", "global", "Scope: global|project")
	planCmd.Flags().String("project-dir", "", "Project directory for project scope")

	// apply
	applyCmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply " + typeName,
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			stateDir, _ := cmd.Flags().GetString("state-dir")
			breakGlass, _ := cmd.Flags().GetBool("break-glass")
			reason, _ := cmd.Flags().GetString("reason")
			agentFilter, _ := cmd.Flags().GetString("agent")
			scope, _ := cmd.Flags().GetString("scope")
			projectDir, _ := cmd.Flags().GetString("project-dir")

			if breakGlass && strings.TrimSpace(reason) == "" {
				fmt.Println(red("--break-glass requires --reason"))
				os.Exit(1)
			}

			if scope == "project" && projectDir == "" {
				cwd, _ := os.Getwd()
				projectDir = cwd
			}

			// When --agent is set, run plan first to show what will change for that agent,
			// then apply all (content apply does not support per-agent filtering).
			// The apply itself targets the type filter, which is the finest granularity.
			if agentFilter != "" {
				planData, err := content.ContentPlan(configDir, content.PlanOpts{
					TypeFilter: typeName,
					Scope:      scope,
					ProjectDir: projectDir,
				})
				if err != nil {
					fmt.Println(red(typeName+" plan failed") + ": " + err.Error())
					os.Exit(1)
				}
				filtered := filterContentByAgent(planData, agentFilter)
				if countMapChangedItems(filtered) == 0 {
					fmt.Printf("%s: no %s changes for agent %s\n", green("no changes"), typeName, agentFilter)
					return nil
				}
			}

			manifest, err := content.ContentApply(configDir, stateDir, content.ApplyOpts{
				BreakGlass: breakGlass,
				Reason:     reason,
				TypeFilter: typeName,
				Scope:      scope,
				ProjectDir: projectDir,
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
	applyCmd.Flags().String("agent", "", "Filter to specific agent")
	applyCmd.Flags().String("scope", "global", "Scope: global|project")
	applyCmd.Flags().String("project-dir", "", "Project directory for project scope")

	// status
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Check " + typeName + " drift",
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			changedOnly, _ := cmd.Flags().GetBool("changed-only")
			agentFilter, _ := cmd.Flags().GetString("agent")
			scope, _ := cmd.Flags().GetString("scope")
			projectDir, _ := cmd.Flags().GetString("project-dir")

			if scope == "project" && projectDir == "" {
				cwd, _ := os.Getwd()
				projectDir = cwd
			}

			data, err := content.ContentPlan(configDir, content.PlanOpts{
				TypeFilter: typeName,
				Scope:      scope,
				ProjectDir: projectDir,
			})
			if err != nil {
				fmt.Println(red(typeName+" status failed") + ": " + err.Error())
				os.Exit(1)
			}

			if agentFilter != "" {
				data = filterContentByAgent(data, agentFilter)
			}

			if changedOnly {
				data = filterContentChanged(data)
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
	statusCmd.Flags().Bool("changed-only", false, "Only show changed items")
	statusCmd.Flags().String("agent", "", "Filter to specific agent")
	statusCmd.Flags().String("scope", "global", "Scope: global|project")
	statusCmd.Flags().String("project-dir", "", "Project directory for project scope")

	cmd.AddCommand(planCmd, applyCmd, statusCmd)

	// Add CRUD sub-commands per content type
	switch typeName {
	case "rules":
		cmd.AddCommand(newRulesListCmd(), newRulesAddCmd(), newRulesRmCmd())
	case "hooks":
		cmd.AddCommand(newHooksListCmd(), newHooksAddCmd(), newHooksRmCmd())
	case "commands":
		cmd.AddCommand(newCommandsListCmd(), newCommandsAddCmd(), newCommandsRmCmd())
	case "ignore":
		cmd.AddCommand(newIgnoreListCmd(), newIgnoreAddCmd(), newIgnoreRmCmd())
	}

	return cmd
}

// filterContentByAgent returns a copy of the plan data with only items matching the agent.
func filterContentByAgent(data map[string]any, agentName string) map[string]any {
	resolved := resolveAgentName(agentName)
	items, ok := data["items"].([]map[string]any)
	if !ok {
		return data
	}
	var filtered []map[string]any
	for _, item := range items {
		if a, _ := item["agent"].(string); a == resolved {
			filtered = append(filtered, item)
		}
	}
	if filtered == nil {
		filtered = []map[string]any{}
	}
	result := make(map[string]any, len(data))
	for k, v := range data {
		result[k] = v
	}
	result["items"] = filtered
	return result
}

// filterContentChanged returns a copy of the plan data with only changed items.
func filterContentChanged(data map[string]any) map[string]any {
	items, ok := data["items"].([]map[string]any)
	if !ok {
		return data
	}
	var filtered []map[string]any
	for _, item := range items {
		if c, _ := item["changed"].(bool); c {
			filtered = append(filtered, item)
		}
	}
	if filtered == nil {
		filtered = []map[string]any{}
	}
	result := make(map[string]any, len(data))
	for k, v := range data {
		result[k] = v
	}
	result["items"] = filtered
	return result
}
