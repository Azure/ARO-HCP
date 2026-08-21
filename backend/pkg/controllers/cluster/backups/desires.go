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
package backups

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/backup"
	internalcontrollerutils "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func backupApplyDesireName(scheduleName string) string {
	return backup.BackupScheduleDesireNamePrefix + scheduleName
}

func buildApplyDesiresFromSchedules(
	subscriptionID, resourceGroupName, clusterName string,
	managementClusterResourceID *azcorearm.ResourceID,
	schedules []*velerov1.Schedule,
) ([]*kubeapplierapi.ApplyDesire, error) {
	desires := make([]*kubeapplierapi.ApplyDesire, 0, len(schedules))
	for _, schedule := range schedules {
		desireName := backupApplyDesireName(schedule.Name)
		resourceIDStr := kubeapplierapi.ToClusterScopedApplyDesireResourceIDString(
			subscriptionID, resourceGroupName, clusterName, desireName,
		)
		resourceID, err := azcorearm.ParseResourceID(resourceIDStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse ApplyDesire resource ID for schedule %s: %w", schedule.Name, err)
		}

		raw, err := json.Marshal(schedule)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal schedule %s: %w", schedule.Name, err)
		}

		desires = append(desires, &kubeapplierapi.ApplyDesire{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(managementClusterResourceID.String())},
			Spec: kubeapplierapi.ApplyDesireSpec{
				ManagementCluster: managementClusterResourceID,
				Type:              kubeapplierapi.ApplyDesireTypeServerSideApply,
				TargetItem: kubeapplierapi.ResourceReference{
					Group:     backup.VeleroGroup,
					Version:   backup.VeleroVersion,
					Resource:  backup.VeleroScheduleResource,
					Namespace: backup.VeleroNamespace,
					Name:      schedule.Name,
				},
				ServerSideApply: &kubeapplierapi.ServerSideApplyConfig{
					KubeContent: &runtime.RawExtension{Raw: raw},
				},
			},
			Tags: map[string]string{backup.DesireTagKeySchedule: ""},
		})
	}
	return desires, nil
}

func ensureApplyDesire(
	ctx context.Context,
	lister kubeapplierlisters.ApplyDesireLister,
	applyDesireCRUD cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire],
	subscriptionID, resourceGroupName, clusterName string,
	desired *kubeapplierapi.ApplyDesire,
) (bool, error) {
	logger := utils.LoggerFromContext(ctx)

	existing, err := lister.GetForCluster(ctx, subscriptionID, resourceGroupName, clusterName, desired.ResourceID.Name)
	if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
		return false, utils.TrackError(fmt.Errorf("get ApplyDesire %s from lister: %w", desired.ResourceID.Name, err))
	}

	if existing != nil && !internalcontrollerutils.NeedsUpdate(existing.Spec, desired.Spec) {
		return false, nil
	}

	if existing == nil {
		if _, err := applyDesireCRUD.Create(ctx, desired, nil); err != nil && !cosmosstorageutils.IsConflictError(err) {
			return false, utils.TrackError(fmt.Errorf("create ApplyDesire %s: %w", desired.ResourceID.Name, err))
		}
		logger.Info("created ApplyDesire", "desire", desired.ResourceID.Name)
		return true, nil
	}

	replacement := existing.DeepCopy()
	replacement.Spec = desired.Spec
	if _, err := applyDesireCRUD.Replace(ctx, replacement, nil); err != nil {
		if cosmosstorageutils.IsPreconditionFailedError(err) {
			return false, nil
		}
		return false, utils.TrackError(fmt.Errorf("replace ApplyDesire %s: %w", desired.ResourceID.Name, err))
	}
	logger.Info("updated ApplyDesire", "desire", desired.ResourceID.Name)
	return true, nil
}

func ensureReadDesireFromApplyDesire(
	ctx context.Context,
	lister kubeapplierlisters.ReadDesireLister,
	readDesireCRUD cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire],
	subscriptionID, resourceGroupName, clusterName string,
	applyDesire *kubeapplierapi.ApplyDesire,
) (bool, error) {
	logger := utils.LoggerFromContext(ctx)

	resourceIDStr := kubeapplierapi.ToClusterScopedReadDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, applyDesire.ResourceID.Name,
	)
	resourceID, err := azcorearm.ParseResourceID(resourceIDStr)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("parse ReadDesire resource ID for %s: %w", applyDesire.ResourceID.Name, err))
	}

	desired := &kubeapplierapi.ReadDesire{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: applyDesire.PartitionKey},
		Spec: kubeapplierapi.ReadDesireSpec{
			ManagementCluster: applyDesire.Spec.ManagementCluster,
			TargetItem:        applyDesire.Spec.TargetItem,
		},
		Tags: applyDesire.Tags,
	}

	existing, err := lister.GetForCluster(ctx, subscriptionID, resourceGroupName, clusterName, applyDesire.ResourceID.Name)
	if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
		return false, utils.TrackError(fmt.Errorf("get ReadDesire %s from lister: %w", applyDesire.ResourceID.Name, err))
	}

	if existing != nil && !internalcontrollerutils.NeedsUpdate(existing.Spec, desired.Spec) {
		return false, nil
	}

	if existing == nil {
		if _, err := readDesireCRUD.Create(ctx, desired, nil); err != nil && !cosmosstorageutils.IsConflictError(err) {
			return false, utils.TrackError(fmt.Errorf("create ReadDesire %s: %w", desired.ResourceID.Name, err))
		}
		logger.Info("created ReadDesire", "desire", desired.ResourceID.Name)
		return true, nil
	}

	replacement := existing.DeepCopy()
	replacement.Spec = desired.Spec
	if _, err := readDesireCRUD.Replace(ctx, replacement, nil); err != nil {
		if cosmosstorageutils.IsPreconditionFailedError(err) {
			return false, nil
		}
		return false, utils.TrackError(fmt.Errorf("replace ReadDesire %s: %w", desired.ResourceID.Name, err))
	}
	logger.Info("updated ReadDesire", "desire", desired.ResourceID.Name)
	return true, nil
}

// isDesireSuccessful reports whether the given desire conditions include a
// successful reconciliation.
func isDesireSuccessful(conditions []metav1.Condition) bool {
	for _, condition := range conditions {
		if condition.Type == kubeapplierapi.ConditionTypeSuccessful && condition.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}

// purgeApplyDesire retires an ApplyDesire by deleting its Cosmos document directly,
// leaving the object it applied untouched on the management cluster.
//
// Per the ApplyDesire contract (see internal/api/kubeapplierapi/types_apply_desire.go),
// removing the document is the sanctioned way to stop reconciliation without affecting the
// applied target. Unlike converting a desire to Type=Delete (which makes the kube-applier
// delete the target), purging leaves the applied object in place. Use purgeApplyDesire for
// one-shot desires whose applied object must outlive the desire — e.g. an on-demand backup
// that must remain a valid restore point until Velero expires it at its own TTL.
func purgeApplyDesire(
	ctx context.Context,
	applyDesire kubeapplierapi.ApplyDesire,
	applyDesireCRUD cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire],
) error {
	name := applyDesire.ResourceID.Name
	if err := applyDesireCRUD.Delete(ctx, name); err != nil && !cosmosstorageutils.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to purge ApplyDesire %s: %w", name, err))
	}
	return nil
}
