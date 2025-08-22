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

	// Azure auth
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

const eaaio2OutDir = "test/e2e/out"

/*
Required:
  SUBSCRIPTION_ID
  RESOURCE_GROUP_LOCATION

Entra app (either supply or an app will be created via Graph if missing):
  ENTRA_TENANT_ID
  ENTRA_CLIENT_ID
  [optional] ENTRA_CLIENT_SECRET

Optional:
  RESOURCE_GROUP_NAME     (default: external-auth-<ts>)
  HCP_CLUSTER_NAME        (default: external-auth-smoke)
  EXTERNAL_AUTH_NAME      (default: entra)
  RP_API_VERSION          (default: 2024-06-10-preview)
  RP_BASE_URL             (use RP proxy; otherwise ARM)
  RP_BEARER_TOKEN         (only for RP_BASE_URL)
  INSECURE_SKIP_TLS       ("true" to skip TLS verify)
  ARM_BEARER_TOKEN        (if set, used for ARM instead of workload identity)
  ALLOW_AZ_CLI_FALLBACK   ("true" to allow local az CLI fallback; CI-safe default is false)
  OPENSHIFT_CP_VERSION    (default: 4.19.7)
  OPENSHIFT_NP_VERSION    (default: 4.19.7)
  NODEPOOL_NAME           (default: np-1)
  NODEPOOL_REPLICAS       (default: 2)
*/

// ---------------- helpers (unique prefix: eaaio2) ----------------

func eaaio2HTTP() *http.Client {
	insec := strings.EqualFold(os.Getenv("INSECURE_SKIP_TLS"), "true")
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: insec}}
	return &http.Client{Transport: tr, Timeout: 60 * time.Second}
}

func eaaio2MustWrite(path string, b []byte) {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	Expect(os.WriteFile(path, b, 0o644)).To(Succeed())
}

// CI-friendly: prefer env token, then DefaultAzureCredential (WI/MI/SP), optional az CLI fallback.
func eaaio2ARMToken(ctx context.Context) (string, error) {
	if t := strings.TrimSpace(os.Getenv("ARM_BEARER_TOKEN")); t != "" {
		return t, nil
	}
	if cred, err := azidentity.NewDefaultAzureCredential(nil); err == nil {
		if tok, terr := cred.GetToken(ctx, policy.TokenRequestOptions{
			Scopes: []string{"https://management.azure.com/.default"},
		}); terr == nil && tok.Token != "" {
			return tok.Token, nil
		}
	}
	if strings.EqualFold(os.Getenv("ALLOW_AZ_CLI_FALLBACK"), "true") {
		cmd := exec.CommandContext(ctx, "az", "account", "get-access-token",
			"--resource=https://management.azure.com/", "--query", "accessToken", "-o", "tsv")
		if out, err := cmd.CombinedOutput(); err == nil && len(out) > 0 {
			return strings.TrimSpace(string(out)), nil
		}
	}
	return "", fmt.Errorf("no ARM token: set ARM_BEARER_TOKEN or configure workload identity / service principal")
}

func eaaio2WriteKubeconfig(path string, rc *rest.Config) {
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

func eaaio2Kubectl(ctx context.Context, args ...string) (string, error) {
	args = append(args, "--insecure-skip-tls-verify")
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	b, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(b)), err
}

// console route host (for redirect URI)
func eaaio2ConsoleHost(ctx context.Context) (string, error) {
	host, err := eaaio2Kubectl(ctx, "-n", "openshift-console", "get", "route", "console", "-o", "jsonpath={.spec.host}")
	if strings.TrimSpace(host) == "" {
		routes, _ := eaaio2Kubectl(ctx, "-n", "openshift-console", "get", "routes")
		return "", fmt.Errorf("console route empty; routes:\n%s", routes)
	}
	return host, err
}

// ---------------- Microsoft Graph helpers (optional new app path) ----------------

