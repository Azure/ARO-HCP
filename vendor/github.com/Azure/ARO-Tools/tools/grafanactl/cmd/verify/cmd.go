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

package verify

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/tools/grafanactl/internal/grafana"
)

const dashboardsGroupID = "dashboards"

func NewVerifyCommand(group string) (*cobra.Command, error) {
	opts := DefaultVerifyDashboardsOptions()

	verifyCmd := &cobra.Command{
		Use:     "verify",
		Short:   "Verify Grafana resources",
		Long:    "Verify Grafana resources for correctness",
		GroupID: group,
	}

	verifyCmd.AddGroup(&cobra.Group{
		ID:    dashboardsGroupID,
		Title: "Verify Commands:",
	})

	verifyDashboardsCmd := &cobra.Command{
		Use:     "dashboards",
		Short:   "Verify Grafana dashboards",
		Long:    "Verify Grafana dashboards present in the configured paths pass validation",
		GroupID: dashboardsGroupID,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context())
		},
	}

	if err := BindVerifyDashboardsOptions(opts, verifyDashboardsCmd); err != nil {
		return nil, err
	}

	verifyCmd.AddCommand(verifyDashboardsCmd)

	return verifyCmd, nil
}

func (opts *RawVerifyDashboardsOptions) Run(ctx context.Context) error {
	validated, err := opts.Validate(ctx)
	if err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	completed, err := validated.Complete(ctx)
	if err != nil {
		return fmt.Errorf("completion failed: %w", err)
	}

	return completed.Run(ctx)
}

func (o *CompletedVerifyDashboardsOptions) Run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	logger.Info("Starting dashboard verification")

	configDir := filepath.Dir(o.ConfigFilePath)

	validationErrors, _, err := grafana.ValidateAllDashboards(ctx, o.Config, configDir)
	if err != nil {
		return fmt.Errorf("verification failed: %w", err)
	}

	if len(validationErrors) > 0 {
		return fmt.Errorf("verification found errors in %d dashboards", len(validationErrors))
	}

	logger.Info("Dashboard verification completed successfully")
	return nil
}
