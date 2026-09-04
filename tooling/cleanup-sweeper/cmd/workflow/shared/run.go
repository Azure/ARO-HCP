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

package shared

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	cleanupengine "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine"
)

// RunOptions configures shared-leftovers workflow execution.
type RunOptions struct {
	SubscriptionID  string
	AzureCredential azcore.TokenCredential
	// GraphCredential is used exclusively for Microsoft Graph directory reads.
	// When nil it defaults to AzureCredential.
	GraphCredential azcore.TokenCredential
	// DirectoryWriteCredential backs a second Graph client used only by the
	// aged-deleted-directory-object purge step, which requires
	// Directory.ReadWrite.All / Application.ReadWrite.All - materially higher
	// privilege than GraphCredential needs. When nil, that step is omitted
	// entirely rather than reusing GraphCredential or AzureCredential.
	DirectoryWriteCredential azcore.TokenCredential

	DryRun      bool
	Wait        bool
	Parallelism int
}

// Run executes the shared-leftovers workflow.
func Run(ctx context.Context, opts RunOptions) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		panic(err)
	}
	logger.Info("Executing shared-leftovers workflow", "implementation", "role-assignments-sweeper")

	workflow, err := cleanupengine.RoleAssignmentsSweeperWorkflow(
		ctx,
		opts.SubscriptionID,
		opts.AzureCredential,
		opts.GraphCredential,
		opts.DirectoryWriteCredential,
		cleanupengine.WorkflowOptions{
			DryRun:      opts.DryRun,
			Wait:        opts.Wait,
			Parallelism: opts.Parallelism,
		},
	)
	if err != nil {
		return err
	}

	if err := workflow.Run(ctx); err != nil {
		return fmt.Errorf("shared-leftovers cleanup failed: %w", err)
	}

	logger.Info("Finished shared-leftovers workflow", "implementation", "role-assignments-sweeper")
	return nil
}
