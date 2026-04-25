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

package database

import (
	"testing"

	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

// TestNewOperation_DoesNotSeedPhaseTimestamps verifies the design constraint
// that initial-phase seeding is the caller's job. Seeding inside NewOperation
// would record a phantom Accepted phase for RevokeCredentials operations,
// because that path overrides Status to Deleting only after NewOperation
// returns.
func TestNewOperation_DoesNotSeedPhaseTimestamps(t *testing.T) {
	externalID := api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c1"))

	for _, req := range []OperationRequest{
		OperationRequestCreate,
		OperationRequestUpdate,
		OperationRequestDelete,
		OperationRequestRequestCredential,
		OperationRequestRevokeCredentials,
	} {
		t.Run(string(req), func(t *testing.T) {
			op := NewOperation(req, externalID, ocm.InternalID{}, "eastus", "", "", "", nil)
			require.Nil(t, op.PhaseTimestamps,
				"NewOperation must not seed PhaseTimestamps; callers record the initial phase after any post-creation overrides")
		})
	}
}

// TestNewOperation_StatusForRequestTypes documents the initial Status each
// request type produces. Callers rely on this for their RecordPhaseEntry call
// immediately after NewOperation (or after the post-creation override for
// RevokeCredentials).
func TestNewOperation_StatusForRequestTypes(t *testing.T) {
	externalID := api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c1"))

	tests := []struct {
		req    OperationRequest
		status arm.ProvisioningState
	}{
		{OperationRequestCreate, arm.ProvisioningStateAccepted},
		{OperationRequestUpdate, arm.ProvisioningStateAccepted},
		{OperationRequestDelete, arm.ProvisioningStateDeleting},
		{OperationRequestRequestCredential, arm.ProvisioningStateAccepted},
		// RevokeCredentials starts Accepted here and is overridden externally.
		{OperationRequestRevokeCredentials, arm.ProvisioningStateAccepted},
	}

	for _, tc := range tests {
		t.Run(string(tc.req), func(t *testing.T) {
			op := NewOperation(tc.req, externalID, ocm.InternalID{}, "eastus", "", "", "", nil)
			require.Equal(t, tc.status, op.Status)
			require.False(t, op.LastTransitionTime.IsZero())
		})
	}
}
