// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
// Copyright 2025 Microsoft
// Licensed under the Apache License, Version 2.0.

// Copyright 2025 Microsoft
// SPDX-License-Identifier: MIT
// test/e2e/external_auth_negative.go
package e2e

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	gen "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

/*
Required env:
  SUBSCRIPTION_ID
  RESOURCE_GROUP
  CLUSTER_NAME

Also required per test:
  - For "bad tenant": ENTRA_CLIENT_ID (a syntactically valid GUID to use as audience)
  - For "bad client": ENTRA_TENANT_ID (a valid tenant to build issuer URL)

No RP port-forwarding here—we go through ARM using the generated SDK.
*/

var _ = Describe("ExternalAuth (negative): invalid Entra IDs via ARM SDK", labels.RequireNothing, labels.Critical, labels.Negative, func() {
	It("rejects ExternalAuth when issuer tenant ID is invalid", func(ctx context.Context) {
		subID := strings.TrimSpace(getenvOrFail("SUBSCRIPTION_ID"))
		rg := strings.TrimSpace(getenvOrFail("RESOURCE_GROUP"))
		cluster := strings.TrimSpace(getenvOrFail("CLUSTER_NAME"))
		validClientID := strings.TrimSpace(getenvOrFail("ENTRA_CLIENT_ID")) // used as audience

		// Completely bogus tenant GUID (syntactically OK, not real)
		const badTenant = "11111111-2222-3333-4444-555555555555"

		tc := framework.NewTestContext()
		client := tc.Get20240610ClientFactoryOrDie(ctx).NewExternalAuthsClient()

		req := gen.ExternalAuth{
			Properties: &gen.ExternalAuthProperties{
				Issuer: &gen.ExternalAuthIssuer{
					URL:       to.Ptr(fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", badTenant)),
					Audiences: []*string{to.Ptr(validClientID)},
				},
				Claim: &gen.ExternalAuthClaim{
					Mappings: &gen.ExternalAuthClaimMappings{
						Username: &gen.ExternalAuthClaimMap{Claim: to.Ptr("email")},
					},
				},
				Clients: []*gen.ExternalAuthClient{
					{
						ClientID: to.Ptr(validClientID),
						Component: &gen.ExternalAuthClientComponent{
							Name:                to.Ptr("console"),
							AuthClientNamespace: to.Ptr("openshift-console"),
						},
						Type: to.Ptr(gen.ExternalAuthClientTypePublic),
					},
				},
			},
		}

		poller, err := client.BeginCreateOrUpdate(ctx, subID, rg, cluster, req, nil)
		// Two possible behaviors:
		// 1) ARM rejects immediately with a 4xx surfaced as err
		// 2) Accepted -> async validation -> provisioningState Failed
		if err != nil {
			// Immediate validation failure path: message should mention tenant
			Expect(strings.ToLower(err.Error())).To(ContainSubstring("tenant"),
				"expected an immediate validation error mentioning tenant; got: %v", err)
			return
		}

		res, pollErr := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: 5 * time.Second})
		// On async failure, the SDK returns an error; ensure it complains about tenant
		Expect(pollErr).To(HaveOccurred(), "expected async failure for bad tenant; got success: %#v", res)
		Expect(strings.ToLower(pollErr.Error())).To(ContainSubstring("tenant"),
			"expected failure to mention tenant; got: %v", pollErr)
	})

	It("rejects ExternalAuth when audience (client ID) is invalid", func(ctx context.Context) {
		subID := strings.TrimSpace(getenvOrFail("SUBSCRIPTION_ID"))
		rg := strings.TrimSpace(getenvOrFail("RESOURCE_GROUP"))
		cluster := strings.TrimSpace(getenvOrFail("CLUSTER_NAME"))
		tenantID := strings.TrimSpace(getenvOrFail("ENTRA_TENANT_ID"))

		// Bogus client GUID (syntactically OK, not registered in tenant)
		const badClient = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

		tc := framework.NewTestContext()
		client := tc.Get20240610ClientFactoryOrDie(ctx).NewExternalAuthsClient()

		req := gen.ExternalAuth{
			Properties: &gen.ExternalAuthProperties{
				Issuer: &gen.ExternalAuthIssuer{
					URL:       to.Ptr(fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", tenantID)),
					Audiences: []*string{to.Ptr(badClient)},
				},
				Claim: &gen.ExternalAuthClaim{
					Mappings: &gen.ExternalAuthClaimMappings{
						Username: &gen.ExternalAuthClaimMap{Claim: to.Ptr("email")},
					},
				},
				Clients: []*gen.ExternalAuthClient{
					{
						ClientID: to.Ptr(badClient),
						Component: &gen.ExternalAuthClientComponent{
							Name:                to.Ptr("console"),
							AuthClientNamespace: to.Ptr("openshift-console"),
						},
						Type: to.Ptr(gen.ExternalAuthClientTypePublic),
					},
				},
			},
		}

		poller, err := client.BeginCreateOrUpdate(ctx, subID, rg, cluster, req, nil)
		if err != nil {
			// Immediate validation failure path: message should mention client/audience
			lc := strings.ToLower(err.Error())
			Expect(lc).To(SatisfyAny(
				ContainSubstring("client"),
				ContainSubstring("audience"),
			), "expected an error mentioning client/audience; got: %v", err)
			return
		}

		res, pollErr := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: 5 * time.Second})
		Expect(pollErr).To(HaveOccurred(), "expected async failure for bad client; got success: %#v", res)
		lc := strings.ToLower(pollErr.Error())
		Expect(lc).To(SatisfyAny(
			ContainSubstring("client"),
			ContainSubstring("audience"),
		), "expected failure to mention client/audience; got: %v", pollErr)
	})
})

// small helper to fail fast on missing envs while keeping the test readable
func getenvOrFail(k string) string {
	v := strings.TrimSpace(os.Getenv(k))
	Expect(v).NotTo(BeEmpty(), "missing required env %s", k)
	return v
}
