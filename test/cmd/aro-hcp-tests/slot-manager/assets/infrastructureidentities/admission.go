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

package infrastructureidentities

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
)

type admitClients struct {
	identities *armmsi.UserAssignedIdentitiesClient
	fics       *armmsi.FederatedIdentityCredentialsClient
	roles      *armauthorization.RoleAssignmentsClient
}

type federatedCredentialRef struct {
	resourceGroup string
	identityName  string
	name          string
}

type inspectedIdentity struct {
	name        string
	principalID uuid.UUID
}

type identityLeaseInventory struct {
	federatedCredentials []federatedCredentialRef
	roleAssignments      []*armauthorization.RoleAssignment
}

func azureAdmitClients(subscriptionID string, credential azcore.TokenCredential) (*admitClients, error) {
	msiFactory, err := armmsi.NewClientFactory(subscriptionID, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("failed creating managed identity client factory: %w", err)
	}
	roles, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("failed creating role assignments client: %w", err)
	}
	return &admitClients{
		identities: msiFactory.NewUserAssignedIdentitiesClient(),
		fics:       msiFactory.NewFederatedIdentityCredentialsClient(),
		roles:      roles,
	}, nil
}

func leaseCredential(request assets.LeaseRequest) (azcore.TokenCredential, string, error) {
	if request.AcquiredSlotState == nil {
		return nil, "", errors.New("acquired slot state is nil")
	}
	subscriptionID := strings.TrimSpace(request.AcquiredSlotState.Slot.Subscriptions.Infrastructure.ID)
	if subscriptionID == "" {
		return nil, "", errors.New("resolved infrastructure subscription ID is empty")
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

func leasedBundleName(request assets.LeaseRequest) (string, error) {
	if request.AcquiredSlotState == nil {
		return "", errors.New("resolved infrastructure identity inventory is empty")
	}
	asset := request.AcquiredSlotState.Slot.Assets.InfrastructureIdentities
	if asset == nil || len(asset.ResourceGroups) != 1 {
		return "", errors.New("resolved infrastructure identity inventory is empty")
	}
	return asset.ResourceGroups[0], nil
}

func admitIdentityLeaseWithClients(ctx context.Context, request assets.LeaseRequest, topology Topology, clients *admitClients, cleanupWorkers int) error {
	logger := logr.FromContextOrDiscard(ctx)
	leaseName, err := leasedBundleName(request)
	if err != nil {
		return err
	}
	logger.Info("Loading infrastructure identity lease inventory",
		"slotName", request.AcquiredSlotState.Slot.ResourceName,
		"bundle", leaseName,
	)
	inventory, err := loadIdentityLeaseInventory(ctx, topology, leaseName, clients)
	if err != nil {
		return err
	}
	logger.Info(fmt.Sprintf("Loaded infrastructure identity lease inventory with %d federated credentials and %d role assignments",
		len(inventory.federatedCredentials), len(inventory.roleAssignments)))
	return cleanupLeaseInventory(ctx, inventory, clients, cleanupWorkers)
}

func loadIdentityLeaseInventory(ctx context.Context, topology Topology, leaseName string, clients *admitClients) (*identityLeaseInventory, error) {
	groups, err := topology.AzureResourceGroups(leaseName)
	if err != nil {
		return nil, err
	}
	inventory := &identityLeaseInventory{}
	principals := sets.New[uuid.UUID]()
	for _, group := range groups {
		identities, err := inspectResourceGroup(ctx, clients, group)
		if err != nil {
			return nil, err
		}
		if err := collectFederatedCredentials(ctx, clients, group, identities, inventory); err != nil {
			return nil, err
		}
		for _, identity := range identities {
			principals.Insert(identity.principalID)
		}
	}
	if err := collectMatchingRoleAssignments(ctx, clients, principals, inventory); err != nil {
		return nil, err
	}
	return inventory, nil
}

func inspectResourceGroup(ctx context.Context, clients *admitClients, group azureResourceGroup) (map[string]inspectedIdentity, error) {
	expected := azureutil.NormalizedNameSet(group.IdentityNames)
	reported := map[string]string{}
	identities := map[string]inspectedIdentity{}
	var unexpected []string
	pager := clients.identities.NewListByResourceGroupPager(group.Name, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed listing identities in resource group %q: %w", group.Name, err)
		}
		for _, identity := range page.Value {
			name, err := listedIdentityName(identity, group.Name)
			if err != nil {
				return nil, err
			}
			normalized := strings.ToLower(name)
			if !expected.Has(normalized) {
				unexpected = append(unexpected, name)
				continue
			}
			principalID, err := listedIdentityPrincipal(identity, name, group.Name)
			if err != nil {
				return nil, err
			}
			if _, found := identities[normalized]; found {
				return nil, fmt.Errorf("identity list for resource group %q returned duplicate identity %q", group.Name, name)
			}
			reported[normalized] = name
			identities[normalized] = inspectedIdentity{name: name, principalID: principalID}
		}
	}
	if len(unexpected) > 0 {
		sort.Strings(unexpected)
		logr.FromContextOrDiscard(ctx).Info("Unexpected identities in leased resource group; excluded from admission cleanup",
			"resourceGroup", group.Name,
			"identityNames", unexpected,
		)
	}
	if err := azureutil.RequireIdentities(group.Name, expected, reported); err != nil {
		return nil, err
	}
	return identities, nil
}

func listedIdentityName(identity *armmsi.Identity, resourceGroup string) (string, error) {
	if identity == nil || identity.Name == nil || strings.TrimSpace(*identity.Name) == "" {
		return "", fmt.Errorf("identity list for resource group %q returned an entry without a name", resourceGroup)
	}
	return *identity.Name, nil
}

func listedIdentityPrincipal(identity *armmsi.Identity, name, resourceGroup string) (uuid.UUID, error) {
	if identity.Properties == nil || identity.Properties.PrincipalID == nil || strings.TrimSpace(*identity.Properties.PrincipalID) == "" {
		return uuid.Nil, fmt.Errorf("identity %q in resource group %q has no principal ID", name, resourceGroup)
	}
	principalID, err := azureutil.ParsePrincipalID(*identity.Properties.PrincipalID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("identity %q has invalid principal ID: %w", name, err)
	}
	return principalID, nil
}

func collectFederatedCredentials(ctx context.Context, clients *admitClients, group azureResourceGroup, identities map[string]inspectedIdentity, inventory *identityLeaseInventory) error {
	for _, identityName := range group.IdentityNames {
		actualName := identities[strings.ToLower(identityName)].name
		pager := clients.fics.NewListPager(group.Name, actualName, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return fmt.Errorf("failed listing federated identity credentials for identity %q in resource group %q: %w", actualName, group.Name, err)
			}
			for _, credential := range page.Value {
				name, err := listedFederatedCredentialName(credential, actualName, group.Name)
				if err != nil {
					return err
				}
				inventory.federatedCredentials = append(inventory.federatedCredentials, federatedCredentialRef{
					resourceGroup: group.Name,
					identityName:  actualName,
					name:          name,
				})
			}
		}
	}
	return nil
}

