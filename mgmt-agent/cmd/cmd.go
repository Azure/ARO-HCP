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

package cmd

import (
	"context"

	"github.com/spf13/cobra"
)

func NewRootCmd() (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:   "mgmt-agent",
		Short: "mgmt-agent is a controller for managing cluster-scoped concerns on ARO HCP management clusters.",
		Long: `mgmt-agent runs on the management cluster to configure cluster-scoped resources
to suit the needs of running ARO-HCP. It provides a place to put logic to bridge
the gap between "brand new AKS cluster" and "ready to run ARO-HCP customer workloads".

mgmt-agent runs two controllers under a single leader election:

1. SWIFT NIC controller: watches Node objects on the management cluster, queries
   the Azure Compute API for each node's VM network configuration, and sets an
   extended resource (aro.openshift.io/swift-nic) on node status via Server-Side
   Apply. This allows the Kubernetes scheduler to allocate SWIFT NICs to pods.

2. KSM HCP controller (enabled via --ksm-image): watches HostedControlPlane CRs
   and deploys a kube-state-metrics instance per HCP to scrape worker node health
   metrics (kube_node_status_condition, kube_node_info) from each HCP's API server.
   Metrics are forwarded to the HCP Azure Managed Prometheus workspace.`,
	}
	subcommands := []func() (*cobra.Command, error){
		NewControllerCommand,
	}

	for _, newCmd := range subcommands {
		subCmd, err := newCmd()
		if err != nil {
			return nil, err
		}
		cmd.AddCommand(subCmd)
	}
	return cmd, nil
}

func NewControllerCommand() (*cobra.Command, error) {
	opts := DefaultControllerOptions()
	cmd := &cobra.Command{
		Use:   "controller",
		Short: "Start the mgmt-agent controller",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runController(cmd.Context(), opts)
		},
	}
	if err := opts.BindFlags(cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}

func runController(ctx context.Context, opts *RawControllerOptions) error {
	validated, err := opts.Validate(ctx)
	if err != nil {
		return err
	}
	completed, err := validated.Complete(ctx)
	if err != nil {
		return err
	}
	return completed.Run(ctx)
}
