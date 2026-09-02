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

package operations

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	operationtesting "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils/operationtesting"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationClusterCreate_SynchronizeOperation(t *testing.T) {
	createdAt := operationtesting.MustParseTime("2025-01-15T10:30:00Z")
	fixture := operationtesting.NewClusterTestFixture()

	succeededDesire := func(t *testing.T) *kubeapplierapi.ReadDesire {
		return operationtesting.NewHostedClusterReadDesire(t, &v1beta1.HostedCluster{
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
	}

	testCases := []struct {
		name              string
		clock             utilsclock.PassiveClock
		existingCluster   *coreapi.HCPOpenShiftCluster
		existingOperation *coreapi.Operation
		readDesireLister  kubeapplierlisters.ReadDesireLister
		setupCSMock       func(ctrl *gomock.Controller, fixture *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec
		wantErr           bool
		verifyDB          func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient)
	}{
		{
			name:              "successful create updates operation to succeeded",
			existingCluster:   newClusterWithAPIURL("https://api.example.com", &createdAt),
			existingOperation: fixture.NewOperation(cosmosstorageutils.OperationRequestCreate),
			setupCSMock: func(ctrl *gomock.Controller, fixture *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, err := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateReady).
					Build()
				require.NoError(t, err)
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.ClusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateSucceeded, op.Status)
			},
		},
		{
			name:              "non-terminal cluster state updates to provisioning",
			existingCluster:   newClusterWithAPIURL("https://api.example.com", nil),
			existingOperation: fixture.NewOperation(cosmosstorageutils.OperationRequestCreate),
			setupCSMock: func(ctrl *gomock.Controller, fixture *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, err := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateInstalling).
					Build()
				require.NoError(t, err)
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.ClusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateProvisioning, op.Status)
			},
		},
		{
			name:            "polls cluster service when operation InternalID is empty",
			existingCluster: newClusterWithAPIURL("https://api.example.com", &createdAt),
			existingOperation: func() *coreapi.Operation {
				op := fixture.NewOperation(cosmosstorageutils.OperationRequestCreate)
				op.InternalID = metadataapi.InternalID{}
				return op
			}(),
			setupCSMock: func(ctrl *gomock.Controller, fixture *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, err := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateReady).
					Build()
				require.NoError(t, err)
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.ClusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateSucceeded, op.Status)
			},
		},
		{
			name: "reports Provisioning when cluster ClusterServiceID is unset",
			existingCluster: func() *coreapi.HCPOpenShiftCluster {
				cluster := newClusterWithAPIURL("https://api.example.com", &createdAt)
				cluster.ServiceProviderProperties.ClusterServiceID = nil
				return cluster
			}(),
			existingOperation: fixture.NewOperation(cosmosstorageutils.OperationRequestCreate),
			// ClusterServiceID is nil, so clusterServiceCreateOperationState reports
			// Provisioning without calling GetClusterStatus; the bare mock therefore
			// has no expectations. Every other sub-state is ready, so the operation
			// is persisted as Provisioning.
			setupCSMock: func(ctrl *gomock.Controller, _ *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec {
				return ocm.NewMockClusterServiceClientSpec(ctrl)
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateProvisioning, op.Status)
			},
		},
		{
			name: "returns early when cluster active operation id mismatches",
			existingCluster: func() *coreapi.HCPOpenShiftCluster {
				cluster := newClusterWithAPIURL("https://api.example.com", &createdAt)
				cluster.ServiceProviderProperties.ActiveOperationID = "other-operation"
				return cluster
			}(),
			existingOperation: fixture.NewOperation(cosmosstorageutils.OperationRequestCreate),
			setupCSMock: func(ctrl *gomock.Controller, _ *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec {
				return ocm.NewMockClusterServiceClientSpec(ctrl)
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:  "deadline exceeded marks operation as failed",
			clock: clocktesting.NewFakePassiveClock(operationtesting.MustParseTime("2025-01-15T12:00:00Z")),
			existingCluster: func() *coreapi.HCPOpenShiftCluster {
				cluster := newClusterWithAPIURL("https://api.example.com", nil)
				deadline := metav1.NewTime(operationtesting.MustParseTime("2025-01-15T11:30:00Z"))
				cluster.ServiceProviderProperties.CreateOperationCompletionDeadline = &deadline
				return cluster
			}(),
			existingOperation: fixture.NewOperation(cosmosstorageutils.OperationRequestCreate),
			setupCSMock: func(ctrl *gomock.Controller, fixture *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, err := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateInstalling).
					Build()
				require.NoError(t, err)
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.ClusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateFailed, op.Status)
				require.NotNil(t, op.Error)
				assert.Equal(t, coreapi.CloudErrorCodeInternalServerError, op.Error.Code)
			},
		},
		{
			name:  "deadline exceeded with CS succeeded but cosmos provisioning marks as failed",
			clock: clocktesting.NewFakePassiveClock(operationtesting.MustParseTime("2025-01-15T12:00:00Z")),
			existingCluster: func() *coreapi.HCPOpenShiftCluster {
				cluster := newClusterWithAPIURL("https://api.example.com", &createdAt)
				deadline := metav1.NewTime(operationtesting.MustParseTime("2025-01-15T11:30:00Z"))
				cluster.ServiceProviderProperties.CreateOperationCompletionDeadline = &deadline
				return cluster
			}(),
			existingOperation: fixture.NewOperation(cosmosstorageutils.OperationRequestCreate),
			readDesireLister:  &kubeapplierlistertesting.SliceReadDesireLister{},
			setupCSMock: func(ctrl *gomock.Controller, fixture *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, err := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateReady).
					Build()
				require.NoError(t, err)
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.ClusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateFailed, op.Status)
				require.NotNil(t, op.Error)
				assert.Equal(t, coreapi.CloudErrorCodeInternalServerError, op.Error.Code)
			},
		},
		{
			name:  "deadline not yet exceeded continues with provisioning",
			clock: clocktesting.NewFakePassiveClock(operationtesting.MustParseTime("2025-01-15T11:00:00Z")),
			existingCluster: func() *coreapi.HCPOpenShiftCluster {
				cluster := newClusterWithAPIURL("https://api.example.com", nil)
				deadline := metav1.NewTime(operationtesting.MustParseTime("2025-01-15T11:30:00Z"))
				cluster.ServiceProviderProperties.CreateOperationCompletionDeadline = &deadline
				return cluster
			}(),
			existingOperation: fixture.NewOperation(cosmosstorageutils.OperationRequestCreate),
			setupCSMock: func(ctrl *gomock.Controller, fixture *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, err := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateInstalling).
					Build()
				require.NoError(t, err)
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.ClusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateProvisioning, op.Status)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))
			ctrl := gomock.NewController(t)

			mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{tc.existingCluster, tc.existingOperation})
			require.NoError(t, err)

			listerOperation, err := mockResourcesDBClient.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
			require.NoError(t, err)

			mockCSClient := tc.setupCSMock(ctrl, fixture)

			testClock := tc.clock
			if testClock == nil {
				testClock = utilsclock.RealClock{}
			}
			controller := &operationClusterCreate{
				clock: testClock,
				activeOperationLister: &corelistertesting.SliceActiveOperationLister{
					Operations: []*coreapi.Operation{listerOperation},
				},
				resourcesDBClient:    mockResourcesDBClient,
				clusterServiceClient: mockCSClient,
				notificationClient:   nil,
				clusterLister: &corelistertesting.SliceClusterLister{
					Clusters: []*coreapi.HCPOpenShiftCluster{tc.existingCluster},
				},
				serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{
					ServiceProviderClusters: []*coreapi.ServiceProviderCluster{
						{
							CosmosMetadata: coreapi.CosmosMetadata{
								ResourceID: metadataapi.Must(azcorearm.ParseResourceID(
									fixture.ClusterResourceID.String() + "/" +
										coreapi.ServiceProviderClusterResourceTypeName + "/" +
										coreapi.ServiceProviderClusterResourceName)),
							},
							Status: coreapi.ServiceProviderClusterStatus{
								ServingCABundle: "fake-ca-data",
								AzureResources: coreapi.AzureResources{
									RoleAssignments: coreapi.AzureMultiReference{
										AzureResources: []*azcorearm.ResourceID{
											metadataapi.Must(azcorearm.ParseResourceID(
												"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/managed-rg/providers/Microsoft.Authorization/roleAssignments/11111111-1111-1111-1111-111111111111")),
										},
									},
								},
							},
						},
					},
				},
				readDesireLister: func() kubeapplierlisters.ReadDesireLister {
					if tc.readDesireLister != nil {
						return tc.readDesireLister
					}
					return &kubeapplierlistertesting.SliceReadDesireLister{
						Desires: []*kubeapplierapi.ReadDesire{succeededDesire(t)},
					}
				}(),
			}

			err = controller.SynchronizeOperation(ctx, fixture.OperationKey())
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tc.verifyDB != nil {
				tc.verifyDB(t, ctx, mockResourcesDBClient)
			}
		})
	}
}

