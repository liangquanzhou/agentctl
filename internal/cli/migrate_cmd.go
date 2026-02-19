package cli

import (
	"fmt"
	"os"

	"agentctl/internal/engine"

	"github.com/spf13/cobra"
)

func newMigrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Migration commands",
	}
	cmd.AddCommand(newMigrateInitCmd())
	cmd.AddCommand(newMigrateFinalizeCmd())
	return cmd
}

func newMigrateInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize migration from legacy config",
		RunE: func(cmd *cobra.Command, args []string) error {
			sourceConfig, _ := cmd.Flags().GetString("source-config")
			configDir, _ := cmd.Flags().GetString("config-dir")
			dryRun, _ := cmd.Flags().GetBool("dry-run")

			summary, err := engine.MigrateInit(sourceConfig, configDir, dryRun)
			if err != nil {
				return err
			}
			PrintJSON(summary)
			return nil
		},
	}
	cmd.Flags().String("source-config", DefaultLegacySource(), "Legacy config path")
	cmd.Flags().Bool("dry-run", false, "Dry run mode")
	return cmd
}

func newMigrateFinalizeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "finalize-legacy",
		Short: "Finalize legacy migration",
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			dryRun, _ := cmd.Flags().GetBool("dry-run")

			summary, err := engine.MigrateFinalizeLegacy(configDir, dryRun)
			if err != nil {
				return err
			}
			PrintJSON(summary)
			return nil
		},
	}
	cmd.Flags().Bool("dry-run", false, "Dry run mode")
	return cmd
}

func newStagebCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stageb",
		Short: "Stage B checks",
	}
	cmd.AddCommand(newStagebCheckCmd())
	return cmd
}

func newStagebCheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Check Stage B criteria",
		RunE: func(cmd *cobra.Command, args []string) error {
			stateDir, _ := cmd.Flags().GetString("state-dir")
			windowDays, _ := cmd.Flags().GetInt("window-days")

			result, err := engine.StageBCheck(stateDir, windowDays)
			if err != nil {
				return err
			}
			PrintJSON(result.ToMap())

			if result.Verdict != "PASS" {
				fmt.Println(red("Stage B check FAILED"))
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().Int("window-days", 3, "Observation window in days")
	return cmd
}
