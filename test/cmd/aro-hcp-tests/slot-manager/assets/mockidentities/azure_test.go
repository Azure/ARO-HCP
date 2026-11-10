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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/microsoft/kiota-abstractions-go/authentication"
	msgraph "github.com/microsoftgraph/msgraph-sdk-go"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
)

type testCredential struct{ token string }

func (credential testCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	token := credential.token
	if token == "" {
		token = "unit-test"
	}
	return azcore.AccessToken{Token: token, ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type ownerTransport func(*http.Request) (*http.Response, error)

func (transport ownerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func (transport ownerTransport) Do(request *http.Request) (*http.Response, error) {
	return transport(request)
}

type graphExchange struct {
	method, path, response string
	status                 int
	query                  map[string]string
	body                   map[string]string
}

func graphTestClient(t *testing.T, exchanges []graphExchange) *msgraph.GraphServiceClient {
	t.Helper()
	index := 0
	transport := ownerTransport(func(request *http.Request) (*http.Response, error) {
		if index >= len(exchanges) {
			t.Fatalf("unexpected Graph request %s %s", request.Method, request.URL)
		}
		expected := exchanges[index]
		index++
		if request.Method != expected.method || request.URL.Path != "/v1.0/"+expected.path || request.URL.Host != "graph.microsoft.com" {
			t.Fatalf("Graph request %d: got %s %s, want %s %s", index, request.Method, request.URL, expected.method, expected.path)
		}
		for key, value := range expected.query {
			if request.URL.Query().Get(key) != value {
				t.Fatalf("Graph query %s = %q, want %q", key, request.URL.Query().Get(key), value)
			}
		}
		if expected.body != nil {
			var body map[string]string
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body) != len(expected.body) {
				t.Fatalf("unexpected Graph mutation fields: %v", body)
			}
			for key, value := range expected.body {
				if body[key] != value {
					t.Fatalf("Graph mutation field %s = %q, want %q", key, body[key], value)
				}
			}
		}
		status := expected.status
		if status == 0 {
			status = http.StatusOK
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, ContentLength: int64(len(expected.response)), Body: io.NopCloser(strings.NewReader(expected.response)), Request: request}, nil
	})
	adapter, err := msgraph.NewGraphRequestAdapterWithParseNodeFactoryAndSerializationWriterFactoryAndHttpClient(&authentication.AnonymousAuthenticationProvider{}, nil, nil, &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if index != len(exchanges) {
			t.Errorf("consumed %d of %d expected Graph requests", index, len(exchanges))
		}
	})
	return msgraph.NewGraphServiceClient(adapter)
}

func TestGraphResolveReadOnly(t *testing.T) {
	name := "aro-hcp-slot-test-msi"
	applicationID, clientID, principalID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	application := fmt.Sprintf(`{"id":%q,"appId":%q,"displayName":%q}`, applicationID, clientID, name)
	principal := fmt.Sprintf(`{"id":%q,"appId":%q,"accountEnabled":true}`, principalID, clientID)
	applicationPath := "applications(uniqueName='" + name + "')"
	principalPath := "servicePrincipals(appId='" + clientID + "')"
	lookup := graphExchange{method: "GET", path: applicationPath, response: application}
	matches := graphExchange{method: "GET", path: "applications", response: fmt.Sprintf(`{"value":[{"id":%q}]}`, applicationID), query: map[string]string{"$filter": "displayName eq '" + name + "'", "$select": "id"}}
	servicePrincipal := graphExchange{method: "GET", path: principalPath, response: principal}
	missing := `{"error":{"code":"Request_ResourceNotFound","message":"missing test object"}}`
	for _, test := range []struct {
		name      string
		exchanges []graphExchange
		wantError string
	}{
		{name: "exact identities", exchanges: []graphExchange{lookup, matches, servicePrincipal}},
		{name: "missing application", exchanges: []graphExchange{{method: "GET", path: applicationPath, status: 404, response: missing}}, wantError: "missing test object"},
		{name: "mismatched application name", exchanges: []graphExchange{{method: "GET", path: applicationPath, response: strings.Replace(application, name, "wrong", 1)}}, wantError: "unexpected identity"},
		{name: "missing application client ID", exchanges: []graphExchange{{method: "GET", path: applicationPath, response: strings.Replace(application, clientID, "", 1)}}, wantError: "unexpected identity"},
		{name: "missing display name result", exchanges: []graphExchange{lookup, {method: "GET", path: "applications", response: `{"value":[]}`}}, wantError: "missing or ambiguous"},
		{name: "different display name match", exchanges: []graphExchange{lookup, {method: "GET", path: "applications", response: fmt.Sprintf(`{"value":[{"id":%q}]}`, uuid.NewString())}}, wantError: "missing or ambiguous"},
		{name: "ambiguous later page", exchanges: []graphExchange{lookup,
			{method: "GET", path: "applications", response: fmt.Sprintf(`{"value":[{"id":%q}],"@odata.nextLink":"https://graph.microsoft.com/v1.0/applications?$skiptoken=next"}`, applicationID)},
			{method: "GET", path: "applications", query: map[string]string{"$skiptoken": "next"}, response: fmt.Sprintf(`{"value":[{"id":%q}]}`, uuid.NewString())}}, wantError: "ambiguous (2 matches)"},
		{name: "missing service principal", exchanges: []graphExchange{lookup, matches, {method: "GET", path: principalPath, status: 404, response: missing}}, wantError: "missing test object"},
		{name: "disabled service principal", exchanges: []graphExchange{lookup, matches, {method: "GET", path: principalPath, response: strings.Replace(principal, "true", "false", 1)}}, wantError: "disabled or mismatched"},
		{name: "mismatched service principal", exchanges: []graphExchange{lookup, matches, {method: "GET", path: principalPath, response: strings.Replace(principal, clientID, uuid.NewString(), 1)}}, wantError: "disabled or mismatched"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &azureStore{graph: graphTestClient(t, test.exchanges)}
			actual, err := store.Resolve(t.Context(), name, false)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("got error %v, want %q", err, test.wantError)
				}
				return
			}
			if err != nil || actual != (leaseenv.MockPrincipal{ClientID: clientID, PrincipalID: principalID, CertificateName: name}) {
				t.Fatalf("resolved identity = %+v, error %v", actual, err)
			}
		})
	}
}

