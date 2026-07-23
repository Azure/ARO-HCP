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
package backupcontroller

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"

	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/backup"
)

const (
	veleroGroup     = "velero.io"
	veleroVersion   = "v1"
	veleroNamespace = "velero"

	veleroScheduleResource = "schedules"
	veleroBackupResource   = "backups"

	keyRotationBackupNameSeparator = "-keyrotation-"
)

func backupApplyDesireName(scheduleName string) string {
	return backup.BackupScheduleDesireNamePrefix + scheduleName
}

func keyRotationBackupName(clusterServiceID, keyVersion string) string {
	return fmt.Sprintf("%s"+keyRotationBackupNameSeparator+"%s", clusterServiceID, keyVersion)
}

func keyRotationDesireName(backupName string) string {
	return backup.OndemandBackupDesireNamePrefix + backupName
}

// azureKMSKeyFingerprint mirrors hypershift's FingerprintAzureKMSKey.
func azureKMSKeyFingerprint(keyVaultName, keyName, keyVersion string) string {
	h := sha256.Sum256([]byte(keyVaultName + "/" + keyName + "/" + keyVersion))
	return fmt.Sprintf("%x", h)
}

func extractKeyVersionFromDesireName(desireName string) (string, bool) {
	_, after, found := strings.Cut(desireName, keyRotationBackupNameSeparator)
	return after, found
}

func buildDeleteApplyDesireForBackup(
	subscriptionID, resourceGroupName, clusterName, desireName string,
	mcResourceID *azcorearm.ResourceID,
	backupName string,
) (*kubeapplier.ApplyDesire, error) {
	resourceIDStr := kubeapplier.ToClusterScopedApplyDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, desireName,
	)
	resourceID, err := azcorearm.ParseResourceID(resourceIDStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ApplyDesire resource ID: %w", err)
	}
	return &kubeapplier.ApplyDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(mcResourceID.String()),
		},
		Spec: kubeapplier.ApplyDesireSpec{
			ManagementCluster: mcResourceID,
			Type:              kubeapplier.ApplyDesireTypeDelete,
			TargetItem: kubeapplier.ResourceReference{
				Group:     veleroGroup,
				Version:   veleroVersion,
				Resource:  veleroBackupResource,
				Namespace: veleroNamespace,
				Name:      backupName,
			},
		},
	}, nil
}

func buildApplyDesiresFromSchedules(
	subscriptionID, resourceGroupName, clusterName string,
	mcResourceID *azcorearm.ResourceID,
	schedules []*velerov1.Schedule,
) ([]*kubeapplier.ApplyDesire, error) {
	desires := make([]*kubeapplier.ApplyDesire, 0, len(schedules))
	for _, schedule := range schedules {
		desireName := backupApplyDesireName(schedule.Name)
		resourceIDStr := kubeapplier.ToClusterScopedApplyDesireResourceIDString(
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

		desires = append(desires, &kubeapplier.ApplyDesire{
			CosmosMetadata: api.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(mcResourceID.String())},
			Spec: kubeapplier.ApplyDesireSpec{
				ManagementCluster: mcResourceID,
				Type:              kubeapplier.ApplyDesireTypeServerSideApply,
				TargetItem: kubeapplier.ResourceReference{
					Group:     veleroGroup,
					Version:   veleroVersion,
					Resource:  veleroScheduleResource,
					Namespace: veleroNamespace,
					Name:      schedule.Name,
				},
				ServerSideApply: &kubeapplier.ServerSideApplyConfig{
					KubeContent: &runtime.RawExtension{Raw: raw},
				},
			},
		})
	}
	return desires, nil
}

func buildDeleteApplyDesireFromApplyDesire(
	ad *kubeapplier.ApplyDesire,
	mcResourceID *azcorearm.ResourceID,
) *kubeapplier.ApplyDesire {
	adDesire := &kubeapplier.ApplyDesire{
		Spec: kubeapplier.ApplyDesireSpec{
			ManagementCluster: mcResourceID,
			Type:              kubeapplier.ApplyDesireTypeDelete,
			TargetItem:        ad.Spec.TargetItem,
		},
	}
	adDesire.CosmosMetadata = *ad.CosmosMetadata.DeepCopy()
	return adDesire
}

