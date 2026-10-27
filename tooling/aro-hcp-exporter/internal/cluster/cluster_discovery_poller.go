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

package cluster

import (
	"context"
	"fmt"
	"math/bits"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/Azure/ARO-HCP/tooling/aro-hcp-exporter/pkg/graphquery"
)

const (
	// maxDiscoveryBackoffBase caps the *base* delay of the exponential backoff
	// applied after consecutive Resource Graph failures. Jitter is added on top,
	// so the longest wait between attempts is
	// maxDiscoveryBackoffBase * (1 + discoveryBackoffJitter).
	//
	// The jitter is deliberately not clamped back to this value: at the ceiling
	// is exactly where the most exporters are backed off simultaneously, and
	// clamping would land all of them on the same delay, re-synchronising the
	// retries the jitter exists to spread.
	maxDiscoveryBackoffBase = 10 * time.Minute
	// discoveryBackoffJitter is the fraction of the computed backoff added as
	// random jitter, so that exporters in different regions do not retry in
	// lockstep against the same tenant-wide Resource Graph quota.
	discoveryBackoffJitter = 0.2
)

// DiscoverResult holds the cluster names and subscription IDs found by
// querying Azure Resource Graph for AKS clusters tagged with the
// requested clusterType values.
type DiscoverResult struct {
	Clusters []ClusterInfo
}

type ClusterInfo struct {
	Name           string `mapstructure:"name"`
	SubscriptionId string `mapstructure:"subscriptionId"`
	ClusterType    string `mapstructure:"clusterType"`
}

type ClusterDiscoveryPoller struct {
	client       graphquery.Querier
	query        string
	resultMutex  sync.Mutex
	discovered   bool
	discoveredCh chan struct{}
	rows         []ClusterInfo
	sleepTime    time.Duration

	// consecutiveFailures drives the retry backoff. Only touched by Poll, which
	// is documented as single-goroutine.
	consecutiveFailures int

	lastSuccess   prometheus.Gauge
	errorsTotal   prometheus.Counter
	clustersFound prometheus.Gauge
}

func NewClusterDiscoveryPoller(client graphquery.Querier, region string, clusterTypes []string, clusterNameFilter string, sleepTime time.Duration) *ClusterDiscoveryPoller {
	return &ClusterDiscoveryPoller{
		client:       client,
		query:        BuildClusterQuery(region, clusterTypes, clusterNameFilter),
		resultMutex:  sync.Mutex{},
		sleepTime:    sleepTime,
		discovered:   false,
		discoveredCh: make(chan struct{}),
		lastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "exporter_cluster_discovery_last_success_timestamp_seconds",
			Help: "Unix timestamp of the last successful Azure Resource Graph cluster discovery query. Stays 0 until the first query succeeds.",
		}),
		errorsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "exporter_cluster_discovery_errors_total",
			Help: "Total number of failed Azure Resource Graph cluster discovery queries.",
		}),
		clustersFound: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "exporter_cluster_discovery_clusters",
			Help: "Number of clusters returned by the most recent successful Azure Resource Graph cluster discovery query.",
		}),
	}
}

// Collectors returns the Prometheus collectors tracking discovery health.
//
// These are plain gauges and counters rather than cached const-metrics, so they
// are emitted on every scrape from process start. That is deliberate: every
// other metric this exporter publishes disappears entirely when its collector
// produces nothing, which makes a wedged exporter indistinguishable from a
// healthy one that happens to have nothing to report. A
// exporter_cluster_discovery_last_success_timestamp_seconds of 0 means
// discovery has never succeeded and every collector is still blocked in
// GetDiscoverResult.
func (c *ClusterDiscoveryPoller) Collectors() []prometheus.Collector {
	return []prometheus.Collector{c.lastSuccess, c.errorsTotal, c.clustersFound}
}

// Poll runs one discovery query and then waits before returning.
//
// The wait is unconditional. Callers drive this from a bare `for { Poll(ctx) }`
// loop, so returning early on error turns a single transient Resource Graph
// failure into an unthrottled retry storm, which gets the exporter throttled and
// keeps the failure alive. Because GetDiscoverResult blocks until the first
// successful poll, that state also leaves every collector blocked indefinitely
// and the exporter publishing nothing while still serving 200s on /metrics.
//
// Poll is not safe for concurrent use; call it from a single goroutine.
func (c *ClusterDiscoveryPoller) Poll(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}

	delay := c.sleepTime
	if err := c.discoverOnce(ctx); err != nil {
		c.errorsTotal.Inc()
		c.consecutiveFailures++
		delay = c.retryDelay()
		logr.FromContextOrDiscard(ctx).Error(err, "failed to execute Resource Graph query",
			"consecutiveFailures", c.consecutiveFailures,
			"retryAfter", delay.String(),
		)
	} else {
		c.consecutiveFailures = 0
	}

	select {
	case <-ctx.Done():
	case <-time.After(delay):
	}
}

