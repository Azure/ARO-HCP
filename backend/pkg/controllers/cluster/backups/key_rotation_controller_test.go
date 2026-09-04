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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	hyperv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/backup"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
)

func TestRotationComplete(t *testing.T) {
	tests := []struct {
		name   string
		hc     *hyperv1beta1.HostedCluster
		expect bool
	}{
		{
			name:   "empty history returns false",
			hc:     &hyperv1beta1.HostedCluster{},
			expect: false,
		},
		{
			name: "target differs from active returns false",
			hc: &hyperv1beta1.HostedCluster{
				Status: hyperv1beta1.HostedClusterStatus{
					SecretEncryption: hyperv1beta1.SecretEncryptionStatus{
						ActiveKey: hyperv1beta1.SecretEncryptionKeyStatus{Azure: hyperv1beta1.AzureKMSKey{KeyVersion: "v1"}},
						TargetKey: hyperv1beta1.SecretEncryptionKeyStatus{Azure: hyperv1beta1.AzureKMSKey{KeyVersion: "v2"}},
						History: []hyperv1beta1.EncryptionMigrationHistory{
							{State: hyperv1beta1.EncryptionMigrationStateMigrating},
						},
					},
				},
			},
			expect: false,
		},
		{
			name: "migrating state returns false",
			hc: &hyperv1beta1.HostedCluster{
				Status: hyperv1beta1.HostedClusterStatus{
					SecretEncryption: hyperv1beta1.SecretEncryptionStatus{
						ActiveKey: hyperv1beta1.SecretEncryptionKeyStatus{Azure: hyperv1beta1.AzureKMSKey{KeyVersion: "v2"}},
						History: []hyperv1beta1.EncryptionMigrationHistory{
							{State: hyperv1beta1.EncryptionMigrationStateMigrating},
						},
					},
				},
			},
			expect: false,
		},
		{
			name: "completed state returns true",
			hc: &hyperv1beta1.HostedCluster{
				Status: hyperv1beta1.HostedClusterStatus{
					SecretEncryption: hyperv1beta1.SecretEncryptionStatus{
						ActiveKey: hyperv1beta1.SecretEncryptionKeyStatus{Azure: hyperv1beta1.AzureKMSKey{KeyVersion: "v2"}},
						History: []hyperv1beta1.EncryptionMigrationHistory{
							{State: hyperv1beta1.EncryptionMigrationStateCompleted},
						},
					},
				},
			},
			expect: true,
		},
		{
			// Guards the removed target==active branch: an unreachable state, since the
			// HostedClusterConfigOperator clears TargetKey when it sets History[0]=Completed.
			name: "completed history with non-empty target returns false",
			hc: &hyperv1beta1.HostedCluster{
				Status: hyperv1beta1.HostedClusterStatus{
					SecretEncryption: hyperv1beta1.SecretEncryptionStatus{
						ActiveKey: hyperv1beta1.SecretEncryptionKeyStatus{Azure: hyperv1beta1.AzureKMSKey{KeyVersion: "v2"}},
						TargetKey: hyperv1beta1.SecretEncryptionKeyStatus{Azure: hyperv1beta1.AzureKMSKey{KeyVersion: "v3"}},
						History: []hyperv1beta1.EncryptionMigrationHistory{
							{State: hyperv1beta1.EncryptionMigrationStateCompleted},
						},
					},
				},
			},
			expect: false,
		},
		{
			name: "completed history with target==active returns false",
			hc: &hyperv1beta1.HostedCluster{
				Status: hyperv1beta1.HostedClusterStatus{
					SecretEncryption: hyperv1beta1.SecretEncryptionStatus{
						ActiveKey: hyperv1beta1.SecretEncryptionKeyStatus{Azure: hyperv1beta1.AzureKMSKey{KeyVersion: "v2"}},
						TargetKey: hyperv1beta1.SecretEncryptionKeyStatus{Azure: hyperv1beta1.AzureKMSKey{KeyVersion: "v2"}},
						History: []hyperv1beta1.EncryptionMigrationHistory{
							{State: hyperv1beta1.EncryptionMigrationStateCompleted},
						},
					},
				},
			},
			expect: false,
		},
		{
			// Unreachable given the HostedClusterConfigOperator's single-patch atomicity;
			// pins that completion requires History[0]==Completed, not just an empty TargetKey.
			name: "stale migrating history with cleared target returns false",
			hc: &hyperv1beta1.HostedCluster{
				Status: hyperv1beta1.HostedClusterStatus{
					SecretEncryption: hyperv1beta1.SecretEncryptionStatus{
						ActiveKey: hyperv1beta1.SecretEncryptionKeyStatus{Azure: hyperv1beta1.AzureKMSKey{KeyVersion: "v2"}},
						History: []hyperv1beta1.EncryptionMigrationHistory{
							{State: hyperv1beta1.EncryptionMigrationStateMigrating},
						},
					},
				},
			},
			expect: false,
		},
		{
			// Not emitted by the pinned HostedClusterConfigOperator version; defensive
			// coverage of a state the type allows, where TargetKey is left non-empty
			// (replaced, not cleared).
			name: "interrupted state returns false",
			hc: &hyperv1beta1.HostedCluster{
				Status: hyperv1beta1.HostedClusterStatus{
					SecretEncryption: hyperv1beta1.SecretEncryptionStatus{
						ActiveKey: hyperv1beta1.SecretEncryptionKeyStatus{Azure: hyperv1beta1.AzureKMSKey{KeyVersion: "v1"}},
						TargetKey: hyperv1beta1.SecretEncryptionKeyStatus{Azure: hyperv1beta1.AzureKMSKey{KeyVersion: "v3"}},
						History: []hyperv1beta1.EncryptionMigrationHistory{
							{State: hyperv1beta1.EncryptionMigrationStateInterrupted},
						},
					},
				},
			},
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, rotationComplete(tt.hc))
		})
	}
}

