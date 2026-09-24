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
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/test-integration/utils/databasemutationhelpers"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

// Exercise the real HTTP CREATE pipeline, not just conversion: injected read-only
// fields must neither reject a valid request nor change its persisted document.
// Reuse the round-trip payloads instead of copying complete artifact scenarios.
func TestCreateIgnoresReadOnlyFields(t *testing.T) {
	defer integrationutils.VerifyNoNewGoLeaks(t)
	const (
		subscriptionID = "6b690bec-0c16-4ecb-8f67-781caf40bba7"
		clusterName    = "readonly-create"
		spoofedID      = "11111111-2222-4333-8444-555555555555"
	)
	armSystemData := map[string]any{
		"createdBy": "arm-creator", "createdByType": "User", "createdAt": "2026-01-02T03:04:05Z",
		"lastModifiedBy": "arm-modifier", "lastModifiedByType": "Application", "lastModifiedAt": "2026-01-02T04:05:06Z",
	}
	armSystemDataJSON, err := json.Marshal(armSystemData)
	require.NoError(t, err)

	for _, tc := range []struct {
		kind       string
		resourceID string
		payload    []byte
	}{
		{"Cluster", clusterResourceID(clusterName), clusterCreatePayload(clusterName, v20261001)},
		// NodePool's latest shape is identical to the existing v20251223 payload.
		{"NodePool", nodePoolResourceID(clusterName, "np01"), nodePoolCreatePayload("np01", v20251223)},
		{"ExternalAuth", externalAuthResourceID(clusterName, "default"), externalAuthCreatePayload(v20261001)},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			var baselineGET map[string]any
			var baselineDocument *cosmosstorageutils.TypedDocument
			for _, inject := range []bool{false, true} {
				name := "WritableOnly"
				if inject {
					name = "WithReadOnlyFields"
				}
				if !t.Run(name, func(t *testing.T) {
					ctx, cancel := context.WithCancel(t.Context())
					ctx = utils.ContextWithLogger(ctx, integrationutils.DefaultLogger(t))
					ti, err := integrationutils.NewIntegrationTestInfoFromEnv(ctx, t, true)
					require.NoError(t, err)
					defer ti.Cleanup(utils.ContextWithLogger(context.Background(), integrationutils.DefaultLogger(t)))
					frontendErr, adminErr := make(chan error, 1), make(chan error, 1)
					go func() { frontendErr <- ti.Frontend.Run(ctx) }()
					go func() { adminErr <- ti.AdminAPI.Run(ctx) }()
					defer func() {
						cancel()
						require.NoError(t, <-frontendErr)
						require.NoError(t, <-adminErr)
					}()
					require.NoError(t, wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
						for _, url := range []string{ti.FrontendURL, ti.AdminURL} {
							request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
							if err != nil {
								return false, err
							}
							response, err := http.DefaultClient.Do(request)
							if err != nil {
								return false, nil
							}
							if err := response.Body.Close(); err != nil {
								return false, err
							}
						}
						return true, nil
					}))

					accessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(ti.FrontendURL, v20261001)
					subscription, err := artifacts.ReadFile("artifacts/VersionCompliance/subscription.json")
					require.NoError(t, err)
					require.NoError(t, accessor.CreateOrUpdate(ctx, "/subscriptions/"+subscriptionID, subscription))
					if tc.kind != "Cluster" {
						createClusterAndComplete(t, ctx, ti, v20261001, subscriptionID, clusterName)
						require.NoError(t, integrationutils.StampRandomClusterServiceID(ctx, ti.ResourcesDBClient(), clusterResourceID(clusterName)))
					}

					var payload map[string]any
					require.NoError(t, json.Unmarshal(tc.payload, &payload))
					properties := payload["properties"].(map[string]any)
					if tc.kind != "ExternalAuth" {
						payload["tags"] = map[string]any{"purpose": "readonly-create"}
					}
					if tc.kind == "Cluster" {
						properties["dns"] = map[string]any{"baseDomainPrefix": "readonly-create"}
					}
					if inject {
						payload["id"] = clusterResourceID("spoofed-resource")
						payload["type"] = "Microsoft.Compute/virtualMachines"
						payload["systemData"] = map[string]any{
							"createdBy": "spoofed-creator", "createdByType": "Application", "createdAt": "2000-01-01T00:00:00Z",
							"lastModifiedBy": "spoofed-modifier", "lastModifiedByType": "User", "lastModifiedAt": "2000-01-02T00:00:00Z",
						}
						properties["provisioningState"] = "Failed"
						status := map[string]any{"conditions": []any{map[string]any{
							"type": "Available", "status": "True", "reason": "Spoofed", "message": "spoofed-status", "lastTransitionTime": "2000-01-01T00:00:00Z",
						}}}
						properties["status"] = status
						if tc.kind != "ExternalAuth" {
							status["activeVersions"] = []any{map[string]any{"version": "99.99"}}
						}
						if tc.kind == "Cluster" {
							identity := payload["identity"].(map[string]any)
							identity["principalId"], identity["tenantId"] = spoofedID, spoofedID
							assigned := identity["userAssignedIdentities"].(map[string]any)
							for id := range assigned {
								assigned[id] = map[string]any{"clientId": spoofedID, "principalId": spoofedID}
							}
							properties["api"].(map[string]any)["url"] = "https://spoofed-api.example.com"
							properties["console"] = map[string]any{"url": "https://spoofed-console.example.com"}
							properties["dns"].(map[string]any)["baseDomain"] = "spoofed.example.com"
							properties["platform"].(map[string]any)["issuerUrl"] = "https://spoofed-issuer.example.com"
						}
					}
					body, err := json.Marshal(payload)
					require.NoError(t, err)
					request, err := http.NewRequestWithContext(ctx, http.MethodPut, ti.FrontendURL+tc.resourceID+"?api-version="+v20261001, bytes.NewReader(body))
					require.NoError(t, err)
					request.Header.Set("Content-Type", "application/json")
					request.Header.Set(coreapi.HeaderNameARMResourceSystemData, string(armSystemDataJSON))
					request.Header.Set(coreapi.HeaderNameHomeTenantID, coreapitesting.TestTenantID)
					request.Header.Set(coreapi.HeaderNameIdentityURL, coreapitesting.TestManagedIdentitiesDataPlaneIdentityURL)
					response, err := http.DefaultClient.Do(request)
					require.NoError(t, err)
					created, err := databasemutationhelpers.DecodeResponseBody(response)
					require.NoError(t, err)
					require.Equal(t, http.StatusCreated, response.StatusCode, "CREATE response: %v", created)
					_, got := getResourceResponse(t, ctx, ti, v20261001, tc.resourceID)
					require.Equal(t, created, got, "CREATE response must match subsequent GET")
					require.Equal(t, tc.resourceID, got["id"])
					require.Equal(t, payload["name"], got["name"])
					require.Equal(t, armSystemData, got["systemData"], "ARM header, not body, owns all systemData fields")
					require.Equal(t, "Accepted", got["properties"].(map[string]any)["provisioningState"])
					if tc.kind != "ExternalAuth" {
						require.Equal(t, map[string]any{"purpose": "readonly-create"}, got["tags"])
					}
					if tc.kind == "Cluster" {
						keysOnly := map[string]any{}
						for id := range payload["identity"].(map[string]any)["userAssignedIdentities"].(map[string]any) {
							keysOnly[id] = map[string]any{}
						}
						assert.Equal(t, map[string]any{"type": "UserAssigned", "userAssignedIdentities": keysOnly}, got["identity"], "keep identity type and map keys, not client-supplied IDs")
					}

					documents, err := ti.ListAllDocuments(ctx)
					require.NoError(t, err)
					var document *cosmosstorageutils.TypedDocument
					for _, candidate := range documents {
						if candidate.ResourceID.String() == tc.resourceID {
							require.Nil(t, document, "CREATE must persist exactly one resource document")
							document = candidate
						}
					}
					require.NotNil(t, document, "successful CREATE must persist the resource")
					var stored map[string]any
					require.NoError(t, json.Unmarshal(document.Properties, &stored))
					require.Equal(t, armSystemData, stored["systemData"], "compare timestamps explicitly; ResourceInstanceEquals ignores them")
					if !inject {
						baselineGET, baselineDocument = got, document
					} else {
						diff, equal := databasemutationhelpers.ResourceInstanceEquals(t, baselineGET, got)
						assert.True(t, equal, "read-only input changed GET or lost writable fields:\n%s", diff)
						diff, equal = databasemutationhelpers.ResourceInstanceEquals(t, baselineDocument, document)
						assert.True(t, equal, "read-only input changed persisted state or lost writable fields:\n%s", diff)
					}

					if tc.kind == "Cluster" {
						// Simulate backend-owned values after CREATE to guard against clearing
						// read-only fields on the output/conversion path as well as the input.
						clusters := ti.ResourcesDBClient().HCPClusters(subscriptionID, "resourceGroupName")
						cluster, err := clusters.Get(ctx, clusterName)
						require.NoError(t, err)
						cluster.ServiceProviderProperties.API.URL = "https://api.server.example.com"
						cluster.ServiceProviderProperties.Console.URL = "https://console.server.example.com"
						cluster.ServiceProviderProperties.DNS.BaseDomain = "server.example.com"
						cluster.ServiceProviderProperties.Platform.IssuerURL = "https://issuer.server.example.com"
						cluster.Status.ActiveVersions = []coreapi.HCPClusterActiveVersion{{Version: "4.20"}}
						clientID, principalID := "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", "bbbbbbbb-cccc-4ddd-8eee-ffffffffffff"
						for id := range cluster.Identity.UserAssignedIdentities {
							cluster.Identity.UserAssignedIdentities[id] = &coreapi.UserAssignedIdentity{ClientID: &clientID, PrincipalID: &principalID}
						}
						_, err = clusters.Replace(ctx, cluster, nil)
						require.NoError(t, err)
						_, served := getResourceResponse(t, ctx, ti, v20261001, tc.resourceID)
						servedProperties := served["properties"].(map[string]any)
						require.Equal(t, "https://api.server.example.com", servedProperties["api"].(map[string]any)["url"])
						require.Equal(t, "https://console.server.example.com", servedProperties["console"].(map[string]any)["url"])
						require.Equal(t, "server.example.com", servedProperties["dns"].(map[string]any)["baseDomain"])
						require.Equal(t, "readonly-create", servedProperties["dns"].(map[string]any)["baseDomainPrefix"])
						require.Equal(t, "https://issuer.server.example.com", servedProperties["platform"].(map[string]any)["issuerUrl"])
						require.Equal(t, []any{map[string]any{"version": "4.20"}}, servedProperties["status"].(map[string]any)["activeVersions"])
						servedIdentities := served["identity"].(map[string]any)["userAssignedIdentities"].(map[string]any)
						require.NotEmpty(t, servedIdentities, "cluster must serve the user-assigned identities it was created with")
						for id, value := range servedIdentities {
							require.Equal(t, map[string]any{"clientId": clientID, "principalId": principalID}, value, "server-populated identity IDs must remain visible for %s", id)
						}
					}
				}) {
					break
				}
			}
		})
	}
}
