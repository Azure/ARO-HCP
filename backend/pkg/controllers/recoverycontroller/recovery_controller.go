// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package recoverycontroller

import (
	"context"
	"fmt"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	internalcontrollerutils "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type recoverySyncState struct {
	key                            controllerutils.HCPClusterKey
	spc                            *coreapi.ServiceProviderCluster
	clusterID                      string
	mcResourceID                   *azcorearm.ResourceID
	spcCrud                        cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster]
	rdCrud                         cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire]
	adCrud                         cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire]
	recoveryRequestToProcess       *coreapi.RecoveryRequest
	recoveryRequestStatusToProcess *coreapi.RecoveryStatus
}

type recoverySyncer struct {
	cooldownChecker internalcontrollerutils.CooldownChecker

	cosmosClient corecosmosstorage.ResourcesDBClient

	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients
}

var _ controllerutils.ClusterSyncer = (*recoverySyncer)(nil)

func NewRecoveryController(
	activeOperationLister corelisters.ActiveOperationLister,
	cosmosClient corecosmosstorage.ResourcesDBClient,
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients,
	informers coreinformers.BackendInformers,
) controllerutils.Controller {

	syncer := &recoverySyncer{
		cooldownChecker:      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		cosmosClient:         cosmosClient,
		kubeApplierDBClients: kubeApplierDBClients,
	}

	controller := controllerutils.NewClusterWatchingController(
		"Recovery",
		cosmosClient,
		informers,
		nil,
		30*time.Second,
		syncer,
	)

	return controller
}

func (c *recoverySyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}

	spc, err := corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, c.cosmosClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}

	if len(spc.Spec.RecoveryRequests) == 0 {
		return nil
	}

	mcResourceID := spc.Status.ManagementClusterResourceID
	if mcResourceID == nil {
		return nil
	}

	kaClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
	if kaClient == nil {
		return nil
	}
	adCrud, err := kaClient.ApplyDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get apply desires for cluster: %w", err))
	}
	rdCrud, err := kaClient.ReadDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get recovery desires for cluster: %w", err))
	}

	clusterID := existingCluster.ServiceProviderProperties.ClusterServiceID.ID()

	recoveryRequestToProcess, recoveryRequestStatusToProcess, err := findActiveRecovery(spc)
	if err != nil {
		return err
	}

	if recoveryRequestToProcess == nil {
		return nil
	}

	state := recoverySyncState{
		key:                            key,
		spc:                            spc,
		clusterID:                      clusterID,
		mcResourceID:                   mcResourceID,
		rdCrud:                         rdCrud,
		adCrud:                         adCrud,
		recoveryRequestToProcess:       recoveryRequestToProcess,
		recoveryRequestStatusToProcess: recoveryRequestStatusToProcess,
	}

	err = c.process(ctx, state)
	if err != nil {
		return err
	}

	return nil
}

func findActiveRecovery(spc *coreapi.ServiceProviderCluster) (*coreapi.RecoveryRequest, *coreapi.RecoveryStatus, error) {
	if spc.Status.Recoveries == nil {
		spc.Status.Recoveries = make([]coreapi.RecoveryStatus, 0)
	}

	var activeCount int
	var activeRequest *coreapi.RecoveryRequest
	var activeStatus *coreapi.RecoveryStatus

	for i, request := range spc.Spec.RecoveryRequests {
		matchFound := false
		for j, status := range spc.Status.Recoveries {
			if request.RecoveryId != status.RecoveryId {
				continue
			}
			matchFound = true
			if !isTerminal(status.State) {
				activeCount++
				activeRequest = &spc.Spec.RecoveryRequests[i]
				activeStatus = &spc.Status.Recoveries[j]
			}
		}

		if !matchFound {
			spc.Status.Recoveries = append(spc.Status.Recoveries, coreapi.RecoveryStatus{
				RecoveryId: request.RecoveryId,
				State:      coreapi.RecoveryStatePending,
			})
			activeCount++
			activeRequest = &spc.Spec.RecoveryRequests[i]
			activeStatus = &spc.Status.Recoveries[len(spc.Status.Recoveries)-1]
		}
	}

	if activeCount > 1 {
		return nil, nil, fmt.Errorf("found %d active recoveries, expected at most 1", activeCount)
	}

	return activeRequest, activeStatus, nil
}

func (c *recoverySyncer) updateSpcStatus(ctx context.Context, state recoverySyncState) error {
	return nil
}

func (c *recoverySyncer) process(ctx context.Context, state recoverySyncState) error {

	if state.spc.Spec.BackupScheduleState != coreapi.BackupScheduleStateDisabled {
		state.spc.Spec.BackupScheduleState = coreapi.BackupScheduleStateDisabled
		return nil
	}

	return nil

}

func isTerminal(restoreState coreapi.RecoveryState) bool {
	return restoreState == coreapi.RecoveryStateCompleted || restoreState == coreapi.RecoveryStateFailed
}
func (c *recoverySyncer) CooldownChecker() internalcontrollerutils.CooldownChecker {
	return c.cooldownChecker
}
