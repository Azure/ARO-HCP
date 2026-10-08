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

package pipeline

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources/fake"
)

// resourceGroupCalls records the writes ensureResourceGroupExists makes.
type resourceGroupCalls struct {
	created *armresources.ResourceGroup
	updated *armresources.ResourceGroupPatchable
}

func newFakeResourceGroupsClient(t *testing.T, getStatus int, existingTags map[string]*string, calls *resourceGroupCalls) *armresources.ResourceGroupsClient {
	t.Helper()
	server := &fake.ResourceGroupsServer{
		Get: func(_ context.Context, name string, _ *armresources.ResourceGroupsClientGetOptions) (resp azfake.Responder[armresources.ResourceGroupsClientGetResponse], errResp azfake.ErrorResponder) {
			if getStatus != http.StatusOK {
				errResp.SetResponseError(getStatus, http.StatusText(getStatus))
				return
			}
			resp.SetResponse(http.StatusOK, armresources.ResourceGroupsClientGetResponse{ResourceGroup: armresources.ResourceGroup{
				Name:     to.Ptr(name),
				Location: to.Ptr("westus3"),
				Tags:     existingTags,
			}}, nil)
			return
		},
		CreateOrUpdate: func(_ context.Context, _ string, parameters armresources.ResourceGroup, _ *armresources.ResourceGroupsClientCreateOrUpdateOptions) (resp azfake.Responder[armresources.ResourceGroupsClientCreateOrUpdateResponse], errResp azfake.ErrorResponder) {
			calls.created = &parameters
			resp.SetResponse(http.StatusCreated, armresources.ResourceGroupsClientCreateOrUpdateResponse{ResourceGroup: parameters}, nil)
			return
		},
		Update: func(_ context.Context, _ string, parameters armresources.ResourceGroupPatchable, _ *armresources.ResourceGroupsClientUpdateOptions) (resp azfake.Responder[armresources.ResourceGroupsClientUpdateResponse], errResp azfake.ErrorResponder) {
			calls.updated = &parameters
			resp.SetResponse(http.StatusOK, armresources.ResourceGroupsClientUpdateResponse{}, nil)
			return
		},
	}
	client, err := armresources.NewResourceGroupsClient("subscription", &azfake.TokenCredential{}, &azcorearm.ClientOptions{ClientOptions: azcore.ClientOptions{
		Retry:     policy.RetryOptions{MaxRetries: -1},
		Transport: fake.NewResourceGroupsServerTransport(server),
	}})
	require.NoError(t, err)
	return client
}

func TestEnsureResourceGroupExists(t *testing.T) {
	creationTags := map[string]string{"jobID.aro-hcp-ci.redhat.com": "2097011220782518272"}

	t.Run("missing group is created with creation tags", func(t *testing.T) {
		calls := &resourceGroupCalls{}
		client := newFakeResourceGroupsClient(t, http.StatusNotFound, nil, calls)

		require.NoError(t, ensureResourceGroupExists(context.Background(), client, "westus3", "rg", false, creationTags))

		require.NotNil(t, calls.created, "expected the missing resource group to be created")
		assert.Equal(t, map[string]*string{"jobID.aro-hcp-ci.redhat.com": to.Ptr("2097011220782518272")}, calls.created.Tags)
		assert.Equal(t, to.Ptr("westus3"), calls.created.Location)
		assert.Nil(t, calls.updated)
	})

	t.Run("existing group keeps its tags", func(t *testing.T) {
		calls := &resourceGroupCalls{}
		existing := map[string]*string{"persist": to.Ptr("true"), "owner": to.Ptr("shared")}
		client := newFakeResourceGroupsClient(t, http.StatusOK, existing, calls)

		require.NoError(t, ensureResourceGroupExists(context.Background(), client, "westus3", "rg", true, creationTags))

		assert.Nil(t, calls.created, "an existing resource group must not be re-created")
		assert.Nil(t, calls.updated, "unchanged tags must not be rewritten")
	})

	t.Run("existing group only reconciles persist", func(t *testing.T) {
		calls := &resourceGroupCalls{}
		existing := map[string]*string{"owner": to.Ptr("shared")}
		client := newFakeResourceGroupsClient(t, http.StatusOK, existing, calls)

		require.NoError(t, ensureResourceGroupExists(context.Background(), client, "westus3", "rg", true, creationTags))

		assert.Nil(t, calls.created)
		require.NotNil(t, calls.updated, "expected the persist tag to be added")
		assert.Equal(t, map[string]*string{"owner": to.Ptr("shared"), "persist": to.Ptr("true")}, calls.updated.Tags)
	})

	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run("get failure "+http.StatusText(status)+" never writes", func(t *testing.T) {
			calls := &resourceGroupCalls{}
			client := newFakeResourceGroupsClient(t, status, nil, calls)

			err := ensureResourceGroupExists(context.Background(), client, "westus3", "rg", true, creationTags)

			require.Error(t, err)
			assert.Nil(t, calls.created, "a failed lookup must not create the resource group")
			assert.Nil(t, calls.updated, "a failed lookup must not update the resource group")
		})
	}
}
