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
package e2e

import (
	"context"
	"crypto/tls"
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

// ----------------- tiny helpers (unique names) -----------------

func eaDelHTTPClient() *http.Client {
	insecure := strings.EqualFold(os.Getenv("INSECURE_SKIP_TLS"), "true")
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure},
	}
	return &http.Client{Transport: tr, Timeout: 60 * time.Second}
}

func eaDelWriteOut(base string, hdr http.Header, body []byte) {
	outDir := "test/e2e/out"
	_ = os.MkdirAll(outDir, 0o755)

	// headers
	hm := map[string][]string{}
	for k, v := range hdr {
		hm[k] = v
	}
	hb, _ := json.MarshalIndent(hm, "", "  ")
	_ = os.WriteFile(filepath.Join(outDir, base+".headers.json"), hb, 0o644)

	// body
	_ = os.WriteFile(filepath.Join(outDir, base+".body.json"), body, 0o644)
}

func eaDelARMBase() string {
	// Default public ARM; override in INT/STAGE via env.
	if b := strings.TrimSpace(os.Getenv("ARM_BASE_URL")); b != "" {
		return strings.TrimRight(b, "/")
	}
	return "https://management.azure.com"
}

func eaDelAPIVersion() string {
	if v := strings.TrimSpace(os.Getenv("ARM_API_VERSION")); v != "" {
		return v
	}
	return "2024-06-10-preview"
}

// ----------------- TEST -----------------

