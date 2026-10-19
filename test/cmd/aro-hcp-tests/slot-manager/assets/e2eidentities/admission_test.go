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

package e2eidentities

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets/azureutil"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
)

func TestLeaseCredentialUsesSelectedProfileWithoutAzureLogin(t *testing.T) {
	t.Setenv("AZURE_CLIENT_ID", "")
	t.Setenv("AZURE_TENANT_ID", "")
	t.Setenv("AZURE_CLIENT_SECRET", "")
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("CLUSTER_PROFILE_DIR", t.TempDir())
	profile := t.TempDir()
	for name, value := range map[string]string{
		"tenant":        "00000000-0000-0000-0000-000000000001",
		"client-id":     "00000000-0000-0000-0000-000000000002",
		"client-secret": "fake-unit-test-secret",
	} {
		if err := os.WriteFile(filepath.Join(profile, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	request := assets.LeaseRequest{
		SelectedClusterProfileDir: profile,
		AcquiredSlotState: &slots.AcquiredSlotState{Slot: slots.ExpandedSlot{
			Subscriptions: slots.ResolvedSubscriptions{E2E: slots.ResolvedSubscription{ID: "customer-subscription"}},
		}},
	}
	credential, err := azureutil.ClusterProfileCredential(profile)
	if err != nil {
		t.Fatalf("mounted profile must supply credentials without Azure CLI or environment credentials: %v", err)
	}
	if _, ok := credential.(*azidentity.ClientSecretCredential); !ok {
		t.Fatalf("expected explicit selected-profile credential, got %T", credential)
	}
	gotCredential, subscription, err := leaseCredential(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := gotCredential.(*azidentity.ClientSecretCredential); !ok || subscription != "customer-subscription" {
		t.Fatalf("expected explicit selected-profile credential and E2E subscription, got %T, %q", gotCredential, subscription)
	}
	if err := os.Remove(filepath.Join(profile, "client-secret")); err != nil {
		t.Fatal(err)
	}
	if _, err := azureutil.ClusterProfileCredential(profile); !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "client-secret") {
		t.Fatalf("missing mounted credential must fail explicitly, got %v", err)
	}
}
