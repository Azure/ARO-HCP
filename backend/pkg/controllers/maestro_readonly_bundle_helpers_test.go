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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	workv1 "open-cluster-management.io/api/work/v1"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

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

func TestMaestroReadonlyBundleHelpers_buildDegradedCondition(t *testing.T) {
	cond := buildDegradedCondition(metav1.ConditionTrue, "MaestroBundleNotFound", "bundle not found")
	assert.Equal(t, "Degraded", cond.Type)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, "MaestroBundleNotFound", cond.Reason)
	assert.Equal(t, "bundle not found", cond.Message)

	condFalse := buildDegradedCondition(metav1.ConditionFalse, "", "")
	assert.Equal(t, metav1.ConditionFalse, condFalse.Status)
	assert.Empty(t, condFalse.Reason)
	assert.Empty(t, condFalse.Message)
}

func TestMaestroReadonlyBundleHelpers_buildObjectsFromUnstructuredObj(t *testing.T) {
	t.Run("single object returns one item", func(t *testing.T) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{Group: "hypershift.openshift.io", Version: "v1beta1", Kind: "HostedCluster"})
		obj.SetName("test-hc")
		obj.SetNamespace("test-ns")

		objs, err := buildObjectsFromUnstructuredObj(obj)
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
		objs, err := buildObjectsFromUnstructuredObj(listObj)
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

func TestMaestroReadonlyBundleHelpers_getSingleResourceStatusFeedbackRawJSONFromMaestroBundle(t *testing.T) {
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
			got, err := getSingleResourceStatusFeedbackRawJSONFromMaestroBundle(tt.bundle)
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

// callCountingMCCCRUD wraps ManagementClusterContentCRUD to count calls to Replace and Create
// so tests can assert that no unnecessary writes happen when the desired and existing documents
// match.
type callCountingMCCCRUD struct {
	database.ManagementClusterContentCRUD
	replaceCount int
	createCount  int
}

func (c *callCountingMCCCRUD) Replace(ctx context.Context, obj *api.ManagementClusterContent, opts *azcosmos.ItemOptions) (*api.ManagementClusterContent, error) {
	c.replaceCount++
	return c.ManagementClusterContentCRUD.Replace(ctx, obj, opts)
}

func (c *callCountingMCCCRUD) Create(ctx context.Context, obj *api.ManagementClusterContent, opts *azcosmos.ItemOptions) (*api.ManagementClusterContent, error) {
	c.createCount++
	return c.ManagementClusterContentCRUD.Create(ctx, obj, opts)
}

var _ database.ManagementClusterContentCRUD = &callCountingMCCCRUD{}

// errorInjectingMCCCRUD wraps ManagementClusterContentCRUD to allow error injection for testing.
type errorInjectingMCCCRUD struct {
	database.ManagementClusterContentCRUD
	getResult  *api.ManagementClusterContent
	getErr     error
	replaceErr error
}

func (e *errorInjectingMCCCRUD) Get(ctx context.Context, resourceID string) (*api.ManagementClusterContent, error) {
	if e.getErr != nil {
		return nil, e.getErr
	}
	if e.getResult != nil {
		return e.getResult, nil
	}
	return e.ManagementClusterContentCRUD.Get(ctx, resourceID)
}

func (e *errorInjectingMCCCRUD) Replace(ctx context.Context, obj *api.ManagementClusterContent, opts *azcosmos.ItemOptions) (*api.ManagementClusterContent, error) {
	if e.replaceErr != nil {
		return nil, e.replaceErr
	}
	return e.ManagementClusterContentCRUD.Replace(ctx, obj, opts)
}

var _ database.ManagementClusterContentCRUD = &errorInjectingMCCCRUD{}

// hcpClusterCRUDWithInjectedMCC wraps HCPClusterCRUD to return a fixed ManagementClusterContentCRUD (for tests).
type hcpClusterCRUDWithInjectedMCC struct {
	database.HCPClusterCRUD
	mccCRUD database.ManagementClusterContentCRUD
}

func (e *hcpClusterCRUDWithInjectedMCC) ManagementClusterContents(hcpClusterName string) database.ManagementClusterContentCRUD {
	return e.mccCRUD
}

var _ database.HCPClusterCRUD = &hcpClusterCRUDWithInjectedMCC{}

// errorInjectingResourcesDBClient wraps mockResourcesDBClient to return error-injecting CRUDs.
type errorInjectingResourcesDBClient struct {
	*databasetesting.MockResourcesDBClient
	mccCRUD      database.ManagementClusterContentCRUD
	clustersCRUD database.HCPClusterCRUD
	spcCRUD      database.ServiceProviderClusterCRUD
}

func (e *errorInjectingResourcesDBClient) HCPClusters(subscriptionID, resourceGroupName string) database.HCPClusterCRUD {
	var base database.HCPClusterCRUD
	if e.clustersCRUD != nil {
		base = e.clustersCRUD
	} else {
		base = e.MockResourcesDBClient.HCPClusters(subscriptionID, resourceGroupName)
	}
	if e.mccCRUD != nil {
		return &hcpClusterCRUDWithInjectedMCC{HCPClusterCRUD: base, mccCRUD: e.mccCRUD}
	}
	return base
}

func (e *errorInjectingResourcesDBClient) ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName string) database.ServiceProviderClusterCRUD {
	if e.spcCRUD != nil {
		return e.spcCRUD
	}
	return e.MockResourcesDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName)
}

