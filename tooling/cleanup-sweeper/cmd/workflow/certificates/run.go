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

package certificates

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	cleanupengine "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine"
	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/policy"
)

// RunOptions configures the certificates workflow.
type RunOptions struct {
	AzureCredential azcore.TokenCredential

	DryRun       bool
	DeleteActive bool
	PurgeDeleted bool
	MaxDeletions int
	MaxPurges    int
	Parallelism  int

	Policy *policy.CertificatePolicy
}

// Validate checks the options that do not need the policy file.
func (o RunOptions) Validate() error {
	if !o.DeleteActive && !o.PurgeDeleted {
		return fmt.Errorf("at least one of --delete-active or --purge-deleted must be enabled")
	}
	if o.DeleteActive && o.MaxDeletions <= 0 {
		return fmt.Errorf("--max-deletions must be positive")
	}
	if o.PurgeDeleted && o.MaxPurges <= 0 {
		return fmt.Errorf("--max-purges must be positive")
	}
	if o.Parallelism <= 0 {
		return fmt.Errorf("--parallelism must be positive")
	}
	return nil
}

// Run executes the certificates workflow against the fixed dev service vault.
func Run(ctx context.Context, opts RunOptions) error {
	if err := opts.Validate(); err != nil {
		return err
	}
	if opts.Policy == nil {
		return fmt.Errorf("--policy is required")
	}
	if err := opts.Policy.Validate("certificates"); err != nil {
		return fmt.Errorf("invalid certificate policy: %w", err)
	}
	workflow, err := cleanupengine.CertificateCleanupWorkflow(ctx, opts.AzureCredential, cleanupengine.CertificateWorkflowOptions{
		DryRun:       opts.DryRun,
		DeleteActive: opts.DeleteActive,
		PurgeDeleted: opts.PurgeDeleted,
		MaxDeletions: opts.MaxDeletions,
		MaxPurges:    opts.MaxPurges,
		Parallelism:  opts.Parallelism,
		Policy:       *opts.Policy,
	})
	if err != nil {
		return err
	}
	return workflow.Run(ctx)
}
