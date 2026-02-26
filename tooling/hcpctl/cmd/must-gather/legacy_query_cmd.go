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
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	azkquery "github.com/Azure/azure-kusto-go/azkustodata/query"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/mustgather"
)

// NormalizedLogLine represents a as expected for output
type LegacyNormalizedLogLine struct {
	Log           []byte    `kusto:"log"`
	Cluster       string    `kusto:"cluster"`
	Namespace     string    `kusto:"namespace_name"`
	ContainerName string    `kusto:"container_name"`
	Timestamp     time.Time `kusto:"timestamp"`
}

func newQueryCommandLegacy() (*cobra.Command, error) {
	opts := DefaultMustGatherOptions()

	cmd := &cobra.Command{
		Use:              "legacy-query",
		Short:            "Execute default queries against Azure Data Explorer",
		Long:             "Execute preconfigured queries against Azure Data Explorer clusters. This command relies on the akskubesystem table.",
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
	clusterIds, err := executeClusterIdQuery(ctx, opts, GetKubeSystemClusterIdQuery(opts))
	if err != nil {
		return fmt.Errorf("failed to execute cluster id query: %w", err)
	}
	logger.V(1).Info("Obtained following clusterIDs", "clusterIds", strings.Join(clusterIds, ", "))
	opts.QueryOptions.ClusterIds = clusterIds

	err = executeKubeSystemQueries(ctx, opts, opts.QueryOptions)
	if err != nil {
		return fmt.Errorf("failed to execute query: %w", err)
	}

	if opts.SkipHostedControlPlaneLogs {
		logger.V(2).Info("Skipping hosted control plane logs")
	} else {
		err = executeKubeSystemHostedControlPlaneLogsQuery(ctx, opts)
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

func processKubesystemLogsRow(row *KubesystemLogsRow) error {
	// read containername/namespace from the row
	// handle inconsistent columns

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
	query := GetKubeSystemQuery(opts, queryOpts.ClusterIds)
	return castQueryAndWriteToFile(ctx, opts, ServicesLogDirectory, []*kusto.ConfigurableQuery{query})
}

func executeKubeSystemHostedControlPlaneLogsQuery(ctx context.Context, opts *MustGatherOptions) error {
	query := GetKubeSystemHostedControlPlaneLogsQuery(opts)
	return castQueryAndWriteToFile(ctx, opts, HostedControlPlaneLogDirectory, query)
}

func castQueryAndWriteToFile(ctx context.Context, opts *MustGatherOptions, targetDirectory string, queries []*kusto.ConfigurableQuery) error {
	castFunction := func(input azkquery.Row) (*LegacyNormalizedLogLine, error) {
		// can directly cast, cause the row is already normalized
		legacyLogLine := &KubesystemLogsRow{}
		if err := input.ToStruct(legacyLogLine); err != nil {
			return nil, fmt.Errorf("failed to convert row to struct: %w", err)
		}
		err := processKubesystemLogsRow(legacyLogLine)
		if err != nil {
			return nil, fmt.Errorf("failed to process kubesystem logs row: %w", err)
		}
		return &LegacyNormalizedLogLine{
			Log:           []byte(legacyLogLine.Log),
			Cluster:       legacyLogLine.Cluster,
			Namespace:     legacyLogLine.Namespace,
			ContainerName: legacyLogLine.ContainerName,
		}, nil
	}
	return queryAndWriteToFile(ctx, opts, targetDirectory, castFunction, queries)
}

// Row represents a row in the query result
type KubesystemLogsRow struct {
	Log           string `kusto:"log"`
	Cluster       string `kusto:"Role"`
	Namespace     string `kusto:"namespace_name"`
	ContainerName string `kusto:"container_name"`
	Timestamp     string `kusto:"timestamp"`
	Kubernetes    string `kusto:"kubernetes"`
}

func GetKubeSystemClusterIdQuery(opts *MustGatherOptions) *kusto.ConfigurableQuery {
	return kusto.NewLegacyClusterIdQuery(opts.SubscriptionID, opts.ResourceGroup, opts.TimestampMin, opts.TimestampMax, opts.Limit)
}

func GetKubeSystemQuery(opts *MustGatherOptions, clusterIds []string) *kusto.ConfigurableQuery {
	return kusto.NewKubeSystemQuery(opts.SubscriptionID, opts.ResourceGroup, clusterIds, opts.TimestampMin, opts.TimestampMax, opts.Limit)
}

func GetKubeSystemHostedControlPlaneLogsQuery(opts *MustGatherOptions) []*kusto.ConfigurableQuery {
	queries := []*kusto.ConfigurableQuery{}
	for _, clusterId := range opts.QueryOptions.ClusterIds {
		query := kusto.NewCustomerKubeSystemQuery(clusterId, opts.TimestampMin, opts.TimestampMax, opts.Limit)
		queries = append(queries, query)
	}
	return queries
}

func queryAndWriteToFile(ctx context.Context, opts *MustGatherOptions, targetDirectory string, castFunction func(input azkquery.Row) (*LegacyNormalizedLogLine, error), queries []*kusto.ConfigurableQuery) error {
	// logger := logr.FromContextOrDiscard(ctx)
	queryOutputChannel := make(chan azkquery.Row)

	queryGroup := new(errgroup.Group)
	queryGroup.Go(func() error {
		return opts.QueryClient.ConcurrentQueries(ctx, queries, queryOutputChannel)
	})

	consumerGroup := new(errgroup.Group)
	consumerGroup.Go(func() error {
		return writeNormalizedLogsToFile(queryOutputChannel, castFunction, opts.OutputPath, targetDirectory)
	})

	if err := queryGroup.Wait(); err != nil {
		return fmt.Errorf("error during query execution: %w", err)
	}
	close(queryOutputChannel)
	if err := consumerGroup.Wait(); err != nil {
		return fmt.Errorf("error during query data transformation: %w", err)
	}
	return nil
}

func writeNormalizedLogsToFile(outputChannel chan azkquery.Row, castFunction func(input azkquery.Row) (*LegacyNormalizedLogLine, error), outputPath string, directory string) error {
	openedFiles := make(map[string]*os.File)
	var allErrors error
	for row := range outputChannel {
		normalizedRow, err := castFunction(row)
		if err != nil {
			return fmt.Errorf("failed to cast row: %w", err)
		}
		fileName := fmt.Sprintf("%s-%s-%s.log", normalizedRow.Cluster, normalizedRow.Namespace, normalizedRow.ContainerName)

		file, ok := openedFiles[fileName]
		if !ok {
			file, err := os.Create(path.Join(outputPath, directory, fileName))
			if err != nil {
				allErrors = errors.Join(allErrors, fmt.Errorf("failed to create output file: %w", err))
				return allErrors
			}
			openedFiles[fileName] = file
		}
		defer file.Close()
		fmt.Fprintf(openedFiles[fileName], "%s\n", string(normalizedRow.Log))
	}
	return allErrors
}

func executeClusterIdQuery(ctx context.Context, opts *MustGatherOptions, query *kusto.ConfigurableQuery) ([]string, error) {
	outputChannel := make(chan azkquery.Row)
	allClusterIds := make([]string, 0)

	g := new(errgroup.Group)
	g.Go(func() error {
		for row := range outputChannel {
			cidRow := &mustgather.ClusterIdRow{}
			if err := row.ToStruct(cidRow); err != nil {
				return fmt.Errorf("failed to convert row to struct: %w", err)
			}
			if cidRow.ClusterId != "" {
				allClusterIds = append(allClusterIds, cidRow.ClusterId)
			}
		}
		return nil
	})

	_, err := opts.QueryClient.ExecutePreconfiguredQuery(ctx, query, outputChannel)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}

	close(outputChannel)

	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}

	return allClusterIds, nil
}
