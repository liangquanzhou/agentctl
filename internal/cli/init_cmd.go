package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"agentctl/internal/agents"
	"agentctl/internal/engine"

	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize agentctl configuration",
		Long:  "Detect installed agents and generate initial agentctl configuration.",
		RunE:  runInit,
	}
	cmd.Flags().Bool("yes", false, "Skip interactive confirmation (auto-select detected agents)")
	cmd.Flags().Bool("force", false, "Overwrite existing configuration")
	cmd.Flags().Bool("dry-run", false, "Show what would be created without writing")
	cmd.Flags().String("output", "text", "Output format: text|json")
	return cmd
}

func runInit(cmd *cobra.Command, args []string) error {
	configDir, _ := cmd.Flags().GetString("config-dir")
	yes, _ := cmd.Flags().GetBool("yes")
	force, _ := cmd.Flags().GetBool("force")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	output, _ := cmd.Flags().GetString("output")

	// Auto-yes when not a TTY
	if !isTTY() {
		yes = true
	}

	// 1. Probe all agents
	registry := agents.LoadAgentRegistry("")
	probes := agents.ProbeAll(registry)

	if output == "text" {
		fmt.Println(bold("── Agent Detection ──"))
		fmt.Println()
	}

	// 2. Show detection results
	if output == "text" {
		headers := []string{"Agent", "Binary", "Config Dir", "Status"}
		var rows [][]string
		for _, p := range probes {
			binary := red("not found")
			if p.BinaryFound {
				binary = green("found") + " (" + ShortenPath(p.BinaryPath) + ")"
			}
			configStatus := red("no")
			if p.Installed {
				configStatus = green("yes")
			}
			status := "--"
			if p.BinaryFound || p.Installed {
				status = green("detected")
			}
			rows = append(rows, []string{p.Name, binary, configStatus, status})
		}
		PrintTable("", headers, rows)
	}

	// 3. Select agents
	var selected []string
	if yes {
		// Auto-select agents that have binary or config dir
		for _, p := range probes {
			if p.BinaryFound || p.Installed {
				selected = append(selected, p.Name)
			}
		}
	} else {
		// Interactive confirmation
		scanner := bufio.NewScanner(os.Stdin)
		for _, p := range probes {
			detected := p.BinaryFound || p.Installed
			defaultAnswer := "n"
			if detected {
				defaultAnswer = "Y"
			}

			fmt.Printf("Include %s? [%s] ", bold(p.Name), defaultAnswer)
			scanner.Scan()
			answer := strings.TrimSpace(scanner.Text())

			if answer == "" {
				if detected {
					selected = append(selected, p.Name)
				}
			} else if strings.ToLower(answer) == "y" || strings.ToLower(answer) == "yes" {
				selected = append(selected, p.Name)
			}
		}
		fmt.Println()
	}

	if len(selected) == 0 {
		if output == "text" {
			fmt.Println(yellow("No agents selected") + " — nothing to do.")
		}
		return nil
	}

	if output == "text" {
		fmt.Printf("Selected agents: %s\n\n", bold(strings.Join(selected, ", ")))
	}

	// 4. Run init
	result, err := engine.Init(engine.InitConfig{
		ConfigDir:      configDir,
		SelectedAgents: selected,
		Force:          force,
		DryRun:         dryRun,
	})
	if err != nil {
		return err
	}

	// 5. Display result
	if output == "json" {
		PrintJSON(result)
		return nil
	}

	if dryRun {
		fmt.Println(yellow("Dry run") + " — the following would be created:")
	} else {
		fmt.Println(green("Initialized") + " agentctl at " + ShortenPath(result.ConfigDir))
	}

	fmt.Printf("\n  Directories: %d\n", len(result.CreatedDirs))
	fmt.Printf("  Files:       %d\n", len(result.CreatedFiles))

	// Next steps
	if !dryRun {
		fmt.Println("\n" + bold("Next steps:"))
		fmt.Println("  1. Edit " + ShortenPath(configDir) + "/rules/shared.md with your shared rules")
		fmt.Println("  2. Register MCP servers: agentctl mcp add <name> ...")
		fmt.Println("  3. Apply configuration:  agentctl apply")
		fmt.Println("  4. Check health:         agentctl doctor")
	}

	return nil
}

// isTTY returns true if stdin is a terminal.
func isTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
