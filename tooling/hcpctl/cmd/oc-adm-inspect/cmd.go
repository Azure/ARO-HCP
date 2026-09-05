// Copyright 2026 Microsoft Corporation
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

// Package ocadminspectcmd provides the `hcpctl oc-adm-inspect` command: a
// Kusto-backed equivalent of `oc adm inspect` that reconstructs a namespace's
// resources, events, and pod logs at a past point in time.
package ocadminspectcmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/ocadminspect"
)

// NewCommand builds the oc-adm-inspect command.
func NewCommand(group string) (*cobra.Command, error) {
	opts := DefaultOptions()
	cmd := &cobra.Command{
		Use:     "oc-adm-inspect [ns/<name> ...]",
		Short:   "Reconstruct namespace state, events, and pod logs at a point in time from Kusto",
		GroupID: group,
		Long: `oc-adm-inspect is a Kusto-backed equivalent of "oc adm inspect".

Given a management or service cluster, one or more namespaces, and a time window
(--timestamp-min/--timestamp-max, matching must-gather), it reconstructs from
telemetry, as of --timestamp-max:
  - the state of every resource in the namespace (kubernetesResourceSnapshots),
    excluding resources that were actually deleted by then (a Delete event, or a
    deletionTimestamp past its grace period),
  - the namespace's Kubernetes events in the window (kubernetesEvents), and
  - the container logs for the namespace's pods in the window (containerLogs).

Namespaces like kube-system require specifying the cluster. When inspecting a
hosted-cluster namespace the paired control-plane namespace is added automatically,
and vice versa. If no cluster is given, the clusters active in the window are
listed so you can pick one.`,
		Example: `  hcpctl oc-adm-inspect -n kube-system --management-cluster mgmt-1 --timestamp-min "2026-08-31 11:00:00" --timestamp-max "2026-08-31 12:00:00" --kusto hcp-stg-uk-2 --region uksouth
  hcpctl oc-adm-inspect ns/ocm-arohcpstg-2abc --management-cluster mgmt-1 --timestamp-min "2026-08-31 11:00:00" --timestamp-max "2026-08-31 12:00:00" --kusto hcp-stg-uk-2 --region uksouth`,
		Args:             cobra.ArbitraryArgs,
		SilenceUsage:     true,
		SilenceErrors:    true,
		TraverseChildren: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context(), args)
		},
	}
	if err := BindOptions(opts, cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}

// Inspect runs the discovery-or-inspect flow against the completed options.
func (o *CompletedOptions) Inspect(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)
	defer func() {
		if err := o.kustoClient.Close(); err != nil {
			logger.Error(err, "failed to close Kusto client")
		}
	}()

	// No cluster specified: discover which clusters were active and tell the user.
	if o.ClusterName == "" {
		clusters, err := ocadminspect.DiscoverActiveClusters(ctx, o.kustoClient, o.factory, o.baseOptions)
		if err != nil {
			return fmt.Errorf("no --management-cluster/--service-cluster given and failed to discover active clusters: %w", err)
		}
		return fmt.Errorf("no --management-cluster or --service-cluster specified\n\n%s", clusterOptionsMessage(clusters, o.TimestampMax))
	}

	inspector := ocadminspect.NewInspector(o.kustoClient, o.factory, o.baseOptions, o.ClusterName, o.writer)

	if o.Out != "" {
		logger.Info("inspecting whole cluster",
			"cluster", o.ClusterName,
			"windowStart", o.TimestampMin.Format(time.RFC3339),
			"windowEnd", o.TimestampMax.Format(time.RFC3339),
			"output", o.Out,
		)
		if err := inspector.InspectCluster(ctx, o.TimestampMin.Add(-pollBaselineLookback)); err != nil {
			return err
		}
		kshrkWriter, ok := o.writer.(*ocadminspect.KshrkWriter)
		if !ok {
			return fmt.Errorf("internal error: --out set but writer is not a KshrkWriter")
		}
		if err := kshrkWriter.Finish(); err != nil {
			return fmt.Errorf("failed to seal kshrk archive: %w", err)
		}
		logger.Info("wrote kshrk archive", "path", o.Out)
		return nil
	}

	namespaces, err := inspector.ExpandWithPairedNamespaces(ctx, o.Namespaces)
	if err != nil {
		return fmt.Errorf("failed to pair hosted-cluster/control-plane namespaces: %w", err)
	}

	logger.Info("inspecting namespaces",
		"cluster", o.ClusterName,
		"namespaces", strings.Join(namespaces, ","),
		"timestamp", o.TimestampMax.Format(time.RFC3339),
		"output", o.OutputPath,
	)
	if err := inspector.InspectNamespaces(ctx, namespaces); err != nil {
		return err
	}
	logger.Info("wrote inspect output", "path", o.OutputPath)
	return nil
}

// clusterOptionsMessage renders the discovered clusters, split into management and
// service clusters, so the user can re-run with the right flag.
func clusterOptionsMessage(clusters []string, timestamp time.Time) string {
	if len(clusters) == 0 {
		return fmt.Sprintf("no clusters were active around %s; widen the --timestamp-min/--timestamp-max window", timestamp.Format(time.RFC3339))
	}
	var management, service []string
	for _, cluster := range clusters {
		if ocadminspect.IsManagementCluster(cluster) {
			management = append(management, cluster)
		} else {
			service = append(service, cluster)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "clusters active around %s:\n", timestamp.Format(time.RFC3339))
	if len(management) > 0 {
		b.WriteString("\n  management clusters (pass --management-cluster <name>):\n")
		for _, cluster := range management {
			fmt.Fprintf(&b, "    %s\n", cluster)
		}
	}
	if len(service) > 0 {
		b.WriteString("\n  service clusters (pass --service-cluster <name>):\n")
		for _, cluster := range service {
			fmt.Fprintf(&b, "    %s\n", cluster)
		}
	}
	return b.String()
}
