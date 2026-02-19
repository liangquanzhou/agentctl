package cli

import (
	"fmt"

	"agentctl/internal/engine"

	"github.com/spf13/cobra"
)

func newRunsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runs",
		Short: "Run manifest commands",
	}
	cmd.AddCommand(newRunsListCmd())
	cmd.AddCommand(newRunsShowCmd())
	return cmd
}

func newRunsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List runs",
		RunE: func(cmd *cobra.Command, args []string) error {
			stateDir, _ := cmd.Flags().GetString("state-dir")
			rows := engine.ListRuns(stateDir)

			headers := []string{"Run ID", "Command", "Result", "Severity", "Timestamp", "Changed"}
			var tableRows [][]string
			for _, row := range rows {
				changed := 0
				if cf, ok := row["changed_files"].([]any); ok {
					changed = len(cf)
				}
				tableRows = append(tableRows, []string{
					fmt.Sprint(row["run_id"]),
					fmt.Sprint(row["command"]),
					fmt.Sprint(row["result"]),
					fmt.Sprint(row["severity"]),
					fmt.Sprint(row["timestamp"]),
					fmt.Sprintf("%d", changed),
				})
			}
			PrintTable("Runs", headers, tableRows)
			return nil
		},
	}
}

func newRunsShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show run details",
		RunE: func(cmd *cobra.Command, args []string) error {
			stateDir, _ := cmd.Flags().GetString("state-dir")
			runID, _ := cmd.Flags().GetString("run")
			row, err := engine.ShowRun(stateDir, runID)
			if err != nil {
				return err
			}
			PrintJSON(row)
			return nil
		},
	}
	cmd.Flags().String("run", "", "Run ID")
	cmd.MarkFlagRequired("run")
	return cmd
}
