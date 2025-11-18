/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package mc

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/Azure/ARO-HCP/sessiongate/internal/hcp"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

const (
	// Azure AKS OAuth2 server ID for RBAC
	aksOAuthServerID = "6dae42f8-4368-4678-94ff-3960e28e3630"

	// CSR minting constants
	csrTimeout      = 15 * time.Second          // Maximum time to wait for CSR approval and certificate issuance
	defaultRSABits  = 2048                      // RSA key size for certificate generation
	kasCertSecret   = "kube-apiserver-tls-cert" // Secret containing the kube-apiserver TLS certificate
	kasCertKey      = "tls.crt"                 // Key in the secret containing the server cert
	kubeAPIServPort = 443                       // Kubernetes API server port
)

// GetAKSRESTConfig creates a REST config for an AKS cluster with dynamic Azure token authentication
func GetAKSRESTConfig(ctx context.Context, resourceID string, credential azcore.TokenCredential) (*rest.Config, error) {
	// Parse the resource ID using the Azure Go SDK
	parsedResourceID, err := azcorearm.ParseResourceID(resourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse resource ID: %w", err)
	}
	subscriptionID := parsedResourceID.SubscriptionID
	resourceGroup := parsedResourceID.ResourceGroupName
	clusterName := parsedResourceID.Name

	// Create AKS client
	client, err := armcontainerservice.NewManagedClustersClient(subscriptionID, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create AKS client: %w", err)
	}

	// Get the cluster user credentials to extract server URL and CA cert
	resp, err := client.ListClusterUserCredentials(ctx, resourceGroup, clusterName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster user credentials: %w", err)
	}
	if len(resp.Kubeconfigs) == 0 {
		return nil, fmt.Errorf("no kubeconfig found")
	}

	// Parse the kubeconfig to extract server URL and CA data
	config, err := clientcmd.Load(resp.Kubeconfigs[0].Value)
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	// Get the first cluster info
	var clusterInfo *clientcmdapi.Cluster
	for _, cluster := range config.Clusters {
		clusterInfo = cluster
		break
	}
	if clusterInfo == nil {
		return nil, fmt.Errorf("no cluster found in kubeconfig")
	}

	// Build REST config with dynamic Azure token authentication
	restConfig := &rest.Config{
		Host: clusterInfo.Server,
		TLSClientConfig: rest.TLSClientConfig{
			CAData:     clusterInfo.CertificateAuthorityData,
			ServerName: clusterInfo.TLSServerName,
			Insecure:   clusterInfo.InsecureSkipTLSVerify,
		},
	}

	// Wrap transport to inject Azure tokens dynamically
	restConfig.Wrap(func(rt http.RoundTripper) http.RoundTripper {
		return &azureTokenRoundTripper{
			credential: credential,
			base:       rt,
		}
	})

	return restConfig, nil
}

// azureTokenRoundTripper injects Azure bearer tokens into Kubernetes API requests
type azureTokenRoundTripper struct {
	credential azcore.TokenCredential
	base       http.RoundTripper
}

