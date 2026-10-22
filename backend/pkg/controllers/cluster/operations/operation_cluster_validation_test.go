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

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	operationtesting "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils/operationtesting"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/validationutils"
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
			name:        "validation at exactly the failure timeout fails an older operation",
			validations: []metav1.Condition{condition("Subnet", metav1.ConditionFalse, clusterValidationFailureTimeout)},
			wantState:   coreapi.ProvisioningStateFailed, wantCode: coreapi.CloudErrorCodeInvalidResource,
			wantMessage: "Subnet: ValidationResult: Subnet details",
		},
		{
			name: "any failure older than the failure timeout fails and all failures are reported",
			validations: []metav1.Condition{
				condition("Subnet", metav1.ConditionFalse, clusterValidationFailureTimeout+time.Second),
				condition("Identity", metav1.ConditionFalse, time.Minute),
				condition("Quota", metav1.ConditionTrue, time.Hour),
			},
			wantState: coreapi.ProvisioningStateFailed, wantCode: coreapi.CloudErrorCodeInvalidResource,
			wantMessage: "Identity: ValidationResult: Identity details; Subnet: ValidationResult: Subnet details",
		},
		{
			name:        "recent unknown validation does not fail the operation",
			validations: []metav1.Condition{condition("Subnet", metav1.ConditionUnknown, 4*time.Minute)},
			wantState:   coreapi.ProvisioningStateProvisioning, wantCode: coreapi.CloudErrorCodeInvalidResource,
			wantMessage: "Subnet: ValidationResult: Subnet details",
		},
		{
			name:        "old unknown validation fails the operation same as a failed one",
			validations: []metav1.Condition{condition("Subnet", metav1.ConditionUnknown, clusterValidationFailureTimeout)},
			wantState:   coreapi.ProvisioningStateFailed, wantCode: coreapi.CloudErrorCodeInvalidResource,
			wantMessage: "Subnet: ValidationResult: Subnet details",
		},
		{
			name: "fresh unknown validation does not extend a failure timeout",
			validations: []metav1.Condition{
				condition("Subnet", metav1.ConditionFalse, clusterValidationFailureTimeout+time.Minute),
				condition("Identity", metav1.ConditionUnknown, time.Minute),
			},
			wantState: coreapi.ProvisioningStateFailed, wantCode: coreapi.CloudErrorCodeInvalidResource,
			wantMessage: "Identity: ValidationResult: Identity details; Subnet: ValidationResult: Subnet details",
		},
		{
			name: "old unknown validation alongside a fresh failure still fails",
			validations: []metav1.Condition{
				condition("Subnet", metav1.ConditionFalse, 4*time.Minute),
				condition("Identity", metav1.ConditionUnknown, clusterValidationFailureTimeout),
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
		{
			name:        "missing transition time does not imply an expired unknown failure",
			validations: []metav1.Condition{{Type: "Subnet", Status: metav1.ConditionUnknown, Reason: "InternalError"}},
			wantState:   coreapi.ProvisioningStateProvisioning, wantCode: coreapi.CloudErrorCodeInvalidResource,
			wantMessage: "Subnet: InternalError",
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
	statuses := []struct {
		name   string
		status metav1.ConditionStatus
		reason string
	}{
		{name: "failed", status: metav1.ConditionFalse, reason: "InvalidSubnet"},
		{name: "unknown", status: metav1.ConditionUnknown, reason: "InternalError"},
	}
	tests := []struct {
		name      string
		startTime time.Time
		wantState coreapi.ProvisioningState
	}{
		{name: "new operation", startTime: now, wantState: coreapi.ProvisioningStateProvisioning},
		{name: "operation just under the failure timeout", startTime: now.Add(-clusterValidationFailureTimeout + time.Second), wantState: coreapi.ProvisioningStateProvisioning},
		{name: "both clocks exactly at the failure timeout", startTime: now.Add(-clusterValidationFailureTimeout), wantState: coreapi.ProvisioningStateFailed},
		{name: "older operation", startTime: now.Add(-time.Hour), wantState: coreapi.ProvisioningStateFailed},
		{name: "missing operation start time", wantState: coreapi.ProvisioningStateProvisioning},
		{name: "future operation start time", startTime: now.Add(time.Minute), wantState: coreapi.ProvisioningStateProvisioning},
	}
	for _, st := range statuses {
		t.Run(st.name, func(t *testing.T) {
			t.Parallel()
			spc := &coreapi.ServiceProviderCluster{Status: coreapi.ServiceProviderClusterStatus{
				Validations: []metav1.Condition{{
					Type: "Subnet", Status: st.status, Reason: st.reason,
					LastTransitionTime: metav1.NewTime(now.Add(-clusterValidationFailureTimeout)),
				}},
			}}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					got := clusterValidationOperationState(spc, tt.startTime, now)
					assert.Equal(t, tt.wantState, got.ProvisioningState)
					assert.Equal(t, coreapi.CloudErrorCodeInvalidResource, got.CloudErrorCode)
					assert.Equal(t, "Subnet: "+st.reason, got.Message)
				})
			}
		})
	}
}

