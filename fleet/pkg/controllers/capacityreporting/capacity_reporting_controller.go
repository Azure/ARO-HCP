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

// Package capacityreporting populates ManagementClusterScheduling documents
// in Cosmos. It contains three controllers:
// EnsureCapacityReadDesireController ensures the CapacityReport ReadDesire
// exists, CapacityReportingController writes observed capacity from the
// mirrored CR, and ScaleCeilingReportingController computes maximum
// scheduling capacity from AKS agent pool scaling limits.
package capacityreporting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

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
	stampInformer cache.SharedIndexInformer,
	fleetDBClient fleetcosmosstorage.FleetDBClient,
	readDesireLister kubeapplierlisters.ReadDesireLister,
	cfg fleetcontrollers.StampWatchingControllerConfig,
) fleetcontrollers.Controller {
	syncer := &capacityReportingSyncer{
		fleetDBClient:    fleetDBClient,
		readDesireLister: readDesireLister,
	}

	controller := fleetcontrollers.NewStampWatchingController(
		CapacityReportingControllerName,
		stampInformer,
		capacityReportingResyncPeriod,
		syncer,
		cfg,
	)

	// Stamp keys already resync once; other inputs only enqueue on changes.
	if err := controller.QueueForInformers(0, readDesireNotifier, managementClusterInformer); err != nil {
		panic(err) // coding error
	}

	return controller
}

func (s *capacityReportingSyncer) SyncOnce(ctx context.Context, key fleetcontrollers.StampKey) error {
	report, err := GetCapacityReport(ctx, s.readDesireLister, key.StampIdentifier)
	if err != nil {
		return s.persistObservedResources(ctx, key.StampIdentifier, fleetapi.ObservedResources{}, nil, nil, observationCondition(err))
	}

	capacity := ComputeObservedResources(report)
	condition := evaluateCapacityCondition(report)

	return s.persistObservedResources(ctx, key.StampIdentifier, capacity, report.Status.HostedControlPlanes.ReadyResourceIDs, report.Status.HostedControlPlanes.NotReadyResourceIDs, condition)
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
	if !HasResourceTotals(report.Status.HostedControlPlanes.Resources) {
		return metav1.Condition{Type: fleetapi.ConditionTypeCapacityDataCurrent, Status: metav1.ConditionUnknown, Reason: "MissingResourceTotals", Message: "CPU, memory and Swift NIC usage and requests must all be present"}
	}
	return metav1.Condition{
		Type:   fleetapi.ConditionTypeCapacityDataCurrent,
		Status: metav1.ConditionTrue,
		Reason: "DataCollected",
	}
}

func (s *capacityReportingSyncer) persistObservedResources(ctx context.Context, stampIdentifier string, observed fleetapi.ObservedResources, readyResourceIDs, notReadyResourceIDs []string, condition metav1.Condition) error {
	existing, err := fleetcosmosstorage.GetOrCreateManagementClusterScheduling(ctx, s.fleetDBClient, stampIdentifier)
	if err != nil {
		return err
	}

	schedulingCRUD := s.fleetDBClient.Stamps().ManagementClusters(stampIdentifier).Scheduling()

	updated := existing.DeepCopy()
	meta.SetStatusCondition(&updated.Status.Conditions, condition)
	if condition.Status == metav1.ConditionTrue {
		updated.Status.ObservedResources = observed
		// Mirror the ready/not-ready HCP resource IDs from the CapacityReport CR,
		// translating the raw ARM ID strings into parsed *azcorearm.ResourceID.
		updated.Status.ReadyResourceIDs = parseResourceIDs(ctx, readyResourceIDs)
		updated.Status.NotReadyResourceIDs = parseResourceIDs(ctx, notReadyResourceIDs)
		// Observation-based cleanup: drop pending reservations that are now observed
		// (present in Ready ∪ NotReady). Their swift-NIC capacity is accounted for by
		// the observed data, so the transient reservation is no longer needed.
		updated.Status.PendingAssignedClusters = dropObservedPendingAssignments(updated.Status.PendingAssignedClusters, readyResourceIDs, notReadyResourceIDs)
	}
	if !controllerutils.NeedsUpdate(existing, updated) {
		return nil
	}
	if _, err := schedulingCRUD.Replace(ctx, updated, nil); err != nil {
		return utils.TrackError(err)
	}
	return nil
}

