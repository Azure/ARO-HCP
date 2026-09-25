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

package identitypool

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/uuid"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
	"github.com/Azure/ARO-HCP/test/util/framework"
)

const admissionPhaseTimeout = 10 * time.Minute

type identityLeaseInventory struct {
	federatedCredentials []federatedCredentialReference
	roleAssignments      []*armauthorization.RoleAssignment
}

type federatedCredentialReference struct {
	resourceGroup string
	identityName  string
	name          string
}

func admitE2EIdentityLease(ctx context.Context, request assets.LeaseRequest) error {
	ctx, cancel := context.WithTimeout(ctx, admissionPhaseTimeout)
	defer cancel()

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

	return admitIdentityLeaseWithClients(ctx, request, msiFactory, roleAssignmentsClient)
}

func admitIdentityLeaseWithClients(ctx context.Context, request assets.LeaseRequest, msiFactory *armmsi.ClientFactory, roleAssignmentsClient *armauthorization.RoleAssignmentsClient) error {
	inventory, err := loadIdentityLeaseInventory(ctx, request, msiFactory, roleAssignmentsClient)
	if err != nil {
		return err
	}

	federatedCredentialsClient := msiFactory.NewFederatedIdentityCredentialsClient()
	deleteOperations := make([]func(context.Context) error, 0, len(inventory.federatedCredentials)+len(inventory.roleAssignments))
	for _, reference := range inventory.federatedCredentials {
		deleteOperations = append(deleteOperations, func(ctx context.Context) error {
			_, err := federatedCredentialsClient.Delete(ctx, reference.resourceGroup, reference.identityName, reference.name, nil)
			if isNotFound(err) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("failed deleting FIC %q from identity %q in resource group %q: %w", reference.name, reference.identityName, reference.resourceGroup, err)
			}
			return nil
		})
	}
	for _, assignment := range inventory.roleAssignments {
		deleteOperations = append(deleteOperations, func(ctx context.Context) error {
			if assignment.ID == nil {
				return errors.New("role assignment has no ID")
			}
			_, err := roleAssignmentsClient.DeleteByID(ctx, *assignment.ID, nil)
			if isNotFound(err) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("failed deleting role assignment %q: %w", *assignment.ID, err)
			}
			return nil
		})
	}

	if err := runSerial(ctx, deleteOperations); err != nil {
		return fmt.Errorf("failed cleaning E2E identity lease: %w", err)
	}
	// Exclusive ownership lets us rely on successful synchronous deletes without
	// polling for absence or repeating the inventory.
	return nil
}

func leaseCredential(request assets.LeaseRequest) (azcore.TokenCredential, string, error) {
	if request.State == nil {
		return nil, "", errors.New("acquired slot state is nil")
	}
	subscriptionID := strings.TrimSpace(request.State.Slot.Subscriptions.E2E.ID)
	if subscriptionID == "" {
		return nil, "", errors.New("resolved E2E subscription ID is empty")
	}
	profileDir := strings.TrimSpace(request.SelectedClusterProfileDir)
	if profileDir == "" {
		return nil, "", errors.New("selected cluster profile dir is empty")
	}

	credential, err := slots.NewClusterProfileCredential(profileDir, nil)
	if err != nil {
		return nil, "", err
	}
	return credential, subscriptionID, nil
}

