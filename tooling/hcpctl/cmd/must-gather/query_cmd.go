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
	"errors"
	"path/filepath"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/mustgather"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/ocadminspect"
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
			return opts.Run(cmd.Context())
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

	gatherer := mustgather.NewCliGatherer(opts.QueryClient, opts.OutputPath, ServicesLogDirectory, HostedControlPlaneLogDirectory, CustomLogsDirectory, mustgather.GathererOptions{
		SkipHostedControlPlaneLogs: opts.SkipHostedControlPlaneLogs,
		SkipKubernetesEventsLogs:   opts.SkipKubernetesEventsLogs,
		CollectSystemdLogs:         opts.CollectSystemdLogs,
		QueryOptions:               &opts.QueryOptions,
	}, false)

	logger.Info("gathering must-gather logs", "output", opts.OutputPath)
	gatherErr := gatherer.GatherLogs(ctx)

	// For every HCP cluster discovered in the resource group, oc-adm-inspect the
	// hosted-cluster namespace (which pulls in the paired control-plane namespace)
	// on the management cluster that hosts it.
	var inspectErr error
	if !opts.SkipOCAdmInspect {
		inspectErr = opts.runOCAdmInspect(ctx)
	}

	if err := errors.Join(gatherErr, inspectErr); err != nil {
		logger.Error(err, "must-gather finished with errors; partial content may have been written", "output", opts.OutputPath)
		return err
	}
	logger.Info("must-gather complete; content written", "output", opts.OutputPath)
	return nil
}

// runOCAdmInspect runs the Kusto-backed oc-adm-inspect for every cluster in the
// resource group, writing under <output>/oc-adm-inspect.
func (opts *CompletedQueryOptions) runOCAdmInspect(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	factory, err := kusto.NewQueryFactory()
	if err != nil {
		return err
	}
	outputPath := filepath.Join(opts.OutputPath, OCAdmInspectDirectory)
	writer := ocadminspect.NewFilesystemWriter(outputPath)
	logger.Info("running oc-adm-inspect for clusters in the resource group", "output", outputPath, "clusterIDs", opts.QueryOptions.ClusterIds)
	return ocadminspect.InspectResourceGroupManagementNamespaces(ctx, opts.QueryClient, factory, opts.QueryOptions, writer, opts.QueryOptions.ClusterIds)
}
