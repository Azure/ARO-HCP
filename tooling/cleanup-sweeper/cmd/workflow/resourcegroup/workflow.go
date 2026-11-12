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

package resourcegroup

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/pflag"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/cmd/workflow"
	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/policy"
)

// Name selects this workflow with --workflow and prefixes its options.
const Name = "rg-ordered"

// Workflow is the rg-ordered cleanup workflow as seen by the command.
type Workflow struct {
	subscriptionID string
	resourceGroups []string
	wait           bool
	policy         *policy.Policy
}

var _ workflow.Workflow = (*Workflow)(nil)

// New returns an rg-ordered workflow with default options.
func New() *Workflow {
	return &Workflow{wait: true}
}

func (w *Workflow) Name() string { return Name }

func (w *Workflow) AttachOptions(flags *pflag.FlagSet) {
	flags.StringVar(&w.subscriptionID, "subscription-id", w.subscriptionID, "Subscription ID to clean (required).")
	flags.StringSliceVar(&w.resourceGroups, "resource-group", w.resourceGroups, "Explicit resource group target (repeatable). Optional; when omitted, discovery rules drive candidate selection.")
	flags.BoolVar(&w.wait, "wait", w.wait, "Wait for long-running deletions to complete.")
}

func (w *Workflow) Validate(common workflow.Common) error {
	if strings.TrimSpace(w.subscriptionID) == "" {
		return fmt.Errorf("%s.subscription-id is required", Name)
	}
	if common.PolicyFile == "" {
		return fmt.Errorf("--policy is required for %s workflow", Name)
	}
	loaded, err := policy.Load(common.PolicyFile)
	if err != nil {
		return err
	}
	if err := loaded.Validate(); err != nil {
		return fmt.Errorf("invalid --policy content: %w", err)
	}
	w.policy = loaded
	return nil
}

func (w *Workflow) LogValues() []any {
	return []any{"subscriptionID", strings.TrimSpace(w.subscriptionID)}
}

func (w *Workflow) Run(ctx context.Context, common workflow.Common, credential azcore.TokenCredential) error {
	return Run(ctx, RunOptions{
		SubscriptionID:  strings.TrimSpace(w.subscriptionID),
		AzureCredential: credential,
		DryRun:          common.DryRun,
		Wait:            w.wait,
		Parallelism:     common.Parallelism,
		ResourceGroups:  setFromTrimmed(w.resourceGroups),
		Policy:          w.policy.RGOrdered,
		ReferenceTime:   time.Now().UTC(),
	})
}

// setFromTrimmed collects nonempty, trimmed selectors.
func setFromTrimmed(values []string) sets.Set[string] {
	result := sets.New[string]()
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			result.Insert(trimmed)
		}
	}
	return result
}
