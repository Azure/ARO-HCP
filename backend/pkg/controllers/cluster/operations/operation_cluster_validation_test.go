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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	operationtesting "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils/operationtesting"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
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
			name:        "validation at exactly five minutes fails an older operation",
			validations: []metav1.Condition{condition("Subnet", metav1.ConditionFalse, 5*time.Minute)},
			wantState:   coreapi.ProvisioningStateFailed, wantCode: coreapi.CloudErrorCodeInvalidResource,
			wantMessage: "Subnet: ValidationResult: Subnet details",
		},
		{
			name: "any failure older than five minutes fails and all failures are reported",
			validations: []metav1.Condition{
				condition("Subnet", metav1.ConditionFalse, 5*time.Minute+time.Second),
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
				condition("Subnet", metav1.ConditionFalse, 6*time.Minute),
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
			LastTransitionTime: metav1.NewTime(now.Add(-5 * time.Minute)),
		}},
	}}
	tests := []struct {
		name      string
		startTime time.Time
		wantState coreapi.ProvisioningState
	}{
		{name: "new operation", startTime: now, wantState: coreapi.ProvisioningStateProvisioning},
		{name: "operation just under five minutes", startTime: now.Add(-5*time.Minute + time.Second), wantState: coreapi.ProvisioningStateProvisioning},
		{name: "both clocks exactly five minutes", startTime: now.Add(-5 * time.Minute), wantState: coreapi.ProvisioningStateFailed},
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
	// A subsequent failure gets its own five-minute grace period.
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
