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

package capacityreporting

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpools"
	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
	fleetcontrollers "github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/fleet/pkg/scheduling"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
	capacityreportv1alpha1 "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
)

const (
	ManagementClusterScaleCeilingReportingControllerName = "ManagementClusterScaleCeilingReportingController"

	scaleCeilingResyncPeriod = 5 * time.Minute
)

type scaleCeilingReportingSyncer struct {
	fleetDBClient          fleetcosmosstorage.FleetDBClient
	readDesireLister       kubeapplierlisters.ReadDesireLister
	agentPoolClientFactory func(subscriptionID string) (*armcontainerservice.AgentPoolsClient, error)
	skuCache               *skucache.SKUCache
}

func NewManagementClusterScaleCeilingReportingController(
	managementClusterInformer cache.SharedIndexInformer,
	stampInformer cache.SharedIndexInformer,
	fleetDBClient fleetcosmosstorage.FleetDBClient,
	readDesireLister kubeapplierlisters.ReadDesireLister,
	region string,
	credential azcore.TokenCredential,
	clientOptions *policy.ClientOptions,
	cfg fleetcontrollers.StampWatchingControllerConfig,
) fleetcontrollers.Controller {
	if clientOptions == nil {
		clientOptions = &policy.ClientOptions{}
	}
	armClientOptions := &azcorearm.ClientOptions{ClientOptions: *clientOptions}

	agentPoolClientFactory := func(subscriptionID string) (*armcontainerservice.AgentPoolsClient, error) {
		return armcontainerservice.NewAgentPoolsClient(subscriptionID, credential, armClientOptions)
	}

	syncer := &scaleCeilingReportingSyncer{
		fleetDBClient:          fleetDBClient,
		readDesireLister:       readDesireLister,
		agentPoolClientFactory: agentPoolClientFactory,
		skuCache:               skucache.NewSKUCache(region, credential, clientOptions, nil),
	}

	controller := fleetcontrollers.NewStampWatchingController(
		ManagementClusterScaleCeilingReportingControllerName,
		stampInformer,
		scaleCeilingResyncPeriod,
		syncer,
		cfg,
	)

	// Stamp keys already resync once; other inputs only enqueue on changes.
	if err := controller.QueueForInformers(0, managementClusterInformer); err != nil {
		panic(err) // coding error
	}

	return controller
}

func (s *scaleCeilingReportingSyncer) SyncOnce(ctx context.Context, key fleetcontrollers.StampKey) error {
	managementCluster, err := s.fleetDBClient.Stamps().ManagementClusters(key.StampIdentifier).Get(ctx, fleetapi.ManagementClusterResourceName)
	if err != nil {
		return s.persistScalingFailure(ctx, key.StampIdentifier, "ManagementClusterUnavailable", err.Error())
	}

	aksResourceID := managementCluster.Status.AKSResourceID
	if aksResourceID == nil {
		return s.persistScalingFailure(ctx, key.StampIdentifier, "MissingAKSResourceID", "Management cluster has no AKS resource ID")
	}

	report, err := GetCapacityReport(ctx, s.readDesireLister, key.StampIdentifier)
	if err != nil {
		utils.LoggerFromContext(ctx).Error(err, "No usable CapacityReport for scale ceiling", "stamp", key.StampIdentifier)
		report = nil
	} else if !meta.IsStatusConditionTrue(report.Status.Conditions, capacityreportv1alpha1.ConditionTypeReportCurrent) {
		utils.LoggerFromContext(ctx).Info("CapacityReport source is not current", "stamp", key.StampIdentifier)
		report = nil
	}

	pools, err := agentpools.ListAgentPools(ctx, s.agentPoolClientFactory, aksResourceID)
	if err != nil {
		return s.persistScalingFailure(ctx, key.StampIdentifier, "AgentPoolsUnavailable", err.Error())
	}

	var skuMetadata map[string]*skucache.SKUMetadata
	bootstrap := needsSKUBootstrap(report, pools)
	if bootstrap {
		skuMetadata, err = s.skuCache.SKUMetadataByVMSize(ctx, aksResourceID.SubscriptionID)
		if err != nil {
			return s.persistScalingFailure(ctx, key.StampIdentifier, "SKUMetadataUnavailable", err.Error())
		}
	}

	max, err := computeMaxCapacity(report, pools, skuMetadata)
	if !hasCeilingMeasurements(max) {
		if err == nil {
			err = fmt.Errorf("no scheduling resource measurements")
		}
		return s.persistScalingFailure(ctx, key.StampIdentifier, "CapacityComputationFailed", err.Error())
	}
	condition := metav1.Condition{Type: fleetapi.ConditionTypeScalingDataCurrent, Status: metav1.ConditionTrue, Reason: "DataCollected"}
	if bootstrap {
		condition.Reason, condition.Message = "BootstrapEstimate", "Uses theoretical SKU/configured-NIC bootstrap where available for pools without ready-node samples"
	}
	if err != nil {
		utils.LoggerFromContext(ctx).Error(err, "Publishing available capacity contributions", "stamp", key.StampIdentifier)
		condition.Reason += "Partial"
		condition.Message += "; unavailable contributions and dimensions omitted"
	}
	now := metav1.Now()
	return s.persistScalingResult(ctx, key.StampIdentifier, max, &now, condition)
}

