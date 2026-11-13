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

package stamp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testStamp = "1"

	// testHostedClusterNamespace and testControlPlaneNamespace are a matching
	// pair under the ocm-<env>-<32 hex> grammar, with testHostedClusterName as
	// the domain prefix that joins them.
	testHostedClusterNamespace = "ocm-production-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testControlPlaneNamespace  = "ocm-production-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-hcp1"
	testHostedClusterName      = "hcp1"
)

func newNamespaceDeletionTestContext(t *testing.T) context.Context {
	t.Helper()
	return utils.ContextWithLogger(context.Background(), testr.New(t))
}

func newMockFleetDBWith(t *testing.T, ctx context.Context, resources []any) *fleetcosmosstoragetesting.MockFleetDBClient {
	t.Helper()
	if len(resources) == 0 {
		return fleetcosmosstoragetesting.NewMockFleetDBClient()
	}
	mockFleetDB, err := fleetcosmosstoragetesting.NewMockFleetDBClientWithResources(ctx, resources)
	require.NoError(t, err)
	return mockFleetDB
}

func newNamespaceDeletion(t *testing.T, stampIdentifier, hostedClusterNamespaceName string) *fleetapi.NamespaceDeletion {
	t.Helper()
	resourceID, err := fleetapihelpers.ToNamespaceDeletionResourceID(stampIdentifier, strings.ToLower(hostedClusterNamespaceName))
	require.NoError(t, err)
	return &fleetapi.NamespaceDeletion{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(stampIdentifier),
		},
		Spec: fleetapi.NamespaceDeletionSpec{
			ManagementClusterName:      fleetapi.ManagementClusterResourceName,
			HostedClusterNamespaceName: hostedClusterNamespaceName,
			HostedClusterName:          testHostedClusterName,
			RequestReason:              "AROSLSRE-2477 sweep",
		},
	}
}

// postNamespaceDeletion drives the handler with no ownership listers wired.
//
// That is deliberate for the rejection tests below: every one of them must be
// refused *before* the gate would run, so if a change ever reorders a check
// past the gate the test fails with an ownership error instead of silently
// passing.
func postNamespaceDeletion(t *testing.T, ctx context.Context, mockFleetDB *fleetcosmosstoragetesting.MockFleetDBClient, stampIdentifier, managementClusterName, body string) (*httptest.ResponseRecorder, error) {
	t.Helper()
	handler := NewNamespaceDeletionCreateHandler(mockFleetDB, nil, nil)
	req := httptest.NewRequest(http.MethodPost,
		"/admin/v1/stamps/"+stampIdentifier+"/managementclusters/"+managementClusterName+"/namespacedeletions",
		strings.NewReader(body))
	req.SetPathValue("stampIdentifier", stampIdentifier)
	req.SetPathValue("managementClusterName", managementClusterName)
	req = req.WithContext(ctx)
	recorder := httptest.NewRecorder()
	return recorder, handler.ServeHTTP(recorder, req)
}

func requireCloudError(t *testing.T, err error, wantStatus int, wantSubstring string) {
	t.Helper()
	require.Error(t, err)
	var cloudErr *coreapi.CloudError
	require.True(t, errors.As(err, &cloudErr), "expected CloudError but got %T: %v", err, err)
	require.Equal(t, wantStatus, cloudErr.StatusCode)
	require.Contains(t, cloudErr.Error(), wantSubstring)
}

