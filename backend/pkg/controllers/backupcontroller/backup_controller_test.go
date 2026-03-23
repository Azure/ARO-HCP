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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	workv1 "open-cluster-management.io/api/work/v1"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

type alwaysSyncCooldownChecker struct{}

func (a *alwaysSyncCooldownChecker) CanSync(_ context.Context, _ any) bool {
	return true
}

func TestNamingHelpers(t *testing.T) {
	assert.Equal(t, "my-cluster-id-hourly", ScheduleNameForCluster("my-cluster-id"))
	assert.Equal(t, "my-cluster-id-dr", ManifestWorkNameForCluster("my-cluster-id"))
}

func TestNewScheduledBackup(t *testing.T) {
	clusterID := "11111111111111111111111111111111"
	clusterName := "test-domprefix"
	hcNamespace := "ocm-testenv-" + clusterID
	hcpNamespace := hcNamespace + "-" + clusterName

	schedule := NewScheduledBackup(clusterID, clusterName, hcNamespace, hcpNamespace)

	assert.Equal(t, clusterID+"-hourly", schedule.Name)
	assert.Equal(t, veleroNamespace, schedule.Namespace)
	assert.Equal(t, "0 */1 * * *", schedule.Spec.Schedule)
	assert.Equal(t, []string{hcNamespace, hcpNamespace}, schedule.Spec.Template.IncludedNamespaces)
	require.NotNil(t, schedule.Spec.Template.SnapshotMoveData)
	assert.True(t, *schedule.Spec.Template.SnapshotMoveData)

	assert.Equal(t, "default", schedule.Labels["velero.io/storage-location"])
	assert.Equal(t, clusterName, schedule.Labels["hypershift.openshift.io/hosted-cluster"])
	assert.Equal(t, hcNamespace, schedule.Labels["hypershift.openshift.io/hosted-cluster-namespace"])
	assert.Equal(t, clusterID, schedule.Labels["api.openshift.com/id"])
}

func TestBuildScheduleManifestWork(t *testing.T) {
	clusterID := "11111111111111111111111111111111"
	schedule := NewScheduledBackup(clusterID, "test-domprefix", "ocm-testenv-"+clusterID, "ocm-testenv-"+clusterID+"-test-domprefix")
	mw := buildScheduleManifestWork(
		types.NamespacedName{Name: clusterID + "-dr", Namespace: "test-consumer"},
		schedule,
	)

	assert.Equal(t, clusterID+"-dr", mw.Name)
	assert.Equal(t, "test-consumer", mw.Namespace)
	assert.Equal(t, backupScheduleManagedByK8sLabelValue, mw.Labels[backupScheduleManagedByK8sLabelKey])

	require.Len(t, mw.Spec.Workload.Manifests, 1)
	assert.Equal(t, schedule, mw.Spec.Workload.Manifests[0].Object)

	require.Len(t, mw.Spec.ManifestConfigs, 1)
	mc := mw.Spec.ManifestConfigs[0]
	assert.Equal(t, "velero.io", mc.ResourceIdentifier.Group)
	assert.Equal(t, "schedules", mc.ResourceIdentifier.Resource)
	assert.Equal(t, schedule.Name, mc.ResourceIdentifier.Name)
	assert.Equal(t, veleroNamespace, mc.ResourceIdentifier.Namespace)
	assert.Equal(t, workv1.UpdateStrategyTypeServerSideApply, mc.UpdateStrategy.Type)

	require.Len(t, mc.FeedbackRules, 1)
	assert.Equal(t, workv1.JSONPathsType, mc.FeedbackRules[0].Type)
	require.Len(t, mc.FeedbackRules[0].JsonPaths, 1)
	assert.Equal(t, "status", mc.FeedbackRules[0].JsonPaths[0].Name)
	assert.Equal(t, ".status", mc.FeedbackRules[0].JsonPaths[0].Path)
}

