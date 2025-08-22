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

package util

import (
	"context"
	"fmt"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	abstractions "github.com/microsoft/kiota-abstractions-go"
	kiotahttp "github.com/microsoft/kiota-http-go"

	"github.com/Azure/ARO-HCP/internal/graph/graphsdk"
	"github.com/Azure/ARO-HCP/internal/graph/graphsdk/organization"
)

// Client wraps the Microsoft Graph SDK with authentication and common operations
type Client struct {
	graphClient *graphsdk.GraphBaseServiceClient
	tenantID    string
}

// azureAuthProvider implements the Kiota AuthenticationProvider interface
type azureAuthProvider struct {
	cred azcore.TokenCredential
}

func (a *azureAuthProvider) AuthenticateRequest(ctx context.Context, request *abstractions.RequestInformation, additionalAuthenticationContext map[string]interface{}) error {
	token, err := a.cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://graph.microsoft.com/.default"},
	})
	if err != nil {
		return fmt.Errorf("get token: %w", err)
	}
	request.Headers.Add("Authorization", "Bearer "+token.Token)
	return nil
}

// NewClient creates a new Graph client with automatic authentication
func NewClient(ctx context.Context) (*Client, error) {
	cred, err := getCredential(ctx)
	if err != nil {
		return nil, fmt.Errorf("get credential: %w", err)
	}

	authProvider := &azureAuthProvider{cred: cred}

	httpClient, err := kiotahttp.NewNetHttpRequestAdapter(authProvider)
	if err != nil {
		return nil, fmt.Errorf("create request adapter: %w", err)
	}

	graphClient := graphsdk.NewGraphBaseServiceClient(httpClient, nil)

	// Resolve tenant ID
	tenantID, err := resolveTenantID(ctx, graphClient)
	if err != nil {
		return nil, fmt.Errorf("resolve tenant ID: %w", err)
	}

	return &Client{
		graphClient: graphClient,
		tenantID:    tenantID,
	}, nil
}

// getCredential implements a fallback chain for authentication
func getCredential(ctx context.Context) (azcore.TokenCredential, error) {
	// 1. Try Client Secret (for CI/CD)
	if tenantID := os.Getenv("AZURE_TENANT_ID"); tenantID != "" {
		if clientID := os.Getenv("AZURE_CLIENT_ID"); clientID != "" {
			if clientSecret := os.Getenv("AZURE_CLIENT_SECRET"); clientSecret != "" {
				cred, err := azidentity.NewClientSecretCredential(tenantID, clientID, clientSecret, nil)
				if err == nil {
					return cred, nil
				}
			}
		}
	}

	// 2. Try Default Azure Credential (production)
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err == nil {
		return cred, nil
	}

	// 3. Try Azure CLI (development)
	if os.Getenv("ALLOW_AZ_CLI_FALLBACK") == "true" {
		cred, err := azidentity.NewAzureCLICredential(nil)
		if err == nil {
			return cred, nil
		}
	}

	return nil, fmt.Errorf("no valid authentication method found")
}

// resolveTenantID gets the tenant ID from the organization
func resolveTenantID(ctx context.Context, graphClient *graphsdk.GraphBaseServiceClient) (string, error) {
	queryParams := &organization.OrganizationRequestBuilderGetQueryParameters{
		Select: []string{"id"},
	}
	config := &organization.OrganizationRequestBuilderGetRequestConfiguration{
		QueryParameters: queryParams,
	}

	orgResponse, err := graphClient.Organization().Get(ctx, config)
	if err != nil {
		return "", fmt.Errorf("get organization: %w", err)
	}

	if len(orgResponse.GetValue()) == 0 {
		return "", fmt.Errorf("no organizations returned; ensure `az account set --tenant <TENANT_ID>`")
	}

	return *orgResponse.GetValue()[0].GetId(), nil
}

// GetTenantID returns the resolved tenant ID
func (c *Client) GetTenantID() string {
	return c.tenantID
}

// GetGraphClient returns the underlying Graph SDK client for advanced operations
func (c *Client) GetGraphClient() *graphsdk.GraphBaseServiceClient {
	return c.graphClient
}