func loadIdentityLeaseInventory(
	ctx context.Context,
	request assets.LeaseRequest,
	msiFactory *armmsi.ClientFactory,
	roleAssignmentsClient *armauthorization.RoleAssignmentsClient,
) (*identityLeaseInventory, error) {
	if request.State == nil || len(request.State.Slot.IdentityContainerNames()) == 0 {
		return nil, errors.New("resolved E2E identity inventory is empty")
	}
	expectedIdentityNames := framework.NewDefaultIdentities().ToSlice()
	expectedIdentities := make(map[string]struct{}, len(expectedIdentityNames))
	for _, identityName := range expectedIdentityNames {
		expectedIdentities[strings.ToLower(identityName)] = struct{}{}
	}

	inventory := &identityLeaseInventory{}
	principalIDs := map[uuid.UUID]struct{}{}
	federatedCredentialsClient := msiFactory.NewFederatedIdentityCredentialsClient()
	identitiesClient := msiFactory.NewUserAssignedIdentitiesClient()

	for _, resourceGroup := range request.State.Slot.IdentityContainerNames() {
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
				if _, expected := expectedIdentities[normalizedName]; !expected {
					unexpectedIdentities = append(unexpectedIdentities, *identity.Name)
					continue
				}
				if identity.Properties == nil || identity.Properties.PrincipalID == nil || strings.TrimSpace(*identity.Properties.PrincipalID) == "" {
					return nil, fmt.Errorf("identity %q in resource group %q has no principal ID", *identity.Name, resourceGroup)
				}
				principalID, err := parsePrincipalID(*identity.Properties.PrincipalID)
				if err != nil {
					return nil, fmt.Errorf("identity %q has invalid principal ID: %w", *identity.Name, err)
				}
				if _, found := actualIdentities[normalizedName]; found {
					return nil, fmt.Errorf("identity list for resource group %q returned duplicate identity %q", resourceGroup, *identity.Name)
				}
				actualIdentities[normalizedName] = *identity.Name
				principalIDs[principalID] = struct{}{}
			}
		}
		if len(unexpectedIdentities) > 0 {
			sort.Strings(unexpectedIdentities)
			logr.FromContextOrDiscard(ctx).Info("Unexpected identities in leased resource group; excluded from admission cleanup",
				"resourceGroup", resourceGroup,
				"identityNames", unexpectedIdentities,
			)
		}
		if err := validateIdentityNames(resourceGroup, expectedIdentities, actualIdentities); err != nil {
			return nil, err
		}

		for _, identityName := range expectedIdentityNames {
			ficPager := federatedCredentialsClient.NewListPager(resourceGroup, identityName, nil)
			for ficPager.More() {
				page, err := ficPager.NextPage(ctx)
				if err != nil {
					return nil, fmt.Errorf("failed listing FICs for identity %q in resource group %q: %w", identityName, resourceGroup, err)
				}
				for _, credential := range page.Value {
					if credential == nil || credential.Name == nil || strings.TrimSpace(*credential.Name) == "" {
						return nil, fmt.Errorf("FIC list for identity %q in resource group %q returned an entry without a name", identityName, resourceGroup)
					}
					inventory.federatedCredentials = append(inventory.federatedCredentials, federatedCredentialReference{
						resourceGroup: resourceGroup,
						identityName:  identityName,
						name:          *credential.Name,
					})
				}
			}
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
			principalID, err := parsePrincipalID(*assignment.Properties.PrincipalID)
			if err != nil {
				return nil, fmt.Errorf("role assignment list returned invalid principal ID %q: %w", *assignment.Properties.PrincipalID, err)
			}
			if _, found := principalIDs[principalID]; found {
				inventory.roleAssignments = append(inventory.roleAssignments, assignment)
			}
		}
	}
	return inventory, nil
}

func parsePrincipalID(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil, err
	}
	if !strings.EqualFold(value, id.String()) {
		return uuid.Nil, errors.New("principal ID must be a hyphenated UUID without surrounding whitespace")
	}
	return id, nil
}

func validateIdentityNames(resourceGroup string, expected map[string]struct{}, actual map[string]string) error {
	var missing []string
	for name := range expected {
		if _, found := actual[name]; !found {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		return fmt.Errorf("identity inventory drift in resource group %q: missing=%v", resourceGroup, missing)
	}
	return nil
}

func runSerial(ctx context.Context, operations []func(context.Context) error) error {
	var errs []error
	for _, operation := range operations {
		if ctx.Err() != nil {
			break
		}
		if err := runOperation(ctx, operation); err != nil {
			errs = append(errs, err)
		}
	}
	errs = append(errs, ctx.Err())
	return errors.Join(errs...)
}

func runOperation(ctx context.Context, operation func(context.Context) error) (err error) {
	// Preserve fail-closed errors when ReallyCrash is false without bypassing
	// the process crash policy when it is true.
	defer utilruntime.HandleCrash(func(value interface{}) {
		err = fmt.Errorf("identity admission operation panicked: %v", value)
	})
	return operation(ctx)
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	var responseError *azcore.ResponseError
	return errors.As(err, &responseError) && responseError.StatusCode == http.StatusNotFound
}
