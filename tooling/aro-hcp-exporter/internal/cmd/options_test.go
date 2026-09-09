// Copyright 2026 Microsoft Corporation
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

package cmd

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/Azure/ARO-HCP/tooling/aro-hcp-exporter/internal/metrics"
)

type fakeTokenCredential struct{}

func (fakeTokenCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func TestValidateRegion(t *testing.T) {
	tests := []struct {
		name            string
		region          string
		wantErrContains string
	}{
		{
			name:   "valid region eastus",
			region: "eastus",
		},
		{
			name:   "valid region westus3",
			region: "westus3",
		},
		{
			name:   "valid region centralus",
			region: "centralus",
		},
		{
			name:   "valid region northeurope",
			region: "northeurope",
		},
		{
			name:   "uppercase is normalized",
			region: "EastUS",
		},
		{
			name:            "empty region",
			region:          "",
			wantErrContains: "region is required",
		},
		{
			name:            "region with spaces",
			region:          "West US 3",
			wantErrContains: "invalid region",
		},
		{
			name:            "region with hyphens",
			region:          "us-east-1",
			wantErrContains: "invalid region",
		},
		{
			name:            "region with single quote",
			region:          "east'us",
			wantErrContains: "invalid region",
		},
		{
			name:            "region with semicolon",
			region:          "eastus;drop",
			wantErrContains: "invalid region",
		},
		{
			name:            "single character region",
			region:          "e",
			wantErrContains: "invalid region",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := DefaultOptions()
			opts.Region = tt.region
			opts.ClusterTypes = []string{"svc-cluster"}

			validated, err := opts.Validate(context.Background())

			if tt.wantErrContains != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrContains)
				return
			}

			require.NoError(t, err)
			assert.Regexp(t, `^[a-z][a-z0-9]+$`, validated.Region)
		})
	}
}

func TestValidateClusterTypes(t *testing.T) {
	tests := []struct {
		name            string
		clusterTypes    []string
		wantErrContains string
		wantTypes       []string
	}{
		{
			name:         "valid single type",
			clusterTypes: []string{"svc-cluster"},
			wantTypes:    []string{"svc-cluster"},
		},
		{
			name:         "valid multiple types",
			clusterTypes: []string{"svc-cluster", "mgmt-cluster"},
			wantTypes:    []string{"svc-cluster", "mgmt-cluster"},
		},
		{
			name:         "valid type with dots and underscores",
			clusterTypes: []string{"my_cluster.v2"},
			wantTypes:    []string{"my_cluster.v2"},
		},
		{
			name:         "whitespace is trimmed",
			clusterTypes: []string{" svc-cluster ", "  mgmt-cluster"},
			wantTypes:    []string{"svc-cluster", "mgmt-cluster"},
		},
		{
			name:            "empty list",
			clusterTypes:    []string{},
			wantErrContains: "cluster-types is required",
		},
		{
			name:            "empty element",
			clusterTypes:    []string{"svc-cluster", ""},
			wantErrContains: "must not contain empty values",
		},
		{
			name:            "whitespace-only element",
			clusterTypes:    []string{"svc-cluster", "  "},
			wantErrContains: "must not contain empty values",
		},
		{
			name:            "type with spaces",
			clusterTypes:    []string{"svc cluster"},
			wantErrContains: "invalid cluster-type",
		},
		{
			name:            "type with single quote",
			clusterTypes:    []string{"svc'cluster"},
			wantErrContains: "invalid cluster-type",
		},
		{
			name:            "type with semicolon",
			clusterTypes:    []string{"type;drop"},
			wantErrContains: "invalid cluster-type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := DefaultOptions()
			opts.ClusterTypes = tt.clusterTypes
			opts.Region = "eastus"

			validated, err := opts.Validate(context.Background())

			if tt.wantErrContains != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrContains)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantTypes, validated.ClusterTypes)
		})
	}
}

