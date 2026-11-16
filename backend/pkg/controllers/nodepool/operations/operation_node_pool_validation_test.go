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
	clocktesting "k8s.io/utils/clock/testing"

	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils/operationtesting"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/validationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

func TestNodePoolValidationOperationState(t *testing.T) {
	t.Parallel()
	now := time.Now()

	condition := func(status metav1.ConditionStatus, age time.Duration) metav1.Condition {
		return metav1.Condition{
			Type: "DiskEncryptionSet", Status: status, Reason: "MissingReaderRoleOnDiskEncryptionSet",
			Message: "identity missing Reader", LastTransitionTime: metav1.NewTime(now.Add(-age)),
		}
	}

	tests := []struct {
		name         string
		validations  []metav1.Condition
		operationAge time.Duration
		wantState    coreapi.ProvisioningState
		wantCode     string
		msgContains  string
	}{
		{
			name:         "no validations is succeeded",
			operationAge: time.Hour,
			wantState:    coreapi.ProvisioningStateSucceeded,
		},
		{
			name:         "all passing is succeeded",
			validations:  []metav1.Condition{condition(metav1.ConditionTrue, time.Hour)},
			operationAge: time.Hour,
			wantState:    coreapi.ProvisioningStateSucceeded,
		},
		{
			name:         "failing within timeout stays provisioning with message and code",
			validations:  []metav1.Condition{condition(metav1.ConditionFalse, 5*time.Minute)},
			operationAge: time.Hour,
			wantState:    coreapi.ProvisioningStateProvisioning,
			wantCode:     coreapi.CloudErrorCodeInvalidResource,
			msgContains:  "identity missing Reader",
		},
		{
			name:         "failing past timeout flips to failed",
			validations:  []metav1.Condition{condition(metav1.ConditionFalse, nodePoolValidationFailureTimeout+time.Minute)},
			operationAge: time.Hour,
			wantState:    coreapi.ProvisioningStateFailed,
			wantCode:     coreapi.CloudErrorCodeInvalidResource,
		},
		{
			name:         "failing past validation age but operation too young stays provisioning",
			validations:  []metav1.Condition{condition(metav1.ConditionFalse, nodePoolValidationFailureTimeout+time.Minute)},
			operationAge: time.Minute,
			wantState:    coreapi.ProvisioningStateProvisioning,
			wantCode:     coreapi.CloudErrorCodeInvalidResource,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := nodePoolValidationOperationState(tt.validations, now.Add(-tt.operationAge), now)
			assert.Equal(t, tt.wantState, got.ProvisioningState)
			if tt.wantCode != "" {
				assert.Equal(t, tt.wantCode, got.CloudErrorCode)
			}
			if tt.msgContains != "" {
				assert.Contains(t, got.Message, tt.msgContains)
			}
		})
	}
}

func TestNodePoolCreateValidationCheckGatesOnlyDES(t *testing.T) {
	t.Parallel()
	now := time.Now()
	old := metav1.NewTime(now.Add(-time.Hour)) // older than the 30m grace window

	fixture := operationtesting.NewNodePoolTestFixture()
	desName := validationutils.AzureNodePoolDiskEncryptionSetPermissionsValidationName

	cond := func(condType string, status metav1.ConditionStatus) metav1.Condition {
		return metav1.Condition{Type: condType, Status: status, Reason: "ValidationResult", Message: condType + " detail", LastTransitionTime: old}
	}

	run := func(validations []metav1.Condition) *operationbase.OperationState {
		spNodePool := fixture.NewServiceProviderNodePool()
		spNodePool.Status.Validations = validations
		lister := &corelistertesting.SliceServiceProviderNodePoolLister{ServiceProviderNodePools: []*coreapi.ServiceProviderNodePool{spNodePool}}
		c := &nodePoolCreateValidationCheck{clock: clocktesting.NewFakePassiveClock(now), serviceProviderNodePoolLister: lister}
		op := fixture.NewOperation(cosmosstorageutils.OperationRequestCreate)
		op.StartTime = now.Add(-time.Hour)
		state, err := c.CalculateOperationStatus(context.Background(), op, struct{}{})
		require.NoError(t, err)
		return state
	}

	t.Run("failing non-DES validation does not gate creation", func(t *testing.T) {
		t.Parallel()
		state := run([]metav1.Condition{cond("AzureNodePoolVMQuotaValidation", metav1.ConditionFalse)})
		assert.Equal(t, coreapi.ProvisioningStateSucceeded, state.ProvisioningState)
	})

	t.Run("failing DES validation past window fails creation", func(t *testing.T) {
		t.Parallel()
		state := run([]metav1.Condition{
			cond("AzureNodePoolVMQuotaValidation", metav1.ConditionFalse),
			cond(desName, metav1.ConditionFalse),
		})
		assert.Equal(t, coreapi.ProvisioningStateFailed, state.ProvisioningState)
		assert.Equal(t, coreapi.CloudErrorCodeInvalidResource, state.CloudErrorCode)
	})

	t.Run("passing DES validation succeeds", func(t *testing.T) {
		t.Parallel()
		state := run([]metav1.Condition{cond(desName, metav1.ConditionTrue)})
		assert.Equal(t, coreapi.ProvisioningStateSucceeded, state.ProvisioningState)
	})
}
