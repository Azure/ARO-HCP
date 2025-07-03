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
	//"errors"
	"fmt"
	//"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/google/uuid"

	api "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/test/util/integration"
	"github.com/Azure/ARO-HCP/test/util/labels"

	"github.com/onsi/gomega/format"
)

// The new test file: cluster_lifecycle_test.go
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

	// This test covers the full create, wait, and delete lifecycle.
	// It is marked as Critical because it validates the most fundamental
	// user workflow, especially for new deployments where no cluster exists.
	It("should create a cluster, wait for success, and then delete it", labels.Critical, labels.Positive, labels.CreateCluster, func(ctx context.Context) {
		// Generate a unique name for the cluster for this specific test run to avoid collisions.
		clusterName := fmt.Sprintf("e2e-lifecycle-%s", uuid.NewString()[:8])
		format.MaxLength = 0
		By("Converting the UserAssignedIdentity map to a map of pointers")
		// The API expects a map of pointers, but the setup model provides a map of values.
		// We need to convert it before using it.
		uamiMap := make(map[string]*api.UserAssignedIdentity, len(customerEnv.IdentityUAMIs))
		for k, v := range customerEnv.IdentityUAMIs {
			identity := v // Create a new variable in the loop's scope to get a unique pointer.
			uamiMap[k] = &identity
		}

		By("Defining a new cluster resource for creation")
		// In a scenario where the cluster does not yet exist, we must construct the
		// cluster resource programmatically based on the provided API models.
		location := "westus3" // A default location, should be sourced from config if possible.
		// Construct resource IDs from the setup configuration.
		// NOTE: This assumes the worker subnet is the primary subnet for the platform profile.
		// This might need adjustment if control-plane and worker subnets are distinct in the platform config.
		subnetID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/virtualNetworks/%s/subnets/worker-subnet", subscriptionID, customerEnv.CustomerRGName, customerEnv.CustomerVNetName)
		nsgID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/networkSecurityGroups/%s", subscriptionID, customerEnv.CustomerRGName, customerEnv.CustomerNSGName)

		// Define values for the new properties
		versionID := "openshift-v4.18.1"
		channelGroup := "stable"
		networkType := api.NetworkTypeOVNKubernetes
		podCidr := "10.128.0.0/14"
		serviceCidr := "172.30.0.0/16"
		machineCidr := "10.0.0.0/16"
		hostPrefix := int32(23)
		//visibility := api.VisibilityPublic
		//visibility := "Public"
		identityType := api.ManagedServiceIdentityTypeUserAssigned
		// Define values for the SystemData field as required by the RP.
		//createdBy := "shadownman@example.com"
		//createdByType := api.CreatedByTypeUser
		//createdAt := time.Now()

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
					Visibility: func(v api.Visibility) *api.Visibility { return &v }("Public"),
				},
				Version: &api.VersionProfile{
					ID:           &versionID,
					ChannelGroup: &channelGroup,
				},
				DNS: &api.DNSProfile{}, // Empty as per the provided JSON
				Network: &api.NetworkProfile{
					NetworkType: &networkType,
					PodCidr:     &podCidr,
					ServiceCidr: &serviceCidr,
					MachineCidr: &machineCidr,
					HostPrefix:  &hostPrefix,
				},
				Console: &api.ConsoleProfile{}, // Empty as per the provided JSON
			},
			Identity: &api.ManagedServiceIdentity{
				Type:                   &identityType,
				UserAssignedIdentities: uamiMap,
			},
		}

		// Defer the cleanup function to ensure the cluster is deleted even if the test fails.
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
		// This timeout should be long enough for a cluster to be created.
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
		// After a successful deletion, a GET request should return a 404 Not Found error.
		/* Eventually(func(g Gomega) {
			_, err := clustersClient.Get(ctx, customerEnv.CustomerRGName, clusterName, nil)
			g.Expect(err).To(HaveOccurred())

			// Define a type assertion target for checking the HTTP status code
			type statusCodeGetter interface {
				error
				StatusCode() int
			}
			var scg statusCodeGetter
			g.Expect(errors.As(err, &scg)).To(BeTrue(), "error does not contain a status code")
			g.Expect(scg.StatusCode()).To(Equal(http.StatusNotFound), "expected a 404 Not Found error after deletion")

		}).WithTimeout(30 * time.Minute).WithPolling(60 * time.Second).Should(Succeed()) */
	})
})
