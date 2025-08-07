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

package validate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func NewCommand() (*cobra.Command, error) {
	opts := DefaultValidationOptions()
	cmd := &cobra.Command{
		Use:           "validate",
		Short:         "Validate configurations and pipeline configuration references.",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := runValidate(cmd.Context(), opts); err != nil {
				lines := strings.Split(err.Error(), "\n")
				if len(lines) == 1 {
					return err
				}
				for _, line := range lines {
					fmt.Println(strings.TrimSpace(line))
				}
				return errors.New(lines[0])
			}
			return nil
		},
	}
	if err := BindValidationOptions(opts, cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}

func runValidate(ctx context.Context, opts *RawValidationOptions) error {
	validated, err := opts.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete()
	if err != nil {
		return err
	}
	return completed.ValidatePipelineConfigReferences(ctx)
}
