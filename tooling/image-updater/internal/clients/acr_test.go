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
	"errors"
	"testing"
)

func TestIsAuthError(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantErr bool
	}{
		{
			name:    "nil error",
			err:     nil,
			wantErr: false,
		},
		{
			name:    "UNAUTHORIZED error",
			err:     errors.New("UNAUTHORIZED access"),
			wantErr: true,
		},
		{
			name:    "401 error",
			err:     errors.New("response 401 from server"),
			wantErr: true,
		},
		{
			name:    "token validation failed",
			err:     errors.New("token validation failed: invalid tenant"),
			wantErr: true,
		},
		{
			name:    "unknown tenantId error",
			err:     errors.New("the received access token has unknown tenantId \"abc123\""),
			wantErr: true,
		},
		{
			name:    "full ACR auth error",
			err:     errors.New("POST https://registry.azurecr.io/oauth2/exchange\nRESPONSE 401: 401 Unauthorized\nerrors: UNAUTHORIZED token validation failed: unknown tenantId"),
			wantErr: true,
		},
		{
			name:    "non-auth error",
			err:     errors.New("network timeout"),
			wantErr: false,
		},
		{
			name:    "404 not found error",
			err:     errors.New("404 not found"),
			wantErr: false,
		},
		{
			name:    "500 internal server error",
			err:     errors.New("500 internal server error"),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAuthError(tt.err)
			if got != tt.wantErr {
				t.Errorf("isAuthError() = %v, want %v for error: %v", got, tt.wantErr, tt.err)
			}
		})
	}
}

func TestNewACRClient(t *testing.T) {
	tests := []struct {
		name             string
		registryURL      string
		useAuth          bool
		wantUseAnonymous bool
	}{
		{
			name:             "ACR with authentication enabled",
			registryURL:      "myregistry.azurecr.io",
			useAuth:          true,
			wantUseAnonymous: false,
		},
		{
			name:             "ACR with authentication disabled",
			registryURL:      "myregistry.azurecr.io",
			useAuth:          false,
			wantUseAnonymous: true,
		},
		{
			name:             "public ACR with auth disabled",
			registryURL:      "kubernetesshared.azurecr.io",
			useAuth:          false,
			wantUseAnonymous: true,
		},
		{
			name:             "private ACR with auth enabled",
			registryURL:      "privateregistry.azurecr.io",
			useAuth:          true,
			wantUseAnonymous: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

			if client.useAnonymous != tt.wantUseAnonymous {
				t.Errorf("NewACRClient() useAnonymous = %v, want %v", client.useAnonymous, tt.wantUseAnonymous)
			}

			// Verify that anonymous client is always created
			if client.anonymousClient == nil {
				t.Error("NewACRClient() anonymousClient should not be nil")
			}

			// Verify authenticated client behavior based on useAuth
			if !tt.useAuth && client.client != nil {
				t.Error("NewACRClient() with useAuth=false should not create authenticated client")
			}
		})
	}
}

func TestACRClient_GetClient(t *testing.T) {
	tests := []struct {
		name             string
		registryURL      string
		useAuth          bool
		forceAnonymous   bool
		wantAnonymous    bool
	}{
		{
			name:          "authenticated client with auth enabled",
			registryURL:   "myregistry.azurecr.io",
			useAuth:       true,
			wantAnonymous: false,
		},
		{
			name:          "anonymous client with auth disabled",
			registryURL:   "myregistry.azurecr.io",
			useAuth:       false,
			wantAnonymous: true,
		},
		{
			name:           "fallback to anonymous after auth failure",
			registryURL:    "myregistry.azurecr.io",
			useAuth:        true,
			forceAnonymous: true,
			wantAnonymous:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewACRClient(tt.registryURL, tt.useAuth)
			if err != nil {
				t.Fatalf("NewACRClient() unexpected error = %v", err)
			}

			// Simulate fallback to anonymous if requested
			if tt.forceAnonymous {
				client.useAnonymous = true
			}

			selectedClient := client.getClient()

			if selectedClient == nil {
				t.Fatal("getClient() returned nil client")
			}

			// Verify the correct client was selected
			if tt.wantAnonymous {
				if selectedClient != client.anonymousClient {
					t.Error("getClient() should return anonymous client")
				}
			} else {
				if client.client != nil && selectedClient != client.client {
					t.Error("getClient() should return authenticated client when available")
				}
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
			name:        "standard ACR registry",
			registryURL: "myregistry.azurecr.io",
			useAuth:     true,
		},
		{
			name:        "ACR in different region",
			registryURL: "myregistry.eastus.azurecr.io",
			useAuth:     true,
		},
		{
			name:        "Microsoft shared registry",
			registryURL: "kubernetesshared.azurecr.io",
			useAuth:     false,
		},
		{
			name:        "dev ACR registry",
			registryURL: "arohcpsvcdev.azurecr.io",
			useAuth:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

			// Verify clients are initialized correctly
			if client.anonymousClient == nil {
				t.Error("NewACRClient() anonymousClient should always be created")
			}

			if tt.useAuth && client.useAnonymous {
				t.Error("NewACRClient() with useAuth=true should not set useAnonymous initially")
			}

			if !tt.useAuth && !client.useAnonymous {
				t.Error("NewACRClient() with useAuth=false should set useAnonymous")
			}
		})
	}
}

func TestACRClient_AuthenticationFallbackScenarios(t *testing.T) {
	tests := []struct {
		name                string
		useAuth             bool
		simulateAuthFailure bool
		wantUseAnonymous    bool
	}{
		{
			name:                "auth enabled - no failures",
			useAuth:             true,
			simulateAuthFailure: false,
			wantUseAnonymous:    false,
		},
		{
			name:                "auth enabled - simulated auth failure",
			useAuth:             true,
			simulateAuthFailure: true,
			wantUseAnonymous:    true,
		},
		{
			name:                "auth disabled from start",
			useAuth:             false,
			simulateAuthFailure: false,
			wantUseAnonymous:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewACRClient("myregistry.azurecr.io", tt.useAuth)
			if err != nil {
				t.Fatalf("NewACRClient() unexpected error = %v", err)
			}

			// Simulate auth failure by setting useAnonymous flag
			if tt.simulateAuthFailure {
				client.useAnonymous = true
			}

			if client.useAnonymous != tt.wantUseAnonymous {
				t.Errorf("useAnonymous = %v, want %v", client.useAnonymous, tt.wantUseAnonymous)
			}

			// Verify that the correct client would be returned
			selectedClient := client.getClient()
			if selectedClient == nil {
				t.Fatal("getClient() returned nil")
			}

			if tt.wantUseAnonymous {
				if selectedClient != client.anonymousClient {
					t.Error("Expected anonymous client to be selected")
				}
			}
		})
	}
}