type eaaio2AppSecret struct {
	TenantID     string
	AppObjectID  string
	ClientID     string
	ClientSecret string
}

// Prefer env SP, then DefaultAzureCredential; optional az CLI fallback for local only.
func eaaio2GraphCred(ctx context.Context) (azcore.TokenCredential, string, error) {
	ten := strings.TrimSpace(os.Getenv("AZURE_TENANT_ID"))
	cid := strings.TrimSpace(os.Getenv("AZURE_CLIENT_ID"))
	sec := strings.TrimSpace(os.Getenv("AZURE_CLIENT_SECRET"))
	if ten != "" && cid != "" && sec != "" {
		cred, err := azidentity.NewClientSecretCredential(ten, cid, sec, nil)
		return cred, ten, err
	}
	if cred, err := azidentity.NewDefaultAzureCredential(nil); err == nil {
		return cred, ten, nil // tenant hint may be empty here
	}
	if strings.EqualFold(os.Getenv("ALLOW_AZ_CLI_FALLBACK"), "true") {
		if cred, err := azidentity.NewAzureCLICredential(nil); err == nil {
			return cred, ten, nil
		}
	}
	return nil, "", fmt.Errorf("no Graph credential: set AZURE_TENANT_ID/CLIENT_ID/CLIENT_SECRET or configure workload identity")
}

func eaaio2GraphToken(ctx context.Context, cred azcore.TokenCredential) (string, error) {
	tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{"https://graph.microsoft.com/.default"}})
	if err != nil {
		return "", err
	}
	return tok.Token, nil
}

