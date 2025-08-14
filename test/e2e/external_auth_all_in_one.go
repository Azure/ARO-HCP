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
	"errors"
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

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"

	"k8s.io/client-go/rest"
	clientcmd "k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

/*
ExternalAuth All-in-One (single test):
  1) Creates an Entra app + client secret
  2) Provisions infra + HCP cluster + node pool via existing bicep templates
  3) Fetches admin creds, discovers console route host, and patches redirect URIs on the Entra app
  4) Applies ExternalAuth config to the RP (via PF to aro-hcp-frontend unless RP_BASE_URL is provided)
  5) Verifies the cluster is reachable with admin creds
*/

const (
	frontendSvcName = "aro-hcp-frontend"
	frontendPort    = 8443

	outDirDefault     = "test/e2e/out"
	secretJSONDefault = "test/e2e/out/entra_app_secret.json"
)

// ---------- Types ----------
type allin1EntraSecretOut struct {
	TenantID     string    `json:"tenant_id"`
	AppObjectID  string    `json:"app_object_id"`
	ClientID     string    `json:"client_id"`
	ClientSecret string    `json:"client_secret"`
	DisplayName  string    `json:"display_name"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"secret_expires_at"`
}

// ---------- Small utils ----------
func allin1FirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
func allin1MustWriteJSON(path string, v any) {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	b, err := json.MarshalIndent(v, "", "  ")
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(path, b, 0o600)).To(Succeed())
}

func allin1HTTPClient() *http.Client {
	insecure := strings.EqualFold(os.Getenv("INSECURE_SKIP_TLS"), "true")
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure}, // for dev/self-signed
	}
	return &http.Client{Transport: tr, Timeout: 45 * time.Second}
}

// ---------- Kubectl helpers ----------
func allin1Run(ctx context.Context, name string, args ...string) (string, error) {
	// Always relax TLS for oc/kubectl/ocm invocations
	if name == "oc" || name == "kubectl" || name == "ocm" {
		args = append(args, "--insecure-skip-tls-verify")
	}
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func allin1FindServiceNamespace(ctx context.Context, svc string) (string, error) {
	out, err := allin1Run(ctx, "kubectl", "get", "svc", "-A",
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

func allin1WithPF(ctx context.Context, ns, svc string, remotePort int, f func(baseURL string)) error {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	localPort := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	args := []string{
		"-n", ns, "port-forward", "svc/" + svc,
		fmt.Sprintf("%d:%d", localPort, remotePort),
		"--insecure-skip-tls-verify",
		"--address", "127.0.0.1",
	}
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	var stderr bytes.Buffer
	cmd.Stdout = &bytes.Buffer{}
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("port-forward start error: %w", err)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", localPort)

	// Wait for PF to open
	deadline := time.Now().Add(12 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		c, derr := net.DialTimeout("tcp", addr, 350*time.Millisecond)
		if derr == nil {
			_ = c.Close()
			lastErr = nil
			break
		}
		lastErr = derr
		time.Sleep(250 * time.Millisecond)
	}
	if lastErr != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("PF didn't open %s: %v; stderr: %s", addr, lastErr, stderr.String())
	}
	defer func() { _ = cmd.Process.Kill() }()

	f("https://" + addr)
	return nil
}

func allin1GetConsoleRouteHost(ctx context.Context) (string, error) {
	host, err := allin1Run(ctx, "kubectl", "-n", "openshift-console",
		"get", "route", "console", "-o", "jsonpath={.spec.host}")
	if err != nil || strings.TrimSpace(host) == "" {
		routes, _ := allin1Run(ctx, "kubectl", "-n", "openshift-console", "get", "routes")
		if strings.TrimSpace(host) == "" {
			return "", fmt.Errorf("console route host empty; routes:\n%s", routes)
		}
		return "", fmt.Errorf("failed to get console route host: %v", err)
	}
	return strings.TrimSpace(host), nil
}

// Write a minimal kubeconfig to disk from a rest.Config so kubectl can use it
func allin1WriteKubeconfigFromRestConfig(path string, rc *rest.Config) error {
	cfg := clientcmdapi.NewConfig()

	clusterName := "e2e-cluster"
	authName := "e2e-auth"
	ctxName := "e2e-context"

	cfg.Clusters[clusterName] = &clientcmdapi.Cluster{
		Server:                   rc.Host,
		InsecureSkipTLSVerify:    rc.Insecure,
		CertificateAuthorityData: rc.CAData,
	}
	cfg.AuthInfos[authName] = &clientcmdapi.AuthInfo{
		Token:                 rc.BearerToken,
		ClientCertificateData: rc.CertData,
		ClientKeyData:         rc.KeyData,
	}
	cfg.Contexts[ctxName] = &clientcmdapi.Context{
		Cluster:  clusterName,
		AuthInfo: authName,
	}
	cfg.CurrentContext = ctxName

	return clientcmd.WriteToFile(*cfg, path)
}

// ---------- Microsoft Graph helpers ----------
func allin1GraphCred(ctx context.Context) (azcore.TokenCredential, string, error) {
	// Prefer Azure CLI locally
	if cred, err := azidentity.NewAzureCLICredential(nil); err == nil {
		tenant := os.Getenv("AZURE_TENANT_ID") // optional, used for issuer URL
		return cred, tenant, nil
	}
	// SP fallback (CI)
	tenant := os.Getenv("AZURE_TENANT_ID")
	clientID := os.Getenv("AZURE_CLIENT_ID")
	clientSecret := os.Getenv("AZURE_CLIENT_SECRET")
	if tenant == "" || clientID == "" || clientSecret == "" {
		return nil, "", errors.New("no az login and missing SP envs (AZURE_TENANT_ID/CLIENT_ID/CLIENT_SECRET)")
	}
	cred, err := azidentity.NewClientSecretCredential(tenant, clientID, clientSecret, nil)
	return cred, tenant, err
}

func allin1GraphToken(ctx context.Context, cred azcore.TokenCredential) (string, error) {
	tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://graph.microsoft.com/.default"},
	})
	if err != nil {
		return "", fmt.Errorf("get Graph token: %w", err)
	}
	return tok.Token, nil
}

