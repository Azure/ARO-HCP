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

package secrets

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/util/sets"
)

// grantConsent reproduces az ad app permission admin-consent: application
// permissions become appRoleAssignments, delegated permissions become a
// tenant-wide oauth2PermissionGrant. Existing grants are left alone, and a
// delegated grant only gains the missing scopes.
func (e *Ensurer) grantConsent(ctx context.Context, logger logr.Logger, application *Application, dryRun bool) error {
	if len(application.RequiredResourceAccess) == 0 {
		return nil
	}

	principal, err := e.graph.FindServicePrincipal(ctx, application.AppID)
	if err != nil {
		return fmt.Errorf("service principal of application %s: %w", application.AppID, err)
	}
	assignments, err := e.graph.ListAppRoleAssignments(ctx, principal.ID)
	if err != nil {
		return fmt.Errorf("list app role assignments: %w", err)
	}
	assigned := sets.New(assignments...)

	for _, resource := range application.RequiredResourceAccess {
		resourcePrincipal, err := e.graph.FindServicePrincipal(ctx, resource.ResourceAppID)
		if err != nil {
			return fmt.Errorf("service principal of resource %s: %w", resource.ResourceAppID, err)
		}

		scopes := sets.New[string]()
		for _, access := range resource.Access {
			switch access.Type {
			case ResourceAccessRole:
				assignment := AppRoleAssignment{ResourceID: resourcePrincipal.ID, AppRoleID: access.ID}
				err := e.grantRole(ctx, logger, principal.ID, resource.ResourceAppID, assignment, assigned, dryRun)
				if err != nil {
					return err
				}
			case ResourceAccessScope:
				value, ok := resourcePrincipal.ScopeValues[access.ID]
				if !ok {
					return fmt.Errorf("delegated permission %s is not defined by resource %s", access.ID, resource.ResourceAppID)
				}
				scopes.Insert(value)
			default:
				return fmt.Errorf("unsupported resource access type %q for %s on %s", access.Type, access.ID, resource.ResourceAppID)
			}
		}

		err = e.grantScopes(ctx, logger, principal.ID, resourcePrincipal.ID, resource.ResourceAppID, scopes, dryRun)
		if err != nil {
			return err
		}
	}
	return nil
}

func (e *Ensurer) grantRole(ctx context.Context, logger logr.Logger, principalID, resourceAppID string, assignment AppRoleAssignment, assigned sets.Set[AppRoleAssignment], dryRun bool) error {
	switch {
	case assigned.Has(assignment):
		return nil
	case dryRun:
		logger.Info("would grant application permission", "resource", resourceAppID, "appRoleId", assignment.AppRoleID)
		return nil
	}

	logger.Info("granting application permission", "resource", resourceAppID, "appRoleId", assignment.AppRoleID)
	err := e.graph.CreateAppRoleAssignment(ctx, principalID, assignment.ResourceID, assignment.AppRoleID)
	if err != nil {
		return fmt.Errorf("assign app role %s on %s: %w", assignment.AppRoleID, resourceAppID, err)
	}
	return nil
}

func (e *Ensurer) grantScopes(ctx context.Context, logger logr.Logger, clientID, resourceID, resourceAppID string, scopes sets.Set[string], dryRun bool) error {
	if scopes.Len() == 0 {
		return nil
	}

	grant, err := e.graph.FindPermissionGrant(ctx, clientID, resourceID)
	if err != nil {
		return fmt.Errorf("find permission grant on %s: %w", resourceAppID, err)
	}

	granted := sets.New[string]()
	if grant != nil {
		granted.Insert(strings.Fields(grant.Scope)...)
	}
	if granted.IsSuperset(scopes) {
		return nil
	}

	merged := strings.Join(sets.List(granted.Union(scopes)), " ")
	if dryRun {
		logger.Info("would grant delegated permissions", "resource", resourceAppID, "scope", merged)
		return nil
	}

	logger.Info("granting delegated permissions", "resource", resourceAppID, "scope", merged)
	switch grant {
	case nil:
		err = e.graph.CreatePermissionGrant(ctx, clientID, resourceID, merged)
	default:
		err = e.graph.UpdatePermissionGrant(ctx, grant.ID, merged)
	}
	if err != nil {
		return fmt.Errorf("grant delegated permissions on %s: %w", resourceAppID, err)
	}
	return nil
}
