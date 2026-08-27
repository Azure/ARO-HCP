// Copyright 2025 Microsoft Corporation
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

package mergegate

import (
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/test/pkg/logger"
)

func NewCommand() (*cobra.Command, error) {
	var logVerbosity int

	opts := DefaultOptions()
	cmd := &cobra.Command{
		Use:   "merge-gate",
		Short: "Ask the release dashboard whether this PR should merge given production alerts.",
		Long: "merge-gate posts the prow JOB_SPEC to the release-dashboard merge-gate API and fails " +
			"the test run when the change touches components with unresolved production alerts. " +
			"The job spec defaults to the JOB_SPEC environment variable.",
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			ctx := logr.NewContext(cmd.Context(), logger.NewWithVerbosity(logVerbosity))
			cmd.SetContext(ctx)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			validated, err := opts.Validate()
			if err != nil {
				return err
			}
			completed, err := validated.Complete(cmd.Context())
			if err != nil {
				return err
			}
			return completed.Run(cmd.Context())
		},
	}

	cmd.PersistentFlags().IntVarP(&logVerbosity, "verbosity", "v", 0, "set the verbosity level")
	if err := BindOptions(opts, cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}
