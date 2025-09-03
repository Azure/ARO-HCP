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

package e2e

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/labels"
)

const eaBadIDOut = "test/e2e/out"

// ---- tiny helpers (unique prefix: eabadid) ----

func eabadidHTTP() *http.Client {
	insec := strings.EqualFold(os.Getenv("INSECURE_SKIP_TLS"), "true")
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: insec}}
	return &http.Client{Transport: tr, Timeout: 45 * time.Second}
}

func eabadidMustWrite(p string, b []byte) {
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, b, 0o644)
}

func eabadidARMToken(ctx context.Context) (string, error) {
	if t := strings.TrimSpace(os.Getenv("ARM_BEARER_TOKEN")); t != "" {
		return t, nil
	}
	cmd := exec.CommandContext(ctx, "az", "account", "get-access-token",
		"--resource=https://management.azure.com/", "--query", "accessToken", "-o", "tsv")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("az get-access-token: %v: %s", err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

// Build URL + headers for ARM or RP proxy
func eabadidEndpoint(ctx context.Context, path string) (string, http.Header) {
	rp := strings.TrimSpace(os.Getenv("RP_BASE_URL"))
	h := http.Header{}
	if rp != "" {
		u := strings.TrimRight(rp, "/") + path
		h.Set("X-Ms-Arm-Resource-System-Data",
			fmt.Sprintf(`{"createdBy":"e2e-negative","createdByType":"User","createdAt":"%s"}`, time.Now().UTC().Format(time.RFC3339)))
		h.Set("X-Ms-Identity-Url", "https://dummy.identity.azure.net")
		if tok := strings.TrimSpace(os.Getenv("RP_BEARER_TOKEN")); tok != "" {
			h.Set("Authorization", "Bearer "+tok)
		}
		return u, h
	}
	u := "https://management.azure.com" + path
	tok, err := eabadidARMToken(ctx)
	Expect(err).NotTo(HaveOccurred())
	h.Set("Authorization", "Bearer "+tok)
	return u, h
}

func eabadidPUT(ctx context.Context, url string, hdr http.Header, payload any, outPrefix string) (int, []byte) {
	j, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(j))
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := eabadidHTTP().Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	eabadidMustWrite(filepath.Join(eaBadIDOut, outPrefix+".put.status.txt"), []byte(fmt.Sprintf("%d", resp.StatusCode)))
	eabadidMustWrite(filepath.Join(eaBadIDOut, outPrefix+".put.body.json"), b)
	return resp.StatusCode, b
}

func eabadidGET(ctx context.Context, url string, hdr http.Header, outPrefix string) (int, []byte) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := eabadidHTTP().Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	eabadidMustWrite(filepath.Join(eaBadIDOut, outPrefix+".get.status.txt"), []byte(fmt.Sprintf("%d", resp.StatusCode)))
	eabadidMustWrite(filepath.Join(eaBadIDOut, outPrefix+".get.body.json"), b)
	return resp.StatusCode, b
}

// Poll GET for async validation failures
func eabadidWaitForFailure(ctx context.Context, url string, hdr http.Header, outPrefix string, max time.Duration, wantSubstr string) {
	deadline := time.Now().Add(max)
	for {
		code, body := eabadidGET(ctx, url, hdr, outPrefix)
		if code == http.StatusOK && strings.Contains(string(body), `"provisioningState":"Failed"`) {
			Expect(strings.ToLower(string(body))).To(ContainSubstring(strings.ToLower(wantSubstr)),
				"expected failure message to mention %q; body=%s", wantSubstr, string(body))
			return
		}
		if time.Now().After(deadline) {
			Fail(fmt.Sprintf("timed out waiting for provisioningState Failed; last=%d body=%s", code, string(body)))
		}
		time.Sleep(5 * time.Second)
	}
}

// ---------------- TESTS ----------------

/*
Required env:
  SUBSCRIPTION_ID
  RESOURCE_GROUP
  CLUSTER_NAME
  ENTRA_TENANT_ID      (valid tenant for the valid-path test)
  ENTRA_CLIENT_ID      (valid client for the valid-path test)

Optional:
  EXTERNAL_AUTH_NAME   (default: "entra")
  RP_API_VERSION       (default: "2024-06-10-preview")
  RP_BASE_URL, RP_BEARER_TOKEN, INSECURE_SKIP_TLS, ARM_BEARER_TOKEN
*/

