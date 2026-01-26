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

package list

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

const datasourcesGroupID = "datasources"

func NewListCommand(group string) (*cobra.Command, error) {
	opts := DefaultListDataSourcesOptions()

	listCmd := &cobra.Command{
		Use:     "list",
		Short:   "List resources in an Azure Managed Grafana instance",
		Long:    "List resources configured in an Azure Managed Grafana instance.",
		GroupID: group,
	}

	listCmd.AddGroup(&cobra.Group{
		ID:    datasourcesGroupID,
		Title: "List Commands:",
	})

	listDatasourcesCmd := &cobra.Command{
		Use:     "datasources",
		Short:   "List all datasources in an Azure Managed Grafana instance",
		Long:    "List all datasources configured in an Azure Managed Grafana instance.",
		GroupID: datasourcesGroupID,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context())
		},
	}

	if err := BindListDataSourcesOptions(opts, listDatasourcesCmd); err != nil {
		return nil, err
	}
	listCmd.AddCommand(listDatasourcesCmd)

	return listCmd, nil
}

func (opts *RawListDataSourcesOptions) Run(ctx context.Context) error {
	validated, err := opts.Validate(ctx)
	if err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	completed, err := validated.Complete(ctx)
	if err != nil {
		return fmt.Errorf("completion failed: %w", err)
	}

	return completed.Run(ctx)
}

func (o *CompletedListDataSourcesOptions) Run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	datasources, err := o.GrafanaClient.ListDataSources(
		ctx,
	)
	if err != nil {
		return fmt.Errorf("failed to list datasources: %w", err)
	}

	if o.OutputFormat == "json" {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(datasources); err != nil {
			return fmt.Errorf("failed to encode JSON output: %w", err)
		}
	} else {
		if len(datasources) == 0 {
			logger.Info("No datasources found")
			return nil
		}

		fmt.Printf("Found %d datasource(s):\n\n", len(datasources))
		fmt.Printf("%-6s %-30s %-20s %-50s\n", "ID", "Name", "Type", "URL")
		fmt.Println(strings.Repeat("-", 106))
		for _, ds := range datasources {
			fmt.Printf("%-6d %-30s %-20s %-50s\n", ds.ID, ds.Name, ds.Type, ds.URL)
		}
	}

	return nil
}
