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
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	operationtesting "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils/operationtesting"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// erroringReadDesireLister fails the HostedCluster probe. The embedded interface
// is left nil: GetForCluster is the only method buildDeletionTimeoutMessage reaches.
type erroringReadDesireLister struct {
	kubeapplierlisters.ReadDesireLister
	err error
}

func (l *erroringReadDesireLister) GetForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string,
) (*kubeapplierapi.ReadDesire, error) {
	return nil, l.err
}

// erroringUntypedCRUDDBClient fails the descendant-resources probe while leaving
// every other read served by the embedded mock client.
type erroringUntypedCRUDDBClient struct {
	corecosmosstorage.ResourcesDBClient
	err error
}

func (c *erroringUntypedCRUDDBClient) UntypedCRUD(parentResourceID azcorearm.ResourceID) (cosmosstorageutils.UntypedResourceCRUD, error) {
	return nil, c.err
}

func TestFormatDeletionTimeoutMessage(t *testing.T) {
	testCases := []struct {
		name         string
		states       []*operationbase.OperationState
		failedProbes []string
		want         string
	}{
		{
			name: "no detail and no errors returns the bare deadline message",
			want: "cluster deletion did not complete before the deadline",
		},
		{
			name:   "detail is appended after the deadline message",
			states: []*operationbase.OperationState{operationbase.NewOperationState(coreapi.ProvisioningStateDeleting, "private state").WithSource("hostedCluster")},
			want:   "cluster deletion did not complete before the deadline; [hostedCluster] hosted control plane still exists",
		},
		{
			name:         "failed probes are named when there is no detail",
			failedProbes: []string{"clusterServiceStatus", "descendantResources"},
			want:         "cluster deletion did not complete before the deadline; diagnostics unavailable (clusterServiceStatus, descendantResources)",
		},
		{
			name:         "detail wins over failed probes",
			states:       []*operationbase.OperationState{operationbase.NewOperationState(coreapi.ProvisioningStateDeleting, "private resource type").WithSource("descendantResources")},
			failedProbes: []string{"hostedCluster"},
			want:         "cluster deletion did not complete before the deadline; [descendantResources] dependent resources still exist",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, formatDeletionTimeoutMessage(tc.states, tc.failedProbes))
		})
	}
}

func TestDeletionTimeoutIgnoresInternalStateDetails(t *testing.T) {
	const privateDetail = "raw-state-error internal-cluster-id https://private.example.invalid/state"
	for source, detail := range map[string]string{
		"clusterServiceDeletion": "cluster deletion is still pending",
		"clusterServiceStatus":   "cluster service cleanup is still pending",
		"descendantResources":    "dependent resources still exist",
		"hostedCluster":          "hosted control plane still exists",
		"resourceTeardown":       "cluster resource cleanup is still pending",
	} {
		t.Run(source, func(t *testing.T) {
			state := operationbase.NewOperationState(coreapi.ProvisioningStateDeleting, privateDetail).WithSource(source)
			state.Error = &coreapi.CloudErrorBody{
				Message: privateDetail,
				Details: []coreapi.CloudErrorBody{{Message: privateDetail}},
			}
			assert.Equal(t, deletionDeadlinePrefix+"; ["+source+"] "+detail, formatDeletionTimeoutMessage([]*operationbase.OperationState{state}, nil))
			state.ProvisioningState = coreapi.ProvisioningStateSucceeded
			assert.Equal(t, deletionDeadlinePrefix, formatDeletionTimeoutMessage([]*operationbase.OperationState{state}, nil))
		})
	}
	unknown := operationbase.NewOperationState(coreapi.ProvisioningStateDeleting, privateDetail).WithSource(privateDetail)
	assert.Equal(t, deletionDeadlinePrefix, formatDeletionTimeoutMessage([]*operationbase.OperationState{nil, unknown}, nil))
}