// buildOnDemandBackupDesires returns a paired AD+RD. Unlike schedules, on-demand
// backups are single-shot — splitting would allow callers to create one without
// the other, producing an unobservable backup.
func buildOnDemandBackupDesires(
	subscriptionID, resourceGroupName, clusterName, desireName string,
	mcResourceID *azcorearm.ResourceID,
	veleroBackup *velerov1.Backup,
) (*kubeapplier.ApplyDesire, *kubeapplier.ReadDesire, error) {
	adResourceIDStr := kubeapplier.ToClusterScopedApplyDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, desireName,
	)
	adResourceID, err := azcorearm.ParseResourceID(adResourceIDStr)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse ApplyDesire resource ID: %w", err)
	}

	raw, err := json.Marshal(veleroBackup)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal backup: %w", err)
	}

	partitionKey := strings.ToLower(mcResourceID.String())

	ad := &kubeapplier.ApplyDesire{
		CosmosMetadata: api.CosmosMetadata{ResourceID: adResourceID, PartitionKey: partitionKey},
		Spec: kubeapplier.ApplyDesireSpec{
			ManagementCluster: mcResourceID,
			Type:              kubeapplier.ApplyDesireTypeServerSideApply,
			TargetItem: kubeapplier.ResourceReference{
				Group:     veleroGroup,
				Version:   veleroVersion,
				Resource:  veleroBackupResource,
				Namespace: veleroNamespace,
				Name:      veleroBackup.Name,
			},
			ServerSideApply: &kubeapplier.ServerSideApplyConfig{
				KubeContent: &runtime.RawExtension{Raw: raw},
			},
		},
	}

	rdResourceIDStr := kubeapplier.ToClusterScopedReadDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, desireName,
	)
	rdResourceID, err := azcorearm.ParseResourceID(rdResourceIDStr)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse ReadDesire resource ID: %w", err)
	}

	rd := &kubeapplier.ReadDesire{
		CosmosMetadata: api.CosmosMetadata{ResourceID: rdResourceID, PartitionKey: partitionKey},
		Spec: kubeapplier.ReadDesireSpec{
			ManagementCluster: mcResourceID,
			TargetItem: kubeapplier.ResourceReference{
				Group:     veleroGroup,
				Version:   veleroVersion,
				Resource:  veleroBackupResource,
				Namespace: veleroNamespace,
				Name:      veleroBackup.Name,
			},
		},
	}

	return ad, rd, nil
}

func buildReadDesiresFromSchedules(
	subscriptionID, resourceGroupName, clusterName string,
	mcResourceID *azcorearm.ResourceID,
	schedules []*velerov1.Schedule,
) ([]*kubeapplier.ReadDesire, error) {
	desires := make([]*kubeapplier.ReadDesire, 0, len(schedules))
	for _, schedule := range schedules {
		desireName := backupApplyDesireName(schedule.Name)
		resourceIDStr := kubeapplier.ToClusterScopedReadDesireResourceIDString(
			subscriptionID, resourceGroupName, clusterName, desireName,
		)
		resourceID, err := azcorearm.ParseResourceID(resourceIDStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse ReadDesire resource ID for schedule %s: %w", schedule.Name, err)
		}

		desires = append(desires, &kubeapplier.ReadDesire{
			CosmosMetadata: api.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(mcResourceID.String())},
			Spec: kubeapplier.ReadDesireSpec{
				ManagementCluster: mcResourceID,
				TargetItem: kubeapplier.ResourceReference{
					Group:     veleroGroup,
					Version:   veleroVersion,
					Resource:  veleroScheduleResource,
					Namespace: veleroNamespace,
					Name:      schedule.Name,
				},
			},
		})
	}
	return desires, nil
}
