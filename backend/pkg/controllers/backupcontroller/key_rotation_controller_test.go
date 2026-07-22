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
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	backendlistertesting "github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/backup"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	dblistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

func TestKeyRotationBackupSyncer_SyncOnce(t *testing.T) {
	const (
		testClusterID    = "11111111111111111111111111111111"
		testClusterIDStr = "/api/aro_hcp/v1alpha1/clusters/" + testClusterID
		testEnvID        = "test-env"
		testDomainPrefix = "test-domprefix"
		testStampID      = "mc1"
	)

	testMgmtClusterResourceID := func() *azcorearm.ResourceID {
		return api.Must(fleet.ToManagementClusterResourceID(testStampID))
	}

	testKey := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	newTestCluster := func() *api.HCPOpenShiftCluster {
		resourceID := api.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testKey.SubscriptionID +
				"/resourceGroups/" + testKey.ResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testKey.HCPClusterName,
		))
		csID := api.Must(api.NewInternalID(testClusterIDStr))
		return &api.HCPOpenShiftCluster{
			CosmosMetadata: arm.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(resourceID.SubscriptionID)},
			TrackedResource: arm.TrackedResource{
				Resource: arm.Resource{ID: resourceID},
			},
			CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
				DNS: api.CustomerDNSProfile{
					BaseDomainPrefix: testDomainPrefix,
				},
			},
			ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
				ProvisioningState:       arm.ProvisioningStateSucceeded,
				ClusterServiceID:        &csID,
				BillingDocumentCosmosID: "test-billing-doc-id",
			},
		}
	}

	newTestSPC := func() *api.ServiceProviderCluster {
		clusterResourceID := api.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testKey.SubscriptionID +
				"/resourceGroups/" + testKey.ResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testKey.HCPClusterName,
		))
		spcResourceID := api.Must(azcorearm.ParseResourceID(fmt.Sprintf("%s/%s/%s",
			clusterResourceID.String(), api.ServiceProviderClusterResourceTypeName, api.ServiceProviderClusterResourceName)))
		return &api.ServiceProviderCluster{
			CosmosMetadata: api.CosmosMetadata{
				ResourceID:   spcResourceID,
				PartitionKey: strings.ToLower(testKey.SubscriptionID),
			},
			Status: api.ServiceProviderClusterStatus{
				ManagementClusterResourceID: testMgmtClusterResourceID(),
			},
		}
	}

	newHostedClusterReadDesireLister := func(keyVersion string, opts ...func(*v1beta1.HostedCluster)) *dblistertesting.SliceReadDesireLister {
		hc := &v1beta1.HostedCluster{
			Status: v1beta1.HostedClusterStatus{
				SecretEncryption: v1beta1.SecretEncryptionStatus{
					ActiveKey: v1beta1.SecretEncryptionKeyStatus{
						Azure: v1beta1.AzureKMSKey{KeyVersion: keyVersion},
					},
				},
			},
		}
		for _, opt := range opts {
			opt(hc)
		}
		raw, _ := json.Marshal(hc)
		resourceID := api.Must(azcorearm.ParseResourceID(
			kubeapplier.ToClusterScopedReadDesireResourceIDString(
				testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName,
				maestrohelpers.ReadDesireNameReadonlyHostedCluster)))
		return &dblistertesting.SliceReadDesireLister{
			Desires: []*kubeapplier.ReadDesire{{
				CosmosMetadata: api.CosmosMetadata{
					ResourceID:   resourceID,
					PartitionKey: strings.ToLower(resourceID.SubscriptionID),
				},
				Status: kubeapplier.ReadDesireStatus{
					KubeContent: &runtime.RawExtension{Raw: raw},
				},
			}},
		}
	}

	tests := []struct {
		name             string
		seedDB           func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient)
		readDesireLister dblisters.ReadDesireLister
		hasPlacement     bool
		expectError      bool
		verify           func(t *testing.T, ctx context.Context, syncer *keyRotationBackupSyncer, mockDB *databasetesting.MockResourcesDBClient, mockKA *databasetesting.MockKubeApplierDBClient)
	}{
		{
			name: "cluster without management cluster placement is no-op",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
			},
		},
		{
			name: "no KMS encryption is no-op",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(), nil)
				require.NoError(t, err)
			},
			hasPlacement:     true,
			readDesireLister: newHostedClusterReadDesireLister(""),
			verify: func(t *testing.T, ctx context.Context, _ *keyRotationBackupSyncer, _ *databasetesting.MockResourcesDBClient, mockKA *databasetesting.MockKubeApplierDBClient) {
				t.Helper()
				adCrud, err := mockKA.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				iter, err := adCrud.List(ctx, nil)
				require.NoError(t, err)
				var count int
				for range iter.Items(ctx) {
					count++
				}
				assert.Equal(t, 0, count, "no ApplyDesire should be created when KMS encryption is absent")
			},
		},
		{
			name: "creates backup on completed rotation",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(), nil)
				require.NoError(t, err)
			},
			hasPlacement: true,
			readDesireLister: newHostedClusterReadDesireLister("key-v2", func(hc *v1beta1.HostedCluster) {
				hc.Status.SecretEncryption.History = []v1beta1.EncryptionMigrationHistory{
					{State: v1beta1.EncryptionMigrationStateCompleted},
				}
			}),
			verify: func(t *testing.T, ctx context.Context, _ *keyRotationBackupSyncer, _ *databasetesting.MockResourcesDBClient, mockKA *databasetesting.MockKubeApplierDBClient) {
				t.Helper()
				adCrud, err := mockKA.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				backupName := keyRotationBackupName(testClusterID, "key-v2")
				desireName := keyRotationDesireName(backupName)
				ad, err := adCrud.Get(ctx, desireName)
				require.NoError(t, err, "ApplyDesire for key rotation backup should exist")
				assert.Equal(t, kubeapplier.ApplyDesireTypeServerSideApply, ad.Spec.Type)
				assert.Equal(t, backupName, ad.Spec.TargetItem.Name)
				assert.Equal(t, veleroBackupResource, ad.Spec.TargetItem.Resource)

				rdCrud, err := mockKA.ReadDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				_, err = rdCrud.Get(ctx, desireName)
				assert.NoError(t, err, "ReadDesire for key rotation backup should exist")
			},
		},
		{
			name: "idempotent when backup already exists",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(), nil)
				require.NoError(t, err)
			},
			hasPlacement: true,
			readDesireLister: newHostedClusterReadDesireLister("key-v2", func(hc *v1beta1.HostedCluster) {
				hc.Status.SecretEncryption.History = []v1beta1.EncryptionMigrationHistory{
					{State: v1beta1.EncryptionMigrationStateCompleted},
				}
			}),
			verify: func(t *testing.T, ctx context.Context, syncer *keyRotationBackupSyncer, _ *databasetesting.MockResourcesDBClient, mockKA *databasetesting.MockKubeApplierDBClient) {
				t.Helper()
				adCrud, err := mockKA.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				backupName := keyRotationBackupName(testClusterID, "key-v2")
				desireName := keyRotationDesireName(backupName)
				_, err = adCrud.Get(ctx, desireName)
				require.NoError(t, err, "backup should exist after first sync")

				err = syncer.SyncOnce(ctx, testKey)
				require.NoError(t, err, "second sync should be a no-op")

				iter, err := adCrud.List(ctx, nil)
				require.NoError(t, err)
				var count int
				for range iter.Items(ctx) {
					count++
				}
				assert.Equal(t, 1, count, "should have exactly one ApplyDesire after second sync")
			},
		},
		{
			name: "skips backup during re-encryption (targetKey differs)",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(), nil)
				require.NoError(t, err)
			},
			hasPlacement: true,
			readDesireLister: newHostedClusterReadDesireLister("key-v1", func(hc *v1beta1.HostedCluster) {
				hc.Status.SecretEncryption.TargetKey = v1beta1.SecretEncryptionKeyStatus{
					Azure: v1beta1.AzureKMSKey{KeyVersion: "key-v2"},
				}
			}),
			verify: func(t *testing.T, ctx context.Context, _ *keyRotationBackupSyncer, _ *databasetesting.MockResourcesDBClient, mockKA *databasetesting.MockKubeApplierDBClient) {
				t.Helper()
				adCrud, err := mockKA.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				backupDesireName := keyRotationDesireName(keyRotationBackupName(testClusterID, "key-v1"))
				_, err = adCrud.Get(ctx, backupDesireName)
				assert.True(t, database.IsNotFoundError(err), "no backup should be created during re-encryption")
			},
		},
		{
			name: "skips backup during re-encryption (history state migrating)",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(), nil)
				require.NoError(t, err)
			},
			hasPlacement: true,
			readDesireLister: newHostedClusterReadDesireLister("key-v2", func(hc *v1beta1.HostedCluster) {
				hc.Status.SecretEncryption.History = []v1beta1.EncryptionMigrationHistory{
					{State: v1beta1.EncryptionMigrationStateMigrating},
				}
			}),
			verify: func(t *testing.T, ctx context.Context, _ *keyRotationBackupSyncer, _ *databasetesting.MockResourcesDBClient, mockKA *databasetesting.MockKubeApplierDBClient) {
				t.Helper()
				adCrud, err := mockKA.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				backupDesireName := keyRotationDesireName(keyRotationBackupName(testClusterID, "key-v2"))
				_, err = adCrud.Get(ctx, backupDesireName)
				assert.True(t, database.IsNotFoundError(err), "no backup should be created while migrating")
			},
		},
		{
			name: "creates missing RD when AD already exists (crash recovery)",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(), nil)
				require.NoError(t, err)
			},
			hasPlacement: true,
			readDesireLister: newHostedClusterReadDesireLister("key-v2", func(hc *v1beta1.HostedCluster) {
				hc.Status.SecretEncryption.History = []v1beta1.EncryptionMigrationHistory{
					{State: v1beta1.EncryptionMigrationStateCompleted},
				}
			}),
			verify: func(t *testing.T, ctx context.Context, syncer *keyRotationBackupSyncer, _ *databasetesting.MockResourcesDBClient, mockKA *databasetesting.MockKubeApplierDBClient) {
				t.Helper()
				adCrud, err := mockKA.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				rdCrud, err := mockKA.ReadDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)

				backupName := keyRotationBackupName(testClusterID, "key-v2")
				desireName := keyRotationDesireName(backupName)

				// Verify first sync created both AD and RD
				_, err = adCrud.Get(ctx, desireName)
				require.NoError(t, err, "AD should exist after first sync")
				_, err = rdCrud.Get(ctx, desireName)
				require.NoError(t, err, "RD should exist after first sync")

				// Simulate crash: delete only the RD
				err = rdCrud.Delete(ctx, desireName)
				require.NoError(t, err)
				_, err = rdCrud.Get(ctx, desireName)
				require.True(t, database.IsNotFoundError(err), "RD should be deleted")

				// Second sync should recreate the missing RD
				err = syncer.SyncOnce(ctx, testKey)
				require.NoError(t, err, "second sync should recreate missing RD")

				_, err = adCrud.Get(ctx, desireName)
				assert.NoError(t, err, "AD should still exist after crash recovery")
				_, err = rdCrud.Get(ctx, desireName)
				assert.NoError(t, err, "RD should be recreated after crash recovery")
			},
		},
		{
			name: "creates missing AD when RD already exists (crash recovery)",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(), nil)
				require.NoError(t, err)
			},
			hasPlacement: true,
			readDesireLister: newHostedClusterReadDesireLister("key-v2", func(hc *v1beta1.HostedCluster) {
				hc.Status.SecretEncryption.History = []v1beta1.EncryptionMigrationHistory{
					{State: v1beta1.EncryptionMigrationStateCompleted},
				}
			}),
			verify: func(t *testing.T, ctx context.Context, syncer *keyRotationBackupSyncer, _ *databasetesting.MockResourcesDBClient, mockKA *databasetesting.MockKubeApplierDBClient) {
				t.Helper()
				adCrud, err := mockKA.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				rdCrud, err := mockKA.ReadDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)

				backupName := keyRotationBackupName(testClusterID, "key-v2")
				desireName := keyRotationDesireName(backupName)

				// Verify first sync created both AD and RD
				_, err = adCrud.Get(ctx, desireName)
				require.NoError(t, err, "AD should exist after first sync")
				_, err = rdCrud.Get(ctx, desireName)
				require.NoError(t, err, "RD should exist after first sync")

				// Simulate crash: delete only the AD
				err = adCrud.Delete(ctx, desireName)
				require.NoError(t, err)
				_, err = adCrud.Get(ctx, desireName)
				require.True(t, database.IsNotFoundError(err), "AD should be deleted")

				// Second sync should recreate the missing AD
				err = syncer.SyncOnce(ctx, testKey)
				require.NoError(t, err, "second sync should recreate missing AD")

				_, err = adCrud.Get(ctx, desireName)
				assert.NoError(t, err, "AD should be recreated after crash recovery")
				_, err = rdCrud.Get(ctx, desireName)
				assert.NoError(t, err, "RD should still exist after crash recovery")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			mockDB := databasetesting.NewMockResourcesDBClient()
			mockKA := databasetesting.NewMockKubeApplierDBClient()
			mockKAClients := databasetesting.NewMockKubeApplierDBClients()
			mockKAClients.Register(testMgmtClusterResourceID(), mockKA)

			if tt.seedDB != nil {
				tt.seedDB(t, ctx, mockDB)
			}

			var spcList []*api.ServiceProviderCluster
			if tt.hasPlacement {
				spcList = []*api.ServiceProviderCluster{newTestSPC()}
			}

			var rdLister dblisters.ReadDesireLister = &dblistertesting.SliceReadDesireLister{}
			if tt.readDesireLister != nil {
				rdLister = tt.readDesireLister
			}

			syncer := &keyRotationBackupSyncer{
				cosmosClient:                        mockDB,
				clusterLister:                       &backendlistertesting.SliceClusterLister{Clusters: []*api.HCPOpenShiftCluster{newTestCluster()}},
				serviceProviderClusterLister:        &backendlistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: spcList},
				readDesireLister:                    rdLister,
				kubeApplierDBClients:                mockKAClients,
				hostedClusterNamespaceEnvIdentifier: testEnvID,
				backupConfig:                        &BackupConfig{Cadence: BackupCadenceProduction},
			}

			err := syncer.SyncOnce(ctx, testKey)
			if tt.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tt.verify != nil {
				tt.verify(t, ctx, syncer, mockDB, mockKA)
			}
		})
	}
}