var _ database.ResourcesDBClient = &errorInjectingResourcesDBClient{}

// errorInjectingSPCCRUD wraps ServiceProviderClusterCRUD to allow error injection.
type errorInjectingSPCCRUD struct {
	database.ServiceProviderClusterCRUD
	getErr error
}

func (e *errorInjectingSPCCRUD) Get(ctx context.Context, resourceID string) (*api.ServiceProviderCluster, error) {
	if e.getErr != nil {
		return nil, e.getErr
	}
	return e.ServiceProviderClusterCRUD.Get(ctx, resourceID)
}

var _ database.ServiceProviderClusterCRUD = &errorInjectingSPCCRUD{}

func TestMaestroReadonlyBundleHelpers_calculateManagementClusterContentFromMaestroBundle(t *testing.T) {
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

			got, err := calculateManagementClusterContentFromMaestroBundle(context.Background(), cluster.ID, ref, mockMaestro)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSub)
			} else {
				require.NoError(t, err)
				require.NotNil(t, got)
				assert.Equal(t, tt.wantKubeContent, got.Status.KubeContent != nil && len(got.Status.KubeContent.Items) > 0)
				hasDegradedTrue := meta.IsStatusConditionTrue(got.Status.Conditions, "Degraded")
				assert.Equal(t, tt.wantDegraded, hasDegradedTrue)
			}
		})
	}
}

