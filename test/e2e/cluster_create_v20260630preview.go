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

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	It("should create a cluster using the 2026-06-30-preview API version",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				clusterName = "v20260630preview-cluster"
				apiVersion  = "2026-06-30-preview"
			)
			tc := framework.NewTestContext()

			By("checking API version availability")
			if !framework.IsDevelopmentEnvironment() {
				resourcesFactory, err := tc.GetARMResourcesClientFactory(ctx)
				Expect(err).NotTo(HaveOccurred(), "failed to get ARM resources client factory")

				providersClient := resourcesFactory.NewProvidersClient()
				provider, err := providersClient.Get(ctx, "Microsoft.RedHatOpenShift", nil)
				Expect(err).NotTo(HaveOccurred(), "failed to get Microsoft.RedHatOpenShift resource provider")

				available := false
				for _, rt := range provider.ResourceTypes {
					if rt.ResourceType == nil || !strings.EqualFold(*rt.ResourceType, "hcpOpenShiftClusters") {
						continue
					}
					for _, v := range rt.APIVersions {
						if v != nil && strings.EqualFold(*v, apiVersion) {
							available = true
							break
						}
					}
				}
				if !available {
					if time.Now().After(framework.Must(time.Parse(time.RFC3339, "2026-07-31T00:00:00Z"))) {
						Fail(fmt.Sprintf("API version %s should be available for Microsoft.RedHatOpenShift/hcpOpenShiftClusters by 2026-07-31 00:00 UTC", apiVersion))
					}
					Skip(fmt.Sprintf("API version %s is not available for Microsoft.RedHatOpenShift/hcpOpenShiftClusters in this environment", apiVersion))
				}
				GinkgoLogr.Info("API version available", "version", apiVersion)
			}

			clusterParams := framework.NewDefaultClusterParams20260630()
			clusterParams.ClusterName = clusterName
			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, clusterName, tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for cluster %s", clusterName)

			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20260630(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create cluster customer resources")

			By("creating the HCP cluster")
			err = tc.CreateHCPCluster20260630FromParam(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %s using API version %s", clusterName, apiVersion)

			By("getting credentials")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %s", clusterName)

			By("ensuring the cluster is viable")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify the cluster is healthy")

		},
	)
})
