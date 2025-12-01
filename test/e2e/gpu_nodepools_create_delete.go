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

	"k8s.io/apimachinery/pkg/util/rand"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("HCP Nodepools GPU instances", func() {
	// Mapping of human-friendly SKU identifiers to Azure VM size names
	type gpuSKU struct {
		display string
		vmSize  string
	}
	gpuSkus := []gpuSKU{
		{display: "NC6sv3", vmSize: "Standard_NC6s_v3"},
		/*{display: "NC4asT4v3", vmSize: "Standard_NC4as_T4_v3"},
		{display: "NC8asT4v3", vmSize: "Standard_NC8as_T4_v3"},
		{display: "NC12sv3", vmSize: "Standard_NC12s_v3"},
		{display: "NC16asT4v3", vmSize: "Standard_NC16as_T4_v3"},
		{display: "NC24sv3", vmSize: "Standard_NC24s_v3"},
		{display: "NC24rsv3", vmSize: "Standard_NC24rs_v3"},
		{display: "NC64asT4v3", vmSize: "Standard_NC64as_T4_v3"},
		{display: "ND96asrv4", vmSize: "Standard_ND96asr_v4"},
		{display: "NC24adsA100v4", vmSize: "Standard_NC24ads_A100_v4"},
		{display: "NC48adsA100v4", vmSize: "Standard_NC48ads_A100_v4"},
		{display: "NC96adsA100v4", vmSize: "Standard_NC96ads_A100_v4"},
		{display: "ND96amsrA100v4", vmSize: "Standard_ND96amsr_A100_v4"},*/
	}

	for _, sku := range gpuSkus {
		sku := sku
		It("creates and deletes vm type "+sku.display+" in a single cluster",
			labels.RequireNothing,
			labels.Critical,
			labels.Positive,
			labels.IntegrationOnly,
			func(ctx context.Context) {
				customerClusterName := "gpu-nodepool-cluster-" + rand.String(6)

				tc := framework.NewTestContext()
				location := tc.Location()

				By("creating a resource group")
				resourceGroup, err := tc.NewResourceGroup(ctx, "gpu-nodepools-"+sku.display, location)
				Expect(err).NotTo(HaveOccurred())

				By("deploying demo template (single-step infra + identities + cluster)")
				_, err = tc.CreateBicepTemplateAndWait(ctx,
					*resourceGroup.Name,
					"aro-hcp-demo",
					framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/demo.json")),
					map[string]interface{}{
						"clusterName": customerClusterName,
					},
					45*time.Minute,
				)
				Expect(err).NotTo(HaveOccurred())

				By("getting credentials and verifying cluster is viable")
				adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster(
					ctx,
					tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
					*resourceGroup.Name,
					customerClusterName,
					10*time.Minute,
				)
				Expect(err).NotTo(HaveOccurred())
				Expect(verifiers.VerifyHCPCluster(ctx, adminRESTConfig)).To(Succeed())

				// Use Bicep template to create a nodepool with the specified parameters
				npName := "np-1" // node pools have very restrictive naming rules
				By(fmt.Sprintf("creating GPU nodepool %q with VM size %q using Bicep template", npName, sku.vmSize))
				_, err = tc.CreateBicepTemplateAndWait(ctx,
					*resourceGroup.Name,
					"aro-hcp-gpu-nodepool-"+sku.display,
					framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/nodepool.json")),
					map[string]interface{}{
						"clusterName":  customerClusterName,
						"nodePoolName": npName,
						"replicas":     1,
						"vmSize":       sku.vmSize,
					},
					45*time.Minute,
				)
				Expect(err).NotTo(HaveOccurred())

				// Verify provisioning succeeded and VM size matches what we requested
				created, err := framework.GetNodePool(ctx,
					tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
					*resourceGroup.Name,
					customerClusterName,
					npName,
				)
				Expect(err).NotTo(HaveOccurred())
				Expect(created.Properties).ToNot(BeNil())
				Expect(created.Properties.ProvisioningState).ToNot(BeNil())
				Expect(*created.Properties.ProvisioningState).To(Equal(hcpsdk20240610preview.ProvisioningStateSucceeded))
				Expect(created.Properties.Platform).ToNot(BeNil())
				Expect(created.Properties.Platform.VMSize).ToNot(BeNil())
				Expect(*created.Properties.Platform.VMSize).To(Equal(sku.vmSize))

				// Delete
				By(fmt.Sprintf("deleting GPU nodepool %qd", npName))
				Expect(framework.DeleteNodePool(
					ctx,
					tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
					*resourceGroup.Name,
					customerClusterName,
					npName,
					25*time.Minute,
				)).To(Succeed())

				// Confirm it's gone
				_, getErr := framework.GetNodePool(ctx,
					tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
					*resourceGroup.Name,
					customerClusterName,
					npName,
				)
				Expect(getErr).To(HaveOccurred())
			},
		)
	}
})
