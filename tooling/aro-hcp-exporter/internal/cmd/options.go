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

package cmd

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/cobra"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/tooling/aro-hcp-exporter/internal/cluster"
	"github.com/Azure/ARO-HCP/tooling/aro-hcp-exporter/internal/metrics"
	"github.com/Azure/ARO-HCP/tooling/aro-hcp-exporter/pkg/graphquery"
)

const (
	DefaultListenAddress      = ":8080"
	DefaultCacheTTL           = 1 * time.Minute
	DefaultCollectionInterval = 1 * time.Minute
)

var (
	validAzureRegion = regexp.MustCompile(`^[a-z][a-z0-9]+$`)
	validClusterType = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
)

type RawOptions struct {
	ListenAddress       string
	ClusterTypes        []string
	Region              string
	CacheTTL            time.Duration
	CollectionInterval  time.Duration
	EnabledCollectors   []string
	KustoCluster        string
	KustoRegion         string
	KustoQueryInterval  time.Duration
	supportedCollectors []string
}

func DefaultOptions() *RawOptions {
	return &RawOptions{
		ListenAddress:       DefaultListenAddress,
		ClusterTypes:        []string{},
		Region:              "",
		CacheTTL:            DefaultCacheTTL,
		CollectionInterval:  DefaultCollectionInterval,
		EnabledCollectors:   []string{metrics.ServiceTagUsageCollectorName, metrics.KustoLogsCurrentCollectorName},
		supportedCollectors: []string{metrics.ServiceTagUsageCollectorName, metrics.KustoLogsCurrentCollectorName},
		KustoCluster:        "",
		KustoRegion:         "",
		KustoQueryInterval:  metrics.KustoQueryInterval,
	}
}

type ValidatedOptions struct {
	ListenAddress      string
	ClusterTypes       []string
	Region             string
	CacheTTL           time.Duration
	CollectionInterval time.Duration
	EnabledCollectors  []string
	KustoCluster       string
	KustoRegion        string
	KustoQueryInterval time.Duration
}

type CompletedOptions struct {
	ListenAddress      string
	Region             string
	CacheTTL           time.Duration
	Registry           *prometheus.Registry
	Collectors         []metrics.CachingCollector
	CollectionInterval time.Duration
}

func (o *RawOptions) Validate(ctx context.Context) (*ValidatedOptions, error) {
	if len(o.ClusterTypes) == 0 {
		return nil, fmt.Errorf("cluster-types is required")
	}

	clusterTypes := make([]string, 0, len(o.ClusterTypes))
	for _, ct := range o.ClusterTypes {
		ct = strings.TrimSpace(ct)
		if ct == "" {
			return nil, fmt.Errorf("cluster-types must not contain empty values")
		}
		if !validClusterType.MatchString(ct) {
			return nil, fmt.Errorf("invalid cluster-type %q: must match %s", ct, validClusterType.String())
		}
		clusterTypes = append(clusterTypes, ct)
	}

	if o.Region == "" {
		return nil, fmt.Errorf("region is required")
	}

	region := strings.ToLower(o.Region)
	if !validAzureRegion.MatchString(region) {
		return nil, fmt.Errorf("invalid region %q: must be a lowercase Azure region name (e.g. eastus, westus3)", o.Region)
	}

	if o.CacheTTL == 0 {
		return nil, fmt.Errorf("cache TTL is required")
	}

	if len(o.EnabledCollectors) == 0 {
		return nil, fmt.Errorf("at least one collector must be enabled")
	}

	for _, collector := range o.EnabledCollectors {
		if !slices.Contains(o.supportedCollectors, collector) {
			return nil, fmt.Errorf("invalid collector: %s", collector)
		}
	}

	return &ValidatedOptions{
		ListenAddress:      o.ListenAddress,
		ClusterTypes:       clusterTypes,
		Region:             region,
		CacheTTL:           o.CacheTTL,
		CollectionInterval: o.CollectionInterval,
		EnabledCollectors:  o.EnabledCollectors,
		KustoCluster:       o.KustoCluster,
		KustoRegion:        o.KustoRegion,
		KustoQueryInterval: o.KustoQueryInterval,
	}, nil
}