func listedFederatedCredentialName(credential *armmsi.FederatedIdentityCredential, identityName, resourceGroup string) (string, error) {
	if credential == nil || credential.Name == nil || strings.TrimSpace(*credential.Name) == "" {
		return "", fmt.Errorf("federated identity credential list for identity %q in resource group %q returned an entry without a name", identityName, resourceGroup)
	}
	return *credential.Name, nil
}

func collectMatchingRoleAssignments(ctx context.Context, clients *admitClients, principals sets.Set[uuid.UUID], inventory *identityLeaseInventory) error {
	pager := clients.roles.NewListForSubscriptionPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed listing role assignments for infrastructure subscription: %w", err)
		}
		for _, assignment := range page.Value {
			principalID, err := roleAssignmentPrincipal(assignment)
			if err != nil {
				return err
			}
			if !principals.Has(principalID) {
				continue
			}
			inventory.roleAssignments = append(inventory.roleAssignments, assignment)
		}
	}
	return nil
}

func roleAssignmentPrincipal(assignment *armauthorization.RoleAssignment) (uuid.UUID, error) {
	if assignment == nil || assignment.Properties == nil || assignment.Properties.PrincipalID == nil || strings.TrimSpace(*assignment.Properties.PrincipalID) == "" {
		return uuid.Nil, errors.New("role assignment list returned an entry without a principal ID")
	}
	principalID, err := azureutil.ParsePrincipalID(*assignment.Properties.PrincipalID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("role assignment list returned invalid principal ID %q: %w", *assignment.Properties.PrincipalID, err)
	}
	return principalID, nil
}

