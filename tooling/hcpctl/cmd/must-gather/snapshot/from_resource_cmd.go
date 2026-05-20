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

package snapshot

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/azure-kusto-go/azkustodata"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
	snapshotpkg "github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/snapshot"
)

// RawFromResourceOptions holds the unvalidated CLI options for from-resource.
type RawFromResourceOptions struct {
	Kusto           string
	Region          string
	ServiceDatabase string
	HCPDatabase     string
	ResourceGroup   string
	StartTime       string
	EndTime         string
	OutputDir       string
	QueryTimeout    time.Duration
}

func defaultFromResourceOptions() *RawFromResourceOptions {
	return &RawFromResourceOptions{
		ServiceDatabase: "intSVCLogs",
		HCPDatabase:     "intHCPLogs",
		QueryTimeout:    5 * time.Minute,
		OutputDir:       fmt.Sprintf("snapshot-%s", time.Now().Format("20060102-150405")),
	}
}

func bindFromResourceOptions(opts *RawFromResourceOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.Kusto, "kusto", opts.Kusto, "Azure Data Explorer cluster name (required)")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Azure Data Explorer cluster region (required)")
	cmd.Flags().StringVar(&opts.ServiceDatabase, "service-database", opts.ServiceDatabase, "Kusto database for service logs")
	cmd.Flags().StringVar(&opts.HCPDatabase, "hcp-database", opts.HCPDatabase, "Kusto database for hosted control plane logs")
	cmd.Flags().StringVar(&opts.ResourceGroup, "resource-group", opts.ResourceGroup, "Azure resource group name (required)")
	cmd.Flags().StringVar(&opts.StartTime, "start-time", opts.StartTime, "Query start time in RFC3339 format (required)")
	cmd.Flags().StringVar(&opts.EndTime, "end-time", opts.EndTime, "Query end time in RFC3339 format (required)")
	cmd.Flags().StringVar(&opts.OutputDir, "output-dir", opts.OutputDir, "Directory to write snapshot output")
	cmd.Flags().DurationVar(&opts.QueryTimeout, "query-timeout", opts.QueryTimeout, "Timeout for individual Kusto queries")

	for _, flag := range []string{"kusto", "region", "resource-group", "start-time", "end-time"} {
		if err := cmd.MarkFlagRequired(flag); err != nil {
			return fmt.Errorf("failed to mark %s as required: %w", flag, err)
		}
	}
	return nil
}

type validatedFromResourceOptions struct {
	kustoEndpoint   *url.URL
	serviceDatabase string
	hcpDatabase     string
	resourceGroup   string
	startTime       time.Time
	endTime         time.Time
	outputDir       string
	queryTimeout    time.Duration
}

func (o *RawFromResourceOptions) validate() (*validatedFromResourceOptions, error) {
	kustoEndpoint, err := kusto.KustoEndpoint(o.Kusto, o.Region)
	if err != nil {
		return nil, err
	}

	startTime, err := time.Parse(time.RFC3339, o.StartTime)
	if err != nil {
		return nil, fmt.Errorf("invalid --start-time %q: must be RFC3339 format: %w", o.StartTime, err)
	}
	endTime, err := time.Parse(time.RFC3339, o.EndTime)
	if err != nil {
		return nil, fmt.Errorf("invalid --end-time %q: must be RFC3339 format: %w", o.EndTime, err)
	}
	if !startTime.Before(endTime) {
		return nil, fmt.Errorf("--start-time must be before --end-time")
	}

	return &validatedFromResourceOptions{
		kustoEndpoint:   kustoEndpoint,
		serviceDatabase: o.ServiceDatabase,
		hcpDatabase:     o.HCPDatabase,
		resourceGroup:   o.ResourceGroup,
		startTime:       startTime,
		endTime:         endTime,
		outputDir:       o.OutputDir,
		queryTimeout:    o.QueryTimeout,
	}, nil
}

type completedFromResourceOptions struct {
	*validatedFromResourceOptions
	kustoClient *azkustodata.Client
}

func (o *validatedFromResourceOptions) complete() (*completedFromResourceOptions, error) {
	kcsb := azkustodata.NewConnectionStringBuilder(o.kustoEndpoint.String())
	kcsb = kcsb.WithDefaultAzureCredential()
	client, err := azkustodata.New(kcsb)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kusto client: %w", err)
	}
	return &completedFromResourceOptions{
		validatedFromResourceOptions: o,
		kustoClient:                  client,
	}, nil
}

func (o *completedFromResourceOptions) run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)
	defer func() {
		if err := o.kustoClient.Close(); err != nil {
			logger.Error(err, "Failed to close Kusto client")
		}
	}()

	gatherer := snapshotpkg.NewGatherer(o.kustoClient)
	input := snapshotpkg.GatherInput{
		ClusterURI:      o.kustoEndpoint.String(),
		ServiceDatabase: o.serviceDatabase,
		HCPDatabase:     o.hcpDatabase,
		ResourceGroup:   o.resourceGroup,
		TimeWindow: snapshotpkg.TimeWindow{
			Start: o.startTime,
			End:   o.endTime,
		},
		QueryTimeout: o.queryTimeout,
	}

	manifest, _, err := gatherer.Gather(ctx, input, o.outputDir)
	if err != nil {
		return err
	}

	logger.Info("Snapshot complete",
		"outputDir", o.outputDir,
		"resources", len(manifest.Resources),
	)

	return nil
}

func newFromResourceCommand() (*cobra.Command, error) {
	opts := defaultFromResourceOptions()
	cmd := &cobra.Command{
		Use:   "from-resource",
		Short: "Gather a diagnostic snapshot starting from a resource group and time window",
		Long: `Gather a structured diagnostic snapshot by querying all ARM requests in the
given resource group during the specified time window, then tracing each
mutating request through the full system stack.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			validated, err := opts.validate()
			if err != nil {
				return err
			}
			completed, err := validated.complete()
			if err != nil {
				return err
			}
			return completed.run(cmd.Context())
		},
	}
	if err := bindFromResourceOptions(opts, cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}
