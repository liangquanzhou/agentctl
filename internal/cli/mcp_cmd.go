package cli

import (
	"fmt"
	"os"

	"agentctl/internal/engine"
	"agentctl/internal/mcpreg"

	"github.com/spf13/cobra"
)

func newMCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "MCP registry commands",
	}
	cmd.AddCommand(newMCPPlanCmd())
	cmd.AddCommand(newMCPStatusCmd())
	cmd.AddCommand(newMCPApplyCmd())
	cmd.AddCommand(newMCPListCmd())
	cmd.AddCommand(newMCPAddCmd())
	cmd.AddCommand(newMCPRmCmd())
	return cmd
}

func newMCPPlanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Preview MCP configuration changes",
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			secretsDir, _ := cmd.Flags().GetString("secrets-dir")
			output, _ := cmd.Flags().GetString("output")

			result, err := engine.Plan(configDir, secretsDir)
			if err != nil {
				return err
			}
			if output == "json" {
				PrintJSON(result.ToMap())
				return nil
			}
			printEnginePlanTable(result)
			return nil
		},
	}
	cmd.Flags().String("output", "text", "Output format: text|json")
	return cmd
}

func newMCPStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Check MCP configuration drift",
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			secretsDir, _ := cmd.Flags().GetString("secrets-dir")
			return runEngineMCPStatus(configDir, secretsDir)
		},
	}
}

func newMCPApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply MCP configuration to all agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			secretsDir, _ := cmd.Flags().GetString("secrets-dir")
			stateDir, _ := cmd.Flags().GetString("state-dir")
			breakGlass, _ := cmd.Flags().GetBool("break-glass")
			reason, _ := cmd.Flags().GetString("reason")

			result, err := engine.Apply(configDir, secretsDir, stateDir, breakGlass, reason, os.Getenv("USER"))
			if err != nil {
				fmt.Println(red("mcp apply failed") + ": " + err.Error())
				os.Exit(1)
			}
			fmt.Printf("%s run_id=%s result=%s changed=%d\n",
				green("mcp apply finished"), result.RunID, result.Result, len(result.ChangedFiles))
			return nil
		},
	}
	cmd.Flags().Bool("break-glass", false, "Emergency override")
	cmd.Flags().String("reason", "", "Reason for break-glass")
	return cmd
}

func newMCPListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List MCP server assignments",
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			output, _ := cmd.Flags().GetString("output")

			data, err := mcpreg.MCPList(configDir)
			if err != nil {
				fmt.Println(red("mcp list failed") + ": " + err.Error())
				os.Exit(1)
			}

			if output == "json" {
				PrintJSON(data)
				return nil
			}

			agentOrder := AgentDisplayOrder()
			var allAgents []string
			if aa, ok := data["agents"].([]string); ok {
				allAgents = aa
			} else if aa, ok := data["agents"].([]any); ok {
				for _, a := range aa {
					allAgents = append(allAgents, fmt.Sprint(a))
				}
			}

			agentSet := make(map[string]bool)
			for _, a := range allAgents {
				agentSet[a] = true
			}
			var orderedAgents []string
			for _, a := range agentOrder {
				if agentSet[a] {
					orderedAgents = append(orderedAgents, a)
					delete(agentSet, a)
				}
			}
			for _, a := range allAgents {
				if agentSet[a] {
					orderedAgents = append(orderedAgents, a)
				}
			}

			headers := []string{"Server", "Runtime"}
			headers = append(headers, orderedAgents...)
			headers = append(headers, "Disabled For")

			var rows [][]string
			serversList, _ := data["servers"].([]any)
			for _, srv := range serversList {
				row, _ := srv.(map[string]any)
				if row == nil {
					continue
				}
				enabled, _ := row["enabled"].(map[string]any)
				vals := []string{fmt.Sprint(row["name"]), fmt.Sprint(row["runtime"])}
				for _, agent := range orderedAgents {
					if e, _ := enabled[agent].(bool); e {
						vals = append(vals, "yes")
					} else {
						vals = append(vals, "")
					}
				}
				var disabledFor []string
				if df, ok := row["disabled_for"].([]string); ok {
					disabledFor = df
				} else if df, ok := row["disabled_for"].([]any); ok {
					for _, d := range df {
						disabledFor = append(disabledFor, fmt.Sprint(d))
					}
				}
				vals = append(vals, joinStrings(disabledFor, ","))
				rows = append(rows, vals)
			}
			PrintTable("MCP Servers", headers, rows)
			return nil
		},
	}
	cmd.Flags().String("output", "text", "Output format: text|json")
	return cmd
}

func newMCPAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add [name]",
		Short: "Add MCP server to agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			agent, _ := cmd.Flags().GetString("agent")
			allAgents, _ := cmd.Flags().GetBool("all")
			toList, _ := cmd.Flags().GetBool("to-list")
			runtime, _ := cmd.Flags().GetString("runtime")
			command, _ := cmd.Flags().GetString("command")
			argsList, _ := cmd.Flags().GetStringSlice("arg")
			envRef, _ := cmd.Flags().GetStringSlice("env-ref")
			output, _ := cmd.Flags().GetString("output")

			data, err := mcpreg.MCPAdd(configDir, args[0], mcpreg.AddOpts{
				Agent:     agent,
				AllAgents: allAgents,
				ToList:    toList,
				Runtime:   runtime,
				Command:   command,
				Args:      argsList,
				EnvRef:    envRef,
			})
			if err != nil {
				fmt.Println(red("mcp add failed") + ": " + err.Error())
				os.Exit(1)
			}

			if output == "json" {
				PrintJSON(data)
				return nil
			}
			var changedAgents []string
			if ca, ok := data["changed_agents"].([]string); ok {
				changedAgents = ca
			}
			fmt.Printf("%s server=%s op=%s changed_agents=%d\n",
				green("mcp add done"), data["server"], data["op"], len(changedAgents))
			return nil
		},
	}
	cmd.Flags().StringP("agent", "a", "", "Target agent")
	cmd.Flags().Bool("all", false, "Apply to all agents")
	cmd.Flags().Bool("to-list", false, "Add server definition to list")
	cmd.Flags().String("runtime", "custom", "Runtime for --to-list")
	cmd.Flags().String("command", "", "Command for --to-list")
	cmd.Flags().StringSlice("arg", nil, "Args for --to-list")
	cmd.Flags().StringSlice("env-ref", nil, "EnvRef for --to-list")
	cmd.Flags().String("output", "text", "Output format: text|json")
	return cmd
}

func newMCPRmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rm [name]",
		Short: "Remove MCP server from agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			agent, _ := cmd.Flags().GetString("agent")
			allAgents, _ := cmd.Flags().GetBool("all")
			fromList, _ := cmd.Flags().GetBool("from-list")
			output, _ := cmd.Flags().GetString("output")

			data, err := mcpreg.MCPRm(configDir, args[0], mcpreg.RmOpts{
				Agent:     agent,
				AllAgents: allAgents,
				FromList:  fromList,
			})
			if err != nil {
				fmt.Println(red("mcp rm failed") + ": " + err.Error())
				os.Exit(1)
			}

			if output == "json" {
				PrintJSON(data)
				return nil
			}
			var changedAgents []string
			if ca, ok := data["changed_agents"].([]string); ok {
				changedAgents = ca
			}
			fmt.Printf("%s server=%s op=%s changed_agents=%d\n",
				yellow("mcp rm done"), data["server"], data["op"], len(changedAgents))
			return nil
		},
	}
	cmd.Flags().StringP("agent", "a", "", "Target agent")
	cmd.Flags().Bool("all", false, "Apply to all agents")
	cmd.Flags().Bool("from-list", false, "Remove server definition from list")
	cmd.Flags().String("output", "text", "Output format: text|json")
	return cmd
}

func joinStrings(s []string, sep string) string {
	result := ""
	for i, v := range s {
		if i > 0 {
			result += sep
		}
		result += v
	}
	return result
}
