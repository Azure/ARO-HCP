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
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/backup"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

func makeOnDemandAD(t *testing.T, mcResourceID *azcorearm.ResourceID, name string) *kubeapplier.ApplyDesire {
	t.Helper()
	resourceIDStr := kubeapplier.ToClusterScopedApplyDesireResourceIDString("test-sub", "test-rg", "test-cluster", name)
	resourceID := api.Must(azcorearm.ParseResourceID(resourceIDStr))
	return &kubeapplier.ApplyDesire{
		CosmosMetadata: api.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(mcResourceID.String())},
		Spec: kubeapplier.ApplyDesireSpec{
			ManagementCluster: mcResourceID,
			Type:              kubeapplier.ApplyDesireTypeServerSideApply,
			TargetItem: kubeapplier.ResourceReference{
				Group: veleroGroup, Version: veleroVersion,
				Resource: "backups", Namespace: veleroNamespace, Name: name,
			},
			ServerSideApply: &kubeapplier.ServerSideApplyConfig{
				KubeContent: &runtime.RawExtension{Raw: []byte(`{"kind":"Backup"}`)},
			},
		},
	}
}

func makeOnDemandRD(t *testing.T, mcResourceID *azcorearm.ResourceID, name string, kubeContent *runtime.RawExtension) *kubeapplier.ReadDesire {
	t.Helper()
	resourceIDStr := kubeapplier.ToClusterScopedReadDesireResourceIDString("test-sub", "test-rg", "test-cluster", name)
	resourceID := api.Must(azcorearm.ParseResourceID(resourceIDStr))
	rd := &kubeapplier.ReadDesire{
		CosmosMetadata: api.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(mcResourceID.String())},
		Spec: kubeapplier.ReadDesireSpec{
			ManagementCluster: mcResourceID,
		},
	}
	rd.Status.KubeContent = kubeContent
	return rd
}

func TestCleanupCompletedOnDemandBackupDesires(t *testing.T) {
	mcResourceID := api.Must(fleet.ToManagementClusterResourceID("mc1"))
	adName := backup.OndemandBackupDesireNamePrefix + "my-backup"
	scheduleName := backup.BackupScheduleDesireNamePrefix + "hourly"

	tests := []struct {
		name           string
		adCrudOverride database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire]
		setup          func(t *testing.T, adCrud database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire], rdCrud database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire])
		wantErr        bool
		checkADName    string
		wantADGone     bool
		checkRDName    string
		wantRDGone     bool
	}{
		{
			name: "pass1: deletes successful on-demand AD, leaves RD intact",
			setup: func(t *testing.T, adCrud database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire], rdCrud database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire]) {
				ad := makeOnDemandAD(t, mcResourceID, adName)
				ad.Status.Conditions = []metav1.Condition{
					{Type: kubeapplier.ConditionTypeSuccessful, Status: metav1.ConditionTrue},
				}
				_, _ = adCrud.Create(context.Background(), ad, nil)
				_, _ = rdCrud.Create(context.Background(), makeOnDemandRD(t, mcResourceID, adName, &runtime.RawExtension{Raw: []byte(`{}`)}), nil)
			},
			checkADName: adName,
			wantADGone:  true,
			checkRDName: adName,
			wantRDGone:  false,
		},
		{
			name: "pass1: skips non-successful on-demand AD",
			setup: func(t *testing.T, adCrud database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire], rdCrud database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire]) {
				_, _ = adCrud.Create(context.Background(), makeOnDemandAD(t, mcResourceID, adName), nil)
			},
			checkADName: adName,
			wantADGone:  false,
		},
		{
			name: "pass2: deletes on-demand RD when KubeContent nil and AD gone",
			setup: func(t *testing.T, adCrud database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire], rdCrud database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire]) {
				_, _ = rdCrud.Create(context.Background(), makeOnDemandRD(t, mcResourceID, adName, nil), nil)
			},
			checkRDName: adName,
			wantRDGone:  true,
		},
		{
			name: "pass2: skips RD when KubeContent non-nil (Velero backup still exists)",
			setup: func(t *testing.T, adCrud database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire], rdCrud database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire]) {
				_, _ = rdCrud.Create(context.Background(), makeOnDemandRD(t, mcResourceID, adName, &runtime.RawExtension{Raw: []byte(`{}`)}), nil)
			},
			checkRDName: adName,
			wantRDGone:  false,
		},
		{
			name: "pass2: skips RD when AD still exists (backup not yet applied)",
			setup: func(t *testing.T, adCrud database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire], rdCrud database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire]) {
				_, _ = adCrud.Create(context.Background(), makeOnDemandAD(t, mcResourceID, adName), nil)
				_, _ = rdCrud.Create(context.Background(), makeOnDemandRD(t, mcResourceID, adName, nil), nil)
			},
			checkRDName: adName,
			wantRDGone:  false,
		},
		{
			name:           "pass1: DB error on List returns error",
			adCrudOverride: &erroringADCrud{err: fmt.Errorf("cosmos unavailable")},
			wantErr:        true,
		},
		{
			name: "non-on-demand ADs are skipped",
			setup: func(t *testing.T, adCrud database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire], rdCrud database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire]) {
				scheduleAD := makeOnDemandAD(t, mcResourceID, scheduleName)
				scheduleAD.Status.Conditions = []metav1.Condition{
					{Type: kubeapplier.ConditionTypeSuccessful, Status: metav1.ConditionTrue},
				}
				_, _ = adCrud.Create(context.Background(), scheduleAD, nil)
			},
			checkADName: scheduleName,
			wantADGone:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockKA := databasetesting.NewMockKubeApplierDBClient()
			adCrud, _ := mockKA.ApplyDesiresForCluster("test-sub", "test-rg", "test-cluster")
			rdCrud, _ := mockKA.ReadDesiresForCluster("test-sub", "test-rg", "test-cluster")

			if tt.setup != nil {
				tt.setup(t, adCrud, rdCrud)
			}

			activeAdCrud := adCrud
			if tt.adCrudOverride != nil {
				activeAdCrud = tt.adCrudOverride
			}

			err := cleanupCompletedOnDemandBackupDesires(context.Background(), activeAdCrud, rdCrud)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tt.checkADName != "" {
				_, err := adCrud.Get(context.Background(), tt.checkADName)
				if tt.wantADGone {
					assert.True(t, database.IsNotFoundError(err), "AD %s should be deleted", tt.checkADName)
				} else {
					assert.NoError(t, err, "AD %s should still exist", tt.checkADName)
				}
			}
			if tt.checkRDName != "" {
				_, err := rdCrud.Get(context.Background(), tt.checkRDName)
				if tt.wantRDGone {
					assert.True(t, database.IsNotFoundError(err), "RD %s should be deleted", tt.checkRDName)
				} else {
					assert.NoError(t, err, "RD %s should still exist", tt.checkRDName)
				}
			}
		})
	}
}
