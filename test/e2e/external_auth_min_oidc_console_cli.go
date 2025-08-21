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

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"

	"k8s.io/client-go/rest"
	clientcmd "k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

const eaOut = "test/e2e/out"

// -------- helpers (unique names) --------

func eoMustWrite(path string, b []byte) {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	Expect(os.WriteFile(path, b, 0o644)).To(Succeed())
}

func eoHTTPClient() *http.Client {
	// allow skipping TLS for dev RP if using https + self-signed
	insecure := strings.EqualFold(os.Getenv("INSECURE_SKIP_TLS"), "true")
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure}}
	return &http.Client{Transport: tr, Timeout: 45 * time.Second}
}

func eoARMToken(ctx context.Context) (string, error) {
	if t := strings.TrimSpace(os.Getenv("ARM_BEARER_TOKEN")); t != "" {
		return t, nil
	}
	cmd := exec.CommandContext(ctx, "az", "account", "get-access-token",
		"--resource=https://management.azure.com/", "--query", "accessToken", "-o", "tsv")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("az get ARM token: %v: %s", err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

func eoBreakglass(ctx context.Context, rg, cluster string, timeout time.Duration) *rest.Config {
	rc, err := framework.GetAdminRESTConfigForHCPCluster(
		ctx,
		framework.NewTestContext().Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
		rg, cluster, timeout,
	)
	Expect(err).NotTo(HaveOccurred())
	return rc
}

func eoWriteKubeconfig(path string, rc *rest.Config) {
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters["hc"] = &clientcmdapi.Cluster{
		Server:                   rc.Host,
		InsecureSkipTLSVerify:    rc.Insecure,
		CertificateAuthorityData: rc.CAData,
	}
	cfg.AuthInfos["hc-admin"] = &clientcmdapi.AuthInfo{
		Token:                 rc.BearerToken,
		ClientCertificateData: rc.CertData,
		ClientKeyData:         rc.KeyData,
	}
	cfg.Contexts["ctx"] = &clientcmdapi.Context{Cluster: "hc", AuthInfo: "hc-admin"}
	cfg.CurrentContext = "ctx"
	Expect(clientcmd.WriteToFile(*cfg, path)).To(Succeed())
}

func eoRun(ctx context.Context, name string, args ...string) (string, error) {
	if name == "kubectl" || name == "oc" {
		args = append(args, "--insecure-skip-tls-verify")
	}
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

var _ = Describe("ExternalAuth minimal OIDC (ARM, console+cli clients)", labels.RequireNothing, labels.Critical, labels.Positive, func() {
	It("applies minimal OIDC (PUT via ARM), optionally stores secret in cluster, and verifies", func(ctx context.Context) {
		subID := strings.TrimSpace(os.Getenv("SUBSCRIPTION_ID"))
		rg := strings.TrimSpace(os.Getenv("RESOURCE_GROUP"))
		cluster := strings.TrimSpace(os.Getenv("CLUSTER_NAME"))

		tenantID := strings.TrimSpace(os.Getenv("ENTRA_TENANT_ID"))
		clientID := strings.TrimSpace(os.Getenv("ENTRA_CLIENT_ID"))
		clientSecret := strings.TrimSpace(os.Getenv("ENTRA_CLIENT_SECRET"))

		externalAuthName := strings.TrimSpace(os.Getenv("EXTERNAL_AUTH_NAME"))
		if externalAuthName == "" {
			externalAuthName = "entra"
		}
		apiVersion := strings.TrimSpace(os.Getenv("RP_API_VERSION"))
		if apiVersion == "" {
			apiVersion = "2024-06-10-preview"
		}

		Expect(subID).NotTo(BeEmpty(), "SUBSCRIPTION_ID")
		Expect(rg).NotTo(BeEmpty(), "RESOURCE_GROUP")
		Expect(cluster).NotTo(BeEmpty(), "CLUSTER_NAME")
		Expect(tenantID).NotTo(BeEmpty(), "ENTRA_TENANT_ID")
		Expect(clientID).NotTo(BeEmpty(), "ENTRA_CLIENT_ID")

		// Compose base + path once. The important fix: externalAuths/<EXTERNAL_AUTH_NAME> (e.g. "entra"), NOT cluster name.
		path := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/%s/externalAuths/%s?api-version=%s",
			subID, rg, cluster, externalAuthName, apiVersion)

		// Decide target (RP vs ARM)
		rpBase := strings.TrimSpace(os.Getenv("RP_BASE_URL")) // e.g. http://localhost:8443
		useRP := rpBase != ""

		var url string
		var reqHeaders http.Header = make(http.Header)
		if useRP {
			url = strings.TrimRight(rpBase, "/") + path
			// Dev RP accepts these ARM-like headers:
			reqHeaders.Set("X-Ms-Arm-Resource-System-Data",
				fmt.Sprintf(`{"createdBy":"e2e","createdByType":"User","createdAt":"%s"}`, time.Now().UTC().Format(time.RFC3339)))
			reqHeaders.Set("X-Ms-Identity-Url", "https://dummy.identity.azure.net")
			if tok := strings.TrimSpace(os.Getenv("RP_BEARER_TOKEN")); tok != "" {
				reqHeaders.Set("Authorization", "Bearer "+tok)
			}
		} else {
			url = "https://management.azure.com" + path
			tok, err := eoARMToken(ctx)
			Expect(err).NotTo(HaveOccurred())
			reqHeaders.Set("Authorization", "Bearer "+tok)
		}

		// 1) PUT minimal ExternalAuth (console confidential + cli public)
		By("PUT minimal ExternalAuth")
		putBody := map[string]any{
			"properties": map[string]any{
				"issuer": map[string]any{
					"url":       fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", tenantID),
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
						"type": "confidential",
					},
					{
						"clientId": clientID,
						"component": map[string]any{
							"name":                "cli",
							"authClientNamespace": "openshift-console",
						},
						"type": "public",
					},
				},
			},
		}
		bodyJSON, _ := json.Marshal(putBody)

		req, _ := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(bodyJSON))
		for k, vs := range reqHeaders {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := eoHTTPClient().Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)

		_ = os.MkdirAll(eaOut, 0o755)
		eoMustWrite(filepath.Join(eaOut, "external_auth_put.body.json"), respBody)
		eoMustWrite(filepath.Join(eaOut, "external_auth_put.status.txt"), []byte(fmt.Sprintf("%d", resp.StatusCode)))

		Expect(resp.StatusCode).To(BeElementOf(http.StatusOK, http.StatusCreated, http.StatusAccepted),
			"PUT %s status=%d body=%s", url, resp.StatusCode, string(respBody))

		// 2) Optional: write confidential client secret into the hosted cluster
		if clientSecret != "" {
			By("breakglass -> create openshift-config/oidc-client-secret")
			rc := eoBreakglass(ctx, rg, cluster, 10*time.Minute)
			kcPath := filepath.Join(eaOut, "breakglass.kubeconfig")
			eoWriteKubeconfig(kcPath, rc)
			Expect(os.Setenv("KUBECONFIG", kcPath)).To(Succeed())

			// ensure ns exists + create/replace secret
			_, err = eoRun(ctx, "kubectl", "get", "ns", "openshift-config", "-o", "name")
			Expect(err).NotTo(HaveOccurred())
			_, _ = eoRun(ctx, "kubectl", "-n", "openshift-config", "delete", "secret", "oidc-client-secret", "--ignore-not-found")
			_, err = eoRun(ctx, "kubectl", "-n", "openshift-config", "create", "secret", "generic", "oidc-client-secret",
				"--from-literal", "clientSecret="+clientSecret)
			Expect(err).NotTo(HaveOccurred())
		}

		// 3) GET to verify
		By("GET verify ExternalAuth")
		getReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		for k, vs := range reqHeaders {
			for _, v := range vs {
				getReq.Header.Add(k, v)
			}
		}
		getResp, err := eoHTTPClient().Do(getReq)
		Expect(err).NotTo(HaveOccurred())
		defer getResp.Body.Close()
		getBody, _ := io.ReadAll(getResp.Body)
		eoMustWrite(filepath.Join(eaOut, "external_auth_get.body.json"), getBody)
		eoMustWrite(filepath.Join(eaOut, "external_auth_get.status.txt"), []byte(fmt.Sprintf("%d", getResp.StatusCode)))
		Expect(getResp.StatusCode).To(Equal(http.StatusOK), "GET %s status=%d body=%s", url, getResp.StatusCode, string(getBody))

		Expect(string(getBody)).To(ContainSubstring(`"name":"` + externalAuthName + `"`))
		Expect(string(getBody)).To(ContainSubstring(`"type":"Microsoft.RedHatOpenShift/hcpOpenShiftClusters/externalAuths"`))
	})
})
