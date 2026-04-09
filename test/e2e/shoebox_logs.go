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

package e2e

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/util/rand"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	It("should be able to forward control plane logs to a storage account via shoebox diagnostic settings",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.StageAndProdOnly,
		func(ctx context.Context) {
			const (
				customerNetworkSecurityGroupName = "customer-nsg-name"
				customerVnetName                 = "customer-vnet-name"
				customerVnetSubnetName           = "customer-vnet-subnet1"
				customerClusterName              = "shoebox-hcp-cluster"
				diagnosticSettingName            = "shoebox-diag-setting"
			)

			logCategories := []string{
				"kube-apiserver",
				"kube-audit",
				"kube-audit-admin",
				"kube-controller-manager",
				"kube-scheduler",
				"cloud-controller-manager",
				"csi-azuredisk-controller",
				"csi-azurefile-controller",
				"csi-snapshot-controller",
			}

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "shoebox-cluster", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{
					"customerNsgName":        customerNetworkSecurityGroupName,
					"customerVnetName":       customerVnetName,
					"customerVnetSubnetName": customerVnetSubnetName,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("sleeping for 1 hour to allow manual testing")
			GinkgoLogr.Info("cluster created, sleeping for 1 hour for manual testing")
			time.Sleep(1 * time.Hour)

			subscriptionID, err := tc.SubscriptionID(ctx)
			Expect(err).NotTo(HaveOccurred())

			creds, err := tc.AzureCredential()
			Expect(err).NotTo(HaveOccurred())

			// TODO: convert shoebox-specific steps below to hard-fail assertions once validated in all stage/prod regions.

			By("creating a storage account for shoebox logs")
			storageAccountName := "shoebox" + rand.String(6)

			storageClient, err := armstorage.NewAccountsClient(subscriptionID, creds, nil)
			if err != nil {
				GinkgoLogr.Error(err, "failed to create storage accounts client")
				return
			}

			storagePoller, err := storageClient.BeginCreate(ctx, *resourceGroup.Name, storageAccountName, armstorage.AccountCreateParameters{
				Kind:     to.Ptr(armstorage.KindStorageV2),
				Location: to.Ptr(tc.Location()),
				SKU: &armstorage.SKU{
					Name: to.Ptr(armstorage.SKUNameStandardLRS),
				},
			}, nil)
			if err != nil {
				GinkgoLogr.Error(err, "failed to begin storage account creation")
				return
			}

			storageAccount, err := storagePoller.PollUntilDone(ctx, nil)
			if err != nil {
				GinkgoLogr.Error(err, "failed to create storage account")
				return
			}
			GinkgoLogr.Info("storage account created", "name", storageAccountName, "id", *storageAccount.ID)

			By("enabling diagnostic settings on the HCP cluster")
			clusterResourceID := fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/%s",
				subscriptionID, *resourceGroup.Name, customerClusterName,
			)

			logSettings := make([]*armmonitor.LogSettings, 0, len(logCategories))
			for _, category := range logCategories {
				logSettings = append(logSettings, &armmonitor.LogSettings{
					Category: to.Ptr(category),
					Enabled:  to.Ptr(true),
				})
			}

			diagnosticsClient, err := armmonitor.NewDiagnosticSettingsClient(creds, &azcorearm.ClientOptions{})
			if err != nil {
				GinkgoLogr.Error(err, "failed to create diagnostics client")
				return
			}

			_, err = diagnosticsClient.CreateOrUpdate(ctx, clusterResourceID, diagnosticSettingName, armmonitor.DiagnosticSettingsResource{
				Properties: &armmonitor.DiagnosticSettings{
					StorageAccountID: storageAccount.ID,
					Logs:             logSettings,
				},
			}, nil)
			if err != nil {
				GinkgoLogr.Error(err, "failed to create diagnostic setting")
				return
			}
			GinkgoLogr.Info("diagnostic setting created", "name", diagnosticSettingName)

			By("waiting for log containers to appear in the storage account")
			blobContainersClient, err := armstorage.NewBlobContainersClient(subscriptionID, creds, nil)
			if err != nil {
				GinkgoLogr.Error(err, "failed to create blob containers client")
				return
			}

			shoeboxVerifier := verifiers.VerifyShoeboxLogs(blobContainersClient, *resourceGroup.Name, storageAccountName)

			// Logs take ~35-40 minutes to appear. Poll for up to 45 minutes.
			ticker := time.NewTicker(60 * time.Second)
			defer ticker.Stop()
			timeout := time.After(45 * time.Minute)
			for {
				select {
				case <-timeout:
					GinkgoLogr.Error(fmt.Errorf("timed out"), "shoebox log containers did not appear", "storageAccount", storageAccountName)
					return
				case <-ctx.Done():
					GinkgoLogr.Error(ctx.Err(), "context cancelled while waiting for shoebox logs")
					return
				case <-ticker.C:
					if err := shoeboxVerifier.Verify(ctx); err != nil {
						if !verifiers.IsRetryable(err) {
							GinkgoLogr.Error(err, "error while verifying shoebox logs")
						}
						continue
					}
					GinkgoLogr.Info("shoebox log containers found in storage account", "storageAccount", storageAccountName, "timestamp", time.Now().UTC().Format(time.RFC3339))
					return
				}
			}
		})
})
