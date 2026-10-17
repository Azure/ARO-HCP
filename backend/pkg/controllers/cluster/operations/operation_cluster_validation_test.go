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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	clustervalidation "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/validation"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	operationtesting "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils/operationtesting"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/billingcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

func TestClusterValidationOperationState(t *testing.T) {
	t.Parallel()
	now := operationtesting.MustParseTime("2026-09-23T12:00:00Z")
	condition := func(name string, status metav1.ConditionStatus, age time.Duration) metav1.Condition {
		return metav1.Condition{
			Type: name, Status: status, Reason: "ValidationResult", Message: name + " details",
			LastTransitionTime: metav1.NewTime(now.Add(-age)),
		}
	}
	tests := []struct {
		name        string
		validations []metav1.Condition
		wantState   coreapi.ProvisioningState
		wantCode    string
		wantMessage string
	}{
		{
			name: "no recorded validations", wantState: coreapi.ProvisioningStateSucceeded,
		},
		{
			name: "all validations passed",
			validations: []metav1.Condition{
				condition("Subnet", metav1.ConditionTrue, time.Hour),
				condition("Identity", metav1.ConditionTrue, time.Hour),
			},
			wantState: coreapi.ProvisioningStateSucceeded,
		},
		{
			name: "recent failure is provisioning and invalid",
			validations: []metav1.Condition{
				condition("Subnet", metav1.ConditionFalse, 4*time.Minute),
				condition("Identity", metav1.ConditionTrue, time.Hour),
			},
			wantState: coreapi.ProvisioningStateProvisioning, wantCode: coreapi.CloudErrorCodeInvalidResource,
			wantMessage: "Subnet: ValidationResult: Subnet details",
		},
		{
			name:        "validation at exactly ten minutes fails an older operation",
			validations: []metav1.Condition{condition("Subnet", metav1.ConditionFalse, 10*time.Minute)},
			wantState:   coreapi.ProvisioningStateFailed, wantCode: coreapi.CloudErrorCodeInvalidResource,
			wantMessage: "Subnet: ValidationResult: Subnet details",
		},
		{
			name: "any failure older than ten minutes fails and all failures are reported",
			validations: []metav1.Condition{
				condition("Subnet", metav1.ConditionFalse, 10*time.Minute+time.Second),
				condition("Identity", metav1.ConditionFalse, time.Minute),
				condition("Quota", metav1.ConditionTrue, time.Hour),
			},
			wantState: coreapi.ProvisioningStateFailed, wantCode: coreapi.CloudErrorCodeInvalidResource,
			wantMessage: "Identity: ValidationResult: Identity details; Subnet: ValidationResult: Subnet details",
		},
		{
			name:        "old unknown validation does not fail the operation",
			validations: []metav1.Condition{condition("Subnet", metav1.ConditionUnknown, time.Hour)},
			wantState:   coreapi.ProvisioningStateProvisioning, wantCode: coreapi.CloudErrorCodeInternalServerError,
			wantMessage: "Subnet: ValidationResult: Subnet details",
		},
		{
			name: "unknown validation does not extend a failure timeout",
			validations: []metav1.Condition{
				condition("Subnet", metav1.ConditionFalse, 11*time.Minute),
				condition("Identity", metav1.ConditionUnknown, time.Minute),
			},
			wantState: coreapi.ProvisioningStateFailed, wantCode: coreapi.CloudErrorCodeInvalidResource,
			wantMessage: "Identity: ValidationResult: Identity details; Subnet: ValidationResult: Subnet details",
		},
		{
			name:        "missing transition time does not imply an expired failure",
			validations: []metav1.Condition{{Type: "Subnet", Status: metav1.ConditionFalse, Reason: "InvalidSubnet"}},
			wantState:   coreapi.ProvisioningStateProvisioning, wantCode: coreapi.CloudErrorCodeInvalidResource,
			wantMessage: "Subnet: InvalidSubnet",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			spc := &coreapi.ServiceProviderCluster{Status: coreapi.ServiceProviderClusterStatus{Validations: tt.validations}}
			original := spc.DeepCopy()
			got := clusterValidationOperationState(spc, now.Add(-time.Hour), now)
			assert.Equal(t, tt.wantState, got.ProvisioningState)
			assert.Equal(t, tt.wantCode, got.CloudErrorCode)
			assert.Equal(t, tt.wantMessage, got.Message)
			if tt.wantState == coreapi.ProvisioningStateFailed {
				require.NotNil(t, got.Error)
				assert.Equal(t, tt.wantCode, got.Error.Code)
				assert.Equal(t, tt.wantMessage, got.Error.Message)
			} else {
				assert.Nil(t, got.Error)
			}
			assert.Equal(t, original, spc)
		})
	}
}

