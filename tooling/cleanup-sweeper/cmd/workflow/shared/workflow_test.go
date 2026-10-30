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

package shared

import (
	"context"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azpolicy "github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type fakeTokenCredential struct{}

func (fakeTokenCredential) GetToken(context.Context, azpolicy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{}, nil
}

func TestNewGraphCredential(t *testing.T) {
	fallback := fakeTokenCredential{}

	t.Run("falls back when no GRAPH_AZURE_* variables are set", func(t *testing.T) {
		t.Setenv("GRAPH_AZURE_TENANT_ID", "")
		t.Setenv("GRAPH_AZURE_CLIENT_ID", "")
		t.Setenv("GRAPH_AZURE_CLIENT_SECRET", "")

		got, err := newGraphCredential(fallback)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if got != fallback {
			t.Fatalf("expected fallback credential to be returned")
		}
	})

	t.Run("errors when GRAPH_AZURE_* variables are partially set", func(t *testing.T) {
		t.Setenv("GRAPH_AZURE_TENANT_ID", "00000000-0000-0000-0000-000000000000")
		t.Setenv("GRAPH_AZURE_CLIENT_ID", "")
		t.Setenv("GRAPH_AZURE_CLIENT_SECRET", "secret")

		_, err := newGraphCredential(fallback)
		if err == nil {
			t.Fatalf("expected error for partial configuration")
		}
		if !strings.Contains(err.Error(), "must all be set") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("builds a dedicated credential when all GRAPH_AZURE_* variables are set", func(t *testing.T) {
		t.Setenv("GRAPH_AZURE_TENANT_ID", "00000000-0000-0000-0000-000000000000")
		t.Setenv("GRAPH_AZURE_CLIENT_ID", "11111111-1111-1111-1111-111111111111")
		t.Setenv("GRAPH_AZURE_CLIENT_SECRET", "secret")

		got, err := newGraphCredential(fallback)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if got == nil {
			t.Fatalf("expected a credential")
		}
		if got == azcore.TokenCredential(fallback) {
			t.Fatalf("expected a dedicated credential, not the fallback")
		}
	})
}
