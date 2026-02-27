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

package metrics

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/Azure/ARO-HCP/tooling/aro-hcp-exporter/pkg/cache"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/mustgather"
)

const (
	KustoLogsCurrentCollectorName = "kusto-logs-current"
	KustoLogsCurrentQueryTimeout  = 1 * time.Minute
	KustoQueryInterval            = 15 * time.Minute
)

var (
	KustoLogsAgeInSecondsDesc = prometheus.NewDesc(
		"kusto_logs_age_in_seconds",
		"Age of last log in seconds from Kusto",
		[]string{"kusto_cluster", "cluster", "table"},
		nil,
	)
)

// KustoLogsCurrentCollector is a Prometheus collector that gathers metrics from Kusto
type KustoLogsCurrentCollector struct {
	kustoClient  *kusto.Client
	database     string
	clusterNames []string
	kustoCluster string
	cache        *cache.MetricsCache
	lastRun      time.Time
}

var _ CachingCollector = &KustoLogsCurrentCollector{}

// NewKustoLogsCurrentCollector creates a new KustoLogsCurrentCollector
func NewKustoLogsCurrentCollector(kustoCluster, kustoRegion string, clusterNames []string, cacheTTL time.Duration) (*KustoLogsCurrentCollector, error) {

	endpoint, err := kusto.KustoEndpoint(kustoCluster, kustoRegion)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kusto endpoint: %w", err)
	}

	kustoClient, err := kusto.NewClient(endpoint, KustoLogsCurrentQueryTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kusto client: %w", err)
	}

	return &KustoLogsCurrentCollector{
		kustoCluster: kustoCluster,
		kustoClient:  kustoClient,
		clusterNames: clusterNames,
		cache:        cache.NewMetricsCache(cacheTTL),
	}, nil
}

func (c *KustoLogsCurrentCollector) Name() string {
	return KustoLogsCurrentCollectorName
}

func (c *KustoLogsCurrentCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- KustoLogsAgeInSecondsDesc
}

func (c *KustoLogsCurrentCollector) Collect(ch chan<- prometheus.Metric) {
	for _, metric := range c.cache.GetAllMetrics() {
		ch <- metric
	}
}

func (c *KustoLogsCurrentCollector) CollectMetricValues(ctx context.Context) {
	logger := logr.FromContextOrDiscard(ctx)
	startTime := time.Now()
	if c.lastRun.Add(KustoQueryInterval).After(startTime) {
		logger.V(1).Info("Skipping Kusto logs collection, last run was less than 30 minutes ago", "lastRun", c.lastRun, "startTime", startTime)
		return
	}
	for _, clusterName := range c.clusterNames {
		logger.V(1).Info("Collecting Kusto logs age in seconds", "cluster", clusterName)

		queryClient := mustgather.NewQueryClientWithFileWriter(c.kustoClient, KustoLogsCurrentQueryTimeout, "", nil)

		queryOptions, err := mustgather.NewInfraQueryOptions(
			clusterName,
			time.Now().Add(-10*time.Minute),
			time.Now(),
			-1,
		)
		if err != nil {
			logger.Error(err, "Failed to create query options", "cluster", clusterName)
			continue
		}

		foundLogSources := make(map[string]time.Time)
		var foundMutex sync.Mutex

		outputFunc := func(ctx context.Context, logLineChan chan *mustgather.NormalizedLogLine, queryType mustgather.QueryType, options mustgather.RowOutputOptions) error {
			for logLine := range logLineChan {
				key := logLine.TableName
				foundMutex.Lock()
				if _, ok := foundLogSources[key]; !ok {
					foundLogSources[key] = logLine.Timestamp
				}
				foundMutex.Unlock()
			}
			return nil
		}

		gatherer := mustgather.NewGatherer(
			queryClient,
			outputFunc,
			mustgather.RowOutputOptions{},
			mustgather.GathererOptions{
				GatherInfraLogs: true,
				QueryOptions:    queryOptions,
			},
		)

		if err := gatherer.GatherLogs(ctx); err != nil {
			logger.Error(err, "Failed to gather logs", "cluster", clusterName)
			continue
		}

		for logSource := range foundLogSources {
			logger.V(1).Info("Found log source", "logSource", logSource)
			c.cache.AddMetric(
				prometheus.MustNewConstMetric(
					KustoLogsAgeInSecondsDesc,
					prometheus.GaugeValue,
					float64(time.Since(foundLogSources[logSource])),
					c.kustoCluster,
					clusterName,
					logSource,
				))
		}
	}
	c.lastRun = time.Now()
}
