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
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"k8s.io/apimachinery/pkg/util/wait"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	observationJitter = 0.2
)

// StorageMetricsObserver periodically observes informer caches and
// updates Prometheus GaugeVec metrics. Each resource type is observed
// independently in its own goroutine, following the pattern used by
// the Kubernetes API server's Store.startObservingCount().
type StorageMetricsObserver struct {
	backendInformers informers.BackendInformers
	pollPeriod       time.Duration

	resourcesObjectCounts            *prometheus.GaugeVec
	controllerConditionsObjectCounts *prometheus.GaugeVec
}

// NewStorageMetricsObserver creates a StorageMetricsObserver that periodically
// polls backendInformers and emits statistics as metrics.
func NewStorageMetricsObserver(registerer prometheus.Registerer, backendInformers informers.BackendInformers) *StorageMetricsObserver {
	return &StorageMetricsObserver{
		backendInformers: backendInformers,
		pollPeriod:       1 * time.Minute,
		resourcesObjectCounts: promauto.With(registerer).NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "backend_resource_provider_objects",
				Help: "Number of resource objects by provisioning state.",
			},
			[]string{"resource_type", "provisioning_state"},
		),
		controllerConditionsObjectCounts: promauto.With(registerer).NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "backend_controller_status_conditions",
				Help: "Number of controller resources by controller name, condition, and status.",
			},
			[]string{"controller_name", "condition", "status"},
		),
	}
}

// Run starts a periodic observation goroutine per resource type and
// blocks until ctx is cancelled.
func (o *StorageMetricsObserver) Run(ctx context.Context) {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("starting storage metrics observer")
	defer logger.Info("stopped storage metrics observer")

	wg := sync.WaitGroup{}

	startObservation := func(name string, observe func(context.Context)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			observationCtx := utils.ContextWithLogger(ctx, logger.WithValues("resource_name", name))
			wait.JitterUntilWithContext(observationCtx, observe, o.pollPeriod, observationJitter, true)
		}()
	}

	startObservation("clusters", o.observeClusters)
	startObservation("nodepools", o.observeNodePools)
	startObservation("externalauths", o.observeExternalAuths)
	startObservation("activeoperations", o.observeActiveOperations)
	startObservation("subscriptions", o.observeSubscriptions)
	startObservation("controllers", o.observeControllerConditions)

	<-ctx.Done()
	wg.Wait()
}

func (o *StorageMetricsObserver) observeClusters(ctx context.Context) {
	logger := utils.LoggerFromContext(ctx)
	informer, lister := o.backendInformers.Clusters()
	if !informer.HasSynced() {
		return
	}
	clusters, err := lister.List(ctx)
	if err != nil {
		logger.Error(err, "failed to list clusters for metrics")
		return
	}

	o.updateStorageMetrics(
		api.ClusterResourceType,
		getStorageStatsByState(clusters, provisioningStates(), func(cluster *api.HCPOpenShiftCluster) string {
			return string(cluster.ServiceProviderProperties.ProvisioningState)
		}),
	)
}

func (o *StorageMetricsObserver) observeNodePools(ctx context.Context) {
	logger := utils.LoggerFromContext(ctx)
	informer, lister := o.backendInformers.NodePools()
	if !informer.HasSynced() {
		return
	}
	nodePools, err := lister.List(ctx)
	if err != nil {
		logger.Error(err, "failed to list node pools for metrics")
		return
	}
	o.updateStorageMetrics(
		api.NodePoolResourceType,
		getStorageStatsByState(nodePools, provisioningStates(), func(nodePool *api.HCPOpenShiftClusterNodePool) string {
			return string(nodePool.Properties.ProvisioningState)
		}),
	)
}

func (o *StorageMetricsObserver) observeExternalAuths(ctx context.Context) {
	logger := utils.LoggerFromContext(ctx)
	informer, lister := o.backendInformers.ExternalAuths()
	if !informer.HasSynced() {
		return
	}
	externalAuths, err := lister.List(ctx)
	if err != nil {
		logger.Error(err, "failed to list external auths for metrics")
		return
	}

	o.updateStorageMetrics(
		api.ExternalAuthResourceType,
		getStorageStatsByState(externalAuths, provisioningStates(), func(externalAuth *api.HCPOpenShiftClusterExternalAuth) string {
			return string(externalAuth.Properties.ProvisioningState)
		}),
	)
}