// Deletes the ExternalAuth via ARM (management) endpoint.
// Optionally deletes the Entra app (via Graph) if ENTRA_APP_OBJECT_ID + GRAPH_BEARER_TOKEN are provided.
//
// Required env:
//
//	SUBSCRIPTION_ID
//	RESOURCE_GROUP
//	CLUSTER_NAME
//
// Optional env:
//
//	EXTERNAL_AUTH_ID            (default: "entra")
//	ARM_BASE_URL                (default: https://management.azure.com ; set to INT/STAGE base as needed)
//	ARM_API_VERSION             (default: 2024-06-10-preview)
//	ARM_BEARER_TOKEN            (token for ARM; if omitted, dev headers still sent but most envs require it)
//	INSECURE_SKIP_TLS=true      (dev/local)
//
// Optional Entra-app deletion:
//
//	ENTRA_APP_OBJECT_ID         (if set, we try to delete the Entra app)
//	GRAPH_BEARER_TOKEN          (Graph token; required to actually delete the app)
//
// Outputs:
//
//	test/e2e/out/external_auth_delete_arm.*
//	test/e2e/out/external_auth_verify_get.*
//	test/e2e/out/external_auth_graph_delete.* (if Entra app deletion attempted)
var _ = Describe("ExternalEntra: delete ExternalAuth (ARM) and optional Entra app", labels.RequireNothing, labels.Critical, labels.Positive, func() {
	It("deletes ExternalAuth via ARM endpoint and verifies; optionally deletes Entra app", func(ctx context.Context) {
		subID := strings.TrimSpace(os.Getenv("SUBSCRIPTION_ID"))
		rg := strings.TrimSpace(os.Getenv("RESOURCE_GROUP"))
		cluster := strings.TrimSpace(os.Getenv("CLUSTER_NAME"))
		Expect(subID).NotTo(BeEmpty(), "set SUBSCRIPTION_ID")
		Expect(rg).NotTo(BeEmpty(), "set RESOURCE_GROUP")
		Expect(cluster).NotTo(BeEmpty(), "set CLUSTER_NAME")

		externalAuthID := strings.TrimSpace(os.Getenv("EXTERNAL_AUTH_ID"))
		if externalAuthID == "" {
			externalAuthID = "entra"
		}

		base := eaDelARMBase()
		apiVersion := eaDelAPIVersion()
		armToken := strings.TrimSpace(os.Getenv("ARM_BEARER_TOKEN"))

		// Resource URLs
		deleteURL := fmt.Sprintf("%s/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/%s/externalAuths/%s?api-version=%s",
			base, subID, rg, cluster, externalAuthID, apiVersion)
		getURL := fmt.Sprintf("%s/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/%s/externalAuths/%s?api-version=%s",
			base, subID, rg, cluster, externalAuthID, apiVersion)

		// ---- DELETE via ARM
		By("Deleting ExternalAuth via ARM endpoint")
		{
			req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, deleteURL, nil)
			if armToken != "" {
				req.Header.Set("Authorization", "Bearer "+armToken)
			}
			// Dev convenience headers (ignored by ARM in prod; harmless in dev proxies)
			req.Header.Set("X-Ms-Identity-Url", "https://dummy.identity.azure.net")
			req.Header.Set("X-Ms-Arm-Resource-System-Data",
				fmt.Sprintf(`{"createdBy":"mfreer@redhat.com","createdByType":"User","createdAt":"%s"}`, time.Now().UTC().Format(time.RFC3339)))

			resp, err := eaDelHTTPClient().Do(req)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			eaDelWriteOut("external_auth_delete_arm", resp.Header, b)

			// ARM may return 200/202/204 depending on backend.
			Expect(resp.StatusCode).To(BeElementOf(http.StatusOK, http.StatusNoContent, http.StatusAccepted),
				"DELETE status=%d body=%s", resp.StatusCode, string(b))
		}

		// ---- Verify GET (expect 404 or NoSuchResource)
		By("Verifying ExternalAuth no longer exists (GET)")
		{
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
			if armToken != "" {
				req.Header.Set("Authorization", "Bearer "+armToken)
			}
			req.Header.Set("X-Ms-Identity-Url", "https://dummy.identity.azure.net")
			req.Header.Set("X-Ms-Arm-Resource-System-Data",
				fmt.Sprintf(`{"createdBy":"dev-user","createdByType":"User","createdAt":"%s"}`, time.Now().UTC().Format(time.RFC3339)))

			resp, err := eaDelHTTPClient().Do(req)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			eaDelWriteOut("external_auth_verify_get", resp.Header, b)

			// Expect 404 after delete; tolerate 200 only if provisioning shows deletion underway.
			if resp.StatusCode == http.StatusOK {
				// best-effort check: body should not have a healthy/succeeded resource
				Expect(string(b)).To(Or(
					ContainSubstring(`"provisioningState":"Deleting"`),
					ContainSubstring(`"provisioningState":"Failed"`),
					ContainSubstring(`"error"`),
				), "expected resource to be gone or deleting; got 200 with body=%s", string(b))
			} else {
				Expect(resp.StatusCode).To(Or(Equal(http.StatusNotFound), Equal(http.StatusNoContent), Equal(http.StatusAccepted)),
					"GET after delete status=%d body=%s", resp.StatusCode, string(b))
			}
		}

		// // ---- Optional: delete Entra app (Graph) you will need to set the graph bearer token
		// // appObjectID := strings.TrimSpace(os.Getenv("ENTRA_APP_OBJECT_ID"))
		// // graphToken := strings.TrimSpace(os.Getenv("GRAPH_BEARER_TOKEN"))
		// if appObjectID != "" {
		// 	By("Optionally deleting Entra app (Graph)")
		// 	if graphToken == "" {
		// 		Skip("ENTRA_APP_OBJECT_ID provided but GRAPH_BEARER_TOKEN not set; skipping Entra app deletion")
		// 	}
		// 	graphURL := "https://graph.microsoft.com/v1.0/applications/" + appObjectID
		// 	req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, graphURL, nil)
		// 	req.Header.Set("Authorization", "Bearer "+graphToken)
		// 	resp, err := eaDelHTTPClient().Do(req)
		// 	Expect(err).NotTo(HaveOccurred())
		// 	defer resp.Body.Close()
		// 	b, _ := io.ReadAll(resp.Body)
		// 	eaDelWriteOut("external_auth_graph_delete", resp.Header, b)
		// 	Expect(resp.StatusCode).To(Equal(http.StatusNoContent), "Graph delete status=%d body=%s", resp.StatusCode, string(b))
		// }
	})
})
