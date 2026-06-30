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

package reconcile

import (
	"github.com/spf13/cobra"
)

func NewReconcileCommand(group string) (*cobra.Command, error) {
	reconcileCmd := &cobra.Command{
		Use:     "reconcile",
		Short:   "Reconcile Grafana resources",
		Long:    "Reconcile Azure Managed Grafana resources and their Azure Monitor Workspace integrations.",
		GroupID: group,
	}

	opts := DefaultGrafanaOptions()
	grafanaCmd := &cobra.Command{
		Use:   "grafana",
		Short: "Reconcile an Azure Managed Grafana instance",
		Long:  "Create or update an Azure Managed Grafana instance, discover all succeeded Azure Monitor Workspaces, and reconcile Azure Monitor Workspace integrations.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context())
		},
	}
	if err := BindGrafanaOptions(opts, grafanaCmd); err != nil {
		return nil, err
	}

	reconcileCmd.AddCommand(grafanaCmd)
	return reconcileCmd, nil
}
