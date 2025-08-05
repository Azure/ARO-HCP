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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/test/util/labels"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("External Auth API Access", Label(labels.Integration, labels.ExternalAuth), func() {
	var (
		ctx          context.Context
		cancel       context.CancelFunc
		clientID     string
		clientSecret string
		tenantID     string
		scope        string
		apiURL       string
	)

	BeforeEach(func() {
		ctx, cancel = context.WithTimeout(context.Background(), 2*time.Minute)

		clientID = os.Getenv("AZURE_CLIENT_ID")
		clientSecret = os.Getenv("AZURE_CLIENT_SECRET")
		tenantID = os.Getenv("AZURE_TENANT_ID")
		scope = os.Getenv("AZURE_SCOPE")                  // typically "api://<resource-app-id>/.default"
		apiURL = os.Getenv("EXTERNAL_AUTH_TEST_ENDPOINT") // e.g., https://<cluster-api>/api/ping

		Expect(clientID).ToNot(BeEmpty())
		Expect(clientSecret).ToNot(BeEmpty())
		Expect(tenantID).ToNot(BeEmpty())
		Expect(scope).ToNot(BeEmpty())
		Expect(apiURL).ToNot(BeEmpty())
	})

	AfterEach(func() {
		cancel()
	})

	It("should acquire token and call protected cluster API", func() {
		authURL := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", tenantID)

		By("Fetching a token from Microsoft Entra ID")
		body := fmt.Sprintf(
			"grant_type=client_credentials&client_id=%s&client_secret=%s&scope=%s",
			clientID, clientSecret, scope,
		)

		req, err := http.NewRequestWithContext(ctx, "POST", authURL, strings.NewReader(body))
		Expect(err).ToNot(HaveOccurred())
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		resp, err := http.DefaultClient.Do(req)
		Expect(err).ToNot(HaveOccurred())
		defer resp.Body.Close()

		Expect(resp.StatusCode).To(Equal(200))

		var tokenResp struct {
			AccessToken string `json:"access_token"`
		}
		err = json.NewDecoder(resp.Body).Decode(&tokenResp)
		Expect(err).ToNot(HaveOccurred())
		Expect(tokenResp.AccessToken).ToNot(BeEmpty())

		By("Calling the protected API with bearer token")
		req2, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
		Expect(err).ToNot(HaveOccurred())
		req2.Header.Set("Authorization", "Bearer "+tokenResp.AccessToken)

		resp2, err := http.DefaultClient.Do(req2)
		Expect(err).ToNot(HaveOccurred())
		defer resp2.Body.Close()

		Expect(resp2.StatusCode).To(Equal(200))

		bodyBytes, err := io.ReadAll(resp2.Body)
		Expect(err).ToNot(HaveOccurred())

		var parsed map[string]interface{}
		err = json.Unmarshal(bodyBytes, &parsed)
		Expect(err).ToNot(HaveOccurred())

		// Validate a sample field (adjust depending on your endpoint)
		Expect(parsed).To(HaveKey("cluster_status"))
		Expect(parsed["cluster_status"]).To(Equal("healthy"))
	})
})