func ptrTo[T any](v T) *T {
	return &v
}

func TestClusterValidationProbeFreshness(t *testing.T) {
	t.Parallel()
	now := operationtesting.MustParseTime("2026-09-23T12:00:00Z")
	failedFor := func(age time.Duration) metav1.Condition {
		return metav1.Condition{
			Type: "Subnet", Status: metav1.ConditionFalse, Reason: "InvalidSubnet",
			LastTransitionTime: metav1.NewTime(now.Add(-age)),
		}
	}
	unknownFor := func(age time.Duration) metav1.Condition {
		return metav1.Condition{
			Type: "Subnet", Status: metav1.ConditionUnknown, Reason: "InternalError",
			LastTransitionTime: metav1.NewTime(now.Add(-age)),
		}
	}
	tests := []struct {
		name       string
		validation metav1.Condition
		probeAge   *time.Duration // nil means no probe recorded
		zeroProbe  bool           // record a zero-value probe time
		wantState  coreapi.ProvisioningState
	}{
		{
			name:       "old failure with fresh probe fails the operation",
			validation: failedFor(10 * time.Minute),
			probeAge:   ptrTo(time.Minute),
			wantState:  coreapi.ProvisioningStateFailed,
		},
		{
			name:       "old failure with stale probe stays provisioning",
			validation: failedFor(10 * time.Minute),
			probeAge:   ptrTo(validationutils.ValidationProbeFreshnessWindow + 2*time.Minute),
			wantState:  coreapi.ProvisioningStateProvisioning,
		},
		{
			name:       "probe exactly at the freshness window boundary still fails",
			validation: failedFor(10 * time.Minute),
			probeAge:   ptrTo(validationutils.ValidationProbeFreshnessWindow),
			wantState:  coreapi.ProvisioningStateFailed,
		},
		{
			name:       "probe just beyond the freshness window stays provisioning",
			validation: failedFor(10 * time.Minute),
			probeAge:   ptrTo(validationutils.ValidationProbeFreshnessWindow + time.Second),
			wantState:  coreapi.ProvisioningStateProvisioning,
		},
		{
			name:       "zero-value recorded probe is treated as stale",
			validation: failedFor(10 * time.Minute),
			zeroProbe:  true,
			wantState:  coreapi.ProvisioningStateProvisioning,
		},
		{
			name:       "missing probe falls back to condition age",
			validation: failedFor(10 * time.Minute),
			probeAge:   nil,
			wantState:  coreapi.ProvisioningStateFailed,
		},
		{
			name:       "old unknown condition without a probe fails the operation",
			validation: unknownFor(10 * time.Minute),
			probeAge:   nil,
			wantState:  coreapi.ProvisioningStateFailed,
		},
		{
			name:       "old unknown condition with a stale probe stays provisioning",
			validation: unknownFor(10 * time.Minute),
			probeAge:   ptrTo(validationutils.ValidationProbeFreshnessWindow + 2*time.Minute),
			wantState:  coreapi.ProvisioningStateProvisioning,
		},
		{
			name:       "fresh probe does not accelerate failure of a young condition",
			validation: failedFor(4 * time.Minute),
			probeAge:   ptrTo(time.Minute),
			wantState:  coreapi.ProvisioningStateProvisioning,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			status := coreapi.ServiceProviderClusterStatus{Validations: []metav1.Condition{tt.validation}}
			switch {
			case tt.zeroProbe:
				status.ValidationProbeTimes = map[string]metav1.Time{"Subnet": {}}
			case tt.probeAge != nil:
				status.ValidationProbeTimes = map[string]metav1.Time{
					"Subnet": metav1.NewTime(now.Add(-*tt.probeAge)),
				}
			}
			spc := &coreapi.ServiceProviderCluster{Status: status}
			got := clusterValidationOperationState(spc, now.Add(-time.Hour), now)
			assert.Equal(t, tt.wantState, got.ProvisioningState)
			assert.Equal(t, coreapi.CloudErrorCodeInvalidResource, got.CloudErrorCode)
			assert.Equal(t, "Subnet: "+tt.validation.Reason, got.Message)
		})
	}
}