func allin1GraphReq[T any](ctx context.Context, method, url, token string, in any, out *T, want int) error {
	var body io.Reader
	if in != nil {
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(in); err != nil {
			return err
		}
		body = &buf
	}
	req, _ := http.NewRequestWithContext(ctx, method, url, body)
	req.Header.Set("Authorization", "Bearer "+token)
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
		return fmt.Errorf("%s %s: got %d want %d; body=%s", method, url, resp.StatusCode, want, string(b))
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			return fmt.Errorf("decode body: %w; body=%s", err, string(b))
		}
	}
	return nil
}

func allin1CreateAppAndSecret(ctx context.Context, token, displayName string, secretLifetime time.Duration) (*allin1EntraSecretOut, error) {
	// 1) Create app
	appBody := map[string]any{
		"displayName":    displayName,
		"signInAudience": "AzureADMyOrg",
		"web":            map[string]any{"redirectUris": []string{}}, // will patch later
	}
	var appOut struct {
		ID      string `json:"id"`    // objectId
		AppID   string `json:"appId"` // clientId
		Created string `json:"createdDateTime"`
	}
	if err := allin1GraphReq(ctx, http.MethodPost, "https://graph.microsoft.com/v1.0/applications", token, appBody, &appOut, http.StatusCreated); err != nil {
		return nil, err
	}
	if appOut.ID == "" || appOut.AppID == "" {
		return nil, fmt.Errorf("app creation returned empty ids: %+v", appOut)
	}

	// 2) Add password credential
	secEnd := time.Now().Add(secretLifetime).UTC().Format(time.RFC3339)
	credBody := map[string]any{
		"passwordCredential": map[string]any{
			"displayName": "e2e-secret",
			"endDateTime": secEnd,
		},
	}
	var credOut struct {
		SecretText string `json:"secretText"`
		End        string `json:"endDateTime"`
	}
	if err := allin1GraphReq(ctx, http.MethodPost, fmt.Sprintf("https://graph.microsoft.com/v1.0/applications/%s/addPassword", appOut.ID), token, credBody, &credOut, http.StatusOK); err != nil {
		return nil, err
	}
	if credOut.SecretText == "" {
		return nil, fmt.Errorf("app secret creation returned empty secret")
	}

	ret := &allin1EntraSecretOut{
		TenantID:     os.Getenv("AZURE_TENANT_ID"), // may be empty in CLI path (we fill from allin1GraphCred hint)
		AppObjectID:  appOut.ID,
		ClientID:     appOut.AppID,
		ClientSecret: credOut.SecretText,
		DisplayName:  displayName,
		CreatedAt:    time.Now().UTC(),
	}
	if t, err := time.Parse(time.RFC3339, credOut.End); err == nil {
		ret.ExpiresAt = t
	}
	return ret, nil
}

func allin1PatchRedirects(ctx context.Context, token, appObjectID string, redirectURIs []string) error {
	body := map[string]any{
		"web": map[string]any{
			"redirectUris": redirectURIs,
		},
	}
	return allin1GraphReq[any](ctx, http.MethodPatch, "https://graph.microsoft.com/v1.0/applications/"+appObjectID, token, body, nil, http.StatusNoContent)
}

