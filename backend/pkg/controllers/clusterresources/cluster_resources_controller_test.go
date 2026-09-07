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

package clusterresources

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	fleetlistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
	kubeapplierlistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testSubscriptionID    = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName = "test-rg"
	testClusterName       = "test-cluster"
	testClusterServiceID  = "/api/clusters_mgmt/v1/clusters/abc123"
)

var testManagementClusterResourceID = metadataapi.Must(azcorearm.ParseResourceID(
	"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default",
))

func testKey() controllerutils.HCPClusterKey {
	return controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}
}

func newCluster(opts ...func(*coreapi.HCPOpenShiftCluster)) *coreapi.HCPOpenShiftCluster {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))
	cluster := &coreapi.HCPOpenShiftCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: testClusterName,
				Type: resourceID.ResourceType.String(),
			},
		},
		ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: metadataapi.Ptr(metadataapi.Must(metadataapi.NewInternalID(testClusterServiceID))),
		},
	}
	for _, opt := range opts {
		opt(cluster)
	}
	return cluster
}

func newSPC(mcResourceID *azcorearm.ResourceID) *coreapi.ServiceProviderCluster {
	spcResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/serviceProviderClusters/" + coreapi.ServiceProviderClusterResourceName,
	))
	return &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   spcResourceID,
			PartitionKey: strings.ToLower(spcResourceID.SubscriptionID),
		},
		Status: coreapi.ServiceProviderClusterStatus{
			ManagementClusterResourceID: mcResourceID,
		},
	}
}

func buildClusterResources(resources map[string]string) *arohcpv1alpha1.ClusterResources {
	cr, err := arohcpv1alpha1.NewClusterResources().
		Resources(resources).
		Build()
	if err != nil {
		panic(fmt.Sprintf("failed to build ClusterResources: %v", err))
	}
	return cr
}

