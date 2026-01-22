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
	"log"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Azure/ARO-HCP/tooling/aro-hcp-exporter/internal/ips"
)

var (
	// PublicIPCountByServiceTagDesc is the descriptor for the public IP count metric by service tag
	PublicIPCountByServiceTagDesc = prometheus.NewDesc(
		"aro_hcp_public_ip_count_by_service_tag",
		"Number of public IP addresses configured for each service tag",
		[]string{"service_tag", "subscription_id", "resource_group", "location"},
		nil,
	)

	// PublicIPTotalDesc is the descriptor for the total public IP count metric
	PublicIPTotalDesc = prometheus.NewDesc(
		"aro_hcp_public_ip_total",
		"Total number of public IP addresses",
		[]string{"subscription_id"},
		nil,
	)
)

// PublicIPCollector is a Prometheus collector that gathers public IP metrics from Azure
type PublicIPCollector struct {
	client ips.PublicIPAddressClient
	mutex  sync.RWMutex
}

// NewPublicIPCollector creates a new PublicIPCollector
func NewPublicIPCollector(client ips.PublicIPAddressClient) *PublicIPCollector {
	return &PublicIPCollector{
		client: client,
	}
}

// Describe implements prometheus.Collector
func (c *PublicIPCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- PublicIPCountByServiceTagDesc
	ch <- PublicIPTotalDesc
}

// Collect implements prometheus.Collector
func (c *PublicIPCollector) Collect(ch chan<- prometheus.Metric) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	// Get all public IP addresses
	ctx := context.Background()
	publicIPs, err := ips.GetAllPublicIPAddresses(ctx, c.client)
	if err != nil {
		log.Printf("Error collecting public IP addresses: %v", err)
		return
	}

	// Count IPs by service tag
	serviceTagCounts := make(map[string]map[string]int) // service_tag -> {subscription_id:resource_group:location -> count}
	subscriptionTotals := make(map[string]int)         // subscription_id -> total count

	for _, ip := range publicIPs {
		// Count total per subscription
		subscriptionTotals[ip.SubscriptionID]++

		// Extract service tag (commonly used tag names for service identification)
		serviceTag := extractServiceTag(ip.Tags)

		// Create a unique key for subscription, resource group, and location
		locationKey := ip.SubscriptionID + ":" + ip.ResourceGroup + ":" + getStringValueFromPointer(ip.Location)

		if serviceTagCounts[serviceTag] == nil {
			serviceTagCounts[serviceTag] = make(map[string]int)
		}
		serviceTagCounts[serviceTag][locationKey]++
	}

	// Export metrics for service tag counts
	for serviceTag, locationCounts := range serviceTagCounts {
		for locationKey, count := range locationCounts {
			parts := parseLocationKey(locationKey)
			if len(parts) == 3 {
				ch <- prometheus.MustNewConstMetric(
					PublicIPCountByServiceTagDesc,
					prometheus.GaugeValue,
					float64(count),
					serviceTag,
					parts[0], // subscription_id
					parts[1], // resource_group
					parts[2], // location
				)
			}
		}
	}

	// Export total counts per subscription
	for subscriptionID, total := range subscriptionTotals {
		ch <- prometheus.MustNewConstMetric(
			PublicIPTotalDesc,
			prometheus.GaugeValue,
			float64(total),
			subscriptionID,
		)
	}
}

// extractServiceTag attempts to extract a service tag from the Azure tags.
// It looks for common service identification tags like "service", "Service", "serviceName", etc.
func extractServiceTag(tags map[string]string) string {
	// List of common tag keys used for service identification
	serviceTagKeys := []string{
		"service",
		"Service",
		"serviceName",
		"ServiceName",
		"service-name",
		"service_name",
		"component",
		"Component",
		"app",
		"App",
		"application",
		"Application",
	}

	for _, key := range serviceTagKeys {
		if value, exists := tags[key]; exists && value != "" {
			return value
		}
	}

	// If no service tag found, use "unknown"
	return "unknown"
}

// getStringValueFromPointer safely extracts a string value from a pointer, returning empty string if nil
func getStringValueFromPointer(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// parseLocationKey splits the location key back into its components
func parseLocationKey(key string) []string {
	var parts []string
	start := 0

	for i := 0; i < len(key); i++ {
		if key[i] == ':' {
			if i > start {
				parts = append(parts, key[start:i])
			}
			start = i + 1
		}
	}

	if start < len(key) {
		parts = append(parts, key[start:])
	}

	return parts
}