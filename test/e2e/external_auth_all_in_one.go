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
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	azidentity "github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/client-go/rest"
	clientcmd "k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

const (
	eaOutDir         = "test/e2e/out"
	eaSecretJSONPath = "test/e2e/out/entra_app_secret.json"

	rpFrontendSvc  = "aro-hcp-frontend"
	rpFrontendPort = 8443
)

// ---------- small helpers (scoped to this file) ----------

type eaEntraSecret struct {
	TenantID     string    `json:"tenant_id"`
	AppObjectID  string    `json:"app_object_id"`
	ClientID     string    `json:"client_id"`
	ClientSecret string    `json:"client_secret"`
	DisplayName  string    `json:"display_name"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"secret_expires_at"`
}

func eaMustWriteJSON(path string, v any) {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	b, err := json.MarshalIndent(v, "", "  ")
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(path, b, 0o600)).To(Succeed())
}

func eaHTTP() *http.Client {
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	return &http.Client{Transport: tr, Timeout: 45 * time.Second}
}

func eaRun(ctx context.Context, name string, args ...string) (string, error) {
	if name == "kubectl" || name == "oc" {
		args = append(args, "--insecure-skip-tls-verify")
	}
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func eaFindSvcNS(ctx context.Context, svc string) (string, error) {
	out, err := eaRun(ctx, "kubectl", "get", "svc", "-A",
		"-o", `jsonpath={range .items[*]}{.metadata.name}{"\t"}{.metadata.namespace}{"\n"}{end}`)
	if err != nil {
		return "", err
	}
	for _, ln := range strings.Split(out, "\n") {
		p := strings.Split(strings.TrimSpace(ln), "\t")
		if len(p) == 2 && p[0] == svc {
			return p[1], nil
		}
	}
	return "", fmt.Errorf("service %q not found", svc)
}

func eaWithPF(ctx context.Context, ns, svc string, remotePort int, f func(baseURL string)) error {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	local := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	cmd := exec.CommandContext(ctx, "kubectl", "-n", ns, "port-forward", "svc/"+svc,
		fmt.Sprintf("%d:%d", local, remotePort),
		"--insecure-skip-tls-verify", "--address", "127.0.0.1")
	var stderr bytes.Buffer
	cmd.Stdout = &bytes.Buffer{}
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("port-forward start: %w", err)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", local)
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		c, d := net.DialTimeout("tcp", addr, 300*time.Millisecond)
		if d == nil {
			_ = c.Close()
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	defer func() { _ = cmd.Process.Kill() }()

	f("https://" + addr)
	return nil
}

func eaWriteKubeconfig(path string, rc *rest.Config) error {
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters["hc"] = &clientcmdapi.Cluster{
		Server: rc.Host, InsecureSkipTLSVerify: rc.Insecure, CertificateAuthorityData: rc.CAData,
	}
	cfg.AuthInfos["hc-admin"] = &clientcmdapi.AuthInfo{
		Token: rc.BearerToken, ClientCertificateData: rc.CertData, ClientKeyData: rc.KeyData,
	}
	cfg.Contexts["hc"] = &clientcmdapi.Context{Cluster: "hc", AuthInfo: "hc-admin"}
	cfg.CurrentContext = "hc"
	return clientcmd.WriteToFile(*cfg, path)
}

func eaConsoleHost(ctx context.Context) (string, error) {
	host, err := eaRun(ctx, "kubectl", "-n", "openshift-console",
		"get", "route", "console", "-o", "jsonpath={.spec.host}")
	if strings.TrimSpace(host) == "" {
		routes, _ := eaRun(ctx, "kubectl", "-n", "openshift-console", "get", "routes")
		return "", fmt.Errorf("console route host empty; routes:\n%s", routes)
	}
	return host, err
}

func eaGraphCred(ctx context.Context) (azcore.TokenCredential, string, error) {
	if c, err := azidentity.NewAzureCLICredential(nil); err == nil {
		return c, os.Getenv("AZURE_TENANT_ID"), nil
	}
	tenant := os.Getenv("AZURE_TENANT_ID")
	id := os.Getenv("AZURE_CLIENT_ID")
	sec := os.Getenv("AZURE_CLIENT_SECRET")
	if tenant == "" || id == "" || sec == "" {
		return nil, "", fmt.Errorf("no az login and missing AZURE_TENANT_ID/CLIENT_ID/CLIENT_SECRET")
	}
	c, err := azidentity.NewClientSecretCredential(tenant, id, sec, nil)
	return c, tenant, err
}

func eaGraphToken(ctx context.Context, cred azcore.TokenCredential) (string, error) {
	tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{"https://graph.microsoft.com/.default"}})
	if err != nil {
		return "", err
	}
	return tok.Token, nil
}

