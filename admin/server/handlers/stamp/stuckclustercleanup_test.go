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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testStamp     = "1"
	testNamespace = "ocm-production-abc123-mycluster"
)

func newStuckCleanupTestContext(t *testing.T) context.Context {
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

func newStuckClusterCleanup(t *testing.T, stampIdentifier, namespaceName string) *fleetapi.StuckClusterCleanup {
	t.Helper()
	name := StuckClusterCleanupNameFor(namespaceName)
	resourceID, err := fleetapihelpers.ToStuckClusterCleanupResourceID(stampIdentifier, name)
	require.NoError(t, err)
	return &fleetapi.StuckClusterCleanup{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(stampIdentifier),
		},
		Spec: fleetapi.StuckClusterCleanupSpec{
			Mode:          fleetapi.StuckClusterCleanupModeInspect,
			NamespaceName: namespaceName,
			RequestReason: "AROSLSRE-2477 sweep",
		},
	}
}

func postCleanup(t *testing.T, ctx context.Context, mockFleetDB *fleetcosmosstoragetesting.MockFleetDBClient, stampIdentifier, managementClusterName, body string) (*httptest.ResponseRecorder, error) {
	t.Helper()
	handler := NewStuckClusterCleanupCreateHandler(mockFleetDB)
	req := httptest.NewRequest(http.MethodPost,
		"/admin/v1/stamps/"+stampIdentifier+"/managementclusters/"+managementClusterName+"/stuckclustercleanups",
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

func TestStuckClusterCleanupCreateHandler(t *testing.T) {
	t.Parallel()

	validBody := `{"namespaceName":"` + testNamespace + `","requestReason":"AROSLSRE-2477 sweep"}`

	tests := []struct {
		name                  string
		stampIdentifier       string
		managementClusterName string
		body                  string
		withManagementCluster bool
		expectedStatusCode    int
		expectedError         string
	}{
		{
			name:                  "creates an inspect request",
			stampIdentifier:       testStamp,
			managementClusterName: fleetapi.ManagementClusterResourceName,
			body:                  validBody,
			withManagementCluster: true,
			expectedStatusCode:    http.StatusCreated,
		},
		{
			name:                  "invalid stamp identifier",
			stampIdentifier:       "",
			managementClusterName: fleetapi.ManagementClusterResourceName,
			body:                  validBody,
			expectedStatusCode:    http.StatusBadRequest,
			expectedError:         "Invalid stamp identifier",
		},
		{
			// The management cluster is a singleton and the Cosmos scope comes
			// from the stamp alone, so a non-default name would otherwise be
			// silently ignored rather than rejected.
			name:                  "non-default management cluster name is rejected",
			stampIdentifier:       testStamp,
			managementClusterName: "secondary",
			body:                  validBody,
			withManagementCluster: true,
			expectedStatusCode:    http.StatusBadRequest,
			expectedError:         "singleton",
		},
		{
			name:                  "management cluster not found",
			stampIdentifier:       testStamp,
			managementClusterName: fleetapi.ManagementClusterResourceName,
			body:                  validBody,
			withManagementCluster: false,
			expectedStatusCode:    http.StatusNotFound,
			expectedError:         "not found",
		},
		{
			name:                  "malformed JSON body",
			stampIdentifier:       testStamp,
			managementClusterName: fleetapi.ManagementClusterResourceName,
			body:                  `{"namespaceName":`,
			withManagementCluster: true,
			expectedStatusCode:    http.StatusBadRequest,
			expectedError:         "could not be deserialized",
		},
		{
			name:                  "unparseable cluster resource ID",
			stampIdentifier:       testStamp,
			managementClusterName: fleetapi.ManagementClusterResourceName,
			body:                  `{"namespaceName":"` + testNamespace + `","requestReason":"r","clusterResourceID":"not-an-arm-id"}`,
			withManagementCluster: true,
			expectedStatusCode:    http.StatusBadRequest,
			expectedError:         "Invalid cluster resource ID",
		},
		{
			// Validation runs in the CRUD layer; the handler must surface it as
			// a 400 naming the field rather than an opaque 500.
			name:                  "missing request reason is a validation failure",
			stampIdentifier:       testStamp,
			managementClusterName: fleetapi.ManagementClusterResourceName,
			body:                  `{"namespaceName":"` + testNamespace + `"}`,
			withManagementCluster: true,
			expectedStatusCode:    http.StatusBadRequest,
			expectedError:         "rejected",
		},
		{
			name:                  "namespace name that is not a DNS label is rejected",
			stampIdentifier:       testStamp,
			managementClusterName: fleetapi.ManagementClusterResourceName,
			body:                  `{"namespaceName":"Not A Namespace","requestReason":"r"}`,
			withManagementCluster: true,
			expectedStatusCode:    http.StatusBadRequest,
			expectedError:         "rejected",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := newStuckCleanupTestContext(t)

			var resources []any
			if tt.withManagementCluster {
				resources = append(resources, newManagementCluster(t, tt.stampIdentifier))
			}
			mockFleetDB := newMockFleetDBWith(t, ctx, resources)

			recorder, handlerErr := postCleanup(t, ctx, mockFleetDB, tt.stampIdentifier, tt.managementClusterName, tt.body)

			if len(tt.expectedError) > 0 {
				requireCloudError(t, handlerErr, tt.expectedStatusCode, tt.expectedError)
				return
			}

			require.NoError(t, handlerErr)
			require.Equal(t, tt.expectedStatusCode, recorder.Code)

			var resp StuckClusterCleanupCreateResponse
			require.NoError(t, json.NewDecoder(recorder.Body).Decode(&resp))
			require.True(t, resp.Created)
			require.Equal(t, testNamespace, resp.NamespaceName)
			require.Equal(t, string(fleetapi.StuckClusterCleanupModeInspect), resp.Mode,
				"an operator who does not ask for Execute must get a dry run")
			require.NotEmpty(t, resp.Name)
		})
	}
}

// Repeating a request for the same namespace must address the same document
// rather than piling up, because the name is a stable function of the namespace.
func TestStuckClusterCleanupCreateHandlerIsIdempotent(t *testing.T) {
	t.Parallel()
	ctx := newStuckCleanupTestContext(t)

	mockFleetDB := newMockFleetDBWith(t, ctx, []any{newManagementCluster(t, testStamp)})
	body := `{"namespaceName":"` + testNamespace + `","requestReason":"AROSLSRE-2477 sweep"}`

	firstRecorder, err := postCleanup(t, ctx, mockFleetDB, testStamp, fleetapi.ManagementClusterResourceName, body)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, firstRecorder.Code)

	var first StuckClusterCleanupCreateResponse
	require.NoError(t, json.NewDecoder(firstRecorder.Body).Decode(&first))
	require.True(t, first.Created)

	secondRecorder, err := postCleanup(t, ctx, mockFleetDB, testStamp, fleetapi.ManagementClusterResourceName, body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, secondRecorder.Code, "a repeat request must not be a second creation")

	var second StuckClusterCleanupCreateResponse
	require.NoError(t, json.NewDecoder(secondRecorder.Body).Decode(&second))
	require.False(t, second.Created)
	require.Equal(t, first.Name, second.Name, "the same namespace must address the same document")
}

