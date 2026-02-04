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

package tenantquota

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/dev-infrastructure/ops-tools/tenant-quota/pkg/config"
)

const (
	graphAPIEndpoint   = "https://graph.microsoft.com/v1.0/organization"
	secretsStorePath   = "/mnt/secrets-store"
	secretsStoreEnvVar = "SECRETS_STORE_PATH"
)

type QuotaData struct {
	TenantID          string
	TenantName        string
	UsagePercentage   int
	QuotaTotal        int
	QuotaUsed         int
	RemainingCapacity int
	Timestamp         time.Time
}

type QuotaClient struct {
	httpClient *http.Client
	logger     *slog.Logger
	credCache  map[string]*azidentity.ClientSecretCredential
	credMu     sync.RWMutex
}

func NewQuotaClient(timeout time.Duration, logger *slog.Logger) *QuotaClient {
	return &QuotaClient{
		httpClient: &http.Client{Timeout: timeout},
		logger:     logger,
		credCache:  make(map[string]*azidentity.ClientSecretCredential),
	}
}

func (c *QuotaClient) GetQuota(ctx context.Context, tenant config.TenantConfig) (*QuotaData, error) {
	cred, err := c.getCredential(tenant)
	if err != nil {
		return nil, fmt.Errorf("get credential: %w", err)
	}

	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{tenant.GetScope()},
	})
	if err != nil {
		return nil, fmt.Errorf("get token: %w", err)
	}

	return c.fetchQuotaFromAPI(ctx, token.Token, tenant)
}

func (c *QuotaClient) getCredential(tenant config.TenantConfig) (*azidentity.ClientSecretCredential, error) {
	cacheKey := tenant.TenantID + ":" + tenant.ServicePrincipalClientId

	c.credMu.RLock()
	if cred, ok := c.credCache[cacheKey]; ok {
		c.credMu.RUnlock()
		return cred, nil
	}
	c.credMu.RUnlock()

	secret, err := readSecret(tenant.KeyVaultSecretName)
	if err != nil {
		return nil, fmt.Errorf("read secret: %w", err)
	}

	cred, err := azidentity.NewClientSecretCredential(
		tenant.TenantID,
		tenant.ServicePrincipalClientId,
		secret,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create credential: %w", err)
	}

	c.credMu.Lock()
	c.credCache[cacheKey] = cred
	c.credMu.Unlock()

	c.logger.Debug("Created credential for tenant", "tenant", tenant.GetDisplayName())
	return cred, nil
}

func (c *QuotaClient) fetchQuotaFromAPI(ctx context.Context, token string, tenant config.TenantConfig) (*QuotaData, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, graphAPIEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("API returned %d (failed to read body: %w)", resp.StatusCode, err)
		}
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	return parseOrganizationResponse(resp.Body, tenant)
}

type organizationResponse struct {
	Value []struct {
		DisplayName        string `json:"displayName"`
		DirectorySizeQuota struct {
			Used  *int `json:"used"`
			Total *int `json:"total"`
		} `json:"directorySizeQuota"`
	} `json:"value"`
}

func parseOrganizationResponse(body io.Reader, tenant config.TenantConfig) (*QuotaData, error) {
	var resp organizationResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(resp.Value) == 0 {
		return nil, fmt.Errorf("no organization data in response")
	}

	org := resp.Value[0]
	if org.DirectorySizeQuota.Total == nil || org.DirectorySizeQuota.Used == nil {
		return nil, fmt.Errorf("incomplete quota data")
	}

	total := *org.DirectorySizeQuota.Total
	used := *org.DirectorySizeQuota.Used

	if total <= 0 {
		return nil, fmt.Errorf("invalid quota total: %d", total)
	}

	name := tenant.TenantName
	if name == "" {
		name = org.DisplayName
	}

	return &QuotaData{
		TenantID:          tenant.TenantID,
		TenantName:        name,
		UsagePercentage:   (used * 100) / total,
		QuotaTotal:        total,
		QuotaUsed:         used,
		RemainingCapacity: total - used,
		Timestamp:         time.Now().UTC(),
	}, nil
}

func readSecret(secretName string) (string, error) {
	basePath := os.Getenv(secretsStoreEnvVar)
	if basePath == "" {
		basePath = secretsStorePath
	}

	path := basePath + "/" + secretName
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}

	return strings.TrimSpace(string(data)), nil
}