func eaGraphReq[T any](ctx context.Context, m, u, tok string, in any, out *T, want int) error {
	var body io.Reader
	if in != nil {
		b := new(bytes.Buffer)
		if err := json.NewEncoder(b).Encode(in); err != nil {
			return err
		}
		body = b
	}
	req, _ := http.NewRequestWithContext(ctx, m, u, body)
	req.Header.Set("Authorization", "Bearer "+tok)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		return fmt.Errorf("%s %s => %d (want %d): %s", m, u, resp.StatusCode, want, string(b))
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			return fmt.Errorf("decode: %w; body=%s", err, string(b))
		}
	}
	return nil
}

func eaCreateAppAndSecret(ctx context.Context, token, name string, ttl time.Duration) (*eaEntraSecret, error) {
	var app struct{ ID, AppID string }
	if err := eaGraphReq(ctx, http.MethodPost, "https://graph.microsoft.com/v1.0/applications",
		token, map[string]any{"displayName": name, "signInAudience": "AzureADMyOrg", "web": map[string]any{"redirectUris": []string{}}}, &app, http.StatusCreated); err != nil {
		return nil, err
	}
	secEnd := time.Now().Add(ttl).UTC().Format(time.RFC3339)
	var pwd struct{ SecretText, End string }
	if err := eaGraphReq(ctx, http.MethodPost,
		fmt.Sprintf("https://graph.microsoft.com/v1.0/applications/%s/addPassword", app.ID),
		token, map[string]any{"passwordCredential": map[string]any{"displayName": "console-secret", "endDateTime": secEnd}},
		&pwd, http.StatusOK); err != nil {
		return nil, err
	}
	out := &eaEntraSecret{
		TenantID:     os.Getenv("AZURE_TENANT_ID"),
		AppObjectID:  app.ID,
		ClientID:     app.AppID,
		ClientSecret: pwd.SecretText,
		DisplayName:  name,
		CreatedAt:    time.Now().UTC(),
	}
	if t, err := time.Parse(time.RFC3339, pwd.End); err == nil {
		out.ExpiresAt = t
	}
	return out, nil
}

func eaPatchRedirects(ctx context.Context, token, appObjectID string, redirects []string) error {
	return eaGraphReq[any](ctx, http.MethodPatch,
		"https://graph.microsoft.com/v1.0/applications/"+appObjectID,
		token, map[string]any{"web": map[string]any{"redirectUris": redirects}}, nil, http.StatusNoContent)
}

func eaPublishExternalAuth(ctx context.Context, baseURL, clusterName string, payload []byte) {
	u := strings.TrimRight(baseURL, "/") + fmt.Sprintf("/api/aro_hcp/v1alpha1/clusters/%s/external_auth_config/external_auths", clusterName)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := eaHTTP().Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer resp.Body.Close()
	Expect(resp.StatusCode).To(BeElementOf(http.StatusOK, http.StatusCreated), "unexpected status from RP")
}

// ---------- TEST ----------