// ---------- RP helpers ----------
func allin1PostExternalAuth(ctx context.Context, baseURL, clusterName string, payload []byte) {
	client := allin1HTTPClient()
	u := strings.TrimRight(baseURL, "/") +
		fmt.Sprintf("/api/aro_hcp/v1alpha1/clusters/%s/external_auth_config/external_auths", clusterName)

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer resp.Body.Close()
	Expect(resp.StatusCode).To(BeElementOf(http.StatusCreated, http.StatusOK), "unexpected status creating external auth")

	// Read back to confirm it stuck
	req2, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp2, err := client.Do(req2)
	Expect(err).NotTo(HaveOccurred())
	defer resp2.Body.Close()
	Expect(resp2.StatusCode).To(Equal(http.StatusOK))
	data, _ := io.ReadAll(resp2.Body)
	Expect(string(data)).To(ContainSubstring(`"id":"e2e-hypershift-oidc"`))
}

// ---------- TEST ----------
var _ = Describe("ExternalAuth All-in-One (app → cluster → OIDC config → verify)",
	labels.RequireNothing, labels.Critical, labels.Positive, func() {

		It("creates Entra app+secret, provisions HCP, configures OIDC via RP, and verifies", func(ctx SpecContext) {
			// ===== 1) Entra app + client secret =====
			By("setting up Graph credentials")
			cred, tenantHint, err := allin1GraphCred(ctx)
			if err != nil {
				Skip("no Azure CLI login and no SP creds for Graph; skipping: " + err.Error())
			}
			tok, err := allin1GraphToken(ctx, cred)
			Expect(err).NotTo(HaveOccurred(), "get Graph token")

			By("creating Entra application + client secret")
			displayName := fmt.Sprintf("aro-e2e-%d", time.Now().Unix())
			secret, err := allin1CreateAppAndSecret(ctx, tok, displayName, 48*time.Hour)
			Expect(err).NotTo(HaveOccurred())
			if secret.TenantID == "" {
				secret.TenantID = tenantHint
			}
			Expect(secret.TenantID).NotTo(BeEmpty(), "AZURE_TENANT_ID must be discoverable to form issuer URL")
			Expect(secret.ClientID).NotTo(BeEmpty())
			Expect(secret.AppObjectID).NotTo(BeEmpty())

			outDir := allin1FirstNonEmpty(os.Getenv("E2E_OUT_DIR"), outDirDefault)
			_ = os.MkdirAll(outDir, 0o755)
			secretPath := allin1FirstNonEmpty(os.Getenv("ENTRA_E2E_SECRET_PATH"), secretJSONDefault)
			allin1MustWriteJSON(secretPath, secret)
			By("saved Entra app credentials JSON at " + secretPath)

			// ===== 2) Provision infra + HCP cluster + nodepool (align with cluster_complete_create.go) =====
			By("provisioning infra + HCP + nodepool")
			tc := framework.NewTestContext()

			const (
				customerNetworkSecurityGroupName = "customer-nsg-name"
				customerVnetName                 = "customer-vnet-name"
				customerVnetSubnetName           = "customer-vnet-subnet1"
				customerClusterName              = "external-auth-cluster"
				customerNodePoolName             = "np-1"
				openshiftControlPlaneVersionId   = "4.19"   // MAJOR.MINOR
				openshiftNodeVersionId           = "4.19.0" // full
			)

			region := allin1FirstNonEmpty(os.Getenv("LOCATION"), "uksouth")

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "external-auth", region)
			Expect(err).NotTo(HaveOccurred())

			By("creating a customer-infra")
			customerInfraDeploymentResult, err := framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name,
				"customer-infra",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/customer-infra.json")),
				map[string]interface{}{
					"persistTagValue":        false,
					"customerNsgName":        customerNetworkSecurityGroupName,
					"customerVnetName":       customerVnetName,
					"customerVnetSubnetName": customerVnetSubnetName,
				},
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating managed identities")
			managedIdentityDeploymentResult, err := framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name,
				"managed-identities",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/managed-identities.json")),
				map[string]interface{}{
					"clusterName": customerClusterName,
					"nsgName":     customerNetworkSecurityGroupName,
					"vnetName":    customerVnetName,
					"subnetName":  customerVnetSubnetName,
				},
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the cluster")
			userAssignedIdentities, err := framework.GetOutputValue(managedIdentityDeploymentResult, "userAssignedIdentitiesValue")
			Expect(err).NotTo(HaveOccurred())
			identity, err := framework.GetOutputValue(managedIdentityDeploymentResult, "identityValue")
			Expect(err).NotTo(HaveOccurred())
			keyVaultName, err := framework.GetOutputValue(customerInfraDeploymentResult, "keyVaultName")
			Expect(err).NotTo(HaveOccurred())
			etcdEncryptionKeyName, err := framework.GetOutputValue(customerInfraDeploymentResult, "etcdEncryptionKeyName")
			Expect(err).NotTo(HaveOccurred())

			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			_, err = framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name,
				"cluster",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/cluster.json")),
				map[string]interface{}{
					"openshiftVersionId":          openshiftControlPlaneVersionId,
					"clusterName":                 customerClusterName,
					"managedResourceGroupName":    managedResourceGroupName,
					"nsgName":                     customerNetworkSecurityGroupName,
					"subnetName":                  customerVnetSubnetName,
					"vnetName":                    customerVnetName,
					"userAssignedIdentitiesValue": userAssignedIdentities,
					"identityValue":               identity,
					"keyVaultName":                keyVaultName,
					"etcdEncryptionKeyName":       etcdEncryptionKeyName,
				},
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("getting admin credentials")
			adminRESTConfig, err := framework.GetAdminRESTConfigForHCPCluster(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("ensuring the cluster is viable")
			Expect(framework.VerifyHCPCluster(ctx, adminRESTConfig)).To(Succeed())

			By("creating the node pool")
			_, err = framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name,
				"node-pool",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/nodepool.json")),
				map[string]interface{}{
					"openshiftVersionId": openshiftNodeVersionId,
					"clusterName":        customerClusterName,
					"nodePoolName":       customerNodePoolName,
					"replicas":           2,
				},
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			// ===== 3) Redirect URI discovery + Entra patch =====
			By("writing a temporary kubeconfig for kubectl access to the hosted cluster")
			tmpKC := filepath.Join(outDir, "breakglass.kubeconfig")
			Expect(allin1WriteKubeconfigFromRestConfig(tmpKC, adminRESTConfig)).To(Succeed())
			Expect(os.Setenv("KUBECONFIG", tmpKC)).To(Succeed())

			By("discovering console route host for redirect URI")
			consoleHost, err := allin1GetConsoleRouteHost(ctx)
			Expect(err).NotTo(HaveOccurred())
			redirect := "https://" + consoleHost + "/oauth/callback"

			By("patching redirect URIs on the Entra application")
			Expect(allin1PatchRedirects(ctx, tok, secret.AppObjectID, []string{redirect})).To(Succeed())

			// ===== 4) Publish ExternalAuth to RP =====
			By("publishing ExternalAuth via RP frontend")
			const (
				externalAuthID            = "e2e-hypershift-oidc"
				usernameClaim             = "email"
				groupsClaim               = "groups"
				externalAuthComponentName = "console"
				externalAuthComponentNS   = "openshift-console"
			)
			issuerURL := fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", secret.TenantID)

			payload := map[string]any{
				"id": externalAuthID,
				"issuer": map[string]any{
					"url":       issuerURL,
					"audiences": []string{secret.ClientID},
				},
				"claim": map[string]any{
					"mappings": map[string]any{
						"userName": map[string]any{"claim": usernameClaim},
						"groups":   map[string]any{"claim": groupsClaim},
					},
					"validation_rules": []map[string]any{}, // add group allow-list here if desired
				},
				"clients": []map[string]any{
					{
						"component": map[string]any{
							"name":      externalAuthComponentName,
							"namespace": externalAuthComponentNS,
						},
						"id":     secret.ClientID,
						"secret": "", // not sending client_secret; add if RP needs it
					},
				},
			}
			body, _ := json.Marshal(payload)

			rpBase := strings.TrimSpace(os.Getenv("RP_BASE_URL"))
			if rpBase == "" {
				// PF to frontend if no direct RP provided
				feNS, err := allin1FindServiceNamespace(ctx, frontendSvcName)
				Expect(err).NotTo(HaveOccurred())
				Expect(allin1WithPF(ctx, feNS, frontendSvcName, frontendPort, func(baseURL string) {
					allin1PostExternalAuth(ctx, baseURL, customerClusterName, body)
				})).To(Succeed())
			} else {
				allin1PostExternalAuth(ctx, rpBase, customerClusterName, body)
			}

			// ===== 5) Verify basic cluster health again (optional final check) =====
			Expect(framework.VerifyHCPCluster(ctx, adminRESTConfig)).To(Succeed())
		})
	})
