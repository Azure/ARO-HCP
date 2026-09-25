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
	"sync"
	"time"

	"github.com/google/uuid"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
	"github.com/Azure/ARO-HCP/test/util/framework"
)

const (
	admissionInventoryParallelism = 8
	admissionDeleteParallelism    = 8
	admissionPreparationTimeout   = 10 * time.Minute
	admissionValidationTimeout    = 2 * time.Minute
)

type identityLeaseInventory struct {
	federatedCredentials []federatedCredentialReference
	roleAssignments      []*armauthorization.RoleAssignment
}

type federatedCredentialReference struct {
	resourceGroup string
	identityName  string
	name          string
}

type resolvedIdentityReference struct {
	resourceGroup string
	name          string
}

type dirtyIdentityLeaseError struct {
	message string
}

func (e *dirtyIdentityLeaseError) Error() string {
	return e.message
}

func prepareE2EIdentityLease(ctx context.Context, request assets.LeaseRequest) error {
	ctx, cancel := context.WithTimeout(ctx, admissionPreparationTimeout)
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

	return prepareIdentityLeaseWithClients(ctx, request, msiFactory, roleAssignmentsClient)
}

func prepareIdentityLeaseWithClients(ctx context.Context, request assets.LeaseRequest, msiFactory *armmsi.ClientFactory, roleAssignmentsClient *armauthorization.RoleAssignmentsClient) error {
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

	if err := runBounded(ctx, admissionDeleteParallelism, deleteOperations); err != nil {
		return fmt.Errorf("failed cleaning E2E identity lease: %w", err)
	}
	return waitForCleanIdentityLease(ctx, inventory, msiFactory, roleAssignmentsClient)
}

func validateE2EIdentityLease(ctx context.Context, request assets.LeaseRequest) error {
	ctx, cancel := context.WithTimeout(ctx, admissionValidationTimeout)
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
	return validateCleanIdentityLease(ctx, request, msiFactory, roleAssignmentsClient)
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
	principalIDs := map[string]struct{}{}
	federatedCredentialsClient := msiFactory.NewFederatedIdentityCredentialsClient()
	identitiesClient := msiFactory.NewUserAssignedIdentitiesClient()

	var inventoryLock sync.Mutex
	resolvedIdentities := make([]resolvedIdentityReference, 0)
	identityInventoryOperations := make([]func(context.Context) error, 0, len(request.State.Slot.IdentityContainerNames()))
	for _, resourceGroup := range request.State.Slot.IdentityContainerNames() {
		identityInventoryOperations = append(identityInventoryOperations, func(ctx context.Context) error {
			actualIdentities := map[string]string{}
			var localPrincipalIDs []string
			pager := identitiesClient.NewListByResourceGroupPager(resourceGroup, nil)
			for pager.More() {
				page, err := pager.NextPage(ctx)
				if err != nil {
					return fmt.Errorf("failed listing identities in resource group %q: %w", resourceGroup, err)
				}
				for _, identity := range page.Value {
					if identity == nil || identity.Name == nil || identity.Properties == nil || identity.Properties.PrincipalID == nil || strings.TrimSpace(*identity.Properties.PrincipalID) == "" {
						return fmt.Errorf("identity list for resource group %q returned an entry without name or principal ID", resourceGroup)
					}
					if _, err := uuid.Parse(*identity.Properties.PrincipalID); err != nil {
						return fmt.Errorf("identity %q has invalid principal ID: %w", *identity.Name, err)
					}
					normalizedName := strings.ToLower(*identity.Name)
					if _, found := actualIdentities[normalizedName]; found {
						return fmt.Errorf("identity list for resource group %q returned duplicate identity %q", resourceGroup, *identity.Name)
					}
					actualIdentities[normalizedName] = *identity.Name
					localPrincipalIDs = append(localPrincipalIDs, strings.ToLower(*identity.Properties.PrincipalID))
				}
			}
			if err := validateIdentityNames(resourceGroup, expectedIdentities, actualIdentities); err != nil {
				return err
			}

			inventoryLock.Lock()
			for _, identityName := range expectedIdentityNames {
				resolvedIdentities = append(resolvedIdentities, resolvedIdentityReference{
					resourceGroup: resourceGroup,
					name:          identityName,
				})
			}
			for _, principalID := range localPrincipalIDs {
				principalIDs[principalID] = struct{}{}
			}
			inventoryLock.Unlock()
			return nil
		})
	}
	if err := runBounded(ctx, admissionInventoryParallelism, identityInventoryOperations); err != nil {
		return nil, fmt.Errorf("failed loading E2E identity inventory: %w", err)
	}

	ficInventoryOperations := make([]func(context.Context) error, 0, len(resolvedIdentities))
	for _, identity := range resolvedIdentities {
		ficInventoryOperations = append(ficInventoryOperations, func(ctx context.Context) error {
			localCredentials := make([]federatedCredentialReference, 0)
			ficPager := federatedCredentialsClient.NewListPager(identity.resourceGroup, identity.name, nil)
			for ficPager.More() {
				page, err := ficPager.NextPage(ctx)
				if err != nil {
					return fmt.Errorf("failed listing FICs for identity %q in resource group %q: %w", identity.name, identity.resourceGroup, err)
				}
				for _, credential := range page.Value {
					if credential == nil || credential.Name == nil || strings.TrimSpace(*credential.Name) == "" {
						return fmt.Errorf("FIC list for identity %q in resource group %q returned an entry without a name", identity.name, identity.resourceGroup)
					}
					localCredentials = append(localCredentials, federatedCredentialReference{
						resourceGroup: identity.resourceGroup,
						identityName:  identity.name,
						name:          *credential.Name,
					})
				}
			}
			inventoryLock.Lock()
			inventory.federatedCredentials = append(inventory.federatedCredentials, localCredentials...)
			inventoryLock.Unlock()
			return nil
		})
	}
	if err := runBounded(ctx, admissionInventoryParallelism, ficInventoryOperations); err != nil {
		return nil, fmt.Errorf("failed loading E2E federated identity credential inventory: %w", err)
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
			if _, found := principalIDs[strings.ToLower(*assignment.Properties.PrincipalID)]; found {
				inventory.roleAssignments = append(inventory.roleAssignments, assignment)
			}
		}
	}
	return inventory, nil
}

