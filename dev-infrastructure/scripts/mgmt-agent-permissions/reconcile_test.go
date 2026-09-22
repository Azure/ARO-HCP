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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
)

func TestReconcileSDK(t *testing.T) {
	for _, tc := range []struct {
		name, fault       string
		enabled, current  bool
		wantErr, noDelete bool
		empty             bool
	}{
		{name: "disabled after rotation"},
		{name: "disabled current and rotated principals", current: true},
		{name: "enabled after rotation", enabled: true, current: true},
		{name: "already absent", empty: true},
		{name: "enabled missing current", enabled: true, wantErr: true, noDelete: true},
		{name: "first page denied", fault: "list-denied", wantErr: true, noDelete: true},
		{name: "later page denied", fault: "page-denied", wantErr: true, noDelete: true},
		{name: "verification list denied", fault: "verify-denied", wantErr: true},
		{name: "read denied", fault: "read-denied", wantErr: true, noDelete: true},
		{name: "delete denied", fault: "delete-denied", wantErr: true},
		{name: "recheck principal mismatch", fault: "principal-changed", wantErr: true, noDelete: true},
		{name: "invalid assignment ID", fault: "invalid-id", wantErr: true, noDelete: true},
		{name: "invalid principal", fault: "invalid-principal", wantErr: true, noDelete: true},
		{name: "missing properties", fault: "missing-properties", wantErr: true, noDelete: true},
		{name: "missing scope", fault: "missing-scope", wantErr: true, noDelete: true},
		{name: "empty scope", fault: "empty-scope", wantErr: true, noDelete: true},
		{name: "empty role", fault: "empty-role", wantErr: true, noDelete: true},
		{name: "current principal mismatch", current: true, enabled: true, fault: "current-mismatch", wantErr: true, noDelete: true},
		{name: "assignment remains after delete", fault: "delete-noop", wantErr: true},
		{name: "new stale assignment during verification", fault: "new-assignment", wantErr: true},
		{name: "current disappears", enabled: true, current: true, fault: "current-disappears", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testEnv()
			cfg, err := parseConfig(func(key string) string { return env[key] })
			if err != nil {
				t.Fatal(err)
			}
			cfg.enabled = tc.enabled
			role := func(n int, scope, definition, principal string) armauthorization.RoleAssignment {
				id := fmt.Sprintf("%s/providers/Microsoft.Authorization/roleAssignments/11111111-2222-3333-4444-%012d", scope, n)
				return armauthorization.RoleAssignment{ID: &id, Properties: &armauthorization.RoleAssignmentProperties{
					Scope: to.Ptr(scope), RoleDefinitionID: to.Ptr(definition), PrincipalID: to.Ptr(principal),
				}}
			}
			old := role(1, cfg.clusterID, cfg.roleDefinitionID, "22222222-2222-3333-4444-555555555555")
			duplicate := role(2, cfg.clusterID, cfg.roleDefinitionID, cfg.principalID)
			older := role(9, cfg.clusterID, cfg.roleDefinitionID, "44444444-2222-3333-4444-555555555555")
			current := role(3, cfg.clusterID, cfg.roleDefinitionID, cfg.principalID)
			current.ID = to.Ptr(cfg.assignmentID)
			otherRole := role(4, cfg.clusterID, strings.Replace(cfg.roleDefinitionID, "666666666666", "999999999999", 1), cfg.principalID)
			otherCluster := role(5, cfg.clusterID+"-other", cfg.roleDefinitionID, cfg.principalID)
			child := role(6, cfg.clusterID+"/agentPools/pool", cfg.roleDefinitionID, cfg.principalID)
			parent := role(7, "/subscriptions/"+cfg.subscriptionID, cfg.roleDefinitionID, cfg.principalID)
			switch tc.fault {
			case "invalid-id":
				old.ID = to.Ptr(*otherCluster.ID)
			case "invalid-principal":
				old.Properties.PrincipalID = to.Ptr("invalid")
			case "missing-properties":
				old.Properties = nil
			case "missing-scope":
				old.Properties.Scope = nil
			case "empty-scope":
				old.Properties.Scope = to.Ptr("")
			case "empty-role":
				old.Properties.RoleDefinitionID = to.Ptr("")
			case "current-mismatch":
				current.Properties.PrincipalID = old.Properties.PrincipalID
			}
			roles := []armauthorization.RoleAssignment{otherRole, otherCluster, child, parent}
			if !tc.empty {
				roles = append(roles, old, older, duplicate)
			}
			if tc.current {
				roles = append(roles, current)
			}
			removed := map[string]bool{}
			rounds, pages, deletes := 0, 0, 0
			collection := cfg.clusterID + "/providers/Microsoft.Authorization/roleAssignments"
			options := &azcorearm.ClientOptions{}
			options.Cloud = cloud.Configuration{Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
				cloud.ResourceManager: {Endpoint: "https://management.example.com", Audience: "https://management.example.com"},
			}}
			options.Retry.MaxRetries = -1
			options.Transport = testTransport(func(request *http.Request) (*http.Response, error) {
				if request.URL.Host != "management.example.com" {
					t.Fatalf("unexpected host: %s", request.URL)
				}
				status := http.StatusOK
				var body any
				denied := func() {
					status = http.StatusForbidden
					body = map[string]any{"error": map[string]string{"code": "AuthorizationFailed", "message": "denied"}}
				}
				if request.URL.Path == collection {
					if request.Method != http.MethodGet {
						t.Fatalf("unexpected collection mutation: %s", request.Method)
					}
					index := 0
					if after := request.URL.Query().Get("after"); after != "" {
						var err error
						index, err = strconv.Atoi(after)
						if err != nil {
							t.Fatal(err)
						}
					} else {
						rounds++
						if request.URL.Query().Get("$filter") != "atScope()" {
							t.Fatal("missing exact-scope query")
						}
						if rounds == 2 && tc.fault == "new-assignment" {
							roles = append(roles, role(8, cfg.clusterID, cfg.roleDefinitionID, cfg.principalID))
						}
						if rounds == 2 && tc.fault == "current-disappears" {
							removed[cfg.assignmentID] = true
						}
					}
					pages++
					var active []armauthorization.RoleAssignment
					for _, r := range roles {
						if !removed[*r.ID] {
							active = append(active, r)
						}
					}
					page := map[string]any{"value": []armauthorization.RoleAssignment{}}
					if index < len(active) {
						page["value"] = active[index : index+1]
						if index+1 < len(active) {
							page["nextLink"] = fmt.Sprintf("https://management.example.com%s?after=%d", collection, index+1)
						}
					}
					body = page
					if tc.fault == "list-denied" || (tc.fault == "page-denied" && index > 0) ||
						(tc.fault == "verify-denied" && rounds == 2) {
						denied()
					}
				} else {
					var found *armauthorization.RoleAssignment
					for _, r := range roles {
						if *r.ID == request.URL.Path && !removed[*r.ID] {
							found = &r
							break
						}
					}
					switch request.Method {
					case http.MethodGet:
						if found == nil {
							status = http.StatusNotFound
							body = map[string]any{"error": map[string]string{"code": "RoleAssignmentNotFound"}}
						} else {
							if tc.fault == "principal-changed" {
								p := *found.Properties
								p.PrincipalID = to.Ptr("33333333-2222-3333-4444-555555555555")
								found.Properties = &p
							}
							body = found
						}
						if tc.fault == "read-denied" {
							denied()
						}
					case http.MethodDelete:
						deletes++
						if found == nil || !matches(found.Properties.Scope, cfg.clusterID) ||
							!matches(found.Properties.RoleDefinitionID, cfg.roleDefinitionID) ||
							(cfg.enabled && matches(found.ID, cfg.assignmentID)) {
							t.Fatalf("unsafe deletion: %s", request.URL)
						}
						status = http.StatusNoContent
						if tc.fault == "delete-denied" {
							denied()
						} else if tc.fault != "delete-noop" {
							removed[*found.ID] = true
						}
					default:
						t.Fatalf("unexpected method: %s", request.Method)
					}
				}
				raw, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(string(raw))),
					Header: http.Header{"Content-Type": []string{"application/json"}}, Request: request}, nil
			})
			client, err := armauthorization.NewRoleAssignmentsClient(cfg.subscriptionID, testCredential{}, options)
			if err != nil {
				t.Fatal(err)
			}
			err = reconcile(t.Context(), cfg, client)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v, expected error=%t", err, tc.wantErr)
			}
			if tc.noDelete && deletes != 0 {
				t.Fatalf("deleted %d assignments before validation completed", deletes)
			}
			if !tc.wantErr {
				wantDeletes := 3
				if tc.empty {
					wantDeletes = 0
				} else if tc.current && !tc.enabled {
					wantDeletes++
				}
				if deletes != wantDeletes || rounds != 2 || pages <= rounds {
					t.Fatalf("deletes=%d, rounds=%d, pages=%d", deletes, rounds, pages)
				}
				if err := reconcile(t.Context(), cfg, client); err != nil || deletes != wantDeletes {
					t.Fatalf("non-idempotent cleanup: deletes=%d error=%v", deletes, err)
				}
			}
		})
	}
}