func TestBackupActionValidate(t *testing.T) {
	t.Run("no fields set is valid", func(t *testing.T) {
		a := &backupAction{}
		assert.NoError(t, a.validate())
	})

	t.Run("one field set is valid", func(t *testing.T) {
		a := &backupAction{createManifestWork: &workv1.ManifestWork{}}
		assert.NoError(t, a.validate())
	})

	t.Run("two fields set is invalid", func(t *testing.T) {
		a := &backupAction{
			createManifestWork: &workv1.ManifestWork{},
			updateSPC:          &api.ServiceProviderCluster{},
		}
		assert.Error(t, a.validate())
		assert.Contains(t, a.validate().Error(), "programmer error")
	})
}

func TestEnsureManifestWorkCreated(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()
	mockMaestroClient := maestro.NewMockClient(ctrl)
	syncer := &backupScheduleSyncer{}

	desiredMW := &workv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{Name: "test-mw"},
	}

	t.Run("MW not found returns create action", func(t *testing.T) {
		mockMaestroClient.EXPECT().Get(gomock.Any(), "test-mw", gomock.Any()).
			Return(nil, k8serrors.NewNotFound(schema.GroupResource{}, "not-found"))

		state := &backupSyncState{
			maestroClient:       mockMaestroClient,
			manifestWorkName:    "test-mw",
			desiredManifestWork: desiredMW,
		}

		done, action, err := syncer.ensureManifestWorkCreated(ctx, state)
		assert.True(t, done)
		assert.NoError(t, err)
		require.NotNil(t, action)
		assert.Equal(t, desiredMW, action.createManifestWork)
		assert.Nil(t, action.updateSPC)
	})

	t.Run("MW exists passes through", func(t *testing.T) {
		existingMW := &workv1.ManifestWork{
			ObjectMeta: metav1.ObjectMeta{Name: "test-mw"},
		}
		mockMaestroClient.EXPECT().Get(gomock.Any(), "test-mw", gomock.Any()).
			Return(existingMW, nil)

		state := &backupSyncState{
			maestroClient:    mockMaestroClient,
			manifestWorkName: "test-mw",
		}

		done, action, err := syncer.ensureManifestWorkCreated(ctx, state)
		assert.False(t, done)
		assert.NoError(t, err)
		assert.Nil(t, action)
	})

	t.Run("MW Get error returns error", func(t *testing.T) {
		mockMaestroClient.EXPECT().Get(gomock.Any(), "test-mw", gomock.Any()).
			Return(nil, fmt.Errorf("maestro API error"))

		state := &backupSyncState{
			maestroClient:    mockMaestroClient,
			manifestWorkName: "test-mw",
		}

		done, action, err := syncer.ensureManifestWorkCreated(ctx, state)
		assert.True(t, done)
		assert.Error(t, err)
		assert.Nil(t, action)
	})
}

func TestRecordManifestWorkInStatus(t *testing.T) {
	syncer := &backupScheduleSyncer{}

	t.Run("already recorded is no-op", func(t *testing.T) {
		spc := &api.ServiceProviderCluster{}
		spc.Status.BackupScheduleManifestWorkName = "test-mw"

		state := &backupSyncState{
			spc:              spc,
			manifestWorkName: "test-mw",
		}

		done, action, err := syncer.recordManifestWorkInStatus(context.Background(), state)
		assert.True(t, done)
		assert.NoError(t, err)
		assert.Nil(t, action)
	})

	t.Run("not recorded returns update action", func(t *testing.T) {
		spc := &api.ServiceProviderCluster{}

		state := &backupSyncState{
			spc:              spc,
			manifestWorkName: "test-mw",
		}

		done, action, err := syncer.recordManifestWorkInStatus(context.Background(), state)
		assert.True(t, done)
		assert.NoError(t, err)
		require.NotNil(t, action)
		assert.Equal(t, spc, action.updateSPC)
		assert.Equal(t, "test-mw", spc.Status.BackupScheduleManifestWorkName)
	})
}

