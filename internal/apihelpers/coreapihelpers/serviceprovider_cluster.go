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

package coreapihelpers

import (
	"encoding/json"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks/v2"

	"github.com/Azure/ARO-HCP/internal/api/azureresourcesapi"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/azureresourcesapihelpers"
)

// IsValidHostedClusterControlPlaneSize reports whether s names a known tier.
func IsValidHostedClusterControlPlaneSize(s string) bool {
	switch coreapi.HostedClusterControlPlaneSize(s) {
	case coreapi.HostedClusterControlPlaneSizeSmall,
		coreapi.HostedClusterControlPlaneSizeMedium,
		coreapi.HostedClusterControlPlaneSizeLarge,
		coreapi.HostedClusterControlPlaneSizeXlarge,
		coreapi.HostedClusterControlPlaneSizeXXlarge:
		return true
	}
	return false
}

// RoleAssignmentResourceType is the ARM resource type stamped on every role
// assignment template resource the mock FPA stack manages. The builder that
// publishes the template and RenderedMockFPARoleAssignments that reads it back
// share this constant so the two sides cannot drift. ARM resource types are
// compared case-insensitively.
const RoleAssignmentResourceType = "Microsoft.Authorization/roleAssignments"

// mockFPARoleAssignmentTemplate is the minimal shape of the ARM template published on
// the mock FPA DeploymentStackDesire.Spec. The published template is the ledger of
// which role assignments the mock FPA stack manages, so it is read back to recover
// each assignment's principal, role definition, and reserved name.
type mockFPARoleAssignmentTemplate struct {
	Resources []mockFPARoleAssignmentTemplateResource `json:"resources"`
}

type mockFPARoleAssignmentTemplateResource struct {
	Type       string `json:"type"`
	Name       string `json:"name"`
	Properties struct {
		PrincipalID      string `json:"principalId"`
		RoleDefinitionID string `json:"roleDefinitionId"`
	} `json:"properties"`
}

// MockFPARoleAssignmentsInSpecTemplate returns the mock FPA role assignments currently
// declared in desire's Spec template, mapped from their RoleAssignmentKey to the
// reserved role assignment name (the last segment of the role assignment resource ID).
//
// The published Spec template is the sole durable ledger of which
// (principalID, roleDefinition) assignments the mock FPA stack manages and under what
// name: the name is baked into the template before Azure ever applies it, so this read
// recovers everything a reader needs without consulting any intermediate state. Returns
// an empty map when desire is nil or declares no resources.
//
// The template is stored as untyped JSON (a map[string]any in memory, round-tripped
// through Cosmos), so it is marshaled and unmarshaled into a typed shape to parse both
// forms identically.
// desire may be nil: the caller passes nil when the mock FPA DeploymentStackDesire has
// not been published yet, which is reported as not configured.
func MockFPARoleAssignmentsInSpecTemplate(desire *azureresourcesapi.DeploymentStackDesire) (map[coreapi.RoleAssignmentKey]string, error) {
	result := map[coreapi.RoleAssignmentKey]string{}

	if desire == nil || desire.Spec.Stack == nil || desire.Spec.Stack.Properties == nil || desire.Spec.Stack.Properties.Template == nil {
		return result, nil
	}

	raw, err := json.Marshal(desire.Spec.Stack.Properties.Template)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal mock FPA role assignments template: %w", err)
	}
	var tmpl mockFPARoleAssignmentTemplate
	if err := json.Unmarshal(raw, &tmpl); err != nil {
		return nil, fmt.Errorf("failed to unmarshal mock FPA role assignments template: %w", err)
	}

	for _, resource := range tmpl.Resources {
		if !strings.EqualFold(resource.Type, RoleAssignmentResourceType) {
			// This stack is published exclusively with role assignment resources, so
			// any other resource type is an invariant violation, not something to
			// silently ignore: surface it rather than misrepresent what the ledger
			// manages.
			return nil, fmt.Errorf("unexpected resource type %q in mock FPA role assignments template: only %q is supported", resource.Type, RoleAssignmentResourceType)
		}
		if resource.Name == "" || resource.Properties.PrincipalID == "" || resource.Properties.RoleDefinitionID == "" {
			return nil, fmt.Errorf("malformed mock FPA role assignment template resource: name=%q principalId=%q roleDefinitionId=%q", resource.Name, resource.Properties.PrincipalID, resource.Properties.RoleDefinitionID)
		}
		key := coreapi.RoleAssignmentKey{
			PrincipalID:              resource.Properties.PrincipalID,
			RoleDefinitionResourceID: resource.Properties.RoleDefinitionID,
		}
		// Two resources sharing a key but naming different Azure assignments means the
		// ledger is ambiguous, so surface it as an error.
		if existingName, dup := result[key]; dup && existingName != resource.Name {
			return nil, fmt.Errorf("duplicate mock FPA role assignment in template spec for principalId=%q roleDefinitionId=%q: names %q and %q", resource.Properties.PrincipalID, resource.Properties.RoleDefinitionID, existingName, resource.Name)
		}
		result[key] = resource.Name
	}
	return result, nil
}

