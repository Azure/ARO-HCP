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
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/google/uuid"

	api "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/integration"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

var _ = Describe("HCPOpenShiftCluster Lifecycle", func() {
	var (
		clustersClient *api.HcpOpenShiftClustersClient
		customerEnv    *integration.CustomerEnv
	)

	BeforeEach(func() {
		By("Preparing HCP clusters client")
		clustersClient = clients.NewHcpOpenShiftClustersClient()

		By("Preparing customer environment values")
		customerEnv = &e2eSetup.CustomerEnv
	})

	It("Creates requried infrastructure resources then creates a cluster", labels.Critical, labels.Positive, labels.CreateCluster, func(ctx context.Context) {
		// Generate a unique name for the cluster for this specific test run to avoid collisions.
		clusterName := fmt.Sprintf("e2e-lifecycle-%s", uuid.NewString()[:8])

		// Declare variables that will be used in the defer function
		var resourceGroup string
		var testCtx *framework.TestContext
		var armResourcesClientFactory *armresources.ClientFactory
		var resourceGroupClient *armresources.ResourceGroupsClient

		// Define cleanup function upfront for better readability
		defer func() {
			By("Cleaning up the cluster resource")
			// Use a new context for cleanup to avoid cancellation if the test context timed out.
			cleanupCtx := context.Background()
			poller, err := clustersClient.BeginDelete(cleanupCtx, resourceGroup, clusterName, nil)

			// We don't want to fail the test here if cleanup fails, but we should log it.
			if err != nil {
				GinkgoLogr.Error(err, "failed to start cluster deletion during cleanup")
				return
			}

			_, err = poller.PollUntilDone(cleanupCtx, nil)
			if err != nil {
				GinkgoLogr.Error(err, "failed to poll for cluster deletion during cleanup")
			}
			By("Cleaning up the resource group")
			// Use the framework's test context for resource group cleanup since it doesn't need the systemData policy
			testCtx = framework.InvocationContext()
			armResourcesClientFactory = testCtx.GetARMResourcesClientFactoryOrDie(ctx)
			resourceGroupClient = armResourcesClientFactory.NewResourceGroupsClient()
			err = framework.DeleteResourceGroup(ctx, resourceGroupClient, resourceGroup, 1*time.Second, 60*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "failed to delete resource group during cleanup")
			By("Verifying the resource group is deleted")
			_, err = resourceGroupClient.Get(ctx, resourceGroup, nil)
			Expect(err).ToNot(BeNil())
			errMessage := fmt.Sprintf("The resource group '%s' could not be found.", resourceGroup)
			Expect(err.Error()).To(ContainSubstring(errMessage))
		}()

		By("Create a new resource group for the cluster")
		// Use the framework's test context for resource group operations since they don't need the systemData policy
		testCtx = framework.InvocationContext()
		armResourcesClientFactory = testCtx.GetARMResourcesClientFactoryOrDie(ctx)
		resourceGroupClient = armResourcesClientFactory.NewResourceGroupsClient()
		resourceGroup = fmt.Sprintf("e2e-lifecycle-%s-rg", uuid.NewString()[:4])
		_, err := framework.CreateResourceGroup(ctx, resourceGroupClient, resourceGroup, "westus3", 10*time.Minute)
		Expect(err).NotTo(HaveOccurred(), "failed to create resource group")
		By("Deploying the infrastructure only bicep template")
		deploymentName := fmt.Sprintf("e2e-lifecycle-%s-deployment", uuid.NewString()[:4])

		// Read the bicep template file
		bicepTemplatePath := "../templates/cluster-lifecycle-infra.bicep"
		bicepContent, err := os.ReadFile(bicepTemplatePath)
		Expect(err).NotTo(HaveOccurred(), "failed to read bicep template")

		// Create deployment parameters
		deploymentParameters := map[string]interface{}{
			"clusterName": map[string]interface{}{
				"value": clusterName,
			},
			"customerNsgName": map[string]interface{}{
				"value": fmt.Sprintf("e2e-lifecycle-%s-nsg", uuid.NewString()[:4]),
			},
			"customerVnetName": map[string]interface{}{
				"value": fmt.Sprintf("e2e-lifecycle-%s-vnet", uuid.NewString()[:4]),
			},
			"customerVnetSubnetName": map[string]interface{}{
				"value": "worker-subnet",
			},
		}

		// Create deployment properties
		deploymentProperties := armresources.DeploymentProperties{
			Template:   bicepContent,
			Parameters: deploymentParameters,
			Mode:       to.Ptr(armresources.DeploymentModeIncremental),
		}

		// Create the deployment using the framework's client factory (no systemData policy needed for deployments)
		deploymentsClient := armResourcesClientFactory.NewDeploymentsClient()
		deployment := armresources.Deployment{
			Properties: &deploymentProperties,
		}

		poller, err := deploymentsClient.BeginCreateOrUpdate(ctx, resourceGroup, deploymentName, deployment, nil)
		Expect(err).NotTo(HaveOccurred(), "failed to start bicep deployment")

		deploymentResult, err := poller.PollUntilDone(ctx, nil)
		Expect(err).NotTo(HaveOccurred(), "failed to complete bicep deployment")

		// Get deployment outputs to retrieve managed identity names
		deploymentOutputs := deploymentResult.Properties.Outputs
		Expect(deploymentOutputs).NotTo(BeNil(), "deployment outputs should not be nil")

		// Get subnet and NSG IDs from deployment outputs
		outputs := deploymentOutputs.(map[string]interface{})
		subnetID := outputs["subnetId"].(map[string]interface{})["value"].(string)
		nsgID := outputs["networkSecurityGroupId"].(map[string]interface{})["value"].(string)

		By("Converting the UserAssignedIdentity map to a map of pointers")
		uamiMap := make(map[string]*api.UserAssignedIdentity, len(customerEnv.IdentityUAMIs))
		for k, v := range customerEnv.IdentityUAMIs {
			identity := v // Create a new variable in the loop's scope to get a unique pointer.
			uamiMap[k] = &identity
		}

		By("Defining a new cluster resource for creation")
		location := "westus3" // We currently do not have location provided in the infra only json config so hard code the location for now.

		// Define values for the new properties, we need the version which not currently specified in the infra only json config, network values are default and we probably don't need them here.
		versionID := "openshift-v4.18.1"
		channelGroup := "stable"
		networkType := api.NetworkTypeOVNKubernetes
		podCidr := "10.128.0.0/14"
		serviceCidr := "172.30.0.0/16"
		machineCidr := "10.0.0.0/16"
		hostPrefix := int32(23)
		visibility := api.VisibilityPublic
		identityType := api.ManagedServiceIdentityTypeUserAssigned

		clusterResource := api.HcpOpenShiftCluster{
			Location: &location,
			Properties: &api.HcpOpenShiftClusterProperties{
				Platform: &api.PlatformProfile{
					SubnetID:               &subnetID,
					NetworkSecurityGroupID: &nsgID,
					OperatorsAuthentication: &api.OperatorsAuthenticationProfile{
						UserAssignedIdentities: &customerEnv.UAMIs,
					},
				},
				API: &api.APIProfile{
					Visibility: &visibility,
				},
				Version: &api.VersionProfile{
					ID:           &versionID,
					ChannelGroup: &channelGroup,
				},
				DNS: &api.DNSProfile{},
				Network: &api.NetworkProfile{
					NetworkType: &networkType,
					PodCidr:     &podCidr,
					ServiceCidr: &serviceCidr,
					MachineCidr: &machineCidr,
					HostPrefix:  &hostPrefix,
				},
				Console: &api.ConsoleProfile{},
			},
			Identity: &api.ManagedServiceIdentity{
				Type:                   &identityType,
				UserAssignedIdentities: uamiMap,
			},
		}

		By("Sending a PUT request to create the cluster")
		// Use the pre-configured clustersClient that has the systemData policy applied
		createPoller, err := clustersClient.BeginCreateOrUpdate(ctx, resourceGroup, clusterName, clusterResource, nil)
		Expect(err).NotTo(HaveOccurred(), "failed to start cluster creation")

		By("Waiting for the create poller to complete")
		_, err = createPoller.PollUntilDone(ctx, nil)
		Expect(err).NotTo(HaveOccurred(), "failed to create cluster")

		By("Polling until the cluster provisioning state is Succeeded")
		Eventually(func(g Gomega) {
			resp, err := clustersClient.Get(ctx, resourceGroup, clusterName, nil)
			g.Expect(err).NotTo(HaveOccurred(), "failed to get cluster status during creation poll")
			g.Expect(resp.Properties).NotTo(BeNil())
			g.Expect(resp.Properties.ProvisioningState).NotTo(BeNil())
			g.Expect(string(*resp.Properties.ProvisioningState)).To(Equal("Succeeded"), "cluster did not reach Succeeded state in time")
		}).WithTimeout(30 * time.Minute).WithPolling(1 * time.Minute).Should(Succeed())

		By("Sending a DELETE request for the cluster")
		deletePoller, err := clustersClient.BeginDelete(ctx, resourceGroup, clusterName, nil)
		Expect(err).NotTo(HaveOccurred(), "failed to start cluster deletion")

		_, err = deletePoller.PollUntilDone(ctx, nil)
		Expect(err).NotTo(HaveOccurred(), "failed to poll for cluster deletion")

		By("Verifying the cluster is not found after deletion")
		_, err = clustersClient.Get(ctx, resourceGroup, clusterName, nil)
		Expect(err).ToNot(BeNil())
		errMessage := fmt.Sprintf("The resource 'hcpOpenShiftClusters/%s' under resource group '%s' was not found.", clusterName, resourceGroup)
		Expect(err.Error()).To(ContainSubstring(errMessage))
		By("Cleaning up the resource group")
		testCtx = framework.InvocationContext()
		armResourcesClientFactory = testCtx.GetARMResourcesClientFactoryOrDie(ctx)
		resourceGroupClient = armResourcesClientFactory.NewResourceGroupsClient()
		err = framework.DeleteResourceGroup(ctx, resourceGroupClient, resourceGroup, 1*time.Second, 60*time.Minute)
		Expect(err).NotTo(HaveOccurred(), "failed to delete resource group during cleanup")
		By("Verifying the resource group is deleted")
		_, err = resourceGroupClient.Get(ctx, resourceGroup, nil)
		Expect(err).ToNot(BeNil())
		errMessage = fmt.Sprintf("The resource group '%s' could not be found.", resourceGroup)
		Expect(err.Error()).To(ContainSubstring(errMessage))
	})
})
