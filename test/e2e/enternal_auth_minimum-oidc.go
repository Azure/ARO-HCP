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
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/labels"
)

// -------- tiny, unique helpers --------
func ea4HTTP() *http.Client { return &http.Client{Timeout: 45 * time.Second} }

func ea4WriteOut(base string, hdr http.Header, body []byte) {
	outDir := "test/e2e/out"
	_ = os.MkdirAll(outDir, 0o755)

	hmap := map[string][]string{}
	for k, v := range hdr {
		hmap[k] = v
	}
	hb, _ := json.MarshalIndent(hmap, "", "  ")
	Expect(os.WriteFile(filepath.Join(outDir, base+".headers.json"), hb, 0o644)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(outDir, base+".body.json"), body, 0o644)).To(Succeed())
}

// -------- TEST --------
//
// Env required:
//
//	SUBSCRIPTION_ID, RESOURCE_GROUP, CLUSTER_NAME
//	ENTRA_TENANT_ID, ENTRA_CLIENT_ID     (existing Entra app)
//
// Optional:
//
//	RP_BASE_URL          (default: http://localhost:8443)
//	RP_BEARER_TOKEN      (if your RP expects it)
var _ = Describe("ExternalAuth minimal OIDC (existing Entra app) via RP frontend",
	labels.RequireNothing, labels.Critical, labels.Positive, func() {

		It("applies minimal OIDC to a cluster via RP (PUT) and verifies (GET)", func(ctx context.Context) {
			// Inputs
			subID := strings.TrimSpace(os.Getenv("SUBSCRIPTION_ID"))
			rg := strings.TrimSpace(os.Getenv("RESOURCE_GROUP"))
			cluster := strings.TrimSpace(os.Getenv("CLUSTER_NAME"))
			tenantID := strings.TrimSpace(os.Getenv("ENTRA_TENANT_ID"))
			clientID := strings.TrimSpace(os.Getenv("ENTRA_CLIENT_ID"))
			baseURL := strings.TrimSpace(os.Getenv("RP_BASE_URL"))
			if baseURL == "" {
				baseURL = "https://management.azure.com/"
			}
			bearer := strings.TrimSpace(os.Getenv("RP_BEARER_TOKEN"))

			Expect(subID).NotTo(BeEmpty(), "set SUBSCRIPTION_ID")
			Expect(rg).NotTo(BeEmpty(), "set RESOURCE_GROUP")
			Expect(cluster).NotTo(BeEmpty(), "set CLUSTER_NAME")
			Expect(tenantID).NotTo(BeEmpty(), "set ENTRA_TENANT_ID")
			Expect(clientID).NotTo(BeEmpty(), "set ENTRA_CLIENT_ID")

			const apiVersion = "2024-06-10-preview"
			resourcePath := fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/%s/externalAuths/entra?api-version=%s",
				subID, rg, cluster, apiVersion,
			)

			// ---- PUT minimal OIDC ----
			issuerURL := fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", tenantID)
			minPayload := map[string]any{
				"properties": map[string]any{
					"claim": map[string]any{
						"mappings": map[string]any{
							"username": map[string]any{"claim": "email"},
						},
					},
					"issuer": map[string]any{
						"url":       issuerURL,
						"audiences": []string{clientID},
					},
				},
			}
			putBody, _ := json.Marshal(minPayload)

			putReq, _ := http.NewRequestWithContext(ctx, http.MethodPut, strings.TrimRight(baseURL, "/")+resourcePath, strings.NewReader(string(putBody)))
			putReq.Header.Set("Content-Type", "application/json")
			putReq.Header.Set("X-Ms-Identity-Url", "https://dummy.identity.azure.net")
			putReq.Header.Set("X-Ms-Arm-Resource-System-Data",
				fmt.Sprintf(`{"createdBy":"dev-user","createdByType":"User","createdAt":"%s"}`, time.Now().UTC().Format(time.RFC3339)))
			if bearer != "" {
				putReq.Header.Set("Authorization", "Bearer "+bearer)
			}

			putResp, err := ea4HTTP().Do(putReq)
			Expect(err).NotTo(HaveOccurred())
			defer putResp.Body.Close()
			putRespBody, _ := io.ReadAll(putResp.Body)

			ea4WriteOut("min_oidc_put", putResp.Header, putRespBody)
			Expect(putResp.StatusCode).To(BeElementOf(http.StatusOK, http.StatusCreated),
				"PUT status=%d body=%s", putResp.StatusCode, string(putRespBody))

			// ---- GET verify ----
			getReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+resourcePath, nil)
			getReq.Header.Set("X-Ms-Identity-Url", "https://dummy.identity.azure.net")
			getReq.Header.Set("X-Ms-Arm-Resource-System-Data",
				fmt.Sprintf(`{"createdBy":"dev-user","createdByType":"User","createdAt":"%s"}`, time.Now().UTC().Format(time.RFC3339)))
			if bearer != "" {
				getReq.Header.Set("Authorization", "Bearer "+bearer)
			}

			getResp, err := ea4HTTP().Do(getReq)
			Expect(err).NotTo(HaveOccurred())
			defer getResp.Body.Close()
			getRespBody, _ := io.ReadAll(getResp.Body)

			ea4WriteOut("min_oidc_get", getResp.Header, getRespBody)
			Expect(getResp.StatusCode).To(Equal(http.StatusOK),
				"GET status=%d body=%s", getResp.StatusCode, string(getRespBody))
			Expect(string(getRespBody)).To(ContainSubstring(`"type": "Microsoft.RedHatOpenShift/hcpOpenShiftClusters/externalAuths"`))
			Expect(string(getRespBody)).To(ContainSubstring(`"name": "entra"`))
		})
	})
