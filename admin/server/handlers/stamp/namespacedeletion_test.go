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
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	apiMeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/coreapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/fleetapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/storage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/storage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
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

func TestNamespaceDeletionCreatePostRetriesSettledStop(t *testing.T) {
	ctx := newNamespaceDeletionTestContext(t)
	request := newNamespaceDeletion(t, testStamp, testHostedClusterNamespace)
	db := newMockFleetDBWith(t, ctx, []any{newManagementCluster(t, testStamp), request})
	crud := db.Stamps().ManagementClusters(testStamp).NamespaceDeletions()
	stored, err := crud.Get(ctx, testHostedClusterNamespace)
	require.NoError(t, err)
	stopped := stored.DeepCopy()
	stopped.Status.NamespaceUID = "namespace-uid"
	stopped.Status.HostedClusterUID = "hosted-cluster-uid"
	apiMeta.SetStatusCondition(&stopped.Status.Conditions, metav1.Condition{
		Type: fleetapi.ConditionTypeNamespaceDeletionStopped, Status: metav1.ConditionTrue,
		Reason: "ManifestWorkPresent", Message: "matching work remains",
	})
	completed := metav1.NewTime(time.Now().Add(-time.Hour))
	stopped.Status.CompletedAt = &completed
	_, err = crud.Replace(ctx, stopped, stored, nil)
	require.NoError(t, err)
	post := func(body string) (*httptest.ResponseRecorder, error) {
		return postNamespaceDeletion(t, ctx, db, testStamp, fleetapi.ManagementClusterResourceName, body)
	}
	retry := `{"hostedClusterNamespaceName":"` + testHostedClusterNamespace + `","hostedClusterName":"hcp1","requestReason":"removed stale ManifestWork"}`
	w, err := post(retry)
	require.NoError(t, err)
	require.Equal(t, http.StatusAccepted, w.Code)
	stored, err = crud.Get(ctx, testHostedClusterNamespace)
	require.NoError(t, err)
	require.EqualValues(t, 1, stored.Spec.RetryGeneration)
	require.Equal(t, "removed stale ManifestWork", stored.Spec.RetryReason)
	require.Equal(t, "namespace-uid", stored.Status.NamespaceUID)
	require.Equal(t, "hosted-cluster-uid", stored.Status.HostedClusterUID)
	require.True(t, apiMeta.IsStatusConditionTrue(stored.Status.Conditions, fleetapi.ConditionTypeNamespaceDeletionStopped),
		"the old attempt remains stopped until the backend transitions it")
	w, err = post(retry)
	require.NoError(t, err)
	require.Equal(t, http.StatusAccepted, w.Code, "same pending retry must be idempotent")

	differentReason := `{"hostedClusterNamespaceName":"` + testHostedClusterNamespace + `","hostedClusterName":"hcp1","requestReason":"different reason"}`
	_, err = post(differentReason)
	requireCloudError(t, err, http.StatusConflict, "different reason is already pending")
	wrongTarget := `{"hostedClusterNamespaceName":"` + testHostedClusterNamespace + `","hostedClusterName":"other","requestReason":"retry"}`
	_, err = post(wrongTarget)
	requireCloudError(t, err, http.StatusConflict, "identity does not match")
}

func TestNamespaceDeletionCreatePostReturnsActiveRequest(t *testing.T) {
	ctx := newNamespaceDeletionTestContext(t)
	request := newNamespaceDeletion(t, testStamp, testHostedClusterNamespace)
	db := newMockFleetDBWith(t, ctx, []any{newManagementCluster(t, testStamp), request})
	body := `{"hostedClusterNamespaceName":"` + testHostedClusterNamespace + `","hostedClusterName":"hcp1","requestReason":"retry"}`
	w, err := postNamespaceDeletion(t, ctx, db, testStamp, fleetapi.ManagementClusterResourceName, body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, w.Code)
}