func TestGraphResolvePartialApplyRetry(t *testing.T) {
	name := "aro-hcp-slot-test-backend"
	applicationID, clientID, principalID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	applicationPath := "applications(uniqueName='" + name + "')"
	principalPath := "servicePrincipals(appId='" + clientID + "')"
	application := fmt.Sprintf(`{"id":%q,"appId":%q,"displayName":%q}`, applicationID, clientID, name)
	principal := fmt.Sprintf(`{"id":%q,"appId":%q,"accountEnabled":true}`, principalID, clientID)
	missing := `{"error":{"code":"Request_ResourceNotFound","message":"missing test object"}}`
	lookup := graphExchange{method: "GET", path: applicationPath, response: application}
	matches := graphExchange{method: "GET", path: "applications", response: fmt.Sprintf(`{"value":[{"id":%q}]}`, applicationID)}
	missingPrincipal := graphExchange{method: "GET", path: principalPath, status: 404, response: missing}
	store := &azureStore{graph: graphTestClient(t, []graphExchange{
		{method: "GET", path: applicationPath, status: 404, response: missing},
		{method: "PATCH", path: applicationPath, response: application, body: map[string]string{"@odata.type": "#microsoft.graph.application", "displayName": name, "signInAudience": "AzureADMyOrg"}},
		matches, missingPrincipal,
		{method: "PATCH", path: principalPath, status: 403, response: `{"error":{"code":"Authorization_RequestDenied","message":"injected partial failure"}}`, body: map[string]string{"@odata.type": "#microsoft.graph.servicePrincipal", "displayName": name}},
		lookup, matches, missingPrincipal,
		{method: "PATCH", path: principalPath, response: principal, body: map[string]string{"@odata.type": "#microsoft.graph.servicePrincipal", "displayName": name}},
		lookup, matches, {method: "GET", path: principalPath, response: principal},
	})}
	if _, err := store.Resolve(t.Context(), name, true); err == nil || !strings.Contains(err.Error(), "injected partial failure") {
		t.Fatalf("expected partial provisioning failure, got %v", err)
	}
	repaired, err := store.Resolve(t.Context(), name, true)
	if err != nil {
		t.Fatal(err)
	}
	again, err := store.Resolve(t.Context(), name, true)
	if err != nil || again != repaired || again.PrincipalID != principalID || again.ClientID != clientID {
		t.Fatalf("idempotent reconciliation = %+v, previous %+v, error %v", again, repaired, err)
	}
}