// errorClusterLister always returns the configured error.
type errorClusterLister struct {
	err error
}

func (l *errorClusterLister) List(_ context.Context) ([]*coreapi.HCPOpenShiftCluster, error) {
	return nil, l.err
}
func (l *errorClusterLister) Get(_ context.Context, _, _, _ string) (*coreapi.HCPOpenShiftCluster, error) {
	return nil, l.err
}
func (l *errorClusterLister) ListForResourceGroup(_ context.Context, _, _ string) ([]*coreapi.HCPOpenShiftCluster, error) {
	return nil, l.err
}

// errorReadDesireLister always returns the configured error.
type errorReadDesireLister struct {
	err error
}

func (l *errorReadDesireLister) List(_ context.Context) ([]*kubeapplierapi.ReadDesire, error) {
	return nil, l.err
}
func (l *errorReadDesireLister) GetForCluster(_ context.Context, _, _, _, _ string) (*kubeapplierapi.ReadDesire, error) {
	return nil, l.err
}
func (l *errorReadDesireLister) GetForNodePool(_ context.Context, _, _, _, _, _ string) (*kubeapplierapi.ReadDesire, error) {
	return nil, l.err
}
func (l *errorReadDesireLister) GetForSystemAdminCredentialRequest(_ context.Context, _, _, _, _, _ string) (*kubeapplierapi.ReadDesire, error) {
	return nil, l.err
}
func (l *errorReadDesireLister) GetForSystemAdminCredentialRevocation(_ context.Context, _, _, _, _, _ string) (*kubeapplierapi.ReadDesire, error) {
	return nil, l.err
}
func (l *errorReadDesireLister) GetForManagementCluster(_ context.Context, _, _ string) (*kubeapplierapi.ReadDesire, error) {
	return nil, l.err
}
func (l *errorReadDesireLister) GetByResourceID(_ context.Context, _ string) (*kubeapplierapi.ReadDesire, error) {
	return nil, l.err
}
func (l *errorReadDesireLister) ListForManagementCluster(_ context.Context, _ *azcorearm.ResourceID) ([]*kubeapplierapi.ReadDesire, error) {
	return nil, l.err
}
func (l *errorReadDesireLister) ListForCluster(_ context.Context, _, _, _ string) ([]*kubeapplierapi.ReadDesire, error) {
	return nil, l.err
}
func (l *errorReadDesireLister) ListForNodePool(_ context.Context, _, _, _, _ string) ([]*kubeapplierapi.ReadDesire, error) {
	return nil, l.err
}