func TestRotationComplete(t *testing.T) {
	tests := []struct {
		name     string
		hc       *v1beta1.HostedCluster
		expected bool
	}{
		{
			name: "no encryption status means no rotation happened",
			hc: &v1beta1.HostedCluster{
				Status: v1beta1.HostedClusterStatus{},
			},
			expected: false,
		},
		{
			name: "target key differs from active key is not complete",
			hc: &v1beta1.HostedCluster{
				Status: v1beta1.HostedClusterStatus{
					SecretEncryption: v1beta1.SecretEncryptionStatus{
						ActiveKey: v1beta1.SecretEncryptionKeyStatus{
							Azure: v1beta1.AzureKMSKey{KeyVersion: "v1"},
						},
						TargetKey: v1beta1.SecretEncryptionKeyStatus{
							Azure: v1beta1.AzureKMSKey{KeyVersion: "v2"},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "history state migrating is not complete",
			hc: &v1beta1.HostedCluster{
				Status: v1beta1.HostedClusterStatus{
					SecretEncryption: v1beta1.SecretEncryptionStatus{
						ActiveKey: v1beta1.SecretEncryptionKeyStatus{
							Azure: v1beta1.AzureKMSKey{KeyVersion: "v2"},
						},
						History: []v1beta1.EncryptionMigrationHistory{
							{State: v1beta1.EncryptionMigrationStateMigrating},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "history state completed is complete",
			hc: &v1beta1.HostedCluster{
				Status: v1beta1.HostedClusterStatus{
					SecretEncryption: v1beta1.SecretEncryptionStatus{
						ActiveKey: v1beta1.SecretEncryptionKeyStatus{
							Azure: v1beta1.AzureKMSKey{KeyVersion: "v2"},
						},
						History: []v1beta1.EncryptionMigrationHistory{
							{State: v1beta1.EncryptionMigrationStateCompleted},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "active matches target with completed history is complete",
			hc: &v1beta1.HostedCluster{
				Status: v1beta1.HostedClusterStatus{
					SecretEncryption: v1beta1.SecretEncryptionStatus{
						ActiveKey: v1beta1.SecretEncryptionKeyStatus{
							Azure: v1beta1.AzureKMSKey{KeyVersion: "v2"},
						},
						TargetKey: v1beta1.SecretEncryptionKeyStatus{
							Azure: v1beta1.AzureKMSKey{KeyVersion: "v2"},
						},
						History: []v1beta1.EncryptionMigrationHistory{
							{State: v1beta1.EncryptionMigrationStateCompleted},
						},
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, rotationComplete(tt.hc))
		})
	}
}

func TestKeyRotationBackupName(t *testing.T) {
	assert.Equal(t, "cluster123-keyrotation-v2", keyRotationBackupName("cluster123", "v2"))
}

func TestKeyRotationDesireName(t *testing.T) {
	name := keyRotationDesireName("cluster123-keyrotation-v2")
	assert.True(t, strings.HasPrefix(name, backup.OndemandBackupDesireNamePrefix), "desire name should start with on-demand prefix")
	assert.Equal(t, backup.OndemandBackupDesireNamePrefix+"cluster123-keyrotation-v2", name, "desire name should be on-demand prefix + backup name")
}
