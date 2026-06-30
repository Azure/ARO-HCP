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
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	internallistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting"
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
			succeededDesire := newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
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
				clock:                utilsclock.RealClock{},
				resourcesDBClient:    mockResourcesDBClient,
				clusterServiceClient: mockCSClient,
				notificationClient:   nil,
				clusterLister: &listertesting.SliceClusterLister{
					Clusters: []*api.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com")},
				},
				readDesireLister: &internallistertesting.SliceReadDesireLister{
					Desires: []*kubeapplier.ReadDesire{succeededDesire},
				},
				serviceProviderClusterLister: &listertesting.SliceServiceProviderClusterLister{
					ServiceProviderClusters: []*api.ServiceProviderCluster{newServiceProviderClusterWithCABundle("test-ca-bundle")},
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

// newServiceProviderClusterWithCABundle builds a ServiceProviderCluster with the
// given ServingCABundle value.
func newServiceProviderClusterWithCABundle(caBundle string) *api.ServiceProviderCluster {
	fixture := newClusterTestFixture()
	spcResourceID := api.Must(azcorearm.ParseResourceID(
		fixture.clusterResourceID.String() + "/serviceProviderClusters/default",
	))
	return &api.ServiceProviderCluster{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   spcResourceID,
			PartitionKey: strings.ToLower(spcResourceID.SubscriptionID),
		},
		Status: api.ServiceProviderClusterStatus{
			ServingCABundle: caBundle,
		},
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

// errorReadDesireLister always returns the configured error.
type errorReadDesireLister struct {
	err error
}

func (l *errorReadDesireLister) List(_ context.Context) ([]*kubeapplier.ReadDesire, error) {
	return nil, l.err
}
func (l *errorReadDesireLister) GetForCluster(_ context.Context, _, _, _, _ string) (*kubeapplier.ReadDesire, error) {
	return nil, l.err
}
func (l *errorReadDesireLister) GetForNodePool(_ context.Context, _, _, _, _, _ string) (*kubeapplier.ReadDesire, error) {
	return nil, l.err
}
func (l *errorReadDesireLister) ListForManagementCluster(_ context.Context, _ *azcorearm.ResourceID) ([]*kubeapplier.ReadDesire, error) {
	return nil, l.err
}
func (l *errorReadDesireLister) ListForCluster(_ context.Context, _, _, _ string) ([]*kubeapplier.ReadDesire, error) {
	return nil, l.err
}
func (l *errorReadDesireLister) ListForNodePool(_ context.Context, _, _, _, _ string) ([]*kubeapplier.ReadDesire, error) {
	return nil, l.err
}

// errorServiceProviderClusterLister always returns the configured error.
type errorServiceProviderClusterLister struct {
	err error
}

func (l *errorServiceProviderClusterLister) List(_ context.Context) ([]*api.ServiceProviderCluster, error) {
	return nil, l.err
}
func (l *errorServiceProviderClusterLister) Get(_ context.Context, _, _, _ string) (*api.ServiceProviderCluster, error) {
	return nil, l.err
}
func (l *errorServiceProviderClusterLister) ListForCluster(_ context.Context, _, _, _ string) ([]*api.ServiceProviderCluster, error) {
	return nil, l.err
}

// newHostedClusterReadDesire builds a ReadDesire whose Status.KubeContent.Raw
// is the serialized HostedCluster. The ReadDesire itself defaults to a
// Successful=True condition (the kube-applier has observed the target);
// pass conditions to override.
func newHostedClusterReadDesire(t *testing.T, hostedCluster *v1beta1.HostedCluster, conditions ...metav1.Condition) *kubeapplier.ReadDesire {
	t.Helper()
	raw, err := json.Marshal(hostedCluster)
	require.NoError(t, err)
	if conditions == nil {
		// Default: kube-applier successfully observed the target.
		conditions = []metav1.Condition{
			{Type: kubeapplier.ConditionTypeSuccessful, Status: metav1.ConditionTrue, Reason: kubeapplier.ConditionReasonNoErrors},
		}
	}

	resourceID := api.Must(azcorearm.ParseResourceID(
		kubeapplier.ToClusterScopedReadDesireResourceIDString(
			testSubscriptionID, testResourceGroupName, testClusterName, maestrohelpers.ReadDesireNameReadonlyHostedCluster)))

	return &kubeapplier.ReadDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		Status: kubeapplier.ReadDesireStatus{
			Conditions:  conditions,
			KubeContent: &kruntime.RawExtension{Raw: raw},
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
		name                         string
		clusterLister                listers.ClusterLister
		readDesireLister             dblisters.ReadDesireLister
		serviceProviderClusterLister listers.ServiceProviderClusterLister
		expectedState                arm.ProvisioningState
		expectedMessage              string
		expectError                  bool
		errContains                  string
	}{
		{
			name: "both checks succeed → Succeeded",
			clusterLister: &listertesting.SliceClusterLister{
				Clusters: []*api.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com")},
			},
			readDesireLister: &internallistertesting.SliceReadDesireLister{
				Desires: []*kubeapplier.ReadDesire{
					newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
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
			readDesireLister: &internallistertesting.SliceReadDesireLister{
				Desires: []*kubeapplier.ReadDesire{
					newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
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
			readDesireLister: &internallistertesting.SliceReadDesireLister{},
			expectedState:    arm.ProvisioningStateProvisioning,
		},
		{
			name:          "cluster lister error → error propagated",
			clusterLister: &errorClusterLister{err: fmt.Errorf("cosmos error")},
			readDesireLister: &internallistertesting.SliceReadDesireLister{
				Desires: []*kubeapplier.ReadDesire{
					newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
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
			name: "read desire lister non-404 error → error propagated",
			clusterLister: &listertesting.SliceClusterLister{
				Clusters: []*api.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com")},
			},
			readDesireLister: &errorReadDesireLister{err: fmt.Errorf("maestro error")},
			expectError:      true,
			errContains:      "maestro error",
		},
		{
			name:             "both errors → joined error",
			clusterLister:    &errorClusterLister{err: fmt.Errorf("cluster error")},
			readDesireLister: &errorReadDesireLister{err: fmt.Errorf("content error")},
			expectError:      true,
			errContains:      "cluster error",
		},
		{
			name: "read desire not yet successful → Provisioning",
			clusterLister: &listertesting.SliceClusterLister{
				Clusters: []*api.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com")},
			},
			readDesireLister: &internallistertesting.SliceReadDesireLister{
				Desires: []*kubeapplier.ReadDesire{
					newHostedClusterReadDesire(t, &v1beta1.HostedCluster{},
						metav1.Condition{Type: kubeapplier.ConditionTypeSuccessful, Status: metav1.ConditionFalse, Reason: kubeapplier.ConditionReasonKubeAPIError, Message: "boom"}),
				},
			},
			expectedState:   arm.ProvisioningStateProvisioning,
			expectedMessage: "ReadDesire is not successful: KubeAPIError: boom",
		},
		{
			name: "hosted cluster not available → Provisioning",
			clusterLister: &listertesting.SliceClusterLister{
				Clusters: []*api.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com")},
			},
			readDesireLister: &internallistertesting.SliceReadDesireLister{
				Desires: []*kubeapplier.ReadDesire{
					newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
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
			readDesireLister: &internallistertesting.SliceReadDesireLister{
				Desires: []*kubeapplier.ReadDesire{
					newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
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
			readDesireLister: &internallistertesting.SliceReadDesireLister{
				Desires: []*kubeapplier.ReadDesire{
					newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
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
			name: "serving CA bundle not yet populated → Provisioning",
			clusterLister: &listertesting.SliceClusterLister{
				Clusters: []*api.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com")},
			},
			readDesireLister: &internallistertesting.SliceReadDesireLister{
				Desires: []*kubeapplier.ReadDesire{
					newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
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
			serviceProviderClusterLister: &listertesting.SliceServiceProviderClusterLister{
				ServiceProviderClusters: []*api.ServiceProviderCluster{newServiceProviderClusterWithCABundle("")},
			},
			expectedState:   arm.ProvisioningStateProvisioning,
			expectedMessage: "serving CA bundle not yet populated",
		},
		{
			name: "ServiceProviderCluster not cached yet → Provisioning",
			clusterLister: &listertesting.SliceClusterLister{
				Clusters: []*api.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com")},
			},
			readDesireLister: &internallistertesting.SliceReadDesireLister{
				Desires: []*kubeapplier.ReadDesire{
					newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
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
			serviceProviderClusterLister: &listertesting.SliceServiceProviderClusterLister{},
			expectedState:                arm.ProvisioningStateProvisioning,
			expectedMessage:              "ServiceProviderCluster not cached yet",
		},
		{
			name: "ServiceProviderCluster lister error → error propagated",
			clusterLister: &listertesting.SliceClusterLister{
				Clusters: []*api.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com")},
			},
			readDesireLister: &internallistertesting.SliceReadDesireLister{
				Desires: []*kubeapplier.ReadDesire{
					newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
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
			serviceProviderClusterLister: &errorServiceProviderClusterLister{err: fmt.Errorf("spc error")},
			expectError:                  true,
			errContains:                  "spc error",
		},
		{
			name: "version with valid success condition but not installed → Provisioning",
			clusterLister: &listertesting.SliceClusterLister{
				Clusters: []*api.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com")},
			},
			readDesireLister: &internallistertesting.SliceReadDesireLister{
				Desires: []*kubeapplier.ReadDesire{
					newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
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

			spcLister := tt.serviceProviderClusterLister
			if spcLister == nil {
				// Default: provide a ServiceProviderCluster with a populated CA bundle
				// so the serving CA check succeeds unless explicitly overridden.
				spcLister = &listertesting.SliceServiceProviderClusterLister{
					ServiceProviderClusters: []*api.ServiceProviderCluster{newServiceProviderClusterWithCABundle("test-ca-bundle")},
				}
			}

			controller := &operationClusterCreate{
				clusterLister:                tt.clusterLister,
				readDesireLister:             tt.readDesireLister,
				serviceProviderClusterLister: spcLister,
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