func TestNeedsWork(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		cluster           *coreapi.HCPOpenShiftCluster
		managementCluster *azcorearm.ResourceID
		want              bool
	}{
		{
			name:              "returns true for cluster with ClusterServiceID and management cluster",
			cluster:           newCluster(),
			managementCluster: testManagementClusterResourceID,
			want:              true,
		},
		{
			name: "returns true for cluster being deleted with management cluster",
			cluster: newCluster(func(c *coreapi.HCPOpenShiftCluster) {
				now := metav1.Now()
				c.ServiceProviderProperties.DeletionTimestamp = &now
			}),
			managementCluster: testManagementClusterResourceID,
			want:              true,
		},
		{
			name: "returns false for cluster without ClusterServiceID",
			cluster: newCluster(func(c *coreapi.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.ClusterServiceID = nil
			}),
			managementCluster: testManagementClusterResourceID,
			want:              false,
		},
		{
			name:              "returns false when management cluster is nil",
			cluster:           newCluster(),
			managementCluster: nil,
			want:              false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := &clusterResourcesController{}
			got := c.NeedsWork(tt.cluster, tt.managementCluster)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSyncOnce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		cluster     *coreapi.HCPOpenShiftCluster
		dbResources []any
		setupCSMock func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec
		wantErr     bool
		verifyDB    func(t *testing.T, ctx context.Context, kaClient *kubeappliercosmosstoragetesting.MockKubeApplierDBClient)
	}{
		{
			name:    "skips cluster not found in cache",
			cluster: nil,
			setupCSMock: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				return ocm.NewMockClusterServiceClientSpec(ctrl)
			},
			wantErr: false,
		},
		{
			name: "skips cluster without ClusterServiceID",
			cluster: newCluster(func(c *coreapi.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.ClusterServiceID = nil
			}),
			dbResources: []any{
				newSPC(testManagementClusterResourceID),
			},
			setupCSMock: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				return ocm.NewMockClusterServiceClientSpec(ctrl)
			},
			wantErr: false,
		},
		{
			name: "skips cluster being deleted",
			cluster: newCluster(func(c *coreapi.HCPOpenShiftCluster) {
				now := metav1.Now()
				c.ServiceProviderProperties.DeletionTimestamp = &now
			}),
			dbResources: []any{
				newSPC(testManagementClusterResourceID),
			},
			setupCSMock: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				return ocm.NewMockClusterServiceClientSpec(ctrl)
			},
			wantErr: false,
		},
		{
			name:    "returns error when GetClusterResources fails",
			cluster: newCluster(),
			dbResources: []any{
				newSPC(testManagementClusterResourceID),
			},
			setupCSMock: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().
					GetClusterResources(gomock.Any(), gomock.Any()).
					Return(nil, fmt.Errorf("connection refused"))
				return mock
			},
			wantErr: true,
		},
		{
			name:    "returns error on timestamp parsing errors from OCM SDK",
			cluster: newCluster(),
			dbResources: []any{
				newSPC(testManagementClusterResourceID),
			},
			setupCSMock: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().
					GetClusterResources(gomock.Any(), gomock.Any()).
					Return(nil, fmt.Errorf("parsing time \"invalid\" as creation_timestamp failed"))
				return mock
			},
			wantErr: true,
		},
		{
			name:    "no-op when GetClusterResources returns nil",
			cluster: newCluster(),
			dbResources: []any{
				newSPC(testManagementClusterResourceID),
			},
			setupCSMock: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().
					GetClusterResources(gomock.Any(), gomock.Any()).
					Return(nil, nil)
				return mock
			},
			wantErr: false,
		},
		{
			name:    "creates ApplyDesire for a single resource",
			cluster: newCluster(),
			dbResources: []any{
				newSPC(testManagementClusterResourceID),
			},
			setupCSMock: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				configMap := `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"default-ingress","namespace":"ocm-env-abc"},"data":{"key":"value"}}`
				mock.EXPECT().
					GetClusterResources(gomock.Any(), gomock.Any()).
					Return(buildClusterResources(map[string]string{
						"default-ingress-configmap": configMap,
					}), nil)
				return mock
			},
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, kaClient *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				crud, err := kaClient.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
				require.NoError(t, err, "failed to get ApplyDesires CRUD")

				desire, err := crud.Get(ctx, "defaultingressconfigmap")
				require.NoError(t, err, "ApplyDesire for DefaultIngressConfigMap should exist")

				assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, desire.Spec.Type, "desire type should be ServerSideApply")
				assert.Equal(t, testManagementClusterResourceID.String(), desire.Spec.ManagementCluster.String(), "management cluster should match")
				assert.Equal(t, "default-ingress", desire.Spec.TargetItem.Name, "target name should match")
				assert.Equal(t, "ocm-env-abc", desire.Spec.TargetItem.Namespace, "target namespace should match")
				assert.Equal(t, "configmaps", desire.Spec.TargetItem.Resource, "target resource should be the plural resource name")
				assert.NotNil(t, desire.Spec.ServerSideApply, "ServerSideApply config should be set")
				assert.NotNil(t, desire.Spec.ServerSideApply.KubeContent, "KubeContent should be set")
			},
		},
		{
			name:    "creates ApplyDesires for multiple resources",
			cluster: newCluster(),
			dbResources: []any{
				newSPC(testManagementClusterResourceID),
			},
			setupCSMock: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().
					GetClusterResources(gomock.Any(), gomock.Any()).
					Return(buildClusterResources(map[string]string{
						"pull-secret":       `{"apiVersion":"v1","kind":"Secret","metadata":{"name":"pull-secret","namespace":"ocm-env-abc"}}`,
						"ingress-configmap": `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"default-ingress","namespace":"ocm-env-abc"}}`,
					}), nil)
				return mock
			},
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, kaClient *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				crud, err := kaClient.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
				require.NoError(t, err, "failed to get ApplyDesires CRUD")

				desireA, err := crud.Get(ctx, "ocppullsecret")
				require.NoError(t, err, "ApplyDesire for OCPPullSecret should exist")
				assert.Equal(t, "pull-secret", desireA.Spec.TargetItem.Name, "secret target name should match")
				assert.Equal(t, "secrets", desireA.Spec.TargetItem.Resource, "secret target resource should match")

				desireB, err := crud.Get(ctx, "defaultingressconfigmap")
				require.NoError(t, err, "ApplyDesire for DefaultIngressConfigMap should exist")
				assert.Equal(t, "default-ingress", desireB.Spec.TargetItem.Name, "configmap target name should match")
				assert.Equal(t, "configmaps", desireB.Spec.TargetItem.Resource, "configmap target resource should match")
			},
		},
		{
			name:    "skips when management cluster is not placed",
			cluster: newCluster(),
			dbResources: []any{
				newSPC(nil),
			},
			setupCSMock: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				return ocm.NewMockClusterServiceClientSpec(ctrl)
			},
			wantErr: false,
		},
		{
			name:    "replaces existing ApplyDesire when content changes",
			cluster: newCluster(),
			dbResources: []any{
				newSPC(testManagementClusterResourceID),
			},
			setupCSMock: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().
					GetClusterResources(gomock.Any(), gomock.Any()).
					Return(buildClusterResources(map[string]string{
						"ingress-configmap": `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"default-ingress","namespace":"ocm-env-abc"},"data":{"key":"updated-value"}}`,
					}), nil)
				return mock
			},
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, kaClient *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				crud, err := kaClient.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
				require.NoError(t, err, "failed to get ApplyDesires CRUD")

				desire, err := crud.Get(ctx, "defaultingressconfigmap")
				require.NoError(t, err, "ApplyDesire should exist after replacement")

				var content map[string]interface{}
				err = json.Unmarshal(desire.Spec.ServerSideApply.KubeContent.Raw, &content)
				require.NoError(t, err, "should unmarshal KubeContent")
				data, ok := content["data"].(map[string]interface{})
				require.True(t, ok, "data field should be a map")
				assert.Equal(t, "updated-value", data["key"], "KubeContent should reflect the updated value")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			ctrl := gomock.NewController(t)

			mockKubeApplierDBClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
			mockKubeApplierClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
			mockKubeApplierDBClients.Register(testManagementClusterResourceID, mockKubeApplierClient)

			// Seed the cluster lister (used by SyncOnce to look up the cluster)
			clusterLister := &corelistertesting.SliceClusterLister{}
			if tt.cluster != nil {
				clusterLister.Clusters = []*coreapi.HCPOpenShiftCluster{tt.cluster}
			}

			// Seed the SPC lister from dbResources
			spcLister := &corelistertesting.SliceServiceProviderClusterLister{}
			for _, r := range tt.dbResources {
				if spc, ok := r.(*coreapi.ServiceProviderCluster); ok {
					spcLister.ServiceProviderClusters = append(spcLister.ServiceProviderClusters, spc)
				}
			}

			mcLister := &fleetlistertesting.SliceManagementClusterLister{
				ManagementClusters: []*fleetapi.ManagementCluster{
					{ResourceID: testManagementClusterResourceID},
				},
			}

			syncer := &clusterResourcesController{
				clusterLister:                clusterLister,
				serviceProviderClusterLister: spcLister,
				clustersServiceClient:        tt.setupCSMock(ctrl),
				kubeApplierDBClients:         mockKubeApplierDBClients,
				applyDesireLister:            &kubeapplierlistertesting.DBApplyDesireLister{Clients: mockKubeApplierDBClients, Lister: mcLister},
			}

			err := syncer.SyncOnce(ctx, testKey())
			if tt.wantErr {
				require.Error(t, err, "expected SyncOnce to return an error")
				return
			}
			require.NoError(t, err, "expected SyncOnce to succeed")
			if tt.verifyDB != nil {
				tt.verifyDB(t, ctx, mockKubeApplierClient)
			}
		})
	}
}

