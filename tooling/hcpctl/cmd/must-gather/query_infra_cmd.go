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
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/mustgather"
)

func newQueryInfraCommand() (*cobra.Command, error) {
	opts := DefaultInfraQueryOptions()

	cmd := &cobra.Command{
		Use:   "query-infra",
		Short: "Execute infrastructure queries against Azure Data Explorer",
		Long: `Execute preconfigured infrastructure queries against Azure Data Explorer clusters.
Gathers kubernetes events, systemd logs, and service logs for infrastructure clusters.

You can provide multiple --service-cluster and --mgmt-cluster flags.
Logs will be collected sequentially and stored in a single output folder.`,
		Args:             cobra.NoArgs,
		SilenceUsage:     true,
		SilenceErrors:    true,
		TraverseChildren: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context())
		},
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}

	if err := BindInfraQueryOptions(opts, cmd); err != nil {
		return nil, err
	}

	return cmd, nil
}

func (opts *CompletedInfraQueryOptions) RunInfra(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)
	defer func() {
		if closeErr := opts.QueryClient.Close(); closeErr != nil {
			logger.Error(closeErr, "Warning: failed to close Kusto client")
		}
	}()

	allErrors := []error{}

	for _, clusterName := range opts.ServiceClusters {
		if err := runQuery(ctx, logger, opts.QueryClient, opts.OutputPath, clusterName, mustgather.InfraClusterTypeService, opts.TimestampMin, opts.TimestampMax, opts.Limit); err != nil {
			allErrors = append(allErrors, err)
		}
	}
	for _, clusterName := range opts.MgmtClusters {
		if err := runQuery(ctx, logger, opts.QueryClient, opts.OutputPath, clusterName, mustgather.InfraClusterTypeManagement, opts.TimestampMin, opts.TimestampMax, opts.Limit); err != nil {
			allErrors = append(allErrors, err)
		}
	}

	if len(allErrors) > 0 {
		return fmt.Errorf("failed to gather infrastructure logs for some clusters: %w", allErrors)
	}

	return nil
}

func runQuery(ctx context.Context, logger logr.Logger, queryClient mustgather.QueryClientInterface, outputPath string, clusterName string, clusterType mustgather.InfraClusterType, timestampMin time.Time, timestampMax time.Time, limit int) error {
	logger.V(1).Info("Gathering infrastructure logs", "cluster", clusterName)

	queryOptions, err := mustgather.NewInfraQueryOptions(clusterType, clusterName, timestampMin, timestampMax, limit)
	if err != nil {
		return fmt.Errorf("failed to create query options for cluster %s: %w", clusterName, err)
	}

	gatherer := mustgather.NewCliGatherer(queryClient, outputPath, ServicesLogDirectory, HostedControlPlaneLogDirectory, mustgather.GathererOptions{
		QueryOptions:    queryOptions,
		GatherInfraLogs: true,
	})

	if err := gatherer.GatherLogs(ctx); err != nil {
		return fmt.Errorf("failed to gather infrastructure logs for cluster %s: %w", clusterName, err)
	}

	return nil
}
