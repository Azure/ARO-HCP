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
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"golang.org/x/sync/errgroup"

	"k8s.io/apimachinery/pkg/util/rand"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventhub/armeventhub"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armfeatures"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
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
	const pollTimeout = 10 * time.Minute

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

	pollCtx, pollCancel := context.WithTimeout(ctx, pollTimeout)
	defer pollCancel()
	storageAccount, err := storagePoller.PollUntilDone(pollCtx, &runtime.PollUntilDoneOptions{
		Frequency: 10 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create storage account %s in resource group %s (timeout '%f' minutes): %w", storageAccountName, resourceGroupName, pollTimeout.Minutes(), err)
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
		hubName      = "shoebox-eh"
		authRuleName = "shoebox-eh-auth"
		pollTimeout  = 10 * time.Minute
	)

	// Event Hubs namespace names are globally unique, including across parallel test runs.
	namespaceName := "shoebox-eh-ns-" + rand.String(5)

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

	pollCtx, pollCancel := context.WithTimeout(ctx, pollTimeout)
	defer pollCancel()
	_, err = nsPoller.PollUntilDone(pollCtx, &runtime.PollUntilDoneOptions{
		Frequency: 10 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Event Hub namespace %s in resource group %s (timeout '%f' minutes): %w", namespaceName, resourceGroupName, pollTimeout.Minutes(), err)
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

const (
	// shoeboxAFECNamespace and shoeboxAFECFeature identify the AFEC feature
	// registration that admits test telemetry into the production Azure Monitor
	// pipeline. Without it, diagnostic settings are accepted but no shoebox
	// logs are ever delivered (see AROSLSRE-2244 / AROSLSRE-2245).
	shoeboxAFECNamespace = "Microsoft.Insights"
	shoeboxAFECFeature   = "testinproduction"

	// mgmtSubscriptionNameSegment is the display name segment identifying
	// management subscriptions. Prod names follow
	// "Azure Red Hat OpenShift HCP - <Region> - MGMT - <stamp>"; Stage names carry
	// the environment as an extra segment so they are distinguishable from the
	// identically-named prod ones (see config/config.msft.clouds-overlay.yaml):
	// "Azure Red Hat OpenShift HCP - stg - <Region> - MGMT - <stamp>".
	mgmtSubscriptionNameSegment = "mgmt"
	stageEnvSubscriptionSegment = "stg"
	deployEnvVarName            = "ARO_HCP_DEPLOY_ENV"
)

// verifyShoeboxLogDeliveryPrerequisites fast-fails when any management
// subscription in the test region is confirmed to be missing the AFEC feature
// registration required for shoebox log delivery. Without this check, a
// missing prerequisite only surfaces after the full 45-minute verification
// window has timed out waiting for logs that can never arrive.
//
// Contract: the test fails only on a definitively unregistered AFEC state
// (including a 404 on the registration query). When the prerequisite cannot
// be confirmed, the check logs a warning and passes, and the delivery
// verification remains the authoritative signal. Inability to confirm arises
// when the test identity cannot see the region's management subscriptions
// (they live in the service tenant, while the CI identity may be a Red Hat
// tenant service principal depending on the leased customer subscription -
// see docs/ci/execution.md), or when the registration query itself errors.
// The check activates as designed whenever the identity does have
// visibility, e.g. when an AME-tenant subscription is leased.
func verifyShoeboxLogDeliveryPrerequisites(ctx context.Context, creds azcore.TokenCredential, location string) error {
	clientOptions := &azcorearm.ClientOptions{
		ClientOptions: azsdk.NewClientOptions(azsdk.ComponentE2E),
	}

	subscriptionsFactory, err := armsubscriptions.NewClientFactory(creds, clientOptions)
	if err != nil {
		return fmt.Errorf("failed to create subscriptions client factory: %w", err)
	}

	deployEnv := os.Getenv(deployEnvVarName)

	var mgmtSubscriptions []*armsubscriptions.Subscription
	pager := subscriptionsFactory.NewClient().NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list subscriptions during AFEC preflight: %w", err)
		}
		for _, sub := range page.Value {
			if isManagementSubscriptionForLocation(sub, location, deployEnv) {
				mgmtSubscriptions = append(mgmtSubscriptions, sub)
			}
		}
	}

	if len(mgmtSubscriptions) == 0 {
		GinkgoLogr.Info("no management subscriptions visible for location; skipping AFEC registration preflight",
			"location", location,
			deployEnvVarName, deployEnv,
		)
		return nil
	}

	var unregistered []string
	for _, sub := range mgmtSubscriptions {
		featuresClient, err := armfeatures.NewSubscriptionFeatureRegistrationsClient(*sub.SubscriptionID, creds, clientOptions)
		if err != nil {
			return fmt.Errorf("failed to create feature registrations client for subscription %s: %w", *sub.SubscriptionID, err)
		}

		resp, err := featuresClient.Get(ctx, shoeboxAFECNamespace, shoeboxAFECFeature, nil)
		if err != nil {
			var respErr *azcore.ResponseError
			if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
				unregistered = append(unregistered, fmt.Sprintf("%s (%s, state: NotRegistered)", *sub.DisplayName, *sub.SubscriptionID))
				continue
			}
			// Unable to confirm (transient ARM failure, or the identity can list
			// the subscription but lacks feature-registration read access): warn
			// and continue rather than fail - only a definitively unregistered
			// state fails the test. Delivery verification remains the backstop.
			GinkgoLogr.Error(err, "unable to query AFEC registration state; skipping subscription",
				"subscription", *sub.DisplayName,
				"subscriptionID", *sub.SubscriptionID,
			)
			continue
		}

		state := "Unknown"
		if resp.Properties != nil && resp.Properties.State != nil {
			state = string(*resp.Properties.State)
		}
		if state != string(armfeatures.SubscriptionFeatureRegistrationStateRegistered) {
			unregistered = append(unregistered, fmt.Sprintf("%s (%s, state: %s)", *sub.DisplayName, *sub.SubscriptionID, state))
		}
	}

	if len(unregistered) > 0 {
		return fmt.Errorf(
			"shoebox log delivery prerequisite missing: %s/%s AFEC feature is not Registered on management subscription(s) in %s: [%s]; "+
				"diagnostic settings will be accepted but no logs will be delivered - register the feature on the affected management subscription(s) "+
				"(see AROSLSRE-2244 / AROSLSRE-2245) before running this test",
			shoeboxAFECNamespace, shoeboxAFECFeature, location, strings.Join(unregistered, "; "))
	}

	GinkgoLogr.Info("shoebox AFEC prerequisite verified",
		"location", location,
		"managementSubscriptions", len(mgmtSubscriptions),
	)
	return nil
}

