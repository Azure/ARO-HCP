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

package inspect

import (
	"context"

	"github.com/spf13/cobra"
)

func NewCommand() (*cobra.Command, error) {
	opts := DefaultOptions()
	cmd := &cobra.Command{
		Use:   "inspect",
		Short: "inspect scopes of a pipeline.yaml file",
		Long:  "inspect scopes of a pipeline.yaml file",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInspect(cmd.Context(), opts)
		},
	}
	if err := BindOptions(opts, cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}

func runInspect(ctx context.Context, opts *RawInspectOptions) error {
	validated, err := opts.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete()
	if err != nil {
		return err
	}
	return completed.RunInspect(ctx)
}
