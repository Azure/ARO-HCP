// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package mustgather

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
	"github.com/spf13/cobra"
)

func NewCommand(group string) (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:     "must-gather",
		Aliases: []string{"mg"},
		Short:   "Azure Data Explorer must-gather operations",
		GroupID: group,
		Long: `must-gather provides data collection operations for Azure Data Explorer clusters.

This command group includes subcommands for querying Azure Data Explorer instances
and collecting diagnostic data for troubleshooting and analysis.`,
		Example: `  hcpctl must-gather query --kusto-endpoint https://my-kusto-cluster.eastus.kusto.windows.net
  hcpctl must-gather query --kusto-endpoint https://my-kusto-cluster.eastus.kusto.windows.net --output results.json`,
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}

	// Add query subcommand
	queryCmd, err := newQueryCommand()
	if err != nil {
		return nil, err
	}
	cmd.AddCommand(queryCmd)

	return cmd, nil
}

func newQueryCommand() (*cobra.Command, error) {
	opts := DefaultMustGatherOptions()

	cmd := &cobra.Command{
		Use:              "query",
		Short:            "Execute default queries against Azure Data Explorer",
		Long:             `Execute preconfigured queries against Azure Data Explorer clusters.`,
		Args:             cobra.NoArgs,
		SilenceUsage:     true,
		SilenceErrors:    true,
		TraverseChildren: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runQuery(cmd.Context(), opts)
		},
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}

	if err := BindMustGatherOptions(opts, cmd); err != nil {
		return nil, err
	}

	return cmd, nil
}

func runQuery(ctx context.Context, opts *RawMustGatherOptions) error {
	validated, err := opts.Validate(ctx)
	if err != nil {
		return err
	}

	completed, err := validated.Complete(ctx)
	if err != nil {
		return err
	}

	client, err := kusto.NewClient(opts.KustoEndpoint, opts.KustoDebug)
	if err != nil {
		return fmt.Errorf("failed to create Kusto client: %w", err)
	}
	defer func() {
		if closeErr := client.Close(); closeErr != nil {
			fmt.Printf("Warning: failed to close Kusto client: %v\n", closeErr)
		}
	}()

	clusterIds, err := executeClusterIdQuery(ctx, client, completed, completed.QueryOptions)
	if err != nil {
		return fmt.Errorf("failed to execute cluster id query: %w", err)
	}
	fmt.Printf("Cluster IDs: %s\n", strings.Join(clusterIds, ", "))
	completed.QueryOptions.ClusterIds = clusterIds

	err = executeServicesQueries(ctx, client, completed, completed.QueryOptions)
	if err != nil {
		return fmt.Errorf("failed to execute query: %w", err)
	}

	if opts.SkipCustomerLogs {
		fmt.Println("Skipping customer logs")
	} else {
		fmt.Println("Executing customer logs")
		err := executeCustomerLogsQuery(ctx, client, completed, completed.QueryOptions)
		if err != nil {
			return fmt.Errorf("failed to execute customer logs query: %w", err)
		}
	}

	return nil
}

func writeContainerLogsToFile(outputChannel chan any, outputPath string, directory string) error {
	openedFiles := make(map[string]*os.File)
	for row := range outputChannel {
		fileName := fmt.Sprintf("%s-%s-%s.log", row.(*ContainerLogsRow).Cluster, row.(*ContainerLogsRow).Namespace, row.(*ContainerLogsRow).ContainerName)

		file, ok := openedFiles[fileName]
		if !ok {
			file, err := os.Create(path.Join(outputPath, directory, fileName))
			if err != nil {
				return fmt.Errorf("failed to create output file: %w", err)
			}
			openedFiles[fileName] = file
		}
		defer file.Close()
		fmt.Fprintf(openedFiles[fileName], "%s\n", string(row.(*ContainerLogsRow).Log))
	}
	return nil
}

func executeClusterIdQuery(ctx context.Context, client *kusto.Client, opts *MustGatherOptions, queryOpts QueryOptions) ([]string, error) {
	query := getClusterIdQuery(queryOpts.SubscriptionId, queryOpts.ResourceGroupName)

	outputChannel := make(chan any)
	allClusterIds := make([]string, 0)

	go func() {
		for row := range outputChannel {
			clusterId := row.(*ClusterIdRow).ClusterId
			if clusterId != "" {
				allClusterIds = append(allClusterIds, clusterId)
			}
		}
	}()

	_, err := client.ExecutePreconfiguredQuery(ctx, query, outputChannel, ClusterIdRow{}, opts.QueryTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}
	close(outputChannel)

	return allClusterIds, nil
}

// executeQuery performs the actual query execution against Azure Data Explorer
func executeServicesQueries(ctx context.Context, client *kusto.Client, opts *MustGatherOptions, queryOpts QueryOptions) error {
	queries := getServicesQueries(queryOpts)

	outputChannel := make(chan any)
	defer close(outputChannel)

	go writeContainerLogsToFile(outputChannel, opts.OutputPath, "serviceLogs")

	return executeContainerLogsQueries(ctx, client, opts, queries, outputChannel)
}

func executeCustomerLogsQuery(ctx context.Context, client *kusto.Client, opts *MustGatherOptions, queryOpts QueryOptions) error {
	query := getCustomerLogsQuery(queryOpts)

	outputChannel := make(chan any)
	defer close(outputChannel)

	go writeContainerLogsToFile(outputChannel, opts.OutputPath, "customerLogs")

	return executeContainerLogsQueries(ctx, client, opts, query, outputChannel)
}

func executeContainerLogsQueries(ctx context.Context, client *kusto.Client, opts *MustGatherOptions, queries []*kusto.ConfigurableQuery, outputChannel chan any) error {
	wg := sync.WaitGroup{}
	wg.Add(len(queries))
	for i, query := range queries {
		go func(query *kusto.ConfigurableQuery) error {
			defer wg.Done()
			_, err := client.ExecutePreconfiguredQuery(ctx, query, outputChannel, ContainerLogsRow{}, opts.QueryTimeout)
			if err != nil {
				fmt.Printf("Query %d failed: %v\n", i+1, err)
				return fmt.Errorf("failed to execute query: %w", err)
			}

			return nil
		}(query)
	}
	wg.Wait()

	return nil
}