var _ = Describe("ExternalAuth All-in-One creates Entra app+secret, provisions HCP, creates nodepool, configures OIDC via RP, and verifies",
	labels.RequireNothing, labels.Critical, labels.Positive, func() {

		It("creates Entra app, provisions HCP, creates nodepool, gets kubeconfig, and publishes ExternalAuth to RP", func(ctx context.Context) {
			// 1) Entra app + secret
			By("creating Entra app + secret")
			cred, tenantHint, err := eaGraphCred(ctx)
			if err != nil {
				Skip("no Azure login/creds: " + err.Error())
			}
			tok, err := eaGraphToken(ctx, cred)
			Expect(err).NotTo(HaveOccurred())
			app, err := eaCreateAppAndSecret(ctx, tok, fmt.Sprintf("entra-%d", time.Now().Unix()), 48*time.Hour)
			Expect(err).NotTo(HaveOccurred())
			if app.TenantID == "" {
				app.TenantID = tenantHint
			}
			Expect(app.TenantID).NotTo(BeEmpty())
			_ = os.MkdirAll(eaOutDir, 0o755)
			eaMustWriteJSON(eaSecretJSONPath, app)

			// 2) Infra → Managed Identities → Cluster
			const (
				customerNSG    = "customer-nsg-name"
				customerVnet   = "customer-vnet-name"
				customerSubnet = "customer-vnet-subnet1"

				clusterName = "external-auth-smoke"
				versionCP   = "4.19.7" // control-plane MAJOR.MINOR
				versionNP   = "4.19.7" // nodepool FULL version
			)
			tc := framework.NewTestContext()

			By("creating a resource group")
			rg, err := tc.NewResourceGroup(ctx, "external-auth-smoke", "uksouth")
			Expect(err).NotTo(HaveOccurred())

			By("deploying customer-infra (KV + etcd key)")
			infra, err := framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*rg.Name, "customer-infra",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/customer-infra.json")),
				map[string]any{
					"persistTagValue":        false,
					"customerNsgName":        customerNSG,
					"customerVnetName":       customerVnet,
					"customerVnetSubnetName": customerSubnet,
				}, 45*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			keyVaultName, err := framework.GetOutputValue(infra, "keyVaultName")
			Expect(err).NotTo(HaveOccurred())
			etcdKeyName, err := framework.GetOutputValue(infra, "etcdEncryptionKeyName")
			Expect(err).NotTo(HaveOccurred())

			By("deploying managed-identities (with KV access)")
			mi, err := framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*rg.Name, "managed-identities",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/managed-identities.json")),
				map[string]any{
					"clusterName":  clusterName,
					"nsgName":      customerNSG,
					"vnetName":     customerVnet,
					"subnetName":   customerSubnet,
					"keyVaultName": keyVaultName,
				}, 50*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			userAssignedIdentities, err := framework.GetOutputValue(mi, "userAssignedIdentitiesValue")
			Expect(err).NotTo(HaveOccurred())
			identity, err := framework.GetOutputValue(mi, "identityValue")
			Expect(err).NotTo(HaveOccurred())

			By("deploying HCP cluster")
			mrg := framework.SuffixName(*rg.Name, "-managed", 64)
			_, err = framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*rg.Name, "cluster",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/cluster.json")),
				map[string]any{
					"openshiftVersionId":          versionCP,
					"clusterName":                 clusterName,
					"managedResourceGroupName":    mrg,
					"nsgName":                     customerNSG,
					"subnetName":                  customerSubnet,
					"vnetName":                    customerVnet,
					"userAssignedIdentitiesValue": userAssignedIdentities,
					"identityValue":               identity,
					"keyVaultName":                keyVaultName,
					"etcdEncryptionKeyName":       etcdKeyName,
				}, 60*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			// 2b) Nodepool (required for console)
			By("creating the node pool")
			const (
				nodePoolName = "np-1"
				replicas     = 2
			)
			_, err = framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*rg.Name, "node-pool",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/nodepool.json")),
				map[string]any{
					"openshiftVersionId": versionNP,
					"clusterName":        clusterName,
					"nodePoolName":       nodePoolName,
					"replicas":           replicas,
				}, 45*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			// 3) kubeconfig (admin) for hosted cluster
			By("getting admin kubeconfig")
			adminRC, err := framework.GetAdminRESTConfigForHCPCluster(
				ctx, tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*rg.Name, clusterName, 10*time.Minute)
			Expect(err).NotTo(HaveOccurred())
			tmpKC := filepath.Join(eaOutDir, "breakglass.kubeconfig")
			Expect(eaWriteKubeconfig(tmpKC, adminRC)).To(Succeed())
			Expect(os.Setenv("KUBECONFIG", tmpKC)).To(Succeed())

			// 4) console redirect + RP publish
			By("patching app redirect URIs")
			consoleHost, err := eaConsoleHost(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(eaPatchRedirects(ctx, tok, app.AppObjectID, []string{"https://" + consoleHost + "/oauth/callback"})).To(Succeed())

			By("publishing ExternalAuth to RP frontend")
			payload := map[string]any{
				"id": "e2e-hypershift-oidc",
				"issuer": map[string]any{
					"url":       fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", app.TenantID),
					"audiences": []string{app.ClientID},
				},
				"claim": map[string]any{
					"mappings": map[string]any{
						"userName": map[string]any{"claim": "email"},
						"groups":   map[string]any{"claim": "groups"},
					},
					"validation_rules": []map[string]any{},
				},
				"clients": []map[string]any{
					{
						"component": map[string]any{"name": "console", "namespace": "openshift-console"},
						"id":        app.ClientID,
						"secret":    "",
					},
				},
			}
			body, _ := json.Marshal(payload)

			if base := strings.TrimSpace(os.Getenv("RP_BASE_URL")); base != "" {
				eaPublishExternalAuth(ctx, base, clusterName, body)
			} else {
				ns, err := eaFindSvcNS(ctx, rpFrontendSvc)
				Expect(err).NotTo(HaveOccurred())
				Expect(eaWithPF(ctx, ns, rpFrontendSvc, rpFrontendPort, func(baseURL string) {
					eaPublishExternalAuth(ctx, baseURL, clusterName, body)
				})).To(Succeed())
			}

			// 5) final sanity
			Expect(framework.VerifyHCPCluster(ctx, adminRC)).To(Succeed())
		})
	})
