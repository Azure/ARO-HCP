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

// --- helpers ---

func eaWriteOut(base string, hdr http.Header, body []byte) error {
	outDir := "test/e2e/out"
	_ = os.MkdirAll(outDir, 0o755)

	hmap := map[string][]string{}
	for k, v := range hdr {
		hmap[k] = v
	}
	hb, _ := json.MarshalIndent(hmap, "", "  ")
	if err := os.WriteFile(filepath.Join(outDir, base+".headers.json"), hb, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, base+".body.json"), body, 0o644)
}

func eaLocalHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

// --- TEST ---

var _ = Describe("ExternalAuth GET (RP smoke, local PF on 8443)", labels.RequireNothing, labels.Critical, labels.Positive, func() {
	It("reads current config via already-forwarded frontend and saves output", func(ctx context.Context) {
		// Env
		subID := strings.TrimSpace(os.Getenv("SUBSCRIPTION_ID"))
		rg := strings.TrimSpace(os.Getenv("RESOURCE_GROUP"))
		cluster := strings.TrimSpace(os.Getenv("CLUSTER_NAME"))
		token := strings.TrimSpace(os.Getenv("RP_BEARER_TOKEN")) // optional

		Expect(subID).NotTo(BeEmpty(), "set SUBSCRIPTION_ID")
		Expect(rg).NotTo(BeEmpty(), "set RESOURCE_GROUP")
		Expect(cluster).NotTo(BeEmpty(), "set CLUSTER_NAME")

		const apiVersion = "2024-06-10-preview"
		baseURL := "https://management.azure.com/"
		path := fmt.Sprintf(
			"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/%s/externalAuths/entra?api-version=%s",
			subID, rg, cluster, apiVersion,
		)

		// Build request
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
		req.Header.Set("X-Ms-Identity-Url", "https://dummy.identity.azure.net")
		req.Header.Set("X-Ms-Arm-Resource-System-Data",
			fmt.Sprintf(`{"createdBy":"dev-user","createdByType":"User","createdAt":"%s"}`, time.Now().UTC().Format(time.RFC3339)))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		// Send
		resp, err := eaLocalHTTPClient().Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		Expect(eaWriteOut("external_auth_get", resp.Header, body)).To(Succeed())

		Expect(resp.StatusCode).To(Equal(http.StatusOK), "status=%d body=%s", resp.StatusCode, string(body))

		// --- Parse JSON and validate fields ---
		var respObj map[string]any
		Expect(json.Unmarshal(body, &respObj)).To(Succeed())

		Expect(respObj["name"]).To(Equal("entra"))
		Expect(respObj["type"]).To(Equal("Microsoft.RedHatOpenShift/hcpOpenShiftClusters/externalAuths"))
	})
})
