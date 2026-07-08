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
	"encoding/json"
	"fmt"
	"strings"
	"time"

	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	hcprecoveryv1alpha1 "github.com/Azure/ARO-HCP/hcp-recovery/pkg/apis/hcprecovery/v1alpha1"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/backup"
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
		return utils.TrackError(fmt.Errorf("failed to get read desires for cluster: %w", err))
	}

	if existingCluster.ServiceProviderProperties.ClusterServiceID == nil {
		return nil
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
		spcCrud:                        c.cosmosClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName),
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
	if _, err := state.spcCrud.Replace(ctx, state.spc, nil); err != nil {
		return utils.TrackError(fmt.Errorf("failed to update ServiceProviderCluster: %w", err))
	}
	return nil
}

func (c *recoverySyncer) process(ctx context.Context, state recoverySyncState) error {
	if state.recoveryRequestStatusToProcess.CompletedAt != nil {
		// Request completed, no further processing needed
		return nil
	}
	if state.recoveryRequestStatusToProcess.StartedAt == nil {
		state.recoveryRequestStatusToProcess.StartedAt = &metav1.Time{Time: time.Now()}
		return c.updateSpcStatus(ctx, state)
	}
	if state.spc.Spec.BackupScheduleState != coreapi.BackupScheduleStateDisabled {
		state.spc.Spec.BackupScheduleState = coreapi.BackupScheduleStateDisabled
		return c.updateSpcStatus(ctx, state)
	}
	// Fetch Schedule Read Desires for cluster and make sure pause was applied
	schedules, err := fetchSchedules(ctx, state)
	if err != nil {
		return fmt.Errorf("failed to fetch schedules: %w", err)
	}
	if !areAllSchedulesPaused(schedules) {
		return fmt.Errorf("waiting for all schedules to be paused")
	}

	// Schedules are paused, check for active hcprecovery
	_, err = state.adCrud.Get(ctx, backup.RecoveryDesireNamePrefix+state.recoveryRequestToProcess.RecoveryId)
	if err != nil {
		if cosmosstorageutils.IsNotFoundError(err) {
			err = createRecoveryApplyDesire(ctx, state)
			if err != nil {
				return fmt.Errorf("failed to create recovery apply desire: %w", err)
			}
			return nil
		}
		return fmt.Errorf("failed to fetch hcprecovery: %w", err)
	}

	// Fetch hcpRecoveryReadDesire and check status, if its terminal set recoveryRequestStatusToProcess completedAt
	rd, err := state.rdCrud.Get(ctx, backup.RecoveryDesireNamePrefix+state.recoveryRequestToProcess.RecoveryId)
	if err != nil {
		if cosmosstorageutils.IsNotFoundError(err) {
			err = createRecoveryReadDesire(ctx, state)
			if err != nil {
				return fmt.Errorf("failed to create recovery read desire: %w", err)
			}
			return nil
		}
		return fmt.Errorf("failed to fetch recovery read desire: %w", err)
	}

	// found a rd, fetch the status and do the next step or skip
	var recovery hcprecoveryv1alpha1.HCPRecovery
	if rd.Status.KubeContent == nil || rd.Status.KubeContent.Raw == nil {
		// Not read yet?
		return nil
	}
	err = json.Unmarshal(rd.Status.KubeContent.Raw, &recovery)
	if err != nil {
		return fmt.Errorf("failed to unmarshal recovery read desire: %w", err)
	}

	// Check for a terminal state
	switch recovery.Status.Phase {
	case hcprecoveryv1alpha1.RestoreStateFailed:
		state.recoveryRequestStatusToProcess.CompletedAt = recovery.Status.CompletedAt
		state.recoveryRequestStatusToProcess.State = coreapi.RecoveryStateFailed
		return c.updateSpcStatus(ctx, state)
		// THINK about what we can do based on conditions from the recovery cr
		//
	case hcprecoveryv1alpha1.RestoreStateCompleted:
		if state.spc.Spec.BackupScheduleState == coreapi.BackupScheduleStateDisabled {
			state.spc.Spec.BackupScheduleState = coreapi.BackupScheduleStateEnabled
			state.recoveryRequestStatusToProcess.CompletedAt = recovery.Status.CompletedAt
			state.recoveryRequestStatusToProcess.State = coreapi.RecoveryStateCompleted
			return c.updateSpcStatus(ctx, state)
		}
	// TODO: Mark in progress
	default:
		return nil
	}
	return nil
}

