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

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func TestReadAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer_buildDegradedCondition(t *testing.T) {
	syncer := &readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer{}

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

func TestReadAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer_buildObjectsFromUnstructuredObj(t *testing.T) {
	syncer := &readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer{}

	t.Run("single object returns one item", func(t *testing.T) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{Group: "hypershift.openshift.io", Version: "v1beta1", Kind: "HostedCluster"})
		obj.SetName("test-hc")
		obj.SetNamespace("test-ns")

		objs, err := syncer.buildObjectsFromUnstructuredObj(obj)
		require.NoError(t, err)
		require.Len(t, objs, 1)
		assert.Equal(t, obj, objs[0].Object)
	})

	t.Run("list object flattens items", func(t *testing.T) {
		// Build an Unstructured that represents a K8s ConfigMapList with two ConfigMap items.
		item1 := map[string]interface{}{"kind": "HostedClusterList", "metadata": map[string]interface{}{"name": "cm1"}}
		item2 := map[string]interface{}{"kind": "HostedClusterList", "metadata": map[string]interface{}{"name": "cm2"}}
		listObj := &unstructured.Unstructured{}
		listObj.SetUnstructuredContent(map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMapList",
			"items":      []interface{}{item1, item2},
		})
		objs, err := syncer.buildObjectsFromUnstructuredObj(listObj)
		require.NoError(t, err)
		require.Len(t, objs, 2)

		// RawExtension has Object set (not Raw) when coming from buildObjectsFromUnstructuredObj; unmarshal to typed.
		require.NotNil(t, objs[0].Object, "Object should be set")
		u1 := objs[0].Object.(*unstructured.Unstructured)
		cm1 := &hsv1beta1.HostedCluster{}
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(u1.UnstructuredContent(), cm1)
		require.NoError(t, err)
		assert.Equal(t, "cm1", cm1.Name)

		u2 := objs[1].Object.(*unstructured.Unstructured)
		cm2 := &hsv1beta1.HostedCluster{}
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(u2.UnstructuredContent(), cm2)
		require.NoError(t, err)
		assert.Equal(t, "cm2", cm2.Name)
	})
}

