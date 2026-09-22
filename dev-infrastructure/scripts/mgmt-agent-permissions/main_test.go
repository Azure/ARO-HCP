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

package main

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
)

func testEnv() map[string]string {
	sub := "00000000-1111-2222-3333-444444444444"
	cluster := "/subscriptions/" + sub + "/resourceGroups/management/providers/Microsoft.ContainerService/managedClusters/cluster"
	return map[string]string{
		"NODE_MITIGATION_ENABLED": "false",
		"SUBSCRIPTION_ID":         sub,
		"PRINCIPAL_ID":            "00000000-1111-2222-3333-555555555555",
		"CLUSTER_ID":              cluster,
		"ROLE_DEFINITION_ID":      "/subscriptions/" + sub + "/providers/Microsoft.Authorization/roleDefinitions/00000000-1111-2222-3333-666666666666",
		"ROLE_ASSIGNMENT_ID":      cluster + "/providers/Microsoft.Authorization/roleAssignments/00000000-1111-2222-3333-777777777777",
	}
}

func TestParseConfig(t *testing.T) {
	base := testEnv()
	for _, enabled := range []string{"true", "false"} {
		env := maps.Clone(base)
		env["NODE_MITIGATION_ENABLED"] = enabled
		cfg, err := parseConfig(func(key string) string { return env[key] })
		if err != nil || cfg.enabled != (enabled == "true") {
			t.Fatalf("enabled=%s config=%+v error=%v", enabled, cfg, err)
		}
	}
	for key := range base {
		t.Run("missing/"+key, func(t *testing.T) {
			env := maps.Clone(base)
			delete(env, key)
			if _, err := parseConfig(func(key string) string { return env[key] }); err == nil {
				t.Fatal("missing input was accepted")
			}
		})
	}
	for _, tc := range []struct{ key, value string }{
		{"NODE_MITIGATION_ENABLED", "invalid"},
		{"SUBSCRIPTION_ID", "not-a-subscription"},
		{"PRINCIPAL_ID", "not-a-principal"},
		{"CLUSTER_ID", strings.Replace(base["CLUSTER_ID"], base["SUBSCRIPTION_ID"], base["PRINCIPAL_ID"], 1)},
		{"CLUSTER_ID", strings.Replace(base["CLUSTER_ID"], "managedClusters", "agentPools", 1)},
		{"ROLE_DEFINITION_ID", strings.Replace(base["ROLE_DEFINITION_ID"], base["SUBSCRIPTION_ID"], base["PRINCIPAL_ID"], 1)},
		{"ROLE_DEFINITION_ID", base["ROLE_DEFINITION_ID"] + "/child/wrong"},
		{"ROLE_ASSIGNMENT_ID", strings.Replace(base["ROLE_ASSIGNMENT_ID"], "/cluster/", "/another-cluster/", 1)},
		{"ROLE_ASSIGNMENT_ID", strings.Replace(base["ROLE_ASSIGNMENT_ID"], "roleAssignments", "roleDefinitions", 1)},
	} {
		t.Run(tc.key+"/"+tc.value, func(t *testing.T) {
			env := maps.Clone(base)
			env[tc.key] = tc.value
			if _, err := parseConfig(func(key string) string { return env[key] }); err == nil {
				t.Fatal("invalid target was accepted")
			}
		})
	}
}

type response struct {
	verb string
	role armauthorization.RoleAssignment
	err  error
}

type fakeAssignments struct {
	t         *testing.T
	id        string
	responses []response
}

func (f *fakeAssignments) next(verb, id string) response {
	f.t.Helper()
	if id != f.id || len(f.responses) == 0 || f.responses[0].verb != verb {
		f.t.Fatalf("unexpected request: %s %s", verb, id)
	}
	result := f.responses[0]
	f.responses = f.responses[1:]
	return result
}

func (f *fakeAssignments) GetByID(_ context.Context, id string, _ *armauthorization.RoleAssignmentsClientGetByIDOptions) (armauthorization.RoleAssignmentsClientGetByIDResponse, error) {
	r := f.next(http.MethodGet, id)
	return armauthorization.RoleAssignmentsClientGetByIDResponse{RoleAssignment: r.role}, r.err
}

func (f *fakeAssignments) DeleteByID(_ context.Context, id string, _ *armauthorization.RoleAssignmentsClientDeleteByIDOptions) (armauthorization.RoleAssignmentsClientDeleteByIDResponse, error) {
	r := f.next(http.MethodDelete, id)
	return armauthorization.RoleAssignmentsClientDeleteByIDResponse{}, r.err
}

type testCredential struct{}

func (testCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) Do(request *http.Request) (*http.Response, error) { return f(request) }