func validateIdentityNames(resourceGroup string, expected map[string]struct{}, actual map[string]string) error {
	var missing, unexpected []string
	for name := range expected {
		if _, found := actual[name]; !found {
			missing = append(missing, name)
		}
	}
	for normalizedName, name := range actual {
		if _, found := expected[normalizedName]; !found {
			unexpected = append(unexpected, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(unexpected)
	if len(missing) > 0 || len(unexpected) > 0 {
		return fmt.Errorf("identity inventory drift in resource group %q: missing=%v unexpected=%v", resourceGroup, missing, unexpected)
	}
	return nil
}

func identityLeaseValidationBackoff() wait.Backoff {
	backoff := retry.DefaultBackoff
	backoff.Duration = 5 * time.Second
	backoff.Factor = 2
	backoff.Cap = time.Minute
	// Jitter is applied after Cap, so disable it to keep a strict one-minute limit.
	backoff.Jitter = 0
	return backoff
}

func waitForCleanIdentityLease(
	ctx context.Context,
	inventory *identityLeaseInventory,
	msiFactory *armmsi.ClientFactory,
	roleAssignmentsClient *armauthorization.RoleAssignmentsClient,
) error {
	waitCtx, cancel := context.WithTimeout(ctx, admissionValidationTimeout)
	defer cancel()

	// Only re-read resources we deleted. ValidateLease independently reloads the
	// complete inventory before publication, including any newly added residue.
	checks := make([]func(context.Context) error, 0, len(inventory.federatedCredentials)+len(inventory.roleAssignments))
	credentialsClient := msiFactory.NewFederatedIdentityCredentialsClient()
	for _, reference := range inventory.federatedCredentials {
		checks = append(checks, func(ctx context.Context) error {
			_, err := credentialsClient.Get(ctx, reference.resourceGroup, reference.identityName, reference.name, nil)
			if isNotFound(err) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("failed verifying deletion of FIC %q from identity %q in resource group %q: %w", reference.name, reference.identityName, reference.resourceGroup, err)
			}
			return &dirtyIdentityLeaseError{message: fmt.Sprintf("FIC %q on identity %q in resource group %q still exists after preparation", reference.name, reference.identityName, reference.resourceGroup)}
		})
	}
	for _, assignment := range inventory.roleAssignments {
		if assignment == nil || assignment.ID == nil || strings.TrimSpace(*assignment.ID) == "" {
			return errors.New("cannot verify deletion of role assignment without an ID")
		}
		checks = append(checks, func(ctx context.Context) error {
			_, err := roleAssignmentsClient.GetByID(ctx, *assignment.ID, nil)
			if isNotFound(err) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("failed verifying deletion of role assignment %q: %w", *assignment.ID, err)
			}
			return &dirtyIdentityLeaseError{message: fmt.Sprintf("role assignment %q still exists after preparation", *assignment.ID)}
		})
	}

	var lastErr error
	// Unlike retry.OnError, DelayFunc.Until interrupts sleeps on cancellation and
	// keeps retrying at the cap until the convergence deadline.
	err := identityLeaseValidationBackoff().DelayFunc().Until(waitCtx, true, true, func(ctx context.Context) (bool, error) {
		results := make([]error, len(checks))
		operations := make([]func(context.Context) error, len(checks))
		for i, check := range checks {
			operations[i] = func(ctx context.Context) error {
				results[i] = check(ctx)
				var dirtyErr *dirtyIdentityLeaseError
				if errors.As(results[i], &dirtyErr) {
					return nil
				}
				return results[i]
			}
		}
		if err := runBounded(ctx, admissionInventoryParallelism, operations); err != nil {
			return false, err
		}
		pending := checks[:0]
		for i, check := range checks {
			if results[i] != nil {
				pending = append(pending, check)
			}
		}
		checks = pending
		lastErr = errors.Join(results...)
		return len(checks) == 0, nil
	})
	if ctx.Err() != nil {
		return fmt.Errorf("waiting for clean E2E identity lease: %w", ctx.Err())
	}
	if waitCtx.Err() != nil {
		return fmt.Errorf("E2E identity lease did not converge to a clean baseline: %w", errors.Join(waitCtx.Err(), lastErr))
	}
	return err
}

func validateCleanIdentityLease(
	ctx context.Context,
	request assets.LeaseRequest,
	msiFactory *armmsi.ClientFactory,
	roleAssignmentsClient *armauthorization.RoleAssignmentsClient,
) error {
	inventory, err := loadIdentityLeaseInventory(ctx, request, msiFactory, roleAssignmentsClient)
	if err != nil {
		return err
	}
	if len(inventory.federatedCredentials) > 0 {
		return &dirtyIdentityLeaseError{message: fmt.Sprintf("found %d federated identity credential(s) after preparation", len(inventory.federatedCredentials))}
	}
	if len(inventory.roleAssignments) > 0 {
		return &dirtyIdentityLeaseError{message: fmt.Sprintf("found %d role assignment(s) after preparation", len(inventory.roleAssignments))}
	}
	return nil
}

func runBounded(ctx context.Context, parallelism int, operations []func(context.Context) error) error {
	if len(operations) == 0 {
		return nil
	}
	if parallelism <= 0 {
		return fmt.Errorf("parallelism must be greater than zero")
	}

	jobs := make(chan func(context.Context) error)
	errCh := make(chan error, len(operations))
	var wg sync.WaitGroup
	workers := min(parallelism, len(operations))
	wg.Add(workers)
	for range workers {
		go func() {
			defer utilruntime.HandleCrash()
			defer wg.Done()
			for operation := range jobs {
				if ctx.Err() != nil {
					continue
				}
				if err := runOperation(ctx, operation); err != nil {
					errCh <- err
				}
			}
		}()
	}
enqueue:
	for _, operation := range operations {
		select {
		case <-ctx.Done():
			break enqueue
		case jobs <- operation:
		}
	}
	close(jobs)
	wg.Wait()
	close(errCh)

	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}
	errs = append(errs, ctx.Err())
	return errors.Join(errs...)
}

func runOperation(ctx context.Context, operation func(context.Context) error) (err error) {
	// Recover at the operation boundary so workers keep draining the producer
	// when ReallyCrash is false. HandleCrash still owns the process crash policy.
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
