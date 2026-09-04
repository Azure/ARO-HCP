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

package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
	directoryobjectsteps "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/steps/directoryobjects"
	kvsteps "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/steps/keyvault"
	roleassignmentsteps "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/steps/roleassignments"
)

const (
	orphanedRoleAssignmentStepRetries = 3
	orphanedVaultStepRetries          = 3
	agedDeletedObjectStepRetries      = 3
)

// RoleAssignmentsSweeperWorkflow builds the shared-leftovers cleanup workflow.
//
// credential backs the ARM clients (role assignments, key vaults, resource
// groups). graphCredential is used exclusively for the Microsoft Graph
// directory reads performed by the orphaned role-assignment step; when nil it
// defaults to credential, preserving single-identity behavior.
//
// directoryWriteCredential, when non-nil, backs a second Graph client used
// only by the aged-deleted-directory-object purge step. That step permanently
// deletes directory objects (Directory.ReadWrite.All / Application.ReadWrite.All),
// a materially higher privilege than the read-only access graphCredential
// needs, so the two are kept separate rather than reusing graphCredential.
// When nil, the aged-deleted-directory-object purge step is omitted entirely -
// it is opt-in, since most callers won't have an identity holding that grant.
func RoleAssignmentsSweeperWorkflow(
	_ context.Context,
	subscriptionID string,
	credential azcore.TokenCredential,
	graphCredential azcore.TokenCredential,
	directoryWriteCredential azcore.TokenCredential,
	opts WorkflowOptions,
) (*runner.Engine, error) {
	if strings.TrimSpace(subscriptionID) == "" {
		return nil, fmt.Errorf("subscription ID is required")
	}
	if credential == nil {
		return nil, fmt.Errorf("azure credential is required")
	}
	if graphCredential == nil {
		graphCredential = credential
	}

	clientOptions := normalizeARMClientOptions(opts.ClientOptions)

	graphClient, err := roleassignmentsteps.NewGraphClient(graphCredential)
	if err != nil {
		return nil, fmt.Errorf("failed to create graph client: %w", err)
	}

	roleAssignmentsClient, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, credential, clientOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to create role assignments client: %w", err)
	}

	vaultsClient, err := armkeyvault.NewVaultsClient(subscriptionID, credential, clientOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to create vaults client: %w", err)
	}
	rgClient, err := armresources.NewResourceGroupsClient(subscriptionID, credential, clientOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource groups client: %w", err)
	}
	resourceGroupExists := func(ctx context.Context, resourceGroupName string) (bool, error) {
		resp, err := rgClient.CheckExistence(ctx, resourceGroupName, nil)
		if err != nil {
			return false, err
		}
		return resp.Success, nil
	}

	steps := []runner.Step{}

	if directoryWriteCredential != nil {
		directoryWriteGraphClient, err := roleassignmentsteps.NewGraphClient(directoryWriteCredential)
		if err != nil {
			return nil, fmt.Errorf("failed to create directory-write graph client: %w", err)
		}
		// Runs before the orphaned-role-assignment step: purging an aged
		// deletedItems object here means its role assignment is picked up as
		// orphaned by that step in this very same run instead of waiting for
		// Entra's 30-day recycle-bin timer.
		steps = append(steps, directoryobjectsteps.MustNewPurgeAgedDeletedStep(directoryobjectsteps.PurgeAgedDeletedStepConfig{
			RoleAssignmentsClient: roleAssignmentsClient,
			GraphClient:           directoryWriteGraphClient,
			SubscriptionID:        subscriptionID,
			Name:                  "Purge aged deleted directory objects",
			Retries:               agedDeletedObjectStepRetries,
			ContinueOnError:       true,
		}))
	}

	steps = append(steps,
		roleassignmentsteps.MustNewDeleteOrphanedStep(roleassignmentsteps.DeleteOrphanedStepConfig{
			RoleAssignmentsClient:       roleAssignmentsClient,
			GraphClient:                 graphClient,
			SubscriptionID:              subscriptionID,
			Name:                        "Delete orphaned role assignments",
			Retries:                     orphanedRoleAssignmentStepRetries,
			ContinueOnTargetDeleteError: true,
		}),
		kvsteps.MustNewPurgeOrphanedDeletedStep(kvsteps.PurgeOrphanedDeletedStepConfig{
			VaultsClient:        vaultsClient,
			ResourceGroupExists: resourceGroupExists,
			Name:                "Purge orphaned soft-deleted Key Vaults",
			Retries:             orphanedVaultStepRetries,
			ContinueOnError:     true,
		}),
	)

	return &runner.Engine{
		Parallelism: opts.Parallelism,
		DryRun:      opts.DryRun,
		Wait:        opts.Wait,
		Steps:       steps,
	}, nil
}
