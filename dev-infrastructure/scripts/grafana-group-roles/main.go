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

// grafana-group-roles assigns Azure Managed Grafana roles to the principals
// listed in GRAFANA_ROLES on a given Grafana resource.
//
// Unlike the built-in grafana-roles ARM step, whose EV2 deployment identity can
// only assign roles to ServicePrincipal principals, this tool runs as an EV2
// Shell step under an identity that is also allowed to assign Group and User
// principals. It is written in Go rather than `az` because the EV2 Shell runner
// image ships no Azure CLI.
//
// Inputs (environment variables):
//
//	SUBSCRIPTION_ID - subscription holding the Grafana resource
//	RESOURCE_GROUP  - resource group holding the Grafana resource
//	GRAFANA_NAME    - Grafana resource name
//	GRAFANA_ROLES   - space-separated "principalId/principalType/role" list
//	                  (same format as modules/grafana/instance.bicep). Empty is a no-op.
//	LOG_VERBOSITY   - optional slog verbosity (default 0)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
)

// grafanaBuiltInRoles maps a Grafana role name to its built-in role definition
// GUID. Must match modules/grafana/instance.bicep.
var grafanaBuiltInRoles = map[string]string{
	"Contributor": "5c2d7e57-b7c2-4d8a-be4f-82afa42c6e95",
	"Admin":       "22926164-76b3-42b3-bc55-97df8dab3e41",
	"Viewer":      "60921a7e-fef1-4a43-9b16-a26c52ad4769",
}

func main() {
	verbosity := 0
	if v := os.Getenv("LOG_VERBOSITY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			verbosity = n
		}
	}
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		// slog levels are spaced by 4 (Debug=-4, Info=0), so scale verbosity by 4:
		// LOG_VERBOSITY=1 enables Debug, higher values go progressively more verbose.
		Level: slog.Level(verbosity * -4),
	})
	slog.SetDefault(slog.New(handler).With("component", "grafana-group-roles"))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if err := run(ctx); err != nil {
		slog.Error("run failed", "error", err.Error())
		os.Exit(1)
	}
}

// config holds all inputs sourced from environment variables.
type config struct {
	subscriptionID string
	resourceGroup  string
	grafanaName    string
	roles          []roleAssignment
}

// roleAssignment is one parsed GRAFANA_ROLES entry.
type roleAssignment struct {
	principalID   string
	principalType string
	roleName      string
}

// parseEnvConfig builds a config from environment variables only. It does not
// call any external tools or APIs, which makes it safe to unit-test.
func parseEnvConfig(env func(string) string) (*config, error) {
	c := &config{
		subscriptionID: env("SUBSCRIPTION_ID"),
		resourceGroup:  env("RESOURCE_GROUP"),
		grafanaName:    env("GRAFANA_NAME"),
	}

	roles, err := parseRoles(env("GRAFANA_ROLES"))
	if err != nil {
		return nil, err
	}
	c.roles = roles

	// GRAFANA_ROLES empty is a valid no-op; the other inputs are only required
	// when there is something to assign.
	if len(c.roles) == 0 {
		return c, nil
	}

	missing := []string{}
	for _, kv := range []struct{ key, val string }{
		{"SUBSCRIPTION_ID", c.subscriptionID},
		{"RESOURCE_GROUP", c.resourceGroup},
		{"GRAFANA_NAME", c.grafanaName},
	} {
		if kv.val == "" {
			missing = append(missing, kv.key)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	return c, nil
}

// parseRoles parses a space-separated list of "principalId/principalType/role"
// entries. An empty string yields no entries (a valid no-op).
func parseRoles(raw string) ([]roleAssignment, error) {
	var out []roleAssignment
	for _, entry := range strings.Fields(raw) {
		parts := strings.Split(entry, "/")
		if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
			return nil, fmt.Errorf("malformed GRAFANA_ROLES entry %q (expected principalId/principalType/role)", entry)
		}
		if _, ok := grafanaBuiltInRoles[parts[2]]; !ok {
			return nil, fmt.Errorf("unknown Grafana role %q in entry %q", parts[2], entry)
		}
		out = append(out, roleAssignment{
			principalID:   parts[0],
			principalType: parts[1],
			roleName:      parts[2],
		})
	}
	return out, nil
}

// grafanaResourceID builds the ARM resource ID used as the assignment scope.
func grafanaResourceID(subscriptionID, resourceGroup, grafanaName string) string {
	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Dashboard/grafana/%s",
		subscriptionID, resourceGroup, grafanaName)
}