func (s *scaleCeilingReportingSyncer) persistMaxCapacity(ctx context.Context, stampIdentifier string, max corev1.ResourceList, now *metav1.Time) error {
	return s.persistScalingResult(ctx, stampIdentifier, max, now, metav1.Condition{Type: fleetapi.ConditionTypeScalingDataCurrent, Status: metav1.ConditionTrue, Reason: "DataCollected"})
}

func (s *scaleCeilingReportingSyncer) persistScalingFailure(ctx context.Context, stampIdentifier string, reason, message string) error {
	utils.LoggerFromContext(ctx).Error(fmt.Errorf("%s: %s", reason, message), "Unable to refresh scale ceiling", "stamp", stampIdentifier)
	now := metav1.Now()
	return s.persistScalingResult(ctx, stampIdentifier, nil, &now, metav1.Condition{Type: fleetapi.ConditionTypeScalingDataCurrent, Status: metav1.ConditionUnknown, Reason: "NoUsableCeiling", Message: "No successfully computed usable ceiling is available"})
}

func (s *scaleCeilingReportingSyncer) persistScalingResult(ctx context.Context, stampIdentifier string, max corev1.ResourceList, now *metav1.Time, condition metav1.Condition) error {
	existing, err := fleetcosmosstorage.GetOrCreateManagementClusterScheduling(ctx, s.fleetDBClient, stampIdentifier)
	if err != nil {
		return err
	}
	if condition.Status != metav1.ConditionTrue && meta.IsStatusConditionTrue(existing.Status.Conditions, fleetapi.ConditionTypeScalingDataCurrent) && hasCeilingMeasurements(existing.Status.ScaleCeiling.Capacity) {
		return nil
	}

	schedulingCRUD := s.fleetDBClient.Stamps().ManagementClusters(stampIdentifier).Scheduling()

	updated := existing.DeepCopy()
	condition.LastTransitionTime = *now
	meta.SetStatusCondition(&updated.Status.Conditions, condition)
	if condition.Status == metav1.ConditionTrue {
		updated.Status.ScaleCeiling.Capacity = max
	}
	// Compare before advancing the publication timestamp: an unchanged
	// computation must not produce another Cosmos event and wake us again.
	if !controllerutils.NeedsUpdate(existing, updated) {
		return nil
	}
	updated.Status.ScaleCeiling.LastReportedAt = now
	if _, err := schedulingCRUD.Replace(ctx, updated, nil); err != nil {
		return utils.TrackError(err)
	}
	return nil
}

func hasCeilingMeasurements(capacity corev1.ResourceList) bool {
	found := false
	for _, name := range scheduling.Resources() {
		if quantity, exists := capacity[name]; exists {
			if quantity.Sign() < 0 {
				return false
			}
			found = true
		}
	}
	return found
}
