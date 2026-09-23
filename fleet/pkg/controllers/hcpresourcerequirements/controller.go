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

// Package hcpresourcerequirements implements a controller that computes the
// average resource requirements per ready HCP across all management clusters.
package hcpresourcerequirements

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"

	fleetcontrollers "github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/capacityreporting"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
	capacityreportv1alpha1 "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
)

const (
	HCPResourceRequirementsControllerName = "HCPResourceRequirementsController"
)

// Controller periodically computes average resource requirements per ready HCP
// across all management clusters and writes the result to a singleton
// HCPResourceRequirements document in Cosmos.
type Controller struct {
	name             string
	pollInterval     time.Duration
	fleetDBClient    fleetcosmosstorage.FleetDBClient
	readDesireLister kubeapplierlisters.ReadDesireLister
	stampLister      fleetlisters.StampLister
	queue            workqueue.TypedRateLimitingInterface[string]
}

// NewController creates a new HCPResourceRequirements controller.
func NewController(
	pollInterval time.Duration,
	fleetDBClient fleetcosmosstorage.FleetDBClient,
	readDesireLister kubeapplierlisters.ReadDesireLister,
	stampLister fleetlisters.StampLister,
) *Controller {
	return &Controller{
		name:             HCPResourceRequirementsControllerName,
		pollInterval:     pollInterval,
		fleetDBClient:    fleetDBClient,
		readDesireLister: readDesireLister,
		stampLister:      stampLister,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{
				Name: HCPResourceRequirementsControllerName,
			},
		),
	}
}

// Run starts the controller. It blocks until the context is cancelled.
func (c *Controller) Run(ctx context.Context) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	ctx = utils.ContextWithControllerName(ctx, c.name)
	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues(utils.LogValues{}.AddControllerName(c.name)...)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("Starting")

	go wait.UntilWithContext(ctx, c.runWorker, time.Second)

	go wait.JitterUntilWithContext(ctx, func(ctx context.Context) { c.queue.Add("default") }, c.pollInterval, 0.1, true)

	logger.Info("Started workers")

	<-ctx.Done()
	logger.Info("Shutting down")
}

func (c *Controller) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *Controller) processNextWorkItem(ctx context.Context) bool {
	key, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(key)

	fleetcontrollers.ReconcileTotal.WithLabelValues(c.name).Inc()
	err := c.syncOnce(ctx)
	if err == nil {
		c.queue.Forget(key)
		return true
	}

	utilruntime.HandleErrorWithContext(ctx, err, "Error syncing; requeuing for later retry", "objectReference", key)
	c.queue.AddRateLimited(key)

	return true
}

func (c *Controller) syncOnce(ctx context.Context) error {
	logger := utils.LoggerFromContext(ctx)

	stamps, err := c.stampLister.List(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	var reports []*capacityreportv1alpha1.CapacityReport
	for _, stamp := range stamps {
		stampIdentifier := stamp.GetStampIdentifier()

		report, err := capacityreporting.GetCapacityReport(ctx, c.readDesireLister, stampIdentifier)
		if cosmosstorageutils.IsNotFoundError(err) {
			logger.V(2).Info("Capacity ReadDesire not found, skipping", "stamp", stampIdentifier)
			continue
		}
		if err != nil {
			logger.Error(err, "Failed to get capacity report", "stamp", stampIdentifier)
			continue
		}
		if report == nil {
			logger.V(2).Info("Capacity report not mirrored yet, skipping", "stamp", stampIdentifier)
			continue
		}

		if !meta.IsStatusConditionTrue(report.Status.Conditions, capacityreportv1alpha1.ConditionTypeReportCurrent) {
			logger.V(1).Info("Capacity report not current, skipping", "stamp", stampIdentifier)
			continue
		}

		if len(report.Status.HostedControlPlanes.ReadyResourceIDs) == 0 {
			logger.V(2).Info("No ready HCPs, skipping", "stamp", stampIdentifier)
			continue
		}

		reports = append(reports, report)
	}

	averageUsage, averageRequests, sampleSize := computeAverageRequirements(reports)
	now := metav1.Now()

	condition := metav1.Condition{
		Type:   fleetapi.ConditionTypeDataCurrent,
		Reason: "Computed",
	}
	if sampleSize > 0 {
		condition.Status = metav1.ConditionTrue
	} else {
		condition.Status = metav1.ConditionFalse
		condition.Reason = "NoData"
		condition.Message = "No management clusters with current capacity data and ready HCPs"
	}

	return c.persist(ctx, averageUsage, averageRequests, sampleSize, &now, condition)
}

func (c *Controller) persist(
	ctx context.Context,
	averageUsage, averageRequests corev1.ResourceList,
	sampleSize int,
	now *metav1.Time,
	condition metav1.Condition,
) error {
	existing, err := fleetcosmosstorage.GetOrCreateHCPResourceRequirements(ctx, c.fleetDBClient, fleetapi.HCPResourceRequirementsResourceName)
	if err != nil {
		return err
	}

	updated := existing.DeepCopy()
	meta.SetStatusCondition(&updated.Status.Conditions, condition)
	updated.Status.LastReportedAt = now
	updated.Status.AverageUsage = averageUsage
	updated.Status.AverageRequests = averageRequests
	updated.Status.SampleSize = sampleSize

	if _, err := c.fleetDBClient.HCPResourceRequirements().Replace(ctx, updated, nil); err != nil {
		return utils.TrackError(err)
	}
	return nil
}
