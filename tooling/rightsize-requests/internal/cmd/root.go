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

package cmd

import (
	"fmt"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/tooling/rightsize-requests/internal/grafana"
	"github.com/Azure/ARO-HCP/tooling/rightsize-requests/internal/rightsize"
)

// NewRootCommand builds the rightsize-requests command tree.
func NewRootCommand() *cobra.Command {
	opts := rightsize.Options{
		Window:          "14d",
		Step:            "5m",
		Margin:          1.25,
		Percentile:      0.95,
		FleetPercentile: 0.95,
		LimitMultiple:   2.0,
		SourcePrefix:    "defaults",
	}
	var grafanaURL string

	cmd := &cobra.Command{
		Use:   "rightsize-requests",
		Short: "Right-size ARO-HCP CPU/memory requests from production Grafana usage",
		Long: `rightsize-requests queries per-cluster production usage from Azure Managed
Grafana (each production cluster is a separate Prometheus datasource) and updates
the CPU/memory requests recorded in config/config.yaml in place.

For every mapped service it computes the peak observed usage across all clusters
over a lookback window, multiplies by a safety margin (default 1.25x), and writes
the result back to config/config.yaml, preserving comments and formatting.

Authentication uses your ambient Azure credentials (az login / managed identity)
scoped to the Azure Managed Grafana service application.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			log := logr.FromContextOrDiscard(ctx)

			cred, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{
				RequireAzureTokenCredentials: true,
			})
			if err != nil {
				return fmt.Errorf("failed to obtain Azure credentials: %w", err)
			}

			gc, err := grafana.New(grafanaURL, cred)
			if err != nil {
				return err
			}
			opts.GrafanaURL = grafanaURL

			return rightsize.Run(ctx, log, gc, opts)
		},
	}

	cmd.Flags().StringVar(&grafanaURL, "grafana-url", "", "Azure Managed Grafana base URL (required)")
	cmd.Flags().StringVar(&opts.ConfigPath, "config", "../../config/config.yaml", "config file to read CURRENT request values from")
	cmd.Flags().StringVar(&opts.SourcePrefix, "source-prefix", opts.SourcePrefix, "dotted key prefix in the source config (e.g. defaults)")
	cmd.Flags().StringVar(&opts.WritePath, "write-config", "", "config file to WRITE new values to (defaults to --config)")
	cmd.Flags().StringVar(&opts.WritePrefix, "write-prefix", "", "dotted key prefix in the write config (defaults to --source-prefix; e.g. clouds.public.defaults for the msft overlay)")
	cmd.Flags().Float64Var(&opts.LimitMultiple, "limit-multiple", opts.LimitMultiple, "when a container sets a numeric memory limit, set it to this multiple of the new request (0 disables)")
	cmd.Flags().StringVar(&opts.Window, "window", opts.Window, "PromQL lookback window for peak usage")
	cmd.Flags().StringVar(&opts.Step, "step", opts.Step, "subquery resolution step for peak usage")
	cmd.Flags().Float64Var(&opts.Margin, "margin", opts.Margin, "safety multiplier applied to observed usage")
	cmd.Flags().Float64Var(&opts.Percentile, "percentile", opts.Percentile, "per-pod OVER TIME statistic (0<p<1 => that quantile, e.g. 0.95; 0 or >=1 => raw max/peak)")
	cmd.Flags().Float64Var(&opts.FleetPercentile, "fleet-percentile", opts.FleetPercentile, "ACROSS PODS/clusters statistic (0<p<1 => that quantile, e.g. 0.95; 0 or >=1 => max). Guards against a single anomalous pod/cluster driving the fleet-wide request.")
	cmd.Flags().StringVar(&opts.DatasourcePattern, "datasource-pattern", "^services-", "regexp; only query datasources whose uid matches. Defaults to prod services clusters; hcps-* control-plane datasources are excluded.")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "report proposed changes without editing config.yaml")
	cmd.Flags().BoolVar(&opts.Commit, "commit", false, "git-commit the edited file (with a summary + Grafana Explore links) after writing")
	cmd.Flags().StringVar(&opts.RenderCmd, "render-cmd", "", "shell command to regenerate rendered configs, run in the write repo root before committing (e.g. 'make -C hcp/ render-service-configuration-examples'); its output is folded into the commit")
	cmd.Flags().BoolVar(&opts.AllowDecrease, "allow-decrease", false, "also lower requests that are more than 2x the observed peak")

	_ = cmd.MarkFlagRequired("grafana-url")

	return cmd
}
