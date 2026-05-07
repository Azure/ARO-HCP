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

package azsdk

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/internal/version"
)

type fakeCredential struct{}

func (f *fakeCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{
		Token:     "fake-token",
		ExpiresOn: time.Now().Add(1 * time.Hour),
	}, nil
}

func TestFirstN(t *testing.T) {
	tests := []struct {
		name     string
		str      string
		n        int
		expected string
	}{
		{
			name:     "string shorter than n",
			str:      "hello",
			n:        10,
			expected: "hello",
		},
		{
			name:     "string equal to n",
			str:      "hello",
			n:        5,
			expected: "hello",
		},
		{
			name:     "string longer than n",
			str:      "hello world",
			n:        5,
			expected: "hello",
		},
		{
			name:     "empty string",
			str:      "",
			n:        5,
			expected: "",
		},
		{
			name:     "n is zero",
			str:      "hello",
			n:        0,
			expected: "",
		},
		{
			name:     "n is negative",
			str:      "hello",
			n:        -1,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := firstN(tt.str, tt.n)
			if result != tt.expected {
				t.Errorf("firstN(%q, %d) = %q, expected %q", tt.str, tt.n, result, tt.expected)
			}
		})
	}
}

func TestApplicationID(t *testing.T) {
	tests := []struct {
		name      string
		component Component
		commitSHA string
		expected  string
	}{
		{
			name:      "short SHA fits within limit",
			component: ComponentFrontend,
			commitSHA: "abc123",
			expected:  "frontend/abc123",
		},
		{
			name:      "long SHA gets truncated to 24 chars",
			component: ComponentBackend,
			commitSHA: "abcdef1234567890abcdef12",
			expected:  "backend/abcdef1234567890",
		},
		{
			name:      "default development value",
			component: ComponentAdmin,
			commitSHA: "development",
			expected:  "admin/development",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := version.CommitSHA
			version.CommitSHA = tt.commitSHA
			t.Cleanup(func() { version.CommitSHA = original })

			result := ApplicationID(tt.component)
			if result != tt.expected {
				t.Errorf("ApplicationID(%q) = %q, expected %q", tt.component, result, tt.expected)
			}
			if len(result) > 24 {
				t.Errorf("ApplicationID(%q) length %d exceeds 24-char limit", tt.component, len(result))
			}
		})
	}
}

func TestClientOptionsUserAgentHeader(t *testing.T) {
	expectedValue := ApplicationID(ComponentFrontend)

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userAgent := r.Header.Get("User-Agent")
		if !strings.Contains(userAgent, expectedValue) {
			t.Errorf("expected User-Agent to contain %q, got %q", expectedValue, userAgent)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	clientOptions := NewClientOptions(ComponentFrontend)
	clientOptions.Cloud = cloud.AzurePublic
	clientOptions.Transport = ts.Client()
	clientOptions.Cloud.Services = map[cloud.ServiceName]cloud.ServiceConfiguration{
		cloud.ResourceManager: {
			Endpoint: ts.URL,
			Audience: "test",
		},
	}

	rgClient, err := armresources.NewResourceGroupsClient("test-subscription", &fakeCredential{}, &azcorearm.ClientOptions{
		ClientOptions: clientOptions,
	})
	if err != nil {
		t.Fatalf("failed to create ResourceGroupsClient: %v", err)
	}

	_, err = rgClient.Get(context.Background(), "test-rg", nil)
	if err != nil {
		t.Fatalf("failed to make Get request: %v", err)
	}
}
