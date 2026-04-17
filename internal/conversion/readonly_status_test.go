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

package conversion

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api"
)

// Status.Conditions is backend-owned. These tests lock the contract
// documented in internal/api/STATUS_OWNERSHIP.md: during PUT/PATCH the
// readonly-copy helpers must overwrite the body-derived Status with the
// stored Status so a client cannot influence conditions, and the stored
// Status must survive verbatim when the body does not carry one.

func storedCondition() api.Condition {
	return api.Condition{
		Type:               "Progressing",
		Status:             api.ConditionTrue,
		LastTransitionTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Reason:             "Reconciling",
		Message:            "backend-written",
	}
}

func maliciousCondition() api.Condition {
	return api.Condition{
		Type:               "Installed",
		Status:             api.ConditionTrue,
		LastTransitionTime: time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
		Reason:             "ClientInjected",
		Message:            "set by client body - must be discarded",
	}
}

func TestCopyReadOnlyClusterValues_StatusIsBackendOwned(t *testing.T) {
	stored := &api.HCPOpenShiftCluster{
		Status: api.HCPOpenShiftClusterStatus{
			Conditions: []api.Condition{storedCondition()},
		},
	}
	// body-derived dest carries a client-supplied Status that must be discarded.
	body := &api.HCPOpenShiftCluster{
		Status: api.HCPOpenShiftClusterStatus{
			Conditions: []api.Condition{maliciousCondition()},
		},
	}

	CopyReadOnlyClusterValues(body, stored)

	require.Len(t, body.Status.Conditions, 1, "body-supplied conditions must be discarded, stored conditions must win")
	assert.Equal(t, "", cmp.Diff(stored.Status, body.Status), "body Status must match stored Status exactly after readonly copy")

	// Defensive: mutating the body-side slice must not reach the stored value.
	body.Status.Conditions[0].Message = "mutated after copy"
	assert.Equal(t, "backend-written", stored.Status.Conditions[0].Message, "readonly copy must deep-copy, not alias, Status")
}

func TestCopyReadOnlyClusterValues_EmptyStoredClearsBody(t *testing.T) {
	stored := &api.HCPOpenShiftCluster{}
	body := &api.HCPOpenShiftCluster{
		Status: api.HCPOpenShiftClusterStatus{
			Conditions: []api.Condition{maliciousCondition()},
		},
	}

	CopyReadOnlyClusterValues(body, stored)

	assert.Empty(t, body.Status.Conditions, "empty stored Status must clear any body-supplied conditions")
}

func TestCopyReadOnlyNodePoolValues_StatusIsBackendOwned(t *testing.T) {
	stored := &api.HCPOpenShiftClusterNodePool{
		Status: api.HCPOpenShiftClusterNodePoolStatus{
			Conditions: []api.Condition{storedCondition()},
		},
	}
	body := &api.HCPOpenShiftClusterNodePool{
		Status: api.HCPOpenShiftClusterNodePoolStatus{
			Conditions: []api.Condition{maliciousCondition()},
		},
	}

	CopyReadOnlyNodePoolValues(body, stored)

	require.Len(t, body.Status.Conditions, 1)
	assert.Equal(t, "", cmp.Diff(stored.Status, body.Status))

	body.Status.Conditions[0].Message = "mutated after copy"
	assert.Equal(t, "backend-written", stored.Status.Conditions[0].Message)
}

func TestCopyReadOnlyNodePoolValues_EmptyStoredClearsBody(t *testing.T) {
	stored := &api.HCPOpenShiftClusterNodePool{}
	body := &api.HCPOpenShiftClusterNodePool{
		Status: api.HCPOpenShiftClusterNodePoolStatus{
			Conditions: []api.Condition{maliciousCondition()},
		},
	}

	CopyReadOnlyNodePoolValues(body, stored)

	assert.Empty(t, body.Status.Conditions)
}

func TestCopyReadOnlyExternalAuthValues_StatusIsBackendOwned(t *testing.T) {
	stored := &api.HCPOpenShiftClusterExternalAuth{
		Status: api.HCPOpenShiftClusterExternalAuthStatus{
			Conditions: []api.Condition{storedCondition()},
		},
	}
	body := &api.HCPOpenShiftClusterExternalAuth{
		Status: api.HCPOpenShiftClusterExternalAuthStatus{
			Conditions: []api.Condition{maliciousCondition()},
		},
	}

	CopyReadOnlyExternalAuthValues(body, stored)

	require.Len(t, body.Status.Conditions, 1)
	assert.Equal(t, "", cmp.Diff(stored.Status, body.Status))

	body.Status.Conditions[0].Message = "mutated after copy"
	assert.Equal(t, "backend-written", stored.Status.Conditions[0].Message)
}

func TestCopyReadOnlyExternalAuthValues_EmptyStoredClearsBody(t *testing.T) {
	stored := &api.HCPOpenShiftClusterExternalAuth{}
	body := &api.HCPOpenShiftClusterExternalAuth{
		Status: api.HCPOpenShiftClusterExternalAuthStatus{
			Conditions: []api.Condition{maliciousCondition()},
		},
	}

	CopyReadOnlyExternalAuthValues(body, stored)

	assert.Empty(t, body.Status.Conditions)
}
