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
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/dev-infrastructure/ops-tools/tenant-quota/pkg/config"
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

type organizationResponse struct {
	Value []struct {
		ID                 string `json:"id"`
		DisplayName        string `json:"displayName"`
		DirectorySizeQuota struct {
			Used  *int `json:"used"`
			Total *int `json:"total"`
		} `json:"directorySizeQuota"`
	} `json:"value"`
}

// CollectQuota collects tenant quota information from Microsoft Graph API for the specified tenant.
func CollectQuota(ctx context.Context, tenant config.TenantConfig) (*QuotaData, error) {
	handler := slog.NewJSONHandler(os.Stderr, nil)
	logger := slog.New(handler)

	// Get secret from Key Vault
	secret, err := getSecretFromKeyVault(tenant.KeyVaultSecretName)
	if err != nil {
		return nil, fmt.Errorf("get secret from vault for tenant %s: %w", tenant.TenantID, err)
	}

	scope := tenant.Scope
	if scope == "" {
		scope = "https://graph.microsoft.com/.default"
	}

	// Create credential for this tenant
	cred, err := azidentity.NewClientSecretCredential(
		tenant.TenantID,
		tenant.ServicePrincipalClientId,
		secret,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create credential for tenant %s: %w", tenant.TenantID, err)
	}

	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{scope},
	})
	if err != nil {
		return nil, fmt.Errorf("get token for tenant %s: %w", tenant.TenantID, err)
	}

	// Collect quota data
	quotaData, err := collectQuotaForTenant(ctx, token.Token, tenant.TenantID)
	if err != nil {
		return nil, fmt.Errorf("collect quota for tenant %s: %w", tenant.TenantID, err)
	}

	// Use tenant name from config if available, otherwise use API response
	if tenant.TenantName != "" {
		quotaData.TenantName = tenant.TenantName
	}

	logger.Debug("Successfully collected quota for tenant",
		"tenant_id", quotaData.TenantID,
		"tenant_name", quotaData.TenantName,
		"usage_percentage", quotaData.UsagePercentage,
	)

	return quotaData, nil
}

// collectQuotaForTenant makes the Graph API call and parses the response
func collectQuotaForTenant(ctx context.Context, tokenString string, tenantID string) (*QuotaData, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://graph.microsoft.com/v1.0/organization", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+tokenString)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("graph API returned status %d (failed to read response body: %w)", resp.StatusCode, err)
		}
		return nil, fmt.Errorf("graph API returned status %d: %s", resp.StatusCode, string(body))
	}

	var orgResp organizationResponse
	if err := json.NewDecoder(resp.Body).Decode(&orgResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(orgResp.Value) == 0 {
		return nil, fmt.Errorf("no organization data found in response")
	}

	org := orgResp.Value[0]
	if org.DirectorySizeQuota.Total == nil || org.DirectorySizeQuota.Used == nil {
		return nil, fmt.Errorf("quota data incomplete: total=%v, used=%v", org.DirectorySizeQuota.Total, org.DirectorySizeQuota.Used)
	}

	quotaTotal := *org.DirectorySizeQuota.Total
	quotaUsed := *org.DirectorySizeQuota.Used

	if quotaTotal <= 0 {
		return nil, fmt.Errorf("invalid quota total: %d", quotaTotal)
	}

	usagePercentage := (quotaUsed * 100) / quotaTotal

	return &QuotaData{
		TenantID:          tenantID,
		TenantName:        org.DisplayName,
		UsagePercentage:   usagePercentage,
		QuotaTotal:        quotaTotal,
		QuotaUsed:         quotaUsed,
		RemainingCapacity: quotaTotal - quotaUsed,
		Timestamp:         time.Now().UTC(),
	}, nil
}

// getSecretFromKeyVault reads a secret from the mounted Key Vault volume
func getSecretFromKeyVault(secretName string) (string, error) {
	secretPath := "/mnt/secrets-store/client-secret"
	secretBytes, err := os.ReadFile(secretPath)
	if err != nil {
		// Fallback: try with the secret name directly
		secretPath = fmt.Sprintf("/mnt/secrets-store/%s", secretName)
		secretBytes, err = os.ReadFile(secretPath)
		if err != nil {
			return "", fmt.Errorf("read secret from %s: %w", secretPath, err)
		}
	}

	return strings.TrimSpace(string(secretBytes)), nil
}
