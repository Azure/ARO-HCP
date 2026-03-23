package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
	"github.com/go-logr/logr"
)

func TestRoleAssignmentsSweeperWorkflow_BuildsSingleStep(t *testing.T) {
	t.Parallel()

	workflow, err := RoleAssignmentsSweeperWorkflow(
		context.Background(),
		"00000000-0000-0000-0000-000000000000",
		nil,
		WorkflowOptions{
			DryRun:      true,
			Wait:        true,
			Parallelism: 7,
		},
	)
	if err != nil {
		t.Fatalf("expected no error while building workflow, got %v", err)
	}
	if workflow == nil {
		t.Fatalf("expected workflow")
	}
	if len(workflow.Steps) != 1 {
		t.Fatalf("expected one step, got %d", len(workflow.Steps))
	}
	if workflow.Parallelism != 7 || !workflow.DryRun || !workflow.Wait {
		t.Fatalf("unexpected workflow options: %+v", workflow)
	}
}

func TestResourceGroupOrderedCleanupWorkflow_PropagatesContextCancellation(t *testing.T) {
	t.Parallel()

	baseCtx := runner.ContextWithLogger(context.Background(), logr.Discard())
	ctx, cancel := context.WithCancel(baseCtx)
	cancel()

	_, err := ResourceGroupOrderedCleanupWorkflow(
		ctx,
		"rg-example",
		"00000000-0000-0000-0000-000000000000",
		nil,
		WorkflowOptions{},
	)
	if err == nil {
		t.Fatalf("expected error when context is canceled")
	}
	if !strings.Contains(err.Error(), "failed to get resource group") {
		t.Fatalf("unexpected error: %v", err)
	}
}
