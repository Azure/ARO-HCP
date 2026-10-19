// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package coreapi

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

func TestCredentialDeadlinePersistence(t *testing.T) {
	var legacy Operation
	require.NoError(t, json.Unmarshal([]byte(`{"status":"Accepted"}`), &legacy))
	require.Nil(t, legacy.CompletionDeadline, "legacy operation decoding must not backfill a deadline")
	deadline := time.Date(2026, 10, 1, 12, 20, 0, 0, time.UTC)
	op := &Operation{CompletionDeadline: &deadline}
	encoded, err := json.Marshal(op)
	require.NoError(t, err)
	var decoded Operation
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Equal(t, deadline, *decoded.CompletionDeadline)
	copied := op.DeepCopy()
	*copied.CompletionDeadline = copied.CompletionDeadline.Add(time.Minute)
	require.Equal(t, deadline, *op.CompletionDeadline, "deepcopy must not alias the stored deadline")
}

func TestCredentialReservedIdentityPersistence(t *testing.T) {
	id, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/systemAdminCredentialRequests/reserved")
	require.NoError(t, err)
	revocationID, err := azcorearm.ParseResourceID(id.Parent.String() + "/systemAdminCredentialRevocations/reserved")
	require.NoError(t, err)
	op := &Operation{
		Status:                          ProvisioningStateAccepted,
		SystemAdminCredentialRequest:    &OperationSystemAdminCredentialRequest{SystemAdminCredentialRequestResourceID: id},
		SystemAdminCredentialRevocation: &OperationSystemAdminCredentialRevocation{SystemAdminCredentialRevocationResourceID: revocationID},
	}
	encoded, err := json.Marshal(op)
	require.NoError(t, err)
	var decoded Operation
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Equal(t, ProvisioningStateAccepted, decoded.Status)
	require.Equal(t, id.String(), decoded.SystemAdminCredentialRequest.SystemAdminCredentialRequestResourceID.String())
	require.Equal(t, revocationID.String(), decoded.SystemAdminCredentialRevocation.SystemAdminCredentialRevocationResourceID.String())
	require.Nil(t, decoded.CompletionDeadline, "identity persistence must not backfill legacy deadlines")
}
