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

package tenant

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

	"github.com/Azure/ARO-HCP/dev-infrastructure/sre-tooling/tenant-quota/pkg/auth"
	"github.com/Azure/ARO-HCP/dev-infrastructure/sre-tooling/tenant-quota/pkg/config"
)

// CollectQuotaFunc returns a function that collects quota data and formats it.
// This is used by the collector package to register the collector.
// It detects which tenant the pod is running in, then uses the matching credentials from config.
func CollectQuotaFunc() func(ctx config.CollectorContext) (string, error) {
	return func(ctx config.CollectorContext) (string, error) {
		return CollectQuota(ctx)
	}
}

// QuotaData represents the tenant quota information returned from Microsoft Graph API
type QuotaData struct {
	TenantID          string
	TenantName        string
	UsagePercentage   int
	QuotaTotal        int
	QuotaUsed         int
	RemainingCapacity int
	Timestamp         time.Time
}

// organizationResponse represents the Microsoft Graph organization API response
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

// CollectQuota collects tenant quota information from Microsoft Graph API.
func CollectQuota(ctx config.CollectorContext) (string, error) {
	logger, _ := ctx.Logger.(*slog.Logger)
	if logger == nil {
		handler := slog.NewJSONHandler(os.Stderr, nil)
		logger = slog.New(handler)
	}

	ctxContext, ok := ctx.Context.(context.Context)
	if !ok {
		return "", fmt.Errorf("invalid context type")
	}

	return collectSingleTenant(ctx, ctxContext, logger)
}