func TestValidateClusterNameFilter(t *testing.T) {
	tests := []struct {
		name            string
		filter          string
		wantErrContains string
		wantFilter      string
	}{
		{
			name:       "empty filter is valid",
			filter:     "",
			wantFilter: "",
		},
		{
			name:       "valid environment-region prefix",
			filter:     "cspr-westus3",
			wantFilter: "cspr-westus3",
		},
		{
			name:       "valid pers prefix with short region",
			filter:     "pers-ws3jbol",
			wantFilter: "pers-ws3jbol",
		},
		{
			name:       "whitespace is trimmed",
			filter:     "  cspr-westus3  ",
			wantFilter: "cspr-westus3",
		},
		{
			name:            "filter with single quote",
			filter:          "cspr'westus3",
			wantErrContains: "invalid cluster-name-filter",
		},
		{
			name:            "filter with semicolon",
			filter:          "cspr;drop",
			wantErrContains: "invalid cluster-name-filter",
		},
		{
			name:            "filter with spaces",
			filter:          "cspr westus3",
			wantErrContains: "invalid cluster-name-filter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := DefaultOptions()
			opts.ClusterTypes = []string{"svc-cluster"}
			opts.Region = "eastus"
			opts.ClusterNameFilter = tt.filter

			validated, err := opts.Validate(context.Background())

			if tt.wantErrContains != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrContains)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantFilter, validated.ClusterNameFilter)
		})
	}
}

