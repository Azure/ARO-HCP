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
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
)

func TestWorkflowBuilders(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name       string
		execute    func(t *testing.T) (interface{}, error)
		assertions func(t *testing.T, workflow interface{}, err error)
	}{
		{
			name: "role assignments workflow builds shared-leftovers steps",
			execute: func(_ *testing.T) (interface{}, error) {
				return RoleAssignmentsSweeperWorkflow(
					context.Background(),
					"00000000-0000-0000-0000-000000000000",
					workflowsTestCredential{},
					WorkflowOptions{
						DryRun:      true,
						Wait:        true,
						Parallelism: 7,
					},
				)
			},
			assertions: func(t *testing.T, workflow interface{}, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("expected no error while building workflow, got %v", err)
				}
				builtWorkflow, ok := workflow.(*runner.Engine)
				if !ok || builtWorkflow == nil {
					t.Fatalf("expected *runner.Engine workflow")
				}
				if len(builtWorkflow.Steps) != 2 {
					t.Fatalf("expected two steps, got %d", len(builtWorkflow.Steps))
				}
				if builtWorkflow.Parallelism != 7 || !builtWorkflow.DryRun || !builtWorkflow.Wait {
					t.Fatalf("unexpected workflow options: %+v", builtWorkflow)
				}
			},
		},
		{
			name: "resource group ordered workflow propagates canceled context",
			execute: func(_ *testing.T) (interface{}, error) {
				baseCtx := logr.NewContext(context.Background(), logr.Discard())
				ctx, cancel := context.WithCancel(baseCtx)
				cancel()
				return ResourceGroupOrderedCleanupWorkflow(
					ctx,
					"rg-example",
					"00000000-0000-0000-0000-000000000000",
					workflowsTestCredential{},
					WorkflowOptions{},
				)
			},
			assertions: func(t *testing.T, _ interface{}, err error) {
				t.Helper()
				if err == nil {
					t.Fatalf("expected error when context is canceled")
				}
				if !strings.Contains(err.Error(), "failed to get resource group") {
					t.Fatalf("unexpected error: %v", err)
				}
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			workflow, err := tc.execute(t)
			tc.assertions(t, workflow, err)
		})
	}
}

type workflowsTestCredential struct{}

func (workflowsTestCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}
