package engine

import (
	"context"
	"fmt"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
	roleassignmentsteps "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/steps/roleassignments"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
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
