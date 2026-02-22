package cli

import (
	"fmt"
	"os"
	"sort"

	"agentctl/internal/content"

	"github.com/spf13/cobra"
)

// ── Rules list/add/rm ────────────────────────────────────────────────

func newRulesListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List rule source files and per-agent compose mappings",
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			output, _ := cmd.Flags().GetString("output")

			data, err := content.RulesList(configDir)
			if err != nil {
				fmt.Println(red("rules list failed") + ": " + err.Error())
				os.Exit(1)
			}

			if output == "json" {
				PrintJSON(data)
				return nil
			}

			printRulesListText(data)
			return nil
		},
	}
	cmd.Flags().String("output", "text", "Output format: text|json")
	return cmd
}

func printRulesListText(data map[string]any) {
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
	headers := []string{"Agent", "Target", "Compose", "Separator"}
	var rows [][]string
	for _, name := range agentNames {
		info, _ := agentsMap[name].(map[string]any)
		if info == nil {
			continue
		}
		compose := ""
		if c, ok := info["compose"].([]string); ok {
			compose = joinStrings(c, ", ")
		}
		rows = append(rows, []string{
			name,
			fmt.Sprint(info["target"]),
			compose,
			fmt.Sprintf("%q", info["separator"]),
		})
	}
	PrintTable("Rules Agents", headers, rows)
}

func newRulesAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add [filename]",
		Short: "Add a source file to an agent's compose list",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			agent, _ := cmd.Flags().GetString("agent")
			target, _ := cmd.Flags().GetString("target")
			separator, _ := cmd.Flags().GetString("separator")
			output, _ := cmd.Flags().GetString("output")

			data, err := content.RulesAdd(configDir, args[0], agent, target, separator)
			if err != nil {
				fmt.Println(red("rules add failed") + ": " + err.Error())
				os.Exit(1)
			}

			if output == "json" {
				PrintJSON(data)
				return nil
			}
			fmt.Printf("%s agent=%s file=%s compose=%v\n",
				green("rules add done"), data["agent"], data["filename"], data["compose"])
			return nil
		},
	}
	cmd.Flags().StringP("agent", "a", "", "Agent name")
	cmd.MarkFlagRequired("agent")
	cmd.Flags().String("target", "", "Target path (required for new agent)")
	cmd.Flags().String("separator", "", "Separator between composed files")
	cmd.Flags().String("output", "text", "Output format: text|json")
	return cmd
}

func newRulesRmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rm [filename]",
		Short: "Remove a source file from an agent's compose list",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			agent, _ := cmd.Flags().GetString("agent")
			output, _ := cmd.Flags().GetString("output")

			data, err := content.RulesRm(configDir, args[0], agent)
			if err != nil {
				fmt.Println(red("rules rm failed") + ": " + err.Error())
				os.Exit(1)
			}

			if output == "json" {
				PrintJSON(data)
				return nil
			}
			msg := fmt.Sprintf("%s agent=%s file=%s",
				yellow("rules rm done"), data["agent"], data["filename"])
			if removed, _ := data["removed_agent"].(bool); removed {
				msg += " (agent entry removed)"
			}
			fmt.Println(msg)
			return nil
		},
	}
	cmd.Flags().StringP("agent", "a", "", "Agent name")
	cmd.MarkFlagRequired("agent")
	cmd.Flags().String("output", "text", "Output format: text|json")
	return cmd
}

// ── Hooks list/add/rm ────────────────────────────────────────────────

func newHooksListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List per-agent hook configurations",
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			output, _ := cmd.Flags().GetString("output")

			data, err := content.HooksList(configDir)
			if err != nil {
				fmt.Println(red("hooks list failed") + ": " + err.Error())
				os.Exit(1)
			}

			if output == "json" {
				PrintJSON(data)
				return nil
			}

			printHooksListText(data)
			return nil
		},
	}
	cmd.Flags().String("output", "text", "Output format: text|json")
	return cmd
}

