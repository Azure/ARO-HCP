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
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
)

type config struct {
	enabled                                bool
	subscriptionID, principalID, clusterID string
	roleDefinitionID, assignmentID         string
}

func parseConfig(env func(string) string) (config, error) {
	c := config{
		subscriptionID:   env("SUBSCRIPTION_ID"),
		principalID:      env("PRINCIPAL_ID"),
		clusterID:        env("CLUSTER_ID"),
		roleDefinitionID: env("ROLE_DEFINITION_ID"),
		assignmentID:     env("ROLE_ASSIGNMENT_ID"),
	}
	switch env("NODE_MITIGATION_ENABLED") {
	case "true":
		c.enabled = true
	case "false":
	default:
		return c, fmt.Errorf("NODE_MITIGATION_ENABLED must be true or false")
	}
	for name, value := range map[string]string{"SUBSCRIPTION_ID": c.subscriptionID, "PRINCIPAL_ID": c.principalID} {
		if _, err := uuid.Parse(value); err != nil {
			return c, fmt.Errorf("invalid %s: %w", name, err)
		}
	}
	cluster, err := azcorearm.ParseResourceID(c.clusterID)
	if err != nil || !strings.EqualFold(cluster.SubscriptionID, c.subscriptionID) ||
		!strings.EqualFold(cluster.ResourceType.String(), "Microsoft.ContainerService/managedClusters") || cluster.ResourceGroupName == "" {
		return c, fmt.Errorf("CLUSTER_ID must identify an AKS cluster in SUBSCRIPTION_ID")
	}
	role, err := azcorearm.ParseResourceID(c.roleDefinitionID)
	if err != nil || !strings.EqualFold(c.roleDefinitionID, fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s", c.subscriptionID, role.Name)) {
		return c, fmt.Errorf("ROLE_DEFINITION_ID must identify a subscription-scoped role in SUBSCRIPTION_ID")
	}
	if _, err := uuid.Parse(role.Name); err != nil {
		return c, fmt.Errorf("invalid role definition name: %w", err)
	}
	if !assignmentAtScope(c.assignmentID, c.clusterID) {
		return c, fmt.Errorf("ROLE_ASSIGNMENT_ID must be scoped to CLUSTER_ID")
	}
	return c, nil
}

func assignmentAtScope(id, scope string) bool {
	assignment, err := azcorearm.ParseResourceID(id)
	if err != nil {
		return false
	}
	_, err = uuid.Parse(assignment.Name)
	return err == nil && strings.EqualFold(id, scope+"/providers/Microsoft.Authorization/roleAssignments/"+assignment.Name)
}

type roleAssignments interface {
	GetByID(context.Context, string, *armauthorization.RoleAssignmentsClientGetByIDOptions) (armauthorization.RoleAssignmentsClientGetByIDResponse, error)
	DeleteByID(context.Context, string, *armauthorization.RoleAssignmentsClientDeleteByIDOptions) (armauthorization.RoleAssignmentsClientDeleteByIDResponse, error)
}

func notFound(err error) bool {
	var response *azcore.ResponseError
	return errors.As(err, &response) && response.StatusCode == http.StatusNotFound
}

func matches(value *string, expected string) bool {
	return value != nil && strings.EqualFold(*value, expected)
}

