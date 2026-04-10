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

package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	workv1 "open-cluster-management.io/api/work/v1"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func TestReadAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer_buildDegradedCondition(t *testing.T) {
	syncer := &readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer{}

	cond := syncer.buildDegradedCondition(api.ConditionTrue, "MaestroBundleNotFound", "bundle not found")
	assert.Equal(t, "Degraded", cond.Type)
	assert.Equal(t, api.ConditionTrue, cond.Status)
	assert.Equal(t, "MaestroBundleNotFound", cond.Reason)
	assert.Equal(t, "bundle not found", cond.Message)

	condFalse := syncer.buildDegradedCondition(api.ConditionFalse, "", "")
	assert.Equal(t, api.ConditionFalse, condFalse.Status)
	assert.Empty(t, condFalse.Reason)
	assert.Empty(t, condFalse.Message)
}

func TestReadAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer_buildObjectsFromUnstructuredObj(t *testing.T) {
	syncer := &readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer{}

	t.Run("single object returns one item", func(t *testing.T) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{Group: "hypershift.openshift.io", Version: "v1beta1", Kind: "NodePool"})
		obj.SetName("test-np")
		obj.SetNamespace("test-ns")

		objs, err := syncer.buildObjectsFromUnstructuredObj(obj)
		require.NoError(t, err)
		require.Len(t, objs, 1)
		assert.Equal(t, obj, objs[0].Object)
	})

	t.Run("list object flattens items", func(t *testing.T) {
		item1 := map[string]interface{}{"kind": "NodePool", "metadata": map[string]interface{}{"name": "np1"}}
		item2 := map[string]interface{}{"kind": "NodePool", "metadata": map[string]interface{}{"name": "np2"}}
		listObj := &unstructured.Unstructured{}
		listObj.SetUnstructuredContent(map[string]interface{}{
			"apiVersion": "hypershift.openshift.io/v1beta1",
			"kind":       "NodePoolList",
			"items":      []interface{}{item1, item2},
		})
		objs, err := syncer.buildObjectsFromUnstructuredObj(listObj)
		require.NoError(t, err)
		require.Len(t, objs, 2)

		require.NotNil(t, objs[0].Object, "Object should be set")
		u1 := objs[0].Object.(*unstructured.Unstructured)
		np1 := &hsv1beta1.NodePool{}
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(u1.UnstructuredContent(), np1)
		require.NoError(t, err)
		assert.Equal(t, "np1", np1.Name)

		u2 := objs[1].Object.(*unstructured.Unstructured)
		np2 := &hsv1beta1.NodePool{}
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(u2.UnstructuredContent(), np2)
		require.NoError(t, err)
		assert.Equal(t, "np2", np2.Name)
	})
}

// buildTestNodePoolMaestroBundleWithStatusFeedback builds a ManifestWork with exactly one resource status manifest
// and one status feedback value named "resource" with JsonRaw type, for NodePool testing.
func buildTestNodePoolMaestroBundleWithStatusFeedback(name, namespace, rawJSON string) *workv1.ManifestWork {
	jsonRaw := rawJSON
	return &workv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Status: workv1.ManifestWorkStatus{
			ResourceStatus: workv1.ManifestResourceStatus{
				Manifests: []workv1.ManifestCondition{
					{
						ResourceMeta: workv1.ManifestResourceMeta{
							Group:     "hypershift.openshift.io",
							Version:   "v1beta1",
							Kind:      "NodePool",
							Name:      "test-np",
							Namespace: "test-ns",
						},
						StatusFeedbacks: workv1.StatusFeedbackResult{
							Values: []workv1.FeedbackValue{
								{
									Name: "resource",
									Value: workv1.FieldValue{
										Type:    workv1.JsonRaw,
										JsonRaw: &jsonRaw,
									},
								},
							},
						},
						Conditions: []metav1.Condition{},
					},
				},
			},
		},
	}
}

func TestReadAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer_getSingleResourceStatusFeedbackRawJSONFromMaestroBundle(t *testing.T) {
	syncer := &readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer{}
	validJSON := `{"apiVersion":"hypershift.openshift.io/v1beta1","kind":"NodePool","metadata":{"name":"test-np","namespace":"test-ns"}}`

	tests := []struct {
		name    string
		bundle  *workv1.ManifestWork
		want    string
		wantErr bool
		errSub  string
	}{
		{
			name:   "success - returns raw JSON",
			bundle: buildTestNodePoolMaestroBundleWithStatusFeedback("bundle-1", "ns", validJSON),
			want:   validJSON,
		},
		{
			name: "error - zero manifests",
			bundle: &workv1.ManifestWork{
				Status: workv1.ManifestWorkStatus{
					ResourceStatus: workv1.ManifestResourceStatus{
						Manifests: []workv1.ManifestCondition{},
					},
				},
			},
			wantErr: true,
			errSub:  "expected exactly one resource within the Maestro Bundle, got 0",
		},
		{
			name: "error - zero status feedback values",
			bundle: &workv1.ManifestWork{
				Status: workv1.ManifestWorkStatus{
					ResourceStatus: workv1.ManifestResourceStatus{
						Manifests: []workv1.ManifestCondition{
							{
								ResourceMeta:    workv1.ManifestResourceMeta{},
								StatusFeedbacks: workv1.StatusFeedbackResult{Values: []workv1.FeedbackValue{}},
								Conditions:      []metav1.Condition{},
							},
						},
					},
				},
			},
			wantErr: true,
			errSub:  "expected exactly one status feedback value",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := syncer.getSingleResourceStatusFeedbackRawJSONFromMaestroBundle(tt.bundle)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSub)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, string(got))
			}
		})
	}
}

func TestReadAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer_managementClusterContentResourceIDFromNodePoolResourceID(t *testing.T) {
	syncer := &readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer{}
	nodePoolRID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/mycluster/nodePools/mynodepool"))

	got := syncer.managementClusterContentResourceIDFromNodePoolResourceID(nodePoolRID, api.MaestroBundleInternalNameReadonlyHypershiftNodePool)
	require.NotNil(t, got)
	assert.Equal(t, api.NodePoolManagementClusterContentResourceType.Type, got.ResourceType.Type)
	assert.Equal(t, string(api.MaestroBundleInternalNameReadonlyHypershiftNodePool), got.Name)
}

func TestReadAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer_calculateManagementClusterContentFromMaestroBundle(t *testing.T) {
	nodePoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/nodePools/mynodepool"))
	nodePool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: nodePoolResourceID}},
	}
	ref := &api.MaestroBundleReference{
		Name:                        api.MaestroBundleInternalNameReadonlyHypershiftNodePool,
		MaestroAPIMaestroBundleName: "bundle-name",
	}

	np := hsv1beta1.NodePool{TypeMeta: metav1.TypeMeta{APIVersion: "hypershift.openshift.io/v1beta1", Kind: "NodePool"}, ObjectMeta: metav1.ObjectMeta{Name: "np1", Namespace: "ns1"}}
	npJSONBytes, err := json.Marshal(np)
	require.NoError(t, err)
	validNPJSON := string(npJSONBytes)

	tests := []struct {
		name            string
		maestroGet      func(*maestro.MockClient)
		wantDegraded    bool
		wantKubeContent bool
		wantErr         bool
		errSub          string
	}{
		{
			name: "bundle not found - desired with degraded condition",
			maestroGet: func(m *maestro.MockClient) {
				m.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(nil, k8serrors.NewNotFound(schema.GroupResource{}, "bundle-name"))
			},
			wantDegraded:    true,
			wantKubeContent: false,
		},
		{
			name: "maestro get error - returns error",
			maestroGet: func(m *maestro.MockClient) {
				m.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(nil, fmt.Errorf("connection error"))
			},
			wantErr: true,
			errSub:  "failed to get Maestro Bundle",
		},
		{
			name: "bundle has invalid status feedback - desired with degraded",
			maestroGet: func(m *maestro.MockClient) {
				b := &workv1.ManifestWork{
					Status: workv1.ManifestWorkStatus{
						ResourceStatus: workv1.ManifestResourceStatus{
							Manifests: []workv1.ManifestCondition{
								{ResourceMeta: workv1.ManifestResourceMeta{}, StatusFeedbacks: workv1.StatusFeedbackResult{Values: []workv1.FeedbackValue{}}, Conditions: []metav1.Condition{}},
							},
						},
					},
				}
				m.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(b, nil)
			},
			wantDegraded:    true,
			wantKubeContent: false,
		},
		{
			name: "success - desired with kube content",
			maestroGet: func(m *maestro.MockClient) {
				b := buildTestNodePoolMaestroBundleWithStatusFeedback("bundle-name", "ns", validNPJSON)
				m.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(b, nil)
			},
			wantDegraded:    false,
			wantKubeContent: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockMaestro := maestro.NewMockClient(ctrl)
			tt.maestroGet(mockMaestro)

			syncer := &readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer{}
			got, err := syncer.calculateManagementClusterContentFromMaestroBundle(context.Background(), nodePool, ref, mockMaestro)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSub)
			} else {
				require.NoError(t, err)
				require.NotNil(t, got)
				assert.Equal(t, tt.wantKubeContent, got.Status.KubeContent != nil && len(got.Status.KubeContent.Items) > 0)
				hasDegradedTrue := controllerutils.IsConditionTrue(got.Status.Conditions, "Degraded")
				assert.Equal(t, tt.wantDegraded, hasDegradedTrue)
			}
		})
	}
}

// errorInjectingNodePoolMCCCRUD wraps ManagementClusterContentCRUD to allow error injection for NP testing.
type errorInjectingNodePoolMCCCRUD struct {
	database.ManagementClusterContentCRUD
	getResult  *api.ManagementClusterContent
	getErr     error
	replaceErr error
}

func (e *errorInjectingNodePoolMCCCRUD) Get(ctx context.Context, resourceID string) (*api.ManagementClusterContent, error) {
	if e.getErr != nil {
		return nil, e.getErr
	}
	if e.getResult != nil {
		return e.getResult, nil
	}
	return e.ManagementClusterContentCRUD.Get(ctx, resourceID)
}

func (e *errorInjectingNodePoolMCCCRUD) Replace(ctx context.Context, obj *api.ManagementClusterContent, opts *azcosmos.ItemOptions) (*api.ManagementClusterContent, error) {
	if e.replaceErr != nil {
		return nil, e.replaceErr
	}
	return e.ManagementClusterContentCRUD.Replace(ctx, obj, opts)
}

var _ database.ManagementClusterContentCRUD = &errorInjectingNodePoolMCCCRUD{}

// errorInjectingDBClientForNodePoolReadPersist wraps MockDBClient for node pool read/persist tests.
type errorInjectingDBClientForNodePoolReadPersist struct {
	*databasetesting.MockDBClient
	npMccCRUD database.ManagementClusterContentCRUD
	spnpCRUD  database.ServiceProviderNodePoolCRUD
}

func (e *errorInjectingDBClientForNodePoolReadPersist) NodePoolManagementClusterContents(subscriptionID, resourceGroupName, clusterName, nodePoolName string) database.ManagementClusterContentCRUD {
	if e.npMccCRUD != nil {
		return e.npMccCRUD
	}
	return e.MockDBClient.NodePoolManagementClusterContents(subscriptionID, resourceGroupName, clusterName, nodePoolName)
}