// Mode defaults to Inspect, and an explicit Execute is recorded verbatim so the
// controller — not the handler — decides what Execute is allowed to do.
func TestStuckClusterCleanupCreateHandlerRecordsExplicitMode(t *testing.T) {
	t.Parallel()
	ctx := newStuckCleanupTestContext(t)

	mockFleetDB := newMockFleetDBWith(t, ctx, []any{newManagementCluster(t, testStamp)})
	body := `{"namespaceName":"` + testNamespace + `","requestReason":"r","mode":"Execute"}`

	recorder, err := postCleanup(t, ctx, mockFleetDB, testStamp, fleetapi.ManagementClusterResourceName, body)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, recorder.Code)

	var resp StuckClusterCleanupCreateResponse
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&resp))
	require.Equal(t, string(fleetapi.StuckClusterCleanupModeExecute), resp.Mode)
}

// A caller must not be able to assert the namespace UID: establishing it is the
// controller's job, and accepting one would let a caller pin an identity that
// was never observed.
func TestStuckClusterCleanupCreateHandlerIgnoresCallerSuppliedUID(t *testing.T) {
	t.Parallel()
	ctx := newStuckCleanupTestContext(t)

	mockFleetDB := newMockFleetDBWith(t, ctx, []any{newManagementCluster(t, testStamp)})
	body := `{"namespaceName":"` + testNamespace + `","requestReason":"r","namespaceUID":"11111111-2222-3333-4444-555555555555"}`

	recorder, err := postCleanup(t, ctx, mockFleetDB, testStamp, fleetapi.ManagementClusterResourceName, body)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, recorder.Code)

	stored, getErr := mockFleetDB.Stamps().ManagementClusters(testStamp).StuckClusterCleanups().
		Get(ctx, StuckClusterCleanupNameFor(testNamespace))
	require.NoError(t, getErr)
	require.Empty(t, stored.Spec.NamespaceUID,
		"the request body must not be able to set namespaceUID")
}

