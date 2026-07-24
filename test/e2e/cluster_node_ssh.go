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
	// Deadline for v20260630preview API deployment in non-dev environments
	timeBombDeadline := framework.Must(time.Parse(time.RFC3339, "2026-07-31T00:00:00Z"))

	It("should persist nodeSshPublicKey set at cluster creation and return it via ARM GET",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.CreateCluster,
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

			By("creating cluster parameters with nodeSshPublicKey")
			const sshPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyForE2ETesting e2e@test"
			clusterParams := framework.NewDefaultClusterParams20260630()
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.NodeSSHPublicKey = to.Ptr(sshPublicKey)

			By("creating customer resources (infrastructure and managed identities)")
			clusterParams, err = tc.CreateClusterCustomerResources20260630(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for node SSH cluster")

			By("creating the HCP cluster with nodeSshPublicKey via v20260630preview")
			err = tc.CreateHCPClusterFromParam20260630(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			if isAPINotDeployedError(err) {
				if time.Now().Before(timeBombDeadline) {
					Skip(fmt.Sprintf("v20260630preview API not yet deployed; skipping until %s", timeBombDeadline.Format(time.RFC3339)))
				}
				Fail(fmt.Sprintf("v20260630preview API still not deployed as of %s deadline", timeBombDeadline.Format(time.RFC3339)))
			}
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q with nodeSshPublicKey", customerClusterName)

			By("verifying nodeSshPublicKey is returned unchanged via ARM GET")
			clientFactory := tc.Get20260630ClientFactoryOrDie(ctx)
			cluster, err := clientFactory.NewHcpOpenShiftClustersClient().Get(
				ctx,
				*resourceGroup.Name,
				customerClusterName,
				nil,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get cluster %q to verify nodeSshPublicKey", customerClusterName)
			Expect(cluster.Properties).ToNot(BeNil(), "cluster %q Properties was nil", customerClusterName)
			Expect(cluster.Properties.NodeSSHPublicKey).ToNot(BeNil(),
				"cluster %q Properties.NodeSSHPublicKey was nil", customerClusterName)
			Expect(*cluster.Properties.NodeSSHPublicKey).To(Equal(sshPublicKey),
				"cluster %q nodeSshPublicKey should match what was set at creation", customerClusterName)
			GinkgoLogr.Info("Cluster nodeSshPublicKey verified", "clusterName", customerClusterName)
		},
	)
})
