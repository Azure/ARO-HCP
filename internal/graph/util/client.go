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

	abstractions "github.com/microsoft/kiota-abstractions-go"
	kiotahttp "github.com/microsoft/kiota-http-go"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/Azure/ARO-HCP/internal/graph/graphsdk"
)

// Client wraps the Microsoft Graph SDK with authentication and common operations
type Client struct {
	graphClient *graphsdk.GraphBaseServiceClient
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
func NewClient(ctx context.Context, cred azcore.TokenCredential) (*Client, error) {
	authProvider := &azureAuthProvider{cred: cred}

	httpClient, err := kiotahttp.NewNetHttpRequestAdapter(authProvider)
	if err != nil {
		return nil, fmt.Errorf("create request adapter: %w", err)
	}

	graphClient := graphsdk.NewGraphBaseServiceClient(httpClient, nil)

	return &Client{
		graphClient: graphClient,
	}, nil
}

// GetGraphClient returns the underlying Graph SDK client for advanced operations
func (c *Client) GetGraphClient() *graphsdk.GraphBaseServiceClient {
	return c.graphClient
}