func eaaio2GraphReq[T any](ctx context.Context, m, u, tok string, in any, out *T, want int) error {
	var body io.Reader
	if in != nil {
		buf := new(bytes.Buffer)
		if err := json.NewEncoder(buf).Encode(in); err != nil {
			return err
		}
		body = buf
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
		return fmt.Errorf("%s %s -> %d (want %d): %s", m, u, resp.StatusCode, want, string(b))
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

func eaaio2CreateAppAndSecret(ctx context.Context, token, name string, ttl time.Duration, tenantHint string) (*eaaio2AppSecret, error) {
	var app struct{ ID, AppID string }
	if err := eaaio2GraphReq(ctx, http.MethodPost, "https://graph.microsoft.com/v1.0/applications",
		token, map[string]any{"displayName": name, "signInAudience": "AzureADMyOrg", "web": map[string]any{"redirectUris": []string{}}}, &app, http.StatusCreated); err != nil {
		return nil, err
	}
	secEnd := time.Now().Add(ttl).UTC().Format(time.RFC3339)
	var pwd struct{ SecretText, End string }
	if err := eaaio2GraphReq(ctx, http.MethodPost,
		fmt.Sprintf("https://graph.microsoft.com/v1.0/applications/%s/addPassword", app.ID),
		token, map[string]any{"passwordCredential": map[string]any{"displayName": "console-secret", "endDateTime": secEnd}},
		&pwd, http.StatusOK); err != nil {
		return nil, err
	}
	return &eaaio2AppSecret{
		TenantID:     tenantHint,
		AppObjectID:  app.ID,
		ClientID:     app.AppID,
		ClientSecret: pwd.SecretText,
	}, nil
}

func eaaio2PatchRedirects(ctx context.Context, token, appObjectID string, redirects []string) error {
	return eaaio2GraphReq[any](ctx, http.MethodPatch,
		"https://graph.microsoft.com/v1.0/applications/"+appObjectID,
		token, map[string]any{"web": map[string]any{"redirectUris": redirects}}, nil, http.StatusNoContent)
}

// ---------------------------- TEST ----------------------------

var _ = Describe("ExternalAuth All-in-One (ARM/RP): infra → cluster → nodepool → breakglass → minimal OIDC (console+cli) → verify",
	labels.RequireNothing, labels.Critical, labels.Positive, func() {

		It("provisions infra+HCP+nodepool, logs in via helper, applies minimal OIDC to ARM/RP, and verifies", func(ctx context.Context) {
			// Inputs
			subID := strings.TrimSpace(os.Getenv("SUBSCRIPTION_ID"))
			loc := strings.TrimSpace(os.Getenv("RESOURCE_GROUP_LOCATION"))
			Expect(subID).NotTo(BeEmpty(), "SUBSCRIPTION_ID")
			Expect(loc).NotTo(BeEmpty(), "RESOURCE_GROUP_LOCATION")

			rgName := strings.TrimSpace(os.Getenv("RESOURCE_GROUP_NAME"))
			if rgName == "" {
				rgName = fmt.Sprintf("external-auth-%d", time.Now().Unix()%100000)
			}
			clusterName := strings.TrimSpace(os.Getenv("HCP_CLUSTER_NAME"))
			if clusterName == "" {
				clusterName = "external-auth-smoke"
			}
			externalAuthName := strings.TrimSpace(os.Getenv("EXTERNAL_AUTH_NAME"))
			if externalAuthName == "" {
				externalAuthName = "entra"
			}
			apiVersion := strings.TrimSpace(os.Getenv("RP_API_VERSION"))
			if apiVersion == "" {
				apiVersion = "2024-06-10-preview"
			}
			cpVer := strings.TrimSpace(os.Getenv("OPENSHIFT_CP_VERSION"))
			if cpVer == "" {
				cpVer = "4.19.7"
			}
			npVer := strings.TrimSpace(os.Getenv("OPENSHIFT_NP_VERSION"))
			if npVer == "" {
				npVer = "4.19.7"
			}
			npName := strings.TrimSpace(os.Getenv("NODEPOOL_NAME"))
			if npName == "" {
				npName = "np-1"
			}
			npReplicas := 2
			if v := strings.TrimSpace(os.Getenv("NODEPOOL_REPLICAS")); v != "" {
				if n, scanErr := fmt.Sscanf(v, "%d", &npReplicas); scanErr != nil || n != 1 {
					GinkgoWriter.Printf("WARN: invalid NODEPOOL_REPLICAS=%q (scanErr=%v, n=%d); using default %d\n", v, scanErr, n, npReplicas)
				}
			}

			// app inputs: either from env, or create one
			tenantID := strings.TrimSpace(os.Getenv("ENTRA_TENANT_ID"))
			clientID := strings.TrimSpace(os.Getenv("ENTRA_CLIENT_ID"))
			clientSecret := strings.TrimSpace(os.Getenv("ENTRA_CLIENT_SECRET"))
			var app *eaaio2AppSecret

			// Prime TestContext (avoids cleanup panic if factory created late)
			tc := framework.NewTestContext()
			_ = tc.GetARMResourcesClientFactoryOrDie(ctx)

			// RG
			By("creating a resource group")
			rg, err := tc.NewResourceGroup(ctx, rgName, loc)
			Expect(err).NotTo(HaveOccurred())

			// Entra app (create only if missing) — CI-safe: if creds not present, Skip
			if tenantID == "" || clientID == "" {
				By("creating Entra app + secret (since ENTRA_* not supplied)")
				cred, hint, err := eaaio2GraphCred(ctx)
				if err != nil {
					Skip("skipping: Graph credential unavailable in CI: " + err.Error())
				}
				tok, err := eaaio2GraphToken(ctx, cred)
				if err != nil || tok == "" {
					Skip("skipping: cannot get Graph token with available credential")
				}
				created, err := eaaio2CreateAppAndSecret(ctx, tok, fmt.Sprintf("entra-%d", time.Now().Unix()), 48*time.Hour, hint)
				Expect(err).NotTo(HaveOccurred())
				app = created
				tenantID, clientID, clientSecret = app.TenantID, app.ClientID, app.ClientSecret
			} else {
				app = &eaaio2AppSecret{
					TenantID:     tenantID,
					ClientID:     clientID,
					ClientSecret: clientSecret,
					AppObjectID:  "",
				}
			}
			Expect(tenantID).NotTo(BeEmpty())
			Expect(clientID).NotTo(BeEmpty())

			// customer-infra
			const (
				customerNSG    = "customer-nsg-name"
				customerVnet   = "customer-vnet-name"
				customerSubnet = "customer-vnet-subnet1"
			)
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
				},
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			keyVaultName, err := framework.GetOutputValue(infra, "keyVaultName")
			Expect(err).NotTo(HaveOccurred())
			etcdKeyName, err := framework.GetOutputValue(infra, "etcdEncryptionKeyName")
			Expect(err).NotTo(HaveOccurred())

			// managed-identities
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
				},
				50*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			userAssignedIdentities, err := framework.GetOutputValue(mi, "userAssignedIdentitiesValue")
			Expect(err).NotTo(HaveOccurred())
			identity, err := framework.GetOutputValue(mi, "identityValue")
			Expect(err).NotTo(HaveOccurred())

			// cluster
			By("deploying HCP cluster")
			mrg := framework.SuffixName(*rg.Name, "-managed", 64)
			_, err = framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*rg.Name, "cluster",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/cluster.json")),
				map[string]any{
					"openshiftVersionId":          cpVer,
					"clusterName":                 clusterName,
					"managedResourceGroupName":    mrg,
					"nsgName":                     customerNSG,
					"subnetName":                  customerSubnet,
					"vnetName":                    customerVnet,
					"userAssignedIdentitiesValue": userAssignedIdentities,
					"identityValue":               identity,
					"keyVaultName":                keyVaultName,
					"etcdEncryptionKeyName":       etcdKeyName,
				},
				60*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			// nodepool (console needs workers)
			By("creating nodepool")
			_, err = framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*rg.Name, "node-pool",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/nodepool.json")),
				map[string]any{
					"openshiftVersionId": npVer,
					"clusterName":        clusterName,
					"nodePoolName":       npName,
					"replicas":           npReplicas,
				},
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			// breakglass kubeconfig
			By("obtaining admin REST config (helper) and logging in")
			adminRC, err := framework.GetAdminRESTConfigForHCPCluster(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*rg.Name,
				clusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())
			kc := filepath.Join(eaaio2OutDir, "breakglass.kubeconfig")
			eaaio2WriteKubeconfig(kc, adminRC)
			Expect(os.Setenv("KUBECONFIG", kc)).To(Succeed())

			// optional: patch app redirect for console if we created an app
			if app.AppObjectID != "" {
				By("patching Entra app redirect URIs for console")
				consoleHost, err := eaaio2ConsoleHost(ctx)
				Expect(err).NotTo(HaveOccurred())
				cred, hint, err := eaaio2GraphCred(ctx)
				Expect(err).NotTo(HaveOccurred())
				if app.TenantID == "" && hint != "" {
					app.TenantID = hint
				}
				tok, err := eaaio2GraphToken(ctx, cred)
				Expect(err).NotTo(HaveOccurred())
				Expect(eaaio2PatchRedirects(ctx, tok, app.AppObjectID, []string{"https://" + consoleHost + "/oauth/callback"})).To(Succeed())
			}

			// apply ExternalAuth via ARM or RP
			By("applying minimal ExternalAuth (console+cli) to ARM/RP")
			path := fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/%s/externalAuths/%s?api-version=%s",
				subID, *rg.Name, clusterName, externalAuthName, apiVersion,
			)
			rpBase := strings.TrimSpace(os.Getenv("RP_BASE_URL"))
			useRP := rpBase != ""
			var url string
			reqHeaders := make(http.Header)
			if useRP {
				url = strings.TrimRight(rpBase, "/") + path
				reqHeaders.Set("X-Ms-Arm-Resource-System-Data",
					fmt.Sprintf(`{"createdBy": "e2e", "createdByType": "User", "createdAt": "%s"}`, time.Now().UTC().Format(time.RFC3339)))
				reqHeaders.Set("X-Ms-Identity-Url", "https://dummy.identity.azure.net")
				if t := strings.TrimSpace(os.Getenv("RP_BEARER_TOKEN")); t != "" {
					reqHeaders.Set("Authorization", "Bearer "+t)
				}
			} else {
				url = "https://management.azure.com" + path
				tok, err := eaaio2ARMToken(ctx)
				Expect(err).NotTo(HaveOccurred())
				reqHeaders.Set("Authorization", "Bearer "+tok)
			}

			putBody := map[string]any{
				"properties": map[string]any{
					"issuer": map[string]any{
						"url":       fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", app.TenantID),
						"audiences": []string{app.ClientID},
					},
					"claim": map[string]any{
						"mappings": map[string]any{
							"username": map[string]any{"claim": "email"},
						},
					},
					"clients": []map[string]any{
						{
							"clientId": app.ClientID,
							"component": map[string]any{
								"name":                "console",
								"authClientNamespace": "openshift-console",
							},
							"type": "confidential",
						},
						{
							"clientId": app.ClientID,
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

			req, _ := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(putJSON))
			for k, vs := range reqHeaders {
				for _, v := range vs {
					req.Header.Add(k, v)
				}
			}
			req.Header.Set("Content-Type", "application/json")

			resp, err := eaaio2HTTP().Do(req)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()
			respBody, _ := io.ReadAll(resp.Body)
			eaaio2MustWrite(filepath.Join(eaaio2OutDir, "external_auth_put.body.json"), respBody)
			eaaio2MustWrite(filepath.Join(eaaio2OutDir, "external_auth_put.status.txt"), []byte(fmt.Sprintf("%d", resp.StatusCode)))
			Expect(resp.StatusCode).To(BeElementOf(http.StatusOK, http.StatusCreated, http.StatusAccepted),
				"PUT %s status=%d body=%s", url, resp.StatusCode, string(respBody))

			// optionally store clientSecret for console
			// optionally store clientSecret for console
			if clientSecret != "" {
				By("storing client secret in openshift-config/entra-console-openshift-console")
				_, err = eaaio2Kubectl(ctx, "get", "ns", "openshift-config", "-o", "name")
				Expect(err).NotTo(HaveOccurred())
				_, _ = eaaio2Kubectl(ctx, "-n", "openshift-config", "delete", "secret", "entra-console-openshift-console", "--ignore-not-found")
				_, err = eaaio2Kubectl(ctx, "-n", "openshift-config", "create", "secret", "generic", "entra-console-openshift-console",
					"--from-literal", "clientSecret="+clientSecret)
				Expect(err).NotTo(HaveOccurred())
			}

			// verify via GET
			By("verifying ExternalAuth via GET")
			getReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			for k, vs := range reqHeaders {
				for _, v := range vs {
					getReq.Header.Add(k, v)
				}
			}
			getResp, err := eaaio2HTTP().Do(getReq)
			Expect(err).NotTo(HaveOccurred())
			defer getResp.Body.Close()
			getB, _ := io.ReadAll(getResp.Body)
			eaaio2MustWrite(filepath.Join(eaaio2OutDir, "external_auth_get.body.json"), getB)
			eaaio2MustWrite(filepath.Join(eaaio2OutDir, "external_auth_get.status.txt"), []byte(fmt.Sprintf("%d", getResp.StatusCode)))
			Expect(getResp.StatusCode).To(Equal(http.StatusOK),
				"GET %s status=%d body=%s", url, getResp.StatusCode, string(getB))
			Expect(string(getB)).To(ContainSubstring(`"type": "Microsoft.RedHatOpenShift/hcpOpenShiftClusters/externalAuths"`))
			Expect(string(getB)).To(ContainSubstring(`"name": "` + externalAuthName + `"`))

			// cluster sanity
			Expect(framework.VerifyHCPCluster(ctx, adminRC)).To(Succeed())
		})
	})
