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

package mc

import (
	"context"
	"fmt"
	"net/http"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice"
)

const (
	// Azure AKS OAuth2 server ID for RBAC
	aksOAuthServerID = "6dae42f8-4368-4678-94ff-3960e28e3630"
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
