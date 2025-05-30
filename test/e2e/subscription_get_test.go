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
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/HTTPRequest"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Check if Subscriptions for HCPOpenShiftCluster are registered using HTTP GET requests to RP", func() {

	Context("Negative", func() {
		It("Sending GET request for unregistered subscription fails with 404 SubscriptionNotFound", labels.Medium, labels.Negative, func(ctx context.Context) {
			unregisteredSubscription := "00000000-0000-0000-0000-000000000001"
			By("Sending a GET request for the unregistered subscription")
			HTTPClientConfig := HTTPRequest.HTTPRequestConfig{
				Method: "GET",
				URL:    fmt.Sprintf("http://localhost:8443/subscriptions/%s?api-version=2.0", unregisteredSubscription),
			}
			response, err := HTTPRequest.PerformHTTPRequest(HTTPClientConfig)
			Expect(err).To(BeNil())
			Expect(response.StatusCode).To(Equal(404))
			Expect(response.Body).To(ContainSubstring("SubscriptionNotFound"))
		})
		It("Sends Get request for mal-formed subscription ID fails with 400 InvalidSubscriptionID", labels.Medium, labels.Negative, func(ctx context.Context) {
			malformedSubscription := "00000000-0000-0000-0000-000000BADSUB"
			By("Sending a GET request for the mal-formed subscription ID")
			HTTPClientConfig := HTTPRequest.HTTPRequestConfig{
				Method: "GET",
				URL:    fmt.Sprintf("http://localhost:8443/subscriptions/%s?api-version=2.0", malformedSubscription),
			}
			response, err := HTTPRequest.PerformHTTPRequest(HTTPClientConfig)
			Expect(err).To(BeNil())
			Expect(response.StatusCode).To(Equal(400))
			Expect(response.Body).To(ContainSubstring("InvalidSubscriptionID"))
		})
	})
	Context("Positive", func() {
		It("Sends get request for a valid subscription succeeds with 200 Registered", labels.Medium, labels.Positive, func(ctx context.Context) {
			customerSubscriptionID := os.Getenv("CUSTOMER_SUBSCRIPTION")
			By("Sending a GET request for the valid, registred subscription ID")
			HTTPClientConfig := HTTPRequest.HTTPRequestConfig{
				Method: "GET",
				URL:    fmt.Sprintf("http://localhost:8443/subscriptions/%s?api-version=2.0", customerSubscriptionID),
			}
			response, err := HTTPRequest.PerformHTTPRequest(HTTPClientConfig)
			Expect(err).To(BeNil())
			Expect(response.StatusCode).To(Equal(200))
			Expect(response.Body).To(ContainSubstring("Registered"))
		})
	})
})
