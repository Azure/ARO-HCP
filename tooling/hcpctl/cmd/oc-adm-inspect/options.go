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

package ocadminspectcmd

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/ocadminspect"
)

// namespacePattern matches a valid Kubernetes namespace name (an RFC 1123 label:
// lowercase alphanumerics and '-', starting and ending alphanumeric, up to 63
// chars). It rejects path separators and traversal (e.g. "../"), so a namespace
// is safe to use as an on-disk path segment.
var namespacePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// RawOptions is the unvalidated CLI configuration for oc-adm-inspect.
type RawOptions struct {
	Kusto             string
	Region            string
	SubscriptionID    string
	ResourceGroup     string
	Namespaces        []string
	TimestampMin      time.Time
	TimestampMax      time.Time
	ManagementCluster string
	ServiceCluster    string
	OutputPath        string
	QueryTimeout      time.Duration
	Limit             int
}

// DefaultOptions returns RawOptions with sensible defaults. The time-window
// defaults mirror must-gather (last 24h).
func DefaultOptions() *RawOptions {
	return &RawOptions{
		TimestampMin: time.Now().Add(-24 * time.Hour),
		TimestampMax: time.Now(),
		QueryTimeout: 5 * time.Minute,
		Limit:        -1,
		OutputPath:   fmt.Sprintf("oc-adm-inspect-%s", time.Now().Format("20060102-150405")),
	}
}

// BindOptions wires the cobra flags. The time-window flags match must-gather
// exactly (--timestamp-min / --timestamp-max, "YYYY-MM-DD HH:MM:SS" layout).
func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.Kusto, "kusto", opts.Kusto, "Azure Data Explorer cluster name (required)")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Azure Data Explorer cluster region (required)")
	cmd.Flags().StringVar(&opts.SubscriptionID, "subscription-id", opts.SubscriptionID, "subscription ID (optional, recorded for context)")
	cmd.Flags().StringVar(&opts.ResourceGroup, "resource-group", opts.ResourceGroup, "resource group (optional, recorded for context)")
	cmd.Flags().StringArrayVarP(&opts.Namespaces, "namespace", "n", opts.Namespaces, "namespace to inspect (repeatable; also accepts positional ns/<name> args)")
	cmd.Flags().TimeVar(&opts.TimestampMin, "timestamp-min", opts.TimestampMin, []string{time.DateTime}, "start of the window to search for the latest resource snapshots and logs")
	cmd.Flags().TimeVar(&opts.TimestampMax, "timestamp-max", opts.TimestampMax, []string{time.DateTime}, "point in time to reconstruct state at (also the end of the search window)")
	cmd.Flags().StringVar(&opts.ManagementCluster, "management-cluster", opts.ManagementCluster, "management cluster to inspect (the telemetry 'cluster' name)")
	cmd.Flags().StringVar(&opts.ServiceCluster, "service-cluster", opts.ServiceCluster, "service cluster to inspect (the telemetry 'cluster' name)")
	cmd.Flags().StringVar(&opts.OutputPath, "output-path", opts.OutputPath, "path to write the gathered data")
	cmd.Flags().DurationVar(&opts.QueryTimeout, "query-timeout", opts.QueryTimeout, "timeout for query execution")
	cmd.Flags().IntVar(&opts.Limit, "limit", opts.Limit, "limit the number of rows per query (-1 for no limit)")

	cmd.MarkFlagsMutuallyExclusive("management-cluster", "service-cluster")
	for _, flag := range []string{"kusto", "region"} {
		if err := cmd.MarkFlagRequired(flag); err != nil {
			return fmt.Errorf("failed to mark %s as required: %w", flag, err)
		}
	}
	return nil
}

// ValidatedOptions is a RawOptions that passed validation.
type ValidatedOptions struct {
	*RawOptions

	KustoEndpoint *url.URL
	Namespaces    []string
	ClusterName   string // empty when neither management- nor service-cluster was given
}

