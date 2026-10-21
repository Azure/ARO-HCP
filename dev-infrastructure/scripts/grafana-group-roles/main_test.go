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
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestParseEnvConfig_EmptyRolesIsNoOp(t *testing.T) {
	// Empty GRAFANA_ROLES is a valid no-op even with the other inputs missing.
	c, err := parseEnvConfig(envFrom(map[string]string{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(c.roles) != 0 {
		t.Fatalf("expected no roles, got %d", len(c.roles))
	}
}

func TestParseEnvConfig_MissingRequiredWithRoles(t *testing.T) {
	_, err := parseEnvConfig(envFrom(map[string]string{
		"GRAFANA_ROLES": "7697afa1-161f-40ea-b020-a019879e0d87/Group/Viewer",
	}))
	if err == nil {
		t.Fatal("expected error for missing required env vars, got nil")
	}
}

func TestParseEnvConfig_Valid(t *testing.T) {
	c, err := parseEnvConfig(envFrom(map[string]string{
		"SUBSCRIPTION_ID": "d63a7f3d-bb5a-4594-915e-810ad566839e",
		"RESOURCE_GROUP":  "global-shared-resources",
		"GRAFANA_NAME":    "arohcp-stg2",
		"GRAFANA_ROLES":   "7697afa1-161f-40ea-b020-a019879e0d87/Group/Viewer",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(c.roles) != 1 {
		t.Fatalf("expected 1 role, got %d", len(c.roles))
	}
	got := c.roles[0]
	want := roleAssignment{
		principalID:   "7697afa1-161f-40ea-b020-a019879e0d87",
		principalType: "Group",
		roleName:      "Viewer",
	}
	if got != want {
		t.Fatalf("parsed role = %+v, want %+v", got, want)
	}
}

func TestParseRoles(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    int
		wantErr bool
	}{
		{name: "empty", raw: "", want: 0},
		{name: "whitespace only", raw: "   ", want: 0},
		{name: "single group viewer", raw: "abc/Group/Viewer", want: 1},
		{name: "multiple", raw: "abc/Group/Viewer def/ServicePrincipal/Admin", want: 2},
		{name: "too few parts", raw: "abc/Group", wantErr: true},
		{name: "too many parts", raw: "abc/Group/Viewer/extra", wantErr: true},
		{name: "empty field", raw: "abc//Viewer", wantErr: true},
		{name: "unknown role", raw: "abc/Group/Superuser", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRoles(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (parsed %+v)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != tt.want {
				t.Fatalf("got %d roles, want %d", len(got), tt.want)
			}
		})
	}
}

func TestGrafanaResourceID(t *testing.T) {
	got := grafanaResourceID("sub", "rg", "arohcp-stg2")
	want := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Dashboard/grafana/arohcp-stg2"
	if got != want {
		t.Fatalf("grafanaResourceID = %q, want %q", got, want)
	}
}

func TestRoleDefinitionID(t *testing.T) {
	got := roleDefinitionID("sub", "60921a7e-fef1-4a43-9b16-a26c52ad4769")
	want := "/subscriptions/sub/providers/Microsoft.Authorization/roleDefinitions/60921a7e-fef1-4a43-9b16-a26c52ad4769"
	if got != want {
		t.Fatalf("roleDefinitionID = %q, want %q", got, want)
	}
}

func TestAssignmentNameDeterministic(t *testing.T) {
	scope := grafanaResourceID("sub", "rg", "arohcp-stg2")
	roleDef := roleDefinitionID("sub", grafanaBuiltInRoles["Viewer"])
	a := assignmentName(scope, "principal", roleDef)
	b := assignmentName(scope, "principal", roleDef)
	if a != b {
		t.Fatalf("assignmentName not deterministic: %q != %q", a, b)
	}
	if c := assignmentName(scope, "other-principal", roleDef); c == a {
		t.Fatal("assignmentName should differ for a different principal")
	}
}

func TestMapPrincipalType(t *testing.T) {
	tests := []struct {
		in      string
		want    armauthorization.PrincipalType
		wantErr bool
	}{
		{in: "Group", want: armauthorization.PrincipalTypeGroup},
		{in: "group", want: armauthorization.PrincipalTypeGroup},
		{in: "ServicePrincipal", want: armauthorization.PrincipalTypeServicePrincipal},
		{in: "User", want: armauthorization.PrincipalTypeUser},
		{in: "Bogus", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := mapPrincipalType(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got nil", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("mapPrincipalType(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
