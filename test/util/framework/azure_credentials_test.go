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

package framework

import (
	"context"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

func TestAzureCredentialRejectsAmbiguousAutomationSelection(t *testing.T) {
	for _, tt := range []struct {
		name        string
		development bool
		selector    string
		wantError   string
	}{
		{name: "production requires selection", wantError: "AZURE_TOKEN_CREDENTIALS"},
		{name: "explicit invalid selector overrides development secret", development: true, selector: "invalid-credential", wantError: "AZURE_TOKEN_CREDENTIALS"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AZURE_TOKEN_CREDENTIALS", tt.selector)
			t.Setenv("AZURE_TENANT_ID", "64dc69e4-d083-49fc-9569-ebece1dd1408")
			t.Setenv("AZURE_CLIENT_ID", "11111111-1111-4111-8111-111111111111")
			t.Setenv("AZURE_CLIENT_SECRET", "unused-test-secret")
			_, err := NewAzureCredential(tt.development, "22222222-2222-4222-8222-222222222222")
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("credential selection error = %v, want %q", err, tt.wantError)
			}
		})
	}
}

func TestAzureCredentialDoesNotFallbackFromExplicitEnvironmentToCLI(t *testing.T) {
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "EnvironmentCredential")
	for _, name := range []string{"AZURE_TENANT_ID", "AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET", "AZURE_CLIENT_CERTIFICATE_PATH", "AZURE_USERNAME", "AZURE_PASSWORD", "AZURE_FEDERATED_TOKEN_FILE"} {
		t.Setenv(name, "")
	}
	credential, err := NewAzureCredential(true, "11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	_, err = credential.GetToken(context.Background(), policy.TokenRequestOptions{Scopes: []string{"https://management.azure.com/.default"}})
	if err == nil || !strings.Contains(err.Error(), "EnvironmentCredential") || strings.Contains(err.Error(), "AzureCLICredential") {
		t.Fatalf("selected environment credential must fail without CLI fallback, got %v", err)
	}
}