// collectSingleTenant collects quota for the tenant the pod is currently running in.
func collectSingleTenant(ctx config.CollectorContext, ctxContext context.Context, logger *slog.Logger) (string, error) {
	// First, get an initial token to detect which tenant we're in
	// Use default credential to get a token (Workload Identity, Managed Identity, etc.)
	initialCred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return "", fmt.Errorf("create initial credential: %w", err)
	}

	scope := "https://graph.microsoft.com/.default"
	if ctx.Auth != nil && ctx.Auth.Scope != "" {
		scope = ctx.Auth.Scope
	}

	// Get initial token to detect tenant
	initialToken, err := initialCred.GetToken(ctxContext, policy.TokenRequestOptions{
		Scopes: []string{scope},
	})
	if err != nil {
		return "", fmt.Errorf("get initial token: %w", err)
	}

	// Extract tenant ID from token
	detectedTenantID, err := auth.ExtractTenantIDFromToken(initialToken.Token)
	if err != nil {
		return "", fmt.Errorf("extract tenant ID from token: %w", err)
	}

	logger.Debug("Detected tenant ID from token", "tenant_id", detectedTenantID)

	// Look up matching tenant config from the 'tenants' array (if configured)
	var selectedTenant *config.TenantConfig
	if len(ctx.Tenants) > 0 {
		for i := range ctx.Tenants {
			if ctx.Tenants[i].TenantID == detectedTenantID {
				selectedTenant = &ctx.Tenants[i]
				logger.Debug("Found matching tenant config", "tenant_id", detectedTenantID, "tenant_name", selectedTenant.TenantName)
				break
			}
		}
		if selectedTenant == nil {
			logger.Warn("Tenant not found in 'tenants' config array, falling back to per-collector auth or default", "tenant_id", detectedTenantID)
		}
	}

	// If we found a matching tenant config, use its credentials
	if selectedTenant != nil {
		secret, err := getSecretFromKeyVault(selectedTenant.KeyVaultSecretName)
		if err != nil {
			return "", fmt.Errorf("get secret from vault for tenant %s: %w", detectedTenantID, err)
		}

		tenantScope := selectedTenant.Scope
		if tenantScope == "" {
			tenantScope = scope
		}

		cred, err := azidentity.NewClientSecretCredential(
			selectedTenant.TenantID,
			selectedTenant.ServicePrincipalClientId,
			secret,
			nil,
		)
		if err != nil {
			return "", fmt.Errorf("create credential for tenant %s: %w", detectedTenantID, err)
		}

		// Get token with the selected tenant's credentials
		token, err := cred.GetToken(ctxContext, policy.TokenRequestOptions{
			Scopes: []string{tenantScope},
		})
		if err != nil {
			return "", fmt.Errorf("get token for tenant %s: %w", detectedTenantID, err)
		}

		// Collect quota data from the current tenant
		quotaData, err := collectQuotaForTenant(ctxContext, token.Token)
		if err != nil {
			return "", fmt.Errorf("collect quota for tenant %s: %w", detectedTenantID, err)
		}

		logger.Debug("Successfully collected quota for tenant", "tenant_id", detectedTenantID, "tenant_name", selectedTenant.TenantName)
		return FormatOutput(quotaData), nil
	}

	// No matching tenant config found, fall back to per-collector auth or default
	if ctx.Auth != nil && ctx.Auth.ServicePrincipalClientId != "" {
		logger.Debug("No matching tenant config, using per-collector auth", "tenant_id", detectedTenantID)
		secret, err := getSecretFromKeyVault(ctx.Auth.KeyVaultSecretName)
		if err != nil {
			return "", fmt.Errorf("get secret from vault: %w", err)
		}

		tenantID := ctx.Auth.TenantID
		if tenantID == "" {
			tenantID = detectedTenantID // Use detected tenant ID if not specified
		}

		cred, err := azidentity.NewClientSecretCredential(
			tenantID,
			ctx.Auth.ServicePrincipalClientId,
			secret,
			nil,
		)
		if err != nil {
			return "", fmt.Errorf("create credential: %w", err)
		}

		// Get token with per-collector auth credentials
		token, err := cred.GetToken(ctxContext, policy.TokenRequestOptions{
			Scopes: []string{scope},
		})
		if err != nil {
			return "", fmt.Errorf("get token: %w", err)
		}

		// Collect quota data
		quotaData, err := collectQuotaForTenant(ctxContext, token.Token)
		if err != nil {
			return "", fmt.Errorf("collect quota: %w", err)
		}

		logger.Debug("Successfully collected quota for tenant using per-collector auth", "tenant_id", detectedTenantID)
		return FormatOutput(quotaData), nil
	}

	logger.Debug("No matching tenant config, using default credential", "tenant_id", detectedTenantID)
	quotaData, err := collectQuotaForTenant(ctxContext, initialToken.Token)
	if err != nil {
		return "", fmt.Errorf("collect quota: %w", err)
	}

	logger.Debug("Successfully collected quota for tenant using default credential", "tenant_id", detectedTenantID)
	return FormatOutput(quotaData), nil
}

// collectQuotaForTenant makes the Graph API call and parses the response
func collectQuotaForTenant(ctx context.Context, tokenString string) (*QuotaData, error) {
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
		TenantID:          org.ID,
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
	// Secrets are mounted at /mnt/secrets-store/{objectAlias}
	// For SecretProviderClass, it uses objectAlias = "client-secret"
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

// FormatOutput formats the quota data as key=value pairs for the collector
func FormatOutput(data *QuotaData) string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("TENANT_ID=%s\n", data.TenantID))
	builder.WriteString(fmt.Sprintf("TENANT_NAME=%s\n", data.TenantName))
	builder.WriteString(fmt.Sprintf("USAGE_PERCENTAGE=%d\n", data.UsagePercentage))
	builder.WriteString(fmt.Sprintf("QUOTA_TOTAL=%d\n", data.QuotaTotal))
	builder.WriteString(fmt.Sprintf("QUOTA_USED=%d\n", data.QuotaUsed))
	builder.WriteString(fmt.Sprintf("REMAINING_CAPACITY=%d\n", data.RemainingCapacity))
	builder.WriteString(fmt.Sprintf("TIMESTAMP=%s\n", data.Timestamp.Format(time.RFC3339)))
	return builder.String()
}
