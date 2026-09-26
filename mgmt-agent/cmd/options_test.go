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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompleteRequiresCredentialSelection(t *testing.T) {
	// Prevent in-cluster credentials from bypassing the missing kubeconfig.
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")

	for _, selector := range []string{"", "invalid", "WorkloadIdentityCredential"} {
		t.Run(selector, func(t *testing.T) {
			t.Setenv("AZURE_TOKEN_CREDENTIALS", selector)
			if selector == "" {
				if err := os.Unsetenv("AZURE_TOKEN_CREDENTIALS"); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("AZURE_CLIENT_ID", "00000000-1111-2222-3333-444444444444")
			t.Setenv("AZURE_TENANT_ID", "00000000-1111-2222-3333-555555555555")
			t.Setenv("AZURE_FEDERATED_TOKEN_FILE", filepath.Join(t.TempDir(), "token"))
			options := DefaultControllerOptions()
			options.Namespace = "mgmt-agent"
			options.Kubeconfig = filepath.Join(t.TempDir(), "missing-kubeconfig")
			validated, err := options.Validate(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			_, err = validated.Complete(t.Context())
			want := "failed to create Azure credential"
			if selector == "WorkloadIdentityCredential" {
				want = "failed to build kubeconfig"
			}
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("expected %q, got %v", want, err)
			}
		})
	}
}