func (o *StorageMetricsObserver) observeActiveOperations(ctx context.Context) {
	logger := utils.LoggerFromContext(ctx)
	informer, lister := o.backendInformers.ActiveOperations()
	if !informer.HasSynced() {
		return
	}
	operations, err := lister.List(ctx)
	if err != nil {
		logger.Error(err, "failed to list active operations for metrics")
		return
	}
	o.updateStorageMetrics(
		api.OperationStatusResourceType,
		getStorageStatsByState(operations, provisioningStates(), func(operation *api.Operation) string {
			return string(operation.Status)
		}),
	)
}

func (o *StorageMetricsObserver) observeSubscriptions(ctx context.Context) {
	logger := utils.LoggerFromContext(ctx)
	informer, lister := o.backendInformers.Subscriptions()
	if !informer.HasSynced() {
		return
	}
	subscriptions, err := lister.List(ctx)
	if err != nil {
		logger.Error(err, "failed to list subscriptions for metrics")
		return
	}
	subscriptionStates := []string{}
	for state := range arm.ListSubscriptionStates() {
		subscriptionStates = append(subscriptionStates, string(state))
	}
	o.updateStorageMetrics(
		azcorearm.SubscriptionResourceType,
		getStorageStatsByState(subscriptions, subscriptionStates, func(subscription *arm.Subscription) string {
			return string(subscription.State)
		}),
	)
}

// observeControllerConditions aggregates controller condition counts by controller name.
// Unlike resource counts, controller names are not pre-initialized to zero because they
// are derived from the data. This means that if a controller name were to disappear
// entirely, its gauge values would become stale. In practice this is not a concern
// because controller names only change with new software versions (process restart).
func (o *StorageMetricsObserver) observeControllerConditions(ctx context.Context) {
	logger := utils.LoggerFromContext(ctx)
	informer, lister := o.backendInformers.Controllers()
	if !informer.HasSynced() {
		return
	}
	controllers, err := lister.List(ctx)
	if err != nil {
		logger.Error(err, "failed to list controllers for metrics")
		return
	}

	controllerDegradedStats := make(map[string]controllerStats)
	for _, controller := range controllers {
		degradedCondition := controllerutils.GetCondition(controller.Status.Conditions, controllerutils.ConditionTypeDegraded)
		if degradedCondition == nil {
			continue
		}
		name := controller.GetResourceID().Name
		stats := controllerDegradedStats[name]
		switch degradedCondition.Status {
		case api.ConditionTrue:
			stats.DegradedTrue++
		case api.ConditionFalse:
			stats.DegradedFalse++
		case api.ConditionUnknown:
			stats.DegradedUnknown++
		}
		controllerDegradedStats[name] = stats
	}
	for controllerName, stats := range controllerDegradedStats {
		o.updateControllerMetrics(controllerName, stats)
	}
}

func getStorageStatsByState[T any](items []T, stateList []string, stateLabeler func(item T) string) resourceStats {
	phaseCounts := make(map[string]int64)
	for _, phase := range stateList {
		phaseCounts[phase] = 0
	}
	for _, item := range items {
		phaseCounts[stateLabeler(item)]++
	}
	return resourceStats{
		CountsByPhase: phaseCounts,
	}
}

type resourceStats struct {
	CountsByPhase map[string]int64
}

func (o *StorageMetricsObserver) updateStorageMetrics(resourceType azcorearm.ResourceType, stats resourceStats) {
	for phase, count := range stats.CountsByPhase {
		o.resourcesObjectCounts.WithLabelValues(resourceType.String(), phase).Set(float64(count))
	}
}

type controllerStats struct {
	DegradedTrue    int64
	DegradedFalse   int64
	DegradedUnknown int64
}

func (o *StorageMetricsObserver) updateControllerMetrics(controllerName string, stats controllerStats) {
	o.controllerConditionsObjectCounts.WithLabelValues(controllerName, controllerutils.ConditionTypeDegraded, string(api.ConditionTrue)).Set(float64(stats.DegradedTrue))
	o.controllerConditionsObjectCounts.WithLabelValues(controllerName, controllerutils.ConditionTypeDegraded, string(api.ConditionFalse)).Set(float64(stats.DegradedFalse))
	o.controllerConditionsObjectCounts.WithLabelValues(controllerName, controllerutils.ConditionTypeDegraded, string(api.ConditionUnknown)).Set(float64(stats.DegradedUnknown))
}

func provisioningStates() []string {
	states := []string{}
	for state := range arm.ListProvisioningStates() {
		states = append(states, string(state))
	}
	return states
}