func (c *ClusterDiscoveryPoller) discoverOnce(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	var newRows []ClusterInfo
	if err := c.client.ExecuteConvertRequest(ctx, graphquery.ResourceGraphRequest{
		Query:  &c.query,
		Output: &newRows,
	}); err != nil {
		return err
	}

	c.resultMutex.Lock()
	if !c.discovered {
		c.discovered = true
		close(c.discoveredCh)
		logger.Info("First discovery of clusters", "total", len(newRows))
	}
	oldNames := clusterNames(c.rows)
	c.rows = newRows
	c.resultMutex.Unlock()

	c.lastSuccess.SetToCurrentTime()
	c.clustersFound.Set(float64(len(newRows)))

	newNames := clusterNames(newRows)
	if !oldNames.Equal(newNames) {
		added := newNames.Difference(oldNames)
		removed := oldNames.Difference(newNames)
		logger.Info("Discovered clusters changed",
			"total", newNames.Len(),
			"added", added.UnsortedList(),
			"removed", removed.UnsortedList(),
		)
	}
	return nil
}

// retryDelay returns how long to wait after consecutiveFailures consecutive
// errors: the normal poll interval for the first failure, doubling for each one
// after that, with the base capped at maxDiscoveryBackoffBase and jitter added
// on top. A zero poll interval (used by tests) stays zero.
//
// Growth is deliberately suppressed until the first successful discovery.
// Until then the exporter publishes nothing at all — every collector is parked
// in GetDiscoverResult — so there is no cached data that a longer backoff would
// protect, and escalating only widens the window in which a recovered Resource
// Graph goes unnoticed. Waiting one full poll interval already removes the
// retry storm, which is the behaviour this backoff exists to prevent.
func (c *ClusterDiscoveryPoller) retryDelay() time.Duration {
	if c.sleepTime <= 0 {
		return 0
	}

	if !c.hasDiscovered() {
		return wait.Jitter(c.sleepTime, discoveryBackoffJitter)
	}

	maxBase := maxDiscoveryBackoffBase
	if c.sleepTime > maxBase {
		maxBase = c.sleepTime
	}

	shift := c.consecutiveFailures - 1
	if shift < 0 {
		shift = 0
	}
	// Bound the exponent by the ceiling rather than by a fixed constant. A
	// fixed cap does not prevent overflow: for a large enough poll interval
	// `c.sleepTime << shift` wraps int64 and can land on a small *positive*
	// value, which slips past both the ceiling and the `<= 0` check below and
	// collapses the backoff into a hot retry loop.
	//
	// maxBase >= c.sleepTime > 0 here, so the multiplier is at least 1 and
	// bits.Len64 is at least 1. Taking the full bit length (rather than
	// length-1) keeps the ceiling reachable: the shift is allowed to overshoot
	// maxBase by less than 2x and is then clamped to it, so the schedule still
	// tops out at exactly maxBase.
	maxShift := bits.Len64(uint64(maxBase / c.sleepTime))
	if shift > maxShift {
		shift = maxShift
	}

	base := c.sleepTime << shift
	if base > maxBase || base <= 0 {
		base = maxBase
	}
	return wait.Jitter(base, discoveryBackoffJitter)
}

func (c *ClusterDiscoveryPoller) hasDiscovered() bool {
	c.resultMutex.Lock()
	defer c.resultMutex.Unlock()
	return c.discovered
}

func (c *ClusterDiscoveryPoller) GetDiscoverResult(ctx context.Context) DiscoverResult {
	select {
	case <-c.discoveredCh:
	case <-ctx.Done():
		return DiscoverResult{}
	}

	c.resultMutex.Lock()
	defer c.resultMutex.Unlock()
	return DiscoverResult{Clusters: slices.Clone(c.rows)}
}

func clusterNames(rows []ClusterInfo) sets.Set[string] {
	s := sets.New[string]()
	for _, r := range rows {
		s.Insert(r.Name)
	}
	return s
}

// BuildClusterQuery constructs a KQL query that finds AKS managed clusters
// in the specified region with a clusterType tag matching the given type.
// When clusterNameFilter is non-empty, an additional "contains" filter
// restricts results to clusters whose name contains the given substring.
func BuildClusterQuery(region string, clusterTypes []string, clusterNameFilter string) string {
	quoted := make([]string, 0, len(clusterTypes))
	for _, clusterType := range clusterTypes {
		quoted = append(quoted, fmt.Sprintf("'%s'", graphquery.EscapeKQL(clusterType)))
	}
	query := fmt.Sprintf(
		"resources\n"+
			"| where type =~ 'Microsoft.ContainerService/managedClusters'\n"+
			"| where location =~ '%s'\n"+
			"| where tags['clusterType'] in~ (%s)",
		graphquery.EscapeKQL(region),
		strings.Join(quoted, ", "),
	)
	if clusterNameFilter != "" {
		query += fmt.Sprintf("\n| where name contains '%s'", graphquery.EscapeKQL(clusterNameFilter))
	}
	query += "\n| project name, subscriptionId, clusterType = tostring(tags['clusterType'])"
	return query
}