func TestStuckClusterCleanupGetHandler(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                  string
		stampIdentifier       string
		managementClusterName string
		cleanupName           string
		withCleanup           bool
		expectedStatusCode    int
		expectedError         string
	}{
		{
			name:                  "returns findings",
			stampIdentifier:       testStamp,
			managementClusterName: fleetapi.ManagementClusterResourceName,
			cleanupName:           StuckClusterCleanupNameFor(testNamespace),
			withCleanup:           true,
			expectedStatusCode:    http.StatusOK,
		},
		{
			name:                  "cleanup not found",
			stampIdentifier:       testStamp,
			managementClusterName: fleetapi.ManagementClusterResourceName,
			cleanupName:           "ns-doesnotexist",
			expectedStatusCode:    http.StatusNotFound,
			expectedError:         "not found",
		},
		{
			name:                  "invalid stamp identifier",
			stampIdentifier:       "",
			managementClusterName: fleetapi.ManagementClusterResourceName,
			cleanupName:           "ns-whatever",
			expectedStatusCode:    http.StatusBadRequest,
			expectedError:         "Invalid stamp identifier",
		},
		{
			name:                  "non-default management cluster name is rejected",
			stampIdentifier:       testStamp,
			managementClusterName: "secondary",
			cleanupName:           "ns-whatever",
			expectedStatusCode:    http.StatusBadRequest,
			expectedError:         "singleton",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := newStuckCleanupTestContext(t)

			var resources []any
			if tt.withCleanup {
				resources = append(resources, newStuckClusterCleanup(t, tt.stampIdentifier, testNamespace))
			}
			mockFleetDB := newMockFleetDBWith(t, ctx, resources)

			handler := NewStuckClusterCleanupGetHandler(mockFleetDB)
			req := httptest.NewRequest(http.MethodGet,
				"/admin/v1/stamps/"+tt.stampIdentifier+"/managementclusters/"+tt.managementClusterName+"/stuckclustercleanups/"+tt.cleanupName, nil)
			req.SetPathValue("stampIdentifier", tt.stampIdentifier)
			req.SetPathValue("managementClusterName", tt.managementClusterName)
			req.SetPathValue("name", tt.cleanupName)
			req = req.WithContext(ctx)
			recorder := httptest.NewRecorder()

			handlerErr := handler.ServeHTTP(recorder, req)

			if len(tt.expectedError) > 0 {
				requireCloudError(t, handlerErr, tt.expectedStatusCode, tt.expectedError)
				return
			}

			require.NoError(t, handlerErr)
			require.Equal(t, tt.expectedStatusCode, recorder.Code)

			var resp StuckClusterCleanupGetResponse
			require.NoError(t, json.NewDecoder(recorder.Body).Decode(&resp))
			require.Equal(t, testNamespace, resp.NamespaceName)
			require.Equal(t, tt.cleanupName, resp.Name)
		})
	}
}

