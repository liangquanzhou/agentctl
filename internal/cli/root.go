package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agentctl/internal/content"
	"agentctl/internal/engine"
	"agentctl/internal/skills"
	"agentctl/internal/validate"

	"github.com/spf13/cobra"
)

// NewRootCmd creates the root cobra command.
func NewRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:     "agentctl",
		Short:   "agentctl - multi-agent control plane CLI",
		Version: version,
	}

	root.PersistentFlags().String("config-dir", DefaultConfigDir(), "Config directory")
	root.PersistentFlags().String("secrets-dir", DefaultSecretsDir(), "Secrets directory")
	root.PersistentFlags().String("state-dir", DefaultStateDir(), "State directory")

	root.AddCommand(newValidateCmd())
	root.AddCommand(newApplyAllCmd())
	root.AddCommand(newStatusAllCmd())
	root.AddCommand(newDriftCmd())
	root.AddCommand(newReconcileCmd())
	root.AddCommand(newRollbackCmd())
	root.AddCommand(newDoctorCmd())
	root.AddCommand(newRunsCmd())
	root.AddCommand(newMigrateCmd())
	root.AddCommand(newStagebCmd())
	root.AddCommand(newMCPCmd())
	root.AddCommand(newSkillsCmd())
	root.AddCommand(newContentCmd())

	for _, typeName := range []string{"rules", "hooks", "commands", "ignore"} {
		root.AddCommand(newContentTypeCmd(typeName))
	}

	return root
}

// ── validate ─────────────────────────────────────────────────────────

func newValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate config",
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			ok, errors := validate.ValidateConfig(configDir)
			if ok {
				fmt.Println(green("OK") + " config valid: " + configDir)
				return nil
			}
			fmt.Println(red("FAILED") + " config invalid: " + configDir)
			for _, e := range errors {
				fmt.Println("- " + e)
			}
			os.Exit(1)
			return nil
		},
	}
}

// ── apply all ────────────────────────────────────────────────────────

func newApplyAllCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply all: MCP + rules + hooks + commands + ignore + skills",
		RunE:  runApplyAll,
	}
	cmd.Flags().Bool("break-glass", false, "Emergency override")
	cmd.Flags().String("reason", "", "Reason for break-glass")
	cmd.Flags().Bool("best-effort", false, "Continue on failure")
	addSkillsFlags(cmd)
	return cmd
}

func runApplyAll(cmd *cobra.Command, args []string) error {
	configDir, _ := cmd.Flags().GetString("config-dir")
	secretsDir, _ := cmd.Flags().GetString("secrets-dir")
	stateDir, _ := cmd.Flags().GetString("state-dir")
	breakGlass, _ := cmd.Flags().GetBool("break-glass")
	reason, _ := cmd.Flags().GetString("reason")
	bestEffort, _ := cmd.Flags().GetBool("best-effort")

	if breakGlass && strings.TrimSpace(reason) == "" {
		fmt.Println(red("--break-glass requires --reason"))
		os.Exit(1)
	}

	var errs []string

	// 1. MCP apply
	mcpResult, err := engine.Apply(configDir, secretsDir, stateDir, breakGlass, reason, os.Getenv("USER"))
	if err != nil {
		fmt.Println(red("mcp apply failed") + ": " + err.Error())
		if !bestEffort {
			os.Exit(1)
		}
		errs = append(errs, "mcp: "+err.Error())
	} else {
		fmt.Printf("%s run_id=%s changed=%d\n", green("mcp apply"), mcpResult.RunID, len(mcpResult.ChangedFiles))
	}

	// 2. Content apply
	contentManifest, err := content.ContentApply(configDir, stateDir, content.ApplyOpts{
		BreakGlass: breakGlass, Reason: reason,
	})
	if err != nil {
		fmt.Println(red("content apply failed") + ": " + err.Error())
		if !bestEffort {
			os.Exit(1)
		}
		errs = append(errs, "content: "+err.Error())
	} else {
		changed := countMapChangedFiles(contentManifest)
		fmt.Printf("%s run_id=%s changed=%d\n", green("content apply"), contentManifest["run_id"], changed)
	}

	// 3. Skills sync (H5: errors propagated, not silently ignored)
	targets := getSkillsTargets(cmd)
	skillsData := skills.SkillsSync(getSkillsSource(cmd), targets, stateDir, false)
	if skillsErrors, ok := skillsData["errors"]; ok {
		if errList, ok := skillsErrors.([]string); ok && len(errList) > 0 {
			fmt.Println(red("skills sync had errors") + ":")
			for _, e := range errList {
				fmt.Println("  - " + e)
			}
			if !bestEffort {
				os.Exit(1)
			}
			errs = append(errs, "skills: "+strings.Join(errList, "; "))
		}
	}
	fmt.Printf("%s actions=%v\n", green("skills sync"), skillsData["actions"])

	if len(errs) > 0 {
		fmt.Printf("%s apply finished with %d error(s)\n", red("FAIL"), len(errs))
		os.Exit(1)
	}
	fmt.Println(green("All applied"))
	return nil
}