func TestBackupScheduleSyncer_SyncOnce_ClusterNotFound(t *testing.T) {
	mockDBClient := databasetesting.NewMockDBClient()
	syncer := &backupScheduleSyncer{
		cooldownChecker:                    &alwaysSyncCooldownChecker{},
		cosmosClient:                       mockDBClient,
		maestroSourceEnvironmentIdentifier: "test-env",
	}

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	err := syncer.SyncOnce(context.Background(), key)
	assert.NoError(t, err)
}

func TestBackupScheduleSyncer_SyncOnce_SkipsNonSucceededCluster(t *testing.T) {
	ctx := context.Background()
	mockDBClient := databasetesting.NewMockDBClient()

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{ID: clusterResourceID},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: arm.ProvisioningStateProvisioning,
			ClusterServiceID:  api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}
	_, err := mockDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)

	syncer := &backupScheduleSyncer{
		cooldownChecker:                    &alwaysSyncCooldownChecker{},
		cosmosClient:                       mockDBClient,
		maestroSourceEnvironmentIdentifier: "test-env",
	}

	err = syncer.SyncOnce(ctx, key)
	assert.NoError(t, err)
}

func TestBackupScheduleSyncer_SyncOnce_CreatesManifestWork(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()

	mockDBClient := databasetesting.NewMockDBClient()
	mockClusterService := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockMaestroBuilder := maestro.NewMockMaestroClientBuilder(ctrl)
	mockMaestroClient := maestro.NewMockClient(ctrl)

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{ID: clusterResourceID},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: arm.ProvisioningStateSucceeded,
			ClusterServiceID:  api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}
	_, err := mockDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)

	provisionShard := buildTestProvisionShard(t, "test-shard-id", "test-consumer", "https://maestro-rest", "https://maestro-grpc")
	mockClusterService.EXPECT().GetClusterProvisionShard(gomock.Any(), cluster.ServiceProviderProperties.ClusterServiceID).Return(provisionShard, nil)
	mockMaestroBuilder.EXPECT().NewClient(gomock.Any(), "https://maestro-rest", "https://maestro-grpc", "test-consumer", gomock.Any()).Return(mockMaestroClient, nil)

	csCluster := buildTestCSCluster(t, "test-domprefix")
	mockClusterService.EXPECT().GetCluster(gomock.Any(), cluster.ServiceProviderProperties.ClusterServiceID).Return(csCluster, nil)

	manifestWorkName := "11111111111111111111111111111111-dr"

	// Step 1: MW not found → create action applied
	mockMaestroClient.EXPECT().Get(gomock.Any(), manifestWorkName, gomock.Any()).
		Return(nil, k8serrors.NewNotFound(schema.GroupResource{}, "not-found"))
	createdMW := &workv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{Name: manifestWorkName, Namespace: "test-consumer"},
	}
	mockMaestroClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return(createdMW, nil)

	syncer := &backupScheduleSyncer{
		cooldownChecker:                    &alwaysSyncCooldownChecker{},
		cosmosClient:                       mockDBClient,
		clusterServiceClient:               mockClusterService,
		maestroSourceEnvironmentIdentifier: "test-env",
		maestroClientBuilder:               mockMaestroBuilder,
	}

	err = syncer.SyncOnce(ctx, key)
	assert.NoError(t, err)

	// SPC should not be updated (only 1 action: create MW)
	spc, err := database.GetOrCreateServiceProviderCluster(ctx, mockDBClient, key.GetResourceID())
	require.NoError(t, err)
	assert.Empty(t, spc.Status.BackupScheduleManifestWorkName)
}

