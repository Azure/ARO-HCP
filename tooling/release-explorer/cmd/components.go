package cmd

// import (
// 	"encoding/json"
// 	"fmt"

// 	"github.com/Azure/ARO-HCP/tooling/release-explorer/pkg/client"
// 	"github.com/spf13/cobra"
// 	"gopkg.in/yaml.v3"
// )

// var (
// 	componentsOutputFormat string
// 	componentsLast         bool
// )

// var componentsCmd = &cobra.Command{
// 	Use:     "components",
// 	Aliases: []string{"comp"},
// 	Short:   "List components from deployments (alias: comp)",
// 	Long: `List components (container images) from deployments.

// Examples:
//   release-explorer components --last
//   release-explorer comp -e int --since 2w -o json
//   release-explorer components --last --since 7d -o yaml`,
// 	RunE:         runComponents,
// 	SilenceUsage: true,
// }

// func runComponents(cmd *cobra.Command, args []string) error {
// 	ctx := cmd.Context()

// 	// Build list options
// 	limit := 10
// 	if componentsLast {
// 		limit = 1
// 	}

// 	rawOptions := &client.RawOptions{
// 		StorageAccountURI:    fmt.Sprintf("https://%s.blob.core.windows.net/", accountName),
// 		StorageContainerName: container,
// 		Environment:          environment,
// 		Since:                since,
// 		Until:                until,
// 		ServiceGroupBase:     serviceGroupBase,
// 		PipelineRevision:     pipelineRev,
// 		SourceRevision:       sourceRev,
// 		IncludeComponents:    true,
// 		UseLocalTime:         !useUTC,
// 	}

// 	validatedOptions, err := rawOptions.Validate()
// 	if err != nil {
// 		return fmt.Errorf("invalid options: %w", err)
// 	}

// 	options, err := validatedOptions.Complete()
// 	if err != nil {
// 		return fmt.Errorf("failed to complete options: %w", err)
// 	}

// 	// List deployments
// 	deployments, err := options.ListReleaseDeployments(ctx)
// 	if err != nil {
// 		return fmt.Errorf("failed to list deployments: %w", err)
// 	}

// 	// Apply client-side limit
// 	if limit > 0 && len(deployments) > limit {
// 		deployments = deployments[:limit]
// 	}

// 	if len(deployments) == 0 {
// 		if pipelineRev != "" || sourceRev != "" {
// 			return fmt.Errorf("no deployments found in '%s' matching the specified revision filters", environment)
// 		}
// 		return fmt.Errorf("no deployments found in '%s' between %s and %s.\nMaybe try a longer --since, e.g.\n  release-explorer components -e %s --since 'last month'",
// 			environment, since, until, environment)
// 	}

// 	// Output based on format
// 	switch componentsOutputFormat {
// 	case "json":
// 		// Extract just components
// 		type ComponentOutput struct {
// 			ReleaseId  string                       `json:"releaseId"`
// 			Components map[string]*client.Component `json:"components"`
// 		}

// 		output := make([]ComponentOutput, len(deployments))
// 		for i, d := range deployments {
// 			output[i] = ComponentOutput{
// 				ReleaseId:  d.Metadata.ReleaseId.String(),
// 				Components: d.Components,
// 			}
// 		}

// 		jsonBytes, err := json.MarshalIndent(output, "", "  ")
// 		if err != nil {
// 			return fmt.Errorf("failed to format results: %w", err)
// 		}
// 		fmt.Println(string(jsonBytes))

// 	case "yaml":
// 		// Extract just components
// 		type ComponentOutput struct {
// 			ReleaseId  string                       `yaml:"releaseId"`
// 			Components map[string]*client.Component `yaml:"components"`
// 		}

// 		output := make([]ComponentOutput, len(deployments))
// 		for i, d := range deployments {
// 			output[i] = ComponentOutput{
// 				ReleaseId:  d.Metadata.ReleaseId.String(),
// 				Components: d.Components,
// 			}
// 		}

// 		yamlBytes, err := yaml.Marshal(output)
// 		if err != nil {
// 			return fmt.Errorf("failed to format results: %w", err)
// 		}
// 		fmt.Print(string(yamlBytes))

// 	default:
// 		// Human-readable format
// 		for _, deployment := range deployments {
// 			fmt.Printf("Components for release %s:\n", deployment.Metadata.ReleaseId.String())
// 			if len(deployment.Components) == 0 {
// 				fmt.Println("  No components found (may not have region configs)")
// 				continue
// 			}

// 			for name, comp := range deployment.Components {
// 				fmt.Printf("  - %s\n", name)
// 				if comp.ImageInfo.Registry != "" {
// 					fmt.Printf("    Registry: %s\n", comp.ImageInfo.Registry)
// 				}
// 				if comp.ImageInfo.Repository != "" {
// 					fmt.Printf("    Repository: %s\n", comp.ImageInfo.Repository)
// 				}
// 				fmt.Printf("    Digest: %s\n", comp.ImageInfo.Digest)
// 			}
// 			fmt.Println()
// 		}
// 	}

// 	return nil
// }

// func init() {
// 	componentsCmd.Flags().StringVarP(&componentsOutputFormat, "output", "o", "", "Output format (json, yaml)")
// 	componentsCmd.Flags().BoolVar(&componentsLast, "last", false, "Show only the most recent deployment's components")
// }
