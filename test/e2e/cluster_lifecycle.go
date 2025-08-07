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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/google/uuid"

	//"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	api "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

// extractStringSafely safely extracts a string value from nested map structure
func extractStringSafely(data map[string]interface{}, parentKey, childKey string) string {
	parent, ok := data[parentKey].(map[string]interface{})
	if !ok {
		Fail(fmt.Sprintf("parent key '%s' not found or not a map in deployment outputs", parentKey))
	}
	value, ok := parent[childKey].(string)
	if !ok {
		Fail(fmt.Sprintf("child key '%s' not found or not a string in deployment outputs under '%s'", childKey, parentKey))
	}
	return value
}

var _ = Describe("HCPOpenShiftCluster Lifecycle", func() {
	var (
		clustersClient *api.HcpOpenShiftClustersClient
	)

	BeforeEach(func() {
		// do nothing here
	})

	It("Creates requried infrastructure resources then creates a cluster", labels.Critical, labels.Positive, labels.CreateCluster, labels.RequireNothing, func(ctx context.Context) {
		// Generate a unique name for the cluster for this specific test run to avoid collisions.
		clusterName := fmt.Sprintf("e2e-lifecycle-%s", uuid.NewString()[:8])

		By("Create a new resource group for the cluster")
		// Use the framework's test context for resource group operations since they don't need the systemData policy
		tc := framework.NewTestContext()
		rgPrefix := "cluster_lifecycle"
		resourceGroup, err := tc.NewResourceGroup(ctx, rgPrefix, tc.Location())
		Expect(err).NotTo(HaveOccurred(), "failed to create resource group")

		By("Deploying the infrastructure only bicep template")
		deploymentName := fmt.Sprintf("e2e-lifecycle-%s-deployment", uuid.NewString()[:4])

		// Read the bicep template file converted to json from the correct location
		//templateContent := framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/cluster-lifecycle/infra-only.json"))
		templateContent := framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/bicep-templates/infra-only.json"))
		// Create deployment parameters for infra-only.json
		deploymentParameters := map[string]interface{}{
			"clusterName":     clusterName,
			"persistTagValue": false,
		}

		// Use the framework's CreateBicepTemplateAndWait function for consistency
		deploymentResult, err := framework.CreateBicepTemplateAndWait(
			ctx,
			tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
			*resourceGroup.Name,
			deploymentName,
			templateContent,
			deploymentParameters,
			45*time.Minute,
		)
		Expect(err).NotTo(HaveOccurred(), "failed to complete bicep deployment")

		// Extract and validate deployment outputs to retrieve managed identity information
		// This section uses safe extraction patterns to avoid panics and provide clear error messages
		deploymentOutputs := deploymentResult.Properties.Outputs
		Expect(deploymentOutputs).NotTo(BeNil(), "deployment outputs should not be nil")

		// Extract identityValue and userAssignedIdentitiesValue from outputs using GetOutputValue
		identityValueRaw, err := framework.GetOutputValue(deploymentResult, "identityValue")
		Expect(err).NotTo(HaveOccurred(), "failed to get identityValue from deployment outputs")
		identityValue := identityValueRaw.(map[string]interface{})

		userAssignedIdentitiesValueRaw, err := framework.GetOutputValue(deploymentResult, "userAssignedIdentitiesValue")
		Expect(err).NotTo(HaveOccurred(), "failed to get userAssignedIdentitiesValue from deployment outputs")
		userAssignedIdentitiesValue := userAssignedIdentitiesValueRaw.(map[string]interface{})

		// Validate that required nested structures exist
		Expect(userAssignedIdentitiesValue).To(HaveKey("controlPlaneOperators"), "controlPlaneOperators not found in deployment outputs")
		Expect(userAssignedIdentitiesValue).To(HaveKey("dataPlaneOperators"), "dataPlaneOperators not found in deployment outputs")
		Expect(userAssignedIdentitiesValue).To(HaveKey("serviceManagedIdentity"), "serviceManagedIdentity not found in deployment outputs")

		// Get subnet and NSG IDs from the main template outputs using GetOutputValueString
		subnetID, err := framework.GetOutputValueString(deploymentResult, "subnetId")
		Expect(err).NotTo(HaveOccurred(), "failed to get subnetId from deployment outputs")
		nsgID, err := framework.GetOutputValueString(deploymentResult, "networkSecurityGroupId")
		Expect(err).NotTo(HaveOccurred(), "failed to get networkSecurityGroupId from deployment outputs")

		By("Converting the UserAssignedIdentity map to a map of pointers")
		// Use the identityValue from bicep deployment instead of customerEnv.IdentityUAMIs
		identityValueMap := identityValue["userAssignedIdentities"].(map[string]interface{})
		uamiMap := make(map[string]*api.UserAssignedIdentity, len(identityValueMap))
		for identityID := range identityValueMap {
			// Create a UserAssignedIdentity with empty struct (the ID is the key)
			uamiMap[identityID] = &api.UserAssignedIdentity{}
		}

		By("Defining a new cluster resource for creation")
		location := tc.Location()

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

		// Extract service managed identity from the deployment outputs with safe extraction
		serviceManagedIdentityRaw, ok := userAssignedIdentitiesValue["serviceManagedIdentity"]
		if !ok {
			Fail("serviceManagedIdentity not found in deployment outputs")
		}
		serviceManagedIdentity, ok := serviceManagedIdentityRaw.(string)
		if !ok {
			Fail("serviceManagedIdentity is not a string in deployment outputs")
		}

		clusterResource := api.HcpOpenShiftCluster{
			Location: &location,
			Properties: &api.HcpOpenShiftClusterProperties{
				Platform: &api.PlatformProfile{
					SubnetID:               &subnetID,
					NetworkSecurityGroupID: &nsgID,
					OperatorsAuthentication: &api.OperatorsAuthenticationProfile{
						UserAssignedIdentities: &api.UserAssignedIdentitiesProfile{
							ControlPlaneOperators: map[string]*string{
								"cluster-api-azure":        to.Ptr(extractStringSafely(userAssignedIdentitiesValue, "controlPlaneOperators", "cluster-api-azure")),
								"control-plane":            to.Ptr(extractStringSafely(userAssignedIdentitiesValue, "controlPlaneOperators", "control-plane")),
								"cloud-controller-manager": to.Ptr(extractStringSafely(userAssignedIdentitiesValue, "controlPlaneOperators", "cloud-controller-manager")),
								"ingress":                  to.Ptr(extractStringSafely(userAssignedIdentitiesValue, "controlPlaneOperators", "ingress")),
								"disk-csi-driver":          to.Ptr(extractStringSafely(userAssignedIdentitiesValue, "controlPlaneOperators", "disk-csi-driver")),
								"file-csi-driver":          to.Ptr(extractStringSafely(userAssignedIdentitiesValue, "controlPlaneOperators", "file-csi-driver")),
								"image-registry":           to.Ptr(extractStringSafely(userAssignedIdentitiesValue, "controlPlaneOperators", "image-registry")),
								"cloud-network-config":     to.Ptr(extractStringSafely(userAssignedIdentitiesValue, "controlPlaneOperators", "cloud-network-config")),
							},
							DataPlaneOperators: map[string]*string{
								"disk-csi-driver": to.Ptr(extractStringSafely(userAssignedIdentitiesValue, "dataPlaneOperators", "disk-csi-driver")),
								"file-csi-driver": to.Ptr(extractStringSafely(userAssignedIdentitiesValue, "dataPlaneOperators", "file-csi-driver")),
								"image-registry":  to.Ptr(extractStringSafely(userAssignedIdentitiesValue, "dataPlaneOperators", "image-registry")),
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
		// Initialize the clustersClient using environment subscription ID
		subscriptionID, err := tc.GetSubscriptionID(ctx)
		Expect(err).NotTo(HaveOccurred(), "failed to get subscription ID")
		// Create a new credential
		credential, err := azidentity.NewDefaultAzureCredential(nil)
		Expect(err).NotTo(HaveOccurred(), "failed to create credential")
		clustersClient, err = api.NewHcpOpenShiftClustersClient(subscriptionID, credential, nil)
		Expect(err).NotTo(HaveOccurred(), "failed to create clustersClient")

		createPoller, err := clustersClient.BeginCreateOrUpdate(ctx, *resourceGroup.Name, clusterName, clusterResource, nil)
		Expect(err).NotTo(HaveOccurred(), "failed to start cluster creation")

		By("Waiting for the create poller to complete")
		_, err = createPoller.PollUntilDone(ctx, nil)
		Expect(err).NotTo(HaveOccurred(), "failed to create cluster")

		By("Polling until the cluster provisioning state is Succeeded")
		Eventually(func(g Gomega) {
			resp, err := clustersClient.Get(ctx, *resourceGroup.Name, clusterName, nil)
			g.Expect(err).NotTo(HaveOccurred(), "failed to get cluster status during creation poll")
			g.Expect(resp.Properties).NotTo(BeNil())
			g.Expect(resp.Properties.ProvisioningState).NotTo(BeNil())
			g.Expect(string(*resp.Properties.ProvisioningState)).To(Equal("Succeeded"), "cluster did not reach Succeeded state in time")
		}).WithTimeout(30 * time.Minute).WithPolling(1 * time.Minute).Should(Succeed())

		By("Sending a DELETE request for the cluster")
		deletePoller, err := clustersClient.BeginDelete(ctx, *resourceGroup.Name, clusterName, nil)
		Expect(err).NotTo(HaveOccurred(), "failed to start cluster deletion")

		_, err = deletePoller.PollUntilDone(ctx, nil)
		Expect(err).NotTo(HaveOccurred(), "failed to poll for cluster deletion")

		By("Verifying the cluster is not found after deletion")
		_, err = clustersClient.Get(ctx, *resourceGroup.Name, clusterName, nil)
		Expect(err).ToNot(BeNil())
		//errMessage := fmt.Sprintf("The resource 'hcpOpenShiftClusters/%s' under resource group '%s' was not found.", clusterName, *resourceGroup.Name)
		errMessage := "ResourceNotFound"
		Expect(err.Error()).To(ContainSubstring(errMessage))
		By("Cleaning up the resource group")
		resourceGroupClient := tc.GetARMResourcesClientFactoryOrDie(ctx).NewResourceGroupsClient()
		err = framework.DeleteResourceGroup(ctx, resourceGroupClient, *resourceGroup.Name, 60*time.Minute)
		Expect(err).NotTo(HaveOccurred(), "failed to delete resource group during cleanup")
		By("Verifying the resource group is deleted")
		_, err = resourceGroupClient.Get(ctx, *resourceGroup.Name, nil)
		Expect(err).ToNot(BeNil())
		errMessage = fmt.Sprintf("'%s' could not be found", *resourceGroup.Name)
		Expect(err.Error()).To(ContainSubstring(errMessage))
	})
})
