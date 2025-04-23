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

package azauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/go-logr/logr"
)

const (
	AZURE_CLIENT_ID                = "AZURE_CLIENT_ID"
	AZURE_TENANT_ID                = "AZURE_TENANT_ID"
	AZURE_FEDERATED_TOKEN_FILE     = "AZURE_FEDERATED_TOKEN_FILE"
	ACTIONS_ID_TOKEN_REQUEST_URL   = "ACTIONS_ID_TOKEN_REQUEST_URL"
	ACTIONS_ID_TOKEN_REQUEST_TOKEN = "ACTIONS_ID_TOKEN_REQUEST_TOKEN"
)

func githubAuthSupported() bool {
	if _, ok := os.LookupEnv(AZURE_CLIENT_ID); !ok {
		return false
	}
	if _, ok := os.LookupEnv(AZURE_TENANT_ID); !ok {
		return false
	}
	if _, ok := os.LookupEnv(ACTIONS_ID_TOKEN_REQUEST_URL); !ok {
		return false
	}
	if _, ok := os.LookupEnv(ACTIONS_ID_TOKEN_REQUEST_TOKEN); !ok {
		return false
	}
	return true
}

func setupGithubAzureFederationAuthRefresher(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)
	clientId := os.Getenv(AZURE_CLIENT_ID)
	tenantId := os.Getenv(AZURE_TENANT_ID)
	requestToken := os.Getenv(ACTIONS_ID_TOKEN_REQUEST_TOKEN)
	requestURL := os.Getenv(ACTIONS_ID_TOKEN_REQUEST_URL)
	err := refreshGithubAzureFederatedSession(ctx, clientId, tenantId, requestURL, requestToken)
	if err != nil {
		return fmt.Errorf("failed to refresh Azure session with federated GitHub ID token: %w", err)
	}
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				err := refreshGithubAzureFederatedSession(ctx, clientId, tenantId, requestURL, requestToken)
				if err != nil {
					logger.Error(err, "failed to refresh Azure session with federated GitHub ID token")
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return nil
}

func refreshGithubAzureFederatedSession(ctx context.Context, clientId, tenantId, requestUrl, requestToken string) error {
	logger := logr.FromContextOrDiscard(ctx)
	logger.V(7).Info("Refreshing Azure session with federated GitHub ID token")
	token, err := getGithubIDToken(ctx, requestUrl, requestToken)
	if err != nil {
		return fmt.Errorf("failed to get GitHub ID token: %w", err)
	}
	cmd := exec.CommandContext(ctx, "az", "login", "--service-principal", "--username", clientId, "--tenant", tenantId, "--federated-token", token)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to run az login: %s %v", string(output), err)
	}
	logger.V(7).Info("Azure session refreshed with federated GitHub ID token", "az cli output", output)
	return nil
}

func getGithubIDToken(ctx context.Context, requestURL, requestToken string) (string, error) {
	timeoutContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// build request, add auth and audience
	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", requestToken))
	q := req.URL.Query()
	q.Add("audience", "api://AzureADTokenExchange")
	req.URL.RawQuery = q.Encode()

	// send request with timeout
	client := &http.Client{}
	resp, err := client.Do(req.WithContext(timeoutContext))
	if err != nil {
		return "", fmt.Errorf("failed to get ID token: %w", err)
	}
	defer resp.Body.Close()

	// process response
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to get ID token: status code %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}
	var tokenResponse struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(body, &tokenResponse); err != nil {
		return "", fmt.Errorf("failed to unmarshal response body: %w", err)
	}

	return tokenResponse.Value, nil
}
