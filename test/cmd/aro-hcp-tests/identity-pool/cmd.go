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

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/test/pkg/logger"
)

func NewCommand() (*cobra.Command, error) {
	var logVerbosity int

	cmd := &cobra.Command{
		Use:           "identity-pool",
		Short:         "Manage the pooled managed identities used by e2e tests.",
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	cmd.PersistentFlags().IntVarP(&logVerbosity, "verbosity", "v", 0, "set the verbosity level")
	cmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		ctx := logr.NewContext(cmd.Context(), logger.NewWithVerbosity(logVerbosity))
		cmd.SetContext(ctx)
	}
	cmd.AddCommand(api.Must(newApplyCommand()))

	return cmd, nil
}

func newApplyCommand() (*cobra.Command, error) {
	opts := DefaultApplyOptions()

	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply the managed identity pool ARM deployment stack.",
		Long: `Apply the managed identity pool ARM deployment stack.

This command applies the managed identity pool ARM deployment stack to the Azure subscription for the given environment

An identity pool is a number of resource groups, determined by the pool size, containing the managed identities required to
create a single HCP cluster.

The identity pool must be kept in sync with the Boskos leasing server configuration 
https://github.com/openshift/release/blob/master/core-services/prow/02_config/generate-boskos.py#L687-L691

The identity pool is applied to the Azure subscription for the given environment.
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