func TestBackupScheduleSyncer_SyncOnce_UpdatesSPCWhenMWExists(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()

	mockDBClient := databasetesting.NewMockDBClient()
	mockClusterService := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockMaestroBuilder := maestro.NewMockMaestroClientBuilder(ctrl)
	mockMaestroClient := maestro.NewMockClient(ctrl)

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{ID: clusterResourceID},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: arm.ProvisioningStateSucceeded,
			ClusterServiceID:  api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}
	_, err := mockDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)

	provisionShard := buildTestProvisionShard(t, "test-shard-id", "test-consumer", "https://maestro-rest", "https://maestro-grpc")
	mockClusterService.EXPECT().GetClusterProvisionShard(gomock.Any(), cluster.ServiceProviderProperties.ClusterServiceID).Return(provisionShard, nil)
	mockMaestroBuilder.EXPECT().NewClient(gomock.Any(), "https://maestro-rest", "https://maestro-grpc", "test-consumer", gomock.Any()).Return(mockMaestroClient, nil)

	csCluster := buildTestCSCluster(t, "test-domprefix")
	mockClusterService.EXPECT().GetCluster(gomock.Any(), cluster.ServiceProviderProperties.ClusterServiceID).Return(csCluster, nil)

	manifestWorkName := "11111111111111111111111111111111-dr"

	// Step 1: MW exists → pass through. Step 2: SPC empty → update action applied
	existingMW := &workv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{Name: manifestWorkName, Namespace: "test-consumer"},
	}
	mockMaestroClient.EXPECT().Get(gomock.Any(), manifestWorkName, gomock.Any()).Return(existingMW, nil)

	syncer := &backupScheduleSyncer{
		cooldownChecker:                    &alwaysSyncCooldownChecker{},
		cosmosClient:                       mockDBClient,
		clusterServiceClient:               mockClusterService,
		maestroSourceEnvironmentIdentifier: "test-env",
		maestroClientBuilder:               mockMaestroBuilder,
	}

	err = syncer.SyncOnce(ctx, key)
	assert.NoError(t, err)

	// Verify SPC status was updated
	spc, err := database.GetOrCreateServiceProviderCluster(ctx, mockDBClient, key.GetResourceID())
	require.NoError(t, err)
	assert.Equal(t, manifestWorkName, spc.Status.BackupScheduleManifestWorkName)
}

func TestBackupScheduleSyncer_SyncOnce_NoOpWhenFullyReconciled(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()

	mockDBClient := databasetesting.NewMockDBClient()
	mockClusterService := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockMaestroBuilder := maestro.NewMockMaestroClientBuilder(ctrl)
	mockMaestroClient := maestro.NewMockClient(ctrl)

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{ID: clusterResourceID},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: arm.ProvisioningStateSucceeded,
			ClusterServiceID:  api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}
	_, err := mockDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)

	// Pre-populate SPC with MW name already recorded
	manifestWorkName := "11111111111111111111111111111111-dr"
	spc, err := database.GetOrCreateServiceProviderCluster(ctx, mockDBClient, key.GetResourceID())
	require.NoError(t, err)
	spc.Status.BackupScheduleManifestWorkName = manifestWorkName
	spcCRUD := mockDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	_, err = spcCRUD.Replace(ctx, spc, nil)
	require.NoError(t, err)

	provisionShard := buildTestProvisionShard(t, "test-shard-id", "test-consumer", "https://maestro-rest", "https://maestro-grpc")
	mockClusterService.EXPECT().GetClusterProvisionShard(gomock.Any(), cluster.ServiceProviderProperties.ClusterServiceID).Return(provisionShard, nil)
	mockMaestroBuilder.EXPECT().NewClient(gomock.Any(), "https://maestro-rest", "https://maestro-grpc", "test-consumer", gomock.Any()).Return(mockMaestroClient, nil)

	csCluster := buildTestCSCluster(t, "test-domprefix")
	mockClusterService.EXPECT().GetCluster(gomock.Any(), cluster.ServiceProviderProperties.ClusterServiceID).Return(csCluster, nil)

	// Step 1: MW exists → pass through. Step 2: SPC already set → no-op
	existingMW := &workv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{Name: manifestWorkName, Namespace: "test-consumer"},
	}
	mockMaestroClient.EXPECT().Get(gomock.Any(), manifestWorkName, gomock.Any()).Return(existingMW, nil)
	// No Create or Replace calls expected

	syncer := &backupScheduleSyncer{
		cooldownChecker:                    &alwaysSyncCooldownChecker{},
		cosmosClient:                       mockDBClient,
		clusterServiceClient:               mockClusterService,
		maestroSourceEnvironmentIdentifier: "test-env",
		maestroClientBuilder:               mockMaestroBuilder,
	}

	err = syncer.SyncOnce(ctx, key)
	assert.NoError(t, err)
}