func createRecoveryReadDesire(ctx context.Context, state recoverySyncState) error {
	desireName := backup.RecoveryDesireNamePrefix + state.recoveryRequestToProcess.RecoveryId
	resourceIDStr := kubeapplierapi.ToClusterScopedReadDesireResourceIDString(
		state.key.SubscriptionID,
		state.key.ResourceGroupName,
		state.key.HCPClusterName,
		desireName,
	)
	resourceID, err := azcorearm.ParseResourceID(resourceIDStr)
	if err != nil {
		return fmt.Errorf("failed to parse read desire resource ID: %w", err)
	}

	readDesire := &kubeapplierapi.ReadDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(state.mcResourceID.String()),
		},
		Spec: kubeapplierapi.ReadDesireSpec{
			ManagementCluster: state.mcResourceID,
			TargetItem: kubeapplierapi.ResourceReference{
				Group:     hcprecoveryv1alpha1.SchemeGroupVersion.Group,
				Version:   hcprecoveryv1alpha1.SchemeGroupVersion.Version,
				Resource:  hcprecoveryv1alpha1.HCPRecoveryResource,
				Namespace: hcprecoveryv1alpha1.HCPRecoveryNamespace,
				Name:      state.recoveryRequestToProcess.RecoveryId,
			},
		},
	}

	if _, err := state.rdCrud.Create(ctx, readDesire, nil); err != nil {
		return fmt.Errorf("failed to create recovery read desire: %w", err)
	}
	return nil
}

func createRecoveryApplyDesire(ctx context.Context, state recoverySyncState) error {
	recoveryCr := hcprecoveryv1alpha1.HCPRecovery{
		TypeMeta: metav1.TypeMeta{
			APIVersion: hcprecoveryv1alpha1.SchemeGroupVersion.String(),
			Kind:       "HCPRecovery",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: state.recoveryRequestToProcess.RecoveryId,
		},
		Spec: hcprecoveryv1alpha1.HCPRecoverySpec{
			ClusterId: state.clusterID,
			BackupId:  state.recoveryRequestToProcess.BackupId,
		},
		Status: hcprecoveryv1alpha1.HCPRecoveryStatus{},
	}
	desireName := backup.RecoveryDesireNamePrefix + state.recoveryRequestToProcess.RecoveryId
	resourceIDStr := kubeapplierapi.ToClusterScopedApplyDesireResourceIDString(
		state.key.SubscriptionID,
		state.key.ResourceGroupName,
		state.key.HCPClusterName,
		desireName,
	)
	resourceID, err := azcorearm.ParseResourceID(resourceIDStr)
	if err != nil {
		return fmt.Errorf("failed to parse apply desire resource ID: %w", err)
	}

	raw, err := json.Marshal(recoveryCr)
	if err != nil {
		return fmt.Errorf("failed to marshal HCPRecovery: %w", err)
	}

	applyDesire := &kubeapplierapi.ApplyDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(state.mcResourceID.String()),
		},
		Spec: kubeapplierapi.ApplyDesireSpec{
			ManagementCluster: state.mcResourceID,
			Type:              kubeapplierapi.ApplyDesireTypeServerSideApply,
			TargetItem: kubeapplierapi.ResourceReference{
				Group:     hcprecoveryv1alpha1.SchemeGroupVersion.Group,
				Version:   hcprecoveryv1alpha1.SchemeGroupVersion.Version,
				Resource:  hcprecoveryv1alpha1.HCPRecoveryResource,
				Namespace: hcprecoveryv1alpha1.HCPRecoveryNamespace,
				Name:      state.recoveryRequestToProcess.RecoveryId,
			},
			ServerSideApply: &kubeapplierapi.ServerSideApplyConfig{
				KubeContent: &runtime.RawExtension{Raw: raw},
			},
		},
	}

	if _, err := state.adCrud.Create(ctx, applyDesire, nil); err != nil {
		return fmt.Errorf("failed to create recovery apply desire: %w", err)
	}
	return nil
}

func fetchSchedules(ctx context.Context, state recoverySyncState) ([]*kubeapplierapi.ReadDesire, error) {
	iterator, err := state.rdCrud.List(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list ReadDesires: %w", err)
	}
	var scheduleDesires []*kubeapplierapi.ReadDesire

	for _, rd := range iterator.Items(ctx) {
		if strings.HasPrefix(rd.ResourceID.Name, backup.BackupScheduleDesireNamePrefix) {
			scheduleDesires = append(scheduleDesires, rd)
		}
	}

	if err := iterator.GetError(); err != nil {
		return nil, fmt.Errorf("failed to iterate ReadDesires: %w", err)
	}

	return scheduleDesires, nil
}

func areAllSchedulesPaused(schedulesReadDesires []*kubeapplierapi.ReadDesire) bool {
	for _, sd := range schedulesReadDesires {
		if sd.Status.KubeContent == nil {
			return false
		}
		var schedule velerov1.Schedule
		if err := json.Unmarshal(sd.Status.KubeContent.Raw, &schedule); err != nil {
			return false
		}
		if !schedule.Spec.Paused {
			return false
		}
	}
	return true
}

func isTerminal(restoreState coreapi.RecoveryState) bool {
	return restoreState == coreapi.RecoveryStateCompleted || restoreState == coreapi.RecoveryStateFailed
}
func (c *recoverySyncer) CooldownChecker() internalcontrollerutils.CooldownChecker {
	return c.cooldownChecker
}