func printHooksListText(data map[string]any) {
	agentsMap, _ := data["agents"].(map[string]any)
	if len(agentsMap) == 0 {
		fmt.Println("No hooks configured.")
		return
	}

	agentNames := sortedKeys(agentsMap)
	headers := []string{"Agent", "Target", "Format", "Detail"}
	var rows [][]string
	for _, name := range agentNames {
		info, _ := agentsMap[name].(map[string]any)
		if info == nil {
			continue
		}
		detail := ""
		if events, ok := info["events"].(map[string]any); ok && len(events) > 0 {
			eventNames := sortedKeys(events)
			detail = "events: " + joinStrings(eventNames, ", ")
		} else if notify, ok := info["notify"].([]string); ok && len(notify) > 0 {
			detail = "notify: " + joinStrings(notify, " ")
		}
		rows = append(rows, []string{
			name,
			fmt.Sprint(info["target"]),
			fmt.Sprint(info["format"]),
			detail,
		})
	}
	PrintTable("Hooks Agents", headers, rows)
}

func newHooksAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a hook entry to an agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			agent, _ := cmd.Flags().GetString("agent")
			event, _ := cmd.Flags().GetString("event")
			command, _ := cmd.Flags().GetString("command")
			timeout, _ := cmd.Flags().GetInt("timeout")
			notify, _ := cmd.Flags().GetString("notify")
			target, _ := cmd.Flags().GetString("target")
			format, _ := cmd.Flags().GetString("format")
			output, _ := cmd.Flags().GetString("output")

			data, err := content.HooksAdd(configDir, agent, content.HooksAddOpts{
				Event:   event,
				Command: command,
				Timeout: timeout,
				Notify:  notify,
				Target:  target,
				Format:  format,
			})
			if err != nil {
				fmt.Println(red("hooks add failed") + ": " + err.Error())
				os.Exit(1)
			}

			if output == "json" {
				PrintJSON(data)
				return nil
			}
			fmt.Printf("%s agent=%s format=%s detail=%v\n",
				green("hooks add done"), data["agent"], data["format"], data["detail"])
			return nil
		},
	}
	cmd.Flags().StringP("agent", "a", "", "Agent name")
	cmd.MarkFlagRequired("agent")
	cmd.Flags().StringP("event", "e", "", "Event name (claude/gemini)")
	cmd.Flags().StringP("command", "c", "", "Hook command (claude/gemini)")
	cmd.Flags().Int("timeout", 0, "Hook timeout in seconds")
	cmd.Flags().String("notify", "", "Notify value (codex)")
	cmd.Flags().String("target", "", "Target path (required for new agent)")
	cmd.Flags().StringP("format", "f", "", "Hook format (required for new agent)")
	cmd.Flags().String("output", "text", "Output format: text|json")
	return cmd
}

func newHooksRmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rm",
		Short: "Remove hook entries from an agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			agent, _ := cmd.Flags().GetString("agent")
			event, _ := cmd.Flags().GetString("event")
			command, _ := cmd.Flags().GetString("command")
			notify, _ := cmd.Flags().GetString("notify")
			output, _ := cmd.Flags().GetString("output")

			data, err := content.HooksRm(configDir, agent, content.HooksRmOpts{
				Event:   event,
				Command: command,
				Notify:  notify,
			})
			if err != nil {
				fmt.Println(red("hooks rm failed") + ": " + err.Error())
				os.Exit(1)
			}

			if output == "json" {
				PrintJSON(data)
				return nil
			}
			msg := fmt.Sprintf("%s agent=%s", yellow("hooks rm done"), data["agent"])
			if removed, _ := data["removed_agent"].(bool); removed {
				msg += " (agent entry removed)"
			}
			fmt.Println(msg)
			return nil
		},
	}
	cmd.Flags().StringP("agent", "a", "", "Agent name")
	cmd.MarkFlagRequired("agent")
	cmd.Flags().StringP("event", "e", "", "Event to remove (claude/gemini)")
	cmd.Flags().StringP("command", "c", "", "Command to remove from event")
	cmd.Flags().String("notify", "", "Notify value to remove (codex)")
	cmd.Flags().String("output", "text", "Output format: text|json")
	return cmd
}

// ── Helpers ──────────────────────────────────────────────────────────

// sortedKeys returns sorted keys of a map[string]any.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
