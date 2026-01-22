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
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	"github.com/Azure/ARO-HCP/tooling/aro-hcp-exporter/pkg/cache"
	"github.com/Azure/ARO-HCP/tooling/aro-hcp-exporter/pkg/graphquery"
)

const (
	ServiceTagUsageCollectorName = "service-tag-usage"
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
	client *graphquery.ResourceGraphClient
	cache  *cache.MetricsCache
}

var _ CachingCollector = &ServiceTagUsageCollector{}

// NewServiceTagUsageCollector creates a new ServiceTagUsageCollector
func NewServiceTagUsageCollector(subscriptionID string, credential azcore.TokenCredential, cacheTTL time.Duration) (*ServiceTagUsageCollector, error) {
	var resourceGraphClient *graphquery.ResourceGraphClient
	var err error
	resourceGraphClient, err = graphquery.NewResourceGraphClient(credential, []*string{to.Ptr(subscriptionID)})
	if err != nil {
		return nil, fmt.Errorf("failed to create Resource Graph client: %w", err)
	}

	return &ServiceTagUsageCollector{
		client: resourceGraphClient,
		cache:  cache.NewMetricsCache(cacheTTL),
	}, nil
}

func (c *ServiceTagUsageCollector) Name() string {
	return ServiceTagUsageCollectorName
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

type IPTag struct {
	ServiceTagType  string
	ServiceTagValue string
}

type PublicIPAddress struct {
	Location       string
	IpTagsString   string
	SubscriptionId string
	Count          float64
}

var query = `
resources
| where type == 'microsoft.network/publicipaddresses'
| extend ipTagsString = tostring(properties['ipTags'])
| summarize Count=count()  by  subscriptionId, location, ipTagsString
`

func parseIPTags(ipTagsAsString string) ([]IPTag, error) {
	ipTags := []IPTag{}

	if ipTagsAsString == "" {
		return ipTags, nil
	}
	err := json.Unmarshal([]byte(ipTagsAsString), &ipTags)
	if err != nil {
		return nil, fmt.Errorf("error parsing IPs %s, %w", ipTagsAsString, err)
	}
	return ipTags, nil
}

func (c *ServiceTagUsageCollector) CollectMetricValues(ctx context.Context) {
	logger := logr.FromContextOrDiscard(ctx)

	var publicIPs []PublicIPAddress
	var err error

	err = c.client.ExecuteConvertRequest(ctx, graphquery.ResourceGraphRequest{
		Query:  &query,
		Output: &publicIPs,
	})
	if err != nil {
		logger.Error(err, "error collecting public IP addresses")
		return
	}

	for _, publicIP := range publicIPs {
		var ipTags []IPTag
		if publicIP.IpTagsString != "[]" {
			ipTags, err = parseIPTags(publicIP.IpTagsString)
			if err != nil {
				logger.Error(err, "error parsing IP tags")
				continue
			}
		} else {
			ipTags = []IPTag{
				{
					ServiceTagType:  "not-set",
					ServiceTagValue: "not-set",
				},
			}
		}
		for _, ipTag := range ipTags {
			err = c.cache.AddMetric(prometheus.MustNewConstMetric(
				ServiceTagUsageByPublicIpCountDesc,
				prometheus.GaugeValue,
				publicIP.Count,
				publicIP.SubscriptionId,
				publicIP.Location,
				ipTag.ServiceTagType,
				ipTag.ServiceTagValue,
			))
			if err != nil {
				logger.Error(err, "error adding metric to cache")
				continue
			}
		}
	}
}