// ── status all ───────────────────────────────────────────────────────

func newStatusAllCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Status of all subsystems",
		RunE:  runStatusAll,
	}
	cmd.Flags().String("output", "text", "Output format: text|json")
	addSkillsFlags(cmd)
	return cmd
}

func runStatusAll(cmd *cobra.Command, args []string) error {
	configDir, _ := cmd.Flags().GetString("config-dir")
	secretsDir, _ := cmd.Flags().GetString("secrets-dir")
	output, _ := cmd.Flags().GetString("output")
	hasDrift := false

	// 1. MCP
	mcpResult, mcpErr := engine.Plan(configDir, secretsDir)
	mcpDrift := 0
	if mcpErr == nil {
		for _, a := range mcpResult.Agents {
			if a.Changed {
				mcpDrift++
			}
		}
		if mcpDrift > 0 {
			hasDrift = true
		}
	} else {
		hasDrift = true
	}

	// 2. Content
	contentData, contentErr := content.ContentPlan(configDir, content.PlanOpts{})
	contentDrift := 0
	if contentErr == nil {
		contentDrift = countMapChangedItems(contentData)
		if contentDrift > 0 {
			hasDrift = true
		}
	} else {
		hasDrift = true
	}

	// 3. Skills
	targets := getSkillsTargets(cmd)
	skillsData := skills.SkillsStatus(getSkillsSource(cmd), targets)
	skillsDrift := 0
	if total, ok := skillsData["unsynced_total"].(int); ok {
		skillsDrift = total
		if total > 0 {
			hasDrift = true
		}
	}

	if output == "json" {
		result := map[string]any{
			"mcp":     map[string]any{"drift": mcpDrift, "error": errStr(mcpErr)},
			"content": map[string]any{"drift": contentDrift, "error": errStr(contentErr)},
			"skills":  map[string]any{"drift": skillsDrift},
			"healthy": !hasDrift,
		}
		PrintJSON(result)
		if hasDrift {
			os.Exit(1)
		}
		return nil
	}

	// Text output
	fmt.Println(bold("── MCP ──"))
	if mcpErr != nil {
		fmt.Println(red("mcp status failed") + ": " + mcpErr.Error())
	} else {
		printEnginePlanTable(mcpResult)
		if mcpDrift > 0 {
			fmt.Printf("%s: %d agent(s)\n", yellow("MCP drift"), mcpDrift)
		}
	}

	fmt.Println(bold("\n── Content (rules/hooks/commands/ignore) ──"))
	if contentErr != nil {
		fmt.Println(red("content status failed") + ": " + contentErr.Error())
	} else {
		printContentPlanTable(contentData)
		if contentDrift > 0 {
			fmt.Printf("%s: %d item(s)\n", yellow("Content drift"), contentDrift)
		}
	}

	fmt.Println(bold("\n── Skills ──"))
	if skillsDrift > 0 {
		fmt.Printf("%s: %d unsynced\n", yellow("Skills drift"), skillsDrift)
	} else {
		fmt.Println(green("Skills: in sync"))
	}

	if hasDrift {
		fmt.Println("\n" + yellow("Drift detected") + " — run `agentctl apply` to fix")
		os.Exit(1)
	}
	fmt.Println("\n" + green("Healthy") + ": no drift across all subsystems")
	return nil
}