func cleanupLeaseInventory(ctx context.Context, inventory *identityLeaseInventory, clients *admitClients, cleanupWorkers int) error {
	logger := logr.FromContextOrDiscard(ctx)
	operations := make([]func(context.Context) error, 0, len(inventory.federatedCredentials)+len(inventory.roleAssignments))
	operations = append(operations, deleteFederatedCredentials(logger, clients, inventory.federatedCredentials)...)
	operations = append(operations, deleteRoleAssignments(logger, clients, inventory.roleAssignments)...)
	workers := max(cleanupWorkers, 1)
	logger.Info("Deleting federated credentials and role assignments", "count", len(operations), "workers", workers)
	if err := azureutil.RunWithWorkers(ctx, operations, workers); err != nil {
		return fmt.Errorf("failed cleaning infrastructure identity lease: %w", err)
	}
	return nil
}

func deleteFederatedCredentials(logger logr.Logger, clients *admitClients, credentials []federatedCredentialRef) []func(context.Context) error {
	operations := make([]func(context.Context) error, 0, len(credentials))
	for _, credential := range credentials {
		operations = append(operations, func(ctx context.Context) error {
			logger.Info("Attempting to delete federated identity credential",
				"resourceGroup", credential.resourceGroup,
				"identityName", credential.identityName,
				"federatedCredentialName", credential.name,
			)
			_, err := clients.fics.Delete(ctx, credential.resourceGroup, credential.identityName, credential.name, nil)
			if azureutil.IsNotFound(err) {
				logger.Info("Federated identity credential not found. Nothing to delete",
					"resourceGroup", credential.resourceGroup,
					"identityName", credential.identityName,
					"federatedCredentialName", credential.name,
				)
				return nil
			}
			if err != nil {
				return fmt.Errorf("failed deleting federated identity credential %q on identity %q in resource group %q: %w",
					credential.name, credential.identityName, credential.resourceGroup, err)
			}
			logger.Info("Deleted federated identity credential",
				"resourceGroup", credential.resourceGroup,
				"identityName", credential.identityName,
				"federatedCredentialName", credential.name,
			)
			return nil
		})
	}
	return operations
}

func deleteRoleAssignments(logger logr.Logger, clients *admitClients, assignments []*armauthorization.RoleAssignment) []func(context.Context) error {
	operations := make([]func(context.Context) error, 0, len(assignments))
	for _, assignment := range assignments {
		operations = append(operations, func(ctx context.Context) error {
			if assignment.ID == nil {
				return errors.New("role assignment has no ID")
			}
			logger.Info("Attempting to delete role assignment", "roleAssignmentResourceID", *assignment.ID)
			_, err := clients.roles.DeleteByID(ctx, *assignment.ID, nil)
			if azureutil.IsNotFound(err) {
				logger.Info("Role assignment not found. Nothing to delete", "roleAssignmentResourceID", *assignment.ID)
				return nil
			}
			if err != nil {
				return fmt.Errorf("failed deleting role assignment %q: %w", *assignment.ID, err)
			}
			logger.Info("Deleted role assignment",
				"roleAssignmentResourceID", *assignment.ID,
				"roleAssignmentPrincipalID", *assignment.Properties.PrincipalID,
				"roleAssignmentRoleDefinitionResourceID", assignment.Properties.RoleDefinitionID,
				"roleAssignmentScope", ptr.Deref(assignment.Properties.Scope, "<nil>"),
				"roleAssignmentCreatedBy", ptr.Deref(assignment.Properties.CreatedBy, "<nil>"),
				"roleAssignmentCreatedOn", ptr.Deref(assignment.Properties.CreatedOn, time.Time{}),
			)
			return nil
		})
	}
	return operations
}
