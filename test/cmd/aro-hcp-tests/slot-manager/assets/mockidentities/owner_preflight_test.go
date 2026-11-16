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

package mockidentities

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
	"github.com/Azure/ARO-HCP/tooling/entra-app-credentials/pkg/owners"
)

type ownerLookup struct{ owners.Directory }

func (ownerLookup) Lookup(context.Context, string) (*owners.Object, error) {
	return &owners.Object{}, nil
}

func TestOwnerGroupPreflight(t *testing.T) {
	group, owner := uuid.NewString(), uuid.NewString()
	path := "groups/" + group + "/transitiveMembers"
	user := fmt.Sprintf(`{"@odata.type":"#microsoft.graph.user","id":%q}`, owner)
	page := `{"value":[` + user + `]}`
	link := "https://graph.microsoft.com/v1.0/" + path + "?$skiptoken=opaque%2B%2F%3D%3D&$top=2"
	for _, scenario := range []string{"exact", "paginated", "mismatch", "empty", "incomplete", "nil-object", "missing-id", "read-error", "page-error", "cycle", "foreign-link"} {
		t.Run(scenario, func(t *testing.T) {
			exchanges := []graphExchange{{method: "GET", path: path, response: page}}
			switch scenario {
			case "mismatch", "empty":
				exchanges[0].response = `{"value":[]}`
			case "incomplete":
				exchanges[0].response = `{}`
			case "nil-object":
				exchanges[0].response = `{"value":[null]}`
			case "missing-id":
				exchanges[0].response = `{"value":[{"@odata.type":"#microsoft.graph.user"}]}`
			case "read-error":
				exchanges[0].status = 403
				exchanges[0].response = `{"error":{"code":"Authorization_RequestDenied","message":"denied"}}`
			case "foreign-link":
				exchanges[0].response = `{"value":[],"@odata.nextLink":"https://example.invalid/page"}`
			case "paginated", "page-error", "cycle":
				exchanges[0].response = fmt.Sprintf(`{"value":[],"@odata.nextLink":%q}`, link)
				second := graphExchange{method: "GET", path: path, query: map[string]string{"$skiptoken": "opaque+/==", "$top": "2"}, response: page}
				if scenario == "page-error" {
					second.status = 403
					second.response = `{"error":{"code":"Authorization_RequestDenied","message":"denied"}}`
				}
				if scenario == "cycle" {
					second.response = exchanges[0].response
				}
				exchanges = append(exchanges, second)
			}
			directory := strictOwnerDirectory{Directory: ownerLookup{}, store: &azureStore{graph: graphTestClient(t, exchanges)}}
			err := owners.Check(t.Context(), directory, group, []string{owner})
			if (err == nil) != (scenario == "exact" || scenario == "paginated") {
				t.Fatalf("group preflight %s: %v", scenario, err)
			}
		})
	}
}

func TestOwnerGroupSupportedObjectTypes(t *testing.T) {
	group, user, principal := uuid.NewString(), uuid.NewString(), uuid.NewString()
	objects := []string{
		fmt.Sprintf(`{"@odata.type":"#microsoft.graph.user","id":%q}`, user),
		fmt.Sprintf(`{"@odata.type":"#microsoft.graph.servicePrincipal","id":%q}`, principal),
	}
	for _, kind := range []string{"group", "device", "orgContact"} {
		objects = append(objects, fmt.Sprintf(`{"@odata.type":"#microsoft.graph.%s","id":%q}`, kind, uuid.NewString()))
	}
	directory := strictOwnerDirectory{Directory: ownerLookup{}, store: &azureStore{graph: graphTestClient(t, []graphExchange{{
		method: "GET", path: "groups/" + group + "/transitiveMembers",
		response: `{"value":[` + strings.Join(objects, ",") + `]}`,
	}})}}
	if err := owners.Check(t.Context(), directory, group, []string{user, principal}); err != nil {
		t.Fatalf("supported directory types with limited id/type fields: %v", err)
	}
}

func TestPoolOwnerSettingsFailBeforeCredentials(t *testing.T) {
	t.Setenv(OwnerIDsEnv, "null")
	t.Setenv(OwnersGroupIDEnv, "null")
	pool := slots.Pool{SlotCount: 1, SlotAssets: slots.SlotAssets{MockIdentities: &slots.MockIdentitiesAsset{Allocation: slots.AllocationDedicated}}}
	for _, apply := range []bool{false, true} {
		if err := NewHandler().pools(t.Context(), assets.PoolRequest{Pools: []slots.Pool{pool}}, apply); err == nil {
			t.Fatal("invalid settings passed pool preflight")
		}
	}
	pool.SlotAssets.E2EIdentities = &slots.E2EIdentitiesAsset{Provisioning: slots.IdentityProvisioningUnmanaged}
	if err := NewHandler().pools(t.Context(), assets.PoolRequest{Pools: []slots.Pool{pool}}, true); err != nil {
		t.Fatalf("unselected unmanaged pool required credentials: %v", err)
	}
}

func TestGraphUpsertConcurrentCreation(t *testing.T) {
	name := "aro-hcp-slot-test-msi"
	applicationID, clientID, principalID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	appPath := "applications(uniqueName='" + name + "')"
	spPath := "servicePrincipals(appId='" + clientID + "')"
	missing := `{"error":{"code":"Request_ResourceNotFound","message":"missing"}}`
	store := &azureStore{graph: graphTestClient(t, []graphExchange{
		{method: "GET", path: appPath, status: 404, response: missing},
		{method: "PATCH", path: appPath, status: 204},
		{method: "GET", path: appPath, response: fmt.Sprintf(`{"id":%q,"appId":%q,"displayName":%q}`, applicationID, clientID, name)},
		{method: "GET", path: "applications", response: fmt.Sprintf(`{"value":[{"id":%q}]}`, applicationID)},
		{method: "GET", path: spPath, status: 404, response: missing},
		{method: "PATCH", path: spPath, status: 204},
		{method: "GET", path: spPath, response: fmt.Sprintf(`{"id":%q,"appId":%q,"accountEnabled":true}`, principalID, clientID)},
	})}
	principal, err := store.Resolve(t.Context(), name, true)
	if err != nil || principal.ClientID != clientID || principal.PrincipalID != principalID {
		t.Fatalf("204 upsert reread: %+v %v", principal, err)
	}
}