func TestBackupScheduleSyncer_SyncOnce_MaestroCreateError(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()

	mockDBClient := databasetesting.NewMockDBClient()
	mockClusterService := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockMaestroBuilder := maestro.NewMockMaestroClientBuilder(ctrl)
	mockMaestroClient := maestro.NewMockClient(ctrl)

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{ID: clusterResourceID},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: arm.ProvisioningStateSucceeded,
			ClusterServiceID:  api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}
	_, err := mockDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)

	provisionShard := buildTestProvisionShard(t, "test-shard-id", "test-consumer", "https://maestro-rest", "https://maestro-grpc")
	mockClusterService.EXPECT().GetClusterProvisionShard(gomock.Any(), cluster.ServiceProviderProperties.ClusterServiceID).Return(provisionShard, nil)
	mockMaestroBuilder.EXPECT().NewClient(gomock.Any(), "https://maestro-rest", "https://maestro-grpc", "test-consumer", gomock.Any()).Return(mockMaestroClient, nil)

	csCluster := buildTestCSCluster(t, "test-domprefix")
	mockClusterService.EXPECT().GetCluster(gomock.Any(), cluster.ServiceProviderProperties.ClusterServiceID).Return(csCluster, nil)

	manifestWorkName := "11111111111111111111111111111111-dr"
	mockMaestroClient.EXPECT().Get(gomock.Any(), manifestWorkName, gomock.Any()).
		Return(nil, k8serrors.NewNotFound(schema.GroupResource{}, "not-found"))
	mockMaestroClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, fmt.Errorf("maestro API error"))

	syncer := &backupScheduleSyncer{
		cooldownChecker:                    &alwaysSyncCooldownChecker{},
		cosmosClient:                       mockDBClient,
		clusterServiceClient:               mockClusterService,
		maestroSourceEnvironmentIdentifier: "test-env",
		maestroClientBuilder:               mockMaestroBuilder,
	}

	err = syncer.SyncOnce(ctx, key)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create ManifestWork")
}

func TestGetHostedClusterNamespace(t *testing.T) {
	syncer := &backupScheduleSyncer{}
	result := syncer.getHostedClusterNamespace("testenv", "11111111111111111111111111111111")
	assert.Equal(t, "ocm-testenv-11111111111111111111111111111111", result)
}

// buildTestProvisionShard creates a test ProvisionShard using the OCM SDK builder.
func buildTestProvisionShard(t *testing.T, id, consumerName, restURL, grpcURL string) *arohcpv1alpha1.ProvisionShard {
	t.Helper()
	ps, err := arohcpv1alpha1.NewProvisionShard().
		ID(id).
		MaestroConfig(arohcpv1alpha1.NewProvisionShardMaestroConfig().
			ConsumerName(consumerName).
			RestApiConfig(arohcpv1alpha1.NewProvisionShardMaestroRestApiConfig().Url(restURL)).
			GrpcApiConfig(arohcpv1alpha1.NewProvisionShardMaestroGrpcApiConfig().Url(grpcURL)),
		).
		Build()
	require.NoError(t, err)
	return ps
}

// buildTestCSCluster creates a test CS cluster with the given domain prefix.
func buildTestCSCluster(t *testing.T, domainPrefix string) *arohcpv1alpha1.Cluster {
	t.Helper()
	cluster, err := arohcpv1alpha1.NewCluster().
		DomainPrefix(domainPrefix).
		Build()
	require.NoError(t, err)
	return cluster
}
