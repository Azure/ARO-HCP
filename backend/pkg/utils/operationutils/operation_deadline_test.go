// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package operationutils

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

func TestApplyOperationDeadline(t *testing.T) {
	now := time.Unix(100, 0)
	failure := errors.New("private dependency details")
	for _, tc := range []struct {
		name     string
		state    *OperationState
		err      error
		deadline *metav1.Time
		want     coreapi.ProvisioningState
		wantErr  bool
		wantCode string
	}{
		{name: "nil deadline retains retry", err: failure, wantErr: true},
		{name: "before deadline retains retry", err: failure, deadline: &metav1.Time{Time: now.Add(time.Second)}, wantErr: true},
		{name: "at deadline retains retry", err: failure, deadline: &metav1.Time{Time: now}, wantErr: true},
		{name: "success wins after deadline", state: NewOperationState(coreapi.ProvisioningStateSucceeded, ""), deadline: &metav1.Time{Time: now.Add(-time.Second)}, want: coreapi.ProvisioningStateSucceeded},
		{name: "partial success cannot hide failed observation", state: NewOperationState(coreapi.ProvisioningStateSucceeded, ""), err: failure, deadline: &metav1.Time{Time: now.Add(-time.Second)}, want: coreapi.ProvisioningStateFailed, wantCode: coreapi.CloudErrorCodeDeadlineExceeded},
		{name: "pending times out", state: NewOperationState(coreapi.ProvisioningStateProvisioning, "waiting for workers"), deadline: &metav1.Time{Time: now.Add(-time.Second)}, want: coreapi.ProvisioningStateFailed, wantCode: coreapi.CloudErrorCodeDeadlineExceeded},
		{name: "specific failure survives expiry", state: NewFailedOperationState(coreapi.CloudErrorCodeInvalidResource, "invalid network", nil), deadline: &metav1.Time{Time: now.Add(-time.Second)}, want: coreapi.ProvisioningStateFailed, wantCode: coreapi.CloudErrorCodeInvalidResource},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ApplyOperationDeadline(tc.state, tc.err, tc.deadline, now, "creation did not complete before the deadline")
			if tc.wantErr {
				require.ErrorIs(t, err, failure)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got.ProvisioningState)
			require.Equal(t, tc.wantCode, got.CloudErrorCode)
			require.NotContains(t, got.Message, failure.Error())
			if tc.err != nil {
				require.Contains(t, got.Message, "Some operation states could not be determined")
			}
			if tc.state != nil && tc.state.Message != "" {
				require.Contains(t, got.Message, tc.state.Message)
			}
		})
	}
}

func TestSummarizePartialOperationStates(t *testing.T) {
	failure := errors.New("observation failed")
	state, err := SummarizeOperationStates([]*OperationState{
		NewOperationState(coreapi.ProvisioningStateSucceeded, "").WithSource("finished"),
		NewOperationState(coreapi.ProvisioningStateUpdating, "waiting for rollout").WithSource("hostedCluster"),
	}, failure)
	require.ErrorIs(t, err, failure)
	require.Equal(t, coreapi.ProvisioningStateUpdating, state.ProvisioningState)
	require.Contains(t, state.Message, "[hostedCluster] waiting for rollout")
	got, err := ApplyOperationDeadline(state, err, &metav1.Time{Time: time.Unix(1, 0)}, time.Unix(2, 0), "update timed out")
	require.NoError(t, err)
	require.Equal(t, coreapi.ProvisioningStateFailed, got.ProvisioningState)
	require.Contains(t, got.Message, "waiting for rollout")
	require.Contains(t, got.Message, "Some operation states could not be determined")
}
