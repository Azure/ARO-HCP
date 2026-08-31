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

	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/cobra"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"

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
	validAzureRegion       = regexp.MustCompile(`^[a-z][a-z0-9]+$`)
	validClusterType       = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	validClusterNameFilter = regexp.MustCompile(`^[A-Za-z0-9_.\-]+$`)
	validKeyVaultName      = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{1,22}[A-Za-z0-9]$`)
	validCertificateName   = regexp.MustCompile(`^[A-Za-z0-9-]+$`)
	validDNSSuffix         = regexp.MustCompile(`^[a-z0-9]+(?:[.-][a-z0-9]+)*$`)
)

type RawOptions struct {
	ListenAddress       string
	ClusterTypes        []string
	Region              string
	ClusterNameFilter   string
	CacheTTL            time.Duration
	CollectionInterval  time.Duration
	EnabledCollectors   []string
	KustoCluster        string
	KustoRegion         string
	KustoQueryInterval  time.Duration
	KeyVaultName        string
	KeyVaultDNSSuffix   string
	CertificateNames    []string
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
		supportedCollectors: []string{metrics.ServiceTagUsageCollectorName, metrics.KustoLogsCurrentCollectorName, metrics.KeyVaultCertificateCollectorName},
		KustoCluster:        "",
		KustoRegion:         "",
		KustoQueryInterval:  metrics.KustoQueryInterval,
	}
}

type ValidatedOptions struct {
	ListenAddress      string
	ClusterTypes       []string
	Region             string
	ClusterNameFilter  string
	CacheTTL           time.Duration
	CollectionInterval time.Duration
	EnabledCollectors  []string
	KustoCluster       string
	KustoRegion        string
	KustoQueryInterval time.Duration
	KeyVaultName       string
	KeyVaultDNSSuffix  string
	CertificateNames   []string
}

type CompletedOptions struct {
	ListenAddress      string
	Region             string
	CacheTTL           time.Duration
	Registry           *prometheus.Registry
	Collectors         []metrics.CachingCollector
	CollectionInterval time.Duration
	ClusterPoller      *cluster.ClusterDiscoveryPoller
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

	clusterNameFilter := strings.TrimSpace(o.ClusterNameFilter)
	if clusterNameFilter != "" && !validClusterNameFilter.MatchString(clusterNameFilter) {
		return nil, fmt.Errorf("invalid cluster-name-filter %q: must match %s", o.ClusterNameFilter, validClusterNameFilter.String())
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

	keyVaultName := strings.TrimSpace(o.KeyVaultName)
	keyVaultDNSSuffix := strings.ToLower(strings.TrimSpace(o.KeyVaultDNSSuffix))
	var certificateNames []string
	if slices.Contains(o.EnabledCollectors, metrics.KeyVaultCertificateCollectorName) {
		certificateNames = make([]string, 0, len(o.CertificateNames))
		if keyVaultName == "" {
			return nil, fmt.Errorf("keyvault-name is required when %s collector is enabled", metrics.KeyVaultCertificateCollectorName)
		}
		if !validKeyVaultName.MatchString(keyVaultName) || strings.Contains(keyVaultName, "--") {
			return nil, fmt.Errorf("invalid keyvault-name %q: must be 3-24 alphanumeric or hyphen characters, start with a letter, end with a letter or number, and not contain consecutive hyphens", keyVaultName)
		}
		if keyVaultDNSSuffix == "" {
			return nil, fmt.Errorf("keyvault-dns-suffix is required when %s collector is enabled", metrics.KeyVaultCertificateCollectorName)
		}
		if !validDNSSuffix.MatchString(keyVaultDNSSuffix) {
			return nil, fmt.Errorf("invalid keyvault-dns-suffix %q: must match %s", o.KeyVaultDNSSuffix, validDNSSuffix.String())
		}
		if len(o.CertificateNames) == 0 {
			return nil, fmt.Errorf("certificate-names is required when %s collector is enabled", metrics.KeyVaultCertificateCollectorName)
		}

		seenCertificateNames := make(map[string]struct{}, len(o.CertificateNames))
		for _, certificateName := range o.CertificateNames {
			certificateName = strings.TrimSpace(certificateName)
			if certificateName == "" {
				return nil, fmt.Errorf("certificate-names must not contain empty values")
			}
			if !validCertificateName.MatchString(certificateName) {
				return nil, fmt.Errorf("invalid certificate name %q: must match %s", certificateName, validCertificateName.String())
			}
			canonicalCertificateName := strings.ToLower(certificateName)
			if _, ok := seenCertificateNames[canonicalCertificateName]; ok {
				return nil, fmt.Errorf("duplicate certificate name %q", certificateName)
			}
			seenCertificateNames[canonicalCertificateName] = struct{}{}
			certificateNames = append(certificateNames, certificateName)
		}
	}

	return &ValidatedOptions{
		ListenAddress:      o.ListenAddress,
		ClusterTypes:       clusterTypes,
		Region:             region,
		ClusterNameFilter:  clusterNameFilter,
		CacheTTL:           o.CacheTTL,
		CollectionInterval: o.CollectionInterval,
		EnabledCollectors:  o.EnabledCollectors,
		KustoCluster:       o.KustoCluster,
		KustoRegion:        o.KustoRegion,
		KustoQueryInterval: o.KustoQueryInterval,
		KeyVaultName:       keyVaultName,
		KeyVaultDNSSuffix:  keyVaultDNSSuffix,
		CertificateNames:   certificateNames,
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

	clusterPoller := cluster.NewClusterDiscoveryPoller(rgClient, o.Region, o.ClusterTypes, o.ClusterNameFilter, o.CollectionInterval)

	collectors, err := o.CreateEnabledCollectors(ctx, cred, clusterPoller)
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
		ClusterPoller:      clusterPoller,
	}, nil
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.ListenAddress, "listen-address", opts.ListenAddress, fmt.Sprintf("Address to listen on for metrics (default: %s)", DefaultListenAddress))
	cmd.Flags().StringSliceVar(&opts.ClusterTypes, "cluster-types", opts.ClusterTypes, "AKS cluster type tag values for Resource Graph discovery")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Azure region this exporter is deployed in")
	cmd.Flags().StringVar(&opts.ClusterNameFilter, "cluster-name-filter", opts.ClusterNameFilter, "Filter discovered clusters to those whose name contains this substring")
	cmd.Flags().DurationVar(&opts.CacheTTL, "cache-ttl", opts.CacheTTL, fmt.Sprintf("Cache TTL (default: %s)", DefaultCacheTTL.String()))
	cmd.Flags().DurationVar(&opts.CollectionInterval, "collection-interval", opts.CollectionInterval, fmt.Sprintf("Collection interval (default: %s)", DefaultCollectionInterval.String()))
	cmd.Flags().StringSliceVar(&opts.EnabledCollectors, "enabled-collectors", opts.EnabledCollectors, fmt.Sprintf("Enabled collectors (default: %s)", strings.Join(opts.EnabledCollectors, ", ")))
	cmd.Flags().StringVar(&opts.KustoCluster, "kusto-cluster", opts.KustoCluster, "Azure Data Explorer (Kusto) cluster name")
	cmd.Flags().StringVar(&opts.KustoRegion, "kusto-region", opts.KustoRegion, "Azure Data Explorer (Kusto) region")
	cmd.Flags().DurationVar(&opts.KustoQueryInterval, "kusto-query-interval", opts.KustoQueryInterval, fmt.Sprintf("Kusto query interval (default: %s)", metrics.KustoQueryInterval.String()))
	cmd.Flags().StringVar(&opts.KeyVaultName, "keyvault-name", opts.KeyVaultName, "Azure Key Vault name containing certificates to monitor")
	cmd.Flags().StringVar(&opts.KeyVaultDNSSuffix, "keyvault-dns-suffix", opts.KeyVaultDNSSuffix, "Azure Key Vault DNS suffix")
	cmd.Flags().StringSliceVar(&opts.CertificateNames, "certificate-names", opts.CertificateNames, "Key Vault certificate names to monitor")

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

func (o *ValidatedOptions) CreateEnabledCollectors(ctx context.Context, creds azcore.TokenCredential, clusterPoller *cluster.ClusterDiscoveryPoller) ([]metrics.CachingCollector, error) {
	var collectors []metrics.CachingCollector
	for _, collector := range o.EnabledCollectors {
		switch collector {
		case metrics.ServiceTagUsageCollectorName:
			errorCounter := collectorErrorsTotal.WithLabelValues(metrics.ServiceTagUsageCollectorName)
			publicIPCollector := metrics.NewServiceTagUsageCollector(clusterPoller, o.Region, creds, o.CacheTTL, errorCounter)
			collectors = append(collectors, publicIPCollector)
		case metrics.KustoLogsCurrentCollectorName:
			errorCounter := collectorErrorsTotal.WithLabelValues(metrics.KustoLogsCurrentCollectorName)
			kustoCollector, err := metrics.NewKustoLogsCurrentCollector(o.KustoCluster, o.KustoRegion, clusterPoller, o.CacheTTL, errorCounter)
			if err != nil {
				return nil, fmt.Errorf("failed to create Kusto logs collector: %w", err)
			}
			collectors = append(collectors, kustoCollector)
		case metrics.KeyVaultCertificateCollectorName:
			vaultURL := fmt.Sprintf("https://%s.%s", o.KeyVaultName, o.KeyVaultDNSSuffix)
			certificateClient, err := azcertificates.NewClient(vaultURL, creds, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to create Key Vault certificate client: %w", err)
			}
			errorCounter := collectorErrorsTotal.WithLabelValues(metrics.KeyVaultCertificateCollectorName)
			certificateCollector := metrics.NewKeyVaultCertificateCollector(
				certificateClient,
				o.KeyVaultName,
				o.Region,
				o.CertificateNames,
				o.CacheTTL,
				errorCounter,
			)
			collectors = append(collectors, certificateCollector)
		}
	}
	return collectors, nil
}
