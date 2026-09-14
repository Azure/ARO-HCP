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

package clients

import (
	"testing"
)

func TestNewACRClient(t *testing.T) {
	tests := []struct {
		name        string
		registryURL string
		useAuth     bool
	}{
		{
			name:        "ACR with authentication enabled",
			registryURL: "myregistry.azurecr.io",
			useAuth:     true,
		},
		{
			name:        "ACR with anonymous access",
			registryURL: "myregistry.azurecr.io",
			useAuth:     false,
		},
		{
			name:        "public ACR with anonymous access",
			registryURL: "kubernetesshared.azurecr.io",
			useAuth:     false,
		},
		{
			name:        "private ACR with auth enabled",
			registryURL: "privateregistry.azurecr.io",
			useAuth:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.useAuth {
				// Authenticated construction is deferred until first use (see
				// getClient), but satisfying RequireAzureTokenCredentials still
				// needs AZURE_TOKEN_CREDENTIALS to have a value; it does not
				// require a live Azure login.
				t.Setenv("AZURE_TOKEN_CREDENTIALS", "prod")
			}

			client, err := NewACRClient(tt.registryURL, tt.useAuth)
			if err != nil {
				t.Errorf("NewACRClient() unexpected error = %v", err)
				return
			}

			if client == nil {
				t.Error("NewACRClient() returned nil client")
				return
			}

			if client.registryURL != tt.registryURL {
				t.Errorf("NewACRClient() registryURL = %v, want %v", client.registryURL, tt.registryURL)
			}

			if !tt.useAuth && client.client == nil {
				t.Error("NewACRClient() client should not be nil")
			}
		})
	}
}

func TestACRClient_GetClient(t *testing.T) {
	tests := []struct {
		name        string
		registryURL string
		useAuth     bool
	}{
		{
			name:        "authenticated client with auth enabled",
			registryURL: "myregistry.azurecr.io",
			useAuth:     true,
		},
		{
			name:        "anonymous client with auth disabled",
			registryURL: "myregistry.azurecr.io",
			useAuth:     false,
		},
		{
			name:        "private ACR with auth enabled",
			registryURL: "privateregistry.azurecr.io",
			useAuth:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.useAuth {
				t.Setenv("AZURE_TOKEN_CREDENTIALS", "prod")
			}

			client, err := NewACRClient(tt.registryURL, tt.useAuth)
			if err != nil {
				t.Fatalf("NewACRClient() unexpected error = %v", err)
			}

			selectedClient, err := client.getClient()
			if err != nil {
				t.Fatalf("getClient() unexpected error = %v", err)
			}

			if selectedClient == nil {
				t.Fatal("getClient() returned nil client")
			}

			if selectedClient != client.client {
				t.Error("getClient() should return the client")
			}
		})
	}
}

func TestACRClient_RegistryURLVariants(t *testing.T) {
	tests := []struct {
		name        string
		registryURL string
		useAuth     bool
	}{
		{
			name:        "standard ACR registry with auth",
			registryURL: "myregistry.azurecr.io",
			useAuth:     true,
		},
		{
			name:        "ACR in different region with auth",
			registryURL: "myregistry.eastus.azurecr.io",
			useAuth:     true,
		},
		{
			name:        "Microsoft shared registry with anonymous access",
			registryURL: "kubernetesshared.azurecr.io",
			useAuth:     false,
		},
		{
			name:        "dev ACR registry with auth",
			registryURL: "arohcpsvcdev.azurecr.io",
			useAuth:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.useAuth {
				t.Setenv("AZURE_TOKEN_CREDENTIALS", "prod")
			}

			client, err := NewACRClient(tt.registryURL, tt.useAuth)
			if err != nil {
				t.Errorf("NewACRClient() failed for %s: %v", tt.registryURL, err)
				return
			}

			if client == nil {
				t.Errorf("NewACRClient() returned nil for %s", tt.registryURL)
				return
			}

			if client.registryURL != tt.registryURL {
				t.Errorf("NewACRClient() registryURL = %v, want %v", client.registryURL, tt.registryURL)
			}

			selectedClient, err := client.getClient()
			if err != nil {
				t.Errorf("getClient() unexpected error for %s: %v", tt.registryURL, err)
				return
			}

			if selectedClient == nil {
				t.Error("getClient() client should be initialized")
			}
		})
	}
}