func TestRevokeSDKTargetsExactAssignment(t *testing.T) {
	env := testEnv()
	cfg, err := parseConfig(func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	methods := []string{http.MethodGet, http.MethodDelete, http.MethodGet}
	calls := 0
	options := &azcorearm.ClientOptions{}
	options.Cloud = cloud.Configuration{Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
		cloud.ResourceManager: {Endpoint: "https://management.example.com", Audience: "https://management.example.com"},
	}}
	options.Retry.MaxRetries = -1
	options.Transport = testTransport(func(request *http.Request) (*http.Response, error) {
		if calls >= len(methods) || request.Method != methods[calls] || request.URL.Host != "management.example.com" || request.URL.Path != cfg.assignmentID {
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL)
		}
		status, body := http.StatusNoContent, ""
		switch calls {
		case 0:
			raw, err := json.Marshal(map[string]any{"id": cfg.assignmentID, "properties": map[string]string{
				"scope": cfg.clusterID, "principalId": cfg.principalID, "roleDefinitionId": cfg.roleDefinitionID,
			}})
			if err != nil {
				t.Fatal(err)
			}
			status, body = http.StatusOK, string(raw)
		case 2:
			status, body = http.StatusNotFound, `{"error":{"code":"RoleAssignmentNotFound","message":"absent"}}`
		}
		calls++
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)),
			Header: http.Header{"Content-Type": []string{"application/json"}}, Request: request}, nil
	})
	client, err := armauthorization.NewRoleAssignmentsClient(cfg.subscriptionID, testCredential{}, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := revoke(t.Context(), cfg, client); err != nil {
		t.Fatal(err)
	}
	if calls != len(methods) {
		t.Fatalf("got %d requests, expected read/delete/verify", calls)
	}
}
func TestRevoke(t *testing.T) {
	env := testEnv()
	cfg, err := parseConfig(func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	role := armauthorization.RoleAssignment{ID: to.Ptr(cfg.assignmentID), Properties: &armauthorization.RoleAssignmentProperties{
		PrincipalID: to.Ptr(cfg.principalID), RoleDefinitionID: to.Ptr(cfg.roleDefinitionID), Scope: to.Ptr(cfg.clusterID),
	}}
	missing := &azcore.ResponseError{StatusCode: http.StatusNotFound}
	denied := &azcore.ResponseError{StatusCode: http.StatusForbidden}
	read := response{verb: http.MethodGet, role: role}
	absent := response{verb: http.MethodGet, err: missing}
	for _, tc := range []struct {
		name      string
		wantErr   bool
		responses []response
	}{
		{name: "already absent", responses: []response{absent}},
		{name: "revoke and verify", responses: []response{read, {verb: http.MethodDelete}, absent}},
		{name: "concurrent removal", responses: []response{read, {verb: http.MethodDelete, err: missing}, absent}},
		{name: "read denied", wantErr: true, responses: []response{{verb: http.MethodGet, err: denied}}},
		{name: "delete denied", wantErr: true, responses: []response{read, {verb: http.MethodDelete, err: denied}}},
		{name: "verification denied", wantErr: true, responses: []response{read, {verb: http.MethodDelete}, {verb: http.MethodGet, err: denied}}},
		{name: "assignment remains", wantErr: true, responses: []response{read, {verb: http.MethodDelete}, read}},
		{name: "missing properties", wantErr: true, responses: []response{{verb: http.MethodGet, role: armauthorization.RoleAssignment{ID: role.ID}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := cfg
			client := &fakeAssignments{t: t, id: c.assignmentID, responses: tc.responses}
			err := revoke(t.Context(), c, client)
			if (err != nil) != tc.wantErr || len(client.responses) != 0 {
				t.Fatalf("error=%v remaining requests=%d", err, len(client.responses))
			}
		})
	}
	for _, field := range []string{"id", "principal", "role", "scope"} {
		t.Run("mismatch/"+field, func(t *testing.T) {
			changed := role
			properties := *role.Properties
			changed.Properties = &properties
			switch field {
			case "id":
				changed.ID = to.Ptr("another-assignment")
			case "principal":
				properties.PrincipalID = to.Ptr("another-principal")
			case "role":
				properties.RoleDefinitionID = to.Ptr("another-role")
			case "scope":
				properties.Scope = to.Ptr("another-cluster")
			}
			client := &fakeAssignments{t: t, id: cfg.assignmentID, responses: []response{{verb: http.MethodGet, role: changed}}}
			if err := revoke(t.Context(), cfg, client); err == nil {
				t.Fatal("mismatched assignment was accepted")
			}
		})
	}
}