// The GET is the operator's only window onto the inspection, so everything the
// controller records has to survive the round trip.
func TestStuckClusterCleanupGetHandlerReportsFullFindings(t *testing.T) {
	t.Parallel()
	ctx := newStuckCleanupTestContext(t)

	// Seed through the real lifecycle rather than fabricating the end state:
	// the create validator forbids a caller-supplied UID, so the UID has to
	// arrive the way the controller delivers it — as an update that pins it.
	cleanup := newStuckClusterCleanup(t, testStamp, testNamespace)
	permitted := false
	observedStatus := fleetapi.StuckClusterCleanupStatus{
		Phase:   fleetapi.StuckClusterCleanupPhaseInspected,
		Message: "inspection complete; ownership could not be fully confirmed",
		Conditions: []metav1.Condition{{
			Type:               fleetapi.ConditionTypeStuckClusterCleanupPreflightPassed,
			Status:             metav1.ConditionUnknown,
			Reason:             "ChecksIncomplete",
			Message:            "preflight fails closed",
			LastTransitionTime: metav1.Now(),
		}},
		Preflight: &fleetapi.StuckClusterCleanupPreflightResult{
			RefusalReason: "ChecksIncomplete",
		},
		ObservedNamespace: &fleetapi.ObservedTerminatingNamespace{
			UID:            "11111111-2222-3333-4444-555555555555",
			Phase:          "Terminating",
			SpecFinalizers: []string{"kubernetes"},
			RemainingContent: []fleetapi.RemainingGroupResource{{
				Group:                "policy",
				Resource:             "poddisruptionbudgets",
				KubeApplierPermitted: &permitted,
			}},
		},
	}

	mockFleetDB := newMockFleetDBWith(t, ctx, []any{cleanup})

	cleanupCRUD := mockFleetDB.Stamps().ManagementClusters(testStamp).StuckClusterCleanups()
	stored, err := cleanupCRUD.Get(ctx, StuckClusterCleanupNameFor(testNamespace))
	require.NoError(t, err)

	pinned := stored.DeepCopy()
	pinned.Spec.NamespaceUID = "11111111-2222-3333-4444-555555555555"
	pinned.Status = observedStatus
	_, err = cleanupCRUD.Replace(ctx, pinned, stored, nil)
	require.NoError(t, err, "the controller must be able to pin the UID it observed")

	handler := NewStuckClusterCleanupGetHandler(mockFleetDB)
	name := StuckClusterCleanupNameFor(testNamespace)
	req := httptest.NewRequest(http.MethodGet,
		"/admin/v1/stamps/"+testStamp+"/managementclusters/default/stuckclustercleanups/"+name, nil)
	req.SetPathValue("stampIdentifier", testStamp)
	req.SetPathValue("managementClusterName", fleetapi.ManagementClusterResourceName)
	req.SetPathValue("name", name)
	req = req.WithContext(ctx)
	recorder := httptest.NewRecorder()

	require.NoError(t, handler.ServeHTTP(recorder, req))
	require.Equal(t, http.StatusOK, recorder.Code)

	var resp StuckClusterCleanupGetResponse
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&resp))

	require.Equal(t, string(fleetapi.StuckClusterCleanupPhaseInspected), resp.Phase)
	require.Equal(t, "11111111-2222-3333-4444-555555555555", resp.NamespaceUID)
	require.NotNil(t, resp.Preflight)
	require.Equal(t, "ChecksIncomplete", resp.Preflight.RefusalReason)
	require.NotNil(t, resp.ObservedNamespace)
	require.Equal(t, "Terminating", resp.ObservedNamespace.Phase)
	require.Len(t, resp.ObservedNamespace.RemainingContent, 1)

	remaining := resp.ObservedNamespace.RemainingContent[0]
	require.Equal(t, "policy", remaining.Group)
	require.Equal(t, "poddisruptionbudgets", remaining.Resource)
	require.NotNil(t, remaining.KubeApplierPermitted,
		"whether a blocker is deletable today is the finding the mutation release is scoped on")
	require.False(t, *remaining.KubeApplierPermitted)
}

func TestStuckClusterCleanupNameForIsStableAndPathSafe(t *testing.T) {
	t.Parallel()

	first := StuckClusterCleanupNameFor(testNamespace)
	require.Equal(t, first, StuckClusterCleanupNameFor(testNamespace),
		"a repeated request must address the same document")
	require.Equal(t, first, StuckClusterCleanupNameFor(strings.ToUpper(testNamespace)),
		"namespace names are case-insensitive for addressing purposes")
	require.NotEqual(t, first, StuckClusterCleanupNameFor("some-other-namespace"))
	require.NotContains(t, first, "/", "the name must be a single ARM path segment")
}