func (o *ValidatedOptions) Complete(ctx context.Context) (*CompletedOptions, error) {
	cred, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{RequireAzureTokenCredentials: true})
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}

	rgClient, err := graphquery.NewResourceGraphClient(cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Resource Graph client: %w", err)
	}

	discovered, err := cluster.Discover(ctx, rgClient, o.Region, o.ClusterTypes)
	if err != nil {
		return nil, fmt.Errorf("failed to discover clusters: %w", err)
	}

	logger := logr.FromContextOrDiscard(ctx)
	logger.Info("discovered AKS clusters",
		"clusterNames", discovered.ClusterNames,
		"subscriptionIDs", discovered.SubscriptionIDs,
	)

	collectors, err := o.CreateEnabledCollectors(ctx, cred, discovered.SubscriptionIDs, discovered.ClusterNames)
	if err != nil {
		return nil, fmt.Errorf("failed to create collectors: %w", err)
	}
	registry := prometheus.NewRegistry()
	for _, collector := range collectors {
		if regErr := registry.Register(collector); regErr != nil {
			return nil, fmt.Errorf("failed to register collector: %s, error: %w", collector.Name(), regErr)
		}
	}

	return &CompletedOptions{
		ListenAddress:      o.ListenAddress,
		Region:             o.Region,
		CacheTTL:           o.CacheTTL,
		Registry:           registry,
		Collectors:         collectors,
		CollectionInterval: o.CollectionInterval,
	}, nil
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.ListenAddress, "listen-address", opts.ListenAddress, fmt.Sprintf("Address to listen on for metrics (default: %s)", DefaultListenAddress))
	cmd.Flags().StringSliceVar(&opts.ClusterTypes, "cluster-types", opts.ClusterTypes, "AKS cluster type tag values for Resource Graph discovery")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Azure region this exporter is deployed in")
	cmd.Flags().DurationVar(&opts.CacheTTL, "cache-ttl", opts.CacheTTL, fmt.Sprintf("Cache TTL (default: %s)", DefaultCacheTTL.String()))
	cmd.Flags().DurationVar(&opts.CollectionInterval, "collection-interval", opts.CollectionInterval, fmt.Sprintf("Collection interval (default: %s)", DefaultCollectionInterval.String()))
	cmd.Flags().StringSliceVar(&opts.EnabledCollectors, "enabled-collectors", opts.EnabledCollectors, fmt.Sprintf("Enabled collectors (default: %s)", strings.Join(opts.supportedCollectors, ", ")))
	cmd.Flags().StringVar(&opts.KustoCluster, "kusto-cluster", opts.KustoCluster, "Azure Data Explorer (Kusto) cluster name")
	cmd.Flags().StringVar(&opts.KustoRegion, "kusto-region", opts.KustoRegion, "Azure Data Explorer (Kusto) region")
	cmd.Flags().DurationVar(&opts.KustoQueryInterval, "kusto-query-interval", opts.KustoQueryInterval, fmt.Sprintf("Kusto query interval (default: %s)", metrics.KustoQueryInterval.String()))

	err := cmd.MarkFlagRequired("cluster-types")
	if err != nil {
		return fmt.Errorf("failed to mark flag %q as required: %w", "cluster-types", err)
	}
	err = cmd.MarkFlagRequired("region")
	if err != nil {
		return fmt.Errorf("failed to mark flag %q as required: %w", "region", err)
	}
	return nil
}

func (o *RawOptions) Run(ctx context.Context) error {
	validated, err := o.Validate(ctx)
	if err != nil {
		return err
	}

	completed, err := validated.Complete(ctx)
	if err != nil {
		return err
	}

	return completed.Run(ctx)
}

func (o *ValidatedOptions) CreateEnabledCollectors(ctx context.Context, creds azcore.TokenCredential, subscriptionIDs, clusterNames []string) ([]metrics.CachingCollector, error) {
	var collectors []metrics.CachingCollector
	for _, collector := range o.EnabledCollectors {
		switch collector {
		case metrics.ServiceTagUsageCollectorName:
			errorCounter := collectorErrorsTotal.WithLabelValues(metrics.ServiceTagUsageCollectorName)
			publicIPCollector, err := metrics.NewServiceTagUsageCollector(subscriptionIDs, o.Region, creds, o.CacheTTL, errorCounter)
			if err != nil {
				return nil, fmt.Errorf("failed to create public IP collector: %w", err)
			}
			collectors = append(collectors, publicIPCollector)
		case metrics.KustoLogsCurrentCollectorName:
			errorCounter := collectorErrorsTotal.WithLabelValues(metrics.KustoLogsCurrentCollectorName)
			kustoCollector, err := metrics.NewKustoLogsCurrentCollector(o.KustoCluster, o.KustoRegion, clusterNames, o.CacheTTL, errorCounter)
			if err != nil {
				return nil, fmt.Errorf("failed to create Kusto logs collector: %w", err)
			}
			collectors = append(collectors, kustoCollector)
		}
	}
	return collectors, nil
}
