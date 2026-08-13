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

package deploy

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"

	extcmd "github.com/openshift-eng/openshift-tests-extension/pkg/cmd"
	e "github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

type clusterRef struct {
	resourceGroupName string
	clusterName       string
}

// tagResourceGroupForPersist adds a "persist: true" tag to the named resource
// group, merging it with any tags already present.
func tagResourceGroupForPersist(ctx context.Context, rgClient *armresources.ResourceGroupsClient, rgName string) error {
	existing, err := rgClient.Get(ctx, rgName, nil)
	if err != nil {
		return fmt.Errorf("getting resource group %q: %w", rgName, err)
	}

	tags := existing.Tags
	if tags == nil {
		tags = make(map[string]*string)
	}
	tags["persist"] = to.Ptr("true")

	_, err = rgClient.Update(ctx, rgName, armresources.ResourceGroupPatchable{Tags: tags}, nil)
	if err != nil {
		return fmt.Errorf("tagging resource group %q: %w", rgName, err)
	}
	return nil
}

func NewCommand(registry *e.Registry, specs et.ExtensionTestSpecs) *cobra.Command {
	var kubeconfigDirPath string

	cmd := &cobra.Command{
		Use:          "deploy TEST_NAME",
		Short:        "Deploy ARO HCP cluster by running given E2E test without teardown",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer cancel()

			testName := args[0]
			if !slices.Contains(specs.Names(), testName) {
				return fmt.Errorf("test case %q not found", testName)
			}

			if err := os.Setenv("ARO_E2E_SKIP_CLEANUP", "true"); err != nil {
				return fmt.Errorf("setting ARO_E2E_SKIP_CLEANUP: %w", err)
			}

			// define hook function to collect resource groups created during
			// E2E test run, guarded by a lock
			var mu sync.Mutex
			var resourceGroups []string
			framework.OnResourceGroupCreated = func(name string) {
				mu.Lock()
				defer mu.Unlock()
				resourceGroups = append(resourceGroups, name)
			}
			// another hook function to collect hosted cluster names created
			// during the E2E test run
			var clusters []clusterRef
			framework.OnHCPClusterCreated = func(resourceGroupName, clusterName string) {
				mu.Lock()
				defer mu.Unlock()
				clusters = append(clusters, clusterRef{resourceGroupName, clusterName})
			}

			runner := &cobra.Command{SilenceUsage: true, SilenceErrors: true}
			for _, c := range extcmd.DefaultExtensionCommands(registry) {
				runner.AddCommand(c)
			}
			runner.SetArgs([]string{"run-test", testName})
			if err := runner.ExecuteContext(ctx); err != nil {
				return err
			}

			// if no resource groups were created during E2E test, we have
			// no resource groups nor hosted clusters process
			if len(resourceGroups) == 0 {
				return nil
			}

			postDeployCtx, postDeployCancel := context.WithTimeout(ctx, 10*time.Minute)
			defer postDeployCancel()

			// get the same azure credentials as the test runner will get
			creds, err := framework.GetAzureCredentials()
			if err != nil {
				return fmt.Errorf("obtaining Azure credentials: %w", err)
			}

			subClient, err := armsubscriptions.NewClient(creds, nil)
			if err != nil {
				return fmt.Errorf("creating subscriptions client: %w", err)
			}

			subscriptionID, err := framework.GetSubscriptionID(postDeployCtx, subClient, os.Getenv("CUSTOMER_SUBSCRIPTION"))
			if err != nil {
				return fmt.Errorf("resolving subscription ID: %w", err)
			}

			rgClient, err := armresources.NewResourceGroupsClient(subscriptionID, creds, nil)
			if err != nil {
				return fmt.Errorf("creating resource groups client: %w", err)
			}

			for _, rg := range resourceGroups {
				if err := tagResourceGroupForPersist(postDeployCtx, rgClient, rg); err != nil {
					fmt.Fprintf(os.Stderr, "deploy: failed to tag resource group %q: %v\n", rg, err)
				} else {
					fmt.Fprintf(os.Stderr, "deploy: tagged resource group %q with persist=true\n", rg)
				}
			}

			// end here if we don't need to fetch the kubeconfig
			if kubeconfigDirPath != "" {
				return nil
			}

			// make sure kubeconfig target dir exists
			if err := os.MkdirAll(kubeconfigDirPath, 0755); err != nil {
				return fmt.Errorf("creating kubeconfig directory %q: %w", kubeconfigDirPath, err)
			}

			for _, cluster := range clusters {
				kubeconfig, err := framework.FetchAdminKubeconfig(postDeployCtx, subscriptionID, cluster.resourceGroupName, cluster.clusterName, framework.GetAdminRESTConfigTimeout)
				if err != nil {
					fmt.Fprintf(os.Stderr, "deploy: failed to fetch kubeconfig for cluster %q: %v\n", cluster.clusterName, err)
					continue
				}
				kubeconfigFile := filepath.Join(kubeconfigDirPath, cluster.clusterName+".kubeconfig")
				if err := os.WriteFile(kubeconfigFile, []byte(kubeconfig), 0600); err != nil {
					fmt.Fprintf(os.Stderr, "deploy: failed to write kubeconfig for cluster %q: %v\n", cluster.clusterName, err)
				} else {
					fmt.Fprintf(os.Stderr, "deploy: wrote kubeconfig for cluster %q to %s\n", cluster.clusterName, kubeconfigFile)
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&kubeconfigDirPath, "kubeconfig-dir", "", "directory to write admin kubeconfigs into (one <cluster-name>.kubeconfig file per cluster)")
	return cmd
}
