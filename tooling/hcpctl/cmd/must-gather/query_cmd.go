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

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/mustgather"
)

func newQueryCommand() (*cobra.Command, error) {
	opts := DefaultQueryOptions()

	cmd := &cobra.Command{
		Use:              "query",
		Short:            "Execute default queries against Azure Data Explorer",
		Long:             `Execute preconfigured queries against Azure Data Explorer clusters.`,
		Args:             cobra.NoArgs,
		SilenceUsage:     true,
		SilenceErrors:    true,
		TraverseChildren: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context(), false)
		},
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}

	if err := BindQueryOptions(opts, cmd); err != nil {
		return nil, err
	}

	return cmd, nil
}

func (opts *CompletedQueryOptions) RunQuery(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)
	defer func() {
		if closeErr := opts.QueryClient.Close(); closeErr != nil {
			logger.Error(closeErr, "Warning: failed to close Kusto client")
		}
	}()

	queryOptions, err := mustgather.NewQueryOptions(opts.SubscriptionID, opts.ResourceGroup, opts.ResourceId, opts.TimestampMin, opts.TimestampMax, opts.Limit)
	if err != nil {
		return fmt.Errorf("failed to create query options: %w", err)
	}

	gatherer := mustgather.NewCliGatherer(opts.QueryClient, opts.OutputPath, ServicesLogDirectory, HostedControlPlaneLogDirectory, mustgather.GathererOptions{
		QueryOptions:               queryOptions,
		SkipHostedControlPlaneLogs: opts.SkipHostedControlPlaneLogs,
		GatherInfraLogs:            false,
	})

	err = gatherer.GatherLogs(ctx)
	if err != nil {
		return fmt.Errorf("failed to gather logs: %w", err)
	}

	return nil
}