// dropObservedPendingAssignments returns pending reservations minus any whose
// cluster resource ID now appears in the observed ready or not-ready sets. Nil
// entries are dropped. It returns nil when nothing remains; because the field is
// tagged omitempty, a nil slice is omitted from the serialized document rather
// than encoded as an empty array.
func dropObservedPendingAssignments(pending []*azcorearm.ResourceID, readyResourceIDs, notReadyResourceIDs []string) []*azcorearm.ResourceID {
	if len(pending) == 0 {
		return nil
	}
	observed := make(map[string]struct{}, len(readyResourceIDs)+len(notReadyResourceIDs))
	for _, id := range readyResourceIDs {
		observed[strings.ToLower(id)] = struct{}{}
	}
	for _, id := range notReadyResourceIDs {
		observed[strings.ToLower(id)] = struct{}{}
	}
	kept := make([]*azcorearm.ResourceID, 0, len(pending))
	for _, entry := range pending {
		if entry == nil {
			continue
		}
		if _, ok := observed[strings.ToLower(entry.String())]; ok {
			continue // now observed → drop the reservation
		}
		kept = append(kept, entry)
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

// parseResourceIDs translates ARM resource ID strings (mirrored from the
// CapacityReport CR) into parsed *azcorearm.ResourceID values. Empty or
// unparseable entries are logged and skipped rather than aborting the mirror,
// so a single malformed ID never blocks capacity reporting. It returns nil when
// no entries parse, so the omitempty-tagged status fields stay absent rather
// than serializing an empty array. Membership is a set: canonicalize casing,
// deduplicate and sort to avoid writes caused only by report iteration order.
func parseResourceIDs(ctx context.Context, ids []string) []*azcorearm.ResourceID {
	if len(ids) == 0 {
		return nil
	}
	logger := utils.LoggerFromContext(ctx)
	parsed := make([]*azcorearm.ResourceID, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		canonical := strings.ToLower(id)
		if seen[canonical] {
			continue
		}
		resourceID, err := azcorearm.ParseResourceID(canonical)
		if err != nil {
			logger.Error(err, "skipping unparseable HCP resource ID from CapacityReport", "resourceID", id)
			continue
		}
		parsed = append(parsed, resourceID)
		seen[canonical] = true
	}
	if len(parsed) == 0 {
		return nil
	}
	slices.SortFunc(parsed, func(a, b *azcorearm.ResourceID) int { return strings.Compare(a.String(), b.String()) })
	return parsed
}

// GetCapacityReport requires a successful observation with no explicit degradation.
// Success describes collection, not freshness; failed reads cannot use retained content.
func GetCapacityReport(ctx context.Context, readDesireLister kubeapplierlisters.ReadDesireLister, stampIdentifier string) (*capacityreportv1alpha1.CapacityReport, error) {
	readDesire, err := readDesireLister.GetForManagementCluster(ctx, stampIdentifier, ReadDesireName)
	if err != nil {
		if cosmosstorageutils.IsNotFoundError(err) {
			return nil, &observationError{metav1.ConditionUnknown, "ReadDesireUnavailable", "CapacityReport ReadDesire does not exist"}
		}
		return nil, fmt.Errorf("reading CapacityReport ReadDesire: %w", err)
	}
	if meta.IsStatusConditionTrue(readDesire.Status.Conditions, kubeapplierapi.ConditionTypeDegraded) {
		return nil, &observationError{metav1.ConditionFalse, "ReadDesireDegraded", "CapacityReport reader is degraded"}
	}
	if meta.IsStatusConditionFalse(readDesire.Status.Conditions, kubeapplierapi.ConditionTypeSuccessful) {
		return nil, &observationError{metav1.ConditionFalse, "ReadDesireFailed", "CapacityReport observation failed"}
	}
	if !meta.IsStatusConditionTrue(readDesire.Status.Conditions, kubeapplierapi.ConditionTypeSuccessful) {
		return nil, &observationError{metav1.ConditionUnknown, "ReadDesireUnobserved", "CapacityReport has not been successfully observed"}
	}
	if readDesire.Status.KubeContent == nil {
		return nil, &observationError{metav1.ConditionFalse, "CapacityReportAbsent", "CapacityReport was successfully observed to be absent"}
	}
	var report *capacityreportv1alpha1.CapacityReport
	if err := json.Unmarshal(readDesire.Status.KubeContent.Raw, &report); err != nil {
		return nil, &observationError{metav1.ConditionFalse, "MalformedCapacityReport", fmt.Sprintf("failed to unmarshal CapacityReport: %v", err)}
	}
	if report == nil || (report.Kind != "" && report.Kind != "CapacityReport") {
		return nil, &observationError{metav1.ConditionFalse, "MalformedCapacityReport", "expected a CapacityReport object"}
	}
	return report, nil
}

type observationError struct {
	status  metav1.ConditionStatus
	reason  string
	message string
}

func (err *observationError) Error() string { return err.message }

func observationCondition(err error) metav1.Condition {
	condition := metav1.Condition{Type: fleetapi.ConditionTypeCapacityDataCurrent, Status: metav1.ConditionFalse, Reason: "ReadDesireError", Message: err.Error()}
	var observation *observationError
	if errors.As(err, &observation) {
		condition.Status, condition.Reason = observation.status, observation.reason
	}
	return condition
}
