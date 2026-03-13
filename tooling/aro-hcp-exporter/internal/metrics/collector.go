// Copyright 2026 Microsoft Corporation

package metrics

import (
	"context"
	"fmt"
	"slices"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
)

// CachingCollector is an interface for collectors that cache metrics
// This is used, cause it improves the performance of the exporter by avoiding
// adhoc API calls during metrics collection.
type CachingCollector interface {
	// Name returns the name of the collector
	Name() string
	// Describe metrics, according to the Prometheus specification
	Describe(ch chan<- *prometheus.Desc)
	// Collect metrics, according to the Prometheus specification
	Collect(ch chan<- prometheus.Metric)
	// CollectMetricValues is a loop that collects metrics from the source and adds them to the cache
	CollectMetricValues(ctx context.Context)
}

func getSubscriptionIDs(ctx context.Context, creds azcore.TokenCredential, subscriptionNames []string) ([]string, error) {
	subscriptionClient, err := armsubscriptions.NewClient(creds, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create subscription client: %w", err)
	}
	subscriptionIDs := make([]string, 0)
	pager := subscriptionClient.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, sub := range page.Value {
			if slices.Contains(subscriptionNames, *sub.DisplayName) {
				subscriptionIDs = append(subscriptionIDs, *sub.SubscriptionID)
			}
		}
	}
	return subscriptionIDs, nil
}
