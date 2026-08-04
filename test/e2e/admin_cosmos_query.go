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
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("CosmosDB Query", func() {
	It("should return results for a valid SELECT query against the Resources container",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.CoreInfraService,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			tc := framework.NewTestContext()

			By("resolving current Azure identity")
			currentIdentity, err := tc.GetCurrentAzureIdentityDetails(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to get current Azure identity details")

			By("querying the Resources container for any documents")
			resp, statusCode, err := tc.CosmosQuery(ctx, framework.CosmosQueryRequest{
				ContainerName: "Resources",
				Query:         "SELECT * FROM c WHERE LENGTH(c.resourceID) > 0",
				MaxItems:      10,
			}, currentIdentity)
			Expect(err).NotTo(HaveOccurred(), "cosmos query request failed")
			Expect(statusCode).To(Equal(http.StatusOK), "expected 200 OK from cosmos query")
			Expect(resp.Count).To(BeNumerically(">", 0), "expected at least one document in Resources container")
			Expect(resp.Results).To(HaveLen(resp.Count), "results length should match count")
		},
	)

	It("should reject a query containing mutating keywords",
		labels.RequireNothing,
		labels.High,
		labels.Negative,
		labels.CoreInfraService,
		func(ctx context.Context) {
			tc := framework.NewTestContext()

			By("resolving current Azure identity")
			currentIdentity, err := tc.GetCurrentAzureIdentityDetails(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to get current Azure identity details")

			By("sending a query with a DELETE keyword")
			_, statusCode, err := tc.CosmosQueryRaw(ctx, framework.CosmosQueryRequest{
				ContainerName: "Resources",
				Query:         "DELETE FROM c WHERE c.id = 'test'",
			}, currentIdentity)
			Expect(err).NotTo(HaveOccurred(), "request itself should not fail")
			Expect(statusCode).To(Equal(http.StatusBadRequest), "expected 400 Bad Request for mutating query")
		},
	)

	It("should return results when querying with a partition key",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.CoreInfraService,
		func(ctx context.Context) {
			tc := framework.NewTestContext()

			By("resolving current Azure identity")
			currentIdentity, err := tc.GetCurrentAzureIdentityDetails(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to get current Azure identity details")

			By("first querying without partition key to discover a subscription ID")
			initialResp, _, err := tc.CosmosQuery(ctx, framework.CosmosQueryRequest{
				ContainerName: "Resources",
				Query:         "SELECT c.partitionKey FROM c WHERE LENGTH(c.partitionKey) > 0",
				MaxItems:      1,
			}, currentIdentity)
			Expect(err).NotTo(HaveOccurred(), "initial cosmos query failed")
			Expect(initialResp.Count).To(BeNumerically(">", 0), "expected at least one document to discover a partition key")

			By("querying with the discovered partition key")
			resp, statusCode, err := tc.CosmosQuery(ctx, framework.CosmosQueryRequest{
				ContainerName: "Fleet",
				Query:         "SELECT * FROM c",
				MaxItems:      5,
			}, currentIdentity)
			Expect(err).NotTo(HaveOccurred(), "partitioned cosmos query failed")
			Expect(statusCode).To(Equal(http.StatusOK), "expected 200 OK from partitioned query")
			Expect(resp.Count).To(BeNumerically(">=", 0), "count should be non-negative")
		},
	)

	It("should return an empty result set for a query matching no documents",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.CoreInfraService,
		func(ctx context.Context) {
			tc := framework.NewTestContext()

			By("resolving current Azure identity")
			currentIdentity, err := tc.GetCurrentAzureIdentityDetails(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to get current Azure identity details")

			By("querying for a non-existent resource ID")
			resp, statusCode, err := tc.CosmosQuery(ctx, framework.CosmosQueryRequest{
				ContainerName: "Resources",
				Query:         "SELECT * FROM c WHERE c.id = 'this-id-definitely-does-not-exist-e2e-test'",
				MaxItems:      10,
			}, currentIdentity)
			Expect(err).NotTo(HaveOccurred(), "cosmos query request failed")
			Expect(statusCode).To(Equal(http.StatusOK), "expected 200 OK even with no results")
			Expect(resp.Count).To(Equal(0), "expected zero results for non-existent ID")
			Expect(resp.Results).To(BeEmpty(), "results should be empty")
		},
	)

})