// isManagementSubscriptionForLocation reports whether the subscription display
// name matches the management subscription pattern for the given Azure
// location and deployment environment. Prod names are
// "Azure Red Hat OpenShift HCP - <Region> - MGMT - <stamp>"; Stage names
// additionally carry the environment segment
// ("Azure Red Hat OpenShift HCP - stg - <Region> - MGMT - <stamp>") so they are
// distinguishable from identically-named prod ones. Segments are compared
// case-insensitively with whitespace removed so that e.g. "East US 2" matches
// location "eastus2" but not "eastus".
func isManagementSubscriptionForLocation(sub *armsubscriptions.Subscription, location, deployEnv string) bool {
	if sub.DisplayName == nil || sub.SubscriptionID == nil {
		return false
	}

	expectStage := strings.EqualFold(deployEnv, "stg")
	locationMatch := false
	mgmtMatch := false
	stageSegmentMatch := false
	for _, segment := range strings.Split(*sub.DisplayName, " - ") {
		normalized := normalizeLocationSegment(segment)
		switch normalized {
		case normalizeLocationSegment(location):
			locationMatch = true
		case mgmtSubscriptionNameSegment:
			mgmtMatch = true
		case stageEnvSubscriptionSegment:
			stageSegmentMatch = true
		}
	}
	if !locationMatch || !mgmtMatch {
		return false
	}
	// A prod run must not match Stage subscriptions and vice versa: the stage
	// environment segment must be present exactly when running against stage.
	return expectStage == stageSegmentMatch
}

func normalizeLocationSegment(segment string) string {
	return strings.Join(strings.Fields(strings.ToLower(segment)), "")
}

var _ = Describe("Customer", func() {
	It("should be able to forward control plane logs to a storage account and Event Hub via shoebox diagnostic settings",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.StageAndProdOnly,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerNetworkSecurityGroupName = "customer-nsg-name"
				customerVnetName                 = "customer-vnet-name"
				customerVnetSubnetName           = "customer-vnet-subnet1"
				customerClusterName              = "shoebox-hcp-cluster"
			)

			tc := framework.NewTestContext()

			By("verifying shoebox log delivery prerequisites (AFEC feature registration)")
			if !framework.IsDevelopmentEnvironment() {
				creds, err := tc.AzureCredential()
				Expect(err).NotTo(HaveOccurred(), "failed to get Azure credential for AFEC preflight")
				err = verifyShoeboxLogDeliveryPrerequisites(ctx, creds, tc.Location())
				Expect(err).NotTo(HaveOccurred(), "shoebox log delivery prerequisites not met")
			}

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "shoebox-cluster", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for shoebox test")

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
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
			Expect(err).NotTo(HaveOccurred(), "failed to create cluster customer resources")

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam20240610(ctx, GinkgoLogr, *resourceGroup.Name, clusterParams, framework.ClusterCreationTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %s", customerClusterName)

			subscriptionID, err := tc.SubscriptionID(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to get subscription ID")

			creds, err := tc.AzureCredential()
			Expect(err).NotTo(HaveOccurred(), "failed to get Azure credential")

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
			err = g.Wait()
			Expect(err).NotTo(HaveOccurred(), "failed to create shoebox storage account and Event Hub destination resources")
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
			Expect(err).NotTo(HaveOccurred(), "failed to create diagnostics client")

			_, err = diagnosticsClient.CreateOrUpdate(ctx, resourceID, "shoebox-storage-diag", armmonitor.DiagnosticSettingsResource{
				Properties: &armmonitor.DiagnosticSettings{
					StorageAccountID: to.Ptr(storage.AccountID),
					Logs:             logSettings,
				},
			}, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create storage account diagnostic setting for cluster %s", resourceID)
			GinkgoLogr.Info("storage account diagnostic setting created")

			_, err = diagnosticsClient.CreateOrUpdate(ctx, resourceID, "shoebox-eh-diag", armmonitor.DiagnosticSettingsResource{
				Properties: &armmonitor.DiagnosticSettings{
					EventHubAuthorizationRuleID: to.Ptr(eventHub.AuthorizationRuleID),
					EventHubName:                to.Ptr(eventHub.EventHubName),
					Logs:                        logSettings,
				},
			}, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create Event Hub diagnostic setting for cluster %s", resourceID)
			GinkgoLogr.Info("Event Hub diagnostic setting created")

			By("waiting for shoebox logs to appear in storage account and Event Hub")
			blobContainersClient, err := armstorage.NewBlobContainersClient(subscriptionID, creds, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create blob containers client")

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
			err = g.Wait()
			Expect(err).NotTo(HaveOccurred(), "failed to verify shoebox logs reached storage account %s and Event Hub %s for cluster %s", storage.AccountName, eventHub.EventHubName, resourceID)
		})
})
