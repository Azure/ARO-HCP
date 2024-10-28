package main

import (
	"fmt"
	"os"
	"text/template"

	"github.com/spf13/cobra"
)

type Config struct {
	StableVersions []string
	RegistryUrl    string
}

var (
	syncCmd = &cobra.Command{
		Use:   "image-set-config-tmpl",
		Short: "image-set-config-tmpl",
		Long:  "image-set-config-tmpl",
		RunE: func(cmd *cobra.Command, args []string) error {
			config := Config{
				StableVersions: stableVersions,
				RegistryUrl:    registryUrl,
			}

			tmpl, err := template.ParseFiles(configTemplateFile)
			if err != nil {
				return fmt.Errorf("error parsing template file %s: %w", configTemplateFile, err)
			}

			outputFile, err := os.Create(configOutputFile)
			if err != nil {
				return fmt.Errorf("error creating output file %s: %w", configOutputFile, err)
			}
			defer outputFile.Close()

			err = tmpl.Execute(outputFile, config)
			if err != nil {
				return fmt.Errorf("error executing template %s: %w", configTemplateFile, err)
			}
			return nil
		},
	}
	configTemplateFile string
	configOutputFile   string
	stableVersions     []string
	registryUrl        string
)

func main() {
	syncCmd.Flags().StringVarP(&configTemplateFile, "config-tmpl", "c", "", "Configuration file template")
	syncCmd.Flags().StringVarP(&configOutputFile, "config-output", "o", "", "Configuration file output")
	syncCmd.Flags().StringSliceVarP(&stableVersions, "stable-versions", "s", nil, "Stable versions")
	syncCmd.Flags().StringVarP(&registryUrl, "registry-url", "r", "", "URL to registry to store metadata image and mirrored images")

	err := syncCmd.Execute()

	if err != nil {
		os.Exit(1)
	}
}
