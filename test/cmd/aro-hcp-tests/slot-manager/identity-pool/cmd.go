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

package identitypool

import (
	"context"

	"github.com/spf13/cobra"
)

func NewApplyCommand() (*cobra.Command, error) {
	opts := DefaultApplyOptions()

	cmd := &cobra.Command{
		Use:   "apply-identity-pool",
		Short: "Apply the managed identity pool ARM deployment stack.",
		Long: `Apply the managed identity pool ARM deployment stack.

This command applies the managed identity pool ARM deployment stack to the Azure subscription for the given environment

A slot-managed identity pool is a set of resource groups derived from the canonical E2E slot catalog. Each slot gets a
slot-specific MSI container prefix, and the command provisions the per-slot containers required to create a single HCP cluster.

The slot catalog is the source of truth for both the MSI pool layout in ARO-HCP and the ARO-HCP-managed section of the
OpenShift release Boskos configuration.

The identity pool is applied across every pool declared for the selected environment. Each pool uses its catalog-declared
subscription name and region.
`,
		SilenceUsage: true,
	}

	if err := BindApplyOptions(opts, cmd); err != nil {
		return nil, err
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return Apply(cmd.Context(), opts)
	}

	return cmd, nil
}

func Apply(ctx context.Context, opts *RawApplyOptions) error {
	validated, err := opts.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete(ctx)
	if err != nil {
		return err
	}
	return completed.Run(ctx)
}
