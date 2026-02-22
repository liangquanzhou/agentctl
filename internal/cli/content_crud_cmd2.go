package cli

import (
	"fmt"
	"os"

	"agentctl/internal/content"

	"github.com/spf13/cobra"
)

// ── Commands list/add/rm ─────────────────────────────────────────────

func newCommandsListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List command source files and per-agent target directories",
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			output, _ := cmd.Flags().GetString("output")

			data, err := content.CommandsList(configDir)
			if err != nil {
				fmt.Println(red("commands list failed") + ": " + err.Error())
				os.Exit(1)
			}

			if output == "json" {
				PrintJSON(data)
				return nil
			}

			printCommandsListText(data)
			return nil
		},
	}
	cmd.Flags().String("output", "text", "Output format: text|json")
	return cmd
}

func printCommandsListText(data map[string]any) {
	fmt.Println(bold("Source files:"))
	if sf, ok := data["source_files"].([]string); ok {
		for _, f := range sf {
			fmt.Println("  " + f)
		}
	}

	agentsMap, _ := data["agents"].(map[string]any)
	if len(agentsMap) == 0 {
		fmt.Println()
		return
	}

	agentNames := sortedKeys(agentsMap)
	headers := []string{"Agent", "Target Dir"}
	var rows [][]string
	for _, name := range agentNames {
		info, _ := agentsMap[name].(map[string]any)
		if info == nil {
			continue
		}
		rows = append(rows, []string{name, fmt.Sprint(info["target_dir"])})
	}
	PrintTable("Commands Agents", headers, rows)
}

func newCommandsAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Register an agent's command sync target directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			agent, _ := cmd.Flags().GetString("agent")
			targetDir, _ := cmd.Flags().GetString("target-dir")
			output, _ := cmd.Flags().GetString("output")

			data, err := content.CommandsAdd(configDir, agent, targetDir)
			if err != nil {
				fmt.Println(red("commands add failed") + ": " + err.Error())
				os.Exit(1)
			}

			if output == "json" {
				PrintJSON(data)
				return nil
			}
			fmt.Printf("%s agent=%s target_dir=%s\n",
				green("commands add done"), data["agent"], data["target_dir"])
			return nil
		},
	}
	cmd.Flags().StringP("agent", "a", "", "Agent name")
	cmd.MarkFlagRequired("agent")
	cmd.Flags().String("target-dir", "", "Target directory path")
	cmd.MarkFlagRequired("target-dir")
	cmd.Flags().String("output", "text", "Output format: text|json")
	return cmd
}

func newCommandsRmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rm",
		Short: "Remove an agent from commands config",
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			agent, _ := cmd.Flags().GetString("agent")
			output, _ := cmd.Flags().GetString("output")

			data, err := content.CommandsRm(configDir, agent)
			if err != nil {
				fmt.Println(red("commands rm failed") + ": " + err.Error())
				os.Exit(1)
			}

			if output == "json" {
				PrintJSON(data)
				return nil
			}
			fmt.Printf("%s agent=%s\n", yellow("commands rm done"), data["agent"])
			return nil
		},
	}
	cmd.Flags().StringP("agent", "a", "", "Agent name")
	cmd.MarkFlagRequired("agent")
	cmd.Flags().String("output", "text", "Output format: text|json")
	return cmd
}

// ── Ignore list/add/rm ───────────────────────────────────────────────

func newIgnoreListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List ignore patterns and per-agent target paths",
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			output, _ := cmd.Flags().GetString("output")

			data, err := content.IgnoreList(configDir)
			if err != nil {
				fmt.Println(red("ignore list failed") + ": " + err.Error())
				os.Exit(1)
			}

			if output == "json" {
				PrintJSON(data)
				return nil
			}

			printIgnoreListText(data)
			return nil
		},
	}
	cmd.Flags().String("output", "text", "Output format: text|json")
	return cmd
}

func printIgnoreListText(data map[string]any) {
	fmt.Println(bold("Patterns:"))
	if patterns, ok := data["patterns"].([]string); ok {
		for _, p := range patterns {
			fmt.Println("  " + p)
		}
	}

	agentsMap, _ := data["agents"].(map[string]any)
	if len(agentsMap) == 0 {
		fmt.Println()
		return
	}

	agentNames := sortedKeys(agentsMap)
	headers := []string{"Agent", "Target"}
	var rows [][]string
	for _, name := range agentNames {
		info, _ := agentsMap[name].(map[string]any)
		if info == nil {
			continue
		}
		rows = append(rows, []string{name, fmt.Sprint(info["target"])})
	}
	PrintTable("Ignore Agents", headers, rows)
}

func newIgnoreAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add [pattern]",
		Short: "Add an ignore pattern or agent target",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			agent, _ := cmd.Flags().GetString("agent")
			target, _ := cmd.Flags().GetString("target")
			output, _ := cmd.Flags().GetString("output")

			pattern := ""
			if len(args) > 0 {
				pattern = args[0]
			}

			data, err := content.IgnoreAdd(configDir, content.IgnoreAddOpts{
				Pattern: pattern,
				Agent:   agent,
				Target:  target,
			})
			if err != nil {
				fmt.Println(red("ignore add failed") + ": " + err.Error())
				os.Exit(1)
			}

			if output == "json" {
				PrintJSON(data)
				return nil
			}
			fmt.Printf("%s op=%s\n", green("ignore add done"), data["op"])
			return nil
		},
	}
	cmd.Flags().StringP("agent", "a", "", "Agent name")
	cmd.Flags().String("target", "", "Ignore file target path")
	cmd.Flags().String("output", "text", "Output format: text|json")
	return cmd
}

func newIgnoreRmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rm [pattern]",
		Short: "Remove an ignore pattern or agent",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			agent, _ := cmd.Flags().GetString("agent")
			output, _ := cmd.Flags().GetString("output")

			pattern := ""
			if len(args) > 0 {
				pattern = args[0]
			}

			data, err := content.IgnoreRm(configDir, content.IgnoreRmOpts{
				Pattern: pattern,
				Agent:   agent,
			})
			if err != nil {
				fmt.Println(red("ignore rm failed") + ": " + err.Error())
				os.Exit(1)
			}

			if output == "json" {
				PrintJSON(data)
				return nil
			}
			fmt.Printf("%s op=%s\n", yellow("ignore rm done"), data["op"])
			return nil
		},
	}
	cmd.Flags().StringP("agent", "a", "", "Agent name to remove")
	cmd.Flags().String("output", "text", "Output format: text|json")
	return cmd
}
