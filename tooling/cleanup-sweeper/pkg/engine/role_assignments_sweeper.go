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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
	roleassignmentsteps "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/steps/roleassignments"
)

const orphanedRoleAssignmentStepRetries = 3

func RoleAssignmentsSweeperWorkflow(
	_ context.Context,
	subscriptionID string,
	credential azcore.TokenCredential,
	opts WorkflowOptions,
) (*runner.Engine, error) {
	roleAssignmentsClient, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create role assignments client: %w", err)
	}

	return &runner.Engine{
		Parallelism: opts.Parallelism,
		DryRun:      opts.DryRun,
		Wait:        opts.Wait,
		Steps: []runner.Step{
			roleassignmentsteps.NewDeleteOrphanedStep(roleassignmentsteps.DeleteOrphanedStepConfig{
				RoleAssignmentsClient: roleAssignmentsClient,
				AzureCredential:       credential,
				SubscriptionID:        subscriptionID,
				Name:                  "Delete orphaned role assignments",
				Retries:               orphanedRoleAssignmentStepRetries,
				// Keep fail-closed behavior: this step enforces a mandatory Graph
				// visibility preflight, and turning ContinueOnError on would swallow
				// that safety failure instead of stopping the run.
				ContinueOnError: false,
			}),
		},
	}, nil
}
