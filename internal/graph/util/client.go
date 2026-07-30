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

	"github.com/golang-jwt/jwt/v5"
	abstractions "github.com/microsoft/kiota-abstractions-go"
	kiotahttp "github.com/microsoft/kiota-http-go"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/Azure/ARO-HCP/internal/graph/graphsdk"
)

// Client wraps the Microsoft Graph SDK with authentication and common operations
type Client struct {
	graphClient *graphsdk.GraphBaseServiceClient
	isUser      bool
	objectID    string
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

// NewClient creates a new Graph client with automatic authentication.
// It inspects the token to determine whether the credential represents a user
// or a service principal, which affects which Graph API endpoints are used.
func NewClient(ctx context.Context, cred azcore.TokenCredential) (*Client, error) {
	authProvider := &azureAuthProvider{cred: cred}

	httpClient, err := kiotahttp.NewNetHttpRequestAdapter(authProvider)
	if err != nil {
		return nil, fmt.Errorf("create request adapter: %w", err)
	}

	graphClient := graphsdk.NewGraphBaseServiceClient(httpClient, nil)

	isUser, objectID, err := identifyCallerFromToken(ctx, cred)
	if err != nil {
		return nil, fmt.Errorf("identify caller: %w", err)
	}

	return &Client{
		graphClient: graphClient,
		isUser:      isUser,
		objectID:    objectID,
	}, nil
}

// identifyCallerFromToken acquires a Graph token and parses the JWT without
// verification to determine whether the caller is a user or a service principal.
// Returns isUser, the caller's object ID, and any error.
func identifyCallerFromToken(ctx context.Context, cred azcore.TokenCredential) (bool, string, error) {
	accessToken, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://graph.microsoft.com/.default"},
	})
	if err != nil {
		return false, "", fmt.Errorf("get token: %w", err)
	}

	parser := jwt.NewParser()
	claims := jwt.MapClaims{}
	// we parse the token unverified as it's used in subsequent graph calls that will fail if the user does not have
	// authZ to perform the requests
	if _, _, err := parser.ParseUnverified(accessToken.Token, claims); err != nil {
		return false, "", fmt.Errorf("parse token: %w", err)
	}

	oid, _ := claims["oid"].(string)
	if oid == "" {
		return false, "", fmt.Errorf("token missing oid claim")
	}

	// idtyp is "app" for service principal tokens, "user" (or absent) for user tokens
	idtyp, _ := claims["idtyp"].(string)
	isUser := idtyp != "app"
	return isUser, oid, nil
}

// GetGraphClient returns the underlying Graph SDK client for advanced operations
func (c *Client) GetGraphClient() *graphsdk.GraphBaseServiceClient {
	return c.graphClient
}
