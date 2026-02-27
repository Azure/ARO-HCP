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
	"slices"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/cobra"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/tooling/aro-hcp-exporter/internal/metrics"
)

const (
	DefaultListenAddress      = ":8080"
	DefaultCacheTTL           = 1 * time.Minute
	DefaultCollectionInterval = 1 * time.Minute
)

type RawOptions struct {
	ListenAddress       string
	SubscriptionID      string
	Region              string
	CacheTTL            time.Duration
	CollectionInterval  time.Duration
	EnabledCollectors   []string
	KustoCluster        string
	KustoRegion         string
	KustoQueryInterval  time.Duration
	ClusterNames        []string
	supportedCollectors []string
}

func DefaultOptions() *RawOptions {
	return &RawOptions{
		ListenAddress:       DefaultListenAddress,
		SubscriptionID:      "",
		Region:              "",
		CacheTTL:            DefaultCacheTTL,
		CollectionInterval:  DefaultCollectionInterval,
		EnabledCollectors:   []string{metrics.ServiceTagUsageCollectorName, metrics.KustoLogsCurrentCollectorName},
		supportedCollectors: []string{metrics.ServiceTagUsageCollectorName, metrics.KustoLogsCurrentCollectorName},
		KustoCluster:        "",
		KustoRegion:         "",
		KustoQueryInterval:  metrics.KustoQueryInterval,
		ClusterNames:        []string{},
	}
}

type ValidatedOptions struct {
	ListenAddress      string
	SubscriptionID     string
	Region             string
	CacheTTL           time.Duration
	CollectionInterval time.Duration
	EnabledCollectors  []string
	KustoCluster       string
	KustoRegion        string
	KustoQueryInterval time.Duration
	ClusterNames       []string
}

type CompletedOptions struct {
	ListenAddress      string
	SubscriptionID     string
	Region             string
	CacheTTL           time.Duration
	Registry           *prometheus.Registry
	Collectors         []metrics.CachingCollector
	CollectionInterval time.Duration
}

func (o *RawOptions) Validate(ctx context.Context) (*ValidatedOptions, error) {
	if o.SubscriptionID == "" {
		return nil, fmt.Errorf("subscription ID is required")
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
		SubscriptionID:     o.SubscriptionID,
		Region:             o.Region,
		CacheTTL:           o.CacheTTL,
		CollectionInterval: o.CollectionInterval,
		EnabledCollectors:  o.EnabledCollectors,
		KustoCluster:       o.KustoCluster,
		KustoRegion:        o.KustoRegion,
		KustoQueryInterval: o.KustoQueryInterval,
		ClusterNames:       o.ClusterNames,
	}, nil
}

func (o *ValidatedOptions) Complete(ctx context.Context) (*CompletedOptions, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}
	collectors, err := o.CreateEnabledCollectors(ctx, cred)
	if err != nil {
		return nil, fmt.Errorf("failed to create collectors: %w", err)
	}
	registry := prometheus.NewRegistry()
	for _, collector := range collectors {
		if registry.Register(collector) != nil {
			return nil, fmt.Errorf("failed to register collector: %s, error: %w", collector.Name(), err)
		}
	}

	return &CompletedOptions{
		ListenAddress:      o.ListenAddress,
		SubscriptionID:     o.SubscriptionID,
		Region:             o.Region,
		CacheTTL:           o.CacheTTL,
		Registry:           registry,
		Collectors:         collectors,
		CollectionInterval: o.CollectionInterval,
	}, nil
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.ListenAddress, "listen-address", opts.ListenAddress, fmt.Sprintf("Address to listen on for metrics (default: %s)", DefaultListenAddress))
	cmd.Flags().StringVar(&opts.SubscriptionID, "subscription-id", opts.SubscriptionID, "Azure subscription ID")
	cmd.Flags().DurationVar(&opts.CacheTTL, "cache-ttl", opts.CacheTTL, fmt.Sprintf("Cache TTL (default: %s)", DefaultCacheTTL.String()))
	cmd.Flags().DurationVar(&opts.CollectionInterval, "collection-interval", opts.CollectionInterval, fmt.Sprintf("Collection interval (default: %s)", DefaultCollectionInterval.String()))
	cmd.Flags().StringSliceVar(&opts.EnabledCollectors, "enabled-collectors", opts.EnabledCollectors, fmt.Sprintf("Enabled collectors (default: %s)", strings.Join(opts.supportedCollectors, ", ")))
	cmd.Flags().StringVar(&opts.KustoCluster, "kusto-cluster", opts.KustoCluster, "Azure Data Explorer (Kusto) cluster name")
	cmd.Flags().StringVar(&opts.KustoRegion, "kusto-region", opts.KustoRegion, "Azure Data Explorer (Kusto) region")
	cmd.Flags().DurationVar(&opts.KustoQueryInterval, "kusto-query-interval", opts.KustoQueryInterval, fmt.Sprintf("Kusto query interval (default: %s)", metrics.KustoQueryInterval.String()))
	cmd.Flags().StringSliceVar(&opts.ClusterNames, "cluster-names", opts.ClusterNames, "Cluster names")

	err := cmd.MarkFlagRequired("subscription-id")
	if err != nil {
		return fmt.Errorf("failed to mark flag %q as required: %w", "subscription-id", err)
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

// CreateEnabledCollectors creates a list of enabled collectors
// It iterates over the enabled collectors and creates the corresponding collector
func (o *ValidatedOptions) CreateEnabledCollectors(ctx context.Context, creds azcore.TokenCredential) ([]metrics.CachingCollector, error) {
	var collectors []metrics.CachingCollector
	for _, collector := range o.EnabledCollectors {
		switch collector {
		case metrics.ServiceTagUsageCollectorName:
			publicIPCollector, err := metrics.NewServiceTagUsageCollector(o.SubscriptionID, creds, o.CacheTTL)
			if err != nil {
				return nil, fmt.Errorf("failed to create public IP collector: %w", err)
			}
			collectors = append(collectors, publicIPCollector)
		case metrics.KustoLogsCurrentCollectorName:
			kustoCollector, err := metrics.NewKustoLogsCurrentCollector(o.KustoCluster, o.KustoRegion, o.ClusterNames, o.CacheTTL)
			if err != nil {
				return nil, fmt.Errorf("failed to create Kusto logs collector: %w", err)
			}
			collectors = append(collectors, kustoCollector)
		}
	}
	return collectors, nil
}
