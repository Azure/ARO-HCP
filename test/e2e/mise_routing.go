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
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

// miseV2HeaderPolicy injects the x-ms-mise-version request header
// so that the Istio VirtualService routes to the MISE v2 frontend.
type miseV2HeaderPolicy struct {
	version string
}

func (p *miseV2HeaderPolicy) Do(req *policy.Request) (*http.Response, error) {
	req.Raw().Header.Set("x-ms-mise-version", p.version)
	return req.Next()
}

// miseVersionValidator checks the x-ms-served-by response header on every
// response and records mismatches so the full cluster lifecycle completes
// before assertions run.
type miseVersionValidator struct {
	expectedVersion string
	mismatches      []string
	requestCount    int
}

func (p *miseVersionValidator) Do(req *policy.Request) (*http.Response, error) {
	resp, err := req.Next()
	if resp != nil {
		p.requestCount++
		actual := resp.Header.Get("x-ms-served-by")
		if actual != p.expectedVersion {
			p.mismatches = append(p.mismatches,
				fmt.Sprintf("request #%d %s %s: got %q, want %q",
					p.requestCount, req.Raw().Method, req.Raw().URL.Path,
					actual, p.expectedVersion))
		}
	}
	return resp, err
}

// Tests the VirtualService routes to the correct frontend instance based on request headers
// by creating and deleting a full HCP cluster, validating every response header.
// In INT and above, this exercises MISE-backed routing. In dev/prow environments, the same
// VirtualService is deployed but fronts non-MISE frontend instances. PR checks connect
// through the Istio ingress gateway (not port-forwarded), so the VirtualService routing
// rules are always evaluated.
var _ = Describe("MISE Routing", func() {
	defer GinkgoRecover()

	DescribeTable("routes to the correct frontend based on version header",
		labels.RequireNothing,
		labels.AroRpApiCompatible,
		labels.Low,
		labels.Positive,
		func(ctx context.Context, rgPrefix string, miseVersionHeader string, expectedVersion string) {
			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred(), "failed to assign identity containers")
			}

			By("Creating resource group")
			rg, err := tc.NewResourceGroup(ctx, rgPrefix, tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for MISE routing test")

			By("Creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = "mise-routing"
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*rg.Name, "-managed", 64)

			By("Creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				rg,
				clusterParams,
				map[string]any{
					"customerNsgName":        "customer-nsg-name",
					"customerVnetName":       "customer-vnet-name",
					"customerVnetSubnetName": "customer-vnet-subnet1",
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create cluster customer resources")

			By("Building client factory with MISE policies")
			validator := &miseVersionValidator{expectedVersion: expectedVersion}
			policies := []policy.Policy{validator}
			if miseVersionHeader != "" {
				policies = append([]policy.Policy{&miseV2HeaderPolicy{version: miseVersionHeader}}, policies...)
			}
			clientFactory, err := tc.Get20251223ClientFactoryWithPolicies(ctx, policies...)
			Expect(err).NotTo(HaveOccurred(), "failed to build client factory with MISE policies")

			By("Building HCP cluster from parameters")
			cluster, err := framework.BuildHCPCluster20251223FromParams(clusterParams, tc.Location(), nil)
			Expect(err).NotTo(HaveOccurred(), "failed to build HCP cluster from params")

			By("Creating HCP cluster")
			hcpClient := clientFactory.NewHcpOpenShiftClustersClient()
			_, err = framework.CreateHCPCluster20251223AndWait(
				ctx, GinkgoLogr, hcpClient,
				*rg.Name, clusterParams.ClusterName, cluster,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster")

			By("Deleting HCP cluster")
			delCtx, delCancel := context.WithTimeout(ctx, 45*time.Minute)
			defer delCancel()
			delPoller, err := hcpClient.BeginDelete(delCtx, *rg.Name, clusterParams.ClusterName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to begin HCP cluster deletion")
			_, err = delPoller.PollUntilDone(delCtx, &runtime.PollUntilDoneOptions{
				Frequency: framework.StandardPollInterval,
			})
			Expect(err).NotTo(HaveOccurred(), "failed to poll HCP cluster deletion to completion")

			Expect(validator.mismatches).To(BeEmpty(), "x-ms-served-by header mismatches detected")
			Expect(validator.requestCount).To(BeNumerically(">", 0), "expected at least one HCP API request")
		},
		Entry("MISE v2 when x-ms-mise-version header is set", "mise-v2-smoke", "v2", "v2"),
		Entry("default route returns no version header", "mise-default-smoke", "", ""),
	)
})
