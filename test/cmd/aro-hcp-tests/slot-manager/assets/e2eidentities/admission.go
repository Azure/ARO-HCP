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

package e2eidentities

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/uuid"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets/azureutil"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
	"github.com/Azure/ARO-HCP/test/util/framework"
)

type identityLeaseInventory struct {
	roleAssignments []*armauthorization.RoleAssignment
}

func admitE2EIdentityLease(ctx context.Context, request assets.LeaseRequest, cleanupWorkers int) error {
	credential, subscriptionID, err := leaseCredential(request)
	if err != nil {
		return err
	}

	msiFactory, err := armmsi.NewClientFactory(subscriptionID, credential, nil)
	if err != nil {
		return fmt.Errorf("failed creating managed identity client factory: %w", err)
	}
	roleAssignmentsClient, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, credential, nil)
	if err != nil {
		return fmt.Errorf("failed creating role assignments client: %w", err)
	}

	return admitIdentityLeaseWithClients(ctx, request, msiFactory, roleAssignmentsClient, cleanupWorkers)
}

func admitIdentityLeaseWithClients(ctx context.Context, request assets.LeaseRequest, msiFactory *armmsi.ClientFactory, roleAssignmentsClient *armauthorization.RoleAssignmentsClient, cleanupWorkers int) error {
	logger := logr.FromContextOrDiscard(ctx)
	logger.Info("Loading identity lease inventory", "slotName", request.AcquiredSlotState.Slot.ResourceName, "resourceGroupCount", len(request.AcquiredSlotState.Slot.IdentityContainerNames()))
	inventory, err := loadIdentityLeaseInventory(ctx, request, msiFactory, roleAssignmentsClient)
	if err != nil {
		return err
	}
	logger.Info(fmt.Sprintf("Loaded identity lease inventory with %d role assignments", len(inventory.roleAssignments)))

	deleteOperations := make([]func(context.Context) error, 0, len(inventory.roleAssignments))
	for _, assignment := range inventory.roleAssignments {
		deleteOperations = append(deleteOperations, func(ctx context.Context) error {
			if assignment.ID == nil {
				return errors.New("role assignment has no ID")
			}
			logger.Info("Attempting to delete role assignment", "roleAssignmentResourceID", *assignment.ID)
			_, err := roleAssignmentsClient.DeleteByID(ctx, *assignment.ID, nil)
			if azureutil.IsNotFound(err) {
				logger.Info("Role assignment not found. Nothing to delete", "roleAssignmentResourceID", *assignment.ID)
				return nil
			}
			if err != nil {
				return fmt.Errorf("failed deleting role assignment %q: %w", *assignment.ID, err)
			}
			logger.Info("Deleted role assignment",
				"roleAssignmentResourceID", *assignment.ID, "roleAssignmentPrincipalID", *assignment.Properties.PrincipalID,
				"roleAssignmentRoleDefinitionResourceID", assignment.Properties.RoleDefinitionID,
				"roleAssignmentScope", ptr.Deref(assignment.Properties.Scope, "<nil>"),
				"roleAssignmentCreatedBy", ptr.Deref(assignment.Properties.CreatedBy, "<nil>"),
				"roleAssignmentCreatedOn", ptr.Deref(assignment.Properties.CreatedOn, time.Time{}),
			)
			return nil
		})
	}

	workers := max(cleanupWorkers, 1)
	logger.Info("Deleting role assignments", "count", len(deleteOperations), "workers", workers)
	if err := azureutil.RunWithWorkers(ctx, deleteOperations, workers); err != nil {
		return fmt.Errorf("failed cleaning E2E identity lease: %w", err)
	}
	// Exclusive ownership lets us rely on successful synchronous deletes without
	// polling for absence or repeating the inventory.
	return nil
}

