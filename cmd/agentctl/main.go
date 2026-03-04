package main

import (
	"fmt"
	"os"

	"agentctl/internal/cli"
)

// Version is set at build time via -ldflags "-X main.Version=..."
var Version = "dev"

func main() {
	root := cli.NewRootCmd(Version)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
