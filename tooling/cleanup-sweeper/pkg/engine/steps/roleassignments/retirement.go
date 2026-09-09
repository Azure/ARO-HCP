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

package roleassignments

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/steps/common"
)

const managedIdentityType = "Microsoft.ManagedIdentity/userAssignedIdentities"

type retiringIdentity struct {
	id, principalID, tenantID string
}

// ResourceGroupRetirement is an in-memory ownership snapshot for explicit
// topology-driven teardown. It must be captured BEFORE removing the resource
// group. It is deliberately not an orphan-discovery policy or an importable
// allowlist: callers must own the entire resource group's retirement.
type ResourceGroupRetirement struct {
	rgID        string
	assignments []roleAssignmentRecord
	api         retirementAPI
}

type retirementAPI struct {
	group       func(context.Context) (armresources.ResourceGroup, error)
	tenant      func(context.Context) (string, error)
	identities  func(context.Context) ([]retiringIdentity, error)
	assignments func(context.Context) ([]roleAssignmentRecord, error)
	assignment  func(context.Context, string) (roleAssignmentRecord, error)
	active      activePrincipalLookup
	deleted     deletedPrincipalLookup
	remove      func(context.Context, string) error
}

// CaptureResourceGroupRetirement records user-assigned identities physically
// contained in a retiring RG and their current same-subscription assignments.
// Referenced identities (including leased pools) are never captured. The Graph
// credential only needs directory reads; this path never writes to Graph.
func CaptureResourceGroupRetirement(ctx context.Context, subscriptionID, resourceGroup string, credential azcore.TokenCredential) (*ResourceGroupRetirement, error) {
	if _, err := uuid.Parse(subscriptionID); err != nil || resourceGroup == "" || strings.ContainsAny(resourceGroup, "/\\") {
		return nil, fmt.Errorf("valid subscription ID and resource group are required")
	}
	groups, err := armresources.NewResourceGroupsClient(subscriptionID, credential, nil)
	if err != nil {
		return nil, err
	}
	resources, err := armresources.NewClient(subscriptionID, credential, nil)
	if err != nil {
		return nil, err
	}
	subscriptions, err := armsubscriptions.NewClient(credential, nil)
	if err != nil {
		return nil, err
	}
	roles, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, credential, nil)
	if err != nil {
		return nil, err
	}
	graph, err := NewGraphClient(credential)
	if err != nil {
		return nil, err
	}
	rgID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionID, resourceGroup)
	api := retirementAPI{
		group: func(ctx context.Context) (armresources.ResourceGroup, error) {
			response, err := groups.Get(ctx, resourceGroup, nil)
			return response.ResourceGroup, err
		},
		tenant: func(ctx context.Context) (string, error) {
			response, err := subscriptions.Get(ctx, subscriptionID, nil)
			if err != nil {
				return "", err
			}
			if response.SubscriptionID == nil || !strings.EqualFold(*response.SubscriptionID, subscriptionID) || response.TenantID == nil {
				return "", fmt.Errorf("subscription lookup returned missing or unexpected identity")
			}
			return *response.TenantID, nil
		},
		identities: func(ctx context.Context) ([]retiringIdentity, error) {
			var identities []retiringIdentity
			pager := resources.NewListByResourceGroupPager(resourceGroup, nil)
			for pager.More() {
				page, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, resource := range page.Value {
					if resource == nil || resource.Type == nil || resource.ID == nil {
						return nil, fmt.Errorf("invalid resource listing")
					}
					if !strings.EqualFold(*resource.Type, managedIdentityType) {
						continue
					}
					response, err := resources.GetByID(ctx, *resource.ID, "2023-01-31", nil)
					if err != nil {
						return nil, err
					}
					if response.ID == nil || !strings.EqualFold(*response.ID, *resource.ID) ||
						response.Type == nil || !strings.EqualFold(*response.Type, managedIdentityType) {
						return nil, fmt.Errorf("identity lookup returned missing or unexpected ID/type")
					}
					properties, ok := response.Properties.(map[string]interface{})
					if !ok {
						return nil, fmt.Errorf("identity lookup returned invalid properties")
					}
					principalID, _ := properties["principalId"].(string)
					tenantID, _ := properties["tenantId"].(string)
					identities = append(identities, retiringIdentity{*resource.ID, principalID, tenantID})
				}
			}
			return identities, nil
		},
		assignments: func(ctx context.Context) ([]roleAssignmentRecord, error) {
			return listRoleAssignments(ctx, roles, subscriptionID, logr.FromContextOrDiscard(ctx), common.NewDiscoverySkipReporter("Capture retiring role assignments"))
		},
		assignment: func(ctx context.Context, id string) (roleAssignmentRecord, error) {
			response, err := roles.GetByID(ctx, id, nil)
			if err != nil {
				return roleAssignmentRecord{}, err
			}
			record, ok := toRoleAssignmentRecord(&response.RoleAssignment, logr.FromContextOrDiscard(ctx), common.NewDiscoverySkipReporter("Revalidate retiring role assignment"))
			if !ok {
				return roleAssignmentRecord{}, fmt.Errorf("invalid role assignment response")
			}
			return record, nil
		},
		active:  newGraphRetiringPrincipalLookup(graph),
		deleted: newGraphDeletedPrincipalLookup(graph),
		remove: func(ctx context.Context, id string) error {
			_, err := roles.DeleteByID(ctx, id, nil)
			return err
		},
	}
	if err := runGraphVisibilityPreflight(ctx, graph); err != nil {
		return nil, fmt.Errorf("%s: %w", preflightFailureMessage, err)
	}
	return captureResourceGroupRetirement(ctx, rgID, api)
}

