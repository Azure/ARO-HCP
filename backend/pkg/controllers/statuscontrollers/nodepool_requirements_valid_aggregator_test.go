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

package statuscontrollers

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

func newTestServiceProviderNodePoolForAggregator(opts ...func(*api.ServiceProviderNodePool)) *api.ServiceProviderNodePool {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/nodePools/" + testNodePoolName +
			"/serviceProviderNodePools/" + api.ServiceProviderNodePoolResourceName,
	))
	spnp := &api.ServiceProviderNodePool{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
	}
	for _, opt := range opts {
		opt(spnp)
	}
	return spnp
}

func TestNodePoolRequirementsValidAggregator_SyncOnce(t *testing.T) {
	failedValidation := newTestValidationCondition("AValidation", metav1.ConditionFalse, "Failed", "Validation failed: boom")
	degradedCondition := metav1.Condition{
		Type:    requirementsValidConditionType,
		Status:  metav1.ConditionFalse,
		Reason:  requirementsValidConditionReasonDegraded,
		Message: "AValidation: Validation failed: boom",
	}
	validCondition := metav1.Condition{
		Type:    requirementsValidConditionType,
		Status:  metav1.ConditionTrue,
		Reason:  requirementsValidConditionReasonValid,
		Message: "",
	}

	testCases := []struct {
		name                            string
		existingNodePool                *api.HCPOpenShiftClusterNodePool
		existingServiceProviderNodePool *api.ServiceProviderNodePool
		// wantCondition is the expected RequirementsValid condition after SyncOnce.
		// nil means the condition must remain absent. Only Type/Status/Reason/Message
		// are asserted; LastTransitionTime is not considered.
		wantCondition *metav1.Condition
	}{
		{
			name:                            "no validations writes True/Valid",
			existingNodePool:                newTestNodePoolForAggregator(),
			existingServiceProviderNodePool: newTestServiceProviderNodePoolForAggregator(),
			wantCondition:                   &validCondition,
		},
		{
			name:             "failed validation writes False/Degraded",
			existingNodePool: newTestNodePoolForAggregator(),
			existingServiceProviderNodePool: newTestServiceProviderNodePoolForAggregator(func(spnp *api.ServiceProviderNodePool) {
				spnp.Status.Validations = []metav1.Condition{failedValidation}
			}),
			wantCondition: &degradedCondition,
		},
		{
			name: "no-op when UserFacingConditions already match",
			existingNodePool: newTestNodePoolForAggregator(func(np *api.HCPOpenShiftClusterNodePool) {
				np.Status.UserFacingConditions = []metav1.Condition{degradedCondition}
			}),
			existingServiceProviderNodePool: newTestServiceProviderNodePoolForAggregator(func(spnp *api.ServiceProviderNodePool) {
				spnp.Status.Validations = []metav1.Condition{failedValidation}
			}),
			wantCondition: &degradedCondition,
		},
		{
			name:                            "missing ServiceProviderNodePool skips write",
			existingNodePool:                newTestNodePoolForAggregator(),
			existingServiceProviderNodePool: nil,
			wantCondition:                   nil,
		},
		{
			name: "deleting node pool skips write",
			existingNodePool: newTestNodePoolForAggregator(func(np *api.HCPOpenShiftClusterNodePool) {
				now := metav1.Now()
				np.ServiceProviderProperties.DeletionTimestamp = &now
			}),
			existingServiceProviderNodePool: newTestServiceProviderNodePoolForAggregator(func(spnp *api.ServiceProviderNodePool) {
				spnp.Status.Validations = []metav1.Condition{failedValidation}
			}),
			wantCondition: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			seed := []any{newTestClusterForAggregator()}
			if tc.existingNodePool != nil {
				seed = append(seed, tc.existingNodePool)
			}
			if tc.existingServiceProviderNodePool != nil {
				seed = append(seed, tc.existingServiceProviderNodePool)
			}

			mockDB, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, seed)
			require.NoError(t, err)

			syncer := &nodePoolRequirementsValidAggregator{
				nodePoolLister:                &listertesting.DBNodePoolLister{ResourcesDBClient: mockDB},
				serviceProviderNodePoolLister: &listertesting.DBServiceProviderNodePoolLister{ResourcesDBClient: mockDB},
				resourcesDBClient:             mockDB,
			}

			err = syncer.SyncOnce(ctx, controllerutils.HCPNodePoolKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    testClusterName,
				HCPNodePoolName:   testNodePoolName,
			})
			require.NoError(t, err)

			updated, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).NodePools(testClusterName).Get(ctx, testNodePoolName)
			require.NoError(t, err)

			cond := apimeta.FindStatusCondition(updated.Status.UserFacingConditions, requirementsValidConditionType)
			if tc.wantCondition == nil {
				assert.Nil(t, cond, "RequirementsValid must remain absent")
				return
			}
			require.NotNil(t, cond, "RequirementsValid must be set")
			assert.Equal(t, tc.wantCondition.Status, cond.Status, "status")
			assert.Equal(t, tc.wantCondition.Reason, cond.Reason, "reason")
			assert.Equal(t, tc.wantCondition.Message, cond.Message, "message")
		})
	}
}

func TestNodePoolRequirementsValidAggregator_NeedsWork(t *testing.T) {
	now := metav1.Now()

	tests := []struct {
		name     string
		nodePool *api.HCPOpenShiftClusterNodePool
		want     bool
	}{
		{
			name:     "proceed when not deleting",
			nodePool: newTestNodePoolForAggregator(),
			want:     true,
		},
		{
			name: "skip when deletion timestamp is set",
			nodePool: newTestNodePoolForAggregator(func(np *api.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &now
			}),
			want: false,
		},
	}

	syncer := &nodePoolRequirementsValidAggregator{}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, syncer.needsWork(tc.nodePool))
		})
	}
}
