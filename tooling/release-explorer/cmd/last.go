package cmd

// import (
// 	"encoding/json"
// 	"fmt"
// 	"time"

// 	"github.com/Azure/ARO-HCP/tooling/release-explorer/pkg/client"
// 	"github.com/Azure/ARO-HCP/tooling/release-explorer/pkg/timeparse"
// 	"github.com/spf13/cobra"
// 	"gopkg.in/yaml.v3"
// )

// var (
// 	lastOutputFormat string
// )

// var lastCmd = &cobra.Command{
// 	Use:   "last",
// 	Short: "Get the last deployment to an environment",
// 	Long: `Get the most recent deployment to an environment.

// Examples:
//   release-explorer last
//   release-explorer last -e int
//   release-explorer last -e prod --since 2w
//   release-explorer last -e stg --since 2025-11-01`,
// 	RunE:         runLast,
// 	SilenceUsage: true,
// }

// func runLast(cmd *cobra.Command, args []string) error {
// 	ctx := cmd.Context()

// 	rawOptions := &client.RawOptions{
// 		StorageAccountURI:    fmt.Sprintf("https://%s.blob.core.windows.net/", accountName),
// 		StorageContainerName: container,
// 		Environment:          environment,
// 		Since:                since,
// 		Until:                until,
// 		ServiceGroupBase:     serviceGroupBase,
// 		PipelineRevision:     pipelineRev,
// 		SourceRevision:       sourceRev,
// 		IncludeComponents:    lastOutputFormat == "json" || lastOutputFormat == "yaml",
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

// 	if len(deployments) == 0 {
// 		if pipelineRev != "" || sourceRev != "" {
// 			return fmt.Errorf("no deployments found in '%s' matching the specified revision filters", environment)
// 		}
// 		return fmt.Errorf("no deployments found in '%s' between %s and %s.\nMaybe try a longer --since, e.g.\n  release-explorer last -e %s --since 'last month'",
// 			environment, since, until, environment)
// 	}

// 	deployment := deployments[0]

// 	// Parse timestamp
// 	timestamp, err := time.Parse(time.RFC3339, deployment.Metadata.Timestamp)
// 	if err != nil {
// 		return fmt.Errorf("failed to parse timestamp: %w", err)
// 	}

// 	// Convert timestamp to appropriate timezone for display
// 	displayTime := timestamp
// 	if !useUTC {
// 		displayTime = timestamp.Local()
// 	}

// 	// Output based on format
// 	switch lastOutputFormat {
// 	case "json":
// 		jsonBytes, err := json.MarshalIndent(deployment, "", "  ")
// 		if err != nil {
// 			return fmt.Errorf("failed to format results: %w", err)
// 		}
// 		fmt.Println(string(jsonBytes))

// 	case "yaml":
// 		yamlBytes, err := yaml.Marshal(deployment)
// 		if err != nil {
// 			return fmt.Errorf("failed to format results: %w", err)
// 		}
// 		fmt.Print(string(yamlBytes))

// 	default:
// 		// Human-readable format
// 		relativeTime := timeparse.FormatRelativeTime(time.Since(timestamp))
// 		fmt.Printf("Last deployment to %s was %s ago (%s)\n",
// 			environment, relativeTime, displayTime.Format("2006-01-02 15:04:05 MST"))
// 		fmt.Printf("  Release ID: %s\n", deployment.Metadata.ReleaseId.String())
// 		fmt.Printf("  Branch: %s\n", deployment.Metadata.Branch)
// 		if deployment.Metadata.PullRequestID > 0 {
// 			fmt.Printf("  PR: #%d\n", deployment.Metadata.PullRequestID)
// 		}
// 		if len(deployment.Target.RegionConfigs) > 0 {
// 			fmt.Printf("  Regions: %v\n", deployment.Target.RegionConfigs)
// 		}
// 	}

// 	return nil
// }

// func init() {
// 	lastCmd.Flags().StringVarP(&lastOutputFormat, "output", "o", "", "Output format (json, yaml)")
// }
