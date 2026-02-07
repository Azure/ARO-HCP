package cmd

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/tooling/aro-hcp-exporter/pkg/metrics"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/cobra"
)

const (
	ServiceTagUsageCollector  = "service-tag-usage"
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
	RunInDevelopment    bool
	supportedCollectors []string
}

func DefaultOptions() *RawOptions {
	return &RawOptions{
		ListenAddress:       DefaultListenAddress,
		SubscriptionID:      "",
		Region:              "",
		CacheTTL:            DefaultCacheTTL,
		CollectionInterval:  DefaultCollectionInterval,
		EnabledCollectors:   []string{ServiceTagUsageCollector},
		RunInDevelopment:    false,
		supportedCollectors: []string{ServiceTagUsageCollector},
	}
}

type ValidatedOptions struct {
	ListenAddress      string
	SubscriptionID     string
	Region             string
	CacheTTL           time.Duration
	CollectionInterval time.Duration
	EnabledCollectors  []string
	RunInDevelopment   bool
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
		RunInDevelopment:   o.RunInDevelopment,
	}, nil
}

func (o *ValidatedOptions) Complete(ctx context.Context) (*CompletedOptions, error) {
	logger := logr.FromContextOrDiscard(ctx)
	if o.RunInDevelopment {
		logger.Info("Running in development mode", "runInDevelopment", o.RunInDevelopment)
	}

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}
	registry := prometheus.NewRegistry()

	var collectors []metrics.CachingCollector
	for _, collector := range o.EnabledCollectors {
		switch collector {
		case ServiceTagUsageCollector:
			publicIPCollector, err := metrics.NewServiceTagUsageCollector(o.SubscriptionID, o.Region, cred, o.CacheTTL, o.RunInDevelopment)
			if err != nil {
				return nil, fmt.Errorf("failed to create public IP collector: %w", err)
			}
			collectors = append(collectors, publicIPCollector)
			registry.MustRegister(publicIPCollector)
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
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Azure region")
	cmd.Flags().DurationVar(&opts.CacheTTL, "cache-ttl", opts.CacheTTL, fmt.Sprintf("Cache TTL (default: %s)", DefaultCacheTTL.String()))
	cmd.Flags().DurationVar(&opts.CollectionInterval, "collection-interval", opts.CollectionInterval, fmt.Sprintf("Collection interval (default: %s)", DefaultCollectionInterval.String()))
	cmd.Flags().StringSliceVar(&opts.EnabledCollectors, "enabled-collectors", opts.EnabledCollectors, fmt.Sprintf("Enabled collectors (default: %s)", strings.Join(opts.supportedCollectors, ", ")))
	cmd.Flags().BoolVar(&opts.RunInDevelopment, "run-in-development", opts.RunInDevelopment, "Run in development mode")

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
