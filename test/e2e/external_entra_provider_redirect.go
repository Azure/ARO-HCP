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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("ExternalEntra: publish OIDC config to RP", func() {
	It("builds OIDC config (incl. allow group) and POSTs to RP [step3]",
		labels.ExternalAuth, labels.Integration,
		func(ctx SpecContext) {
			// Load Entra app creds
			secretPath := os.Getenv("ENTRA_E2E_SECRET_PATH")
			if secretPath == "" {
				secretPath = "test/e2e/out/entra_app_secret.json"
			}
			b, err := os.ReadFile(secretPath)
			Expect(err).NotTo(HaveOccurred(), "read %s", secretPath)
			var sf providerEntraSecretOut
			Expect(json.Unmarshal(b, &sf)).To(Succeed())

			// Load group id saved in step 2 (optional)
			var groupID string
			if gb, err := os.ReadFile(filepath.Join("test", "e2e", "out", "entra_group_id.txt")); err == nil {
				groupID = strings.TrimSpace(string(gb))
			}

			const (
				externalAuthID            = "e2e-hypershift-oidc"
				usernameClaim             = "email"
				groupsClaim               = "groups"
				externalAuthComponentName = "console"
				externalAuthComponentNS   = "openshift-console"
			)
			issuerURL := fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", sf.TenantID)
			clientID := sf.ClientID

			var validationRules []map[string]any
			if groupID != "" {
				validationRules = []map[string]any{
					{"claim": "groups", "allowed_values": []string{groupID}},
				}
			}

			payload := map[string]any{
				"id": externalAuthID,
				"issuer": map[string]any{
					"url":       issuerURL,
					"audiences": []string{clientID},
				},
				"claim": map[string]any{
					"mappings": map[string]any{
						"userName": map[string]any{"claim": usernameClaim},
						"groups":   map[string]any{"claim": groupsClaim},
					},
					"validation_rules": validationRules,
				},
				"clients": []map[string]any{
					{
						"component": map[string]any{
							"name":      externalAuthComponentName,
							"namespace": externalAuthComponentNS,
						},
						"id":     clientID,
						"secret": "",
					},
				},
			}

			body, err := json.Marshal(payload)
			Expect(err).NotTo(HaveOccurred())

			clusterName := os.Getenv("HCP_CLUSTER_NAME")
			if clusterName == "" {
				clusterName = "external-auth-cluster"
			}

			rpBase := strings.TrimSpace(os.Getenv("RP_BASE_URL"))
			if rpBase == "" {
				// PF to frontend if not given (dev)
				feNS, err := providerFindServiceNamespace(ctx, "aro-hcp-frontend")
				Expect(err).NotTo(HaveOccurred())
				err = providerWithPortForward(ctx, feNS, "aro-hcp-frontend", 8443, 0, func(baseURL string) {
					postExternalAuth(ctx, baseURL, clusterName, body)
				})
				Expect(err).NotTo(HaveOccurred())
			} else {
				postExternalAuth(ctx, rpBase, clusterName, body)
			}
		})
})

func postExternalAuth(ctx context.Context, baseURL, clusterName string, body []byte) {
	client := providerHTTPClient()
	u := strings.TrimRight(baseURL, "/") + fmt.Sprintf("/api/aro_hcp/v1alpha1/clusters/%s/external_auth_config/external_auths", clusterName)

	// Create/Upsert
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, u, bytesReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer resp.Body.Close()
	Expect(resp.StatusCode).To(BeElementOf(http.StatusCreated, http.StatusOK),
		"unexpected status creating external auth")

	// Verify
	req2, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp2, err := client.Do(req2)
	Expect(err).NotTo(HaveOccurred())
	defer resp2.Body.Close()
	Expect(resp2.StatusCode).To(Equal(http.StatusOK))
	respData, _ := io.ReadAll(resp2.Body)
	Expect(string(respData)).To(ContainSubstring(`"id":"e2e-hypershift-oidc"`))
}

// tiny helper to avoid importing bytes at top-level
func bytesReader(b []byte) io.Reader { return strings.NewReader(string(b)) }
