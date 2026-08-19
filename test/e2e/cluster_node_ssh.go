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

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Customer", func() {
	// Deadline for v20260901preview API deployment in non-dev environments
	timeBombDeadline := framework.Must(time.Parse(time.RFC3339, "2026-10-31T00:00:00Z"))

	It("should persist nodeSshPublicKeys set at cluster creation and return them via ARM GET",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.CreateCluster,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const customerClusterName = "node-ssh"

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "node-ssh", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for node SSH test")

			By("creating cluster parameters with nodeSshPublicKeys")
			sshKey1, _, err := framework.GenerateSSHKeyPair()
			Expect(err).NotTo(HaveOccurred(), "failed to generate first SSH key pair for node SSH test")
			sshKey2, _, err := framework.GenerateSSHKeyPair()
			Expect(err).NotTo(HaveOccurred(), "failed to generate second SSH key pair for node SSH test")
			sshPublicKeys := []*string{
				to.Ptr(sshKey1),
				to.Ptr(sshKey2),
			}
			clusterParams := framework.NewDefaultClusterParams20260901()
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.NodeSSHPublicKeys = sshPublicKeys

			By("creating customer resources (infrastructure and managed identities)")
			clusterParams, err = tc.CreateClusterCustomerResources20260901(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for node SSH cluster")

			By("creating the HCP cluster with nodeSshPublicKeys via v20260901preview")
			err = tc.CreateHCPClusterFromParam20260901(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			if isAPINotDeployedError(err) {
				if time.Now().Before(timeBombDeadline) {
					Skip(fmt.Sprintf("v20260901preview API not yet deployed; skipping until %s", timeBombDeadline.Format(time.RFC3339)))
				}
				Fail(fmt.Sprintf("v20260901preview API still not deployed as of %s deadline", timeBombDeadline.Format(time.RFC3339)))
			}
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q with nodeSshPublicKeys", customerClusterName)

			By("verifying nodeSshPublicKeys are returned unchanged via ARM GET")
			clientFactory := tc.Get20260901ClientFactoryOrDie(ctx)
			cluster, err := clientFactory.NewHcpOpenShiftClustersClient().Get(
				ctx,
				*resourceGroup.Name,
				customerClusterName,
				nil,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get cluster %q to verify nodeSshPublicKeys", customerClusterName)
			Expect(cluster.Properties).ToNot(BeNil(), "cluster %q Properties was nil", customerClusterName)
			Expect(cluster.Properties.NodeSSHPublicKeys).To(HaveLen(len(sshPublicKeys)),
				"cluster %q Properties.NodeSSHPublicKeys length mismatch", customerClusterName)
			for i, key := range sshPublicKeys {
				Expect(cluster.Properties.NodeSSHPublicKeys[i]).ToNot(BeNil(),
					"cluster %q Properties.NodeSSHPublicKeys[%d] was nil", customerClusterName, i)
				Expect(*cluster.Properties.NodeSSHPublicKeys[i]).To(Equal(*key),
					"cluster %q nodeSshPublicKeys[%d] should match what was set at creation", customerClusterName, i)
			}
			GinkgoLogr.Info("Cluster nodeSshPublicKeys verified", "clusterName", customerClusterName)
		},
	)
})
