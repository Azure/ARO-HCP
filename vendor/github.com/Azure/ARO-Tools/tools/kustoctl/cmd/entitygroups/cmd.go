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

package entitygroups

import (
	"github.com/spf13/cobra"
)

// NewEntityGroupsCommand returns the entity-groups parent command.
func NewEntityGroupsCommand() (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:   "entity-groups",
		Short: "Manage Kusto cross-cluster entity groups",
	}

	opts := DefaultSyncOptions()
	syncCmd := &cobra.Command{
		Use:   "sync",
		Short: "Discover Kusto clusters and sync entity groups on all of them",
		Long: `Discovers all Kusto clusters with the configured discovery tag (aroHCPPurpose by default) via Azure Resource Graph,
builds cross-cluster entity group KQL, and executes it on every discovered cluster. When --env-tag-value is set, discovery is
scoped to that environment so the KQL runs only on the matching clusters, giving each environment an isolated entity group.
This enables federated queries across all regional Kusto clusters.

Entity groups are specified as name:database pairs:
  --entity-group HCPServiceLogsEG:ServiceLogs
  --entity-group HCPHostedControlPlaneLogsEG:HostedControlPlaneLogs`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context())
		},
	}

	if err := BindSyncOptions(opts, syncCmd); err != nil {
		return nil, err
	}

	cmd.AddCommand(syncCmd)

	return cmd, nil
}
