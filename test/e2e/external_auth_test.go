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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ExternalAuth via RP Frontend", Label("external-auth"), func() {
	var (
		baseURL        = "http://localhost:8443"
		subscriptionID string
		resourceGroup  string
		clusterName    string
		providerID     = "e2e-entra"
		apiVersion     = "2024-06-10-preview"
		issuerURL      = "https://login.microsoftonline.com/<tenant-id>/v2.0"
		clientID       = "<client-id>"
	)

	BeforeEach(func() {
		subscriptionID = os.Getenv("CUSTOMER_SUBSCRIPTION")
		Expect(subscriptionID).ToNot(BeEmpty(), "CUSTOMER_SUBSCRIPTION must be set")

		resourceGroup = os.Getenv("RESOURCE_GROUP")
		Expect(resourceGroup).ToNot(BeEmpty(), "RESOURCE_GROUP must be set")

		clusterName = os.Getenv("CLUSTER_NAME")
		Expect(clusterName).ToNot(BeEmpty(), "CLUSTER_NAME must be set")

		if val := os.Getenv("OIDC_ISSUER_URL"); val != "" {
			issuerURL = val
		}
		if val := os.Getenv("OIDC_CLIENT_ID"); val != "" {
			clientID = val
		}
	})

	It("should create, get, list and delete an ExternalAuth config via RP API", func() {
		resourceURL := fmt.Sprintf("%s/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/%s/externalAuths/%s?api-version=%s",
			baseURL, subscriptionID, resourceGroup, clusterName, providerID, apiVersion)

		payload := map[string]interface{}{
			"properties": map[string]interface{}{
				"issuer": map[string]interface{}{
					"url":       issuerURL,
					"audiences": []string{clientID},
				},
				"claimMappings": map[string]interface{}{
					"username": map[string]interface{}{"claim": "email"},
					"groups":   map[string]interface{}{"claim": "groups"},
				},
			},
		}

		By("Creating ExternalAuth config")
		body, _ := json.Marshal(payload)
		req, _ := http.NewRequest("PUT", resourceURL, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Ms-Identity-Url", "https://dummy.identity.azure.net")
		req.Header.Set("X-Ms-Arm-Resource-System-Data",
			fmt.Sprintf(`{"createdBy": "e2e","createdByType":"User","createdAt":"%s"}`, time.Now().UTC().Format(time.RFC3339)))

		resp, err := http.DefaultClient.Do(req)
		Expect(err).ToNot(HaveOccurred())
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(GinkgoWriter, "PUT response body: %s\n", respBody)
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		By("Fetching the created ExternalAuth config")
		Eventually(func() int {
			respGet, err := http.Get(resourceURL)
			Expect(err).ToNot(HaveOccurred())
			defer respGet.Body.Close()
			return respGet.StatusCode
		}, 10*time.Second, 1*time.Second).Should(Equal(http.StatusOK))

		By("Listing ExternalAuth configs")
		listURL := fmt.Sprintf("%s/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/%s/externalAuths?api-version=%s",
			baseURL, subscriptionID, resourceGroup, clusterName, apiVersion)
		respList, err := http.Get(listURL)
		Expect(err).ToNot(HaveOccurred())
		Expect(respList.StatusCode).To(Equal(http.StatusOK))
		defer respList.Body.Close()

		By("Deleting the ExternalAuth config")
		reqDel, _ := http.NewRequest("DELETE", resourceURL, nil)
		reqDel.Header.Set("X-Ms-Identity-Url", "https://dummy.identity.azure.net")
		reqDel.Header.Set("X-Ms-Arm-Resource-System-Data",
			fmt.Sprintf(`{"createdBy": "e2e","createdByType":"User","createdAt":"%s"}`, time.Now().UTC().Format(time.RFC3339)))
		respDel, err := http.DefaultClient.Do(reqDel)
		Expect(err).ToNot(HaveOccurred())
		Expect(respDel.StatusCode).To(Equal(http.StatusNoContent))
		defer respDel.Body.Close()
	})
})
