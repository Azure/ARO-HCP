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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v8"
)

const (
	serviceTagUsageCollectorName = "service-tag-usage"
)

var (
	// ServiceTagUsageByPublicIpCountDesc is the descriptor for the public IP count metric by service tag
	ServiceTagUsageByPublicIpCountDesc = prometheus.NewDesc(
		"public_ip_count_by_region_service_tag",
		"Number of public IP addresses in a region by service tag",
		[]string{"region", "service_tag_type", "service_tag_value"},
		nil,
	)
)

// ServiceTagUsageCollector is a Prometheus collector that gathers public IP metrics from Azure
type ServiceTagUsageCollector struct {
	client           *armnetwork.PublicIPAddressesClient
	cache            *cache.MetricsCache
	name             string
	region           string
	runInDevelopment bool
}

var _ CachingCollector = &ServiceTagUsageCollector{}

// NewServiceTagUsageCollector creates a new ServiceTagUsageCollector
func NewServiceTagUsageCollector(subscriptionID string, region string, credential azcore.TokenCredential, cacheTTL time.Duration, runInDevelopment bool) (*ServiceTagUsageCollector, error) {
	var publicIPClient *armnetwork.PublicIPAddressesClient
	var err error
	if !runInDevelopment {
		publicIPClient, err = armnetwork.NewPublicIPAddressesClient(subscriptionID, credential, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create Azure network public IP addresses client: %w", err)
		}
	}
	return &ServiceTagUsageCollector{
		client:           publicIPClient,
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
	if c.runInDevelopment {
		publicIPs, err = ips.GetDummyPublicIPAddresses()
		if err != nil {
			logger.Error(err, "error collecting dummy public IP addresses")
			return
		}
	} else {
		publicIPs, err = ips.GetAllPublicIPAddresses(ctx, c.client, c.region)
		if err != nil {
			logger.Error(err, "error collecting public IP addresses")
			return
		}
	}

	// mapping of ServiceTagType to ServiceTagValue to count of PublicIPs
	serviceTypeTagCounts := make(map[string]map[string]int)

	for _, publicIP := range publicIPs {
		for _, serviceTag := range publicIP.ServiceTags {
			if _, ok := serviceTypeTagCounts[serviceTag.ServiceTagType]; !ok {
				serviceTypeTagCounts[serviceTag.ServiceTagType] = make(map[string]int)
			}
			serviceTypeTagCounts[serviceTag.ServiceTagType][serviceTag.ServiceTagValue]++
		}
	}
	for serviceTagType, serviceTagValueCounts := range serviceTypeTagCounts {
		for serviceTagValue, count := range serviceTagValueCounts {
			c.cache.AddMetric(prometheus.MustNewConstMetric(
				ServiceTagUsageByPublicIpCountDesc,
				prometheus.GaugeValue,
				float64(count),
				c.region,
				serviceTagType,
				serviceTagValue,
			))
		}
	}
}
