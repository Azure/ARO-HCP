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
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// newExternalAuthMetricsTestCluster builds the parent cluster document that
// backs the ExternalAuth resources used by these tests. The cluster is
// terminal (Succeeded) so checkForProvisioningStateConflict never blocks on
// the parent's state.
func newExternalAuthMetricsTestCluster(t *testing.T) *coreapi.Cluster {
	t.Helper()
	clusterResourceID := newClusterResourceID(t)
	return &coreapi.Cluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   clusterResourceID,
			PartitionKey: strings.ToLower(clusterResourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   clusterResourceID,
				Name: clusterResourceID.Name,
				Type: clusterResourceID.ResourceType.String(),
			},
		},
		ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{
			ProvisioningState: coreapi.ProvisioningStateSucceeded,
		},
	}
}

// newExternalAuthMetricsTestDoc builds a well-formed ExternalAuth document
// with CosmosMetadata populated so it can be seeded directly into the mock
// Cosmos DB, at the given provisioning state.
func newExternalAuthMetricsTestDoc(t *testing.T, state coreapi.ProvisioningState) *coreapi.ExternalAuth {
	t.Helper()
	externalAuth := coreapitesting.MinimumValidExternalAuthTestCase()
	externalAuth.CosmosMetadata = coreapi.CosmosMetadata{
		ResourceID:   externalAuth.ID,
		PartitionKey: strings.ToLower(externalAuth.ID.SubscriptionID),
	}
	externalAuth.Properties.ProvisioningState = state
	return externalAuth
}

// newExternalAuthMetricsTestFrontend builds a Frontend plus mock DB seeded
// with the given cluster and external auth documents, and returns a running
// test HTTP server for it.
func newExternalAuthMetricsTestFrontend(t *testing.T, cluster *coreapi.Cluster, externalAuths ...*coreapi.ExternalAuth) (*corecosmosstoragetesting.MockResourcesDBClient, func(method, path string, body []byte) *http.Response) {
	t.Helper()

	reg := prometheus.NewRegistry()
	mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()

	f := NewFrontend(
		testr.New(t),
		nil,
		nil,
		reg,
		reg,
		mockResourcesDBClient,
		newTestFrontendInformers(t, mockResourcesDBClient),
		nil,
		newNoopAuditClient(t),
		coreapitesting.TestLocation,
		true,
		azure.NewClusterScopedIdentitiesConfig(azure.RoleDefinitionConfigSetNameDev),
	)

	ctx := utils.ContextWithLogger(t.Context(), testr.New(t))

	if cluster != nil {
		_, err := mockResourcesDBClient.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName).Create(ctx, cluster, nil)
		require.NoError(t, err)
	}
	for _, externalAuth := range externalAuths {
		if externalAuth == nil {
			continue
		}
		_, err := mockResourcesDBClient.HCPClusters(externalAuth.ID.SubscriptionID, externalAuth.ID.ResourceGroupName).
			ExternalAuth(externalAuth.ID.Parent.Name).Create(ctx, externalAuth, nil)
		require.NoError(t, err)
	}

	subs := map[string]*coreapi.Subscription{
		coreapitesting.TestSubscriptionID: newTestSubscription(coreapitesting.TestSubscriptionID, coreapi.SubscriptionStateRegistered, nil),
	}
	ts := newHTTPServer(ctx, f, mockResourcesDBClient, subs)
	t.Cleanup(ts.Close)

	do := func(method, path string, body []byte) *http.Response {
		var reader *bytes.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		} else {
			reader = bytes.NewReader(nil)
		}
		req, err := http.NewRequest(method, ts.URL+path, reader)
		require.NoError(t, err)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		// Required by MiddlewareSystemData for PATCH/PUT requests
		req.Header.Set(coreapi.HeaderNameARMResourceSystemData, "{}")
		resp, err := ts.Client().Do(req)
		require.NoError(t, err)
		return resp
	}

	return mockResourcesDBClient, do
}

// TestUpdateExternalAuthEmitsStateTransitionMetric exercises the real PATCH
// handler path (patchExternalAuth -> updateExternalAuthInCosmos) and verifies
// it emits frontend_externalauth_state_transitions_total with the expected
// labels on a successful transaction, and that a request rejected before the
// transaction (provisioning-state conflict) does not touch the counter.
func TestUpdateExternalAuthEmitsStateTransitionMetric(t *testing.T) {
	tests := []struct {
		name             string
		docState         coreapi.ProvisioningState
		expectStatusCode int
		expectIncrement  bool
		expectFromState  string
		expectToState    string
	}{
		{
			name:             "terminal state: update succeeds and emits metric",
			docState:         coreapi.ProvisioningStateSucceeded,
			expectStatusCode: http.StatusAccepted,
			expectIncrement:  true,
			expectFromState:  "succeeded",
			expectToState:    "accepted",
		},
		{
			name:             "non-terminal state: conflict is returned and metric is not touched",
			docState:         coreapi.ProvisioningStateProvisioning,
			expectStatusCode: http.StatusConflict,
			expectIncrement:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			externalAuthStateTransitionsTotal.Reset()

			cluster := newExternalAuthMetricsTestCluster(t)
			externalAuth := newExternalAuthMetricsTestDoc(t, tc.docState)

			_, do := newExternalAuthMetricsTestFrontend(t, cluster, externalAuth)

			requestPath := coreapitesting.TestExternalAuthResourceID + "?api-version=" + coreapitesting.TestAPIVersion
			resp := do(http.MethodPatch, requestPath, []byte("{}"))
			defer resp.Body.Close()

			assert.Equal(t, tc.expectStatusCode, resp.StatusCode)

			resourceType := strings.ToLower(coreapi.ExternalAuthResourceType.String())
			if tc.expectIncrement {
				expected := strings.NewReader(`
					# HELP frontend_externalauth_state_transitions_total Total number of provisioning state transitions for ExternalAuth resources initiated by the frontend.
					# TYPE frontend_externalauth_state_transitions_total counter
					frontend_externalauth_state_transitions_total{from_state="` + tc.expectFromState + `",resource_type="` + resourceType + `",to_state="` + tc.expectToState + `"} 1
				`)
				err := testutil.CollectAndCompare(externalAuthStateTransitionsTotal, expected, "frontend_externalauth_state_transitions_total")
				assert.NoError(t, err, "metric mismatch for frontend_externalauth_state_transitions_total")
			} else {
				assert.Equal(t, 0, testutil.CollectAndCount(externalAuthStateTransitionsTotal),
					"expected no counter series for frontend_externalauth_state_transitions_total when the update request is rejected before the transaction")
			}
		})
	}
}

