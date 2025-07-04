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

	"github.com/google/uuid"

	api "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/test/util/integration"
	"github.com/Azure/ARO-HCP/test/util/labels"

	"github.com/onsi/gomega/format"
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

	It("PUT a cluster, wait for RP to report success, and then DELETE it", labels.Critical, labels.Positive, labels.CreateCluster, func(ctx context.Context) {
		// Generate a unique name for the cluster for this specific test run to avoid collisions.
		clusterName := fmt.Sprintf("e2e-lifecycle-%s", uuid.NewString()[:8])
		format.MaxLength = 0
		By("Converting the UserAssignedIdentity map to a map of pointers")
		uamiMap := make(map[string]*api.UserAssignedIdentity, len(customerEnv.IdentityUAMIs))
		for k, v := range customerEnv.IdentityUAMIs {
			identity := v // Create a new variable in the loop's scope to get a unique pointer.
			uamiMap[k] = &identity
		}

		By("Defining a new cluster resource for creation")
		location := "westus3" // We curently do not have location provided in the infra only json config so hard code the location for now.
		// NOTE: We currently hard code the name of the subnet because the infra only json config does not provide the name of the subnet.
		subnetID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/virtualNetworks/%s/subnets/worker-subnet", subscriptionID, customerEnv.CustomerRGName, customerEnv.CustomerVNetName)
		nsgID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/networkSecurityGroups/%s", subscriptionID, customerEnv.CustomerRGName, customerEnv.CustomerNSGName)

		// Define values for the new properties, we need the version which not currently specified in the infra only json config, network values are default and we probably don't need them here.
		versionID := "openshift-v4.18.1"
		channelGroup := "stable"
		networkType := api.NetworkTypeOVNKubernetes
		podCidr := "10.128.0.0/14"
		serviceCidr := "172.30.0.0/16"
		machineCidr := "10.0.0.0/16"
		hostPrefix := int32(23)
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
					Visibility: func(v api.Visibility) *api.Visibility { return &v }("Public"), // api.Visibility returns 'public' for some reason which the RP does not accept.
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

		defer func() {
			By("Cleaning up the cluster resource")
			// Use a new context for cleanup to avoid cancellation if the test context timed out.
			cleanupCtx := context.Background()
			poller, err := clustersClient.BeginDelete(cleanupCtx, customerEnv.CustomerRGName, clusterName, nil)

			// We don't want to fail the test here if cleanup fails, but we should log it.
			if err != nil {
				GinkgoLogr.Error(err, "failed to start cluster deletion during cleanup")
				return
			}

			_, err = poller.PollUntilDone(cleanupCtx, nil)
			if err != nil {
				GinkgoLogr.Error(err, "failed to poll for cluster deletion during cleanup")
			}
		}()

		By("Sending a PUT request to create the cluster")
		createPoller, err := clustersClient.BeginCreateOrUpdate(ctx, customerEnv.CustomerRGName, clusterName, clusterResource, nil)
		Expect(err).NotTo(HaveOccurred(), "failed to start cluster creation")

		By("Waiting for the create poller to complete")
		_, err = createPoller.PollUntilDone(ctx, nil)
		Expect(err).NotTo(HaveOccurred(), "failed to create cluster")

		By("Polling until the cluster provisioning state is Succeeded")
		Eventually(func(g Gomega) {
			resp, err := clustersClient.Get(ctx, customerEnv.CustomerRGName, clusterName, nil)
			g.Expect(err).NotTo(HaveOccurred(), "failed to get cluster status during creation poll")
			g.Expect(resp.Properties).NotTo(BeNil())
			g.Expect(resp.Properties.ProvisioningState).NotTo(BeNil())
			g.Expect(string(*resp.Properties.ProvisioningState)).To(Equal("Succeeded"), "cluster did not reach Succeeded state in time")
		}).WithTimeout(30 * time.Minute).WithPolling(1 * time.Minute).Should(Succeed())

		By("Sending a DELETE request for the cluster")
		deletePoller, err := clustersClient.BeginDelete(ctx, customerEnv.CustomerRGName, clusterName, nil)
		Expect(err).NotTo(HaveOccurred(), "failed to start cluster deletion")

		_, err = deletePoller.PollUntilDone(ctx, nil)
		Expect(err).NotTo(HaveOccurred(), "failed to poll for cluster deletion")

		By("Verifying the cluster is not found after deletion")
		_, err = clustersClient.Get(ctx, customerEnv.CustomerRGName, clusterName, nil)
		Expect(err).ToNot(BeNil())
		errMessage := fmt.Sprintf("The resource 'hcpOpenShiftClusters/%s' under resource group '%s' was not found.", clusterName, customerEnv.CustomerRGName)
		Expect(err.Error()).To(ContainSubstring(errMessage))
	})
})
