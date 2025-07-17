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
	"bytes"
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Customer", func() {
	BeforeEach(func() {
		// do nothing.  per test initialization usually ages better than shared.
	})

	for _, version := range []string{
		"openshift-v4.18.1",
		// TODO add other disabled versions here.
	} {
		It("should not be able to create a "+version+" HCP cluster",
			labels.RequireNothing,
			labels.Critical,
			labels.Negative,
			func(ctx context.Context) {
				const (
					customerNetworkSecurityGroupName = "customer-nsg-name"
					customerVnetName                 = "customer-vnet-name"
					customerVnetSubnetName           = "customer-vnet-subnet1"
					customerClusterName              = "illegal-hcp-cluster"
				)
				ic := framework.InvocationContext()

				By("creating a resource group")
				resourceGroup, cleanupResourceGroup, err := ic.NewResourceGroup(ctx, "basic-create", "uksouth")
				DeferCleanup(func(ctx SpecContext) {
					err := cleanupResourceGroup(ctx)
					Expect(err).NotTo(HaveOccurred())
				}, NodeTimeout(45*time.Minute))
				Expect(err).NotTo(HaveOccurred())

				By("creating a prereqs in the resource group")
				infraDeploymentResult, err := framework.CreateBicepTemplateAndWait(ctx,
					ic.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
					*resourceGroup.Name,
					"infra",
					framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/standard-cluster-create/customer-infra.json")),
					map[string]string{
						"customerNsgName":        customerNetworkSecurityGroupName,
						"customerVnetName":       customerVnetName,
						"customerVnetSubnetName": customerVnetSubnetName,
					},
					30*time.Second,
					45*time.Minute,
				)
				Expect(err).NotTo(HaveOccurred())

				By("creating the hcp cluster")
				networkSecurityGroupID, err := framework.GetOutputValueString(infraDeploymentResult, "networkSecurityGroupId")
				Expect(err).NotTo(HaveOccurred())
				subnetID, err := framework.GetOutputValueString(infraDeploymentResult, "subnetId")
				Expect(err).NotTo(HaveOccurred())
				managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)

				clusterTemplate := framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/illegal-install-version/cluster.json"))
				clusterTemplate = bytes.ReplaceAll(clusterTemplate, []byte("VERSION_REPLACE_ME"), []byte(version))

				_, err = framework.CreateBicepTemplateAndWait(ctx,
					ic.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
					*resourceGroup.Name,
					"infra",
					clusterTemplate,
					map[string]string{
						"networkSecurityGroupId":   networkSecurityGroupID,
						"subnetId":                 subnetID,
						"clusterName":              customerClusterName,
						"managedResourceGroupName": managedResourceGroupName,
					},
					30*time.Second,
					45*time.Minute,
				)
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(MatchRegexp("Version .* is disabled")))
			},
		)
	}
})