func revoke(ctx context.Context, c config, client roleAssignments) error {
	assignment, err := client.GetByID(ctx, c.assignmentID, nil)
	if notFound(err) {
		slog.Info("machine-deletion assignment is absent", "assignmentId", c.assignmentID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("read machine-deletion assignment: %w", err)
	}
	p := assignment.Properties
	if !matches(assignment.ID, c.assignmentID) || p == nil ||
		!matches(p.Scope, c.clusterID) ||
		!matches(p.PrincipalID, c.principalID) ||
		!matches(p.RoleDefinitionID, c.roleDefinitionID) {
		return fmt.Errorf("machine-deletion assignment does not match the expected resource, scope, principal and role")
	}
	if _, err := client.DeleteByID(ctx, c.assignmentID, nil); err != nil && !notFound(err) {
		return fmt.Errorf("revoke machine-deletion assignment: %w", err)
	}
	if _, err := client.GetByID(ctx, c.assignmentID, nil); !notFound(err) {
		if err != nil {
			return fmt.Errorf("verify machine-deletion revocation: %w", err)
		}
		return fmt.Errorf("machine-deletion assignment still exists after deletion")
	}
	slog.Info("machine-deletion assignment revoked", "assignmentId", c.assignmentID)
	return nil
}

func listAssignments(ctx context.Context, c config, client *armauthorization.RoleAssignmentsClient) ([]armauthorization.RoleAssignment, error) {
	filter := "atScope()"
	pager := client.NewListForScopePager(c.clusterID, &armauthorization.RoleAssignmentsClientListForScopeOptions{Filter: &filter})
	var assignments []armauthorization.RoleAssignment
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list machine-deletion assignments: %w", err)
		}
		for _, assignment := range page.Value {
			if assignment == nil || assignment.Properties == nil ||
				assignment.Properties.Scope == nil || assignment.Properties.RoleDefinitionID == nil ||
				*assignment.Properties.Scope == "" || *assignment.Properties.RoleDefinitionID == "" {
				return nil, fmt.Errorf("listed assignment has incomplete scope or role metadata")
			}
			p := assignment.Properties
			if !matches(p.Scope, c.clusterID) || !matches(p.RoleDefinitionID, c.roleDefinitionID) {
				continue
			}
			if assignment.ID == nil || !assignmentAtScope(*assignment.ID, c.clusterID) || p.PrincipalID == nil {
				return nil, fmt.Errorf("listed machine-deletion assignment has invalid resource or principal metadata")
			}
			if _, err := uuid.Parse(*p.PrincipalID); err != nil {
				return nil, fmt.Errorf("invalid listed principal: %w", err)
			}
			if matches(assignment.ID, c.assignmentID) && !matches(p.PrincipalID, c.principalID) {
				return nil, fmt.Errorf("current assignment has an unexpected principal")
			}
			assignments = append(assignments, *assignment)
		}
	}
	return assignments, nil
}

func reconcile(ctx context.Context, c config, client *armauthorization.RoleAssignmentsClient) error {
	assignments, err := listAssignments(ctx, c, client)
	if err != nil {
		return err
	}
	currentFound := false
	for _, assignment := range assignments {
		currentFound = currentFound || matches(assignment.ID, c.assignmentID)
	}
	if c.enabled && !currentFound {
		return fmt.Errorf("enabled mitigation is missing its current machine-deletion assignment")
	}
	for _, assignment := range assignments {
		if c.enabled && matches(assignment.ID, c.assignmentID) {
			continue
		}
		target := c
		target.assignmentID = *assignment.ID
		target.principalID = *assignment.Properties.PrincipalID
		if err := revoke(ctx, target, client); err != nil {
			return err
		}
	}
	remaining, err := listAssignments(ctx, c, client)
	if err != nil {
		return err
	}
	if c.enabled {
		if len(remaining) != 1 || !matches(remaining[0].ID, c.assignmentID) {
			return fmt.Errorf("enabled mitigation must retain only its current machine-deletion assignment")
		}
	} else if len(remaining) != 0 {
		return fmt.Errorf("disabled mitigation still has machine-deletion assignments")
	}
	slog.Info("machine-deletion assignments reconciled", "clusterId", c.clusterID, "enabled", c.enabled, "remaining", len(remaining))
	return nil
}

func run(ctx context.Context) error {
	c, err := parseConfig(os.Getenv)
	if err != nil {
		return err
	}
	credential, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{RequireAzureTokenCredentials: true})
	if err != nil {
		return fmt.Errorf("create cleanup credential: %w", err)
	}
	client, err := armauthorization.NewRoleAssignmentsClient(c.subscriptionID, credential, nil)
	if err != nil {
		return fmt.Errorf("create role assignments client: %w", err)
	}
	return reconcile(ctx, c, client)
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)).With("component", "mgmt-agent-permissions"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := run(ctx); err != nil {
		slog.Error("permission cleanup failed", "error", err)
		os.Exit(1)
	}
}
