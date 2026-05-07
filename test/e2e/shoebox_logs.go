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

	"golang.org/x/sync/errgroup"

	"k8s.io/apimachinery/pkg/util/rand"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventhub/armeventhub"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"

	"github.com/Azure/ARO-HCP/internal/azsdk"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var shoeboxLogCategories = []string{
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

type shoeboxLogVerifier interface {
	Verify(context.Context) error
}

type storageAccountResult struct {
	AccountID   string
	AccountName string
}

func shoeboxDiagnosticLogSettings() []*armmonitor.LogSettings {
	logSettings := make([]*armmonitor.LogSettings, 0, len(shoeboxLogCategories))
	for _, category := range shoeboxLogCategories {
		logSettings = append(logSettings, &armmonitor.LogSettings{
			Category: to.Ptr(category),
			Enabled:  to.Ptr(true),
		})
	}
	return logSettings
}

func clusterResourceID(subscriptionID, resourceGroupName, clusterName string) string {
	return fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/%s",
		subscriptionID, resourceGroupName, clusterName,
	)
}

// pollVerifier polls a verifier at 60-second intervals until it succeeds or the timeout is reached.
func pollVerifier(ctx context.Context, name string, verifier shoeboxLogVerifier, timeout time.Duration) bool {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	timer := time.After(timeout)
	for {
		select {
		case <-timer:
			GinkgoLogr.Error(fmt.Errorf("timed out"), name+" verification timed out")
			return false
		case <-ctx.Done():
			GinkgoLogr.Error(ctx.Err(), "context cancelled while waiting for "+name)
			return false
		case <-ticker.C:
			if err := verifier.Verify(ctx); err != nil {
				if !verifiers.IsRetryable(err) {
					GinkgoLogr.Error(err, "error while verifying "+name)
				}
				continue
			}
			GinkgoLogr.Info(name+" verified successfully", "timestamp", time.Now().UTC().Format(time.RFC3339))
			return true
		}
	}
}

