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
	"net/http"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

// miseV2HeaderPolicy injects the x-ms-mise-version: v2 request header
// so that the Istio VirtualService routes to the MISE v2 frontend.
type miseV2HeaderPolicy struct {
	version string
}

func (p *miseV2HeaderPolicy) Do(req *policy.Request) (*http.Response, error) {
	req.Raw().Header.Set("x-ms-mise-version", p.version)
	return req.Next()
}

// miseVersionCapture captures the x-ms-served-by response header set by the
// VirtualService to verify which MISE version handled the request.
type miseVersionCapture struct {
	version string
}

func (p *miseVersionCapture) Do(req *policy.Request) (*http.Response, error) {
	resp, err := req.Next()
	if resp != nil {
		p.version = resp.Header.Get("x-ms-served-by")
	}
	return resp, err
}

// Tests the VirtualService routes to the correct frontend instance based on request headers.
// In INT and above, this exercises MISE-backed routing. In dev/prow environments, the same
// VirtualService is deployed but fronts non-MISE frontend instances. PR checks connect
// through the Istio ingress gateway (not port-forwarded), so the VirtualService routing
// rules are always evaluated.
var _ = Describe("MISE Routing", func() {
	defer GinkgoRecover()

	BeforeEach(func() {
		suiteName := strings.ToLower(os.Getenv("ARO_HCP_SUITE_NAME"))
		if strings.Contains(suiteName, "prod") || strings.Contains(suiteName, "stage") {
			Skip("Skipping MISE routing test in production or staging environment")
		}
	})

	DescribeTable("routes to the correct frontend based on version header",
		labels.RequireNothing,
		labels.AroRpApiCompatible,
		labels.Low,
		labels.Positive,
		func(ctx context.Context, rgPrefix string, miseVersionHeader string, expectedVersion string) {
			tc := framework.NewTestContext()

			By("Creating resource group")
			rg, err := tc.NewResourceGroup(ctx, rgPrefix, tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("Building client factory")
			capture := &miseVersionCapture{}
			policies := []policy.Policy{capture}
			if miseVersionHeader != "" {
				policies = append([]policy.Policy{&miseV2HeaderPolicy{version: miseVersionHeader}}, policies...)
			}
			clientFactory, err := tc.Get20251223ClientFactoryWithPolicies(ctx, policies...)
			Expect(err).NotTo(HaveOccurred())

			By("Listing clusters")
			pager := clientFactory.NewHcpOpenShiftClustersClient().NewListByResourceGroupPager(*rg.Name, nil)
			_, err = pager.NextPage(ctx)
			Expect(err).NotTo(HaveOccurred())

			Expect(capture.version).To(Equal(expectedVersion))
		},
		Entry("MISE v2 when x-ms-mise-version header is set", "mise-v2-smoke", "v2", "v2"),
		Entry("default route returns no version header", "mise-default-smoke", "", ""),
	)
})