// buildTestMaestroBundleWithStatusFeedback builds a ManifestWork with exactly one resource status manifest
// and one status feedback value named "resource" with JsonRaw type.
func buildTestMaestroBundleWithStatusFeedback(name, namespace, rawJSON string) *workv1.ManifestWork {
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
							Kind:      "HostedCluster",
							Name:      "test-hc",
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

func TestReadAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer_getSingleResourceStatusFeedbackRawJSONFromMaestroBundle(t *testing.T) {
	syncer := &readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer{}
	validJSON := `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"test"}}`

	tests := []struct {
		name    string
		bundle  *workv1.ManifestWork
		want    string
		wantErr bool
		errSub  string
	}{
		{
			name:   "success - returns raw JSON",
			bundle: buildTestMaestroBundleWithStatusFeedback("bundle-1", "ns", validJSON),
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
			name: "error - two manifests",
			bundle: &workv1.ManifestWork{
				Status: workv1.ManifestWorkStatus{
					ResourceStatus: workv1.ManifestResourceStatus{
						Manifests: []workv1.ManifestCondition{
							{ResourceMeta: workv1.ManifestResourceMeta{}, Conditions: []metav1.Condition{}},
							{ResourceMeta: workv1.ManifestResourceMeta{}, Conditions: []metav1.Condition{}},
						},
					},
				},
			},
			wantErr: true,
			errSub:  "expected exactly one resource within the Maestro Bundle, got 2",
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
		{
			name: "error - wrong feedback name",
			bundle: func() *workv1.ManifestWork {
				b := buildTestMaestroBundleWithStatusFeedback("b", "ns", validJSON)
				b.Status.ResourceStatus.Manifests[0].StatusFeedbacks.Values[0].Name = "wrong"
				return b
			}(),
			wantErr: true,
			errSub:  "expected status feedback value name to be 'resource', got wrong",
		},
		{
			name: "error - wrong feedback type",
			bundle: func() *workv1.ManifestWork {
				b := buildTestMaestroBundleWithStatusFeedback("b", "ns", validJSON)
				b.Status.ResourceStatus.Manifests[0].StatusFeedbacks.Values[0].Value.Type = workv1.String
				return b
			}(),
			wantErr: true,
			errSub:  "expected status feedback value type to be JsonRaw",
		},
		{
			name: "error - nil JsonRaw",
			bundle: func() *workv1.ManifestWork {
				b := buildTestMaestroBundleWithStatusFeedback("b", "ns", validJSON)
				b.Status.ResourceStatus.Manifests[0].StatusFeedbacks.Values[0].Value.JsonRaw = nil
				return b
			}(),
			wantErr: true,
			errSub:  "expected status feedback value JsonRaw to be not nil",
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

func TestReadAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer_managementClusterContentResourceIDFromClusterResourceID(t *testing.T) {
	syncer := &readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer{}
	clusterRID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/mycluster"))

	got := syncer.managementClusterContentResourceIDFromClusterResourceID(clusterRID, api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster)
	require.NotNil(t, got)
	assert.Equal(t, got.ResourceType.Type, api.ManagementClusterContentResourceType.Type)
	// Name is the last segment of the resource ID (the management cluster content name)
	assert.Equal(t, got.Name, string(api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster))
}

func TestReadAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer_calculateManagementClusterContentFromMaestroBundle(t *testing.T) {
	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterResourceID}},
	}
	ref := &api.MaestroBundleReference{
		Name:                        api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster,
		MaestroAPIMaestroBundleName: "bundle-name",
	}

	hc := hsv1beta1.HostedCluster{TypeMeta: metav1.TypeMeta{APIVersion: "hypershift.openshift.io/v1beta1", Kind: "HostedCluster"}, ObjectMeta: metav1.ObjectMeta{Name: "hc1", Namespace: "ns1"}}
	hcJSONBytes, err := json.Marshal(hc)
	require.NoError(t, err)
	validHCJSON := string(hcJSONBytes)

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
			name: "maestro api maestro bundleget error - returns error",
			maestroGet: func(m *maestro.MockClient) {
				m.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(nil, fmt.Errorf("connection error"))
			},
			wantErr: true,
			errSub:  "failed to get Maestro Bundle",
		},
		{
			name: "bundle has invalid status feedback - desired with degraded",
			maestroGet: func(m *maestro.MockClient) {
				// Bundle with no status feedback values
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
				b := buildTestMaestroBundleWithStatusFeedback("bundle-name", "ns", validHCJSON)
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

			syncer := &readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer{}
			got, err := syncer.calculateManagementClusterContentFromMaestroBundle(context.Background(), cluster, ref, mockMaestro)
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

func TestReadAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer_readAndPersistMaestroBundleContent(t *testing.T) {
	ctx := context.Background()
	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterResourceID}},
	}
	ref := &api.MaestroBundleReference{
		Name:                        api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster,
		MaestroAPIMaestroBundleName: "bundle-name",
	}
	hc := hsv1beta1.HostedCluster{TypeMeta: metav1.TypeMeta{APIVersion: "hypershift.openshift.io/v1beta1", Kind: "HostedCluster"}, ObjectMeta: metav1.ObjectMeta{Name: "hc1", Namespace: "ns1"}}
	hcJSONBytes, err := json.Marshal(hc)
	require.NoError(t, err)
	validHCJSON := string(hcJSONBytes)

	t.Run("creates new ManagementClusterContent when not found", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockMaestro := maestro.NewMockClient(ctrl)
		b := buildTestMaestroBundleWithStatusFeedback("bundle-name", "ns", validHCJSON)
		mockMaestro.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(b, nil)

		mockDB := databasetesting.NewMockDBClient()
		mccCRUD := mockDB.ManagementClusterContents("sub", "rg", "cluster")

		syncer := &readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer{cosmosClient: mockDB}
		err := syncer.readAndPersistMaestroBundleContent(ctx, cluster, ref, mockMaestro)
		require.NoError(t, err)

		// Content should have been created (name = bundle internal name)
		got, err := mccCRUD.Get(ctx, string(api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster))
		require.NoError(t, err)
		require.NotNil(t, got)
		require.NotNil(t, got.Status.KubeContent)
		require.Len(t, got.Status.KubeContent.Items, 1)
	})

	t.Run("replaces existing when content changed", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockMaestro := maestro.NewMockClient(ctrl)
		b := buildTestMaestroBundleWithStatusFeedback("bundle-name", "ns", validHCJSON)
		mockMaestro.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(b, nil)

		mockDB := databasetesting.NewMockDBClient()
		mccCRUD := mockDB.ManagementClusterContents("sub", "rg", "cluster")
		// Pre-create existing content with different payload
		existingRID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/managementClusterContents/readonlyHypershiftHostedCluster"))
		existing := &api.ManagementClusterContent{
			CosmosMetadata: api.CosmosMetadata{ResourceID: existingRID},
			ResourceID:     *existingRID,
			Status:         api.ManagementClusterContentStatus{KubeContent: &metav1.List{Items: []runtime.RawExtension{}}},
		}
		_, err := mccCRUD.Create(ctx, existing, nil)
		require.NoError(t, err)

		syncer := &readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer{cosmosClient: mockDB}
		err = syncer.readAndPersistMaestroBundleContent(ctx, cluster, ref, mockMaestro)
		require.NoError(t, err)

		got, err := mccCRUD.Get(ctx, string(api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster))
		require.NoError(t, err)
		require.NotNil(t, got.Status.KubeContent)
		require.Len(t, got.Status.KubeContent.Items, 1)
	})

	t.Run("keeps existing kube content when desired has no content (degraded)", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockMaestro := maestro.NewMockClient(ctrl)
		// Return bundle that has no valid status feedback so desired has no KubeContent
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
		mccCRUD := mockDB.ManagementClusterContents("sub", "rg", "cluster")
		existingRID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/managementClusterContents/readonlyHypershiftHostedCluster"))
		existingContent := &metav1.List{Items: []runtime.RawExtension{{Raw: []byte(`{}`)}}}
		existing := &api.ManagementClusterContent{
			CosmosMetadata: api.CosmosMetadata{ResourceID: existingRID},
			ResourceID:     *existingRID,
			Status:         api.ManagementClusterContentStatus{KubeContent: existingContent},
		}
		_, err := mccCRUD.Create(ctx, existing, nil)
		require.NoError(t, err)

		syncer := &readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer{cosmosClient: mockDB}
		err = syncer.readAndPersistMaestroBundleContent(ctx, cluster, ref, mockMaestro)
		require.NoError(t, err)

		got, err := mccCRUD.Get(ctx, string(api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster))
		require.NoError(t, err)
		// Should have kept existing content
		require.NotNil(t, got.Status.KubeContent)
		assert.Equal(t, existingContent.Items[0].Raw, got.Status.KubeContent.Items[0].Raw)
	})

	t.Run("no replace occurs when content has not changed", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockMaestro := maestro.NewMockClient(ctrl)
		b := buildTestMaestroBundleWithStatusFeedback("bundle-name", "ns", validHCJSON)
		// Get is called once when building desired for pre-create, and once inside readAndPersistMaestroBundleContent.
		mockMaestro.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(b, nil).Times(2)

		mockDB := databasetesting.NewMockDBClient()
		mccCRUD := mockDB.ManagementClusterContents("sub", "rg", "cluster")
		existingRID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/managementClusterContents/readonlyHypershiftHostedCluster"))
		// Pre-create content that matches exactly what the syncer would compute (same KubeContent and Degraded=False condition)
		// so that DeepEqual(existing, desired) is true and Replace is not called.
		syncer := &readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer{cosmosClient: mockDB}
		desired, err := syncer.calculateManagementClusterContentFromMaestroBundle(ctx, cluster, ref, mockMaestro)
		require.NoError(t, err)
		require.NotNil(t, desired)
		existing := &api.ManagementClusterContent{
			CosmosMetadata: api.CosmosMetadata{ResourceID: existingRID},
			ResourceID:     *existingRID,
			Status:         desired.Status,
		}
		_, err = mccCRUD.Create(ctx, existing, nil)
		require.NoError(t, err)

		err = syncer.readAndPersistMaestroBundleContent(ctx, cluster, ref, mockMaestro)
		require.NoError(t, err)

		// Document should still exist with same content (no Replace was needed)
		got, err := mccCRUD.Get(ctx, string(api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster))
		require.NoError(t, err)
		require.NotNil(t, got.Status.KubeContent)
		require.Len(t, got.Status.KubeContent.Items, 1)
	})

	t.Run("error occurs when object has been modified in Cosmos since we retrieved it", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockMaestro := maestro.NewMockClient(ctrl)
		b := buildTestMaestroBundleWithStatusFeedback("bundle-name", "ns", validHCJSON)
		mockMaestro.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(b, nil)

		existingRID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/managementClusterContents/readonlyHypershiftHostedCluster"))
		existingDoc := &api.ManagementClusterContent{
			CosmosMetadata: api.CosmosMetadata{ResourceID: existingRID},
			ResourceID:     *existingRID,
			Status:         api.ManagementClusterContentStatus{KubeContent: &metav1.List{Items: []runtime.RawExtension{{Raw: []byte(validHCJSON)}}}},
		}

		mockMCC := database.NewMockManagementClusterContentCRUD(ctrl)
		mockMCC.EXPECT().Get(gomock.Any(), string(api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster)).Return(existingDoc, nil)
		mockMCC.EXPECT().Replace(gomock.Any(), gomock.Any(), nil).Return(nil, databasetesting.NewPreconditionFailedError())

		mockDB := database.NewMockDBClient(ctrl)
		mockDB.EXPECT().ManagementClusterContents("sub", "rg", "cluster").Return(mockMCC)

		syncer := &readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer{cosmosClient: mockDB}
		err := syncer.readAndPersistMaestroBundleContent(ctx, cluster, ref, mockMaestro)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to replace ManagementClusterContent")
		assert.True(t, database.IsResponseError(err, http.StatusPreconditionFailed), "expected 412 Precondition Failed")
	})

	t.Run("error occurs when managementClusterContentsDBClient.Get fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockMaestro := maestro.NewMockClient(ctrl)
		b := buildTestMaestroBundleWithStatusFeedback("bundle-name", "ns", validHCJSON)
		mockMaestro.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(b, nil)

		getErr := fmt.Errorf("cosmos connection error")
		mockMCC := database.NewMockManagementClusterContentCRUD(ctrl)
		mockMCC.EXPECT().Get(gomock.Any(), string(api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster)).Return(nil, getErr)

		mockDB := database.NewMockDBClient(ctrl)
		mockDB.EXPECT().ManagementClusterContents("sub", "rg", "cluster").Return(mockMCC)

		syncer := &readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer{cosmosClient: mockDB}
		err := syncer.readAndPersistMaestroBundleContent(ctx, cluster, ref, mockMaestro)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get ManagementClusterContent")
		assert.Contains(t, err.Error(), "cosmos connection error")
	})
}

func TestReadAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_ClusterNotFound(t *testing.T) {
	mockDBClient := databasetesting.NewMockDBClient()
	syncer := &readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker: &alwaysSyncCooldownChecker{},
		cosmosClient:    mockDBClient,
	}

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	err := syncer.SyncOnce(context.Background(), key)
	assert.NoError(t, err)
}

func TestReadAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_GetServiceProviderClusterError(t *testing.T) {
	ctrl := gomock.NewController(t)

	mockDBClient := database.NewMockDBClient(ctrl)
	mockClusters := database.NewMockHCPClusterCRUD(ctrl)
	mockSPCs := database.NewMockServiceProviderClusterCRUD(ctrl)

	syncer := &readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker: &alwaysSyncCooldownChecker{},
		cosmosClient:    mockDBClient,
	}

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	cluster := &api.HCPOpenShiftCluster{
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}

	mockDBClient.EXPECT().HCPClusters(key.SubscriptionID, key.ResourceGroupName).Return(mockClusters)
	mockClusters.EXPECT().Get(gomock.Any(), key.HCPClusterName).Return(cluster, nil)
	mockDBClient.EXPECT().ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Return(mockSPCs)
	expectedError := fmt.Errorf("database error")
	mockSPCs.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, expectedError)

	err := syncer.SyncOnce(context.Background(), key)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get or create ServiceProviderCluster")
}

func TestReadAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_NoMaestroReadonlyBundlesRefs(t *testing.T) {
	ctx := context.Background()
	mockDBClient := databasetesting.NewMockDBClient()
	syncer := &readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker: &alwaysSyncCooldownChecker{},
		cosmosClient:    mockDBClient,
	}

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
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

	spcResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/serviceProviderClusters/default"))
	spc := &api.ServiceProviderCluster{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
		ResourceID:     *spcResourceID,
	}
	spcCRUD := mockDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	_, err = spcCRUD.Create(ctx, spc, nil)
	require.NoError(t, err)

	err = syncer.SyncOnce(ctx, key)
	assert.NoError(t, err)
}

func TestReadAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_GetProvisionShardError(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()

	mockDBClient := databasetesting.NewMockDBClient()
	mockClusterService := ocm.NewMockClusterServiceClientSpec(ctrl)

	syncer := &readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker:      &alwaysSyncCooldownChecker{},
		cosmosClient:         mockDBClient,
		clusterServiceClient: mockClusterService,
	}

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
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

	spcResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/serviceProviderClusters/default"))
	spc := &api.ServiceProviderCluster{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
		ResourceID:     *spcResourceID,
		Status: api.ServiceProviderClusterStatus{
			MaestroReadonlyBundles: api.MaestroBundleReferenceList{
				{Name: api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster, MaestroAPIMaestroBundleName: "bundle-name"},
			},
		},
	}
	spcCRUD := mockDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	_, err = spcCRUD.Create(ctx, spc, nil)
	require.NoError(t, err)

	mockClusterService.EXPECT().
		GetClusterProvisionShard(gomock.Any(), cluster.ServiceProviderProperties.ClusterServiceID).
		Return(nil, fmt.Errorf("provision shard error"))

	err = syncer.SyncOnce(ctx, key)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get Cluster Provision Shard")
}

func TestReadAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_ReadAndPersistFlow(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()

	mockDBClient := databasetesting.NewMockDBClient()
	mockClusterService := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockMaestroBuilder := maestro.NewMockMaestroClientBuilder(ctrl)
	mockMaestroClient := maestro.NewMockClient(ctrl)

	syncer := &readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker:                    &alwaysSyncCooldownChecker{},
		cosmosClient:                       mockDBClient,
		clusterServiceClient:               mockClusterService,
		maestroClientBuilder:               mockMaestroBuilder,
		maestroSourceEnvironmentIdentifier: "test-env",
	}

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
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

	spcResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/serviceProviderClusters/default"))
	spc := &api.ServiceProviderCluster{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
		ResourceID:     *spcResourceID,
		Status: api.ServiceProviderClusterStatus{
			MaestroReadonlyBundles: api.MaestroBundleReferenceList{
				{Name: api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster, MaestroAPIMaestroBundleName: "bundle-name"},
			},
		},
	}
	spcCRUD := mockDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	_, err = spcCRUD.Create(ctx, spc, nil)
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

	validHCJSON := `{"apiVersion":"hypershift.openshift.io/v1beta1","kind":"HostedCluster","metadata":{"name":"hc1","namespace":"ns1"}}`
	bundle := buildTestMaestroBundleWithStatusFeedback("bundle-name", "test-consumer", validHCJSON)
	mockMaestroClient.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(bundle, nil)

	err = syncer.SyncOnce(ctx, key)
	require.NoError(t, err)

	mccCRUD := mockDBClient.ManagementClusterContents(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	got, err := mccCRUD.Get(ctx, string(api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster))
	require.NoError(t, err)
	require.NotNil(t, got)
	require.NotNil(t, got.Status.KubeContent)
	require.Len(t, got.Status.KubeContent.Items, 1)
	// Decode and spot-check
	var u unstructured.Unstructured
	err = json.Unmarshal(got.Status.KubeContent.Items[0].Raw, &u)
	require.NoError(t, err)
	assert.Equal(t, "HostedCluster", u.GetKind())
	assert.Equal(t, "hc1", u.GetName())
}
