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
	kvsteps "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/steps/keyvault"
	roleassignmentsteps "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/steps/roleassignments"
)

const (
	orphanedRoleAssignmentStepRetries = 3
	orphanedVaultStepRetries          = 3
)

// RoleAssignmentsSweeperWorkflow builds the shared-leftovers cleanup workflow.
func RoleAssignmentsSweeperWorkflow(
	_ context.Context,
	subscriptionID string,
	credential azcore.TokenCredential,
	opts WorkflowOptions,
) (*runner.Engine, error) {
	if strings.TrimSpace(subscriptionID) == "" {
		return nil, fmt.Errorf("subscription ID is required")
	}
	if credential == nil {
		return nil, fmt.Errorf("azure credential is required")
	}

	clientOptions := normalizeARMClientOptions(opts.ClientOptions)

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

	return &runner.Engine{
		Parallelism: opts.Parallelism,
		DryRun:      opts.DryRun,
		Wait:        opts.Wait,
		Steps: []runner.Step{
			roleassignmentsteps.MustNewDeleteOrphanedStep(roleassignmentsteps.DeleteOrphanedStepConfig{
				RoleAssignmentsClient:       roleAssignmentsClient,
				AzureCredential:             credential,
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
		},
	}, nil
}
