package main

import (
	"os"

	cmd "github.com/touchardv/argocd-offline-cli/cmd/commands"
)

func main() {
	command := cmd.NewCommand()
	if err := command.Execute(); err != nil {
		os.Exit(1)
	}
}
