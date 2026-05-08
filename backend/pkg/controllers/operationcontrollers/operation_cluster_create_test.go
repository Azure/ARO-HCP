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

package operationcontrollers

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationClusterCreate_SynchronizeOperation(t *testing.T) {
	createdAt := mustParseTime("2025-01-15T10:30:00Z")

	tests := []struct {
		name         string
		clusterState arohcpv1alpha1.ClusterState
		createdAt    *time.Time
		expectError  bool
		verify       func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture)
	}{
		{
			name:         "successful create updates operation to succeeded",
			clusterState: arohcpv1alpha1.ClusterStateReady,
			createdAt:    &createdAt,
			expectError:  false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)
			},
		},
		{
			name:         "non-terminal cluster state updates to provisioning",
			clusterState: arohcpv1alpha1.ClusterStateInstalling,
			createdAt:    nil,
			expectError:  false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateProvisioning, op.Status)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			fixture := newClusterTestFixture()
			cluster := fixture.newCluster(tt.createdAt)
			operation := fixture.newOperation(database.OperationRequestCreate)

			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, operation})
			require.NoError(t, err)

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			clusterStatus, err := arohcpv1alpha1.NewClusterStatus().
				State(tt.clusterState).
				Build()
			require.NoError(t, err)

			mockCSClient.EXPECT().
				GetClusterStatus(gomock.Any(), fixture.clusterInternalID).
				Return(clusterStatus, nil)

			// Provide listers so that determineOperationStatus can check cluster
			// and hosted cluster state from cosmos.
			succeededContent := newHostedClusterContent(t, &v1beta1.HostedCluster{
				Status: v1beta1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{Type: string(v1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue},
					},
					ControlPlaneVersion: v1beta1.ControlPlaneVersionStatus{
						History: []v1beta1.ControlPlaneUpdateHistory{
							{Version: "4.17.3", State: configv1.CompletedUpdate},
						},
					},
					ControlPlaneEndpoint: v1beta1.APIEndpoint{
						Host: "api.example.com",
						Port: 6443,
					},
				},
			})

			controller := &operationClusterCreate{
				resourcesDBClient:    mockResourcesDBClient,
				clusterServiceClient: mockCSClient,
				notificationClient:   nil,
				clusterLister: &listertesting.SliceClusterLister{
					Clusters: []*api.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com")},
				},
				clusterManagementClusterContentLister: &listertesting.SliceManagementClusterContentLister{
					Contents: []*api.ManagementClusterContent{succeededContent},
				},
			}

			err = controller.SynchronizeOperation(ctx, fixture.operationKey())

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tt.verify != nil {
				tt.verify(t, ctx, mockResourcesDBClient, fixture)
			}
		})
	}
}

// errorClusterLister always returns the configured error.
type errorClusterLister struct {
	err error
}

func (l *errorClusterLister) List(_ context.Context) ([]*api.HCPOpenShiftCluster, error) {
	return nil, l.err
}
func (l *errorClusterLister) Get(_ context.Context, _, _, _ string) (*api.HCPOpenShiftCluster, error) {
	return nil, l.err
}
func (l *errorClusterLister) ListForResourceGroup(_ context.Context, _, _ string) ([]*api.HCPOpenShiftCluster, error) {
	return nil, l.err
}

// errorManagementClusterContentLister always returns the configured error.
type errorManagementClusterContentLister struct {
	err error
}

func (l *errorManagementClusterContentLister) List(_ context.Context) ([]*api.ManagementClusterContent, error) {
	return nil, l.err
}
func (l *errorManagementClusterContentLister) GetForCluster(_ context.Context, _, _, _, _ string) (*api.ManagementClusterContent, error) {
	return nil, l.err
}
func (l *errorManagementClusterContentLister) ListForCluster(_ context.Context, _, _, _ string) ([]*api.ManagementClusterContent, error) {
	return nil, l.err
}
func (l *errorManagementClusterContentLister) ListForNodePool(_ context.Context, _, _, _, _ string) ([]*api.ManagementClusterContent, error) {
	return nil, l.err
}

func newHostedClusterContent(t *testing.T, hostedCluster *v1beta1.HostedCluster, conditions ...metav1.Condition) *api.ManagementClusterContent {
	t.Helper()
	raw, err := json.Marshal(hostedCluster)
	require.NoError(t, err)
	if conditions == nil {
		// Default: not degraded (required for the content to be considered healthy)
		conditions = []metav1.Condition{
			{Type: "Degraded", Status: metav1.ConditionFalse},
		}
	}

	contentResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/managementClusterContents/" + string(api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster)))

	return &api.ManagementClusterContent{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: contentResourceID,
		},
		Status: api.ManagementClusterContentStatus{
			Conditions: conditions,
			KubeContent: &metav1.List{
				Items: []kruntime.RawExtension{{Raw: raw}},
			},
		},
	}
}