// roleDefinitionID builds the subscription-scoped role definition ID.
func roleDefinitionID(subscriptionID, roleGUID string) string {
	return fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s",
		subscriptionID, roleGUID)
}

// assignmentName derives a deterministic role assignment name so re-runs update
// the same assignment (idempotent) instead of creating duplicates. The name is a
// UUIDv5 over scope|principalId|roleDefinitionId; it does not need to match any
// other tool's naming because Azure deduplicates assignments by
// principal+role+scope regardless of name (surfaced as RoleAssignmentExists).
func assignmentName(scope, principalID, roleDefID string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(scope+"|"+principalID+"|"+roleDefID)).String()
}

// mapPrincipalType maps the config principalType string to the ARM enum.
func mapPrincipalType(s string) (armauthorization.PrincipalType, error) {
	switch strings.ToLower(s) {
	case "group":
		return armauthorization.PrincipalTypeGroup, nil
	case "serviceprincipal":
		return armauthorization.PrincipalTypeServicePrincipal, nil
	case "user":
		return armauthorization.PrincipalTypeUser, nil
	default:
		return "", fmt.Errorf("unsupported principalType %q (expected Group, ServicePrincipal, or User)", s)
	}
}

func run(ctx context.Context) error {
	cfg, err := parseEnvConfig(os.Getenv)
	if err != nil {
		return err
	}
	if len(cfg.roles) == 0 {
		slog.Info("GRAFANA_ROLES is empty; nothing to assign.")
		return nil
	}

	// DefaultAzureCredential resolves to the injected MSI in EV2 and to the
	// operator's `az login` locally; it never prompts interactively.
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return fmt.Errorf("azidentity: %w", err)
	}

	client, err := armauthorization.NewRoleAssignmentsClient(cfg.subscriptionID, cred, nil)
	if err != nil {
		return fmt.Errorf("role assignments client: %w", err)
	}

	scope := grafanaResourceID(cfg.subscriptionID, cfg.resourceGroup, cfg.grafanaName)
	slog.Info("assigning Grafana roles", "scope", scope, "count", len(cfg.roles))

	for _, r := range cfg.roles {
		if err := assignRole(ctx, client, cfg.subscriptionID, scope, r); err != nil {
			return err
		}
	}

	slog.Info("done")
	return nil
}

// assignRole idempotently creates a single role assignment. An existing
// assignment for the same principal/role/scope (created out-of-band under a
// different name) surfaces as RoleAssignmentExists and is treated as success.
func assignRole(ctx context.Context, client *armauthorization.RoleAssignmentsClient, subscriptionID, scope string, r roleAssignment) error {
	pt, err := mapPrincipalType(r.principalType)
	if err != nil {
		return err
	}
	roleDefID := roleDefinitionID(subscriptionID, grafanaBuiltInRoles[r.roleName])
	name := assignmentName(scope, r.principalID, roleDefID)

	slog.Info("assigning role",
		"role", r.roleName,
		"principalId", r.principalID,
		"principalType", r.principalType,
	)

	_, err = client.Create(ctx, scope, name, armauthorization.RoleAssignmentCreateParameters{
		Properties: &armauthorization.RoleAssignmentProperties{
			PrincipalID:      to.Ptr(r.principalID),
			PrincipalType:    to.Ptr(pt),
			RoleDefinitionID: to.Ptr(roleDefID),
		},
	}, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.ErrorCode == "RoleAssignmentExists" {
			slog.Info("role already assigned; skipping", "role", r.roleName, "principalId", r.principalID)
			return nil
		}
		return fmt.Errorf("assign role %q to %s: %w", r.roleName, r.principalID, err)
	}
	return nil
}