func createStorageAccount(ctx context.Context, subscriptionID string, creds azcore.TokenCredential, resourceGroupName, location string) (*storageAccountResult, error) {
	storageAccountName := "shoebox" + rand.String(6)

	storageClient, err := armstorage.NewAccountsClient(subscriptionID, creds, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage accounts client: %w", err)
	}

	storagePoller, err := storageClient.BeginCreate(ctx, resourceGroupName, storageAccountName, armstorage.AccountCreateParameters{
		Kind:     to.Ptr(armstorage.KindStorageV2),
		Location: to.Ptr(location),
		SKU: &armstorage.SKU{
			Name: to.Ptr(armstorage.SKUNameStandardLRS),
		},
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin storage account creation: %w", err)
	}

	storageAccount, err := storagePoller.PollUntilDone(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage account: %w", err)
	}

	return &storageAccountResult{
		AccountID:   *storageAccount.ID,
		AccountName: storageAccountName,
	}, nil
}

type eventHubResult struct {
	AuthorizationRuleID string
	ConnectionString    string
	EventHubName        string
}

func createEventHub(ctx context.Context, subscriptionID string, creds azcore.TokenCredential, resourceGroupName, location string) (*eventHubResult, error) {
	const (
		namespaceName = "shoebox-eh-ns"
		hubName       = "shoebox-eh"
		authRuleName  = "shoebox-eh-auth"
	)

	nsClient, err := armeventhub.NewNamespacesClient(subscriptionID, creds, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Event Hub namespaces client: %w", err)
	}

	nsPoller, err := nsClient.BeginCreateOrUpdate(ctx, resourceGroupName, namespaceName, armeventhub.EHNamespace{
		Location: to.Ptr(location),
		SKU: &armeventhub.SKU{
			Name: to.Ptr(armeventhub.SKUNameStandard),
			Tier: to.Ptr(armeventhub.SKUTierStandard),
		},
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin Event Hub namespace creation: %w", err)
	}

	_, err = nsPoller.PollUntilDone(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Event Hub namespace: %w", err)
	}

	hubClient, err := armeventhub.NewEventHubsClient(subscriptionID, creds, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Event Hubs client: %w", err)
	}

	_, err = hubClient.CreateOrUpdate(ctx, resourceGroupName, namespaceName, hubName, armeventhub.Eventhub{}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Event Hub: %w", err)
	}

	authRuleResp, err := nsClient.CreateOrUpdateAuthorizationRule(ctx, resourceGroupName, namespaceName, authRuleName, armeventhub.AuthorizationRule{
		Properties: &armeventhub.AuthorizationRuleProperties{
			Rights: []*armeventhub.AccessRights{
				to.Ptr(armeventhub.AccessRightsSend),
				to.Ptr(armeventhub.AccessRightsListen),
			},
		},
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Event Hub authorization rule: %w", err)
	}
	if authRuleResp.ID == nil {
		return nil, fmt.Errorf("authorization rule ID was nil")
	}

	keysResp, err := nsClient.ListKeys(ctx, resourceGroupName, namespaceName, authRuleName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list Event Hub namespace keys: %w", err)
	}
	if keysResp.PrimaryConnectionString == nil {
		return nil, fmt.Errorf("primary connection string was nil")
	}

	return &eventHubResult{
		AuthorizationRuleID: *authRuleResp.ID,
		ConnectionString:    *keysResp.PrimaryConnectionString,
		EventHubName:        hubName,
	}, nil
}

var _ = Describe("Customer", func() {
	It("should be able to forward control plane logs to a storage account and Event Hub via shoebox diagnostic settings",
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
			)

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
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)

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
			err = tc.CreateHCPClusterFromParam(ctx, GinkgoLogr, *resourceGroup.Name, clusterParams, 45*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			subscriptionID, err := tc.SubscriptionID(ctx)
			Expect(err).NotTo(HaveOccurred())

			creds, err := tc.AzureCredential()
			Expect(err).NotTo(HaveOccurred())

			// TODO: convert shoebox-specific steps below to hard-fail assertions once validated in all stage/prod regions.

			By("creating storage account and Event Hub namespace in parallel")
			var (
				storage  *storageAccountResult
				eventHub *eventHubResult
			)

			g, gCtx := errgroup.WithContext(ctx)
			g.Go(func() error {
				var err error
				storage, err = createStorageAccount(gCtx, subscriptionID, creds, *resourceGroup.Name, tc.Location())
				return err
			})
			g.Go(func() error {
				var err error
				eventHub, err = createEventHub(gCtx, subscriptionID, creds, *resourceGroup.Name, tc.Location())
				return err
			})
			if err := g.Wait(); err != nil {
				GinkgoLogr.Error(err, "failed to create shoebox destination resources")
				return
			}
			GinkgoLogr.Info("shoebox destination resources created",
				"storageAccount", storage.AccountName,
				"eventHub", eventHub.EventHubName,
			)

			By("enabling diagnostic settings on the HCP cluster")
			resourceID := clusterResourceID(subscriptionID, *resourceGroup.Name, customerClusterName)
			logSettings := shoeboxDiagnosticLogSettings()

			diagnosticsClient, err := armmonitor.NewDiagnosticSettingsClient(creds, &azcorearm.ClientOptions{
				ClientOptions: azsdk.NewClientOptions(azsdk.ComponentE2E),
			})
			if err != nil {
				GinkgoLogr.Error(err, "failed to create diagnostics client")
				return
			}

			_, err = diagnosticsClient.CreateOrUpdate(ctx, resourceID, "shoebox-storage-diag", armmonitor.DiagnosticSettingsResource{
				Properties: &armmonitor.DiagnosticSettings{
					StorageAccountID: to.Ptr(storage.AccountID),
					Logs:             logSettings,
				},
			}, nil)
			if err != nil {
				GinkgoLogr.Error(err, "failed to create storage account diagnostic setting")
				return
			}
			GinkgoLogr.Info("storage account diagnostic setting created")

			_, err = diagnosticsClient.CreateOrUpdate(ctx, resourceID, "shoebox-eh-diag", armmonitor.DiagnosticSettingsResource{
				Properties: &armmonitor.DiagnosticSettings{
					EventHubAuthorizationRuleID: to.Ptr(eventHub.AuthorizationRuleID),
					EventHubName:                to.Ptr(eventHub.EventHubName),
					Logs:                        logSettings,
				},
			}, nil)
			if err != nil {
				GinkgoLogr.Error(err, "failed to create Event Hub diagnostic setting")
				return
			}
			GinkgoLogr.Info("Event Hub diagnostic setting created")

			By("waiting for shoebox logs to appear in storage account and Event Hub")
			blobContainersClient, err := armstorage.NewBlobContainersClient(subscriptionID, creds, nil)
			if err != nil {
				GinkgoLogr.Error(err, "failed to create blob containers client")
				return
			}

			storageVerifier := verifiers.VerifyShoeboxLogs(blobContainersClient, *resourceGroup.Name, storage.AccountName)
			eventHubVerifier := verifiers.VerifyShoeboxEventHub(eventHub.ConnectionString, eventHub.EventHubName)

			g, gCtx = errgroup.WithContext(ctx)
			g.Go(func() error {
				if !pollVerifier(gCtx, "shoebox storage account logs", storageVerifier, 45*time.Minute) {
					return fmt.Errorf("storage account log verification failed")
				}
				return nil
			})
			g.Go(func() error {
				if !pollVerifier(gCtx, "shoebox Event Hub logs", eventHubVerifier, 45*time.Minute) {
					return fmt.Errorf("event hub log verification failed")
				}
				return nil
			})
			if err := g.Wait(); err != nil {
				GinkgoLogr.Error(err, "shoebox log verification failed")
				return
			}
		})
})
