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

package framework

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

func (tc *perBinaryInvocationTestContext) getCurrentAzureIdentityName(ctx context.Context) (string, error) {
	// Use Azure CLI credentials in development, default credentials otherwise
	cred, err := tc.getAzureCredentials()
	if err != nil {
		return "", fmt.Errorf("failed to get Azure credentials: %w", err)
	}

	// Get token for Azure Resource Manager
	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://management.azure.com/.default"},
	})
	if err != nil {
		return "", fmt.Errorf("failed to get Azure token: %w", err)
	}

	// Parse JWT token to extract username from claims
	parts := strings.Split(token.Token, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid JWT token format")
	}

	// Decode payload (second part of JWT)
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("failed to decode JWT payload: %w", err)
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("failed to parse JWT claims: %w", err)
	}

	// Try to get username from common claims (upn, unique_name, email, preferred_username)
	if upn, ok := claims["upn"].(string); ok && upn != "" {
		return upn, nil
	}
	if uniqueName, ok := claims["appid"].(string); ok && uniqueName != "" {
		return uniqueName, nil
	}

	return "", fmt.Errorf("no identifying claim found in token claims")
}

func createSREBreakglassSession(ctx context.Context, httpClient *http.Client, breakglassEndpoint string, username string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, breakglassEndpoint, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("X-Ms-Client-Principal-Name", username)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("expected status 202 Accepted, got %d: %s", resp.StatusCode, string(body))
	}

	location := resp.Header.Get("Location")
	if location == "" {
		return "", fmt.Errorf("no Location header in response")
	}
	return location, nil
}

func waitForSREBreakglassSessionReady(ctx context.Context, httpClient *http.Client, kubeconfigEndpoint string, username string) (*clientcmdapi.Config, error) {
	timeout := time.NewTimer(5 * time.Minute)
	defer timeout.Stop()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeout.C:
			return nil, fmt.Errorf("timeout waiting for session to become ready")
		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, kubeconfigEndpoint, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to create request: %w", err)
			}

			req.Header.Set("X-Ms-Client-Principal-Name", username)

			resp, err := httpClient.Do(req)
			if err != nil {
				return nil, fmt.Errorf("failed to send request: %w", err)
			}

			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				return nil, fmt.Errorf("failed to read response body: %w", err)
			}

			if resp.StatusCode == http.StatusAccepted {
				// Session not ready yet, continue polling
				continue
			}

			if resp.StatusCode != http.StatusOK {
				return nil, fmt.Errorf("expected status 200 OK, got %d: %s", resp.StatusCode, string(body))
			}

			// Parse the kubeconfig from the response (YAML format)
			config, err := clientcmd.Load(body)
			if err != nil {
				return nil, fmt.Errorf("failed to load kubeconfig: %w", err)
			}
			return config, nil
		}
	}
}

func (tc *perItOrDescribeTestContext) SREBreakglassCredentials(ctx context.Context, resourceID string, ttl time.Duration, accessLevel string) (*rest.Config, error) {
	username, err := tc.perBinaryInvocationTestContext.getCurrentAzureIdentityName(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get Azure username: %w", err)
	}

	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
		Timeout: 30 * time.Second,
	}

	adminAPIEndpoint := tc.perBinaryInvocationTestContext.adminAPIAddress

	By(fmt.Sprintf("reaching out to the admin API to create a breakglass session for %s with %s permissions", resourceID, accessLevel))
	breakglassEndpoint := fmt.Sprintf("%s/admin/v1/hcp%s/breakglass?group=%s&ttl=%s",
		adminAPIEndpoint,
		resourceID,
		url.QueryEscape(accessLevel),
		ttl.String(),
	)
	kubeconfigReqPath, err := createSREBreakglassSession(ctx, httpClient, breakglassEndpoint, username)
	if err != nil {
		return nil, fmt.Errorf("failed to create SRE breakglass session: %w", err)
	}

	By(fmt.Sprintf("waiting for SRE breakglass session to be ready at %s", kubeconfigReqPath))
	kubeconfigEndpoint := fmt.Sprintf("%s%s",
		adminAPIEndpoint,
		kubeconfigReqPath,
	)
	kubeconfig, err := waitForSREBreakglassSessionReady(ctx, httpClient, kubeconfigEndpoint, username)
	if err != nil {
		return nil, fmt.Errorf("failed to get ready session kubeconfig from %s: %w", kubeconfigReqPath, err)
	}

	restConfig, err := clientcmd.NewDefaultClientConfig(*kubeconfig, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to create rest config from kubeconfig: %w", err)
	}
	// Skip TLS verification for development environments with self-signed certificates
	if IsDevelopmentEnvironment() {
		restConfig.Insecure = true
	}
	return restConfig, nil
}
