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

package acrauth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/containers/azcontainerregistry"
)

// NullGUIDUsername is the username ACR expects when the password is a refresh token.
const NullGUIDUsername = "00000000-0000-0000-0000-000000000000"

const armScope = "https://management.azure.com/.default"

// ExchangeForRefreshToken trades an Entra token for an ACR refresh token, which is what
// container tooling stores as the registry password.
func ExchangeForRefreshToken(ctx context.Context, cred azcore.TokenCredential, acrFQDN string) (string, error) {
	endpoint, err := url.Parse(fmt.Sprintf("https://%s", acrFQDN))
	if err != nil {
		return "", fmt.Errorf("failed to parse ACR endpoint: %w", err)
	}

	client, err := azcontainerregistry.NewAuthenticationClient(endpoint.String(), nil)
	if err != nil {
		return "", fmt.Errorf("failed to create ACR authentication client: %w", err)
	}

	armToken, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{armScope}})
	if err != nil {
		return "", fmt.Errorf("failed to get ARM token: %w", err)
	}

	response, err := client.ExchangeAADAccessTokenForACRRefreshToken(
		ctx,
		azcontainerregistry.PostContentSchemaGrantTypeAccessToken,
		endpoint.Hostname(),
		&azcontainerregistry.AuthenticationClientExchangeAADAccessTokenForACRRefreshTokenOptions{
			AccessToken: &armToken.Token,
		},
	)
	if err != nil {
		return "", fmt.Errorf("failed to exchange AAD access token for ACR refresh token: %w", err)
	}
	if response.RefreshToken == nil || *response.RefreshToken == "" {
		return "", errors.New("got an empty response when exchanging AAD access token for ACR refresh token")
	}

	return *response.RefreshToken, nil
}

const (
	retryAttempts = 5
	retryInitial  = 2 * time.Second
	retryFactor   = 2
)

// ExchangeForRefreshTokenWithRetry retries the exchange, which fails transiently while a
// freshly-granted role assignment propagates.
func ExchangeForRefreshTokenWithRetry(ctx context.Context, cred azcore.TokenCredential, acrFQDN string) (string, error) {
	var lastErr error
	delay := retryInitial

	for attempt := range retryAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", fmt.Errorf("failed to exchange ACR refresh token: %w: last exchange error: %w", ctx.Err(), lastErr)
			case <-time.After(delay):
			}
			delay *= retryFactor
		}

		token, err := ExchangeForRefreshToken(ctx, cred, acrFQDN)
		if err == nil {
			return token, nil
		}
		lastErr = err
	}

	return "", fmt.Errorf("failed to exchange ACR refresh token after %d attempts: %w", retryAttempts, lastErr)
}