func TestValidateKeyVaultCertificateOptions(t *testing.T) {
	tests := []struct {
		name                  string
		enabled               bool
		keyVaultName          string
		keyVaultDNSSuffix     string
		certificateNames      []string
		wantErrContains       string
		wantKeyVaultName      string
		wantKeyVaultDNSSuffix string
		wantCertificateNames  []string
	}{
		{
			name:    "disabled collector does not require options",
			enabled: false,
		},
		{
			name:                  "valid options are normalized",
			enabled:               true,
			keyVaultName:          " aro-hcp-dev-svc-kv ",
			keyVaultDNSSuffix:     " VAULT.AZURE.NET ",
			certificateNames:      []string{" frontend-cert-dev-usw3 ", "admin-api-cert-dev-usw3"},
			wantKeyVaultName:      "aro-hcp-dev-svc-kv",
			wantKeyVaultDNSSuffix: "vault.azure.net",
			wantCertificateNames:  []string{"frontend-cert-dev-usw3", "admin-api-cert-dev-usw3"},
		},
		{
			name:                  "minimum length vault name",
			enabled:               true,
			keyVaultName:          "a-1",
			keyVaultDNSSuffix:     "vault.azure.net",
			certificateNames:      []string{"certificate"},
			wantKeyVaultName:      "a-1",
			wantKeyVaultDNSSuffix: "vault.azure.net",
			wantCertificateNames:  []string{"certificate"},
		},
		{
			name:                  "maximum length vault name",
			enabled:               true,
			keyVaultName:          "abcdefghijklmnopqrstuvwx",
			keyVaultDNSSuffix:     "vault.azure.net",
			certificateNames:      []string{"certificate"},
			wantKeyVaultName:      "abcdefghijklmnopqrstuvwx",
			wantKeyVaultDNSSuffix: "vault.azure.net",
			wantCertificateNames:  []string{"certificate"},
		},
		{
			name:              "missing vault name",
			enabled:           true,
			keyVaultDNSSuffix: "vault.azure.net",
			certificateNames:  []string{"certificate"},
			wantErrContains:   "keyvault-name is required",
		},
		{
			name:              "vault name starts with number",
			enabled:           true,
			keyVaultName:      "1vault",
			keyVaultDNSSuffix: "vault.azure.net",
			certificateNames:  []string{"certificate"},
			wantErrContains:   "invalid keyvault-name",
		},
		{
			name:              "vault name starts with hyphen",
			enabled:           true,
			keyVaultName:      "-vault",
			keyVaultDNSSuffix: "vault.azure.net",
			certificateNames:  []string{"certificate"},
			wantErrContains:   "invalid keyvault-name",
		},
		{
			name:              "vault name ends with hyphen",
			enabled:           true,
			keyVaultName:      "vault-",
			keyVaultDNSSuffix: "vault.azure.net",
			certificateNames:  []string{"certificate"},
			wantErrContains:   "invalid keyvault-name",
		},
		{
			name:              "vault name contains consecutive hyphens",
			enabled:           true,
			keyVaultName:      "vault--name",
			keyVaultDNSSuffix: "vault.azure.net",
			certificateNames:  []string{"certificate"},
			wantErrContains:   "invalid keyvault-name",
		},
		{
			name:              "vault name is too short",
			enabled:           true,
			keyVaultName:      "ab",
			keyVaultDNSSuffix: "vault.azure.net",
			certificateNames:  []string{"certificate"},
			wantErrContains:   "invalid keyvault-name",
		},
		{
			name:              "vault name is too long",
			enabled:           true,
			keyVaultName:      "abcdefghijklmnopqrstuvwxy",
			keyVaultDNSSuffix: "vault.azure.net",
			certificateNames:  []string{"certificate"},
			wantErrContains:   "invalid keyvault-name",
		},
		{
			name:             "missing DNS suffix",
			enabled:          true,
			keyVaultName:     "vault",
			certificateNames: []string{"certificate"},
			wantErrContains:  "keyvault-dns-suffix is required",
		},
		{
			name:              "missing certificate names",
			enabled:           true,
			keyVaultName:      "vault",
			keyVaultDNSSuffix: "vault.azure.net",
			wantErrContains:   "certificate-names is required",
		},
		{
			name:              "empty certificate name",
			enabled:           true,
			keyVaultName:      "vault",
			keyVaultDNSSuffix: "vault.azure.net",
			certificateNames:  []string{"certificate", " "},
			wantErrContains:   "must not contain empty values",
		},
		{
			name:              "duplicate certificate name",
			enabled:           true,
			keyVaultName:      "vault",
			keyVaultDNSSuffix: "vault.azure.net",
			certificateNames:  []string{"Certificate", " certificate "},
			wantErrContains:   "duplicate certificate name",
		},
		{
			name:              "invalid certificate name",
			enabled:           true,
			keyVaultName:      "vault",
			keyVaultDNSSuffix: "vault.azure.net",
			certificateNames:  []string{"certificate/name"},
			wantErrContains:   "invalid certificate name",
		},
		{
			name:              "invalid DNS suffix",
			enabled:           true,
			keyVaultName:      "vault",
			keyVaultDNSSuffix: "https://vault.azure.net",
			certificateNames:  []string{"certificate"},
			wantErrContains:   "invalid keyvault-dns-suffix",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := DefaultOptions()
			opts.ClusterTypes = []string{"svc-cluster"}
			opts.Region = "westus3"
			if tt.enabled {
				opts.EnabledCollectors = []string{metrics.KeyVaultCertificateCollectorName}
			}
			opts.KeyVaultName = tt.keyVaultName
			opts.KeyVaultDNSSuffix = tt.keyVaultDNSSuffix
			opts.CertificateNames = tt.certificateNames

			validated, err := opts.Validate(context.Background())
			if tt.wantErrContains != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrContains)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantKeyVaultName, validated.KeyVaultName)
			assert.Equal(t, tt.wantKeyVaultDNSSuffix, validated.KeyVaultDNSSuffix)
			assert.Equal(t, tt.wantCertificateNames, validated.CertificateNames)
		})
	}
}

func TestCreateEnabledCollectorsCreatesKeyVaultCertificateCollector(t *testing.T) {
	opts := &ValidatedOptions{
		Region:            "westus3",
		CacheTTL:          time.Hour,
		EnabledCollectors: []string{metrics.KeyVaultCertificateCollectorName},
		KeyVaultName:      "vault",
		KeyVaultDNSSuffix: "vault.azure.net",
		CertificateNames:  []string{"certificate"},
	}

	collectors, err := opts.CreateEnabledCollectors(context.Background(), fakeTokenCredential{}, nil)

	require.NoError(t, err)
	require.Len(t, collectors, 1)
	assert.Equal(t, metrics.KeyVaultCertificateCollectorName, collectors[0].Name())
}