func TestClusterValidationOperationAge(t *testing.T) {
	t.Parallel()
	now := operationtesting.MustParseTime("2026-09-23T12:00:00Z")
	spc := &coreapi.ServiceProviderCluster{Status: coreapi.ServiceProviderClusterStatus{
		Validations: []metav1.Condition{{
			Type: "Subnet", Status: metav1.ConditionFalse, Reason: "InvalidSubnet",
			LastTransitionTime: metav1.NewTime(now.Add(-10 * time.Minute)),
		}},
	}}
	tests := []struct {
		name      string
		startTime time.Time
		wantState coreapi.ProvisioningState
	}{
		{name: "new operation", startTime: now, wantState: coreapi.ProvisioningStateProvisioning},
		{name: "operation just under 10 minutes", startTime: now.Add(-10*time.Minute + time.Second), wantState: coreapi.ProvisioningStateProvisioning},
		{name: "both clocks exactly 10 minutes", startTime: now.Add(-10 * time.Minute), wantState: coreapi.ProvisioningStateFailed},
		{name: "older operation", startTime: now.Add(-time.Hour), wantState: coreapi.ProvisioningStateFailed},
		{name: "missing operation start time", wantState: coreapi.ProvisioningStateProvisioning},
		{name: "future operation start time", startTime: now.Add(time.Minute), wantState: coreapi.ProvisioningStateProvisioning},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := clusterValidationOperationState(spc, tt.startTime, now)
			assert.Equal(t, tt.wantState, got.ProvisioningState)
			assert.Equal(t, coreapi.CloudErrorCodeInvalidResource, got.CloudErrorCode)
			assert.Equal(t, "Subnet: InvalidSubnet", got.Message)
		})
	}
}

func TestClusterValidationRecovery(t *testing.T) {
	t.Parallel()
	now := operationtesting.MustParseTime("2026-09-23T12:00:00Z")
	spc := &coreapi.ServiceProviderCluster{Status: coreapi.ServiceProviderClusterStatus{
		Validations: []metav1.Condition{{
			Type: "Subnet", Status: metav1.ConditionFalse, Reason: "InvalidSubnet",
			LastTransitionTime: metav1.NewTime(now.Add(-4 * time.Minute)),
		}},
	}}
	assert.Equal(t, coreapi.ProvisioningStateProvisioning, clusterValidationOperationState(spc, now.Add(-time.Hour), now).ProvisioningState)
	spc.Status.Validations[0].Status = metav1.ConditionTrue
	spc.Status.Validations[0].LastTransitionTime = metav1.NewTime(now)
	got := clusterValidationOperationState(spc, now.Add(-time.Hour), now.Add(2*time.Minute))
	assert.Equal(t, coreapi.ProvisioningStateSucceeded, got.ProvisioningState)
	assert.Empty(t, got.CloudErrorCode)
	assert.Empty(t, got.Message)
	// A subsequent failure gets its own ten-minute grace period.
	spc.Status.Validations[0].Status = metav1.ConditionFalse
	spc.Status.Validations[0].LastTransitionTime = metav1.NewTime(now.Add(2 * time.Minute))
	got = clusterValidationOperationState(spc, now.Add(-time.Hour), now.Add(3*time.Minute))
	assert.Equal(t, coreapi.ProvisioningStateProvisioning, got.ProvisioningState)
	assert.Equal(t, coreapi.CloudErrorCodeInvalidResource, got.CloudErrorCode)
}

func TestOperationClusterCreate_ClusterValidationNotCached(t *testing.T) {
	t.Parallel()
	c := &operationClusterCreate{serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{}}
	operation := operationtesting.NewClusterTestFixture().NewOperation(coreapi.OperationRequestCreate)
	got, err := c.clusterValidation(context.Background(), operation)
	require.NoError(t, err)
	assert.Equal(t, coreapi.ProvisioningStateAccepted, got.ProvisioningState)
	assert.Equal(t, coreapi.CloudErrorCodeInternalServerError, got.CloudErrorCode)
	assert.Equal(t, "ServiceProviderCluster not cached yet", got.Message)
}

