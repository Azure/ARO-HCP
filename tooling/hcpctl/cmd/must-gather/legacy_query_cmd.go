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
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/mustgather"
)

func newQueryCommandLegacy() (*cobra.Command, error) {
	opts := DefaultMustGatherOptions()

	cmd := &cobra.Command{
		Use:              "legacy-query",
		Short:            "Execute default queries against Azure Data Explorer",
		Long:             `Execute preconfigured queries against Azure Data Explorer clusters. This command relies on the akskubesystem table.`,
		Args:             cobra.NoArgs,
		SilenceUsage:     true,
		SilenceErrors:    true,
		TraverseChildren: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context(), true)
		},
	}
	if err := BindMustGatherOptions(opts, cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}

func (opts *MustGatherOptions) RunLegacy(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)
	// clusterIds, err := executeClusterIdQuery(ctx, opts, mustgather.GetKubeSystemClusterIdQuery(opts.SubscriptionID, opts.ResourceGroup))
	// if err != nil {
	// 	return fmt.Errorf("failed to execute cluster id query: %w", err)
	// }
	// logger.V(1).Info("Obtained following clusterIDs", "clusterIds", strings.Join(clusterIds, ", "))
	// opts.QueryOptions.ClusterIds = clusterIds
	// err = serializeOutputToFile(opts.OutputPath, OptionsOutputFile, opts.QueryOptions)
	// if err != nil {
	// 	return fmt.Errorf("failed to write query options to file: %w", err)
	// }

	err := executeKubeSystemQueries(ctx, opts, opts.QueryOptions)
	if err != nil {
		return fmt.Errorf("failed to execute query: %w", err)
	}

	if opts.SkipHostedControlePlaneLogs {
		logger.V(2).Info("Skipping hosted control plane logs")
	} else {
		err = executeKubeSystemHostedControlPlaneLogsQuery(ctx, opts, opts.QueryOptions)
		if err != nil {
			return fmt.Errorf("failed to execute hosted control plane logs query: %w", err)
		}
	}

	return nil
}

type kubernetesCol struct {
	ContainerName string `json:"container_name"`
	Namespace     string `json:"namespace_name"`
}

func processKubesystemLogsRow(row *mustgather.KubesystemLogsRow) error {
	// read containername/namespace from the row
	// handle inconsitent columns

	if row.ContainerName == "" {
		kubernetesCol := kubernetesCol{}
		err := json.Unmarshal([]byte(row.Kubernetes), &kubernetesCol)
		if err != nil {
			return fmt.Errorf("failed to unmarshal kubernetes column: %w", err)
		}
		row.ContainerName = kubernetesCol.ContainerName
		row.Namespace = kubernetesCol.Namespace
	}

	return nil
}

func executeKubeSystemQueries(ctx context.Context, opts *MustGatherOptions, queryOpts mustgather.QueryOptions) error {
	query := mustgather.GetKubeSystemQuery(opts.SubscriptionID, opts.ResourceGroup, queryOpts.ClusterIds)
	return castQueryAndWriteToFile(ctx, opts, ServicesLogDirectory, []*kusto.ConfigurableQuery{query})
}

func executeKubeSystemHostedControlPlaneLogsQuery(ctx context.Context, opts *MustGatherOptions, queryOpts mustgather.QueryOptions) error {
	query := mustgather.GetKubeSystemHostedControlPlaneLogsQuery(queryOpts)
	return castQueryAndWriteToFile(ctx, opts, HostedControlPlaneLogDirectory, query)
}

func castQueryAndWriteToFile(ctx context.Context, opts *MustGatherOptions, targetDirectory string, queries []*kusto.ConfigurableQuery) error {
	// castFunction := func(input *table.Row) (*NormalizedLogLine, error) {
	// 	// can directly cast, cause the row is already normalized
	// 	legacyLogLine := &mustgather.KubesystemLogsRow{}
	// 	if err := input.ToStruct(legacyLogLine); err != nil {
	// 		return nil, fmt.Errorf("failed to convert row to struct: %w", err)
	// 	}
	// 	err := processKubesystemLogsRow(legacyLogLine)
	// 	if err != nil {
	// 		return nil, fmt.Errorf("failed to process kubesystem logs row: %w", err)
	// 	}
	// 	return &NormalizedLogLine{
	// 		Log:           []byte(legacyLogLine.Log),
	// 		Cluster:       legacyLogLine.Cluster,
	// 		Namespace:     legacyLogLine.Namespace,
	// 		ContainerName: legacyLogLine.ContainerName,
	// 	}, nil
	// }
	// return queryAndWriteToFile(ctx, opts, targetDirectory, castFunction, queries)
	return nil
}
