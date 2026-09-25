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
	"math"

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
		ChangeThreshold: 0.1,
		Percentile:      0.95,
		FleetPercentile: 0.95,
		LimitMultiple:   2.0,
		SourcePrefix:    "defaults",
	}
	var grafanaURL, input, sizingTemplate, namespacePrefix string

	cmd := &cobra.Command{
		Use:   "rightsize-requests",
		Short: "Right-size ARO-HCP CPU/memory requests from Grafana or an offline report",
		Long: `rightsize-requests queries per-cluster production usage from Azure Managed
Grafana (each production cluster is a separate Prometheus datasource) and updates
the CPU/memory requests recorded in config/config.yaml in place.

For every mapped service it computes the peak observed usage across all clusters
over a lookback window, multiplies by a safety margin (default 1.25x), and writes
the result back to config/config.yaml, preserving comments and formatting.

Authentication uses your ambient Azure credentials (az login / managed identity)
scoped to the Azure Managed Grafana service application.

Alternatively, --input right-sizing.json consumes a validated offline report
without credentials and writes only clouds.dev.defaults request overrides.
With --sizing-template and --namespace-prefix it instead updates existing
e2e_minimal requests in the limitClusterSizes=true branch of the Helm template.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if math.IsNaN(opts.ChangeThreshold) || math.IsInf(opts.ChangeThreshold, 0) || opts.ChangeThreshold < 0 || opts.ChangeThreshold > 1 {
				return fmt.Errorf("--change-threshold must be finite and between 0 and 1")
			}
			if (input == "") == (grafanaURL == "") {
				return fmt.Errorf("exactly one of --input or --grafana-url is required")
			}
			if cmd.Flags().Changed("input") && cmd.Flags().Changed("grafana-url") {
				return fmt.Errorf("--input and --grafana-url are mutually exclusive")
			}
			if cmd.Flags().Changed("sizing-template") || cmd.Flags().Changed("namespace-prefix") {
				if input == "" || sizingTemplate == "" || namespacePrefix == "" {
					return fmt.Errorf("--sizing-template and --namespace-prefix require --input and must both be nonempty")
				}
				for _, flag := range []string{"config", "source-prefix", "write-config", "write-prefix"} {
					if cmd.Flags().Changed(flag) {
						return fmt.Errorf("--%s cannot be used with --sizing-template", flag)
					}
				}
			}
			if input != "" {
				for _, flag := range []string{"window", "step", "margin", "percentile", "fleet-percentile", "datasource-pattern", "limit-multiple", "commit", "render-cmd"} {
					if cmd.Flags().Changed(flag) {
						return fmt.Errorf("--%s is not supported with --input", flag)
					}
				}
				if cmd.Flags().Changed("source-prefix") && opts.SourcePrefix != "defaults" {
					return fmt.Errorf("--input requires --source-prefix=defaults")
				}
				if cmd.Flags().Changed("write-prefix") && opts.WritePrefix != "clouds.dev.defaults" {
					return fmt.Errorf("--input requires --write-prefix=clouds.dev.defaults")
				}
			} else if cmd.Flags().Changed("change-threshold") {
				return fmt.Errorf("--change-threshold requires --input")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			log := logr.FromContextOrDiscard(ctx)
			if input != "" {
				if sizingTemplate != "" {
					return rightsize.RunSizingInput(ctx, log, input, sizingTemplate, namespacePrefix, opts)
				}
				return rightsize.RunInput(ctx, log, input, opts)
			}

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

	cmd.Flags().StringVar(&grafanaURL, "grafana-url", "", "Azure Managed Grafana base URL (alternative to --input)")
	cmd.Flags().StringVar(&input, "input", "", "offline right-sizing.json report; writes only clouds.dev.defaults requests without Azure credentials")
	cmd.Flags().StringVar(&sizingTemplate, "sizing-template", "", "with --input, edit limited-branch e2e_minimal requests in this Hypershift Helm template instead of config.yaml")
	cmd.Flags().StringVar(&namespacePrefix, "namespace-prefix", "", "literal HCP namespace prefix for --sizing-template (e.g. ocm-arohcpci01-); use only evidence from e2e_minimal clusters")
	cmd.Flags().Float64Var(&opts.ChangeThreshold, "change-threshold", opts.ChangeThreshold, "input-only fractional deadband in [0,1] against effective current requests; 0 disables; overrides report threshold")
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
	cmd.Flags().BoolVar(&opts.AllowDecrease, "allow-decrease", false, "allow decreases (Grafana: only more than 2x oversized; input: any validated decrease)")

	return cmd
}
