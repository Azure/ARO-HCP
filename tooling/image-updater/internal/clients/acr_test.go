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
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/containers/azcontainerregistry"
	"github.com/go-logr/logr"
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

func TestACRClientGetAllTagsUsesListMetadata(t *testing.T) {
	const createdOn = "2026-09-15T10:11:12Z"
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/acr/v1/test/repository/_tags" {
			t.Errorf("unexpected ACR request path %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"registry":"test.azurecr.io","imageName":"test/repository","tags":[{"name":"latest","digest":"sha256:digest","createdTime":"` + createdOn + `","lastUpdateTime":"` + createdOn + `","changeableAttributes":{}}]}`))
	}))
	defer server.Close()

	sdkClient, err := azcontainerregistry.NewClient(server.URL, nil, &azcontainerregistry.ClientOptions{ClientOptions: azcore.ClientOptions{Transport: server.Client()}})
	if err != nil {
		t.Fatalf("failed to create ACR SDK client: %v", err)
	}
	ctx := logr.NewContext(context.Background(), logr.Discard())
	tags, err := (&ACRClient{}).getAllTagsWithClient(ctx, "test/repository", sdkClient)
	if err != nil {
		t.Fatalf("getAllTagsWithClient() unexpected error = %v", err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("ACR requests = %d, want one list request and no per-tag property requests", got)
	}
	if len(tags) != 1 || tags[0].Name != "latest" || tags[0].Digest != "sha256:digest" {
		t.Fatalf("getAllTagsWithClient() tags = %#v", tags)
	}
	wantCreatedOn, err := time.Parse(time.RFC3339, createdOn)
	if err != nil {
		t.Fatalf("failed to parse fixture timestamp: %v", err)
	}
	if !tags[0].LastModified.Equal(wantCreatedOn) {
		t.Fatalf("tag timestamp = %v, want %v", tags[0].LastModified, wantCreatedOn)
	}
}

func TestPrepareACRTagsForArchValidationChecksMatchingCandidates(t *testing.T) {
	now := time.Now()
	tags := []Tag{
		{Name: "unrelated", Digest: "sha256:unrelated"},
		{Name: "abcdef0", Digest: "sha256:matching", LastModified: now},
	}

	prepared, err := prepareACRTagsForArchValidation(tags, "test/repository", `^[a-f0-9]{7}$`)
	if err != nil {
		t.Fatalf("prepareACRTagsForArchValidation() unexpected error = %v", err)
	}
	if len(prepared) != 1 || prepared[0].Name != "abcdef0" {
		t.Fatalf("prepareACRTagsForArchValidation() tags = %#v, want matching candidate", prepared)
	}

	tags[1].LastModified = time.Time{}
	_, err = prepareACRTagsForArchValidation(tags, "test/repository", `^[a-f0-9]{7}$`)
	if err == nil || !strings.Contains(err.Error(), "tag abcdef0") || !strings.Contains(err.Error(), "no creation timestamp") {
		t.Fatalf("prepareACRTagsForArchValidation() error = %v, want matching candidate timestamp failure", err)
	}

	equivalentVersions := []Tag{
		{Name: "v1.2.3+build1", Digest: "sha256:missing"},
		{Name: "v1.2.3+build2", Digest: "sha256:present", LastModified: now},
	}
	_, err = prepareACRTagsForArchValidation(equivalentVersions, "test/repository", `^v\d+\.\d+\.\d+\+build\d+$`)
	if err == nil || !strings.Contains(err.Error(), "v1.2.3+build1") {
		t.Fatalf("prepareACRTagsForArchValidation() error = %v, want tied candidate timestamp failure", err)
	}
}

func TestACRClientDoesNotFallBackAfterCandidateMetadataFailure(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/acr/v1/test/repository/_tags":
			_, _ = w.Write([]byte(`{"registry":"test.azurecr.io","imageName":"test/repository","tags":[{"name":"v1.0.0","digest":"sha256:old","createdTime":"2026-09-14T00:00:00Z","changeableAttributes":{}},{"name":"v2.0.0","digest":"sha256:latest","createdTime":"2026-09-15T00:00:00Z","changeableAttributes":{}}]}`))
		case "/acr/v1/test/repository/_manifests/sha256:latest":
			http.Error(w, `{"errors":[{"code":"UNAVAILABLE","message":"temporary failure"}]}`, http.StatusServiceUnavailable)
		default:
			t.Errorf("unexpected fallback request path %q", r.URL.Path)
			http.Error(w, `{"errors":[{"code":"UNAVAILABLE","message":"unexpected fallback"}]}`, http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()

	sdkClient, err := azcontainerregistry.NewClient(server.URL, nil, &azcontainerregistry.ClientOptions{ClientOptions: azcore.ClientOptions{Transport: server.Client(), Retry: policy.RetryOptions{MaxRetries: -1}}})
	if err != nil {
		t.Fatalf("failed to create ACR SDK client: %v", err)
	}
	client := &ACRClient{client: sdkClient, registryURL: "test.azurecr.io"}
	ctx := logr.NewContext(context.Background(), logr.Discard())
	_, err = client.GetArchSpecificDigest(ctx, "test/repository", `^v\d+\.\d+\.\d+$`, "amd64", false, "")
	if err == nil || !strings.Contains(err.Error(), "candidate tag v2.0.0") {
		t.Fatalf("GetArchSpecificDigest() error = %v, want latest candidate metadata failure", err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("ACR requests = %d, want tag listing plus latest candidate only", got)
	}
}
