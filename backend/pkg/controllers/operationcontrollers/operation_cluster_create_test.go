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
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
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
	fixture := newClusterTestFixture()

	succeededDesire := func(t *testing.T) *kubeapplier.ReadDesire {
		return newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
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
		existingCluster   *api.HCPOpenShiftCluster
		existingOperation *api.Operation
		readDesireLister  dblisters.ReadDesireLister
		setupCSMock       func(ctrl *gomock.Controller, fixture *clusterTestFixture) ocm.ClusterServiceClientSpec
		wantErr           bool
		verifyDB          func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
	}{
		{
			name:              "successful create updates operation to succeeded",
			existingCluster:   newClusterWithAPIURL("https://api.example.com", &createdAt),
			existingOperation: fixture.newOperation(database.OperationRequestCreate),
			setupCSMock: func(ctrl *gomock.Controller, fixture *clusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, err := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateReady).
					Build()
				require.NoError(t, err)
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.clusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)
			},
		},
		{
			name:              "non-terminal cluster state updates to provisioning",
			existingCluster:   newClusterWithAPIURL("https://api.example.com", nil),
			existingOperation: fixture.newOperation(database.OperationRequestCreate),
			setupCSMock: func(ctrl *gomock.Controller, fixture *clusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, err := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateInstalling).
					Build()
				require.NoError(t, err)
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.clusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateProvisioning, op.Status)
			},
		},
		{
			name:            "polls cluster service when operation InternalID is empty",
			existingCluster: newClusterWithAPIURL("https://api.example.com", &createdAt),
			existingOperation: func() *api.Operation {
				op := fixture.newOperation(database.OperationRequestCreate)
				op.InternalID = api.InternalID{}
				return op
			}(),
			setupCSMock: func(ctrl *gomock.Controller, fixture *clusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, err := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateReady).
					Build()
				require.NoError(t, err)
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.clusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)
			},
		},
		{
			name: "waits when cluster ClusterServiceID is unset",
			existingCluster: func() *api.HCPOpenShiftCluster {
				cluster := newClusterWithAPIURL("https://api.example.com", &createdAt)
				cluster.ServiceProviderProperties.ClusterServiceID = nil
				return cluster
			}(),
			existingOperation: fixture.newOperation(database.OperationRequestCreate),
			setupCSMock: func(ctrl *gomock.Controller, _ *clusterTestFixture) ocm.ClusterServiceClientSpec {
				return ocm.NewMockClusterServiceClientSpec(ctrl)
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name: "returns early when cluster active operation id mismatches",
			existingCluster: func() *api.HCPOpenShiftCluster {
				cluster := newClusterWithAPIURL("https://api.example.com", &createdAt)
				cluster.ServiceProviderProperties.ActiveOperationID = "other-operation"
				return cluster
			}(),
			existingOperation: fixture.newOperation(database.OperationRequestCreate),
			setupCSMock: func(ctrl *gomock.Controller, _ *clusterTestFixture) ocm.ClusterServiceClientSpec {
				return ocm.NewMockClusterServiceClientSpec(ctrl)
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:  "deadline exceeded marks operation as failed",
			clock: clocktesting.NewFakePassiveClock(mustParseTime("2025-01-15T12:00:00Z")),
			existingCluster: func() *api.HCPOpenShiftCluster {
				cluster := newClusterWithAPIURL("https://api.example.com", nil)
				deadline := metav1.NewTime(mustParseTime("2025-01-15T11:30:00Z"))
				cluster.ServiceProviderProperties.CreateOperationCompletionDeadline = &deadline
				return cluster
			}(),
			existingOperation: fixture.newOperation(database.OperationRequestCreate),
			setupCSMock: func(ctrl *gomock.Controller, fixture *clusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, err := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateInstalling).
					Build()
				require.NoError(t, err)
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.clusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, op.Status)
				require.NotNil(t, op.Error)
				assert.Equal(t, arm.CloudErrorCodeInternalServerError, op.Error.Code)
			},
		},
		{
			name:  "deadline exceeded with CS succeeded but cosmos provisioning marks as failed",
			clock: clocktesting.NewFakePassiveClock(mustParseTime("2025-01-15T12:00:00Z")),
			existingCluster: func() *api.HCPOpenShiftCluster {
				cluster := newClusterWithAPIURL("https://api.example.com", &createdAt)
				deadline := metav1.NewTime(mustParseTime("2025-01-15T11:30:00Z"))
				cluster.ServiceProviderProperties.CreateOperationCompletionDeadline = &deadline
				return cluster
			}(),
			existingOperation: fixture.newOperation(database.OperationRequestCreate),
			readDesireLister:  &internallistertesting.SliceReadDesireLister{},
			setupCSMock: func(ctrl *gomock.Controller, fixture *clusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, err := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateReady).
					Build()
				require.NoError(t, err)
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.clusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, op.Status)
				require.NotNil(t, op.Error)
				assert.Equal(t, arm.CloudErrorCodeInternalServerError, op.Error.Code)
			},
		},
		{
			name:  "deadline not yet exceeded continues with provisioning",
			clock: clocktesting.NewFakePassiveClock(mustParseTime("2025-01-15T11:00:00Z")),
			existingCluster: func() *api.HCPOpenShiftCluster {
				cluster := newClusterWithAPIURL("https://api.example.com", nil)
				deadline := metav1.NewTime(mustParseTime("2025-01-15T11:30:00Z"))
				cluster.ServiceProviderProperties.CreateOperationCompletionDeadline = &deadline
				return cluster
			}(),
			existingOperation: fixture.newOperation(database.OperationRequestCreate),
			setupCSMock: func(ctrl *gomock.Controller, fixture *clusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, err := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateInstalling).
					Build()
				require.NoError(t, err)
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.clusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateProvisioning, op.Status)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{tc.existingCluster, tc.existingOperation})
			require.NoError(t, err)

			listerOperation, err := mockResourcesDBClient.Operations(testSubscriptionID).Get(ctx, testOperationName)
			require.NoError(t, err)

			mockCSClient := tc.setupCSMock(ctrl, fixture)

			testClock := tc.clock
			if testClock == nil {
				testClock = utilsclock.RealClock{}
			}
			controller := &operationClusterCreate{
				clock: testClock,
				activeOperationLister: &listertesting.SliceActiveOperationLister{
					Operations: []*api.Operation{listerOperation},
				},
				resourcesDBClient:    mockResourcesDBClient,
				clusterServiceClient: mockCSClient,
				notificationClient:   nil,
				clusterLister: &listertesting.SliceClusterLister{
					Clusters: []*api.HCPOpenShiftCluster{tc.existingCluster},
				},
				serviceProviderClusterLister: &listertesting.SliceServiceProviderClusterLister{
					ServiceProviderClusters: []*api.ServiceProviderCluster{
						{
							CosmosMetadata: api.CosmosMetadata{
								ResourceID: api.Must(azcorearm.ParseResourceID(
									fixture.clusterResourceID.String() + "/" +
										api.ServiceProviderClusterResourceTypeName + "/" +
										api.ServiceProviderClusterResourceName)),
							},
							Status: api.ServiceProviderClusterStatus{
								ServingCABundle: "fake-ca-data",
							},
						},
					},
				},
				readDesireLister: func() dblisters.ReadDesireLister {
					if tc.readDesireLister != nil {
						return tc.readDesireLister
					}
					return &internallistertesting.SliceReadDesireLister{
						Desires: []*kubeapplier.ReadDesire{succeededDesire(t)},
					}
				}(),
			}

			err = controller.SynchronizeOperation(ctx, fixture.operationKey())
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
			testSubscriptionID, testResourceGroupName, testClusterName, kubeapplierhelpers.ReadDesireNameReadonlyHostedCluster)))

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

