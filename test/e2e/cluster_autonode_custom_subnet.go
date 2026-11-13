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

package e2e

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk20270201 "github.com/Azure/ARO-HCP/test/sdk/v20270201/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	It("should be able to provision a Karpenter node on a custom subnet in the cluster VNet with autonode enabled on OCP version >= 4.22",
		labels.RequireNothing, labels.Medium, labels.Slow, labels.Positive, labels.AroRpApiCompatible, labels.CreateCluster,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const clusterName = "autonode-subnet-422"
			tc := framework.NewTestContext()

			By("checking that the v20270201 API is deployed before creating Azure resources")
			apiDeployed, probeErr := framework.IsAPI20270201Deployed(ctx, tc.Get20270201ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient())
			Expect(probeErr).NotTo(HaveOccurred(), "failed to probe the v20270201 API")
			if !apiDeployed {
				if time.Now().Before(framework.V20270201DeploymentDeadline) {
					Skip(fmt.Sprintf("v20270201 API not yet deployed; skipping until %s", framework.V20270201DeploymentDeadline.Format(time.RFC3339)))
				}
				Fail(fmt.Sprintf("v20270201 API still not deployed as of %s deadline", framework.V20270201DeploymentDeadline.Format(time.RFC3339)))
			}

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "autonode-subnet", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create the resource group for the AutoNode custom subnet test")

			By("creating cluster parameters with AutoNode enabled on OCP version 4.22")
			clusterParams := framework.NewDefaultClusterParams20270201()
			clusterParams.ClusterName = clusterName
			clusterParams.OpenshiftVersionId = autoNodeTestOpenshiftVersion
			clusterParams.AutoNodeMode = to.Ptr(hcpsdk20270201.AutoNodeModeEnabled)
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20270201(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for the AutoNode custom subnet test")

			By("creating a separate customer subnet in the cluster VNet")
			networkFactory, err := tc.GetARMNetworkClientFactory(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to create the Azure network client for the custom subnet")
			// customer-infra.json reserves 10.0.0.0/24 and 10.0.1.0/24.
			// This subnet stays inside its 10.0.0.0/16 VNet and machine CIDR.
			// TestContext removes the subnet with the customer resource group.
			subnetID, err := framework.CreateKarpenterSubnet(ctx, networkFactory,
				clusterParams.SubnetResourceID, "autonode-custom-subnet", "10.0.2.0/24", clusterParams.NsgResourceID)
			Expect(err).NotTo(HaveOccurred(), "failed to create the custom subnet in the cluster VNet")
			Expect(strings.EqualFold(subnetID, clusterParams.SubnetResourceID)).To(BeFalse(),
				"custom subnet %q must differ from cluster default subnet %q", subnetID, clusterParams.SubnetResourceID)

			By("verifying the autonode data-plane operator identity was provisioned")
			validateAutoNodeIdentity20270201(clusterParams.UserAssignedIdentitiesProfile)

			By("creating the HCP cluster with AutoNode enabled")
			err = tc.CreateHCPClusterFromParam20270201(ctx, GinkgoLogr, *resourceGroup.Name, clusterParams, nil, framework.ClusterCreationTimeout)
			if framework.IsAPINotDeployedError(err) {
				if time.Now().Before(framework.V20270201DeploymentDeadline) {
					Skip(fmt.Sprintf("v20270201 API not yet deployed; skipping until %s", framework.V20270201DeploymentDeadline.Format(time.RFC3339)))
				}
				Fail(fmt.Sprintf("v20270201 API still not deployed as of %s deadline", framework.V20270201DeploymentDeadline.Format(time.RFC3339)))
			}
			Expect(err).NotTo(HaveOccurred(), "failed to create the HCP cluster for the AutoNode custom subnet test")

			By("verifying the typed AutoNode field round-trips on the created cluster")
			validateAutoNodeEnabled20270201(ctx, tc.Get20270201ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(), *resourceGroup.Name, clusterName, tc.SubscriptionID)

			By("getting admin REST config")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20270201(ctx, tc.Get20270201ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(), *resourceGroup.Name, clusterName, framework.GetAdminRESTConfigTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for HCP cluster %s", clusterName)

			By("verifying the cluster is available with AutoNode enabled")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify HCP cluster %s is available", clusterName)

			By("creating one regular worker for cluster services and node drain")
			nodePoolParams := framework.NewDefaultNodePoolParams20270201()
			nodePoolParams.ClusterName = clusterName
			nodePoolParams.NodePoolName = "workers"
			nodePoolParams.OpenshiftVersionId = resolveAutoNodeWorkerVersion(ctx, adminRESTConfig, clusterName)
			nodePoolParams.Replicas = 1
			err = tc.CreateNodePoolFromParam20270201(ctx, GinkgoLogr, *resourceGroup.Name, managedResourceGroupName, clusterName, nodePoolParams, framework.NodePoolCreationTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to create the regular worker for HCP cluster %s", clusterName)

			By("checking that no Karpenter node exists before the NodeClass carries the custom subnet")
			// The node must start on the custom subnet. A node that already exists
			// would make the subnet a change to a running NodeClass, and the check
			// below could then describe a replacement instead of the first node.
			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create the Kubernetes client for the node subnet check")
			existing, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{LabelSelector: framework.KarpenterNodePoolLabel})
			Expect(err).NotTo(HaveOccurred(), "failed to list Karpenter nodes before the custom subnet NodeClass")
			Expect(existing.Items).To(BeEmpty(), "expected no Karpenter node before the NodeClass is created with the custom subnet, observed %d", len(existing.Items))

			By("creating the NodeClass with the custom subnet and provisioning the workload on it")
			run := provisionKarpenterNode(ctx, adminRESTConfig, clusterName, karpenterRunOptions{
				nodeClassOptions: []framework.OpenShiftAzureNodeClassOption{framework.WithNodeClassVNetSubnetID(subnetID)},
			})

			By("checking the actual Azure NIC subnet for each Karpenter workload node")
			computeFactory, err := tc.GetARMComputeClientFactory(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to create the Azure compute client for the node subnet check")
			Expect(run.nodeNames).To(HaveLen(1), "the workload must run on one Karpenter node before the Azure subnet check")
			for _, nodeName := range run.nodeNames {
				node, err := kubeClient.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred(), "failed to get Karpenter workload node %q for the Azure subnet check", nodeName)
				nic, err := framework.ResolveNodePrimaryNIC(ctx, computeFactory, networkFactory, node)
				Expect(err).NotTo(HaveOccurred(), "failed to resolve the Azure VM and primary NIC for Karpenter node %q", nodeName)
				// Read Azure state, not the NodeClass request or its Ready condition.
				err = framework.CheckKarpenterNICSubnet(nic, subnetID)
				Expect(err).NotTo(HaveOccurred(),
					"Karpenter node %q must use custom subnet %q, not cluster default subnet %q", nodeName, subnetID, clusterParams.SubnetResourceID)
			}

			// Do not wait for the Karpenter node to drain. Cleanup deletes the
			// NodePool and the cluster, and that removes the node.
		})
})