// Validate checks the inputs and normalizes namespaces. The reconstruction time
// is TimestampMax; TimestampMin is the lookback floor for snapshots and logs.
func (o *RawOptions) Validate(ctx context.Context, args []string) (*ValidatedOptions, error) {
	if o.Kusto == "" {
		return nil, fmt.Errorf("--kusto is required")
	}
	if o.Region == "" {
		return nil, fmt.Errorf("--region is required")
	}
	kustoEndpoint, err := kusto.KustoEndpoint(o.Kusto, o.Region)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kusto endpoint: %w", err)
	}

	if o.QueryTimeout < 30*time.Second {
		return nil, fmt.Errorf("--query-timeout must be at least 30 seconds")
	}
	if o.QueryTimeout > 30*time.Minute {
		return nil, fmt.Errorf("--query-timeout cannot exceed 30 minutes")
	}
	if o.TimestampMin.After(o.TimestampMax) {
		return nil, fmt.Errorf("--timestamp-min cannot be after --timestamp-max")
	}

	namespaces := normalizeNamespaces(append(append([]string{}, o.Namespaces...), args...))
	if len(namespaces) == 0 {
		return nil, fmt.Errorf("at least one namespace is required (use --namespace or a positional ns/<name> argument)")
	}
	for _, namespace := range namespaces {
		if !namespacePattern.MatchString(namespace) {
			return nil, fmt.Errorf("invalid namespace %q: must be a valid Kubernetes namespace name (RFC 1123 label; this also rejects path separators and traversal)", namespace)
		}
	}

	clusterName := o.ManagementCluster
	if clusterName == "" {
		clusterName = o.ServiceCluster
	}

	return &ValidatedOptions{
		RawOptions:    o,
		KustoEndpoint: kustoEndpoint,
		Namespaces:    namespaces,
		ClusterName:   clusterName,
	}, nil
}

// CompletedOptions is fully initialized and ready to run.
type CompletedOptions struct {
	*ValidatedOptions

	kustoClient *kusto.Client
	factory     *kusto.QueryFactory
	baseOptions kusto.QueryOptions
	writer      ocadminspect.Writer
}

// Complete builds the Kusto client, query factory, base query options, and writer.
func (o *ValidatedOptions) Complete(ctx context.Context) (*CompletedOptions, error) {
	kustoClient, err := kusto.NewClient(o.KustoEndpoint, o.QueryTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kusto client: %w", err)
	}
	factory, err := kusto.NewQueryFactory()
	if err != nil {
		return nil, fmt.Errorf("failed to create query factory: %w", err)
	}

	baseOptions := kusto.NewQueryOptions()
	baseOptions.SubscriptionId = o.SubscriptionID
	baseOptions.ResourceGroupName = o.ResourceGroup
	baseOptions.TimestampMin = o.TimestampMin
	baseOptions.TimestampMax = o.TimestampMax
	baseOptions.Limit = o.Limit

	return &CompletedOptions{
		ValidatedOptions: o,
		kustoClient:      kustoClient,
		factory:          factory,
		baseOptions:      baseOptions,
		writer:           ocadminspect.NewFilesystemWriter(o.OutputPath),
	}, nil
}

// Run is the RunE entry point: Validate -> Complete -> Inspect.
func (o *RawOptions) Run(ctx context.Context, args []string) error {
	validated, err := o.Validate(ctx, args)
	if err != nil {
		return err
	}
	completed, err := validated.Complete(ctx)
	if err != nil {
		return err
	}
	return completed.Inspect(ctx)
}

// normalizeNamespaces strips ns/ and namespace/ prefixes, trims, and de-duplicates
// while preserving order.
func normalizeNamespaces(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		name := strings.TrimSpace(value)
		for _, prefix := range []string{"namespace/", "ns/"} {
			if strings.HasPrefix(name, prefix) {
				name = strings.TrimPrefix(name, prefix)
				break
			}
		}
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}