func newClusterWithAPIURL(url string, createdAt *time.Time) *coreapi.HCPOpenShiftCluster {
	fixture := operationtesting.NewClusterTestFixture()
	cluster := fixture.NewCluster(createdAt)
	cluster.ServiceProviderProperties.API = coreapi.ServiceProviderAPIProfile{URL: url}
	return cluster
}

func TestDetermineOperationState(t *testing.T) {
	fixture := operationtesting.NewClusterTestFixture()
	operation := fixture.NewOperation(cosmosstorageutils.OperationRequestCreate)
	cluster := newClusterWithAPIURL("https://api.example.com", nil)

	readyClusterServiceMock := func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
		mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
		clusterStatus, err := arohcpv1alpha1.NewClusterStatus().
			State(arohcpv1alpha1.ClusterStateReady).
			Build()
		require.NoError(t, err)
		mockCSClient.EXPECT().
			GetClusterStatus(gomock.Any(), fixture.ClusterInternalID).
			Return(clusterStatus, nil).
			AnyTimes()
		return mockCSClient
	}

	tests := []struct {
		name              string
		clusterLister     corelisters.ClusterLister
		readDesireLister  kubeapplierlisters.ReadDesireLister
		setupCSMock       func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec
		clusterOverride   *coreapi.HCPOpenShiftCluster
		expectedState     coreapi.ProvisioningState
		wantMessageSubstr string
		expectError       bool
		errContains       string
	}{
		{
			name: "both checks succeed → Succeeded",
			clusterLister: &corelistertesting.SliceClusterLister{
				Clusters: []*coreapi.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com", nil)},
			},
			setupCSMock: readyClusterServiceMock,
			readDesireLister: &kubeapplierlistertesting.SliceReadDesireLister{
				Desires: []*kubeapplierapi.ReadDesire{
					operationtesting.NewHostedClusterReadDesire(t, &v1beta1.HostedCluster{
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
			expectedState:     coreapi.ProvisioningStateSucceeded,
			wantMessageSubstr: "",
		},
		{
			name: "cluster API URL empty → Provisioning (lowest priority wins)",
			clusterLister: &corelistertesting.SliceClusterLister{
				Clusters: []*coreapi.HCPOpenShiftCluster{newClusterWithAPIURL("", nil)},
			},
			setupCSMock: readyClusterServiceMock,
			readDesireLister: &kubeapplierlistertesting.SliceReadDesireLister{
				Desires: []*kubeapplierapi.ReadDesire{
					operationtesting.NewHostedClusterReadDesire(t, &v1beta1.HostedCluster{
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
			expectedState:     coreapi.ProvisioningStateProvisioning,
			wantMessageSubstr: ".api.url is empty",
		},
		{
			name: "hosted cluster not found → Provisioning",
			clusterLister: &corelistertesting.SliceClusterLister{
				Clusters: []*coreapi.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com", nil)},
			},
			setupCSMock:      readyClusterServiceMock,
			readDesireLister: &kubeapplierlistertesting.SliceReadDesireLister{},
			expectedState:    coreapi.ProvisioningStateProvisioning,
		},
		{
			name:          "cluster lister error → error propagated",
			clusterLister: &errorClusterLister{err: fmt.Errorf("cosmos error")},
			setupCSMock:   readyClusterServiceMock,
			readDesireLister: &kubeapplierlistertesting.SliceReadDesireLister{
				Desires: []*kubeapplierapi.ReadDesire{
					operationtesting.NewHostedClusterReadDesire(t, &v1beta1.HostedCluster{
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
			clusterLister: &corelistertesting.SliceClusterLister{
				Clusters: []*coreapi.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com", nil)},
			},
			setupCSMock:      readyClusterServiceMock,
			readDesireLister: &errorReadDesireLister{err: fmt.Errorf("maestro error")},
			expectError:      true,
			errContains:      "maestro error",
		},
		{
			name:             "both errors → joined error",
			clusterLister:    &errorClusterLister{err: fmt.Errorf("cluster error")},
			setupCSMock:      readyClusterServiceMock,
			readDesireLister: &errorReadDesireLister{err: fmt.Errorf("content error")},
			expectError:      true,
			errContains:      "cluster error",
		},
		{
			name: "read desire not yet successful → Provisioning",
			clusterLister: &corelistertesting.SliceClusterLister{
				Clusters: []*coreapi.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com", nil)},
			},
			setupCSMock: readyClusterServiceMock,
			readDesireLister: &kubeapplierlistertesting.SliceReadDesireLister{
				Desires: []*kubeapplierapi.ReadDesire{
					operationtesting.NewHostedClusterReadDesire(t, &v1beta1.HostedCluster{},
						metav1.Condition{Type: kubeapplierapi.ConditionTypeSuccessful, Status: metav1.ConditionFalse, Reason: kubeapplierapi.ConditionReasonKubeAPIError, Message: "boom"}),
				},
			},
			expectedState:     coreapi.ProvisioningStateProvisioning,
			wantMessageSubstr: "ReadDesire is not successful: KubeAPIError: boom",
		},
		{
			name: "hosted cluster not available → Provisioning",
			clusterLister: &corelistertesting.SliceClusterLister{
				Clusters: []*coreapi.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com", nil)},
			},
			setupCSMock: readyClusterServiceMock,
			readDesireLister: &kubeapplierlistertesting.SliceReadDesireLister{
				Desires: []*kubeapplierapi.ReadDesire{
					operationtesting.NewHostedClusterReadDesire(t, &v1beta1.HostedCluster{
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
			expectedState:     coreapi.ProvisioningStateProvisioning,
			wantMessageSubstr: "hosted cluster is not available: NotReady: cluster is not ready",
		},
		{
			name: "no control plane endpoint host → Provisioning",
			clusterLister: &corelistertesting.SliceClusterLister{
				Clusters: []*coreapi.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com", nil)},
			},
			setupCSMock: readyClusterServiceMock,
			readDesireLister: &kubeapplierlistertesting.SliceReadDesireLister{
				Desires: []*kubeapplierapi.ReadDesire{
					operationtesting.NewHostedClusterReadDesire(t, &v1beta1.HostedCluster{
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
			expectedState:     coreapi.ProvisioningStateProvisioning,
			wantMessageSubstr: "hosted cluster has no control plane endpoint host",
		},
		{
			name: "no control plane endpoint port → Provisioning",
			clusterLister: &corelistertesting.SliceClusterLister{
				Clusters: []*coreapi.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com", nil)},
			},
			setupCSMock: readyClusterServiceMock,
			readDesireLister: &kubeapplierlistertesting.SliceReadDesireLister{
				Desires: []*kubeapplierapi.ReadDesire{
					operationtesting.NewHostedClusterReadDesire(t, &v1beta1.HostedCluster{
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
			expectedState:     coreapi.ProvisioningStateProvisioning,
			wantMessageSubstr: "hosted cluster has no control plane endpoint port",
		},
		{
			name: "version with valid success condition but not installed → Provisioning",
			clusterLister: &corelistertesting.SliceClusterLister{
				Clusters: []*coreapi.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com", nil)},
			},
			setupCSMock: readyClusterServiceMock,
			readDesireLister: &kubeapplierlistertesting.SliceReadDesireLister{
				Desires: []*kubeapplierapi.ReadDesire{
					operationtesting.NewHostedClusterReadDesire(t, &v1beta1.HostedCluster{
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
			expectedState:     coreapi.ProvisioningStateProvisioning,
			wantMessageSubstr: "hosted cluster control plane version not yet completed: version 4.23.0 is Partial (want Completed)",
		},
		{
			name: "cluster-service succeeded but cosmos not ready → Provisioning",
			clusterLister: &corelistertesting.SliceClusterLister{
				Clusters: []*coreapi.HCPOpenShiftCluster{newClusterWithAPIURL("", nil)},
			},
			setupCSMock: readyClusterServiceMock,
			readDesireLister: &kubeapplierlistertesting.SliceReadDesireLister{
				Desires: []*kubeapplierapi.ReadDesire{
					operationtesting.NewHostedClusterReadDesire(t, &v1beta1.HostedCluster{
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
			expectedState:     coreapi.ProvisioningStateProvisioning,
			wantMessageSubstr: ".api.url is empty",
		},
		{
			name: "cluster-service still installing → Provisioning",
			clusterLister: &corelistertesting.SliceClusterLister{
				Clusters: []*coreapi.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com", nil)},
			},
			setupCSMock: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, err := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateInstalling).
					Build()
				require.NoError(t, err)
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.ClusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			readDesireLister: &kubeapplierlistertesting.SliceReadDesireLister{
				Desires: []*kubeapplierapi.ReadDesire{
					operationtesting.NewHostedClusterReadDesire(t, &v1beta1.HostedCluster{
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
			expectedState: coreapi.ProvisioningStateProvisioning,
		},
		{
			name: "cluster ClusterServiceID unset → Provisioning",
			clusterOverride: func() *coreapi.HCPOpenShiftCluster {
				c := newClusterWithAPIURL("https://api.example.com", nil)
				c.ServiceProviderProperties.ClusterServiceID = nil
				return c
			}(),
			clusterLister: &corelistertesting.SliceClusterLister{
				Clusters: []*coreapi.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com", nil)},
			},
			// ClusterServiceID is nil, so clusterServiceCreateOperationState returns
			// early without calling GetClusterStatus; use a bare mock with no
			// expectations to avoid an unmet-expectation failure.
			setupCSMock: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				return ocm.NewMockClusterServiceClientSpec(ctrl)
			},
			readDesireLister: &kubeapplierlistertesting.SliceReadDesireLister{
				Desires: []*kubeapplierapi.ReadDesire{
					operationtesting.NewHostedClusterReadDesire(t, &v1beta1.HostedCluster{
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
			expectedState:     coreapi.ProvisioningStateProvisioning,
			wantMessageSubstr: "cluster service has not been successfully created",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			ctrl := gomock.NewController(t)

			setupCSMock := tt.setupCSMock
			if setupCSMock == nil {
				setupCSMock = readyClusterServiceMock
			}

			controller := &operationClusterCreate{
				clusterLister:        tt.clusterLister,
				readDesireLister:     tt.readDesireLister,
				clusterServiceClient: setupCSMock(ctrl),
				serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{
					ServiceProviderClusters: []*coreapi.ServiceProviderCluster{
						{
							CosmosMetadata: coreapi.CosmosMetadata{
								ResourceID: metadataapi.Must(azcorearm.ParseResourceID(
									fixture.ClusterResourceID.String() + "/" +
										coreapi.ServiceProviderClusterResourceTypeName + "/" +
										coreapi.ServiceProviderClusterResourceName)),
							},
							Status: coreapi.ServiceProviderClusterStatus{
								ServingCABundle: "fake-ca-data",
								AzureResources: coreapi.AzureResources{
									RoleAssignments: coreapi.AzureMultiReference{
										AzureResources: []*azcorearm.ResourceID{
											metadataapi.Must(azcorearm.ParseResourceID(
												"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/managed-rg/providers/Microsoft.Authorization/roleAssignments/11111111-1111-1111-1111-111111111111")),
										},
									},
								},
							},
						},
					},
				},
			}

			clusterArg := cluster
			if tt.clusterOverride != nil {
				clusterArg = tt.clusterOverride
			}
			result, err := controller.determineOperationState(ctx, operation, clusterArg)

			if tt.expectError {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, tt.expectedState, result.ProvisioningState)
			if tt.wantMessageSubstr != "" {
				assert.Contains(t, result.Message, tt.wantMessageSubstr)
			}
		})
	}
}

func TestServingCABundleOperationStatus(t *testing.T) {
	fixture := operationtesting.NewClusterTestFixture()
	operation := fixture.NewOperation(cosmosstorageutils.OperationRequestCreate)

	spcLister := func(bundle string) corelisters.ServiceProviderClusterLister {
		return &corelistertesting.SliceServiceProviderClusterLister{
			ServiceProviderClusters: []*coreapi.ServiceProviderCluster{
				{
					CosmosMetadata: coreapi.CosmosMetadata{
						ResourceID: metadataapi.Must(azcorearm.ParseResourceID(
							fixture.ClusterResourceID.String() + "/" +
								coreapi.ServiceProviderClusterResourceTypeName + "/" +
								coreapi.ServiceProviderClusterResourceName)),
					},
					Status: coreapi.ServiceProviderClusterStatus{
						ServingCABundle: bundle,
					},
				},
			},
		}
	}

	tests := []struct {
		name          string
		spcLister     corelisters.ServiceProviderClusterLister
		expectedState coreapi.ProvisioningState
		wantMsgSubstr string
	}{
		{
			// The serving CA check is no longer gated on the cluster version:
			// once the bundle is populated the check succeeds.
			name:          "populated bundle → Succeeded",
			spcLister:     spcLister("fake-ca-data"),
			expectedState: coreapi.ProvisioningStateSucceeded,
		},
		{
			name:          "empty bundle → Provisioning",
			spcLister:     spcLister(""),
			expectedState: coreapi.ProvisioningStateProvisioning,
			wantMsgSubstr: "ServingCABundle not yet populated",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			controller := &operationClusterCreate{
				serviceProviderClusterLister: tt.spcLister,
			}

			result, err := controller.servingCABundleOperationStatus(ctx, operation)
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, tt.expectedState, result.ProvisioningState)
			if tt.wantMsgSubstr != "" {
				assert.Contains(t, result.Message, tt.wantMsgSubstr)
			}
		})
	}
}

func TestRoleAssignmentsOperationStatus(t *testing.T) {
	fixture := operationtesting.NewClusterTestFixture()
	operation := fixture.NewOperation(cosmosstorageutils.OperationRequestCreate)

	roleAssignmentID := func(name string) *azcorearm.ResourceID {
		return metadataapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/managed-rg/providers/Microsoft.Authorization/roleAssignments/" + name))
	}

	spcLister := func(ra coreapi.AzureMultiReference) corelisters.ServiceProviderClusterLister {
		return &corelistertesting.SliceServiceProviderClusterLister{
			ServiceProviderClusters: []*coreapi.ServiceProviderCluster{
				{
					CosmosMetadata: coreapi.CosmosMetadata{
						ResourceID: metadataapi.Must(azcorearm.ParseResourceID(
							fixture.ClusterResourceID.String() + "/" +
								coreapi.ServiceProviderClusterResourceTypeName + "/" +
								coreapi.ServiceProviderClusterResourceName)),
					},
					Status: coreapi.ServiceProviderClusterStatus{
						AzureResources: coreapi.AzureResources{
							RoleAssignments: ra,
						},
					},
				},
			},
		}
	}

	tests := []struct {
		name            string
		roleAssignments coreapi.AzureMultiReference
		expectedState   coreapi.ProvisioningState
		wantMsgSubstr   string
	}{
		{
			name: "all confirmed and none pending → Succeeded",
			roleAssignments: coreapi.AzureMultiReference{
				AzureResources: []*azcorearm.ResourceID{roleAssignmentID("11111111-1111-1111-1111-111111111111")},
			},
			expectedState: coreapi.ProvisioningStateSucceeded,
		},
		{
			name:            "none confirmed → Provisioning",
			roleAssignments: coreapi.AzureMultiReference{},
			expectedState:   coreapi.ProvisioningStateProvisioning,
			wantMsgSubstr:   "role assignments not yet confirmed",
		},
		{
			name: "some still pending → Provisioning",
			roleAssignments: coreapi.AzureMultiReference{
				AzureResources:        []*azcorearm.ResourceID{roleAssignmentID("11111111-1111-1111-1111-111111111111")},
				PendingAzureResources: []*azcorearm.ResourceID{roleAssignmentID("22222222-2222-2222-2222-222222222222")},
			},
			expectedState: coreapi.ProvisioningStateProvisioning,
			wantMsgSubstr: "role assignments not yet confirmed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			controller := &operationClusterCreate{
				serviceProviderClusterLister: spcLister(tt.roleAssignments),
			}

			result, err := controller.roleAssignmentsOperationStatus(ctx, operation)
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, tt.expectedState, result.ProvisioningState)
			if tt.wantMsgSubstr != "" {
				assert.Contains(t, result.Message, tt.wantMsgSubstr)
			}
		})
	}
}

func TestDescribeVersionHistory(t *testing.T) {
	tests := []struct {
		name     string
		history  []v1beta1.ControlPlaneUpdateHistory
		expected string
	}{
		{
			name:     "empty history",
			history:  nil,
			expected: "hosted cluster has no version history entries",
		},
		{
			name: "single version partial",
			history: []v1beta1.ControlPlaneUpdateHistory{
				{Version: "4.23.0", State: configv1.PartialUpdate},
			},
			expected: "hosted cluster control plane version not yet completed: version 4.23.0 is Partial (want Completed)",
		},
		{
			name: "multiple versions all partial",
			history: []v1beta1.ControlPlaneUpdateHistory{
				{Version: "4.23.0", State: configv1.PartialUpdate},
				{Version: "4.22.5", State: configv1.PartialUpdate},
			},
			expected: "hosted cluster control plane version not yet completed: version 4.23.0 is Partial (want Completed); version 4.22.5 is Partial (want Completed)",
		},
		{
			name: "completed version does not show elapsed duration",
			history: []v1beta1.ControlPlaneUpdateHistory{
				{
					Version:     "4.23.0",
					State:       configv1.CompletedUpdate,
					StartedTime: metav1.NewTime(time.Now().Add(-5 * time.Minute)),
				},
			},
			expected: "hosted cluster control plane version not yet completed: version 4.23.0 is Completed (want Completed)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := describeVersionHistory(tt.history)
			assert.Equal(t, tt.expected, result)
		})
	}

	t.Run("version with started time shows elapsed duration", func(t *testing.T) {
		history := []v1beta1.ControlPlaneUpdateHistory{
			{
				Version:     "4.23.0",
				State:       configv1.PartialUpdate,
				StartedTime: metav1.NewTime(time.Now().Add(-5 * time.Minute)),
			},
		}
		result := describeVersionHistory(history)
		assert.Regexp(t, `^hosted cluster control plane version not yet completed: version 4\.23\.0 is Partial \(want Completed\), started \d+m\d+s ago$`, result)
	})

	t.Run("future started time clamps elapsed to zero", func(t *testing.T) {
		history := []v1beta1.ControlPlaneUpdateHistory{
			{
				Version:     "4.23.0",
				State:       configv1.PartialUpdate,
				StartedTime: metav1.NewTime(time.Now().Add(5 * time.Minute)),
			},
		}
		result := describeVersionHistory(history)
		assert.Equal(t, "hosted cluster control plane version not yet completed: version 4.23.0 is Partial (want Completed), started 0s ago", result)
	})
}