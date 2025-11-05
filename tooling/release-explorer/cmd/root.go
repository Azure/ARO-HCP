// tooling/release-explorer/cmd/root.go
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "release-explorer",
	Short: "A tool to explore ARO-HCP release deployments",
	Long: `Release Explorer is a CLI tool for querying and inspecting
release deployment information stored in Azure Blob Storage.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