func leaseCredential(request assets.LeaseRequest) (azcore.TokenCredential, string, error) {
	if request.AcquiredSlotState == nil {
		return nil, "", errors.New("acquired slot state is nil")
	}
	subscriptionID := strings.TrimSpace(request.AcquiredSlotState.Slot.Subscriptions.E2E.ID)
	if subscriptionID == "" {
		return nil, "", errors.New("resolved E2E subscription ID is empty")
	}
	profileDir := strings.TrimSpace(request.SelectedClusterProfileDir)
	if profileDir == "" {
		return nil, "", errors.New("selected cluster profile dir is empty")
	}

	credential, err := slots.NewClusterProfileCredential(profileDir, nil)
	if err != nil {
		return nil, "", fmt.Errorf("failed constructing cluster profile credential: %w", err)
	}
	return credential, subscriptionID, nil
}

func loadIdentityLeaseInventory(
	ctx context.Context,
	request assets.LeaseRequest,
	msiFactory *armmsi.ClientFactory,
	roleAssignmentsClient *armauthorization.RoleAssignmentsClient,
) (*identityLeaseInventory, error) {
	if request.AcquiredSlotState == nil || len(request.AcquiredSlotState.Slot.IdentityContainerNames()) == 0 {
		return nil, errors.New("resolved E2E identity inventory is empty")
	}
	expectedIdentities := azureutil.NormalizedNameSet(framework.NewDefaultIdentities().ToSlice())
	inventory := &identityLeaseInventory{}
	principalIDs := sets.New[uuid.UUID]()
	identitiesClient := msiFactory.NewUserAssignedIdentitiesClient()

	for _, resourceGroup := range request.AcquiredSlotState.Slot.IdentityContainerNames() {
		actualIdentities := map[string]string{}
		var unexpectedIdentities []string
		pager := identitiesClient.NewListByResourceGroupPager(resourceGroup, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed listing identities in resource group %q: %w", resourceGroup, err)
			}
			for _, identity := range page.Value {
				if identity == nil || identity.Name == nil || strings.TrimSpace(*identity.Name) == "" {
					return nil, fmt.Errorf("identity list for resource group %q returned an entry without a name", resourceGroup)
				}
				normalizedName := strings.ToLower(*identity.Name)
				if !expectedIdentities.Has(normalizedName) {
					unexpectedIdentities = append(unexpectedIdentities, *identity.Name)
					continue
				}
				if identity.Properties == nil || identity.Properties.PrincipalID == nil || strings.TrimSpace(*identity.Properties.PrincipalID) == "" {
					return nil, fmt.Errorf("identity %q in resource group %q has no principal ID", *identity.Name, resourceGroup)
				}
				principalID, err := azureutil.ParsePrincipalID(*identity.Properties.PrincipalID)
				if err != nil {
					return nil, fmt.Errorf("identity %q has invalid principal ID: %w", *identity.Name, err)
				}
				if _, found := actualIdentities[normalizedName]; found {
					return nil, fmt.Errorf("identity list for resource group %q returned duplicate identity %q", resourceGroup, *identity.Name)
				}
				actualIdentities[normalizedName] = *identity.Name
				principalIDs.Insert(principalID)
			}
		}
		if len(unexpectedIdentities) > 0 {
			sort.Strings(unexpectedIdentities)
			logr.FromContextOrDiscard(ctx).Info("Unexpected identities in leased resource group; excluded from admission cleanup",
				"resourceGroup", resourceGroup,
				"identityNames", unexpectedIdentities,
			)
		}
		if err := azureutil.RequireIdentities(resourceGroup, expectedIdentities, actualIdentities); err != nil {
			return nil, err
		}
	}

	rolePager := roleAssignmentsClient.NewListForSubscriptionPager(nil)
	for rolePager.More() {
		page, err := rolePager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed listing role assignments for E2E subscription: %w", err)
		}
		for _, assignment := range page.Value {
			if assignment == nil || assignment.Properties == nil || assignment.Properties.PrincipalID == nil || strings.TrimSpace(*assignment.Properties.PrincipalID) == "" {
				return nil, errors.New("role assignment list returned an entry without a principal ID")
			}
			principalID, err := azureutil.ParsePrincipalID(*assignment.Properties.PrincipalID)
			if err != nil {
				return nil, fmt.Errorf("role assignment list returned invalid principal ID %q: %w", *assignment.Properties.PrincipalID, err)
			}
			if principalIDs.Has(principalID) {
				inventory.roleAssignments = append(inventory.roleAssignments, assignment)
			}
		}
	}
	return inventory, nil
}
