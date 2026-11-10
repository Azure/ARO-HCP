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

func (transport ownerTransport) Do(request *http.Request) (*http.Response, error) {
	return transport(request)
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