func TestAuthorizeOwner(t *testing.T) {
	subscription := uuid.NewString()
	objectID := uuid.NewString()
	groupID := uuid.NewString()
	scope := "/subscriptions/" + subscription
	ownerRole := scope + "/providers/Microsoft.Authorization/roleDefinitions/8e3af657-a8ff-443c-a75c-2fe8c4bcb635"
	token := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"oid":"`+objectID+`"}`)) + "."
	for _, test := range []struct {
		name      string
		principal string
		scope     string
		role      string
		condition string
		status    int
		wantError bool
	}{
		{name: "direct subscription Owner", principal: objectID, scope: scope, role: ownerRole},
		{name: "inherited Owner", principal: objectID, scope: "/providers/Microsoft.Management/managementGroups/parent", role: ownerRole},
		{name: "group Owner", principal: groupID, scope: scope, role: ownerRole},
		{name: "inherited transitive group Owner", principal: groupID, scope: "/providers/Microsoft.Management/managementGroups/parent", role: ownerRole},
		{name: "conditional Owner", principal: objectID, scope: scope, role: ownerRole, condition: "condition", wantError: true},
		{name: "Contributor is not Owner", principal: objectID, scope: scope, role: scope + "/providers/Microsoft.Authorization/roleDefinitions/" + contributorRole, wantError: true},
		{name: "no applicable active assignment", wantError: true},
		{name: "unreadable assignments", status: http.StatusForbidden, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			transport := ownerTransport(func(request *http.Request) (*http.Response, error) {
				requests++
				if request.Method != http.MethodGet || request.URL.Path != scope+"/providers/Microsoft.Authorization/roleAssignments" {
					t.Fatalf("Owner preflight must only list customer subscription roles: %s %s", request.Method, request.URL.Path)
				}
				if request.URL.Query().Get("$filter") != "atScope() and assignedTo('"+objectID+"')" {
					t.Fatalf("Owner preflight must include caller group membership and exclude descendant scopes: %s", request.URL.RawQuery)
				}
				status := http.StatusOK
				if test.status != 0 {
					status = test.status
				}
				payload := `{"value":[]}`
				if test.principal != "" {
					payload = fmt.Sprintf(`{"value":[null,{"properties":{"principalId":%q,"scope":%q,"roleDefinitionId":%q,"condition":%q}}]}`, test.principal, test.scope, test.role, test.condition)
				}
				if requests == 1 && status == http.StatusOK {
					payload = fmt.Sprintf(`{"value":[],"nextLink":%q}`, request.URL.String()+"&page=2")
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(payload)), Request: request}, nil
			})
			store := &azureStore{credential: testCredential{token: token}, armOptions: &azcorearm.ClientOptions{ClientOptions: azcore.ClientOptions{Transport: transport}}}
			err := store.AuthorizeOwner(context.Background(), subscription)
			if (err != nil) != test.wantError {
				t.Fatalf("Owner preflight error = %v, want error %t", err, test.wantError)
			}
			if test.status == 0 && requests != 2 {
				t.Fatalf("expected both role-assignment pages, got %d requests", requests)
			}
			if test.wantError && test.status == 0 && !strings.Contains(err.Error(), "activate eligible Owner access") {
				t.Fatalf("missing actionable Owner instruction: %v", err)
			}
		})
	}
}

type roleTransport struct {
	testing    *testing.T
	bundle     leaseenv.MockIdentityBundlePayload
	missing    bool
	wrongScope bool
	writes     int
}

func (transport *roleTransport) Do(request *http.Request) (*http.Response, error) {
	scope := "/subscriptions/" + transport.bundle.SubscriptionID
	roleID := scope + "/providers/Microsoft.Authorization/roleDefinitions/msi-custom"
	var payload any
	switch {
	case request.Method == http.MethodPut:
		if !strings.HasPrefix(request.URL.Path, scope+"/providers/Microsoft.Authorization/roleAssignments/") {
			transport.testing.Fatal("write outside customer subscription")
		}
		transport.writes++
		payload = map[string]any{"id": request.URL.Path, "properties": map[string]string{"principalId": transport.bundle.MSI.PrincipalID, "roleDefinitionId": roleID}}
	case request.Method != http.MethodGet:
		transport.testing.Fatalf("unexpected mutation: %s", request.Method)
	case strings.HasSuffix(request.URL.Path, "/roleDefinitions"):
		payload = map[string]any{"value": []any{map[string]any{"id": roleID}}}
	case strings.HasSuffix(request.URL.Path, "/roleAssignments"):
		var assignments []any
		for index, binding := range bindings(transport.bundle, "msi-custom") {
			if transport.missing && index == 0 {
				continue
			}
			assignmentScope := scope
			if transport.wrongScope {
				assignmentScope += "/resourceGroups/foreign"
			}
			assignments = append(assignments, map[string]any{"properties": map[string]string{"scope": assignmentScope, "principalId": binding.principal, "roleDefinitionId": scope + "/providers/Microsoft.Authorization/roleDefinitions/" + binding.role}})
		}
		payload = map[string]any{"value": assignments}
	default:
		transport.testing.Fatalf("unexpected URL: %s", request.URL.Path)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(data))), Request: request}, nil
}

func TestRBACReadOnlyScopeAndRepair(t *testing.T) {
	principal := func(name string) leaseenv.MockPrincipal {
		return leaseenv.MockPrincipal{ClientID: uuid.NewString(), PrincipalID: uuid.NewString(), CertificateName: name}
	}
	bundle := leaseenv.MockIdentityBundlePayload{SlotName: "slot", SubscriptionID: uuid.NewString(), MSI: principal("msi"), Backend: principal("backend"), ClustersService: principal("cs")}
	transport := &roleTransport{testing: t, bundle: bundle}
	store := &azureStore{credential: testCredential{}, armOptions: &azcorearm.ClientOptions{ClientOptions: azcore.ClientOptions{Transport: transport}}}
	if err := store.Roles(context.Background(), bundle.SubscriptionID, bundle, false); err != nil {
		t.Fatal(err)
	}
	if err := store.Roles(context.Background(), bundle.SubscriptionID, bundle, true); err != nil || transport.writes != 0 {
		t.Fatalf("healthy apply wrote RBAC: %v", err)
	}
	transport.missing = true
	if err := store.Roles(context.Background(), bundle.SubscriptionID, bundle, false); err == nil || transport.writes != 0 {
		t.Fatal("admission repaired RBAC")
	}
	if err := store.Roles(context.Background(), bundle.SubscriptionID, bundle, true); err != nil || transport.writes != 1 {
		t.Fatalf("repair did not add exactly one missing assignment: %v", err)
	}
	transport.missing = false
	transport.wrongScope = true
	if err := store.Roles(context.Background(), bundle.SubscriptionID, bundle, false); err == nil || transport.writes != 1 {
		t.Fatal("accepted wrong-scope assignments or wrote during validation")
	}
}
