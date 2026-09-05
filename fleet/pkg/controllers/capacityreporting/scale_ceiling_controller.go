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
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpools"
	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
	fleetcontrollers "github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	ManagementClusterScaleCeilingReportingControllerName = "ManagementClusterScaleCeilingReportingController"

	scaleCeilingResyncPeriod = 5 * time.Minute
)

type scaleCeilingReportingSyncer struct {
	managementClusterLister fleetlisters.ManagementClusterLister
	fleetDBClient           fleetcosmosstorage.FleetDBClient
	readDesireLister        kubeapplierlisters.ReadDesireLister
	agentPoolClientFactory  func(subscriptionID string) (*armcontainerservice.AgentPoolsClient, error)
	skuCache                *skucache.SKUCache
}

func NewManagementClusterScaleCeilingReportingController(
	managementClusterInformer cache.SharedIndexInformer,
	managementClusterLister fleetlisters.ManagementClusterLister,
	fleetDBClient fleetcosmosstorage.FleetDBClient,
	readDesireLister kubeapplierlisters.ReadDesireLister,
	region string,
	credential azcore.TokenCredential,
	clientOptions *policy.ClientOptions,
	cfg fleetcontrollers.StampWatchingControllerConfig,
) fleetcontrollers.Controller {
	agentPoolClientFactory, _ := agentpools.NewClientFactory(credential, clientOptions)

	syncer := &scaleCeilingReportingSyncer{
		managementClusterLister: managementClusterLister,
		fleetDBClient:           fleetDBClient,
		readDesireLister:        readDesireLister,
		agentPoolClientFactory:  agentPoolClientFactory,
		skuCache:                skucache.NewSKUCache(region, credential, clientOptions, nil),
	}

	controller := fleetcontrollers.NewStampWatchingController(
		ManagementClusterScaleCeilingReportingControllerName,
		syncer,
		cfg,
	)

	if err := controller.QueueForInformers(scaleCeilingResyncPeriod, managementClusterInformer); err != nil {
		panic(err) // coding error
	}

	return controller
}

func (s *scaleCeilingReportingSyncer) SyncOnce(ctx context.Context, key fleetcontrollers.StampKey) error {
	logger := utils.LoggerFromContext(ctx)

	managementCluster, err := s.managementClusterLister.Get(ctx, key.StampIdentifier)
	if err != nil {
		return utils.TrackError(err)
	}

	aksResourceID := managementCluster.Status.AKSResourceID
	if aksResourceID == nil {
		logger.V(1).Info("management cluster has no AKS resource ID yet, will retry on next resync")
		return nil
	}

	report, err := GetCapacityReport(ctx, s.readDesireLister, key.StampIdentifier)
	if cosmosstorageutils.IsNotFoundError(err) {
		logger.V(1).Info("capacity ReadDesire not found, will retry on next resync")
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	pools, err := agentpools.ListAgentPools(ctx, s.agentPoolClientFactory, aksResourceID)
	if err != nil {
		return utils.TrackError(err)
	}

	skuMetadata, err := s.skuCache.SKUMetadataByVMSize(ctx, aksResourceID.SubscriptionID)
	if err != nil {
		return utils.TrackError(err)
	}

	max := computeMaxCapacity(report, pools, skuMetadata)

	now := metav1.Now()
	return s.persistMaxCapacity(ctx, key.StampIdentifier, max, &now)
}

func (s *scaleCeilingReportingSyncer) persistMaxCapacity(ctx context.Context, stampIdentifier string, max corev1.ResourceList, now *metav1.Time) error {
	existing, err := fleetcosmosstorage.GetOrCreateManagementClusterScheduling(ctx, s.fleetDBClient, stampIdentifier)
	if err != nil {
		return err
	}

	schedulingCRUD := s.fleetDBClient.Stamps().ManagementClusters(stampIdentifier).Scheduling()

	condition := metav1.Condition{
		Type:   fleetapi.ConditionTypeScalingDataCurrent,
		Status: metav1.ConditionTrue,
		Reason: "DataCollected",
	}

	updated := existing.DeepCopy()
	meta.SetStatusCondition(&updated.Status.Conditions, condition)
	updated.Status.ScaleCeiling.LastReportedAt = now
	updated.Status.ScaleCeiling.Capacity = max
	if _, err := schedulingCRUD.Replace(ctx, updated, nil); err != nil {
		return utils.TrackError(err)
	}
	return nil
}
