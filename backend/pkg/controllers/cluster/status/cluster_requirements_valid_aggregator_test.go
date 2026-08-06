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

package status

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/statusutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

// newTestValidationCondition builds a validation condition with a stable
// LastTransitionTime derived from statusutils.FixedNow.
func newTestValidationCondition(name string, status metav1.ConditionStatus, reason, message string) metav1.Condition {
	return metav1.Condition{
		Type:               name,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.NewTime(statusutils.FixedNow.Add(-time.Minute)),
	}
}

func newTestServiceProviderClusterForAggregator(opts ...func(*api.ServiceProviderCluster)) *api.ServiceProviderCluster {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + statusutils.TestSubscriptionID +
			"/resourceGroups/" + statusutils.TestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + statusutils.TestClusterName +
			"/serviceProviderClusters/" + api.ServiceProviderClusterResourceName,
	))
	spc := &api.ServiceProviderCluster{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
	}
	for _, opt := range opts {
		opt(spc)
	}
	return spc
}

func TestClusterRequirementsValidAggregator_SyncOnce(t *testing.T) {
	failedValidation := newTestValidationCondition("AValidation", metav1.ConditionFalse, "Failed", "Validation failed: boom")
	degradedCondition := metav1.Condition{
		Type:    statusutils.RequirementsValidConditionType,
		Status:  metav1.ConditionFalse,
		Reason:  statusutils.RequirementsValidConditionReasonDegraded,
		Message: "AValidation: Validation failed: boom",
	}
	validCondition := metav1.Condition{
		Type:    statusutils.RequirementsValidConditionType,
		Status:  metav1.ConditionTrue,
		Reason:  statusutils.RequirementsValidConditionReasonValid,
		Message: "",
	}

	testCases := []struct {
		name                           string
		existingCluster                *api.HCPOpenShiftCluster
		existingServiceProviderCluster *api.ServiceProviderCluster
		// wantCondition is the expected RequirementsValid condition after SyncOnce.
		// nil means the condition must remain absent. Only Type/Status/Reason/Message
		// are asserted; LastTransitionTime is not considered.
		wantCondition *metav1.Condition
	}{
		{
			name:                           "no validations writes True/Valid",
			existingCluster:                newTestClusterForAggregator(),
			existingServiceProviderCluster: newTestServiceProviderClusterForAggregator(),
			wantCondition:                  &validCondition,
		},
		{
			name:            "failed validation writes False/Degraded",
			existingCluster: newTestClusterForAggregator(),
			existingServiceProviderCluster: newTestServiceProviderClusterForAggregator(func(spc *api.ServiceProviderCluster) {
				spc.Status.Validations = []metav1.Condition{failedValidation}
			}),
			wantCondition: &degradedCondition,
		},
		{
			name: "no-op when UserFacingConditions already match",
			existingCluster: newTestClusterForAggregator(func(c *api.HCPOpenShiftCluster) {
				c.Status.UserFacingConditions = []metav1.Condition{degradedCondition}
			}),
			existingServiceProviderCluster: newTestServiceProviderClusterForAggregator(func(spc *api.ServiceProviderCluster) {
				spc.Status.Validations = []metav1.Condition{failedValidation}
			}),
			wantCondition: &degradedCondition,
		},
		{
			name:                           "missing ServiceProviderCluster skips write",
			existingCluster:                newTestClusterForAggregator(),
			existingServiceProviderCluster: nil,
			wantCondition:                  nil,
		},
		{
			name: "deleting cluster skips write",
			existingCluster: newTestClusterForAggregator(func(c *api.HCPOpenShiftCluster) {
				now := metav1.Now()
				c.ServiceProviderProperties.DeletionTimestamp = &now
			}),
			existingServiceProviderCluster: newTestServiceProviderClusterForAggregator(func(spc *api.ServiceProviderCluster) {
				spc.Status.Validations = []metav1.Condition{failedValidation}
			}),
			wantCondition: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			seed := []any{}
			if tc.existingCluster != nil {
				seed = append(seed, tc.existingCluster)
			}
			if tc.existingServiceProviderCluster != nil {
				seed = append(seed, tc.existingServiceProviderCluster)
			}

			mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, seed)
			require.NoError(t, err)

			syncer := &clusterRequirementsValidAggregator{
				clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
				serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
				resourcesDBClient:            mockDB,
			}

			err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
				SubscriptionID:    statusutils.TestSubscriptionID,
				ResourceGroupName: statusutils.TestResourceGroupName,
				HCPClusterName:    statusutils.TestClusterName,
			})
			require.NoError(t, err)

			updated, err := mockDB.HCPClusters(statusutils.TestSubscriptionID, statusutils.TestResourceGroupName).Get(ctx, statusutils.TestClusterName)
			require.NoError(t, err)

			cond := apimeta.FindStatusCondition(updated.Status.UserFacingConditions, statusutils.RequirementsValidConditionType)
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

func TestClusterRequirementsValidAggregator_NeedsWork(t *testing.T) {
	now := metav1.Now()

	tests := []struct {
		name    string
		cluster *api.HCPOpenShiftCluster
		want    bool
	}{
		{
			name:    "proceed when not deleting",
			cluster: newTestClusterForAggregator(),
			want:    true,
		},
		{
			name: "skip when deletion timestamp is set",
			cluster: newTestClusterForAggregator(func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &now
			}),
			want: false,
		},
	}

	syncer := &clusterRequirementsValidAggregator{}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, syncer.needsWork(tc.cluster))
		})
	}
}