func TestNamespaceDeletionCreateHandlerRejections(t *testing.T) {
	for _, tc := range []struct {
		name          string
		managementCl  string
		body          string
		wantStatus    int
		wantSubstring string
	}{
		{
			name:          "malformed body",
			managementCl:  fleetapi.ManagementClusterResourceName,
			body:          "{",
			wantStatus:    http.StatusBadRequest,
			wantSubstring: "could not be deserialized",
		},
		{
			name:          "obsolete delete flag",
			managementCl:  fleetapi.ManagementClusterResourceName,
			body:          `{"hostedClusterNamespaceName":"` + testHostedClusterNamespace + `","hostedClusterName":"hcp1","requestReason":"r","deleteHostedCluster":true}`,
			wantStatus:    http.StatusBadRequest,
			wantSubstring: "unknown field",
		},
		{
			name:          "old namespace field is not accepted",
			managementCl:  fleetapi.ManagementClusterResourceName,
			body:          `{"namespaceName":"` + testHostedClusterNamespace + `","hostedClusterName":"hcp1","requestReason":"r"}`,
			wantStatus:    http.StatusBadRequest,
			wantSubstring: "unknown field",
		},
		{
			name:          "non-singleton management cluster",
			managementCl:  "not-default",
			body:          `{"hostedClusterNamespaceName":"` + testHostedClusterNamespace + `","hostedClusterName":"hcp1","requestReason":"r"}`,
			wantStatus:    http.StatusBadRequest,
			wantSubstring: "singleton",
		},
		{
			// A platform namespace is never a valid target, and the denylist is
			// evaluated before the grammar so a parser bug cannot put one in
			// reach.
			name:          "protected namespace",
			managementCl:  fleetapi.ManagementClusterResourceName,
			body:          `{"hostedClusterNamespaceName":"kube-system","hostedClusterName":"hcp1","requestReason":"r"}`,
			wantStatus:    http.StatusBadRequest,
			wantSubstring: "ProtectedNamespace",
		},
		{
			name:          "protected namespace by prefix",
			managementCl:  fleetapi.ManagementClusterResourceName,
			body:          `{"hostedClusterNamespaceName":"openshift-monitoring","hostedClusterName":"hcp1","requestReason":"r"}`,
			wantStatus:    http.StatusBadRequest,
			wantSubstring: "ProtectedNamespace",
		},
		{
			name:          "namespace does not match the grammar",
			managementCl:  fleetapi.ManagementClusterResourceName,
			body:          `{"hostedClusterNamespaceName":"some-random-namespace","hostedClusterName":"hcp1","requestReason":"r"}`,
			wantStatus:    http.StatusBadRequest,
			wantSubstring: "is not a HostedCluster namespace",
		},
		{
			name:          "control plane namespace is not accepted",
			managementCl:  fleetapi.ManagementClusterResourceName,
			body:          `{"hostedClusterNamespaceName":"` + testControlPlaneNamespace + `","hostedClusterName":"hcp1","requestReason":"r"}`,
			wantStatus:    http.StatusBadRequest,
			wantSubstring: "is a control plane namespace",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := newNamespaceDeletionTestContext(t)
			mockFleetDB := newMockFleetDBWith(t, ctx, nil)

			_, err := postNamespaceDeletion(t, ctx, mockFleetDB, testStamp, tc.managementCl, tc.body)

			requireCloudError(t, err, tc.wantStatus, tc.wantSubstring)
		})
	}
}

func TestNamespaceDeletionCreateHandlerAcceptsHostedClusterNamespace(t *testing.T) {
	ctx := newNamespaceDeletionTestContext(t)
	mockFleetDB := newMockFleetDBWith(t, ctx, nil)
	body := `{"hostedClusterNamespaceName":"` + testHostedClusterNamespace + `","hostedClusterName":"` + testHostedClusterName + `","requestReason":"r"}`

	_, err := postNamespaceDeletion(t, ctx, mockFleetDB, testStamp, fleetapi.ManagementClusterResourceName, body)

	// Without a management cluster in the mock the handler stops at the
	// 404, which is past both the denylist and the grammar.
	requireCloudError(t, err, http.StatusNotFound, "Management cluster")
}

func TestNamespaceDeletionGetHandler(t *testing.T) {
	ctx := newNamespaceDeletionTestContext(t)
	request := newNamespaceDeletion(t, testStamp, testHostedClusterNamespace)
	mockFleetDB := newMockFleetDBWith(t, ctx, []any{request})

	handler := NewNamespaceDeletionGetHandler(mockFleetDB)
	req := httptest.NewRequest(http.MethodGet, "/admin/v1/stamps/"+testStamp+
		"/managementclusters/default/namespacedeletions/"+testHostedClusterNamespace, nil)
	req.SetPathValue("stampIdentifier", testStamp)
	req.SetPathValue("managementClusterName", fleetapi.ManagementClusterResourceName)
	req.SetPathValue("name", strings.ToLower(testHostedClusterNamespace))
	req = req.WithContext(ctx)
	recorder := httptest.NewRecorder()

	require.NoError(t, handler.ServeHTTP(recorder, req), "GET on an existing request must succeed")
	require.Equal(t, http.StatusOK, recorder.Code)

	var response NamespaceDeletionGetResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response), "GET response must be valid JSON")
	require.Equal(t, testHostedClusterNamespace, response.HostedClusterNamespaceName)
	require.Equal(t, testHostedClusterName, response.HostedClusterName)
	require.Equal(t, fleetapi.ManagementClusterResourceName, response.ManagementClusterName)
	require.Empty(t, response.NamespaceUID)
}

func TestNamespaceDeletionGetHandlerNotFound(t *testing.T) {
	ctx := newNamespaceDeletionTestContext(t)
	mockFleetDB := newMockFleetDBWith(t, ctx, nil)

	handler := NewNamespaceDeletionGetHandler(mockFleetDB)
	req := httptest.NewRequest(http.MethodGet, "/admin/v1/stamps/"+testStamp+
		"/managementclusters/default/namespacedeletions/missing", nil)
	req.SetPathValue("stampIdentifier", testStamp)
	req.SetPathValue("managementClusterName", fleetapi.ManagementClusterResourceName)
	req.SetPathValue("name", "missing")
	req = req.WithContext(ctx)

	err := handler.ServeHTTP(httptest.NewRecorder(), req)

	requireCloudError(t, err, http.StatusNotFound, "not found")
}
