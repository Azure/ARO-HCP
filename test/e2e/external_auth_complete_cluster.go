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
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

/*
This test:
1) Creates a resource group + infra (customer NSG/VNet/Subnet)
2) Creates an HCP cluster (injecting MAJOR.MINOR version into the template)
3) Creates a node pool
4) Loads Entra creds JSON (output of Step 1 test) and POSTs ExternalAuth to the RP
*/

// ---------- Local-only Entra secret types/helpers (not shared with Step 1) ----------
type entraSecretFileStep2 struct {
	TenantID        string    `json:"tenant_id"`
	AppObjectID     string    `json:"app_object_id"`
	ClientID        string    `json:"client_id"`
	ClientSecret    string    `json:"client_secret"`
	DisplayName     string    `json:"display_name"`
	CreatedAt       time.Time `json:"created_at"`
	SecretExpiresAt time.Time `json:"secret_expires_at"`
}

func mustLoadEntraSecretStep2(path string) entraSecretFileStep2 {
	b, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred(), "failed to read Entra secret JSON at %s", path)
	var sf entraSecretFileStep2
	Expect(json.Unmarshal(b, &sf)).To(Succeed(), "failed to parse Entra secret JSON at %s", path)
	Expect(sf.TenantID).NotTo(BeEmpty())
	Expect(sf.ClientID).NotTo(BeEmpty())
	return sf
}

// ---------- helpers ----------
func majorMinor(v string) string { // normalize "4.19.0" -> "4.19"
	p := strings.SplitN(v, ".", 3)
	if len(p) >= 2 {
		return p[0] + "." + p[1]
	}
	return v
}

func applyVersionToTemplate(tpl []byte, version string) []byte { // replace placeholder/patch value
	v := majorMinor(version)
	s := string(tpl)
	s = strings.ReplaceAll(s, "VERSION_REPLACE_ME", v)
	// safety if a concrete 4.x.y slipped in:
	s = strings.ReplaceAll(s, `"id": "4.19.0"`, `"id": "`+v+`"`)
	return []byte(s)
}

func httpClientInsecure() *http.Client {
	insecure := true
	if v := os.Getenv("INSECURE_SKIP_TLS"); strings.TrimSpace(strings.ToLower(v)) == "false" {
		insecure = false
	}
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure}, // dev/self-signed
	}
	return &http.Client{Transport: tr, Timeout: 45 * time.Second}
}

var _ = Describe("ExternalAuth Full E2E", func() {
	It("creates a full HCP cluster and applies ExternalAuth config",
		labels.RequireNothing, labels.Critical, labels.Positive, labels.ExternalEntra,
		func(ctx context.Context) {
			tc := framework.NewTestContext()

			const (
				region                 = "uksouth"
				customerNSGName        = "customer-nsg-name"
				customerVnetName       = "customer-vnet-name"
				customerVnetSubnetName = "customer-vnet-subnet1"
				customerClusterName    = "external-auth-cluster"
				customerNodePoolName   = "np-1"

				// ExternalAuth constants (non-secret)
				externalAuthID            = "e2e-hypershift-oidc"
				usernameClaim             = "email"
				groupsClaim               = "groups"
				externalAuthComponentName = "console"
				externalAuthComponentNS   = "openshift-console"
			)

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "external-auth", region)
			Expect(err).NotTo(HaveOccurred())

			By("provisioning infra")
			_, err = framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name, "infra",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/standard-cluster-create/customer-infra.json")),
				map[string]interface{}{
					"customerNsgName":        customerNSGName,
					"customerVnetName":       customerVnetName,
					"customerVnetSubnetName": customerVnetSubnetName,
				}, 45*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			By("creating HCP cluster (injecting MAJOR.MINOR version)")
			rawClusterTpl := framework.Must(
				TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/standard-cluster-create/cluster.json"),
			)
			ver := strings.TrimSpace(os.Getenv("HCP_OPENSHIFT_VERSION"))
			if ver == "" {
				ver = "4.19" // default MAJOR.MINOR required by API
			}
			clusterTpl := applyVersionToTemplate(rawClusterTpl, ver)

			managedRG := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			_, err = framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name, "hcp-cluster",
				clusterTpl,
				map[string]interface{}{
					"nsgName":                  customerNSGName,
					"vnetName":                 customerVnetName,
					"subnetName":               customerVnetSubnetName,
					"clusterName":              customerClusterName,
					"managedResourceGroupName": managedRG,
				}, 45*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			By("creating node pool")
			_, err = framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name, "node-pool",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/standard-cluster-create/nodepool.json")),
				map[string]interface{}{
					"clusterName":  customerClusterName,
					"nodePoolName": customerNodePoolName,
				}, 45*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			By("applying ExternalAuth config via RP (using Entra app from Step 1)")

			// 1) Load Entra creds JSON (local loader)
			secretPath := strings.TrimSpace(os.Getenv("ENTRA_E2E_SECRET_PATH"))
			if secretPath == "" {
				secretPath = "test/e2e/out/entra_app_secret.json"
			}
			sf := mustLoadEntraSecretStep2(secretPath)

			// 2) Build issuer/audience/client from Entra
			issuerURL := fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", sf.TenantID)
			externalAuthClientID := sf.ClientID
			cliAudience := sf.ClientID
			externalAuthClientSecret := ""

			// 3) Build payload
			authPayload := map[string]interface{}{
				"id": externalAuthID,
				"issuer": map[string]interface{}{
					"url":       issuerURL,
					"audiences": []string{cliAudience},
				},
				"claim": map[string]interface{}{
					"mappings": map[string]interface{}{
						"userName": map[string]interface{}{"claim": usernameClaim},
						"groups":   map[string]interface{}{"claim": groupsClaim},
					},
					"validation_rules": []any{},
				},
				"clients": []map[string]interface{}{
					{
						"component": map[string]interface{}{
							"name":      externalAuthComponentName,
							"namespace": externalAuthComponentNS,
						},
						"id":     externalAuthClientID,
						"secret": externalAuthClientSecret,
					},
				},
			}

			body, err := json.Marshal(authPayload)
			Expect(err).NotTo(HaveOccurred())

			// 4) POST to RP
			baseURL := strings.TrimSpace(os.Getenv("RP_BASE_URL"))
			if baseURL == "" {
				baseURL = "https://127.0.0.1:8443"
			}
			rpEndpoint := fmt.Sprintf("%s/api/aro_hcp/v1alpha1/clusters/%s/external_auth_config/external_auths",
				baseURL, customerClusterName)

			req, err := http.NewRequestWithContext(ctx, http.MethodPost, rpEndpoint, bytes.NewReader(body))
			Expect(err).NotTo(HaveOccurred())
			req.Header.Set("Content-Type", "application/json")

			client := httpClientInsecure()
			resp, err := client.Do(req)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(BeElementOf(http.StatusCreated, http.StatusOK),
				"unexpected status creating external auth")

			By("verifying ExternalAuth config was applied")
			verifyReq, err := http.NewRequestWithContext(ctx, http.MethodGet, rpEndpoint, nil)
			Expect(err).NotTo(HaveOccurred())
			verifyResp, err := client.Do(verifyReq)
			Expect(err).NotTo(HaveOccurred())
			defer verifyResp.Body.Close()
			Expect(verifyResp.StatusCode).To(Equal(http.StatusOK))

			respData, err := io.ReadAll(verifyResp.Body)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(respData)).To(ContainSubstring(externalAuthID))
		})
})