func TestClusterValidationRefreshOperationState(t *testing.T) {
	for _, reportError := range []bool{true, false} {
		name := "log only"
		if reportError {
			name = "report error"
		}
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			fixture := operationtesting.NewClusterTestFixture()
			cluster := fixture.NewCluster(nil)
			subscriptionID := cluster.ID.SubscriptionID
			resourceGroup := cluster.ID.ResourceGroupName
			clusterName := cluster.ID.Name
			subscription := &coreapi.Subscription{
				CosmosMetadata: coreapi.CosmosMetadata{
					ResourceID:   metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + subscriptionID)),
					PartitionKey: subscriptionID,
				},
				State: coreapi.SubscriptionStateRegistered,
			}
			db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, subscription})
			require.NoError(t, err)
			_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, db, cluster.ID)
			require.NoError(t, err)
			billing := billingcosmosstoragetesting.NewMockBillingDBClient()
			informers := coreinformers.NewBackendInformers(ctx, db.ResourcesGlobalListers(), db, billing.BillingGlobalListers())
			clusterInformer, _ := informers.Clusters()
			require.NoError(t, clusterInformer.GetStore().Add(cluster))

			const validationName = "DataPlaneIdentitiesPermissionsValidation"
			validation := clustervalidation.NewMockClusterValidation(validationName)
			controller := clustervalidation.NewClusterValidationController(
				validation, db, &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: db}, informers,
			)
			key := controllerutils.HCPClusterKey{
				SubscriptionID: subscriptionID, ResourceGroupName: resourceGroup, HCPClusterName: clusterName,
			}
			spcCRUD := db.ServiceProviderClusters(subscriptionID, resourceGroup, clusterName)
			refresh := func(wantErr bool) *coreapi.ServiceProviderCluster {
				t.Helper()
				validation.WithEarliestRetryAfter(nil)
				err := controller.SyncOnce(ctx, key)
				if wantErr {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
				spc, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				return spc
			}

			validation.WithFailed("MissingRequiredPermissions", "CheckAccess denied", "Missing required permissions.")
			spc := refresh(false)
			denial := meta.FindStatusCondition(spc.Status.Validations, validationName)
			require.NotNil(t, denial)
			require.Equal(t, metav1.ConditionFalse, denial.Status)
			// Seed an aged denial without waiting for wall-clock time to pass.
			denial.LastTransitionTime = metav1.NewTime(operationtesting.MustParseTime("2026-01-01T00:00:00Z"))
			deniedAt := denial.LastTransitionTime.Time
			operationStart := deniedAt.Add(-time.Minute)
			_, err = spcCRUD.Replace(ctx, spc, nil)
			require.NoError(t, err)

			// Each reporting policy must clear False on its first inconclusive refresh.
			if reportError {
				validation.WithUnknownReportError("InternalError", "CheckAccess returned HTTP 429", "Unable to verify permissions.")
			} else {
				validation.WithUnknownLogOnly("InternalError", "CheckAccess returned HTTP 429", "Unable to verify permissions.")
			}
			spc = refresh(reportError)
			unknown := meta.FindStatusCondition(spc.Status.Validations, validationName)
			require.NotNil(t, unknown)
			require.Equal(t, metav1.ConditionUnknown, unknown.Status)
			require.True(t, unknown.LastTransitionTime.After(deniedAt))
			state := clusterValidationOperationState(spc, operationStart, unknown.LastTransitionTime.Add(11*time.Minute))
			require.Equal(t, coreapi.ProvisioningStateProvisioning, state.ProvisioningState)
			require.Equal(t, coreapi.CloudErrorCodeInternalServerError, state.CloudErrorCode)
			require.Nil(t, state.Error)

			// Suppressed repeat Unknowns cannot resurrect the original denial.
			spc = refresh(reportError)
			require.Equal(t, unknown, meta.FindStatusCondition(spc.Status.Validations, validationName))
			state = clusterValidationOperationState(spc, operationStart, unknown.LastTransitionTime.Add(time.Hour))
			require.Equal(t, coreapi.ProvisioningStateProvisioning, state.ProvisioningState)
			require.Nil(t, state.Error)

			validation.WithPassed()
			spc = refresh(false)
			passed := meta.FindStatusCondition(spc.Status.Validations, validationName)
			require.NotNil(t, passed)
			require.Equal(t, metav1.ConditionTrue, passed.Status)
			state = clusterValidationOperationState(spc, operationStart, passed.LastTransitionTime.Time)
			require.Equal(t, coreapi.ProvisioningStateSucceeded, state.ProvisioningState)
			require.Empty(t, state.CloudErrorCode)
			require.Nil(t, state.Error)

			// A fresh denial after recovery receives its own full grace period.
			validation.WithFailed("MissingRequiredPermissions", "CheckAccess denied", "Missing required permissions.")
			spc = refresh(false)
			denial = meta.FindStatusCondition(spc.Status.Validations, validationName)
			require.NotNil(t, denial)
			require.Equal(t, metav1.ConditionFalse, denial.Status)
			require.True(t, denial.LastTransitionTime.After(deniedAt))
			state = clusterValidationOperationState(spc, operationStart, denial.LastTransitionTime.Add(9*time.Minute))
			require.Equal(t, coreapi.ProvisioningStateProvisioning, state.ProvisioningState)
			require.Equal(t, coreapi.CloudErrorCodeInvalidResource, state.CloudErrorCode)
			require.Nil(t, state.Error)

			// A repeated definitive denial retains its age and fails at the grace boundary.
			spc = refresh(false)
			require.Equal(t, denial, meta.FindStatusCondition(spc.Status.Validations, validationName))
			state = clusterValidationOperationState(spc, operationStart, denial.LastTransitionTime.Add(10*time.Minute))
			require.Equal(t, coreapi.ProvisioningStateFailed, state.ProvisioningState)
			require.Equal(t, coreapi.CloudErrorCodeInvalidResource, state.CloudErrorCode)
			require.NotNil(t, state.Error)
			require.Equal(t, coreapi.CloudErrorCodeInvalidResource, state.Error.Code)
		})
	}
}
