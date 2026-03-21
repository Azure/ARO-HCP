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
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("NodePool Admission Validation", func() {

	It("should reject node pool creation with mismatched channel group",
		labels.RequireNothing,
		labels.High,
		labels.Negative,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				customerClusterName  = "np-admission-channelgroup"
				customerNodePoolName = "invalid-channelgroup"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "nodepool-channelgroup", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName
			clusterParams.ChannelGroup = "stable"

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("attempting to create node pool with mismatched channel group")
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.ChannelGroup = "candidate"

			err = tc.CreateNodePoolFromParam(ctx,
				*resourceGroup.Name,
				clusterParams.ClusterName,
				nodePoolParams,
				5*time.Minute,
			)
			Expect(err).To(HaveOccurred(), "expected error for mismatched channel group")
			Expect(strings.ToLower(err.Error())).To(ContainSubstring("must be the same as control plane channel group"),
				"error should mention channel group mismatch")
		})

	It("should reject node pool creation with version newer than cluster",
		labels.RequireNothing,
		labels.High,
		labels.Negative,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				customerClusterName  = "np-admission-version-newer"
				customerNodePoolName = "newer-version"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "nodepool-version-newer", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("attempting to create node pool with version newer than cluster")
			clusterVersion := clusterParams.OpenshiftVersionId
			parts := strings.Split(clusterVersion, ".")
			minor, _ := strconv.Atoi(parts[1])
			newerNodePoolVersion := fmt.Sprintf("%s.%d.0", parts[0], minor+1)

			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.OpenshiftVersionId = newerNodePoolVersion

			err = tc.CreateNodePoolFromParam(ctx,
				*resourceGroup.Name,
				clusterParams.ClusterName,
				nodePoolParams,
				5*time.Minute,
			)
			Expect(err).To(HaveOccurred(), "expected error for node pool version newer than cluster")
			Expect(strings.ToLower(err.Error())).To(ContainSubstring("must not exceed cluster minor version"),
				"error should mention version exceeding cluster version")
		})

	It("should reject node pool creation with version more than 2 minors behind cluster",
		labels.RequireNothing,
		labels.Medium,
		labels.Negative,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				customerClusterName  = "np-admission-version-skew"
				customerNodePoolName = "old-version"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "nodepool-version-skew", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName
			clusterParams.OpenshiftVersionId = "4.23"

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("attempting to create node pool with version 4.20 (3 minors behind)")
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.OpenshiftVersionId = "4.20.0"

			err = tc.CreateNodePoolFromParam(ctx,
				*resourceGroup.Name,
				clusterParams.ClusterName,
				nodePoolParams,
				5*time.Minute,
			)
			Expect(err).To(HaveOccurred(), "expected error for version more than 2 minors behind")
			Expect(strings.ToLower(err.Error())).To(ContainSubstring("must be within"),
				"error should mention allowed version range")
		})

	It("should accept node pool creation with version within allowed skew",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.Slow,
		func(ctx context.Context) {
			const (
				customerClusterName  = "np-admission-valid-skew"
				customerNodePoolName = "valid-version"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "nodepool-valid-skew", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating node pool with version within allowed skew (same minor)")
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.NodePoolName = customerNodePoolName

			err = tc.CreateNodePoolFromParam(ctx,
				*resourceGroup.Name,
				clusterParams.ClusterName,
				nodePoolParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred(), "node pool creation with valid version should succeed")

			By("verifying node pool was created successfully")
			nodePoolClient := tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient()
			nodePool, err := nodePoolClient.Get(ctx, *resourceGroup.Name, clusterParams.ClusterName, customerNodePoolName, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(nodePool.Properties).NotTo(BeNil(), "node pool Properties was nil")
			Expect(nodePool.Properties.Version).NotTo(BeNil(), "node pool Properties.Version was nil")
			Expect(nodePool.Properties.Version.ID).NotTo(BeNil(), "node pool Properties.Version.ID was nil")
		})

	It("should reject node pool creation with cross-major version outside allowlist",
		labels.RequireNothing,
		labels.Medium,
		labels.Negative,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				customerClusterName  = "np-admission-cross-major"
				customerNodePoolName = "invalid-cross-major"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "nodepool-cross-major", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName
			clusterParams.OpenshiftVersionId = "5.0"

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("attempting to create node pool with version 4.19 (not in allowlist for 5.0)")
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.OpenshiftVersionId = "4.19.0"

			err = tc.CreateNodePoolFromParam(ctx,
				*resourceGroup.Name,
				clusterParams.ClusterName,
				nodePoolParams,
				5*time.Minute,
			)
			Expect(err).To(HaveOccurred(), "expected error for cross-major version not in allowlist")
			Expect(strings.ToLower(err.Error())).To(ContainSubstring("must be one of"),
				"error should mention allowed cross-major versions")
		})

	It("should accept node pool creation with cross-major version in allowlist",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.Slow,
		func(ctx context.Context) {
			const (
				customerClusterName  = "np-admission-cross-major-ok"
				customerNodePoolName = "valid-cross-major"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "nodepool-cross-major-ok", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName
			clusterParams.OpenshiftVersionId = "5.0"

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating node pool with version 4.21 (in allowlist for 5.0)")
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.OpenshiftVersionId = "4.21.0"

			err = tc.CreateNodePoolFromParam(ctx,
				*resourceGroup.Name,
				clusterParams.ClusterName,
				nodePoolParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred(), "node pool creation with valid cross-major version should succeed")

			By("verifying node pool was created successfully")
			nodePoolClient := tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient()
			nodePool, err := nodePoolClient.Get(ctx, *resourceGroup.Name, clusterParams.ClusterName, customerNodePoolName, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(nodePool.Properties).NotTo(BeNil(), "node pool Properties was nil")
			Expect(nodePool.Properties.Version).NotTo(BeNil(), "node pool Properties.Version was nil")
			Expect(nodePool.Properties.Version.ID).NotTo(BeNil(), "node pool Properties.Version.ID was nil")
		})

	It("should reject node pool update with version after lowest active control plane",
		labels.RequireNothing,
		labels.High,
		labels.Negative,
		labels.AroRpApiCompatible,
		labels.Slow,
		func(ctx context.Context) {
			const (
				customerClusterName  = "np-admission-upgrade-limit"
				customerNodePoolName = "upgrade-test"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "nodepool-upgrade-limit", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating initial node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.NodePoolName = customerNodePoolName

			err = tc.CreateNodePoolFromParam(ctx,
				*resourceGroup.Name,
				clusterParams.ClusterName,
				nodePoolParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("attempting to update node pool version higher than cluster version")
			clusterVersion := clusterParams.OpenshiftVersionId
			parts := strings.Split(clusterVersion, ".")
			minor, _ := strconv.Atoi(parts[1])
			invalidNodePoolVersion := fmt.Sprintf("%s.%d.0", parts[0], minor+1)

			versionUpdate := hcpsdk20240610preview.NodePoolUpdate{
				Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
					Version: &hcpsdk20240610preview.NodePoolVersionProfile{
						ID: to.Ptr(invalidNodePoolVersion),
					},
				},
			}

			_, err = framework.UpdateNodePoolAndWait(ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name,
				clusterParams.ClusterName,
				nodePoolParams.NodePoolName,
				versionUpdate,
				10*time.Minute,
			)
			Expect(err).To(HaveOccurred(), "expected error for node pool version exceeding control plane")
			Expect(strings.ToLower(err.Error())).To(ContainSubstring("cannot exceed control plane version"),
				"error should mention version cannot exceed control plane")
		})
})