func (e *errorInjectingDBClientForNodePoolReadPersist) ServiceProviderNodePools(subscriptionID, resourceGroupName, clusterName, nodePoolName string) database.ServiceProviderNodePoolCRUD {
	if e.spnpCRUD != nil {
		return e.spnpCRUD
	}
	return e.MockDBClient.ServiceProviderNodePools(subscriptionID, resourceGroupName, clusterName, nodePoolName)
}

var _ database.DBClient = &errorInjectingDBClientForNodePoolReadPersist{}

func TestReadAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer_readAndPersistMaestroBundleContent(t *testing.T) {
	ctx := context.Background()
	nodePoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/nodePools/mynodepool"))
	nodePool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: nodePoolResourceID}},
	}
	ref := &api.MaestroBundleReference{
		Name:                        api.MaestroBundleInternalNameReadonlyHypershiftNodePool,
		MaestroAPIMaestroBundleName: "bundle-name",
	}
	np := hsv1beta1.NodePool{TypeMeta: metav1.TypeMeta{APIVersion: "hypershift.openshift.io/v1beta1", Kind: "NodePool"}, ObjectMeta: metav1.ObjectMeta{Name: "np1", Namespace: "ns1"}}
	npJSONBytes, err := json.Marshal(np)
	require.NoError(t, err)
	validNPJSON := string(npJSONBytes)

	t.Run("creates new ManagementClusterContent when not found", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockMaestro := maestro.NewMockClient(ctrl)
		b := buildTestNodePoolMaestroBundleWithStatusFeedback("bundle-name", "ns", validNPJSON)
		mockMaestro.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(b, nil)

		mockDB := databasetesting.NewMockDBClient()
		mccCRUD := mockDB.NodePoolManagementClusterContents("sub", "rg", "cluster", "mynodepool")

		syncer := &readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer{cosmosClient: mockDB}
		err := syncer.readAndPersistMaestroBundleContent(ctx, nodePool, ref, mockMaestro)
		require.NoError(t, err)

		got, err := mccCRUD.Get(ctx, string(api.MaestroBundleInternalNameReadonlyHypershiftNodePool))
		require.NoError(t, err)
		require.NotNil(t, got)
		require.NotNil(t, got.Status.KubeContent)
		require.Len(t, got.Status.KubeContent.Items, 1)
	})

	t.Run("replaces existing when content changed", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockMaestro := maestro.NewMockClient(ctrl)
		b := buildTestNodePoolMaestroBundleWithStatusFeedback("bundle-name", "ns", validNPJSON)
		mockMaestro.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(b, nil)

		mockDB := databasetesting.NewMockDBClient()
		mccCRUD := mockDB.NodePoolManagementClusterContents("sub", "rg", "cluster", "mynodepool")
		existingRID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/nodePools/mynodepool/managementClusterContents/readonlyHypershiftNodePool"))
		existing := &api.ManagementClusterContent{
			CosmosMetadata: api.CosmosMetadata{ResourceID: existingRID},
			ResourceID:     *existingRID,
			Status:         api.ManagementClusterContentStatus{KubeContent: &metav1.List{Items: []runtime.RawExtension{}}},
		}
		_, err := mccCRUD.Create(ctx, existing, nil)
		require.NoError(t, err)

		syncer := &readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer{cosmosClient: mockDB}
		err = syncer.readAndPersistMaestroBundleContent(ctx, nodePool, ref, mockMaestro)
		require.NoError(t, err)

		got, err := mccCRUD.Get(ctx, string(api.MaestroBundleInternalNameReadonlyHypershiftNodePool))
		require.NoError(t, err)
		require.NotNil(t, got.Status.KubeContent)
		require.Len(t, got.Status.KubeContent.Items, 1)
	})

	t.Run("keeps existing kube content when desired has no content (degraded)", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockMaestro := maestro.NewMockClient(ctrl)
		b := &workv1.ManifestWork{
			Status: workv1.ManifestWorkStatus{
				ResourceStatus: workv1.ManifestResourceStatus{
					Manifests: []workv1.ManifestCondition{
						{ResourceMeta: workv1.ManifestResourceMeta{}, StatusFeedbacks: workv1.StatusFeedbackResult{Values: []workv1.FeedbackValue{}}, Conditions: []metav1.Condition{}},
					},
				},
			},
		}
		mockMaestro.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(b, nil)

		mockDB := databasetesting.NewMockDBClient()
		mccCRUD := mockDB.NodePoolManagementClusterContents("sub", "rg", "cluster", "mynodepool")
		existingRID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/nodePools/mynodepool/managementClusterContents/readonlyHypershiftNodePool"))
		existingContent := &metav1.List{Items: []runtime.RawExtension{{Raw: []byte(`{}`)}}}
		existing := &api.ManagementClusterContent{
			CosmosMetadata: api.CosmosMetadata{ResourceID: existingRID},
			ResourceID:     *existingRID,
			Status:         api.ManagementClusterContentStatus{KubeContent: existingContent},
		}
		_, err := mccCRUD.Create(ctx, existing, nil)
		require.NoError(t, err)

		syncer := &readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer{cosmosClient: mockDB}
		err = syncer.readAndPersistMaestroBundleContent(ctx, nodePool, ref, mockMaestro)
		require.NoError(t, err)

		got, err := mccCRUD.Get(ctx, string(api.MaestroBundleInternalNameReadonlyHypershiftNodePool))
		require.NoError(t, err)
		require.NotNil(t, got.Status.KubeContent)
		assert.Equal(t, existingContent.Items[0].Raw, got.Status.KubeContent.Items[0].Raw)
	})

	t.Run("error occurs when Replace fails with precondition", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockMaestro := maestro.NewMockClient(ctrl)
		b := buildTestNodePoolMaestroBundleWithStatusFeedback("bundle-name", "ns", validNPJSON)
		mockMaestro.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(b, nil)

		existingRID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/nodePools/mynodepool/managementClusterContents/readonlyHypershiftNodePool"))
		existingDoc := &api.ManagementClusterContent{
			CosmosMetadata: api.CosmosMetadata{ResourceID: existingRID},
			ResourceID:     *existingRID,
			Status:         api.ManagementClusterContentStatus{KubeContent: &metav1.List{Items: []runtime.RawExtension{{Raw: []byte(validNPJSON)}}}},
		}

		mockDB := &errorInjectingDBClientForNodePoolReadPersist{
			MockDBClient: databasetesting.NewMockDBClient(),
			npMccCRUD: &errorInjectingNodePoolMCCCRUD{
				getResult:  existingDoc,
				replaceErr: databasetesting.NewPreconditionFailedError(),
			},
		}

		syncer := &readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer{cosmosClient: mockDB}
		err := syncer.readAndPersistMaestroBundleContent(ctx, nodePool, ref, mockMaestro)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to replace ManagementClusterContent")
		assert.True(t, database.IsResponseError(err, http.StatusPreconditionFailed))
	})

	t.Run("error occurs when managementClusterContentsDBClient.Get fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockMaestro := maestro.NewMockClient(ctrl)
		b := buildTestNodePoolMaestroBundleWithStatusFeedback("bundle-name", "ns", validNPJSON)
		mockMaestro.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(b, nil)

		getErr := fmt.Errorf("cosmos connection error")
		mockDB := &errorInjectingDBClientForNodePoolReadPersist{
			MockDBClient: databasetesting.NewMockDBClient(),
			npMccCRUD: &errorInjectingNodePoolMCCCRUD{
				getErr: getErr,
			},
		}

		syncer := &readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer{cosmosClient: mockDB}
		err := syncer.readAndPersistMaestroBundleContent(ctx, nodePool, ref, mockMaestro)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get ManagementClusterContent")
		assert.Contains(t, err.Error(), "cosmos connection error")
	})
}

func TestReadAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_NodePoolNotFound(t *testing.T) {
	mockDBClient := databasetesting.NewMockDBClient()
	syncer := &readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker: &alwaysSyncCooldownChecker{},
		cosmosClient:    mockDBClient,
	}

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "test-nodepool",
	}

	err := syncer.SyncOnce(context.Background(), key)
	assert.NoError(t, err)
}

func TestReadAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_GetServiceProviderNodePoolError(t *testing.T) {
	ctx := context.Background()
	baseMockDB := databasetesting.NewMockDBClient()

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "my-nodepool",
	}

	nodePoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/my-nodepool"))
	nodePool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: nodePoolResourceID}},
	}
	nodePoolsCRUD := baseMockDB.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	_, err := nodePoolsCRUD.Create(ctx, nodePool, nil)
	require.NoError(t, err)

	expectedError := fmt.Errorf("database error")
	mockDBClient := &errorInjectingDBClientForNodePoolReadPersist{
		MockDBClient: baseMockDB,
		spnpCRUD: &errorInjectingSPNPCRUD{
			getErr: expectedError,
		},
	}

	syncer := &readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker: &alwaysSyncCooldownChecker{},
		cosmosClient:    mockDBClient,
	}

	err = syncer.SyncOnce(ctx, key)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get or create ServiceProviderNodePool")
}

func TestReadAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_NoMaestroReadonlyBundleRefs(t *testing.T) {
	ctx := context.Background()
	mockDBClient := databasetesting.NewMockDBClient()

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "my-nodepool",
	}

	nodePoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/my-nodepool"))
	nodePool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: nodePoolResourceID}},
	}
	nodePoolsCRUD := mockDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	_, err := nodePoolsCRUD.Create(ctx, nodePool, nil)
	require.NoError(t, err)

	spnpResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/my-nodepool/serviceProviderNodePools/default"))
	spnp := &api.ServiceProviderNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
		ResourceID:     *spnpResourceID,
	}
	spnpCRUD := mockDBClient.ServiceProviderNodePools(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	_, err = spnpCRUD.Create(ctx, spnp, nil)
	require.NoError(t, err)

	syncer := &readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker: &alwaysSyncCooldownChecker{},
		cosmosClient:    mockDBClient,
	}

	err = syncer.SyncOnce(ctx, key)
	assert.NoError(t, err)
}

func TestReadAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_GetProvisionShardError(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()

	mockDBClient := databasetesting.NewMockDBClient()
	mockClusterService := ocm.NewMockClusterServiceClientSpec(ctrl)

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "my-nodepool",
	}

	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterResourceID}},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}
	clustersCRUD := mockDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
	_, err := clustersCRUD.Create(ctx, cluster, nil)
	require.NoError(t, err)

	nodePoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/my-nodepool"))
	nodePool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: nodePoolResourceID}},
	}
	nodePoolsCRUD := mockDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	_, err = nodePoolsCRUD.Create(ctx, nodePool, nil)
	require.NoError(t, err)

	spnpResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/my-nodepool/serviceProviderNodePools/default"))
	spnp := &api.ServiceProviderNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
		ResourceID:     *spnpResourceID,
		Status: api.ServiceProviderNodePoolStatus{
			MaestroReadonlyBundles: api.MaestroBundleReferenceList{
				{Name: api.MaestroBundleInternalNameReadonlyHypershiftNodePool, MaestroAPIMaestroBundleName: "bundle-name"},
			},
		},
	}
	spnpCRUD := mockDBClient.ServiceProviderNodePools(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	_, err = spnpCRUD.Create(ctx, spnp, nil)
	require.NoError(t, err)

	mockClusterService.EXPECT().
		GetClusterProvisionShard(gomock.Any(), cluster.ServiceProviderProperties.ClusterServiceID).
		Return(nil, fmt.Errorf("provision shard error"))

	syncer := &readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker:      &alwaysSyncCooldownChecker{},
		cosmosClient:         mockDBClient,
		clusterServiceClient: mockClusterService,
	}

	err = syncer.SyncOnce(ctx, key)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get Cluster Provision Shard")
}

func TestReadAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_ReadAndPersistFlow(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()

	mockDBClient := databasetesting.NewMockDBClient()
	mockClusterService := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockMaestroBuilder := maestro.NewMockMaestroClientBuilder(ctrl)
	mockMaestroClient := maestro.NewMockClient(ctrl)

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "my-nodepool",
	}

	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterResourceID}},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}
	clustersCRUD := mockDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
	_, err := clustersCRUD.Create(ctx, cluster, nil)
	require.NoError(t, err)

	nodePoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/my-nodepool"))
	nodePool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: nodePoolResourceID}},
	}
	nodePoolsCRUD := mockDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	_, err = nodePoolsCRUD.Create(ctx, nodePool, nil)
	require.NoError(t, err)

	spnpResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/my-nodepool/serviceProviderNodePools/default"))
	spnp := &api.ServiceProviderNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
		ResourceID:     *spnpResourceID,
		Status: api.ServiceProviderNodePoolStatus{
			MaestroReadonlyBundles: api.MaestroBundleReferenceList{
				{Name: api.MaestroBundleInternalNameReadonlyHypershiftNodePool, MaestroAPIMaestroBundleName: "bundle-name"},
			},
		},
	}
	spnpCRUD := mockDBClient.ServiceProviderNodePools(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	_, err = spnpCRUD.Create(ctx, spnp, nil)
	require.NoError(t, err)

	provisionShard := buildTestProvisionShard("test-consumer")
	mockClusterService.EXPECT().
		GetClusterProvisionShard(gomock.Any(), cluster.ServiceProviderProperties.ClusterServiceID).
		Return(provisionShard, nil)

	restEndpoint := provisionShard.MaestroConfig().RestApiConfig().Url()
	grpcEndpoint := provisionShard.MaestroConfig().GrpcApiConfig().Url()
	consumerName := provisionShard.MaestroConfig().ConsumerName()
	sourceID := maestro.GenerateMaestroSourceID("test-env", provisionShard.ID())
	mockMaestroBuilder.EXPECT().
		NewClient(gomock.Any(), restEndpoint, grpcEndpoint, consumerName, sourceID).
		Return(mockMaestroClient, nil)

	validNPJSON := `{"apiVersion":"hypershift.openshift.io/v1beta1","kind":"NodePool","metadata":{"name":"np1","namespace":"ns1"}}`
	bundle := buildTestNodePoolMaestroBundleWithStatusFeedback("bundle-name", "test-consumer", validNPJSON)
	mockMaestroClient.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(bundle, nil)

	syncer := &readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer{
		maestroReadonlyBundleHelper: maestroReadonlyBundleHelper{
			maestroSourceEnvironmentIdentifier: "test-env",
			maestroClientBuilder:               mockMaestroBuilder,
		},
		cooldownChecker:      &alwaysSyncCooldownChecker{},
		cosmosClient:         mockDBClient,
		clusterServiceClient: mockClusterService,
	}

	err = syncer.SyncOnce(ctx, key)
	require.NoError(t, err)

	mccCRUD := mockDBClient.NodePoolManagementClusterContents(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	got, err := mccCRUD.Get(ctx, string(api.MaestroBundleInternalNameReadonlyHypershiftNodePool))
	require.NoError(t, err)
	require.NotNil(t, got)
	require.NotNil(t, got.Status.KubeContent)
	require.Len(t, got.Status.KubeContent.Items, 1)
	var u unstructured.Unstructured
	err = json.Unmarshal(got.Status.KubeContent.Items[0].Raw, &u)
	require.NoError(t, err)
	assert.Equal(t, "NodePool", u.GetKind())
	assert.Equal(t, "np1", u.GetName())
}