func TestKeyRotationBackupName(t *testing.T) {
	fp := backup.AzureKMSKeyFingerprint("vault1", "key1", "v1")
	name := keyRotationBackupName("ocm-test-cluster", fp)
	assert.Contains(t, name, keyRotationBackupNameSeparator)
	assert.True(t, strings.HasPrefix(name, "ocm-test-cluster"+keyRotationBackupNameSeparator))
}

func TestKeyRotationDesireName(t *testing.T) {
	name := keyRotationDesireName("backup-name")
	assert.Equal(t, backup.OndemandBackupDesireNamePrefix+"backup-name", name)
}

func TestKeyRotationBackupSyncer_SyncOnce(t *testing.T) {
	const (
		testClusterID    = "11111111111111111111111111111111"
		testClusterIDStr = "/api/aro_hcp/v1alpha1/clusters/" + testClusterID
		testEnvID        = "test-env"
		testDomainPrefix = "test-domprefix"
		testStampID      = "mc1"
	)

	testBackupConfig := &BackupConfig{
		BackupCadenceProfile: BackupCadenceProduction,
	}

	testMgmtClusterResourceID := func() *azcorearm.ResourceID {
		return metadataapi.Must(fleetapi.ToManagementClusterResourceID(testStampID))
	}

	testKey := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	hostedClusterNamespace := controllerutils.HostedClusterNamespace(testEnvID, testClusterID)
	controlPlaneNamespace := fmt.Sprintf("%s-%s", hostedClusterNamespace, testDomainPrefix)

	withKMS := func(c *coreapi.HCPOpenShiftCluster) {
		c.CustomerProperties.Etcd.DataEncryption.CustomerManaged = &coreapi.CustomerManagedEncryptionProfile{
			Kms: &coreapi.KmsEncryptionProfile{
				ActiveKey: coreapi.KmsKey{Version: "v2", Name: "key1", VaultName: "vault1"},
			},
		}
	}

	completedRotationHC := func(t *testing.T) *kubeapplierapi.ReadDesire {
		t.Helper()
		hc := &hyperv1beta1.HostedCluster{}
		hc.Status.SecretEncryption = hyperv1beta1.SecretEncryptionStatus{
			ActiveKey: hyperv1beta1.SecretEncryptionKeyStatus{
				Azure: hyperv1beta1.AzureKMSKey{KeyVaultName: "vault1", KeyName: "key1", KeyVersion: "v2"},
			},
			History: []hyperv1beta1.EncryptionMigrationHistory{
				{State: hyperv1beta1.EncryptionMigrationStateCompleted},
			},
		}
		raw, err := json.Marshal(hc)
		require.NoError(t, err)
		rdResourceIDStr := kubeapplierapi.ToClusterScopedReadDesireResourceIDString(
			testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName,
			kubeapplierhelpers.ReadDesireNameReadonlyHostedCluster,
		)
		return &kubeapplierapi.ReadDesire{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   metadataapi.Must(azcorearm.ParseResourceID(rdResourceIDStr)),
				PartitionKey: strings.ToLower(testMgmtClusterResourceID().String()),
			},
			Status: kubeapplierapi.ReadDesireStatus{
				KubeContent: &runtime.RawExtension{Raw: raw},
			},
		}
	}

	migratingHC := func(t *testing.T) *kubeapplierapi.ReadDesire {
		t.Helper()
		hc := &hyperv1beta1.HostedCluster{}
		hc.Status.SecretEncryption = hyperv1beta1.SecretEncryptionStatus{
			ActiveKey: hyperv1beta1.SecretEncryptionKeyStatus{
				Azure: hyperv1beta1.AzureKMSKey{KeyVaultName: "vault1", KeyName: "key1", KeyVersion: "v1"},
			},
			TargetKey: hyperv1beta1.SecretEncryptionKeyStatus{
				Azure: hyperv1beta1.AzureKMSKey{KeyVaultName: "vault1", KeyName: "key1", KeyVersion: "v2"},
			},
			History: []hyperv1beta1.EncryptionMigrationHistory{
				{State: hyperv1beta1.EncryptionMigrationStateMigrating},
			},
		}
		raw, err := json.Marshal(hc)
		require.NoError(t, err)
		rdResourceIDStr := kubeapplierapi.ToClusterScopedReadDesireResourceIDString(
			testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName,
			kubeapplierhelpers.ReadDesireNameReadonlyHostedCluster,
		)
		return &kubeapplierapi.ReadDesire{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   metadataapi.Must(azcorearm.ParseResourceID(rdResourceIDStr)),
				PartitionKey: strings.ToLower(testMgmtClusterResourceID().String()),
			},
			Status: kubeapplierapi.ReadDesireStatus{
				KubeContent: &runtime.RawExtension{Raw: raw},
			},
		}
	}

	expectedFingerprint := backup.AzureKMSKeyFingerprint("vault1", "key1", "v2")
	expectedBackupName := keyRotationBackupName(hostedClusterNamespace, expectedFingerprint)
	expectedDesireName := keyRotationDesireName(expectedBackupName)

	// A desire pair from a previous key rotation (fingerprint v1), distinct from
	// the current desired one (v2).
	staleFingerprint := backup.AzureKMSKeyFingerprint("vault1", "key1", "v1")
	staleBackupName := keyRotationBackupName(hostedClusterNamespace, staleFingerprint)
	staleDesireName := keyRotationDesireName(staleBackupName)

	// seedOnDemandDesire creates an on-demand key-rotation ApplyDesire (and,
	// optionally, its companion ReadDesire) for backupName, mimicking a desire
	// left behind by an earlier rotation.
	seedOnDemandDesire := func(t *testing.T, ctx context.Context, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient, backupName string, withReadDesire bool) {
		t.Helper()
		managementClusterResourceID := testMgmtClusterResourceID()
		ttl := testBackupConfig.Schedules()[0].TTL
		veleroBackup := backup.NewBackup(backupName, "resource-id", "", hostedClusterNamespace, controlPlaneNamespace, ttl)
		ad, err := buildOnDemandBackupApplyDesire(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName, managementClusterResourceID, veleroBackup)
		require.NoError(t, err)
		applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
		require.NoError(t, err)
		_, err = applyDesireCRUD.Create(ctx, ad, nil)
		require.NoError(t, err)
		if !withReadDesire {
			return
		}
		rdResourceIDStr := kubeapplierapi.ToClusterScopedReadDesireResourceIDString(
			testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName, ad.ResourceID.Name,
		)
		rd := &kubeapplierapi.ReadDesire{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   metadataapi.Must(azcorearm.ParseResourceID(rdResourceIDStr)),
				PartitionKey: ad.PartitionKey,
			},
			Spec: kubeapplierapi.ReadDesireSpec{
				ManagementCluster: ad.Spec.ManagementCluster,
				TargetItem:        ad.Spec.TargetItem,
			},
			Tags: ad.Tags,
		}
		readDesireCRUD, err := mockKubeApplier.ReadDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
		require.NoError(t, err)
		_, err = readDesireCRUD.Create(ctx, rd, nil)
		require.NoError(t, err)
	}

	// seedOnDemandDesireObserved creates an on-demand ApplyDesire/ReadDesire pair with the
	// given observed Backup phase.
	seedOnDemandDesireObserved := func(t *testing.T, ctx context.Context, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient, backupName, fingerprint string, phase velerov1.BackupPhase, kubeContentPresent bool) *kubeapplierapi.ApplyDesire {
		t.Helper()
		managementClusterResourceID := testMgmtClusterResourceID()
		ttl := testBackupConfig.Schedules()[0].TTL
		veleroBackup := backup.NewBackup(backupName, "resource-id", fingerprint, hostedClusterNamespace, controlPlaneNamespace, ttl)
		veleroBackup.Status.Phase = phase
		ad, err := buildOnDemandBackupApplyDesire(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName, managementClusterResourceID, veleroBackup)
		require.NoError(t, err)
		applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
		require.NoError(t, err)
		createdAD, err := applyDesireCRUD.Create(ctx, ad, nil)
		require.NoError(t, err)

		var kubeContent *runtime.RawExtension
		if kubeContentPresent {
			raw, err := json.Marshal(veleroBackup)
			require.NoError(t, err)
			kubeContent = &runtime.RawExtension{Raw: raw}
		}
		rdResourceIDStr := kubeapplierapi.ToClusterScopedReadDesireResourceIDString(
			testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName, ad.ResourceID.Name,
		)
		rd := &kubeapplierapi.ReadDesire{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   metadataapi.Must(azcorearm.ParseResourceID(rdResourceIDStr)),
				PartitionKey: ad.PartitionKey,
			},
			Spec: kubeapplierapi.ReadDesireSpec{
				ManagementCluster: ad.Spec.ManagementCluster,
				TargetItem:        ad.Spec.TargetItem,
			},
			Status: kubeapplierapi.ReadDesireStatus{
				KubeContent: kubeContent,
				Conditions: []metav1.Condition{
					{Type: kubeapplierapi.ConditionTypeSuccessful, Status: metav1.ConditionTrue},
				},
			},
			Tags: ad.Tags,
		}
		readDesireCRUD, err := mockKubeApplier.ReadDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
		require.NoError(t, err)
		_, err = readDesireCRUD.Create(ctx, rd, nil)
		require.NoError(t, err)
		return createdAD
	}

	// seedCompletedOnDemandDesire is seedOnDemandDesireObserved with a completed Backup
	// (KubeContent present, Phase=Completed) — the state that should trigger cleanup.
	seedCompletedOnDemandDesire := func(t *testing.T, ctx context.Context, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient, backupName, fingerprint string) *kubeapplierapi.ApplyDesire {
		t.Helper()
		return seedOnDemandDesireObserved(t, ctx, mockKubeApplier, backupName, fingerprint, velerov1.BackupPhaseCompleted, true)
	}

	newTestCluster := func(opts ...func(*coreapi.HCPOpenShiftCluster)) *coreapi.HCPOpenShiftCluster {
		resourceID := metadataapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testKey.SubscriptionID +
				"/resourceGroups/" + testKey.ResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testKey.HCPClusterName,
		))
		csID := metadataapi.Must(metadataapi.NewInternalID(testClusterIDStr))
		cluster := &coreapi.HCPOpenShiftCluster{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(resourceID.SubscriptionID)},
			TrackedResource: coreapi.TrackedResource{
				Resource: coreapi.Resource{ID: resourceID},
			},
			CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
				DNS: coreapi.CustomerDNSProfile{BaseDomainPrefix: testDomainPrefix},
			},
			ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
				ProvisioningState:       coreapi.ProvisioningStateSucceeded,
				ClusterServiceID:        &csID,
				BillingDocumentCosmosID: "test-billing-doc-id",
			},
		}
		for _, opt := range opts {
			opt(cluster)
		}
		return cluster
	}

	newTestServiceProviderCluster := func() *coreapi.ServiceProviderCluster {
		clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testKey.SubscriptionID +
				"/resourceGroups/" + testKey.ResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testKey.HCPClusterName,
		))
		serviceProviderClusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(fmt.Sprintf("%s/%s/%s",
			clusterResourceID.String(), coreapi.ServiceProviderClusterResourceTypeName, coreapi.ServiceProviderClusterResourceName)))
		return &coreapi.ServiceProviderCluster{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   serviceProviderClusterResourceID,
				PartitionKey: strings.ToLower(testKey.SubscriptionID),
			},
			Status: coreapi.ServiceProviderClusterStatus{
				ManagementClusterResourceID: testMgmtClusterResourceID(),
				HostedClusterNamespace:      hostedClusterNamespace,
				ControlPlaneNamespace:       controlPlaneNamespace,
			},
		}
	}

	tests := []struct {
		name                       string
		clusterOpts                []func(*coreapi.HCPOpenShiftCluster)
		hasPlacement               bool
		seedServiceProviderCluster func(spc *coreapi.ServiceProviderCluster)
		seedHCReadDesire           func(t *testing.T) *kubeapplierapi.ReadDesire
		seedKubeApplier            func(t *testing.T, ctx context.Context, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient)
		syncCount                  int
		expectError                bool
		verify                     func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient)
	}{
		{
			name: "cluster not found is no-op",
		},
		{
			name:             "no KMS encryption is no-op",
			hasPlacement:     true,
			seedHCReadDesire: completedRotationHC,
			verify: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				_, err = applyDesireCRUD.Get(ctx, expectedDesireName)
				assert.True(t, cosmosstorageutils.IsNotFoundError(err), "no on-demand backup should be created for non-KMS cluster")
			},
		},
		{
			name:         "no HostedCluster ReadDesire is no-op",
			clusterOpts:  []func(*coreapi.HCPOpenShiftCluster){withKMS},
			hasPlacement: true,
			verify: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				_, err = applyDesireCRUD.Get(ctx, expectedDesireName)
				assert.True(t, cosmosstorageutils.IsNotFoundError(err), "no on-demand backup without HC ReadDesire")
			},
		},
		{
			name:             "rotation not complete is no-op",
			clusterOpts:      []func(*coreapi.HCPOpenShiftCluster){withKMS},
			hasPlacement:     true,
			seedHCReadDesire: migratingHC,
			verify: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				_, err = applyDesireCRUD.Get(ctx, expectedDesireName)
				assert.True(t, cosmosstorageutils.IsNotFoundError(err), "no on-demand backup while rotation is in progress")
			},
		},
		{
			name:             "creates backup on completed rotation",
			clusterOpts:      []func(*coreapi.HCPOpenShiftCluster){withKMS},
			hasPlacement:     true,
			seedHCReadDesire: completedRotationHC,
			// The first sync creates both the ReadDesire and the ApplyDesire; the
			// second confirms the create-or-update path is idempotent and lets the
			// recorded fingerprint settle into the refreshed lister snapshot.
			syncCount: 2,
			verify: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				ad, err := applyDesireCRUD.Get(ctx, expectedDesireName)
				require.NoError(t, err, "on-demand backup ApplyDesire should exist")
				assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, ad.Spec.Type)
				assert.Equal(t, backup.VeleroBackupResource, ad.Spec.TargetItem.Resource)
				assert.Equal(t, expectedBackupName, ad.Spec.TargetItem.Name)
				assert.Contains(t, ad.Tags, backup.DesireTagKeyOndemandBackup, "on-demand backup ApplyDesire must carry the on-demand tag")
				assert.Equal(t, KeyRotationBackupControllerName, ad.Tags[kubeapplierapi.TagControllerName], "on-demand backup ApplyDesire must carry the controller-name tag required by EnsureApplyDesire")

				readDesireCRUD, err := mockKubeApplier.ReadDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				_, err = readDesireCRUD.Get(ctx, expectedDesireName)
				assert.NoError(t, err, "on-demand backup ReadDesire should exist")
			},
		},
		{
			name:             "idempotent when backup already exists",
			clusterOpts:      []func(*coreapi.HCPOpenShiftCluster){withKMS},
			hasPlacement:     true,
			seedHCReadDesire: completedRotationHC,
			seedKubeApplier: func(t *testing.T, ctx context.Context, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				managementClusterResourceID := testMgmtClusterResourceID()
				ttl := testBackupConfig.Schedules()[0].TTL
				veleroBackup := backup.NewBackup(expectedBackupName, "resource-id", expectedFingerprint, hostedClusterNamespace, controlPlaneNamespace, ttl)
				ad, err := buildOnDemandBackupApplyDesire(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName, managementClusterResourceID, veleroBackup)
				require.NoError(t, err)
				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				_, err = applyDesireCRUD.Create(ctx, ad, nil)
				require.NoError(t, err)

				rdResourceIDStr := kubeapplierapi.ToClusterScopedReadDesireResourceIDString(
					testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName, ad.ResourceID.Name,
				)
				rd := &kubeapplierapi.ReadDesire{
					CosmosMetadata: coreapi.CosmosMetadata{
						ResourceID:   metadataapi.Must(azcorearm.ParseResourceID(rdResourceIDStr)),
						PartitionKey: ad.PartitionKey,
					},
					Spec: kubeapplierapi.ReadDesireSpec{
						ManagementCluster: ad.Spec.ManagementCluster,
						TargetItem:        ad.Spec.TargetItem,
					},
					Tags: ad.Tags,
				}
				readDesireCRUD, err := mockKubeApplier.ReadDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				_, err = readDesireCRUD.Create(ctx, rd, nil)
				require.NoError(t, err)
			},
			verify: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				_, err = applyDesireCRUD.Get(ctx, expectedDesireName)
				assert.NoError(t, err, "existing on-demand backup should remain unchanged")
			},
		},
		{
			name:             "crash recovery recreates missing RD when AD exists",
			clusterOpts:      []func(*coreapi.HCPOpenShiftCluster){withKMS},
			hasPlacement:     true,
			seedHCReadDesire: completedRotationHC,
			seedKubeApplier: func(t *testing.T, ctx context.Context, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				managementClusterResourceID := testMgmtClusterResourceID()
				ttl := testBackupConfig.Schedules()[0].TTL
				veleroBackup := backup.NewBackup(expectedBackupName, "resource-id", expectedFingerprint, hostedClusterNamespace, controlPlaneNamespace, ttl)
				ad, err := buildOnDemandBackupApplyDesire(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName, managementClusterResourceID, veleroBackup)
				require.NoError(t, err)
				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				_, err = applyDesireCRUD.Create(ctx, ad, nil)
				require.NoError(t, err)
			},
			verify: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				readDesireCRUD, err := mockKubeApplier.ReadDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				_, err = readDesireCRUD.Get(ctx, expectedDesireName)
				assert.NoError(t, err, "missing RD should be recreated when AD exists")
			},
		},
		{
			name:             "crash recovery recreates missing AD when RD exists",
			clusterOpts:      []func(*coreapi.HCPOpenShiftCluster){withKMS},
			hasPlacement:     true,
			seedHCReadDesire: completedRotationHC,
			seedKubeApplier: func(t *testing.T, ctx context.Context, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				managementClusterResourceID := testMgmtClusterResourceID()
				rdResourceIDStr := kubeapplierapi.ToClusterScopedReadDesireResourceIDString(
					testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName, expectedDesireName,
				)
				rd := &kubeapplierapi.ReadDesire{
					CosmosMetadata: coreapi.CosmosMetadata{
						ResourceID:   metadataapi.Must(azcorearm.ParseResourceID(rdResourceIDStr)),
						PartitionKey: strings.ToLower(managementClusterResourceID.String()),
					},
					Spec: kubeapplierapi.ReadDesireSpec{
						ManagementCluster: managementClusterResourceID,
						TargetItem: kubeapplierapi.ResourceReference{
							Group: backup.VeleroGroup, Version: backup.VeleroVersion,
							Resource: backup.VeleroBackupResource, Namespace: backup.VeleroNamespace,
							Name: expectedBackupName,
						},
					},
					Tags: map[string]string{backup.DesireTagKeyOndemandBackup: ""},
				}
				readDesireCRUD, err := mockKubeApplier.ReadDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				_, err = readDesireCRUD.Create(ctx, rd, nil)
				require.NoError(t, err)
			},
			verify: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				_, err = applyDesireCRUD.Get(ctx, expectedDesireName)
				assert.NoError(t, err, "missing AD should be recreated when RD exists")
			},
		},
		{
			name:             "cleans up stale desire from previous key rotation",
			clusterOpts:      []func(*coreapi.HCPOpenShiftCluster){withKMS},
			hasPlacement:     true,
			seedHCReadDesire: completedRotationHC,
			seedKubeApplier: func(t *testing.T, ctx context.Context, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				seedOnDemandDesire(t, ctx, mockKubeApplier, staleBackupName, true)
			},
			// syncs 1-2 bootstrap the current RD/AD; sync3 purges the stale AD's document.
			syncCount: 3,
			verify: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)

				_, err = applyDesireCRUD.Get(ctx, staleDesireName)
				assert.True(t, cosmosstorageutils.IsNotFoundError(err), "stale ApplyDesire document should be purged (not converted to Delete, which would delete its Backup)")

				currentAD, err := applyDesireCRUD.Get(ctx, expectedDesireName)
				require.NoError(t, err, "current ApplyDesire should exist")
				assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, currentAD.Spec.Type, "current ApplyDesire should remain ServerSideApply")
			},
		},
		{
			name:             "does not delete current backup desire",
			clusterOpts:      []func(*coreapi.HCPOpenShiftCluster){withKMS},
			hasPlacement:     true,
			seedHCReadDesire: completedRotationHC,
			// syncs 1-2 bootstrap the current RD/AD; sync3 runs cleanup with no stale desires present.
			syncCount: 3,
			verify: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				currentAD, err := applyDesireCRUD.Get(ctx, expectedDesireName)
				require.NoError(t, err, "current ApplyDesire should exist")
				assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, currentAD.Spec.Type, "current ApplyDesire should not be touched by cleanup")
			},
		},
		{
			name:             "purges stale desire even when its ReadDesire is missing",
			clusterOpts:      []func(*coreapi.HCPOpenShiftCluster){withKMS},
			hasPlacement:     true,
			seedHCReadDesire: completedRotationHC,
			seedKubeApplier: func(t *testing.T, ctx context.Context, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				seedOnDemandDesire(t, ctx, mockKubeApplier, staleBackupName, false)
			},
			syncCount: 3,
			verify: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				_, err = applyDesireCRUD.Get(ctx, staleDesireName)
				assert.True(t, cosmosstorageutils.IsNotFoundError(err), "a superseded ApplyDesire is purged directly and does not depend on its ReadDesire")
			},
		},
		{
			name:             "skips creating when fingerprint already recorded",
			clusterOpts:      []func(*coreapi.HCPOpenShiftCluster){withKMS},
			hasPlacement:     true,
			seedHCReadDesire: completedRotationHC,
			seedServiceProviderCluster: func(spc *coreapi.ServiceProviderCluster) {
				spc.Status.KeyRotationBackupFingerprint = expectedFingerprint
			},
			verify: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				_, err = applyDesireCRUD.Get(ctx, expectedDesireName)
				assert.True(t, cosmosstorageutils.IsNotFoundError(err), "on-demand backup should not be recreated once its fingerprint is already recorded")
			},
		},
		{
			name:             "records fingerprint and purges current desire once successful",
			clusterOpts:      []func(*coreapi.HCPOpenShiftCluster){withKMS},
			hasPlacement:     true,
			seedHCReadDesire: completedRotationHC,
			seedKubeApplier: func(t *testing.T, ctx context.Context, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				seedCompletedOnDemandDesire(t, ctx, mockKubeApplier, expectedBackupName, expectedFingerprint)
			},
			// sync1 reconciles the seeded ApplyDesire's spec; sync2 records the fingerprint
			// on ServiceProviderCluster.Status and stops (fingerprint-before-purge
			// invariant); sync3 purges the ApplyDesire document.
			syncCount: 3,
			verify: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				spc, err := mockDB.ServiceProviderClusters(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				assert.Equal(t, expectedFingerprint, spc.Status.KeyRotationBackupFingerprint, "fingerprint should be recorded once the backup succeeds")

				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				_, err = applyDesireCRUD.Get(ctx, expectedDesireName)
				assert.True(t, cosmosstorageutils.IsNotFoundError(err), "current ApplyDesire document should be purged once its backup succeeds")

				// The ReadDesire is retained so the completed backup stays listable until Velero
				// expires it at its own TTL (its KubeContent still holds the Completed Backup).
				readDesireCRUD, err := mockKubeApplier.ReadDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				_, err = readDesireCRUD.Get(ctx, expectedDesireName)
				assert.NoError(t, err, "current ReadDesire should be retained while its Backup still exists")
			},
		},
		{
			// Regression test: kube-applier's Successful condition is a liveness signal
			// (true even when the target was never observed), not a completion signal.
			// Misreading it as "backup completed" caused premature deletion.
			name:             "does not delete current desire when Successful but backup never observed",
			clusterOpts:      []func(*coreapi.HCPOpenShiftCluster){withKMS},
			hasPlacement:     true,
			seedHCReadDesire: completedRotationHC,
			seedKubeApplier: func(t *testing.T, ctx context.Context, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				seedOnDemandDesireObserved(t, ctx, mockKubeApplier, expectedBackupName, expectedFingerprint, "", false)
			},
			syncCount: 3,
			verify: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				spc, err := mockDB.ServiceProviderClusters(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				assert.Empty(t, spc.Status.KeyRotationBackupFingerprint, "fingerprint must not be recorded before the Backup is ever observed")

				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				ad, err := applyDesireCRUD.Get(ctx, expectedDesireName)
				require.NoError(t, err)
				assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, ad.Spec.Type, "current ApplyDesire must not be deleted before its Backup is ever observed")

				readDesireCRUD, err := mockKubeApplier.ReadDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				_, err = readDesireCRUD.Get(ctx, expectedDesireName)
				assert.NoError(t, err, "current ReadDesire must not be deleted before its Backup is ever observed")
			},
		},
		{
			name:             "does not delete current desire while backup is still in progress",
			clusterOpts:      []func(*coreapi.HCPOpenShiftCluster){withKMS},
			hasPlacement:     true,
			seedHCReadDesire: completedRotationHC,
			seedKubeApplier: func(t *testing.T, ctx context.Context, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				seedOnDemandDesireObserved(t, ctx, mockKubeApplier, expectedBackupName, expectedFingerprint, velerov1.BackupPhaseInProgress, true)
			},
			syncCount: 3,
			verify: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				spc, err := mockDB.ServiceProviderClusters(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				assert.Empty(t, spc.Status.KeyRotationBackupFingerprint, "fingerprint must not be recorded while the Backup is still in progress")

				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				ad, err := applyDesireCRUD.Get(ctx, expectedDesireName)
				require.NoError(t, err)
				assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, ad.Spec.Type, "current ApplyDesire must not be deleted while its Backup is still in progress")
			},
		},
		{
			name:             "does not record fingerprint from a superseded rotation's successful desire",
			clusterOpts:      []func(*coreapi.HCPOpenShiftCluster){withKMS},
			hasPlacement:     true,
			seedHCReadDesire: completedRotationHC,
			seedKubeApplier: func(t *testing.T, ctx context.Context, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				seedCompletedOnDemandDesire(t, ctx, mockKubeApplier, staleBackupName, staleFingerprint)
			},
			// syncs 1-2 bootstrap the current RD/AD; sync3 sweeps the stale-but-successful
			// desire without recording its fingerprint (superseded rotation, not this one).
			syncCount: 3,
			verify: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				_, err = applyDesireCRUD.Get(ctx, staleDesireName)
				assert.True(t, cosmosstorageutils.IsNotFoundError(err), "a successful-but-superseded ApplyDesire should still be purged")

				spc, err := mockDB.ServiceProviderClusters(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				assert.Empty(t, spc.Status.KeyRotationBackupFingerprint, "a superseded rotation's successful backup must not be recorded as the current one")
			},
		},
		{
			name:             "purges current ApplyDesire when fingerprint already recorded and ReadDesire is missing",
			clusterOpts:      []func(*coreapi.HCPOpenShiftCluster){withKMS},
			hasPlacement:     true,
			seedHCReadDesire: completedRotationHC,
			seedServiceProviderCluster: func(spc *coreapi.ServiceProviderCluster) {
				spc.Status.KeyRotationBackupFingerprint = expectedFingerprint
			},
			seedKubeApplier: func(t *testing.T, ctx context.Context, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				// Simulates the ReadDesire already having been deleted (e.g. by
				// deleteStaleOnDemandReadDesires on a prior sync) while the ApplyDesire
				// for the current, already-recorded rotation is still present.
				seedOnDemandDesire(t, ctx, mockKubeApplier, expectedBackupName, false)
			},
			verify: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				_, err = applyDesireCRUD.Get(ctx, expectedDesireName)
				assert.True(t, cosmosstorageutils.IsNotFoundError(err), "current ApplyDesire must be purged once its fingerprint is durably recorded, even if the ReadDesire is gone")
			},
		},
		{
			name:             "purges current ApplyDesire when fingerprint already recorded and ReadDesire KubeContent was GC'd",
			clusterOpts:      []func(*coreapi.HCPOpenShiftCluster){withKMS},
			hasPlacement:     true,
			seedHCReadDesire: completedRotationHC,
			seedServiceProviderCluster: func(spc *coreapi.ServiceProviderCluster) {
				spc.Status.KeyRotationBackupFingerprint = expectedFingerprint
			},
			seedKubeApplier: func(t *testing.T, ctx context.Context, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				// Simulates Velero having GC'd the Backup at its TTL and kube-applier
				// observing its absence (nil KubeContent), before the ApplyDesire for
				// the already-recorded rotation was purged.
				seedOnDemandDesireObserved(t, ctx, mockKubeApplier, expectedBackupName, expectedFingerprint, "", false)
			},
			verify: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				_, err = applyDesireCRUD.Get(ctx, expectedDesireName)
				assert.True(t, cosmosstorageutils.IsNotFoundError(err), "current ApplyDesire must be purged once its fingerprint is durably recorded, even if the ReadDesire's KubeContent was cleared by Velero TTL GC")
			},
		},
		{
			name:             "removes read desire once velero purges the backup via ttl",
			clusterOpts:      []func(*coreapi.HCPOpenShiftCluster){withKMS},
			hasPlacement:     true,
			seedHCReadDesire: completedRotationHC,
			seedKubeApplier: func(t *testing.T, ctx context.Context, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				// ReadDesire with nil KubeContent AND a Successful read, no companion
				// ApplyDesire: kube-applier has confirmed the Backup absent (Velero purged it
				// via TTL) and the ApplyDesire was already purged. Nil KubeContent alone is
				// ambiguous ("not observed yet"); the Successful condition disambiguates it.
				rdResourceIDStr := kubeapplierapi.ToClusterScopedReadDesireResourceIDString(
					testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName, staleDesireName,
				)
				rd := &kubeapplierapi.ReadDesire{
					CosmosMetadata: coreapi.CosmosMetadata{
						ResourceID:   metadataapi.Must(azcorearm.ParseResourceID(rdResourceIDStr)),
						PartitionKey: strings.ToLower(testMgmtClusterResourceID().String()),
					},
					Status: kubeapplierapi.ReadDesireStatus{
						Conditions: []metav1.Condition{
							{Type: kubeapplierapi.ConditionTypeSuccessful, Status: metav1.ConditionTrue},
						},
					},
					Tags: map[string]string{backup.DesireTagKeyOndemandBackup: ""},
				}
				readDesireCRUD, err := mockKubeApplier.ReadDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				_, err = readDesireCRUD.Create(ctx, rd, nil)
				require.NoError(t, err)
			},
			// syncs 1-2 bootstrap the current RD/AD; sync3 cleans up the orphaned
			// ReadDesire whose backup Velero already purged via TTL.
			syncCount: 3,
			verify: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				readDesireCRUD, err := mockKubeApplier.ReadDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				_, err = readDesireCRUD.Get(ctx, staleDesireName)
				assert.True(t, cosmosstorageutils.IsNotFoundError(err), "read desire with nil KubeContent should be cleaned up")
			},
		},
		{
			name: "purges on-demand desires when cluster is being deleted",
			clusterOpts: []func(*coreapi.HCPOpenShiftCluster){withKMS, func(c *coreapi.HCPOpenShiftCluster) {
				now := metav1.Now()
				c.ServiceProviderProperties.DeletionTimestamp = &now
			}},
			hasPlacement: true,
			seedKubeApplier: func(t *testing.T, ctx context.Context, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				seedOnDemandDesire(t, ctx, mockKubeApplier, expectedBackupName, true)
			},
			verify: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				_, err = applyDesireCRUD.Get(ctx, expectedDesireName)
				assert.True(t, cosmosstorageutils.IsNotFoundError(err), "on-demand ApplyDesire should be purged (not converted to Delete) when the cluster is being deleted")

				readDesireCRUD, err := mockKubeApplier.ReadDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				_, err = readDesireCRUD.Get(ctx, expectedDesireName)
				assert.True(t, cosmosstorageutils.IsNotFoundError(err), "on-demand ReadDesire should be deleted when the cluster is being deleted")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
			mockKubeApplierDBClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
			mockKubeApplierDBClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
			mockKubeApplierDBClients.Register(testMgmtClusterResourceID(), mockKubeApplierDBClient)

			if tt.seedKubeApplier != nil {
				tt.seedKubeApplier(t, ctx, mockKubeApplierDBClient)
			}

			// Seed the HostedCluster ReadDesire so GetCachedHostedClusterForCluster finds it.
			if tt.seedHCReadDesire != nil {
				readDesireCRUD, err := mockKubeApplierDBClient.ReadDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				_, err = readDesireCRUD.Create(ctx, tt.seedHCReadDesire(t), nil)
				require.NoError(t, err)
			}

			clusterLister := &corelistertesting.SliceClusterLister{
				Clusters: []*coreapi.HCPOpenShiftCluster{newTestCluster(tt.clusterOpts...)},
			}

			// Also seed the ServiceProviderCluster into the cosmos mock (not just the
			// lister) so recordKeyRotationBackupFingerprint's Replace has a real document
			// to match etags against, as it would against real Cosmos.
			var serviceProviderClusterList []*coreapi.ServiceProviderCluster
			if tt.hasPlacement {
				spc := newTestServiceProviderCluster()
				if tt.seedServiceProviderCluster != nil {
					tt.seedServiceProviderCluster(spc)
				}
				created, err := mockDB.ServiceProviderClusters(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName).Create(ctx, spc, nil)
				require.NoError(t, err)
				serviceProviderClusterList = []*coreapi.ServiceProviderCluster{created}
			}

			mcLister := &fleetlistertesting.SliceManagementClusterLister{
				ManagementClusters: []*fleetapi.ManagementCluster{
					{ResourceID: testMgmtClusterResourceID()},
				},
			}

			serviceProviderClusterLister := &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: serviceProviderClusterList}

			syncer := &keyRotationBackupSyncer{
				cosmosClient:                 mockDB,
				clusterLister:                clusterLister,
				serviceProviderClusterLister: serviceProviderClusterLister,
				kubeApplierDBClients:         mockKubeApplierDBClients,
				applyDesireLister:            &kubeapplierlistertesting.DBApplyDesireLister{Clients: mockKubeApplierDBClients, Lister: mcLister},
				readDesireLister:             &kubeapplierlistertesting.DBReadDesireLister{Clients: mockKubeApplierDBClients, Lister: mcLister},
				backupConfig:                 testBackupConfig,
			}

			syncCount := max(tt.syncCount, 1)
			var err error
			for range syncCount {
				err = syncer.SyncOnce(ctx, testKey)
				if err != nil {
					break
				}
				// Refresh the lister's snapshot from the cosmos mock so a status write
				// made this sync (e.g. recordKeyRotationBackupFingerprint) is visible on
				// the next one, mimicking an informer cache catching up in production.
				if tt.hasPlacement {
					refreshed, gerr := mockDB.ServiceProviderClusters(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
					require.NoError(t, gerr)
					serviceProviderClusterLister.ServiceProviderClusters = []*coreapi.ServiceProviderCluster{refreshed}
				}
			}

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tt.verify != nil {
				tt.verify(t, ctx, mockDB, mockKubeApplierDBClient)
			}
		})
	}
}