// TestDeleteExternalAuthEmitsStateTransitionMetric exercises the real DELETE
// handler path and verifies it emits frontend_externalauth_state_transitions_total
// with the expected labels on a successful transaction, and that a request
// rejected before the transaction (already deleting) does not touch the
// counter.
func TestDeleteExternalAuthEmitsStateTransitionMetric(t *testing.T) {
	tests := []struct {
		name             string
		docState         coreapi.ProvisioningState
		expectStatusCode int
		expectIncrement  bool
		expectFromState  string
		expectToState    string
	}{
		{
			name:             "failed state: delete succeeds and emits metric",
			docState:         coreapi.ProvisioningStateFailed,
			expectStatusCode: http.StatusAccepted,
			expectIncrement:  true,
			expectFromState:  "failed",
			// NewOperation forces OperationRequestDelete to ProvisioningStateDeleting,
			// which is what gets written back onto the ExternalAuth document.
			expectToState: "deleting",
		},
		{
			name:             "already deleting: conflict is returned and metric is not touched",
			docState:         coreapi.ProvisioningStateDeleting,
			expectStatusCode: http.StatusConflict,
			expectIncrement:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			externalAuthStateTransitionsTotal.Reset()

			cluster := newExternalAuthMetricsTestCluster(t)
			externalAuth := newExternalAuthMetricsTestDoc(t, tc.docState)

			_, do := newExternalAuthMetricsTestFrontend(t, cluster, externalAuth)

			requestPath := coreapitesting.TestExternalAuthResourceID + "?api-version=" + coreapitesting.TestAPIVersion
			resp := do(http.MethodDelete, requestPath, nil)
			defer resp.Body.Close()

			assert.Equal(t, tc.expectStatusCode, resp.StatusCode)

			resourceType := strings.ToLower(coreapi.ExternalAuthResourceType.String())
			if tc.expectIncrement {
				expected := strings.NewReader(`
					# HELP frontend_externalauth_state_transitions_total Total number of provisioning state transitions for ExternalAuth resources initiated by the frontend.
					# TYPE frontend_externalauth_state_transitions_total counter
					frontend_externalauth_state_transitions_total{from_state="` + tc.expectFromState + `",resource_type="` + resourceType + `",to_state="` + tc.expectToState + `"} 1
				`)
				err := testutil.CollectAndCompare(externalAuthStateTransitionsTotal, expected, "frontend_externalauth_state_transitions_total")
				assert.NoError(t, err, "metric mismatch for frontend_externalauth_state_transitions_total")
			} else {
				assert.Equal(t, 0, testutil.CollectAndCount(externalAuthStateTransitionsTotal),
					"expected no counter series for frontend_externalauth_state_transitions_total when the delete request is rejected before the transaction")
			}
		})
	}
}

// TestDeleteClusterCascadesExternalAuthStateTransitionMetric exercises DELETE
// on the parent Cluster, which cascades child-ExternalAuth deletion through
// the shared addDeleteExternalAuthToTransaction helper on the cluster's own
// transaction (cluster.go's addDeleteClusterToTransaction). It verifies that
// cascaded deletes still emit frontend_externalauth_state_transitions_total
// for the child ExternalAuth, not just the standalone DeleteExternalAuth path.
func TestDeleteClusterCascadesExternalAuthStateTransitionMetric(t *testing.T) {
	externalAuthStateTransitionsTotal.Reset()

	cluster := newExternalAuthMetricsTestCluster(t)
	externalAuth := newExternalAuthMetricsTestDoc(t, coreapi.ProvisioningStateSucceeded)

	_, do := newExternalAuthMetricsTestFrontend(t, cluster, externalAuth)

	requestPath := coreapitesting.TestClusterResourceID + "?api-version=" + coreapitesting.TestAPIVersion
	resp := do(http.MethodDelete, requestPath, nil)
	defer resp.Body.Close()

	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	resourceType := strings.ToLower(coreapi.ExternalAuthResourceType.String())
	expected := strings.NewReader(`
		# HELP frontend_externalauth_state_transitions_total Total number of provisioning state transitions for ExternalAuth resources initiated by the frontend.
		# TYPE frontend_externalauth_state_transitions_total counter
		frontend_externalauth_state_transitions_total{from_state="succeeded",resource_type="` + resourceType + `",to_state="deleting"} 1
	`)
	err := testutil.CollectAndCompare(externalAuthStateTransitionsTotal, expected, "frontend_externalauth_state_transitions_total")
	assert.NoError(t, err, "expected the cascaded child-ExternalAuth delete (via DeleteCluster) to emit frontend_externalauth_state_transitions_total")
}
