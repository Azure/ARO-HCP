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

package metrics

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/Azure/ARO-HCP/tooling/aro-hcp-exporter/internal/ips"
	"github.com/Azure/ARO-HCP/tooling/aro-hcp-exporter/pkg/cache"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
)

const (
	serviceTagUsageCollectorName = "service-tag-usage"
)

var (
	// ServiceTagUsageByPublicIpCountDesc is the descriptor for the public IP count metric by service tag
	ServiceTagUsageByPublicIpCountDesc = prometheus.NewDesc(
		"public_ip_count_by_region_service_tag",
		"Number of public IP addresses in a region by service tag",
		[]string{"subscription_id", "region", "service_tag_type", "service_tag_value"},
		nil,
	)
)

// ServiceTagUsageCollector is a Prometheus collector that gathers public IP metrics from Azure
type ServiceTagUsageCollector struct {
	client           *armresourcegraph.Client
	cache            *cache.MetricsCache
	name             string
	region           string
	runInDevelopment bool
}

var _ CachingCollector = &ServiceTagUsageCollector{}

// NewServiceTagUsageCollector creates a new ServiceTagUsageCollector
func NewServiceTagUsageCollector(subscriptionID string, region string, credential azcore.TokenCredential, cacheTTL time.Duration, runInDevelopment bool) (*ServiceTagUsageCollector, error) {
	var resourceGraphClient *armresourcegraph.Client
	var err error
	resourceGraphClient, err = armresourcegraph.NewClient(credential, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Resource Graph client: %w", err)
	}

	return &ServiceTagUsageCollector{
		client:           resourceGraphClient,
		cache:            cache.NewMetricsCache(cacheTTL),
		runInDevelopment: runInDevelopment,
		region:           region,
	}, nil
}

func (c *ServiceTagUsageCollector) Name() string {
	return serviceTagUsageCollectorName
}

func (c *ServiceTagUsageCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- ServiceTagUsageByPublicIpCountDesc
}

func (c *ServiceTagUsageCollector) Collect(ch chan<- prometheus.Metric) {
	c.cache.GetAllMetrics()
	for _, metric := range c.cache.GetAllMetrics() {
		ch <- metric
	}
}

func (c *ServiceTagUsageCollector) CollectMetricValues(ctx context.Context) {
	logger := logr.FromContextOrDiscard(ctx)

	var publicIPs []ips.PublicIPAddress
	var err error

	publicIPs, err = ips.DiscoverPublicIPAddresses(ctx, c.client)
	if err != nil {
		logger.Error(err, "error collecting public IP addresses")
		return
	}

	for _, publicIP := range publicIPs {
		tag_type := ""
		tag_value := ""
		if len(publicIP.ServiceTags) > 0 {
			tag_type = publicIP.ServiceTags[0].ServiceTagType
			tag_value = publicIP.ServiceTags[0].ServiceTagValue
		}
		c.cache.AddMetric(prometheus.MustNewConstMetric(
			ServiceTagUsageByPublicIpCountDesc,
			prometheus.GaugeValue,
			publicIP.Count,
			publicIP.SubscriptionId,
			publicIP.Location,
			tag_type,
			tag_value,
		))
	}
}