func TestMaestroReadonlyBundleHelpers_readAndPersistMaestroReadonlyBundleContent(t *testing.T) {
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

		mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
		mccCRUD := mockResourcesDBClient.HCPClusters("sub", "rg").ManagementClusterContents("cluster")

		err := readAndPersistMaestroReadonlyBundleContent(ctx, cluster.ID, ref, mockMaestro, mccCRUD)
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

		mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
		mccCRUD := mockResourcesDBClient.HCPClusters("sub", "rg").ManagementClusterContents("cluster")
		// Pre-create existing content with different payload
		existingRID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/managementClusterContents/readonlyHypershiftHostedCluster"))
		existing := &api.ManagementClusterContent{
			CosmosMetadata: api.CosmosMetadata{ResourceID: existingRID},
			ResourceID:     *existingRID,
			Status:         api.ManagementClusterContentStatus{KubeContent: &metav1.List{Items: []runtime.RawExtension{}}},
		}
		_, err := mccCRUD.Create(ctx, existing, nil)
		require.NoError(t, err)

		err = readAndPersistMaestroReadonlyBundleContent(ctx, cluster.ID, ref, mockMaestro, mccCRUD)
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

		mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
		mccCRUD := mockResourcesDBClient.HCPClusters("sub", "rg").ManagementClusterContents("cluster")
		existingRID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/managementClusterContents/readonlyHypershiftHostedCluster"))
		existingContent := &metav1.List{Items: []runtime.RawExtension{{Raw: []byte(`{}`)}}}
		existing := &api.ManagementClusterContent{
			CosmosMetadata: api.CosmosMetadata{ResourceID: existingRID},
			ResourceID:     *existingRID,
			Status:         api.ManagementClusterContentStatus{KubeContent: existingContent},
		}
		_, err := mccCRUD.Create(ctx, existing, nil)
		require.NoError(t, err)

		err = readAndPersistMaestroReadonlyBundleContent(ctx, cluster.ID, ref, mockMaestro, mccCRUD)
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
		// Get is called once when building desired for pre-create, and once inside readAndPersistMaestroReadonlyBundleContent.
		mockMaestro.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(b, nil).Times(2)

		mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
		mccCRUD := mockResourcesDBClient.HCPClusters("sub", "rg").ManagementClusterContents("cluster")
		existingRID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/managementClusterContents/readonlyHypershiftHostedCluster"))
		// Pre-create content that matches exactly what the syncer would compute (same KubeContent and Degraded=False condition)
		// so that DeepEqual(existing, desired) is true and Replace is not called.
		desired, err := calculateManagementClusterContentFromMaestroBundle(ctx, cluster.ID, ref, mockMaestro)
		require.NoError(t, err)
		require.NotNil(t, desired)
		existing := &api.ManagementClusterContent{
			CosmosMetadata: api.CosmosMetadata{ResourceID: existingRID},
			ResourceID:     *existingRID,
			Status:         desired.Status,
		}
		_, err = mccCRUD.Create(ctx, existing, nil)
		require.NoError(t, err)

		err = readAndPersistMaestroReadonlyBundleContent(ctx, cluster.ID, ref, mockMaestro, mccCRUD)
		require.NoError(t, err)

		// Document should still exist with same content (no Replace was needed)
		got, err := mccCRUD.Get(ctx, string(api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster))
		require.NoError(t, err)
		require.NotNil(t, got.Status.KubeContent)
		require.Len(t, got.Status.KubeContent.Items, 1)
	})

	t.Run("Replace is not called when desired matches existing", func(t *testing.T) {
		// Regression test: when readAndPersistMaestroReadonlyBundleContent is invoked repeatedly
		// against unchanged content, it should not write to Cosmos. Previously the equality check
		// compared `existing` (which has CosmosMetadata.ExistingCosmosUID populated by the read
		// conversion) with a freshly-built `desired` (where ExistingCosmosUID is empty). Because
		// equality.Semantic.DeepEqual sees those struct fields differ, Replace was called every
		// iteration even when the document content was identical, generating churn (new etag /
		// timestamp on each write).
		ctrl := gomock.NewController(t)
		mockMaestro := maestro.NewMockClient(ctrl)
		b := buildTestMaestroBundleWithStatusFeedback("bundle-name", "ns", validHCJSON)
		// Get is called once when building desired for pre-create, and once inside readAndPersistMaestroReadonlyBundleContent.
		mockMaestro.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(b, nil).Times(2)

		mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
		baseMCCCRUD := mockResourcesDBClient.HCPClusters("sub", "rg").ManagementClusterContents("cluster")
		desired, err := calculateManagementClusterContentFromMaestroBundle(ctx, cluster.ID, ref, mockMaestro)
		require.NoError(t, err)
		require.NotNil(t, desired)
		// Pre-create the same content via the underlying CRUD (so the counter wrapper isn't
		// charged for setup work).
		_, err = baseMCCCRUD.Create(ctx, desired, nil)
		require.NoError(t, err)

		counter := &callCountingMCCCRUD{ManagementClusterContentCRUD: baseMCCCRUD}
		err = readAndPersistMaestroReadonlyBundleContent(ctx, cluster.ID, ref, mockMaestro, counter)
		require.NoError(t, err)

		assert.Equal(t, 0, counter.replaceCount, "expected no Replace calls when desired matches existing; CosmosMetadata.ExistingCosmosUID populated by the read conversion is not copied to desired and trips equality.Semantic.DeepEqual")
		assert.Equal(t, 0, counter.createCount, "Create should not be called when the document already exists")
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

		// Use error-injecting wrapper to simulate 412 Precondition Failed on Replace
		mockResourcesDBClient := &errorInjectingResourcesDBClient{
			MockResourcesDBClient: databasetesting.NewMockResourcesDBClient(),
			mccCRUD: &errorInjectingMCCCRUD{
				getResult:  existingDoc,
				replaceErr: databasetesting.NewPreconditionFailedError(),
			},
		}

		err := readAndPersistMaestroReadonlyBundleContent(ctx, cluster.ID, ref, mockMaestro, mockResourcesDBClient.mccCRUD)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to replace ManagementClusterContent")
		assert.True(t, database.IsPreconditionFailedError(err), "expected 412 Precondition Failed")
	})

	t.Run("error occurs when managementClusterContentsDBClient.Get fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockMaestro := maestro.NewMockClient(ctrl)
		b := buildTestMaestroBundleWithStatusFeedback("bundle-name", "ns", validHCJSON)
		mockMaestro.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(b, nil)

		getErr := fmt.Errorf("cosmos connection error")
		// Use error-injecting wrapper to simulate Get error
		mockResourcesDBClient := &errorInjectingResourcesDBClient{
			MockResourcesDBClient: databasetesting.NewMockResourcesDBClient(),
			mccCRUD: &errorInjectingMCCCRUD{
				getErr: getErr,
			},
		}

		err := readAndPersistMaestroReadonlyBundleContent(ctx, cluster.ID, ref, mockMaestro, mockResourcesDBClient.mccCRUD)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get ManagementClusterContent")
		assert.Contains(t, err.Error(), "cosmos connection error")
	})

	t.Run("preserves Degraded LastTransitionTime from Cosmos when status unchanged", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockMaestro := maestro.NewMockClient(ctrl)
		b := buildTestMaestroBundleWithStatusFeedback("bundle-name", "ns", validHCJSON)
		mockMaestro.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(b, nil).Times(1)

		mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
		mccCRUD := mockResourcesDBClient.HCPClusters("sub", "rg").ManagementClusterContents("cluster")
		existingRID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/managementClusterContents/readonlyHypershiftHostedCluster"))

		u := &unstructured.Unstructured{}
		require.NoError(t, json.Unmarshal([]byte(validHCJSON), u))
		historicLTT := metav1.Time{Time: time.Date(2020, 6, 15, 12, 0, 0, 0, time.UTC)}
		existing := &api.ManagementClusterContent{
			CosmosMetadata: api.CosmosMetadata{ResourceID: existingRID},
			ResourceID:     *existingRID,
			Status: api.ManagementClusterContentStatus{
				KubeContent: &metav1.List{
					Items: []runtime.RawExtension{{Object: u}},
				},
				Conditions: []metav1.Condition{
					{
						Type:               "Degraded",
						Status:             metav1.ConditionFalse,
						Reason:             "NoErrors",
						Message:            "As expected.",
						LastTransitionTime: historicLTT,
					},
				},
			},
		}
		_, err := mccCRUD.Create(ctx, existing, nil)
		require.NoError(t, err)

		err = readAndPersistMaestroReadonlyBundleContent(ctx, cluster.ID, ref, mockMaestro, mccCRUD)
		require.NoError(t, err)

		got, err := mccCRUD.Get(ctx, string(api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster))
		require.NoError(t, err)
		degraded := meta.FindStatusCondition(got.Status.Conditions, "Degraded")
		require.NotNil(t, degraded)
		assert.True(t, degraded.LastTransitionTime.Equal(&historicLTT), "expected LastTransitionTime from Cosmos to be preserved, got %v", degraded.LastTransitionTime)
	})

}