func TestDeletionTimeoutARMStatus(t *testing.T) {
	fixture := operationtesting.NewClusterTestFixture()
	probeErr := errors.New("raw-probe-error https://private.example.invalid/inventory/internal-id")
	const privateState = "private-backend-state https://private.example.invalid/cluster"

	// clusterServiceDeletionStatus cannot return an error, so the realistic
	// no-detail case is one where ClusterService is already gone (making the two
	// ClusterService probes report Succeeded with an empty message) and the
	// remaining probes fail.
	clusterWithClusterServiceGone := func() *coreapi.Cluster {
		cluster := fixture.NewCluster(nil)
		cluster.ServiceProviderProperties.ClusterServiceID = nil
		return cluster
	}

	clusterStillDeletingInClusterService := func() *coreapi.Cluster {
		cluster := fixture.NewCluster(nil)
		cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: operationtesting.MustParseTime("2025-01-20T10:00:00Z")}
		return cluster
	}

	testCases := []struct {
		name              string
		cluster           *coreapi.Cluster
		setupCSMock       func(ctrl *gomock.Controller, fixture *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec
		failDescendants   bool
		failHostedCluster bool
		failTeardown      bool
		wantContains      []string
		wantNotContains   []string
		wantLogs          []string
	}{
		{
			name:              "probe failures are reported when no probe produced a usable message",
			cluster:           clusterWithClusterServiceGone(),
			failDescendants:   true,
			failHostedCluster: true,
			wantContains: []string{
				"cluster deletion did not complete before the deadline; diagnostics unavailable (descendantResources, hostedCluster)",
			},
			// CloudErrorBody.Message is caller-visible: the raw probe errors must never reach it.
			wantLogs: []string{"errors building deletion timeout message", probeErr.Error()},
		},
		{
			name:    "stuck-resource detail wins and probe errors stay log-only",
			cluster: clusterStillDeletingInClusterService(),
			setupCSMock: func(ctrl *gomock.Controller, fixture *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.ClusterInternalID).
					Return(nil, errors.New("cluster service unavailable"))
				return mockCSClient
			},
			failHostedCluster: true,
			wantContains: []string{
				"cluster deletion did not complete before the deadline; ",
				"[clusterServiceDeletion] cluster deletion is still pending",
			},
			wantNotContains: []string{"diagnostics unavailable"},
			wantLogs:        []string{"errors building deletion timeout message", "cluster service unavailable", probeErr.Error(), fixture.ClusterInternalID.String()},
		},
		{
			name:    "backend state is logged but not exposed",
			cluster: clusterStillDeletingInClusterService(),
			setupCSMock: func(ctrl *gomock.Controller, fixture *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				status, err := arohcpv1alpha1.NewClusterStatus().State(arohcpv1alpha1.ClusterState(privateState)).Build()
				require.NoError(t, err)
				mockCSClient.EXPECT().GetClusterStatus(gomock.Any(), fixture.ClusterInternalID).Return(status, nil)
				return mockCSClient
			},
			wantContains: []string{"[clusterServiceStatus] cluster service cleanup is still pending"},
			wantLogs:     []string{privateState, fixture.ClusterInternalID.String()},
		},
		{
			name:         "resource teardown probe failure is reported and logged",
			cluster:      clusterWithClusterServiceGone(),
			failTeardown: true,
			wantContains: []string{"diagnostics unavailable (descendantResources, resourceTeardown)"},
			wantLogs:     []string{"errors building deletion timeout message", "failed to get ServiceProviderCluster", probeErr.Error()},
		},
		{
			name:            "no probe failures and no detail keeps the bare deadline message",
			cluster:         clusterWithClusterServiceGone(),
			wantContains:    []string{"cluster deletion did not complete before the deadline"},
			wantNotContains: []string{"diagnostics unavailable", ";"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var logs []string
			logger := funcr.New(func(prefix, args string) {
				logs = append(logs, args)
			}, funcr.Options{})
			ctx := utils.ContextWithLogger(context.Background(), utils.AddLoggerValues(logger, fixture.OperationKey()))
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			deadline := metav1.NewTime(time.Unix(100, 0))
			tc.cluster.ServiceProviderProperties.DeleteOperationCompletionDeadline = &deadline
			operation := fixture.NewOperation(cosmosstorageutils.OperationRequestDelete)
			operation.Error = &coreapi.CloudErrorBody{
				Message: "stale-internal-error",
				Details: []coreapi.CloudErrorBody{{Message: "stale-internal-detail https://private.example.invalid/previous"}},
			}
			mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{tc.cluster, operation})
			require.NoError(t, err)

			var resourcesDBClient corecosmosstorage.ResourcesDBClient = mockResourcesDBClient
			if tc.failDescendants {
				resourcesDBClient = &erroringUntypedCRUDDBClient{ResourcesDBClient: mockResourcesDBClient, err: probeErr}
			}
			if tc.failTeardown {
				resourcesDBClient = diagnosticResources{ResourcesDBClient: resourcesDBClient, spcErr: probeErr}
			}

			var readDesireLister kubeapplierlisters.ReadDesireLister = &kubeapplierlistertesting.SliceReadDesireLister{}
			if tc.failHostedCluster {
				readDesireLister = &erroringReadDesireLister{err: probeErr}
			}

			var mockCSClient ocm.ClusterServiceClientSpec
			if tc.setupCSMock != nil {
				mockCSClient = tc.setupCSMock(ctrl, fixture)
			}

			controller := &operationClusterDelete{
				clock:                clocktesting.NewFakePassiveClock(deadline.Add(time.Second)),
				resourcesDBClient:    resourcesDBClient,
				readDesireLister:     readDesireLister,
				clusterServiceClient: mockCSClient,
			}

			require.NoError(t, controller.SynchronizeOperation(ctx, fixture.OperationKey()))
			updated, err := mockResourcesDBClient.Operations(tc.cluster.ID.SubscriptionID).Get(ctx, operationtesting.TestOperationName)
			require.NoError(t, err)
			require.Equal(t, coreapi.ProvisioningStateFailed, updated.Status)
			require.NotNil(t, updated.Error)
			assert.Equal(t, coreapi.CloudErrorCodeDeadlineExceeded, updated.Error.Code)
			assert.Empty(t, updated.Error.Details)
			response, err := json.Marshal(cosmosstorageutils.ToStatus(updated))
			require.NoError(t, err)
			for _, marker := range []string{probeErr.Error(), "raw-probe-error", "private.example.invalid", "internal-id", privateState, fixture.ClusterInternalID.String(), "cluster service unavailable", "stale-internal-error", "stale-internal-detail"} {
				assert.NotContains(t, string(response), marker)
			}
			for _, want := range tc.wantLogs {
				assert.Contains(t, strings.Join(logs, "\n"), want)
			}
			assert.Contains(t, strings.Join(logs, "\n"), "deletion timeout diagnostics")
			persistedCluster, err := mockResourcesDBClient.HCPClusters(tc.cluster.ID.SubscriptionID, tc.cluster.ID.ResourceGroupName).Get(ctx, tc.cluster.ID.Name)
			require.NoError(t, err)
			expectedCluster := tc.cluster.DeepCopy()
			expectedCluster.ServiceProviderProperties.ProvisioningState = coreapi.ProvisioningStateFailed
			expectedCluster.ServiceProviderProperties.ActiveOperationID = ""
			expectedProperties, err := json.Marshal(expectedCluster.ServiceProviderProperties)
			require.NoError(t, err)
			persistedProperties, err := json.Marshal(persistedCluster.ServiceProviderProperties)
			require.NoError(t, err)
			assert.JSONEq(t, string(expectedProperties), string(persistedProperties))
			message := updated.Error.Message

			assert.True(t, strings.HasPrefix(message, deletionDeadlinePrefix),
				"message must start with the deadline prefix, got %q", message)
			for _, want := range tc.wantContains {
				assert.Contains(t, message, want)
			}
			for _, notWant := range tc.wantNotContains {
				assert.NotContains(t, message, notWant)
			}
		})
	}
}