// mockFPARoleAssignmentManagedByName reports whether the role assignment with the given
// reserved name is present and "managed" per desire's last-observed Azure state. The
// stack's observed resources carry full resource IDs, so the name is matched against the
// last segment of each. A nil desire or a never-observed stack is not managed.
//
// This is a low-level "last-observed managed" primitive. It deliberately does NOT
// consult desire.Spec.Action or the stack's success condition:
//   - The mock FPA stack is always an Apply (this controller never publishes a Delete -
//     the "no keys" case publishes an empty-resources Apply that unmanages via
//     ActionOnUnmanage), so there is no Delete action to distinguish here.
//   - "Managed per the current, successful reconcile" (as opposed to a stale or in-flight
//     observation) is a caller concern: callers that need it gate on ObservedSpecHash and
//     the SuccessfullyApplied condition before/around this check (see
//     MockFPARoleAssignmentsConfigured and MockFPARoleAssignmentConfigured). The
//     SuccessfullyApplied condition is itself Apply-specific, so it also excludes any
//     Delete desire.
func mockFPARoleAssignmentManagedByName(desire *azureresourcesapi.DeploymentStackDesire, name string) bool {
	if name == "" {
		return false
	}
	// If the deployment stack desire has not been observed yet it is not managed.
	if desire == nil || desire.Status.Stack == nil || desire.Status.Stack.Properties == nil {
		return false
	}

	for _, resource := range desire.Status.Stack.Properties.Resources {
		if resource == nil || resource.ID == nil || resource.Status == nil {
			continue
		}
		if strings.EqualFold(roleAssignmentNameFromResourceIDStr(*resource.ID), name) {
			return *resource.Status == armdeploymentstacks.ResourceStatusModeManaged
		}
	}

	return false
}

// roleAssignmentNameFromResourceIDStr returns the last path segment of a role assignment
// resource ID, which is the role assignment name.
func roleAssignmentNameFromResourceIDStr(resourceID string) string {
	idx := strings.LastIndex(resourceID, "/")
	if idx < 0 {
		return resourceID
	}
	return resourceID[idx+1:]
}

// mockFPARoleAssignmentDesireReconciledSuccessfully reports whether desire's observed
// outcome corresponds to its currently published spec (ObservedSpecHash equals the hash
// of desire.Spec) and is a successful Apply. It returns (false, nil) for a nil desire
// (never published) and an error only for an internal hashing failure.
func mockFPARoleAssignmentDesireReconciledSuccessfully(desire *azureresourcesapi.DeploymentStackDesire) (bool, error) {
	if desire == nil {
		return false, nil
	}
	hash, err := azureresourcesapihelpers.DeploymentStackDesireSpecHash(desire.Spec)
	if err != nil {
		return false, fmt.Errorf("failed to hash mock FPA role assignments DeploymentStackDesire spec: %w", err)
	}
	// The observed spec belongs to an older spec than the one currently published, so it
	// says nothing about whether the current desired set has reconciled.
	if desire.Status.ObservedSpecHash != hash {
		return false, nil
	}
	return meta.IsStatusConditionTrue(desire.Status.Conditions, azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied), nil
}

// MockFPARoleAssignmentConfigured reports whether the single role assignment with the
// given reserved name is live in Azure per a current, successful reconciliation of
// desire. It gates dropping a superseded assignment: the replacement is only removed
// once the desired assignment is confirmed live. Returns (false, nil) for an empty name
// (never published) or a nil/stale/unsuccessful desire.
func MockFPARoleAssignmentConfigured(desire *azureresourcesapi.DeploymentStackDesire, name string) (bool, error) {
	if name == "" {
		return false, nil
	}
	reconciled, err := mockFPARoleAssignmentDesireReconciledSuccessfully(desire)
	if err != nil {
		return false, err
	}
	if !reconciled {
		return false, nil
	}
	return mockFPARoleAssignmentManagedByName(desire, name), nil
}

// MockFPARoleAssignmentsConfigured reports whether every mock FPA role assignment over
// the managed resource group is live in Azure per a current, successful reconciliation
// of the mock FPA StackDesire. It gates the FPA-authenticated operator role assignments
// stack, which can only create assignments over the managed resource group once the mock
// FPA principal holds Owner there.
//
// It returns true only when all of the following hold, which is what distinguishes "the
// stack reconciled the current desired set" from "the desire is not published yet" or
// "the observation is stale":
//   - the published template declares at least one role assignment (the intent has been
//     recorded), and
//   - the StackDesire's observed outcome corresponds to its currently published spec
//     (ObservedSpecHash equals the hash of desire.Spec) and is a successful Apply, so
//     "managed" cannot reflect a stale reconcile or a spec not yet reconciled, and
//   - every rendered assignment is managed.
//
// desire may be nil: the caller passes nil when the mock FPA DeploymentStackDesire has
// not been published yet, which is reported as not configured.
//
// It returns an error only for an internal failure (hashing the spec or parsing the
// template), which the caller should surface rather than treat as "not configured".
func MockFPARoleAssignmentsConfigured(desire *azureresourcesapi.DeploymentStackDesire) (bool, error) {
	rendered, err := MockFPARoleAssignmentsInSpecTemplate(desire)
	if err != nil {
		return false, err
	}
	if len(rendered) == 0 {
		return false, nil
	}

	reconciled, err := mockFPARoleAssignmentDesireReconciledSuccessfully(desire)
	if err != nil {
		return false, err
	}
	if !reconciled {
		return false, nil
	}

	for _, name := range rendered {
		if !mockFPARoleAssignmentManagedByName(desire, name) {
			return false, nil
		}
	}
	return true, nil
}