var _ = Describe("ExternalAuth negative: invalid Entra IDs", labels.RequireNothing, labels.Critical, labels.Negative, func() {
	// Invalid TENANT test
	It("rejects ExternalAuth when issuer tenant ID is invalid", func(ctx context.Context) {
		subID := strings.TrimSpace(os.Getenv("SUBSCRIPTION_ID"))
		rg := strings.TrimSpace(os.Getenv("RESOURCE_GROUP"))
		cluster := strings.TrimSpace(os.Getenv("CLUSTER_NAME"))
		// we still need a syntactically valid client ID for audiences
		clientID := strings.TrimSpace(os.Getenv("ENTRA_CLIENT_ID"))

		Expect(subID).NotTo(BeEmpty(), "SUBSCRIPTION_ID")
		Expect(rg).NotTo(BeEmpty(), "RESOURCE_GROUP")
		Expect(cluster).NotTo(BeEmpty(), "CLUSTER_NAME")
		Expect(clientID).NotTo(BeEmpty(), "ENTRA_CLIENT_ID")

		externalAuth := strings.TrimSpace(os.Getenv("EXTERNAL_AUTH_NAME"))
		if externalAuth == "" {
			externalAuth = "entra"
		}
		apiVersion := strings.TrimSpace(os.Getenv("RP_API_VERSION"))
		if apiVersion == "" {
			apiVersion = "2024-06-10-preview"
		}

		// Completely bogus tenant GUID
		const badTenant = "11111111-2222-3333-4444-555555555555"

		path := fmt.Sprintf(
			"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/%s/externalAuths/%s?api-version=%s",
			subID, rg, cluster, externalAuth, apiVersion,
		)
		url, hdr := eabadidEndpoint(ctx, path)

		payload := map[string]any{
			"properties": map[string]any{
				"issuer": map[string]any{
					"url":       fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", badTenant),
					"audiences": []string{clientID},
				},
				"claim": map[string]any{
					"mappings": map[string]any{
						"username": map[string]any{"claim": "email"},
					},
				},
				"clients": []map[string]any{
					{
						"clientId": clientID,
						"component": map[string]any{
							"name":                "console",
							"authClientNamespace": "openshift-console",
						},
						"type": "public", // keep it simple; focus is on bad tenant
					},
				},
			},
		}

		code, body := eabadidPUT(ctx, url, hdr, payload, "neg_bad_tenant")
		switch code {
		case http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusNotFound:
			Expect(strings.ToLower(string(body))).To(ContainSubstring("tenant"),
				"expected error about tenant id; body=%s", string(body))
		case http.StatusAccepted:
			// async: wait for Failed on GET
			eabadidWaitForFailure(ctx, url, hdr, "neg_bad_tenant", 5*time.Minute, "tenant")
		default:
			Fail(fmt.Sprintf("unexpected PUT status=%d body=%s", code, string(body)))
		}
	})

	// Invalid CLIENT test
	It("rejects ExternalAuth when audience (client ID) is invalid", func(ctx context.Context) {
		subID := strings.TrimSpace(os.Getenv("SUBSCRIPTION_ID"))
		rg := strings.TrimSpace(os.Getenv("RESOURCE_GROUP"))
		cluster := strings.TrimSpace(os.Getenv("CLUSTER_NAME"))
		tenantID := strings.TrimSpace(os.Getenv("ENTRA_TENANT_ID"))

		Expect(subID).NotTo(BeEmpty(), "SUBSCRIPTION_ID")
		Expect(rg).NotTo(BeEmpty(), "RESOURCE_GROUP")
		Expect(cluster).NotTo(BeEmpty(), "CLUSTER_NAME")
		Expect(tenantID).NotTo(BeEmpty(), "ENTRA_TENANT_ID")

		externalAuth := strings.TrimSpace(os.Getenv("EXTERNAL_AUTH_NAME"))
		if externalAuth == "" {
			externalAuth = "entra"
		}
		apiVersion := strings.TrimSpace(os.Getenv("RP_API_VERSION"))
		if apiVersion == "" {
			apiVersion = "2024-06-10-preview"
		}

		// not valid client GUID for audiences
		const badClient = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

		path := fmt.Sprintf(
			"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/%s/externalAuths/%s?api-version=%s",
			subID, rg, cluster, externalAuth, apiVersion,
		)
		url, hdr := eabadidEndpoint(ctx, path)

		payload := map[string]any{
			"properties": map[string]any{
				"issuer": map[string]any{
					"url":       fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", tenantID),
					"audiences": []string{badClient},
				},
				"claim": map[string]any{
					"mappings": map[string]any{
						"username": map[string]any{"claim": "email"},
					},
				},
				"clients": []map[string]any{
					{
						"clientId": badClient,
						"component": map[string]any{
							"name":                "console",
							"authClientNamespace": "openshift-console",
						},
						"type": "public",
					},
				},
			},
		}

		code, body := eabadidPUT(ctx, url, hdr, payload, "neg_bad_client")
		switch code {
		case http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusNotFound:
			Expect(strings.ToLower(string(body))).To(ContainSubstring("client"),
				"expected error about client id/audience; body=%s", string(body))
		case http.StatusAccepted:
			eabadidWaitForFailure(ctx, url, hdr, "neg_bad_client", 5*time.Minute, "client")
		default:
			Fail(fmt.Sprintf("unexpected PUT status=%d body=%s", code, string(body)))
		}
	})
})
