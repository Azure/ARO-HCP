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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	api "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("HCPOpenShiftCluster Lifecycle", func() {
	var (
		clustersClient *api.HcpOpenShiftClustersClient
	)

	BeforeEach(func() {
		By("Preparing HCP clusters client")
		clustersClient = clients.NewHcpOpenShiftClustersClient()
	})

	It("Creates requried infrastructure resources then creates a cluster", labels.Critical, labels.Positive, labels.CreateCluster, labels.RequireNothing, func(ctx context.Context) {
		// Generate a unique name for the cluster for this specific test run to avoid collisions.
		clusterName := fmt.Sprintf("e2e-lifecycle-%s", uuid.NewString()[:8])

		By("Create a new resource group for the cluster")
		// Use the framework's test context for resource group operations since they don't need the systemData policy
		ic := framework.NewInvocationContext()
		resourceGroup := fmt.Sprintf("e2e-lifecycle-%s-rg", uuid.NewString()[:4])
		resourceGroup, err := ic.NewResourceGroup(ctx, resourceGroup, ic.(interface{ Location() string }).Location())
		Expect(err).NotTo(HaveOccurred(), "failed to create resource group")

		By("Deploying the infrastructure only bicep template")
		deploymentName := fmt.Sprintf("e2e-lifecycle-%s-deployment", uuid.NewString()[:4])

		// Read the bicep template file from the correct location
		bicepTemplatePath := "../../e2e-setup/bicep/infra-only.bicep"
		bicepContent, err := os.ReadFile(bicepTemplatePath)
		Expect(err).NotTo(HaveOccurred(), "failed to read bicep template")

		// Create deployment parameters for infra-only.bicep
		deploymentParameters := map[string]string{
			"clusterName":     clusterName,
			"persistTagValue": "false",
		}

		// Use the framework's CreateBicepTemplateAndWait function for consistency
		deploymentResult, err := framework.CreateBicepTemplateAndWait(
			ctx,
			armResourcesClientFactory.NewDeploymentsClient(),
			resourceGroup,
			deploymentName,
			bicepContent,
			deploymentParameters,
			45*time.Minute,
		)
		Expect(err).NotTo(HaveOccurred(), "failed to complete bicep deployment")

		// Get deployment outputs to retrieve managed identity information
		deploymentOutputs := deploymentResult.Properties.Outputs
		Expect(deploymentOutputs).NotTo(BeNil(), "deployment outputs should not be nil")

		// Get deployment outputs to retrieve managed identity information
		outputs := deploymentOutputs.(map[string]interface{})

		// Extract identityValue and userAssignedIdentitiesValue from outputs
		identityValue := outputs["identityValue"].(map[string]interface{})
		userAssignedIdentitiesValue := outputs["userAssignedIdentitiesValue"].(map[string]interface{})

		// Get subnet and NSG IDs from the main template outputs
		subnetID := outputs["subnetId"].(map[string]interface{})["value"].(string)
		nsgID := outputs["networkSecurityGroupId"].(map[string]interface{})["value"].(string)

		By("Converting the UserAssignedIdentity map to a map of pointers")
		// Use the identityValue from bicep deployment instead of customerEnv.IdentityUAMIs
		identityValueMap := identityValue["userAssignedIdentities"].(map[string]interface{})
		uamiMap := make(map[string]*api.UserAssignedIdentity, len(identityValueMap))
		for identityID := range identityValueMap {
			// Create a UserAssignedIdentity with empty struct (the ID is the key)
			uamiMap[identityID] = &api.UserAssignedIdentity{}
		}

		By("Defining a new cluster resource for creation")
		location := ic.(interface{ Location() string }).Location()

		// Define values for the new properties, we need the version which not currently specified in the infra only json config, network values are default and we probably don't need them here.
		versionID := "openshift-v4.19.0"
		channelGroup := "stable"
		networkType := api.NetworkTypeOVNKubernetes
		podCidr := "10.128.0.0/14"
		serviceCidr := "172.30.0.0/16"
		machineCidr := "10.0.0.0/16"
		hostPrefix := int32(23)
		visibility := api.VisibilityPublic
		identityType := api.ManagedServiceIdentityTypeUserAssigned

		// Extract service managed identity from the deployment outputs
		serviceManagedIdentity := userAssignedIdentitiesValue["serviceManagedIdentity"].(string)

		clusterResource := api.HcpOpenShiftCluster{
			Location: &location,
			Properties: &api.HcpOpenShiftClusterProperties{
				Platform: &api.PlatformProfile{
					SubnetID:               &subnetID,
					NetworkSecurityGroupID: &nsgID,
					OperatorsAuthentication: &api.OperatorsAuthenticationProfile{
						UserAssignedIdentities: &api.UserAssignedIdentitiesProfile{
							ControlPlaneOperators: map[string]*string{
								"cluster-api-azure":        to.Ptr(userAssignedIdentitiesValue["controlPlaneOperators"].(map[string]interface{})["cluster-api-azure"].(string)),
								"control-plane":            to.Ptr(userAssignedIdentitiesValue["controlPlaneOperators"].(map[string]interface{})["control-plane"].(string)),
								"cloud-controller-manager": to.Ptr(userAssignedIdentitiesValue["controlPlaneOperators"].(map[string]interface{})["cloud-controller-manager"].(string)),
								"ingress":                  to.Ptr(userAssignedIdentitiesValue["controlPlaneOperators"].(map[string]interface{})["ingress"].(string)),
								"disk-csi-driver":          to.Ptr(userAssignedIdentitiesValue["controlPlaneOperators"].(map[string]interface{})["disk-csi-driver"].(string)),
								"file-csi-driver":          to.Ptr(userAssignedIdentitiesValue["controlPlaneOperators"].(map[string]interface{})["file-csi-driver"].(string)),
								"image-registry":           to.Ptr(userAssignedIdentitiesValue["controlPlaneOperators"].(map[string]interface{})["image-registry"].(string)),
								"cloud-network-config":     to.Ptr(userAssignedIdentitiesValue["controlPlaneOperators"].(map[string]interface{})["cloud-network-config"].(string)),
							},
							DataPlaneOperators: map[string]*string{
								"disk-csi-driver": to.Ptr(userAssignedIdentitiesValue["dataPlaneOperators"].(map[string]interface{})["disk-csi-driver"].(string)),
								"file-csi-driver": to.Ptr(userAssignedIdentitiesValue["dataPlaneOperators"].(map[string]interface{})["file-csi-driver"].(string)),
								"image-registry":  to.Ptr(userAssignedIdentitiesValue["dataPlaneOperators"].(map[string]interface{})["image-registry"].(string)),
							},
							ServiceManagedIdentity: &serviceManagedIdentity,
						},
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
		ic = framework.NewInvocationContext()
		armResourcesClientFactory = ic.(interface {
			GetARMResourcesClientFactoryOrDie(context.Context) *armresources.ClientFactory
		}).GetARMResourcesClientFactoryOrDie(ctx)
		resourceGroupClient = armResourcesClientFactory.NewResourceGroupsClient()
		err = framework.DeleteResourceGroup(ctx, resourceGroupClient, resourceGroup, 60*time.Minute)
		Expect(err).NotTo(HaveOccurred(), "failed to delete resource group during cleanup")
		By("Verifying the resource group is deleted")
		_, err = resourceGroupClient.Get(ctx, resourceGroup, nil)
		Expect(err).ToNot(BeNil())
		errMessage = fmt.Sprintf("The resource group '%s' could not be found.", resourceGroup)
		Expect(err.Error()).To(ContainSubstring(errMessage))
	})
})