func newClusterWithAPIURL(url string, createdAt *time.Time) *api.HCPOpenShiftCluster {
	fixture := newClusterTestFixture()
	cluster := fixture.newCluster(createdAt)
	cluster.ServiceProviderProperties.API = api.ServiceProviderAPIProfile{URL: url}
	return cluster
}

func TestDetermineOperationStatus(t *testing.T) {
	fixture := newClusterTestFixture()
	operation := fixture.newOperation(database.OperationRequestCreate)

	tests := []struct {
		name              string
		clusterLister     listers.ClusterLister
		readDesireLister  dblisters.ReadDesireLister
		expectedState     arm.ProvisioningState
		wantMessageSubstr string
		expectError       bool
		errContains       string
	}{
		{
			name: "both checks succeed → Succeeded",
			clusterLister: &listertesting.SliceClusterLister{
				Clusters: []*api.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com", nil)},
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
			expectedState:     arm.ProvisioningStateSucceeded,
			wantMessageSubstr: "",
		},
		{
			name: "cluster API URL empty → Provisioning (lowest priority wins)",
			clusterLister: &listertesting.SliceClusterLister{
				Clusters: []*api.HCPOpenShiftCluster{newClusterWithAPIURL("", nil)},
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
			expectedState:     arm.ProvisioningStateProvisioning,
			wantMessageSubstr: ".api.url is empty",
		},
		{
			name: "hosted cluster not found → Provisioning",
			clusterLister: &listertesting.SliceClusterLister{
				Clusters: []*api.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com", nil)},
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
				Clusters: []*api.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com", nil)},
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
				Clusters: []*api.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com", nil)},
			},
			readDesireLister: &internallistertesting.SliceReadDesireLister{
				Desires: []*kubeapplier.ReadDesire{
					newHostedClusterReadDesire(t, &v1beta1.HostedCluster{},
						metav1.Condition{Type: kubeapplier.ConditionTypeSuccessful, Status: metav1.ConditionFalse, Reason: kubeapplier.ConditionReasonKubeAPIError, Message: "boom"}),
				},
			},
			expectedState:     arm.ProvisioningStateProvisioning,
			wantMessageSubstr: "ReadDesire is not successful: KubeAPIError: boom",
		},
		{
			name: "hosted cluster not available → Provisioning",
			clusterLister: &listertesting.SliceClusterLister{
				Clusters: []*api.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com", nil)},
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
			expectedState:     arm.ProvisioningStateProvisioning,
			wantMessageSubstr: "hosted cluster is not available: NotReady: cluster is not ready",
		},
		{
			name: "no control plane endpoint host → Provisioning",
			clusterLister: &listertesting.SliceClusterLister{
				Clusters: []*api.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com", nil)},
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
			expectedState:     arm.ProvisioningStateProvisioning,
			wantMessageSubstr: "hosted cluster has no control plane endpoint host",
		},
		{
			name: "no control plane endpoint port → Provisioning",
			clusterLister: &listertesting.SliceClusterLister{
				Clusters: []*api.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com", nil)},
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
			expectedState:     arm.ProvisioningStateProvisioning,
			wantMessageSubstr: "hosted cluster has no control plane endpoint port",
		},
		{
			name: "version with valid success condition but not installed → Provisioning",
			clusterLister: &listertesting.SliceClusterLister{
				Clusters: []*api.HCPOpenShiftCluster{newClusterWithAPIURL("https://api.example.com", nil)},
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
			expectedState:     arm.ProvisioningStateProvisioning,
			wantMessageSubstr: "hosted cluster has no installed version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			controller := &operationClusterCreate{
				clusterLister:    tt.clusterLister,
				readDesireLister: tt.readDesireLister,
				serviceProviderClusterLister: &listertesting.SliceServiceProviderClusterLister{
					ServiceProviderClusters: []*api.ServiceProviderCluster{
						{
							CosmosMetadata: api.CosmosMetadata{
								ResourceID: api.Must(azcorearm.ParseResourceID(
									fixture.clusterResourceID.String() + "/" +
										api.ServiceProviderClusterResourceTypeName + "/" +
										api.ServiceProviderClusterResourceName)),
							},
							Status: api.ServiceProviderClusterStatus{
								ServingCABundle: "fake-ca-data",
							},
						},
					},
				},
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
			assert.Equal(t, tt.expectedState, result.ProvisioningState)
			if tt.wantMessageSubstr != "" {
				assert.Contains(t, result.Message, tt.wantMessageSubstr)
			}
		})
	}
}
