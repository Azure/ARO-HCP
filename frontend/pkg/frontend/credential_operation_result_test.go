// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package frontend

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	v20261001 "github.com/Azure/ARO-HCP/internal/azureapi/v20261001"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestCredentialOperationResultRejectsFailedAndCanceled(t *testing.T) {
	registry := coreapi.NewAPIRegistry()
	require.NoError(t, v20261001.RegisterVersion(registry))
	version, ok := registry.Lookup(registry.ListVersions().UnsortedList()[0])
	require.True(t, ok)
	for _, status := range []coreapi.ProvisioningState{coreapi.ProvisioningStateFailed, coreapi.ProvisioningStateCanceled} {
		t.Run(string(status), func(t *testing.T) {
			id := newClusterResourceID(t)
			rid := metadataapi.Must(azcorearm.ParseResourceID(id.String() + "/systemAdminCredentialRequests/intent"))
			op := cosmosstorageutils.NewOperation(coreapi.OperationRequestSystemAdminCredentialRequest, id, metadataapi.InternalID{}, "eastus", "", "", "", nil)
			op.Status = status
			op.SystemAdminCredentialRequest = &coreapi.OperationSystemAdminCredentialRequest{SystemAdminCredentialRequestResourceID: rid}
			cred := &coreapi.SystemAdminCredentialRequest{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: rid, PartitionKey: id.SubscriptionID}, Status: coreapi.SystemAdminCredentialRequestStatus{Conditions: []metav1.Condition{{Type: coreapi.SystemAdminCredentialRequestConditionIssued, Status: metav1.ConditionTrue}}}}
			ctx := ContextWithVersion(utils.ContextWithResourceID(t.Context(), op.OperationID), version)
			db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{op, cred})
			require.NoError(t, err)
			f := &Frontend{resourcesDBClient: db}
			request := httptest.NewRequestWithContext(ctx, "GET", "/", nil)
			request.SetPathValue(PathSegmentSubscriptionID, id.SubscriptionID)
			response := httptest.NewRecorder()
			require.ErrorContains(t, f.OperationResult(response, request), "invalid operation status")
			require.Empty(t, response.Body.String(), "a late Issued condition must not expose credentials through a failed ARM operation")
		})
	}
}
