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

package generate

import (
	"context"

	"github.com/spf13/cobra"
)

func NewCommand() (*cobra.Command, error) {
	opts := DefaultGenerationOptions()
	cmd := &cobra.Command{
		Use:           "generate",
		Short:         "Apply service configuration to a template file.",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return generate(cmd.Context(), opts)
		},
	}
	if err := BindGenerationOptions(opts, cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}

func generate(ctx context.Context, opts *RawGenerationOptions) error {
	validated, err := opts.Validate(ctx)
	if err != nil {
		return err
	}
	completed, err := validated.Complete()
	if err != nil {
		return err
	}
	return completed.ExecuteTemplate()
}