func newClusterWithAPIURL(url string) *api.HCPOpenShiftCluster {
	fixture := newClusterTestFixture()
	cluster := fixture.newCluster(nil)
	cluster.ServiceProviderProperties.API = api.ServiceProviderAPIProfile{URL: url}
	return cluster
}

func TestDetermineOperationStatus(t *testing.T) {
	fixture := newClusterTestFixture()
	operation := fixture.newOperation(database.OperationRequestCreate)

	tests := []struct {
		name            string
		clusterLister   listers.ClusterLister
		contentLister   listers.ManagementClusterContentLister
		expectedState   arm.ProvisioningState
		expectedMessage string
		expectError     bool
		errContains     string
	}{
		{
			name: "both checks succeed → Succeeded",
			clusterLister: &listertesting.SliceClusterLister{
				Clusters: []*api.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com")},
			},
			contentLister: &listertesting.SliceManagementClusterContentLister{
				Contents: []*api.ManagementClusterContent{
					newHostedClusterContent(t, &v1beta1.HostedCluster{
						Status: v1beta1.HostedClusterStatus{
							Conditions: []metav1.Condition{
								{Type: string(v1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue},
							},
							ControlPlaneVersion: v1beta1.ControlPlaneVersionStatus{
								History: []v1beta1.ControlPlaneUpdateHistory{
									{Version: "4.17.3", State: configv1.CompletedUpdate},
								},
							},
							ControlPlaneEndpoint: v1beta1.APIEndpoint{
								Host: "api.example.com",
								Port: 6443,
							},
						},
					}),
				},
			},
			expectedState:   arm.ProvisioningStateSucceeded,
			expectedMessage: "",
		},
		{
			name: "cluster API URL empty → Provisioning (lowest priority wins)",
			clusterLister: &listertesting.SliceClusterLister{
				Clusters: []*api.HCPOpenShiftCluster{newClusterWithAPIURL("")},
			},
			contentLister: &listertesting.SliceManagementClusterContentLister{
				Contents: []*api.ManagementClusterContent{
					newHostedClusterContent(t, &v1beta1.HostedCluster{
						Status: v1beta1.HostedClusterStatus{
							Conditions: []metav1.Condition{
								{Type: string(v1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue},
							},
							ControlPlaneVersion: v1beta1.ControlPlaneVersionStatus{
								History: []v1beta1.ControlPlaneUpdateHistory{
									{Version: "4.17.3", State: configv1.CompletedUpdate},
								},
							},
							ControlPlaneEndpoint: v1beta1.APIEndpoint{
								Host: "api.example.com",
								Port: 6443,
							},
						},
					}),
				},
			},
			expectedState:   arm.ProvisioningStateProvisioning,
			expectedMessage: ".api.url is empty",
		},
		{
			name: "hosted cluster not found → Provisioning",
			clusterLister: &listertesting.SliceClusterLister{
				Clusters: []*api.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com")},
			},
			contentLister: &listertesting.SliceManagementClusterContentLister{},
			expectedState: arm.ProvisioningStateProvisioning,
		},
		{
			name:          "cluster lister error → error propagated",
			clusterLister: &errorClusterLister{err: fmt.Errorf("cosmos error")},
			contentLister: &listertesting.SliceManagementClusterContentLister{
				Contents: []*api.ManagementClusterContent{
					newHostedClusterContent(t, &v1beta1.HostedCluster{
						Status: v1beta1.HostedClusterStatus{
							Conditions: []metav1.Condition{
								{Type: string(v1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue},
							},
							ControlPlaneVersion: v1beta1.ControlPlaneVersionStatus{
								History: []v1beta1.ControlPlaneUpdateHistory{
									{Version: "4.17.3", State: configv1.CompletedUpdate},
								},
							},
							ControlPlaneEndpoint: v1beta1.APIEndpoint{
								Host: "api.example.com",
								Port: 6443,
							},
						},
					}),
				},
			},
			expectError: true,
			errContains: "cosmos error",
		},
		{
			name: "management content lister non-404 error → error propagated",
			clusterLister: &listertesting.SliceClusterLister{
				Clusters: []*api.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com")},
			},
			contentLister: &errorManagementClusterContentLister{err: fmt.Errorf("maestro error")},
			expectError:   true,
			errContains:   "maestro error",
		},
		{
			name:          "both errors → joined error",
			clusterLister: &errorClusterLister{err: fmt.Errorf("cluster error")},
			contentLister: &errorManagementClusterContentLister{err: fmt.Errorf("content error")},
			expectError:   true,
			errContains:   "cluster error",
		},
		{
			name: "hosted cluster degraded → Provisioning",
			clusterLister: &listertesting.SliceClusterLister{
				Clusters: []*api.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com")},
			},
			contentLister: &listertesting.SliceManagementClusterContentLister{
				Contents: []*api.ManagementClusterContent{
					newHostedClusterContent(t, &v1beta1.HostedCluster{} /* no conditions → Degraded not false */),
				},
			},
			expectedState: arm.ProvisioningStateProvisioning,
		},
		{
			name: "hosted cluster not available → Provisioning",
			clusterLister: &listertesting.SliceClusterLister{
				Clusters: []*api.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com")},
			},
			contentLister: &listertesting.SliceManagementClusterContentLister{
				Contents: []*api.ManagementClusterContent{
					newHostedClusterContent(t, &v1beta1.HostedCluster{
						Status: v1beta1.HostedClusterStatus{
							Conditions: []metav1.Condition{
								{Type: string(v1beta1.HostedClusterAvailable), Status: metav1.ConditionFalse, Reason: "NotReady", Message: "cluster is not ready"},
							},
							ControlPlaneVersion: v1beta1.ControlPlaneVersionStatus{
								History: []v1beta1.ControlPlaneUpdateHistory{
									{Version: "4.23.0", State: configv1.PartialUpdate},
								},
							},
						},
					}),
				},
			},
			expectedState:   arm.ProvisioningStateProvisioning,
			expectedMessage: "hosted cluster is not available: NotReady: cluster is not ready",
		},
		{
			name: "no control plane endpoint host → Provisioning",
			clusterLister: &listertesting.SliceClusterLister{
				Clusters: []*api.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com")},
			},
			contentLister: &listertesting.SliceManagementClusterContentLister{
				Contents: []*api.ManagementClusterContent{
					newHostedClusterContent(t, &v1beta1.HostedCluster{
						Status: v1beta1.HostedClusterStatus{
							Conditions: []metav1.Condition{
								{Type: string(v1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue},
							},
							ControlPlaneVersion: v1beta1.ControlPlaneVersionStatus{
								History: []v1beta1.ControlPlaneUpdateHistory{
									{Version: "4.17.3", State: configv1.CompletedUpdate},
								},
							},
						},
					}),
				},
			},
			expectedState:   arm.ProvisioningStateProvisioning,
			expectedMessage: "hosted cluster has no control plane endpoint host",
		},
		{
			name: "no control plane endpoint port → Provisioning",
			clusterLister: &listertesting.SliceClusterLister{
				Clusters: []*api.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com")},
			},
			contentLister: &listertesting.SliceManagementClusterContentLister{
				Contents: []*api.ManagementClusterContent{
					newHostedClusterContent(t, &v1beta1.HostedCluster{
						Status: v1beta1.HostedClusterStatus{
							Conditions: []metav1.Condition{
								{Type: string(v1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue},
							},
							ControlPlaneVersion: v1beta1.ControlPlaneVersionStatus{
								History: []v1beta1.ControlPlaneUpdateHistory{
									{Version: "4.17.3", State: configv1.CompletedUpdate},
								},
							},
							ControlPlaneEndpoint: v1beta1.APIEndpoint{
								Host: "api.example.com",
							},
						},
					}),
				},
			},
			expectedState:   arm.ProvisioningStateProvisioning,
			expectedMessage: "hosted cluster has no control plane endpoint port",
		},
		{
			name: "version with valid success condition but not installed → Provisioning",
			clusterLister: &listertesting.SliceClusterLister{
				Clusters: []*api.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com")},
			},
			contentLister: &listertesting.SliceManagementClusterContentLister{
				Contents: []*api.ManagementClusterContent{
					newHostedClusterContent(t, &v1beta1.HostedCluster{
						Status: v1beta1.HostedClusterStatus{
							Conditions: []metav1.Condition{
								{Type: string(v1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue},
							},
							ControlPlaneVersion: v1beta1.ControlPlaneVersionStatus{
								History: []v1beta1.ControlPlaneUpdateHistory{
									{Version: "4.23.0", State: configv1.PartialUpdate},
								},
							},
							ControlPlaneEndpoint: v1beta1.APIEndpoint{
								Host: "api.example.com",
								Port: 6443,
							},
						},
					}),
				},
			},
			expectedState:   arm.ProvisioningStateProvisioning,
			expectedMessage: "hosted cluster has no installed version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			controller := &operationClusterCreate{
				clusterLister:                         tt.clusterLister,
				clusterManagementClusterContentLister: tt.contentLister,
			}

			result, err := controller.determineOperationStatus(ctx, operation)

			if tt.expectError {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, tt.expectedState, result.provisioningState)
			if tt.expectedMessage != "" {
				assert.Equal(t, tt.expectedMessage, result.message)
			}
		})
	}
}
