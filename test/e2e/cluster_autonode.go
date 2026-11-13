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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk20270201 "github.com/Azure/ARO-HCP/test/sdk/v20270201/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

// This test covers the AutoNode enablement path and the node lifecycle.
//
// Enablement: properties.autoNode.mode becomes the AFEC-gated experimental tag.
// mutateClusterExperimentalFeatures (internal/admission/admit_cluster.go)
// projects it onto ExperimentalFeatures.AutoNode. The v20270201 version package
// normalizes the field.
//
// Identity and delivery: the cluster receives the "autonode" data-plane operator
// identity (ClusterOperatorIdentifierAutoNode, in
// internal/azure/cluster_scoped_identities_config.go). The backend
// AutoNodeEnabler controller writes spec.autoNode to the HostedCluster.
//
// Provisioning: Karpenter provisions a real Azure node under scheduling
// pressure, and removes it when the pressure is gone.
//
// Known gap: Azure has no CSR auto-approver for Karpenter-provisioned nodes.
// This test works around that gap and does not fix it (see
// framework.RunKarpenterNodeCSRApprover). A passing run is not proof that the
// gap is closed.
var _ = Describe("Customer", func() {
	It("should be able to create a cluster with autonode enabled on OCP version >= 4.22 and run a web app on the Karpenter-provisioned node",
		labels.RequireNothing, labels.Medium, labels.Slow, labels.Positive, labels.AroRpApiCompatible, labels.CreateCluster,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const clusterName = "autonode-enable-422"

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
			resourceGroup, err := tc.NewResourceGroup(ctx, "autonode-enable", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for AutoNode enablement test")

			By("creating cluster parameters with version 4.22 and the typed AutoNode field set to Enabled")
			clusterParams := framework.NewDefaultClusterParams20270201()
			clusterParams.ClusterName = clusterName
			clusterParams.OpenshiftVersionId = autoNodeTestOpenshiftVersion
			// NOTE: The E2E subscription must have the ExperimentalReleaseFeatures AFEC
			// registered. NewDefaultClusterParams20270201 relies on the same AFEC for its
			// default tags. Without the registration, mutateClusterExperimentalFeatures
			// (internal/admission/admit_cluster.go) clears ExperimentalFeatures and does not
			// reject the request. A missing registration therefore fails the round-trip
			// assertion below, not the create call.
			clusterParams.AutoNodeMode = to.Ptr(hcpsdk20270201.AutoNodeModeEnabled)

			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20270201(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for AutoNode enablement cluster")

			By("verifying the autonode data-plane operator identity was provisioned")
			validateAutoNodeIdentity20270201(clusterParams.UserAssignedIdentitiesProfile)

			By("creating the HCP cluster with the typed AutoNode field set")
			err = tc.CreateHCPClusterFromParam20270201(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil, // imageDigestMirrors
				framework.ClusterCreationTimeout,
			)
			if framework.IsAPINotDeployedError(err) {
				if time.Now().Before(framework.V20270201DeploymentDeadline) {
					Skip(fmt.Sprintf("v20270201 API not yet deployed; skipping until %s", framework.V20270201DeploymentDeadline.Format(time.RFC3339)))
				}
				Fail(fmt.Sprintf("v20270201 API still not deployed as of %s deadline", framework.V20270201DeploymentDeadline.Format(time.RFC3339)))
			}
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster with the typed AutoNode field set")

			By("verifying the typed AutoNode field round-trips on the created cluster")
			validateAutoNodeEnabled20270201(ctx, tc.Get20270201ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(), *resourceGroup.Name, clusterName, tc.SubscriptionID)

			By("getting admin REST config")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20270201(
				ctx,
				tc.Get20270201ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for HCP cluster %s", clusterName)

			By("verifying the cluster is healthy with AutoNode enabled")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify HCP cluster %s is healthy", clusterName)

			By("creating a regular worker node pool so cluster-infra pods have somewhere to run besides the Karpenter node")
			// Without this node pool, each non-DaemonSet infra pod (router, monitoring,
			// image-registry) has nowhere to run except the Karpenter node. That node would
			// be the only Ready node in the cluster. Karpenter then cannot drain the node
			// during teardown: the NodePool delete tries to evict those pods, and the pods
			// have nowhere to go. The drain hangs, and VerifyKarpenterDeprovisioned times
			// out. Observed event: "Failed to drain node, N pods are waiting to be evicted".
			//
			// Other multi-node e2e tests create the same worker pool (for example
			// complete_cluster_create_multiversion.go). The workload that exercises
			// Karpenter is pinned away from this pool through its nodeSelector.
			const workerNodePoolName = "workers"
			nodePoolParams := framework.NewDefaultNodePoolParams20270201()
			nodePoolParams.ClusterName = clusterName
			nodePoolParams.NodePoolName = workerNodePoolName
			nodePoolParams.Replicas = 1

			// The node pool API requires a full Major.Minor.Patch version. The control
			// plane accepts a bare "4.22". Resolve the node version from the ClusterVersion
			// history of the cluster.
			nodePoolParams.OpenshiftVersionId = resolveAutoNodeWorkerVersion(ctx, adminRESTConfig, clusterName)

			err = tc.CreateNodePoolFromParam20270201(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				clusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create worker node pool %q for cluster %s", workerNodePoolName, clusterName)

			run := provisionKarpenterNode(ctx, adminRESTConfig, clusterName, karpenterRunOptions{})
			deprovisionKarpenterNode(ctx, adminRESTConfig, run)
		})
})