func TestDeleteStaleApplyDesires(t *testing.T) {
	t.Parallel()

	makeTaggedDesire := func(name string) *kubeapplierapi.ApplyDesire {
		resourceIDStr := kubeapplierapi.ToClusterScopedApplyDesireResourceIDString(
			testSubscriptionID, testResourceGroupName, testClusterName, name,
		)
		return &kubeapplierapi.ApplyDesire{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   metadataapi.Must(azcorearm.ParseResourceID(resourceIDStr)),
				PartitionKey: strings.ToLower(testManagementClusterResourceID.String()),
			},
			Tags: map[string]string{kubeapplierapi.TagControllerName: ClusterResourcesControllerName},
			Spec: kubeapplierapi.ApplyDesireSpec{
				ManagementCluster: testManagementClusterResourceID,
				Type:              kubeapplierapi.ApplyDesireTypeServerSideApply,
				TargetItem: kubeapplierapi.ResourceReference{
					Group: "", Version: "v1", Resource: "configmaps",
					Name: name, Namespace: "ns",
				},
				ServerSideApply: &kubeapplierapi.ServerSideApplyConfig{
					KubeContent: &runtime.RawExtension{Raw: []byte(`{}`)},
				},
			},
		}
	}

	makeUntaggedDesire := func(name string) *kubeapplierapi.ApplyDesire {
		d := makeTaggedDesire(name)
		d.Tags = nil
		return d
	}

	t.Run("marks stale tagged desire for deletion", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

		mockKubeApplierClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		mockClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
		mockClients.Register(testManagementClusterResourceID, mockKubeApplierClient)

		crud, _ := mockKubeApplierClient.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
		_, _ = crud.Create(ctx, makeTaggedDesire("configmaps.ns.current"), nil)
		_, _ = crud.Create(ctx, makeTaggedDesire("configmaps.ns.stale"), nil)

		mcLister := &fleetlistertesting.SliceManagementClusterLister{
			ManagementClusters: []*fleetapi.ManagementCluster{{ResourceID: testManagementClusterResourceID}},
		}
		syncer := &clusterResourcesController{
			kubeApplierDBClients: mockClients,
			applyDesireLister:    &kubeapplierlistertesting.DBApplyDesireLister{Clients: mockClients, Lister: mcLister},
		}

		currentResourceID := kubeapplierapi.ToClusterScopedApplyDesireResourceIDString(
			testSubscriptionID, testResourceGroupName, testClusterName, "configmaps.ns.current",
		)
		err := syncer.deleteStaleApplyDesires(ctx, testKey(), testManagementClusterResourceID,
			map[string]bool{currentResourceID: true})
		require.NoError(t, err, "deleteStaleApplyDesires should succeed")

		stale, err := crud.Get(ctx, "configmaps.ns.stale")
		require.NoError(t, err, "stale desire should still exist")
		assert.Equal(t, kubeapplierapi.ApplyDesireTypeDelete, stale.Spec.Type, "stale desire should be marked for deletion")
		assert.Nil(t, stale.Spec.ServerSideApply, "ServerSideApply should be cleared")

		current, err := crud.Get(ctx, "configmaps.ns.current")
		require.NoError(t, err, "current desire should still exist")
		assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, current.Spec.Type, "current desire should be unchanged")
	})

	t.Run("does not touch untagged desires", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

		mockKubeApplierClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		mockClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
		mockClients.Register(testManagementClusterResourceID, mockKubeApplierClient)

		crud, _ := mockKubeApplierClient.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
		_, _ = crud.Create(ctx, makeUntaggedDesire("configmaps.ns.other-controller"), nil)

		mcLister := &fleetlistertesting.SliceManagementClusterLister{
			ManagementClusters: []*fleetapi.ManagementCluster{{ResourceID: testManagementClusterResourceID}},
		}
		syncer := &clusterResourcesController{
			kubeApplierDBClients: mockClients,
			applyDesireLister:    &kubeapplierlistertesting.DBApplyDesireLister{Clients: mockClients, Lister: mcLister},
		}

		err := syncer.deleteStaleApplyDesires(ctx, testKey(), testManagementClusterResourceID, map[string]bool{})
		require.NoError(t, err, "deleteStaleApplyDesires should succeed")

		desire, err := crud.Get(ctx, "configmaps.ns.other-controller")
		require.NoError(t, err, "untagged desire should still exist")
		assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, desire.Spec.Type, "untagged desire should be untouched")
	})

	t.Run("waits for Delete-type desire without successful condition", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

		mockKubeApplierClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		mockClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
		mockClients.Register(testManagementClusterResourceID, mockKubeApplierClient)

		crud, _ := mockKubeApplierClient.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)

		staleDesire := makeTaggedDesire("nodepools.ns.pending")
		staleDesire.Spec.Type = kubeapplierapi.ApplyDesireTypeDelete
		staleDesire.Spec.ServerSideApply = nil
		_, _ = crud.Create(ctx, staleDesire, nil)

		mcLister := &fleetlistertesting.SliceManagementClusterLister{
			ManagementClusters: []*fleetapi.ManagementCluster{{ResourceID: testManagementClusterResourceID}},
		}
		syncer := &clusterResourcesController{
			kubeApplierDBClients: mockClients,
			applyDesireLister:    &kubeapplierlistertesting.DBApplyDesireLister{Clients: mockClients, Lister: mcLister},
		}

		err := syncer.deleteStaleApplyDesires(ctx, testKey(), testManagementClusterResourceID, map[string]bool{})
		require.NoError(t, err, "deleteStaleApplyDesires should succeed")

		_, err = crud.Get(ctx, "nodepools.ns.pending")
		require.NoError(t, err, "desire should still exist while delete is pending")
	})

	t.Run("purges Delete-type desire when SuccessfullyDeleted condition is true", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

		mockKubeApplierClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		mockClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
		mockClients.Register(testManagementClusterResourceID, mockKubeApplierClient)

		crud, _ := mockKubeApplierClient.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)

		staleDesire := makeTaggedDesire("configmaps.ns.gone")
		staleDesire.Spec.Type = kubeapplierapi.ApplyDesireTypeDelete
		staleDesire.Spec.ServerSideApply = nil
		staleDesire.Status.Conditions = []metav1.Condition{
			{Type: kubeapplierapi.ConditionTypeSuccessfullyDeleted, Status: metav1.ConditionTrue},
		}
		_, _ = crud.Create(ctx, staleDesire, nil)

		mcLister := &fleetlistertesting.SliceManagementClusterLister{
			ManagementClusters: []*fleetapi.ManagementCluster{{ResourceID: testManagementClusterResourceID}},
		}
		syncer := &clusterResourcesController{
			kubeApplierDBClients: mockClients,
			applyDesireLister:    &kubeapplierlistertesting.DBApplyDesireLister{Clients: mockClients, Lister: mcLister},
		}

		err := syncer.deleteStaleApplyDesires(ctx, testKey(), testManagementClusterResourceID, map[string]bool{})
		require.NoError(t, err, "deleteStaleApplyDesires should succeed")

		_, err = crud.Get(ctx, "configmaps.ns.gone")
		assert.Error(t, err, "purged ApplyDesire should no longer exist")
	})
}