// RoundTrip implements http.RoundTripper by fetching an Azure token and adding it to the request
func (rt *azureTokenRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Get Azure token for AKS
	token, err := rt.credential.GetToken(req.Context(), policy.TokenRequestOptions{
		Scopes: []string{aksOAuthServerID + "/.default"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get Azure token: %w", err)
	}

	// Clone the request to avoid modifying the original
	reqClone := req.Clone(req.Context())

	// Add the bearer token
	reqClone.Header.Set("Authorization", "Bearer "+token.Token)

	// Execute the request with the base transport
	return rt.base.RoundTrip(reqClone)
}

// GetHostedClusterRESTConfig creates a REST config for a hosted cluster by minting a certificate via CSR
func GetHostedClusterRESTConfig(ctx context.Context, mgmtRestConfig *rest.Config, hcpInfo HCPInfo, user string, privileged bool) (*rest.Config, error) {
	// Step 1: Generate private key and CSR
	privateKey, err := hcp.GeneratePrivateKey(defaultRSABits)
	if err != nil {
		return nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	subject := hcp.BuildSubject(user, privileged)
	csrPEM, err := hcp.GenerateCSR(privateKey, subject)
	if err != nil {
		return nil, fmt.Errorf("failed to generate CSR: %w", err)
	}

	// Step 2: Create CSR manager and submit CSR
	csrManager, err := hcp.NewCSRManager(mgmtRestConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create CSR manager: %w", err)
	}

	csrName, err := csrManager.CreateCSR(ctx, csrPEM, user, hcpInfo.ID, user, hcpInfo.Namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to create CSR: %w", err)
	}

	// Step 3: Create CSR approval
	if err := csrManager.CreateCSRApproval(ctx, csrName, hcpInfo.Namespace, hcpInfo.ID, user); err != nil {
		return nil, fmt.Errorf("failed to create CSR approval: %w", err)
	}

	// Step 4: Wait for CSR approval and certificate issuance
	if err := csrManager.WaitForCSRApproval(ctx, csrName, csrTimeout); err != nil {
		return nil, fmt.Errorf("failed waiting for CSR approval: %w", err)
	}

	certificate, err := csrManager.WaitForCertificate(ctx, csrName, csrTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed waiting for certificate: %w", err)
	}

	// Step 5: Get kube-apiserver TLS certificate
	kubeClient, err := kubernetes.NewForConfig(mgmtRestConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	secret, err := kubeClient.CoreV1().Secrets(hcpInfo.Namespace).Get(ctx, kasCertSecret, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get kube-apiserver certificate secret: %w", err)
	}

	serverCert, ok := secret.Data[kasCertKey]
	if !ok {
		return nil, fmt.Errorf("server certificate not found in secret %s/%s", hcpInfo.Namespace, kasCertSecret)
	}

	// Step 6: Determine CA certificate to use
	// If the server cert is self-signed, use it as the CA
	// Otherwise, rely on system trust store (don't set CA)
	var caCertToUse []byte
	if isSelfSigned, err := isCertSelfSigned(serverCert); err != nil {
		return nil, fmt.Errorf("failed to check if certificate is self-signed: %w", err)
	} else if isSelfSigned {
		// Self-signed: use server cert as CA
		caCertToUse = serverCert
	}
	// Otherwise caCertToUse remains nil, system trust store will be used

	// Step 7: Build kubeconfig
	kubeconfig := buildKubeconfig(hcpInfo.Name, hcpInfo.APIServerDNSName, caCertToUse, certificate, hcp.EncodePrivateKey(privateKey))

	// Step 8: Convert to REST config
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create REST config from kubeconfig: %w", err)
	}

	return restConfig, nil
}

// isCertSelfSigned checks if a PEM-encoded certificate is self-signed
func isCertSelfSigned(certPEM []byte) (bool, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return false, fmt.Errorf("failed to decode PEM certificate")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, fmt.Errorf("failed to parse certificate: %w", err)
	}

	// A certificate is self-signed if the issuer equals the subject
	return cert.Issuer.String() == cert.Subject.String(), nil
}

// buildKubeconfig constructs a kubeconfig byte array from the provided components
func buildKubeconfig(clusterName, server string, caCert, clientCert, clientKey []byte) []byte {
	config := clientcmdapi.Config{
		Kind:       "Config",
		APIVersion: "v1",
		Clusters: map[string]*clientcmdapi.Cluster{
			clusterName: {
				Server:                   fmt.Sprintf("https://%s:%d", server, kubeAPIServPort),
				CertificateAuthorityData: caCert,
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			clusterName: {
				Cluster:  clusterName,
				AuthInfo: "admin",
			},
		},
		CurrentContext: clusterName,
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"admin": {
				ClientCertificateData: clientCert,
				ClientKeyData:         clientKey,
			},
		},
	}

	kubeconfigBytes, _ := clientcmd.Write(config)
	return kubeconfigBytes
}
