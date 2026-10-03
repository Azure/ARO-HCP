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

package mipool

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	armauthorization "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
)

// CleanupContainer removes residual FICs and role assignments from a single
// MI container resource group. It is used both at startup (sweep all
// containers) and after a child process exits (per-container cleanup).
// identityNames is the list of well-known MSI role names inside the container
// (e.g. from framework.NewDefaultIdentities().ToSlice()).
func CleanupContainer(
	ctx context.Context,
	logger *slog.Logger,
	ficsClient *armmsi.FederatedIdentityCredentialsClient,
	roleAssignmentsClient *armauthorization.RoleAssignmentsClient,
	subscriptionID string,
	resourceGroup string,
	identityNames []string,
) error {
	var errs []error

	if err := cleanupFICs(ctx, logger, ficsClient, resourceGroup, identityNames); err != nil {
		errs = append(errs, fmt.Errorf("FIC cleanup: %w", err))
	}

	if err := cleanupRoleAssignments(ctx, logger, roleAssignmentsClient, subscriptionID, resourceGroup); err != nil {
		errs = append(errs, fmt.Errorf("role assignment cleanup: %w", err))
	}

	return errors.Join(errs...)
}

// CleanupAllContainers runs CleanupContainer on every container in the pool.
// It is intended to be called once at binary start to sanitize residual Azure
// state from prior crashed runs.
func CleanupAllContainers(
	ctx context.Context,
	logger *slog.Logger,
	creds azcore.TokenCredential,
	subscriptionID string,
	containers []string,
	identityNames []string,
) error {
	if len(containers) == 0 {
		return nil
	}

	msiClientFactory, err := armmsi.NewClientFactory(subscriptionID, creds, nil)
	if err != nil {
		return fmt.Errorf("failed to create MSI client factory: %w", err)
	}
	ficsClient := msiClientFactory.NewFederatedIdentityCredentialsClient()

	roleAssignmentsClient, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, creds, nil)
	if err != nil {
		return fmt.Errorf("failed to create role assignments client: %w", err)
	}

	logger.Info("Starting startup reconciliation sweep", "containers", len(containers))

	var mu sync.Mutex
	var errs []error
	var wg sync.WaitGroup

	for _, rg := range containers {
		wg.Add(1)
		go func(resourceGroup string) {
			defer wg.Done()
			containerLogger := logger.With("resourceGroup", resourceGroup)
			containerLogger.Info("Sweeping container for residual state")

			if err := CleanupContainer(ctx, containerLogger, ficsClient, roleAssignmentsClient, subscriptionID, resourceGroup, identityNames); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("container %s: %w", resourceGroup, err))
				mu.Unlock()
			}
		}(rg)
	}

	wg.Wait()

	if len(errs) > 0 {
		return fmt.Errorf("startup reconciliation sweep: %w", errors.Join(errs...))
	}

	logger.Info("Startup reconciliation sweep complete", "containers", len(containers))
	return nil
}

func cleanupFICs(
	ctx context.Context,
	logger *slog.Logger,
	ficsClient *armmsi.FederatedIdentityCredentialsClient,
	resourceGroup string,
	identityNames []string,
) error {
	var mu sync.Mutex
	var errs []error
	var wg sync.WaitGroup

	for _, identityName := range identityNames {
		wg.Add(1)
		go func(identity string) {
			defer wg.Done()

			var identityErrs []error
			pager := ficsClient.NewListPager(resourceGroup, identity, nil)
			for pager.More() {
				page, err := pager.NextPage(ctx)
				if err != nil {
					var respErr *azcore.ResponseError
					if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
						break
					}
					identityErrs = append(identityErrs, fmt.Errorf("list FICs for %q: %w", identity, err))
					break
				}
				for _, fic := range page.Value {
					if _, err := ficsClient.Delete(ctx, resourceGroup, identity, *fic.Name, nil); err != nil {
						var respErr *azcore.ResponseError
						if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
							continue
						}
						identityErrs = append(identityErrs, fmt.Errorf("delete FIC %q on %q: %w", *fic.Name, identity, err))
					} else {
						logger.Info("Deleted residual FIC", "identity", identity, "fic", *fic.Name)
					}
				}
			}

			if len(identityErrs) > 0 {
				mu.Lock()
				errs = append(errs, identityErrs...)
				mu.Unlock()
			}
		}(identityName)
	}

	wg.Wait()
	return errors.Join(errs...)
}

func cleanupRoleAssignments(
	ctx context.Context,
	logger *slog.Logger,
	client *armauthorization.RoleAssignmentsClient,
	subscriptionID string,
	resourceGroup string,
) error {
	scope := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionID, resourceGroup)

	var toDelete []*armauthorization.RoleAssignment
	pager := client.NewListForResourceGroupPager(resourceGroup, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("list role assignments for %s: %w", resourceGroup, err)
		}
		for _, ra := range page.Value {
			if !strings.HasPrefix(strings.ToLower(*ra.Properties.Scope), strings.ToLower(scope)) {
				continue
			}
			toDelete = append(toDelete, ra)
		}
	}

	if len(toDelete) == 0 {
		return nil
	}

	logger.Info("Deleting residual role assignments", "count", len(toDelete))

	var errs []error
	for _, ra := range toDelete {
		if _, err := client.Delete(ctx, *ra.Properties.Scope, *ra.Name, nil); err != nil {
			var respErr *azcore.ResponseError
			if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
				continue
			}
			errs = append(errs, fmt.Errorf("delete role assignment %s: %w", *ra.ID, err))
		} else {
			logger.Info("Deleted residual role assignment", "assignmentID", *ra.ID)
		}
	}

	return errors.Join(errs...)
}