func TestClusterValidationProbeFreshnessMetrics(t *testing.T) {
	t.Parallel()
	now := operationtesting.MustParseTime("2026-09-23T12:00:00Z")
	const validationType = "ProbeFreshnessMetricsValidation"
	spc := &coreapi.ServiceProviderCluster{Status: coreapi.ServiceProviderClusterStatus{
		Validations: []metav1.Condition{{
			Type: validationType, Status: metav1.ConditionFalse, Reason: "InvalidSubnet",
			LastTransitionTime: metav1.NewTime(now.Add(-time.Hour)),
		}},
		ValidationProbeTimes: map[string]metav1.Time{validationType: metav1.NewTime(now.Add(-validationutils.ValidationProbeFreshnessWindow - time.Second))},
	}}
	deferralsBefore := testutil.ToFloat64(validationStaleProbeDeferrals.WithLabelValues(validationType))
	failuresBefore := testutil.ToFloat64(validationTerminalFailures.WithLabelValues(validationType))

	got := clusterValidationOperationState(spc, now.Add(-time.Hour), now)
	assert.Equal(t, coreapi.ProvisioningStateProvisioning, got.ProvisioningState)
	assert.Equal(t, deferralsBefore+1, testutil.ToFloat64(validationStaleProbeDeferrals.WithLabelValues(validationType)), "stale probe must count a deferral")
	assert.Equal(t, failuresBefore, testutil.ToFloat64(validationTerminalFailures.WithLabelValues(validationType)), "stale probe must not count a terminal failure")

	spc.Status.ValidationProbeTimes[validationType] = metav1.NewTime(now.Add(-time.Minute))
	got = clusterValidationOperationState(spc, now.Add(-time.Hour), now)
	assert.Equal(t, coreapi.ProvisioningStateFailed, got.ProvisioningState)
	assert.Equal(t, deferralsBefore+1, testutil.ToFloat64(validationStaleProbeDeferrals.WithLabelValues(validationType)), "fresh probe must not count a deferral")
	assert.Equal(t, failuresBefore+1, testutil.ToFloat64(validationTerminalFailures.WithLabelValues(validationType)), "fresh probe must count a terminal failure")
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

// TestClusterValidationPropagationDeferralGate pins both sides of the propagation-deferral
// contract at the operation gate: an established RoleAssignmentPropagationPending condition
// holds the operation open regardless of age, while a RoleAssignmentPropagationCheckFailed
// condition is not a deferral and fails the operation once old enough. That asymmetry is why
// the validation syncer must preserve the Pending reason across transient lookup failures
// (see validationutils.PreservePropagationDeferral): a single ARM 5xx must not terminally
// fail an operation whose permissions were already granted and are still propagating.
func TestClusterValidationPropagationDeferralGate(t *testing.T) {
	t.Parallel()
	now := operationtesting.MustParseTime("2026-09-23T12:00:00Z")
	spc := &coreapi.ServiceProviderCluster{Status: coreapi.ServiceProviderClusterStatus{
		Validations: []metav1.Condition{{
			Type:               "DataPlaneIdentitiesPermissions",
			Status:             metav1.ConditionUnknown,
			Reason:             "RoleAssignmentPropagationPending",
			LastTransitionTime: metav1.NewTime(now.Add(-time.Hour)),
		}},
	}}

	assert.Equal(t, coreapi.ProvisioningStateProvisioning,
		clusterValidationOperationState(spc, now.Add(-time.Hour), now).ProvisioningState,
		"an established propagation deferral holds the operation open")

	spc.Status.Validations[0].Reason = "RoleAssignmentPropagationCheckFailed"
	assert.Equal(t, coreapi.ProvisioningStateFailed,
		clusterValidationOperationState(spc, now.Add(-time.Hour), now).ProvisioningState,
		"a lookup-failure condition is not a deferral and fails the operation once old enough")
}
