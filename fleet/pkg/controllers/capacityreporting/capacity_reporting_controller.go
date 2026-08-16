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

// Package capacityreporting mirrors management-cluster-scoped CapacityReport
// CRs into Cosmos scheduling documents. The package contains two controllers:
// EnsureCapacityReadDesireController ensures the ReadDesire exists, and
// CapacityReportingController consumes the mirrored CR and writes current
// capacity to the scheduling document.
package capacityreporting

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	fleetcontrollers "github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
	capacityreportv1alpha1 "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
)

const (
	CapacityReportingControllerName = "CapacityReportingController"

	capacityReportingResyncPeriod = 10 * time.Minute

	// ReadDesireName is the name of the management-cluster-scoped ReadDesire
	// that mirrors the CapacityReport CR.
	ReadDesireName = "capacity"
)

// CapacityReportTarget is the CapacityReport CR mirrored by the ReadDesire: a
// cluster-scoped singleton named "cluster" published by the mgmt-agent on each
// management cluster.
var CapacityReportTarget = kubeapplierapi.ResourceReference{
	Group:    "mgmtagent.aro-hcp.azure.com",
	Version:  "v1alpha1",
	Resource: "capacityreports",
	Name:     "cluster",
}

type capacityReportingSyncer struct {
	fleetDBClient    fleetcosmosstorage.FleetDBClient
	readDesireLister kubeapplierlisters.ReadDesireLister
}

func NewCapacityReportingController(
	readDesireNotifier controllerutils.Notifier,
	managementClusterInformer cache.SharedIndexInformer,
	fleetDBClient fleetcosmosstorage.FleetDBClient,
	readDesireLister kubeapplierlisters.ReadDesireLister,
	cfg fleetcontrollers.StampWatchingControllerConfig,
) *fleetcontrollers.StampWatchingController {
	syncer := &capacityReportingSyncer{
		fleetDBClient:    fleetDBClient,
		readDesireLister: readDesireLister,
	}

	controller := fleetcontrollers.NewStampWatchingController(
		CapacityReportingControllerName,
		syncer,
		cfg,
	)

	if err := controller.QueueForInformers(capacityReportingResyncPeriod, readDesireNotifier, managementClusterInformer); err != nil {
		panic(err) // coding error
	}

	return controller
}

func (s *capacityReportingSyncer) SyncOnce(ctx context.Context, key fleetcontrollers.StampKey) error {
	logger := utils.LoggerFromContext(ctx)

	report, err := GetCapacityReport(ctx, s.readDesireLister, key.StampIdentifier)
	if cosmosstorageutils.IsNotFoundError(err) {
		logger.V(1).Info("capacity ReadDesire not found, waiting for ensure controller")
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	if report == nil {
		logger.V(1).Info("capacity report not mirrored yet, will retry on next change")
		return nil
	}

	capacity := ComputeObservedResources(report)
	condition := evaluateCapacityCondition(report)

	return s.persistObservedResources(ctx, key.StampIdentifier, capacity, condition)
}

func evaluateCapacityCondition(report *capacityreportv1alpha1.CapacityReport) metav1.Condition {
	if !meta.IsStatusConditionTrue(report.Status.Conditions, capacityreportv1alpha1.ConditionTypeReportCurrent) {
		return metav1.Condition{
			Type:    fleetapi.ConditionTypeCapacityDataCurrent,
			Status:  metav1.ConditionFalse,
			Reason:  "ReportNotCurrent",
			Message: "CapacityReport CR's ReportCurrent condition is not True",
		}
	}
	return metav1.Condition{
		Type:   fleetapi.ConditionTypeCapacityDataCurrent,
		Status: metav1.ConditionTrue,
		Reason: "DataCollected",
	}
}

func (s *capacityReportingSyncer) persistObservedResources(ctx context.Context, stampIdentifier string, observed fleetapi.ObservedResources, condition metav1.Condition) error {
	existing, err := fleetcosmosstorage.GetOrCreateManagementClusterScheduling(ctx, s.fleetDBClient, stampIdentifier)
	if err != nil {
		return err
	}

	schedulingCRUD := s.fleetDBClient.Stamps().ManagementClusters(stampIdentifier).Scheduling()

	updated := existing.DeepCopy()
	meta.SetStatusCondition(&updated.Status.Conditions, condition)
	updated.Status.ObservedResources = observed
	if _, err := schedulingCRUD.Replace(ctx, updated, nil); err != nil {
		return utils.TrackError(err)
	}
	return nil
}

// GetCapacityReport reads and unmarshals the CapacityReport from the
// ReadDesire lister. Exported for use by the nodepoolscaling controller.
func GetCapacityReport(ctx context.Context, readDesireLister kubeapplierlisters.ReadDesireLister, stampIdentifier string) (*capacityreportv1alpha1.CapacityReport, error) {
	readDesire, err := readDesireLister.GetForManagementCluster(ctx, stampIdentifier, ReadDesireName)
	if err != nil {
		return nil, err
	}
	if readDesire.Status.KubeContent == nil {
		return nil, nil
	}
	var report capacityreportv1alpha1.CapacityReport
	if err := json.Unmarshal(readDesire.Status.KubeContent.Raw, &report); err != nil {
		return nil, fmt.Errorf("failed to unmarshal CapacityReport: %w", err)
	}
	return &report, nil
}