// createRaceFleetClient simulates a second POST winning between the first
// request's Get and Create, while retaining the real stored winner for the
// conflict handler to inspect.
type createRaceFleetClient struct {
	fleetcosmosstorage.FleetDBClient
	crud *createRaceNamespaceDeletionCRUD
}

func (c *createRaceFleetClient) Stamps() fleetcosmosstorage.StampsCRUD {
	return createRaceStampsCRUD{StampsCRUD: c.FleetDBClient.Stamps(), crud: c.crud}
}

type createRaceStampsCRUD struct {
	fleetcosmosstorage.StampsCRUD
	crud *createRaceNamespaceDeletionCRUD
}

func (c createRaceStampsCRUD) ManagementClusters(stamp string) fleetcosmosstorage.ManagementClustersCRUD {
	return createRaceManagementClustersCRUD{ManagementClustersCRUD: c.StampsCRUD.ManagementClusters(stamp), crud: c.crud}
}

type createRaceManagementClustersCRUD struct {
	fleetcosmosstorage.ManagementClustersCRUD
	crud *createRaceNamespaceDeletionCRUD
}

func (c createRaceManagementClustersCRUD) NamespaceDeletions() cosmosstorageutils.ValidatingResourceCRUD[fleetapi.NamespaceDeletion, *fleetapi.NamespaceDeletion] {
	return c.crud
}

type createRaceNamespaceDeletionCRUD struct {
	cosmosstorageutils.ValidatingResourceCRUD[fleetapi.NamespaceDeletion, *fleetapi.NamespaceDeletion]
	firstGet bool
}

func (c *createRaceNamespaceDeletionCRUD) Get(ctx context.Context, name string) (*fleetapi.NamespaceDeletion, error) {
	if !c.firstGet {
		c.firstGet = true
		return nil, &azcore.ResponseError{StatusCode: http.StatusNotFound}
	}
	return c.ValidatingResourceCRUD.Get(ctx, name)
}

func (c *createRaceNamespaceDeletionCRUD) Create(context.Context, *fleetapi.NamespaceDeletion, *azcosmos.ItemOptions) (*fleetapi.NamespaceDeletion, error) {
	return nil, &azcore.ResponseError{StatusCode: http.StatusConflict}
}

func TestNamespaceDeletionCreateRaceValidatesStoredWinner(t *testing.T) {
	ctx := newNamespaceDeletionTestContext(t)
	winner := newNamespaceDeletion(t, testStamp, testHostedClusterNamespace)
	db := newMockFleetDBWith(t, ctx, []any{newManagementCluster(t, testStamp), winner})
	client := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
	clients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	managementCluster, err := fleetapihelpers.ToManagementClusterResourceID(testStamp)
	require.NoError(t, err)
	clients.Register(managementCluster, client)
	resourcesDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	post := func(hostedClusterName string) (*httptest.ResponseRecorder, error) {
		crud := &createRaceNamespaceDeletionCRUD{
			ValidatingResourceCRUD: db.Stamps().ManagementClusters(testStamp).NamespaceDeletions(),
		}
		handler := NewNamespaceDeletionCreateHandler(&createRaceFleetClient{FleetDBClient: db, crud: crud}, resourcesDB, clients)
		body := `{"hostedClusterNamespaceName":"` + testHostedClusterNamespace + `","hostedClusterName":"` + hostedClusterName + `","requestReason":"cleanup"}`
		req := httptest.NewRequest(http.MethodPost, "/namespacedeletions", strings.NewReader(body)).WithContext(ctx)
		req.SetPathValue("stampIdentifier", testStamp)
		req.SetPathValue("managementClusterName", fleetapi.ManagementClusterResourceName)
		w := httptest.NewRecorder()
		return w, handler.ServeHTTP(w, req)
	}
	_, err = post("other")
	requireCloudError(t, err, http.StatusConflict, "identity does not match")
	w, err := post(testHostedClusterName)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, w.Code)
}
