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
	"time"

	"github.com/spf13/pflag"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/cmd/workflow"
	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/policy"
)

// Name selects this workflow with --workflow and prefixes its options.
const Name = "certificates"

// defaultTimeout bounds a run when --timeout is unset; the other workflows have
// no overall timeout by default.
const defaultTimeout = 30 * time.Minute

// Workflow is the certificates cleanup workflow as seen by the command.
type Workflow struct {
	deleteActive bool
	purgeDeleted bool
	maxDeletions int
	maxPurges    int
	policy       *policy.CertificatePolicy
}

var _ workflow.Workflow = (*Workflow)(nil)

// New returns a certificates workflow with default options.
func New() *Workflow {
	return &Workflow{deleteActive: true, purgeDeleted: true, maxDeletions: 1000, maxPurges: 1000}
}

func (w *Workflow) Name() string { return Name }

func (w *Workflow) AttachOptions(flags *pflag.FlagSet) {
	flags.BoolVar(&w.deleteActive, "delete-active", w.deleteActive, "Discover and soft-delete old, unowned active certificates.")
	flags.BoolVar(&w.purgeDeleted, "purge-deleted", w.purgeDeleted, "Purge each deleted certificate immediately, and discover and purge eligible previously deleted certificates (tombstones).")
	flags.IntVar(&w.maxDeletions, "max-deletions", w.maxDeletions, "Maximum active certificates selected for deletion per run; inventory continues until this budget is filled or exhausted (must be positive; also caps dry-run selections).")
	flags.IntVar(&w.maxPurges, "max-purges", w.maxPurges, "Maximum previously deleted certificates (tombstones) purged per run; the immediate purge of each deleted active certificate is bounded by max-deletions instead (must be positive; also caps dry-run selections).")
}

func (w *Workflow) Validate(common workflow.Common) error {
	if err := w.runOptions(common).Validate(); err != nil {
		return err
	}
	if common.PolicyFile == "" {
		return fmt.Errorf("--policy is required for %s workflow", Name)
	}
	loaded, err := policy.LoadCertificates(common.PolicyFile)
	if err != nil {
		return err
	}
	w.policy = loaded
	return nil
}

// LogValues is empty: the scope comes from the policy file, which is logged.
func (w *Workflow) LogValues() []any { return nil }

func (w *Workflow) Run(ctx context.Context, common workflow.Common, credential azcore.TokenCredential) error {
	if common.Timeout == 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultTimeout)
		defer cancel()
	}
	opts := w.runOptions(common)
	opts.AzureCredential = credential
	opts.Policy = w.policy
	return Run(ctx, opts)
}

func (w *Workflow) runOptions(common workflow.Common) RunOptions {
	return RunOptions{
		DryRun:       common.DryRun,
		DeleteActive: w.deleteActive,
		PurgeDeleted: w.purgeDeleted,
		MaxDeletions: w.maxDeletions,
		MaxPurges:    w.maxPurges,
		Parallelism:  common.Parallelism,
	}
}
