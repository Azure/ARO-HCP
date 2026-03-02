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
	SubscriptionNames   []string
	Region              string
	CacheTTL            time.Duration
	CollectionInterval  time.Duration
	EnabledCollectors   []string
	supportedCollectors []string
}

func DefaultOptions() *RawOptions {
	return &RawOptions{
		ListenAddress:       DefaultListenAddress,
		SubscriptionNames:   []string{},
		Region:              "",
		CacheTTL:            DefaultCacheTTL,
		CollectionInterval:  DefaultCollectionInterval,
		EnabledCollectors:   []string{metrics.ServiceTagUsageCollectorName},
		supportedCollectors: []string{metrics.ServiceTagUsageCollectorName},
	}
}

type ValidatedOptions struct {
	ListenAddress      string
	SubscriptionNames  []string
	Region             string
	CacheTTL           time.Duration
	CollectionInterval time.Duration
	EnabledCollectors  []string
}

type CompletedOptions struct {
	ListenAddress      string
	SubscriptionNames  []string
	Region             string
	CacheTTL           time.Duration
	Registry           *prometheus.Registry
	Collectors         []metrics.CachingCollector
	CollectionInterval time.Duration
}

func (o *RawOptions) Validate(ctx context.Context) (*ValidatedOptions, error) {
	if len(o.SubscriptionNames) == 0 {
		return nil, fmt.Errorf("subscription IDs are required")
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
		SubscriptionNames:  o.SubscriptionNames,
		Region:             o.Region,
		CacheTTL:           o.CacheTTL,
		CollectionInterval: o.CollectionInterval,
		EnabledCollectors:  o.EnabledCollectors,
	}, nil
}

func (o *ValidatedOptions) Complete(ctx context.Context) (*CompletedOptions, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}
	collectors, err := metrics.CreateEnabledCollectors(ctx, o.SubscriptionNames, cred, o.CacheTTL, o.EnabledCollectors)
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
		SubscriptionNames:  o.SubscriptionNames,
		Region:             o.Region,
		CacheTTL:           o.CacheTTL,
		Registry:           registry,
		Collectors:         collectors,
		CollectionInterval: o.CollectionInterval,
	}, nil
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.ListenAddress, "listen-address", opts.ListenAddress, fmt.Sprintf("Address to listen on for metrics (default: %s)", DefaultListenAddress))
	cmd.Flags().StringSliceVar(&opts.SubscriptionNames, "subscription-names", opts.SubscriptionNames, "Azure subscription names")
	cmd.Flags().DurationVar(&opts.CacheTTL, "cache-ttl", opts.CacheTTL, fmt.Sprintf("Cache TTL (default: %s)", DefaultCacheTTL.String()))
	cmd.Flags().DurationVar(&opts.CollectionInterval, "collection-interval", opts.CollectionInterval, fmt.Sprintf("Collection interval (default: %s)", DefaultCollectionInterval.String()))
	cmd.Flags().StringSliceVar(&opts.EnabledCollectors, "enabled-collectors", opts.EnabledCollectors, fmt.Sprintf("Enabled collectors (default: %s)", strings.Join(opts.supportedCollectors, ", ")))

	err := cmd.MarkFlagRequired("subscription-names")
	if err != nil {
		return fmt.Errorf("failed to mark flag %q as required: %w", "subscription-names", err)
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