// errStr returns "" for nil errors, otherwise the error message.
func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// ── drift ────────────────────────────────────────────────────────────

func newDriftCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "drift",
		Short: "Alias for 'mcp status'",
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			secretsDir, _ := cmd.Flags().GetString("secrets-dir")
			return runEngineMCPStatus(configDir, secretsDir)
		},
	}
}

// ── reconcile ────────────────────────────────────────────────────────

func newReconcileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reconcile",
		Short: "Reconcile MCP drift",
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			secretsDir, _ := cmd.Flags().GetString("secrets-dir")
			stateDir, _ := cmd.Flags().GetString("state-dir")
			fix, _ := cmd.Flags().GetBool("fix")

			if fix {
				result, err := engine.Apply(configDir, secretsDir, stateDir, false, "", os.Getenv("USER"))
				if err != nil {
					return err
				}
				fmt.Printf("%s run_id=%s changed=%d\n",
					green("reconcile fixed"), result.RunID, len(result.ChangedFiles))
				return nil
			}

			planResult, err := engine.Plan(configDir, secretsDir)
			if err != nil {
				return err
			}
			printEnginePlanTable(planResult)
			changed := 0
			for _, a := range planResult.Agents {
				if a.Changed {
					changed++
				}
			}
			if changed > 0 {
				fmt.Printf("%s: %d agent(s) need fix\n", yellow("reconcile dry-run"), changed)
				os.Exit(1)
			}
			fmt.Println(green("reconcile dry-run") + ": no changes needed")
			return nil
		},
	}
	cmd.Flags().Bool("dry-run", true, "Dry run mode")
	cmd.Flags().Bool("fix", false, "Fix drift")
	return cmd
}

// ── rollback ─────────────────────────────────────────────────────────

func newRollbackCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rollback",
		Short: "Rollback last apply",
		RunE: func(cmd *cobra.Command, args []string) error {
			stateDir, _ := cmd.Flags().GetString("state-dir")
			runID, _ := cmd.Flags().GetString("run")
			agent, _ := cmd.Flags().GetString("agent")
			result, err := engine.Rollback(stateDir, runID, agent, os.Getenv("USER"))
			if err != nil {
				return err
			}
			fmt.Printf("%s run_id=%s source=%s restored=%d\n",
				yellow("rollback done"), result.RunID, result.SourceRunID, len(result.ChangedFiles))
			return nil
		},
	}
	cmd.Flags().String("run", "", "Run ID to rollback")
	cmd.MarkFlagRequired("run")
	cmd.Flags().String("agent", "", "Specific agent to rollback")
	return cmd
}

// ── doctor ───────────────────────────────────────────────────────────

func newDoctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Health check",
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			secretsDir, _ := cmd.Flags().GetString("secrets-dir")
			checkSecrets, _ := cmd.Flags().GetBool("check-secrets")
			checkSecretsAge, _ := cmd.Flags().GetBool("check-secrets-age")
			maxAgeDays, _ := cmd.Flags().GetInt("max-age-days")

			ok, validationErrs := validate.ValidateConfig(configDir)
			if !ok {
				fmt.Println(red("doctor failed") + ": config invalid")
				for _, e := range validationErrs {
					fmt.Println("- " + e)
				}
				os.Exit(1)
			}

			var issues []string

			if checkSecrets {
				hasEnv := false
				entries, _ := os.ReadDir(secretsDir)
				for _, e := range entries {
					if !e.IsDir() && strings.HasSuffix(e.Name(), ".env") {
						hasEnv = true
						break
					}
				}
				if !hasEnv {
					issues = append(issues, "no secrets .env files found")
				}
			}

			if checkSecretsAge {
				entries, _ := os.ReadDir(secretsDir)
				for _, e := range entries {
					if e.IsDir() || !strings.HasSuffix(e.Name(), ".env") {
						continue
					}
					info, err := e.Info()
					if err != nil {
						continue
					}
					ageDays := int(time.Since(info.ModTime()).Hours() / 24)
					if ageDays > maxAgeDays {
						p := filepath.Join(secretsDir, e.Name())
						issues = append(issues, fmt.Sprintf("secrets file too old (%dd): %s", ageDays, p))
					}
				}
			}

			if len(issues) > 0 {
				fmt.Println(yellow("doctor warnings"))
				for _, item := range issues {
					fmt.Println("- " + item)
				}
				os.Exit(1)
			}

			fmt.Println(green("doctor ok"))
			return nil
		},
	}
	cmd.Flags().Bool("check-secrets", false, "Check secrets exist")
	cmd.Flags().Bool("check-secrets-age", false, "Check secrets age")
	cmd.Flags().Int("max-age-days", 7, "Max age in days")
	return cmd
}

// ── Table printers ───────────────────────────────────────────────────

func printEnginePlanTable(result *engine.PlanResult) {
	headers := []string{"Agent", "Changed", "Current", "Desired", "Path"}
	var rows [][]string
	for _, a := range result.Agents {
		changed := "no"
		if a.Changed {
			changed = "yes"
		}
		rows = append(rows, []string{
			a.Agent, changed,
			fmt.Sprintf("%d", a.CurrentCount),
			fmt.Sprintf("%d", a.DesiredCount),
			a.Path,
		})
	}
	PrintTable("Plan", headers, rows)
}

func printContentPlanTable(data map[string]any) {
	items, ok := data["items"].([]map[string]any)
	if !ok {
		return
	}
	headers := []string{"Agent", "Type", "Target", "Changed"}
	var rows [][]string
	for _, item := range items {
		changed := "✗"
		if c, _ := item["changed"].(bool); c {
			changed = "✓"
		}
		path := ShortenPath(fmt.Sprint(item["path"]))
		rows = append(rows, []string{
			fmt.Sprint(item["agent"]),
			fmt.Sprint(item["type"]),
			path,
			changed,
		})
	}
	PrintTable("Content Plan", headers, rows)
}

// runEngineMCPStatus is reused by both drift and mcp status commands.
func runEngineMCPStatus(configDir, secretsDir string) error {
	result, err := engine.Plan(configDir, secretsDir)
	if err != nil {
		return err
	}
	printEnginePlanTable(result)
	changed := 0
	for _, a := range result.Agents {
		if a.Changed {
			changed++
		}
	}
	if changed > 0 {
		fmt.Printf("%s: %d agent(s)\n", yellow("Drift detected"), changed)
		os.Exit(1)
	}
	fmt.Println(green("Healthy") + ": no MCP drift")
	return nil
}

// ── Counting helpers ─────────────────────────────────────────────────

func countMapChangedFiles(m map[string]any) int {
	if cf, ok := m["changed_files"].([]map[string]any); ok {
		return len(cf)
	}
	if cf, ok := m["changed_files"].([]any); ok {
		return len(cf)
	}
	return 0
}

func countMapChangedItems(data map[string]any) int {
	items, ok := data["items"].([]map[string]any)
	if !ok {
		return 0
	}
	count := 0
	for _, item := range items {
		if c, _ := item["changed"].(bool); c {
			count++
		}
	}
	return count
}
