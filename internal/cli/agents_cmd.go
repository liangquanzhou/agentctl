package cli

import (
	"fmt"

	"agentctl/internal/agents"

	"github.com/spf13/cobra"
)

func newAgentsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "Agent registry commands",
	}
	cmd.AddCommand(newAgentsListCmd())
	return cmd
}

func newAgentsListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List registered agents and local status",
		RunE: func(cmd *cobra.Command, args []string) error {
			output, _ := cmd.Flags().GetString("output")
			available, _ := cmd.Flags().GetBool("available")

			registry := agents.LoadAgentRegistry("")
			probes := agents.ProbeAll(registry)

			if available {
				var filtered []agents.ProbeStatus
				for _, p := range probes {
					if p.Installed {
						filtered = append(filtered, p)
					}
				}
				probes = filtered
			}

			if output == "json" {
				PrintJSON(probes)
				return nil
			}

			headers := []string{"Agent", "Installed", "Config", "Writable", "MCP Path"}
			var rows [][]string
			for _, p := range probes {
				defn := registry[p.Name]
				rows = append(rows, []string{
					p.Name,
					boolMark(p.Installed),
					boolMark(p.ConfigFound),
					boolMark(p.ConfigWritable),
					defn.MCPPath,
				})
			}

			if len(rows) == 0 {
				fmt.Println("No agents found.")
				return nil
			}
			PrintTable("Agents", headers, rows)
			return nil
		},
	}
	cmd.Flags().String("output", "text", "Output format: text|json")
	cmd.Flags().Bool("available", false, "Only show locally installed agents")
	return cmd
}

func boolMark(b bool) string {
	if b {
		return green("yes")
	}
	return red("no")
}
