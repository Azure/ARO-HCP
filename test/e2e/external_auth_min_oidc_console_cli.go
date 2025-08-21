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

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"

	"k8s.io/client-go/rest"
	clientcmd "k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

/*
Env (required)
  SUBSCRIPTION_ID
  RESOURCE_GROUP
  CLUSTER_NAME
  ENTRA_TENANT_ID
  ENTRA_CLIENT_ID

Env (optional)
  EXTERNAL_AUTH_NAME      default: "entra"
  ENTRA_CLIENT_SECRET     if set, writes secret to openshift-config/oidc-client-secret
  RP_API_VERSION          default: 2024-06-10-preview
  RP_BASE_URL             if set (e.g. http://localhost:8443), uses RP frontend; else ARM
  RP_BEARER_TOKEN         optional bearer for RP frontend
  INSECURE_SKIP_TLS       "true" to skip TLS verify
*/

const ea2OutDir = "test/e2e/out"

// --------- local helpers (unique names to avoid collisions) ---------

func ea2HTTP() *http.Client {
	insecure := strings.EqualFold(os.Getenv("INSECURE_SKIP_TLS"), "true")
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure}}
	return &http.Client{Transport: tr, Timeout: 45 * time.Second}
}

func ea2MustWrite(path string, b []byte) {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	Expect(os.WriteFile(path, b, 0o644)).To(Succeed())
}

func ea2ARMToken(ctx context.Context) (string, error) {
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

func ea2WriteKubeconfig(path string, rc *rest.Config) {
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
	cfg.Contexts["hc"] = &clientcmdapi.Context{Cluster: "hc", AuthInfo: "hc-admin"}
	cfg.CurrentContext = "hc"
	Expect(clientcmd.WriteToFile(*cfg, path)).To(Succeed())
}

func ea2Kubectl(ctx context.Context, args ...string) (string, error) {
	args = append(args, "--insecure-skip-tls-verify")
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	b, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(b)), err
}

// --------------------------- TEST ---------------------------

var _ = Describe("ExternalAuth minimal OIDC (ARM/RP), console+cli", labels.RequireNothing, labels.Critical, labels.Positive, func() {
	It("applies minimal OIDC via PUT, logs into cluster via helper, optionally stores secret, then verifies via GET", func(ctx context.Context) {
		// Inputs
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

		// ARM path (note: externalAuths/<name>, not cluster name)
		path := fmt.Sprintf(
			"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/%s/externalAuths/%s?api-version=%s",
			subID, rg, cluster, externalAuthName, apiVersion,
		)

		// Decide RP vs ARM
		rpBase := strings.TrimSpace(os.Getenv("RP_BASE_URL"))
		useRP := rpBase != ""

		var url string
		reqHeaders := make(http.Header)
		if useRP {
			url = strings.TrimRight(rpBase, "/") + path
			reqHeaders.Set("X-Ms-Arm-Resource-System-Data",
				fmt.Sprintf(`{"createdBy": "mfreer@redhat.com", "createdByType": "User", "createdAt": "%s"}`, time.Now().UTC().Format(time.RFC3339)))
			reqHeaders.Set("X-Ms-Identity-Url", "https://dummy.identity.azure.net")
			if tok := strings.TrimSpace(os.Getenv("RP_BEARER_TOKEN")); tok != "" {
				reqHeaders.Set("Authorization", "Bearer "+tok)
			}
		} else {
			url = "https://management.azure.com" + path
			tok, err := ea2ARMToken(ctx)
			Expect(err).NotTo(HaveOccurred())
			reqHeaders.Set("Authorization", "Bearer "+tok)
		}

		// -------- PUT minimal ExternalAuth (console confidential + cli public) --------
		By("PUT minimal ExternalAuth (console+cli)")
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
		putJSON, _ := json.Marshal(putBody)

		putReq, _ := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(putJSON))
		for k, vs := range reqHeaders {
			for _, v := range vs {
				putReq.Header.Add(k, v)
			}
		}
		putReq.Header.Set("Content-Type", "application/json")
		putResp, err := ea2HTTP().Do(putReq)
		Expect(err).NotTo(HaveOccurred())
		defer putResp.Body.Close()
		putRespBody, _ := io.ReadAll(putResp.Body)

		_ = os.MkdirAll(ea2OutDir, 0o755)
		ea2MustWrite(filepath.Join(ea2OutDir, "external_auth_put.body.json"), putRespBody)
		ea2MustWrite(filepath.Join(ea2OutDir, "external_auth_put.status.txt"), []byte(fmt.Sprintf("%d", putResp.StatusCode)))

		Expect(putResp.StatusCode).To(BeElementOf(http.StatusOK, http.StatusCreated, http.StatusAccepted),
			"PUT %s status=%d body=%s", url, putResp.StatusCode, string(putRespBody))

		// -------- Breakglass admin login via helper & optionally write secret --------
		By("obtaining admin REST config (helper) and logging into cluster")
		tc := framework.NewTestContext()
		adminRC, err := framework.GetAdminRESTConfigForHCPCluster(
			ctx,
			tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
			rg,
			cluster,
			10*time.Minute,
		)
		Expect(err).NotTo(HaveOccurred())

		kcPath := filepath.Join(ea2OutDir, "breakglass.kubeconfig")
		ea2WriteKubeconfig(kcPath, adminRC)
		Expect(os.Setenv("KUBECONFIG", kcPath)).To(Succeed())

		if clientSecret != "" {
			By("creating/overwriting secret openshift-config/ext-auth-client-entra")
			_, err = ea2Kubectl(ctx, "get", "ns", "openshift-config", "-o", "name")
			Expect(err).NotTo(HaveOccurred())
			_, _ = ea2Kubectl(ctx, "-n", "openshift-config", "delete", "secret", "ext-auth-client-entra", "--ignore-not-found")
			_, err = ea2Kubectl(ctx, "-n", "openshift-config", "create", "secret", "generic", "ext-auth-client-entra",
				"--from-literal", "clientSecret="+clientSecret)
			Expect(err).NotTo(HaveOccurred())
		}

		// -------- GET verify with exact spacing you requested --------
		By("GET verify ExternalAuth")
		getReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		for k, vs := range reqHeaders {
			for _, v := range vs {
				getReq.Header.Add(k, v)
			}
		}
		getResp, err := ea2HTTP().Do(getReq)
		Expect(err).NotTo(HaveOccurred())
		defer getResp.Body.Close()
		getRespBody, _ := io.ReadAll(getResp.Body)

		ea2MustWrite(filepath.Join(ea2OutDir, "external_auth_get.body.json"), getRespBody)
		ea2MustWrite(filepath.Join(ea2OutDir, "external_auth_get.status.txt"), []byte(fmt.Sprintf("%d", getResp.StatusCode)))

		Expect(getResp.StatusCode).To(Equal(http.StatusOK),
			"GET %s status=%d body=%s", url, getResp.StatusCode, string(getRespBody))
		Expect(string(getRespBody)).To(ContainSubstring(`"type": "Microsoft.RedHatOpenShift/hcpOpenShiftClusters/externalAuths"`))
		Expect(string(getRespBody)).To(ContainSubstring(`"name": "` + externalAuthName + `"`))
	})
})