func TestClassifyClusterResource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		resource     string
		wantDesire   string
		wantNodePool string
		wantErr      bool
	}{
		{
			name:       "HostedCluster",
			resource:   `{"apiVersion":"hypershift.openshift.io/v1beta1","kind":"HostedCluster","metadata":{"name":"my-hc","namespace":"ocm-env-abc"}}`,
			wantDesire: "HostedCluster",
		},
		{
			name:         "NodePool with spec.clusterName prefix",
			resource:     `{"apiVersion":"hypershift.openshift.io/v1beta1","kind":"NodePool","metadata":{"name":"q2e1p3b8m8a3s6l-np-2dz967","namespace":"ocm-env-abc"},"spec":{"clusterName":"q2e1p3b8m8a3s6l"}}`,
			wantDesire:   "NodePool",
			wantNodePool: "np-2dz967",
		},
		{
			name:         "NodePool without spec.clusterName",
			resource:     `{"apiVersion":"hypershift.openshift.io/v1beta1","kind":"NodePool","metadata":{"name":"worker","namespace":"ocm-env-abc"}}`,
			wantDesire:   "NodePool",
			wantNodePool: "worker",
		},
		{
			name:       "HostedCluster Namespace",
			resource:   `{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"ocm-arohcppers-2sdm6b8jke9sm3h8ukc8mbaahngnre5c","labels":{"api.openshift.com/environment":"arohcppers","api.openshift.com/id":"2sdm6b8jke9sm3h8ukc8mbaahngnre5c","api.openshift.com/name":"cluster-hs-pre-rxhvsd"}}}`,
			wantDesire: "HostedClusterNamespace",
		},
		{
			name:       "ControlPlane Namespace",
			resource:   `{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"ocm-arohcppers-2sdm6b8jke9sm3h8ukc8mbaahngnre5c-j7h3t4w0u1t3b4b","labels":{"app.kubernetes.io/managed-by":"aro-hcp-clusters-service","hypershift.openshift.io/cluster":"2sdm6b8jke9sm3h8ukc8mbaahngnre5c"}}}`,
			wantDesire: "ControlPlaneNamespace",
		},
		{
			name:       "ConfigMap → DefaultIngressConfigMap",
			resource:   `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"default-ingress","namespace":"ocm-env-abc"}}`,
			wantDesire: "DefaultIngressConfigMap",
		},
		{
			name:       "Secret → OCPPullSecret",
			resource:   `{"apiVersion":"v1","kind":"Secret","metadata":{"name":"pull-secret","namespace":"ocm-env-abc"}}`,
			wantDesire: "OCPPullSecret",
		},
		{
			name:       "PodNetwork",
			resource:   `{"apiVersion":"multitenancy.acn.azure.com/v1alpha1","kind":"PodNetwork","metadata":{"name":"pn-abc123"}}`,
			wantDesire: "PodNetwork",
		},
		{
			name:       "PodNetworkInstance",
			resource:   `{"apiVersion":"multitenancy.acn.azure.com/v1alpha1","kind":"PodNetworkInstance","metadata":{"name":"pni-abc123","namespace":"ocm-env-abc-cp"}}`,
			wantDesire: "PodNetworkInstance",
		},
		{
			name:       "SecretSync bound-sa-signing-key",
			resource:   `{"apiVersion":"secret-sync.x-k8s.io/v1alpha1","kind":"SecretSync","metadata":{"name":"bound-sa-signing-key","namespace":"ocm-env-abc"}}`,
			wantDesire: "BoundServiceAccountSigningKeySecretSync",
		},
		{
			name:       "SecretSync bound-service-account-signing-key",
			resource:   `{"apiVersion":"secret-sync.x-k8s.io/v1alpha1","kind":"SecretSync","metadata":{"name":"bound-service-account-signing-key","namespace":"ocm-env-abc"}}`,
			wantDesire: "BoundServiceAccountSigningKeySecretSync",
		},
		{
			name:       "SecretSync default-ingress",
			resource:   `{"apiVersion":"secret-sync.x-k8s.io/v1alpha1","kind":"SecretSync","metadata":{"name":"default-ingress-cert","namespace":"ocm-env-abc"}}`,
			wantDesire: "DefaultIngressWildcardCertSecretSync",
		},
		{
			name:       "SecretSync kube-apiserver",
			resource:   `{"apiVersion":"secret-sync.x-k8s.io/v1alpha1","kind":"SecretSync","metadata":{"name":"kube-apiserver-server-cert","namespace":"ocm-env-abc"}}`,
			wantDesire: "KubeAPIServerServingCertSecretSync",
		},
		{
			name:       "SecretProviderClass bound-sa-signing-key",
			resource:   `{"apiVersion":"secrets-store.csi.x-k8s.io/v1","kind":"SecretProviderClass","metadata":{"name":"bound-sa-signing-key","namespace":"ocm-env-abc"}}`,
			wantDesire: "BoundServiceAccountSigningKeySecretProviderClass",
		},
		{
			name:       "SecretProviderClass bound-service-account-signing-key",
			resource:   `{"apiVersion":"secrets-store.csi.x-k8s.io/v1","kind":"SecretProviderClass","metadata":{"name":"bound-service-account-signing-key","namespace":"ocm-env-abc"}}`,
			wantDesire: "BoundServiceAccountSigningKeySecretProviderClass",
		},
		{
			name:       "SecretProviderClass default-ingress",
			resource:   `{"apiVersion":"secrets-store.csi.x-k8s.io/v1","kind":"SecretProviderClass","metadata":{"name":"default-ingress-cert","namespace":"ocm-env-abc"}}`,
			wantDesire: "DefaultIngressWildcardCertSecretProviderClass",
		},
		{
			name:       "SecretProviderClass kube-apiserver",
			resource:   `{"apiVersion":"secrets-store.csi.x-k8s.io/v1","kind":"SecretProviderClass","metadata":{"name":"kube-apiserver-server-cert","namespace":"ocm-env-abc"}}`,
			wantDesire: "KubeAPIServerServingCertSecretProviderClass",
		},
		{
			name:     "unrecognized kind errors",
			resource: `{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"web","namespace":"app"}}`,
			wantErr:  true,
		},
		{
			name:     "unrecognized SecretSync name errors",
			resource: `{"apiVersion":"secret-sync.x-k8s.io/v1alpha1","kind":"SecretSync","metadata":{"name":"unknown-sync","namespace":"ns"}}`,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var obj unstructured.Unstructured
			err := json.Unmarshal([]byte(tt.resource), &obj)
			require.NoError(t, err, "failed to unmarshal test resource")

			classified, err := classifyClusterResource(&obj)
			if tt.wantErr {
				require.Error(t, err, "expected classifyClusterResource to error")
				return
			}
			require.NoError(t, err, "classifyClusterResource should not error")
			assert.Equal(t, tt.wantDesire, classified.desireName, "desireName mismatch")
			assert.Equal(t, tt.wantNodePool, classified.nodePoolName, "nodePoolName mismatch")
		})
	}
}