func TestRecordKeyRotationBackupFingerprint(t *testing.T) {
	testKey := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	newSPC := func() *coreapi.ServiceProviderCluster {
		clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testKey.SubscriptionID +
				"/resourceGroups/" + testKey.ResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testKey.HCPClusterName,
		))
		serviceProviderClusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(fmt.Sprintf("%s/%s/%s",
			clusterResourceID.String(), coreapi.ServiceProviderClusterResourceTypeName, coreapi.ServiceProviderClusterResourceName)))
		return &coreapi.ServiceProviderCluster{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   serviceProviderClusterResourceID,
				PartitionKey: strings.ToLower(testKey.SubscriptionID),
			},
		}
	}

	t.Run("records fingerprint and requeues", func(t *testing.T) {
		ctx := context.Background()
		mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
		created, err := mockDB.ServiceProviderClusters(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName).Create(ctx, newSPC(), nil)
		require.NoError(t, err)

		syncer := &keyRotationBackupSyncer{cosmosClient: mockDB}
		requeue, err := syncer.recordKeyRotationBackupFingerprint(ctx, testKey, created, "fp1")
		require.NoError(t, err)
		assert.True(t, requeue)

		stored, err := mockDB.ServiceProviderClusters(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		assert.Equal(t, "fp1", stored.Status.KeyRotationBackupFingerprint)
	})

	t.Run("precondition failure returns no error and no requeue", func(t *testing.T) {
		ctx := context.Background()
		mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
		created, err := mockDB.ServiceProviderClusters(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName).Create(ctx, newSPC(), nil)
		require.NoError(t, err)

		syncer := &keyRotationBackupSyncer{cosmosClient: mockDB}
		requeue, err := syncer.recordKeyRotationBackupFingerprint(ctx, testKey, created, "fp1")
		require.NoError(t, err)
		require.True(t, requeue)

		// `created` still carries the etag from before the write above, so replaying
		// it (as a stale informer-cached copy would be) must hit a precondition
		// failure, not a hard error.
		requeue, err = syncer.recordKeyRotationBackupFingerprint(ctx, testKey, created, "fp2")
		require.NoError(t, err)
		assert.False(t, requeue)

		stored, err := mockDB.ServiceProviderClusters(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		assert.Equal(t, "fp1", stored.Status.KeyRotationBackupFingerprint, "the stale write must not have overwritten the fresh one")
	})
}