func captureResourceGroupRetirement(ctx context.Context, rgID string, api retirementAPI) (*ResourceGroupRetirement, error) {
	group, err := api.group(ctx)
	if err != nil {
		return nil, fmt.Errorf("capture retiring resource group: %w", err)
	}
	if group.ID == nil || !strings.EqualFold(*group.ID, rgID) || group.ManagedBy != nil && *group.ManagedBy != "" {
		return nil, fmt.Errorf("refusing missing, unexpected or managed resource group")
	}
	for key, value := range group.Tags {
		if strings.EqualFold(key, "persist") && (value == nil || !strings.EqualFold(*value, "false")) {
			return nil, fmt.Errorf("refusing persistent resource group")
		}
	}
	parsed, err := azcorearm.ParseResourceID(rgID)
	if err != nil || strings.HasPrefix(strings.ToLower(parsed.ResourceGroupName), "aro-hcp-msi-container-") ||
		strings.HasSuffix(strings.ToLower(parsed.ResourceGroupName), "-shared-resources") {
		return nil, fmt.Errorf("refusing shared identity resource group")
	}
	tenant, err := api.tenant(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(tenant); err != nil {
		return nil, fmt.Errorf("subscription tenant ID is invalid")
	}
	identities, err := api.identities(ctx)
	if err != nil {
		return nil, err
	}
	principals := map[string]bool{}
	for _, identity := range identities {
		id, err := azcorearm.ParseResourceID(identity.id)
		if err != nil || !strings.EqualFold(id.ResourceType.String(), managedIdentityType) ||
			!strings.EqualFold(id.Parent.String(), rgID) || !strings.EqualFold(identity.tenantID, tenant) {
			return nil, fmt.Errorf("identity outside retiring resource group or tenant")
		}
		if _, err := uuid.Parse(identity.principalID); err != nil {
			return nil, fmt.Errorf("invalid identity principal ID")
		}
		// Positive resolution before retirement binds the Graph directory to
		// the ARM identity. A wrong-tenant credential must not prove absence.
		active, err := api.active(ctx, identity.principalID)
		if err != nil || !active {
			return nil, fmt.Errorf("cannot establish active ownership of identity %q: active=%t: %v", identity.id, active, err)
		}
		principals[normalizeID(identity.principalID)] = true
	}
	assignments, err := api.assignments(ctx)
	if err != nil {
		return nil, err
	}
	result := &ResourceGroupRetirement{rgID: rgID, api: api}
	for _, assignment := range assignments {
		if !principals[normalizeID(assignment.PrincipalID)] {
			continue
		}
		id, err := azcorearm.ParseResourceID(assignment.ID)
		if err != nil || !strings.EqualFold(id.SubscriptionID, parsed.SubscriptionID) ||
			!strings.EqualFold(id.ResourceType.String(), ResourceType) {
			return nil, fmt.Errorf("assignment outside retirement subscription or invalid resource type")
		}
		result.assignments = append(result.assignments, assignment)
	}
	return result, nil
}

// Cleanup reclaims the captured assignments only after the owning resource
// group is gone. Soft-deleted principals are allowed only on this explicit
// retirement path; active/restored principals and uncertain responses fail
// closed. Each DELETE is preceded by fresh ARM and Graph reads.
func (r *ResourceGroupRetirement) Cleanup(ctx context.Context) error {
	for _, captured := range r.assignments {
		if _, err := r.api.group(ctx); err == nil {
			return fmt.Errorf("retiring resource group %q still exists", r.rgID)
		} else if !armNotFound(err) {
			return fmt.Errorf("confirm retiring resource group %q is deleted: %w", r.rgID, err)
		}
		current, err := r.api.assignment(ctx, captured.ID)
		if armNotFound(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !strings.EqualFold(current.ID, captured.ID) || !strings.EqualFold(current.PrincipalID, captured.PrincipalID) {
			return fmt.Errorf("captured role assignment changed")
		}
		active, err := r.api.active(ctx, captured.PrincipalID)
		if err != nil {
			return fmt.Errorf("revalidate active principal %q: %w", captured.PrincipalID, err)
		}
		if active {
			return fmt.Errorf("%w: principal %q is still active", runner.ErrTargetRetained, captured.PrincipalID)
		}
		if _, err := r.api.deleted(ctx, captured.PrincipalID); err != nil {
			return err
		}
		active, err = r.api.active(ctx, captured.PrincipalID)
		if err != nil {
			return fmt.Errorf("revalidate restored principal %q: %w", captured.PrincipalID, err)
		}
		if active {
			return fmt.Errorf("retaining principal %q restored during retirement", captured.PrincipalID)
		}
		if err := r.api.remove(ctx, captured.ID); err != nil && !armNotFound(err) {
			return err
		}
	}
	return nil
}

func armNotFound(err error) bool {
	var responseErr *azcore.ResponseError
	return errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusNotFound
}

func newGraphRetiringPrincipalLookup(graph *msgraphsdk.GraphServiceClient) activePrincipalLookup {
	return func(ctx context.Context, id string) (bool, error) {
		object, err := graph.ServicePrincipals().ByServicePrincipalId(id).Get(ctx, nil)
		if isGraphNotFoundError(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if object == nil || object.GetId() == nil || !strings.EqualFold(*object.GetId(), id) {
			return false, fmt.Errorf("active service principal lookup returned missing or unexpected ID")
		}
		return true, nil
	}
}
