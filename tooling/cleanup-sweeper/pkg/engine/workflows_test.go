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
	"strings"
	"testing"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
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
