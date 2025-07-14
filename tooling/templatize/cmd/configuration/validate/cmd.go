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
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/rand"
)

func NewCommand(centralRemoteUrl string) (*cobra.Command, error) {
	scratchDir := filepath.Join(os.TempDir(), "config-"+rand.String(8))
	if err := os.MkdirAll(scratchDir, os.ModePerm); err != nil {
		return nil, fmt.Errorf("failed to create scratch directory: %w", err)
	}

	opts := DefaultOptions(scratchDir, centralRemoteUrl)
	cmd := &cobra.Command{
		Use:           "validate",
		Short:         "Validate rendered configurations for all clouds, environments, and regions.",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			logger, err := logr.FromContext(cmd.Context())
			if err != nil {
				return err
			}
			defer func() {
				if err := os.RemoveAll(scratchDir); err != nil {
					logger.Error(err, "Failed to remove scratch directory")
				}
			}()

			return runValidate(cmd.Context(), opts)
		},
	}
	if err := BindOptions(opts, cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}

func runValidate(ctx context.Context, opts *RawOptions) error {
	validated, err := opts.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete()
	if err != nil {
		return err
	}
	return completed.ValidateServiceConfig(ctx)
}
