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
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/json"
	clocktesting "k8s.io/utils/clock/testing"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	operationtesting "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils/operationtesting"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// Drives the full node pool create sync path with fail fast enabled: the Hypershift
// mirror, Cluster Service status, merge, persisted error and log line. Both syncs
// share one controller, because the first seen time lives in the controller.
func TestOperationNodePoolCreate_QuotaFailFastSyncPath(t *testing.T) {
	const clusterServiceFailure = "cluster service internal failure text"

	tests := []struct {
		name string
		// csStates are the Cluster Service node pool states reported by each sync, in order.
		csStates []string
		// csMessage is reported with the last state.
		csMessage string
		// pastDeadline gives the node pool a create deadline that has already passed. The
		// operation then fails on the first sync, without waiting for the window.
		pastDeadline bool
	}{
		{
			name:     "quota alone fails the operation after the window",
			csStates: []string{string(operationbase.NodePoolStateInstalling), string(operationbase.NodePoolStateInstalling)},
		},
		{
			// Customer errors always win the merge. The platform text stays out of the
			// persisted error and is kept in the log.
			name:      "customer error wins over a concurrent platform failure",
			csStates:  []string{string(operationbase.NodePoolStateInstalling), string(operationbase.NodePoolStateError)},
			csMessage: clusterServiceFailure,
		},
		{
			// Quota seen, then the deadline passes: the operation is attributed to the
			// customer and does not end as DeadlineExceeded.
			name:         "quota at the completion deadline fails as quota exceeded",
			csStates:     []string{string(operationbase.NodePoolStateInstalling)},
			pastDeadline: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logged []string
			ctx := utils.ContextWithLogger(context.Background(), funcr.New(func(_, args string) {
				logged = append(logged, args)
				t.Log(args)
			}, funcr.Options{}))
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			fixture := operationtesting.NewNodePoolTestFixture()
			cluster := fixture.NewCluster()
			nodePool := fixture.NewNodePool()
			nodePool.Properties.Replicas = 2
			if tt.pastDeadline {
				deadline := metav1.NewTime(operationtesting.MustParseTime("2025-01-15T11:30:00Z"))
				nodePool.ServiceProviderProperties.CreateOperationCompletionDeadline = &deadline
			}
			operation := fixture.NewOperation(cosmosstorageutils.OperationRequestCreate)

			mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, nodePool, operation})
			require.NoError(t, err)

			readDesire := newNodePoolReadDesire(t, nodePool, cluster)
			hsNodePool := &v1beta1.NodePool{}
			require.NoError(t, json.Unmarshal(readDesire.Status.KubeContent.Raw, hsNodePool))
			for i := range hsNodePool.Status.Conditions {
				if hsNodePool.Status.Conditions[i].Type == v1beta1.NodePoolAllMachinesReadyConditionType {
					hsNodePool.Status.Conditions[i].Status = corev1.ConditionFalse
					hsNodePool.Status.Conditions[i].Reason = "Failed"
					hsNodePool.Status.Conditions[i].Message = quotaCondition
				}
			}
			raw, err := json.Marshal(hsNodePool)
			require.NoError(t, err)
			readDesire.Status.KubeContent.Raw = raw

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			for i, state := range tt.csStates {
				builder := arohcpv1alpha1.NewNodePoolStatus().
					State(arohcpv1alpha1.NewNodePoolState().NodePoolStateValue(state))
				if i == len(tt.csStates)-1 && tt.csMessage != "" {
					builder = builder.Message(tt.csMessage)
				}
				status, err := builder.Build()
				require.NoError(t, err)
				mockCSClient.EXPECT().GetNodePoolStatus(gomock.Any(), fixture.NodePoolInternalID).Return(status, nil)
			}

			clk := clocktesting.NewFakePassiveClock(operationtesting.MustParseTime("2025-01-15T12:00:00Z"))
			controller := &operationNodePoolCreate{
				clock:                  clk,
				resourcesDBClient:      mockResourcesDBClient,
				activeOperationsLister: &corelistertesting.DBActiveOperationLister{ResourcesDBClient: mockResourcesDBClient},
				nodePoolLister:         &corelistertesting.DBNodePoolLister{ResourcesDBClient: mockResourcesDBClient},
				readDesireLister:       &kubeapplierlistertesting.SliceReadDesireLister{Desires: []*kubeapplierapi.ReadDesire{readDesire}},
				clusterServiceClient:   mockCSClient,
				quotaFailFast:          newQuotaFailFast(clk),
			}

			if !tt.pastDeadline {
				// First sync starts the window: the operation is still in progress.
				require.NoError(t, controller.SynchronizeOperation(ctx, fixture.OperationKey()))
				op, err := mockResourcesDBClient.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				require.Equal(t, coreapi.ProvisioningStateProvisioning, op.Status)
				require.Nil(t, op.Error)

				// The next sync happens after the window.
				clk.SetTime(clk.Now().Add(quotaFailFastWindow + time.Minute))
			}
			require.NoError(t, controller.SynchronizeOperation(ctx, fixture.OperationKey()))
			op, err := mockResourcesDBClient.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
			require.NoError(t, err)
			require.Equal(t, coreapi.ProvisioningStateFailed, op.Status)
			require.NotNil(t, op.Error, "failed operation must carry an error body")
			require.Equal(t, coreapi.CloudErrorCodeQuotaExceeded, op.Error.Code)
			require.True(t, coreapi.IsCustomerCaused(op.Error.Code))
			require.Contains(t, op.Error.Message, "enough Azure compute quota")
			require.Contains(t, op.Error.Message, "Total Regional Cores quota")
			require.Empty(t, op.Error.Details)
			for _, leak := range []string{"management.azure.com", "ERROR CODE", "[hypershiftNodePool]", "[clusterServiceNodePoolStatus]", clusterServiceFailure} {
				require.NotContains(t, op.Error.Message, leak, "customer message must stay clean")
			}

			logs := strings.Join(logged, "\n")
			if tt.pastDeadline {
				require.Contains(t, logs, "quota error present at the completion deadline")
				require.NotContains(t, op.Error.Message, "deadline", "a quota failure must not be reported as a timeout")
			} else {
				require.Contains(t, logs, "quota error persisted past the fail fast window")
			}
			require.Contains(t, logs, "node pool create operation failed")
			if tt.csMessage != "" {
				require.Contains(t, logs, tt.csMessage, "withheld platform text must be logged")
			}
		})
	}
}
