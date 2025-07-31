//go:build E2Etests

package externalauth

import (
	"bytes"
	"encoding/json"
	"fmt"
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

		// Optionally override issuer/clientID from env
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
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		By("Fetching the created ExternalAuth config")
		respGet, err := http.Get(resourceURL)
		Expect(err).ToNot(HaveOccurred())
		Expect(respGet.StatusCode).To(Equal(http.StatusOK))

		By("Listing ExternalAuth configs")
		listURL := fmt.Sprintf("%s/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/%s/externalAuths?api-version=%s",
			baseURL, subscriptionID, resourceGroup, clusterName, apiVersion)
		respList, err := http.Get(listURL)
		Expect(err).ToNot(HaveOccurred())
		Expect(respList.StatusCode).To(Equal(http.StatusOK))

		By("Deleting the ExternalAuth config")
		reqDel, _ := http.NewRequest("DELETE", resourceURL, nil)
		reqDel.Header.Set("X-Ms-Identity-Url", "https://dummy.identity.azure.net")
		reqDel.Header.Set("X-Ms-Arm-Resource-System-Data",
			fmt.Sprintf(`{"createdBy": "e2e","createdByType":"User","createdAt":"%s"}`, time.Now().UTC().Format(time.RFC3339)))
		respDel, err := http.DefaultClient.Do(reqDel)
		Expect(err).ToNot(HaveOccurred())
		Expect(respDel.StatusCode).To(Equal(http.StatusNoContent))
	})
})
