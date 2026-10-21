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

package frontend

import (
	"context"
	"io"
	"net/http"
	"path"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// flakyOperationsDBClient simulates Cosmos session-consistency read lag by
// returning an injected error for the first missCount operation reads, then
// delegating to the in-memory mock.
type flakyOperationsDBClient struct {
	*corecosmosstoragetesting.MockResourcesDBClient
	missesRemaining int
	injectedError   error
	getCalls        int
}

func (c *flakyOperationsDBClient) Operations(subscriptionID string) corecosmosstorage.OperationCRUD {
	return &flakyOperationCRUD{
		OperationCRUD: c.MockResourcesDBClient.Operations(subscriptionID),
		client:        c,
	}
}

type flakyOperationCRUD struct {
	corecosmosstorage.OperationCRUD
	client *flakyOperationsDBClient
}

func (c *flakyOperationCRUD) Get(ctx context.Context, name string) (*coreapi.Operation, error) {
	c.client.getCalls++
	if c.client.missesRemaining > 0 {
		c.client.missesRemaining--
		return nil, c.client.injectedError
	}
	return c.OperationCRUD.Get(ctx, name)
}

// speedUpOperationReadLagBackoff shrinks the retry budget so tests exercising
// exhaustion of the budget run in milliseconds instead of seconds.
func speedUpOperationReadLagBackoff(t *testing.T) {
	t.Helper()
	original := operationReadLagBackoff
	operationReadLagBackoff = wait.Backoff{
		Duration: time.Millisecond,
		Factor:   1,
		Jitter:   0,
		Steps:    3,
	}
	t.Cleanup(func() {
		operationReadLagBackoff = original
	})
}

func newTestOperation(t *testing.T) *coreapi.Operation {
	t.Helper()
	return cosmosstorageutils.NewOperation(
		cosmosstorageutils.OperationRequestSystemAdminCredentialRequest,
		newClusterResourceID(t),
		newClusterInternalID(t),
		coreapitesting.TestLocation,
		"", "", "", nil)
}

func TestGetOperationRetriesReadLag(t *testing.T) {
	speedUpOperationReadLagBackoff(t)

	operation := newTestOperation(t)
	subscriptionID := operation.ExternalID.SubscriptionID
	ctx := utils.ContextWithLogger(t.Context(), testr.New(t))

	t.Run("transient NotFound is absorbed by retry", func(t *testing.T) {
		mockClient := corecosmosstoragetesting.NewMockResourcesDBClient()
		_, err := mockClient.Operations(subscriptionID).Create(ctx, operation, nil)
		require.NoError(t, err, "failed to seed operation document")

		flakyClient := &flakyOperationsDBClient{
			MockResourcesDBClient: mockClient,
			missesRemaining:       2,
			injectedError:         cosmosstorageutils.NewNotFoundError(),
		}
		f := &Frontend{resourcesDBClient: flakyClient}

		got, err := f.getOperation(ctx, subscriptionID, operation.ResourceID.Name)
		require.NoError(t, err, "read lag should have been absorbed by the retry")
		require.NotNil(t, got)
		assert.Equal(t, operation.OperationID.String(), got.OperationID.String())
		assert.Equal(t, 3, flakyClient.getCalls, "two lagging reads plus one successful read expected")
	})

	t.Run("genuine NotFound is preserved after the budget is exhausted", func(t *testing.T) {
		mockClient := corecosmosstoragetesting.NewMockResourcesDBClient()
		f := &Frontend{resourcesDBClient: mockClient}

		_, err := f.getOperation(ctx, subscriptionID, uuid.New().String())
		require.Error(t, err, "unknown operation ID must still fail")
		assert.True(t, cosmosstorageutils.IsNotFoundError(err),
			"exhausted retries must surface the NotFound error, got %v", err)
	})

	t.Run("errors other than NotFound are not retried", func(t *testing.T) {
		conflict := &azcore.ResponseError{
			ErrorCode:  "409 Conflict",
			StatusCode: http.StatusConflict,
		}
		require.False(t, cosmosstorageutils.IsNotFoundError(conflict),
			"test precondition: injected error must not be NotFound")

		flakyClient := &flakyOperationsDBClient{
			MockResourcesDBClient: corecosmosstoragetesting.NewMockResourcesDBClient(),
			missesRemaining:       1,
			injectedError:         conflict,
		}
		f := &Frontend{resourcesDBClient: flakyClient}

		_, err := f.getOperation(ctx, subscriptionID, operation.ResourceID.Name)
		require.Error(t, err, "non-NotFound errors must fail the read")
		assert.Equal(t, conflict, err)
		assert.Equal(t, 1, flakyClient.getCalls, "non-NotFound errors must not be retried")
	})
}

func TestOperationStatusEndpointSurvivesReadLag(t *testing.T) {
	speedUpOperationReadLagBackoff(t)

	operation := newTestOperation(t)
	ctx := utils.ContextWithLogger(t.Context(), testr.New(t))

	mockClient := corecosmosstoragetesting.NewMockResourcesDBClient()
	_, err := mockClient.Operations(operation.ExternalID.SubscriptionID).Create(ctx, operation, nil)
	require.NoError(t, err, "failed to seed operation document")

	flakyClient := &flakyOperationsDBClient{
		MockResourcesDBClient: mockClient,
		missesRemaining:       1,
		injectedError:         cosmosstorageutils.NewNotFoundError(),
	}

	reg := prometheus.NewRegistry()
	f := NewFrontend(
		testr.New(t),
		nil,
		nil,
		reg,
		reg,
		flakyClient,
		newTestFrontendInformers(t, flakyClient),
		nil,
		newNoopAuditClient(t),
		coreapitesting.TestLocation,
		true,
		azure.NewClusterScopedIdentitiesConfig(azure.RoleDefinitionConfigSetNameDev),
	)

	subs := map[string]*coreapi.Subscription{
		coreapitesting.TestSubscriptionID: newTestSubscription(coreapitesting.TestSubscriptionID, coreapi.SubscriptionStateRegistered, nil),
	}
	ts := newHTTPServer(ctx, f, mockClient, subs)

	// Poll the exact URL shape handed back in the Azure-AsyncOperation header
	// of a 202 response, the way the azcore ARM poller does.
	statusURL := ts.URL + path.Join(
		"/subscriptions", operation.OperationID.SubscriptionID,
		"providers", coreapi.ProviderNamespace,
		"locations", operation.OperationID.Location,
		coreapi.OperationStatusResourceTypeName, operation.OperationID.Name) +
		"?api-version=" + coreapitesting.TestAPIVersion

	resp, err := ts.Client().Get(statusURL)
	require.NoError(t, err, "operation status request failed")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"first-poll 404 from replication lag must not surface to the poller")
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), string(coreapi.ProvisioningStateAccepted),
		"operation status body should report the committed status")
	assert.Equal(t, 0, flakyClient.missesRemaining, "injected read lag should have been consumed")
}
