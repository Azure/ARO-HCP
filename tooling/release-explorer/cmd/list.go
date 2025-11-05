// tooling/release-explorer/cmd/list.go
package cmd

import (
	"context"
	"fmt"

	"github.com/Azure/ARO-HCP/tooling/release-explorer/pkg/client"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"
)

var (
	serviceGroup string
	cloud        string
	environment  string
	limit        int
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List release deployments",
	Long:  `List release deployments from Azure Blob Storage, sorted by timestamp (newest first)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		// Create the release client
		c, err := client.NewClient()
		if err != nil {
			return fmt.Errorf("failed to create client: %w", err)
		}

		// Build options based on flags
		var opts []client.ListReleaseDeploymentsOption

		if serviceGroup != "" {
			opts = append(opts, client.WithServiceGroup(serviceGroup))
		}
		if cloud != "" {
			opts = append(opts, client.WithCloud(cloud))
		}
		if environment != "" {
			opts = append(opts, client.WithEnvironment(environment))
		}
		if limit > 0 {
			opts = append(opts, client.WithLimit(limit))
		}

		// List releases
		deployments, err := c.ListReleaseDeployments(ctx, opts...)
		if err != nil {
			return fmt.Errorf("failed to list deployments: %w", err)
		}

		// Display results
		if len(deployments) == 0 {
			fmt.Println("No deployments found")
			return nil
		}

		fmt.Printf("Found %d deployment(s):\n\n", len(deployments))
		for _, d := range deployments {
			data, err := yaml.Marshal(d)
			if err != nil {
				return fmt.Errorf("failed to marshal deployments: %w", err)
			}
			fmt.Println(string(data))
			// fmt.Printf("%d. Release: %s\n", i+1, d.Metadata.ReleaseId.String())
			// fmt.Printf("   Timestamp: %s\n", d.Metadata.Timestamp)
			// fmt.Printf("   Branch: %s\n", d.Metadata.Branch)
			// fmt.Printf("   Target: %s/%s\n", d.Target.Cloud, d.Target.Environment)
			// fmt.Printf("   RegionConfigs: %v\n", d.Target.RegionConfigs)
			// fmt.Printf("   Service Group: %s\n", d.Metadata.ServiceGroup)
			// if d.Metadata.PullRequestID > 0 {
			// 	fmt.Printf("   PR: #%d\n", d.Metadata.PullRequestID)
			// }
			fmt.Println()
		}

		return nil
	},
}

func init() {
	listCmd.Flags().StringVarP(&serviceGroup, "service-group", "s", "", "Service group (required, e.g., 'Microsoft.Azure.ARO.HCP.Global')")
	listCmd.Flags().StringVarP(&cloud, "cloud", "c", "", "Cloud environment (e.g., 'public', 'fairfax')")
	listCmd.Flags().StringVarP(&environment, "environment", "e", "", "Environment (e.g., 'int', 'stg', 'prod')")
	listCmd.Flags().IntVarP(&limit, "limit", "l", 10, "Maximum number of results")

	listCmd.MarkFlagRequired("service-group")

	rootCmd.AddCommand(listCmd)
}
