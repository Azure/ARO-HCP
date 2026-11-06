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

package getkubeconfig

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

func NewCommand() *cobra.Command {
	var kubeconfigPath string

	cmd := &cobra.Command{
		Use:          "get-kubeconfig RESOURCE_GROUP CLUSTER_NAME",
		Short:        "Fetch the admin kubeconfig for a deployed ARO HCP cluster",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			resourceGroupName := args[0]
			clusterName := args[1]

			creds, err := framework.GetAzureCredentials()
			if err != nil {
				return fmt.Errorf("obtaining Azure credentials: %w", err)
			}

			subClient, err := armsubscriptions.NewClient(creds, nil)
			if err != nil {
				return fmt.Errorf("creating subscriptions client: %w", err)
			}

			subscriptionID, err := framework.GetSubscriptionID(ctx, subClient, os.Getenv("CUSTOMER_SUBSCRIPTION"))
			if err != nil {
				return fmt.Errorf("resolving subscription ID: %w", err)
			}

			kubeconfig, err := framework.FetchAdminKubeconfig(ctx, subscriptionID, resourceGroupName, clusterName, framework.GetAdminRESTConfigTimeout)
			if err != nil {
				return fmt.Errorf("fetching kubeconfig for cluster %q: %w", clusterName, err)
			}

			if kubeconfigPath != "" {
				if err := os.WriteFile(kubeconfigPath, []byte(kubeconfig), 0600); err != nil {
					return fmt.Errorf("writing kubeconfig for cluster %q: %w", clusterName, err)
				}
				fmt.Fprintf(os.Stderr, "wrote kubeconfig for cluster %q to %s\n", clusterName, kubeconfigPath)
				return nil
			}

			fmt.Print(kubeconfig)
			return nil
		},
	}

	cmd.Flags().StringVar(&kubeconfigPath, "kubeconfig-path", "", "file path to write the kubeconfig into; if omitted the kubeconfig is printed to stdout")
	return cmd
}
