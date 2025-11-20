package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version information set via ldflags
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func NewCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "argocd-offline-cli",
		Short: "An Argo CD CLI offline utility",
		Long: `A utility, based on Argo CD, that can be used "offline" (without requiring a running Argo CD server),
to preview the Kubernetes resource manifests being created and managed by Argo CD.`,
		Version: fmt.Sprintf("%s (commit: %s, built: %s)", version, commit, date),
	}

	// Enable -v as shorthand for --version
	rootCmd.Flags().BoolP("version", "v", false, "version for argocd-offline-cli")

	rootCmd.AddCommand(AppSetCommand())
	rootCmd.AddCommand(AppCommand())

	return rootCmd
}
