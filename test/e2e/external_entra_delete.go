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
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/labels"
)

// This test deletes the Entra app instance AND the ExternalAuth config from the RP.
//
// Env vars used:
//   HCP_CLUSTER_NAME         - name of the HCP cluster in RP
//   EXTERNAL_AUTH_ID         - id of the external auth in RP (default: "e2e-hypershift-oidc")
//   ENTRA_APP_OBJECT_ID      - Entra application objectId to delete
//   RP_BASE_URL              - RP frontend base URL (optional; PF used if empty)
//   INSECURE_SKIP_TLS=true   - skip TLS verification in dev
//
// If RP_BASE_URL is not set, we will port-forward to `aro-hcp-frontend` service on 8443.

var _ = Describe("ExternalEntra: delete Entra app + RP config", func() {
	It("deletes Entra app instance, then deletes ExternalAuth from RP",
		labels.ExternalAuth, labels.Integration,
		func(ctx SpecContext) {

			// Load inputs
			clusterName := strings.TrimSpace(os.Getenv("HCP_CLUSTER_NAME"))
			if clusterName == "" {
				clusterName = "external-auth-cluster"
			}
			externalAuthID := strings.TrimSpace(os.Getenv("EXTERNAL_AUTH_ID"))
			if externalAuthID == "" {
				externalAuthID = "e2e-hypershift-oidc"
			}
			appObjectID := strings.TrimSpace(os.Getenv("ENTRA_APP_OBJECT_ID"))
			Expect(appObjectID).NotTo(BeEmpty(), "ENTRA_APP_OBJECT_ID must be set to delete Entra app")

			By("Step 1: Delete Entra app instance from Microsoft Entra")
			token, err := providerGraphToken(ctx)
			Expect(err).NotTo(HaveOccurred())

			// DELETE application from Microsoft Graph
			deleteAppURL := "https://graph.microsoft.com/v1.0/applications/" + appObjectID
			err = providerGraphReq[any](ctx, http.MethodDelete, deleteAppURL, token, nil, nil, http.StatusNoContent)
			Expect(err).NotTo(HaveOccurred(), "failed to delete Entra app with objectId %s", appObjectID)

			By("Step 2: Delete ExternalAuth config from RP")
			deleteAndVerify := func(baseURL string) {
				client := providerHTTPClient()
				base := strings.TrimRight(baseURL, "/")
				deleteURL := fmt.Sprintf("%s/api/aro_hcp/v1alpha1/clusters/%s/external_auth_config/external_auths/%s",
					base, clusterName, externalAuthID)
				listURL := fmt.Sprintf("%s/api/aro_hcp/v1alpha1/clusters/%s/external_auth_config/external_auths",
					base, clusterName)

				// DELETE
				req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, deleteURL, nil)
				resp, err := client.Do(req)
				Expect(err).NotTo(HaveOccurred())
				defer resp.Body.Close()
				Expect(resp.StatusCode).To(BeElementOf(http.StatusOK, http.StatusNoContent, http.StatusAccepted),
					"unexpected status deleting external auth")

				// Verify it no longer exists
				req2, _ := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
				resp2, err := client.Do(req2)
				Expect(err).NotTo(HaveOccurred())
				defer resp2.Body.Close()
				Expect(resp2.StatusCode).To(Equal(http.StatusOK))
				body, _ := io.ReadAll(resp2.Body)
				Expect(string(body)).NotTo(ContainSubstring(`"id":"` + externalAuthID + `"`))
			}

			if rp := strings.TrimSpace(os.Getenv("RP_BASE_URL")); rp != "" {
				deleteAndVerify(rp)
			} else {
				ns, err := providerFindServiceNamespace(ctx, "aro-hcp-frontend")
				Expect(err).NotTo(HaveOccurred())
				err = providerWithPortForward(ctx, ns, "aro-hcp-frontend", 8443, 0, func(baseURL string) {
					deleteAndVerify(baseURL)
				})
				Expect(err).NotTo(HaveOccurred())
			}
		})
})
