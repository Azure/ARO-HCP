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

package infrastructureidentities

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
)

func TestHandlerIsOptIn(t *testing.T) {
	t.Parallel()
	handler := NewHandler()
	if handler.Declared(slots.Pool{}) {
		t.Fatal("undeclared pools must not activate the handler")
	}
	if !handler.Declared(slots.Pool{SlotAssets: slots.SlotAssets{InfrastructureIdentities: &slots.LeasedAsset{}}}) {
		t.Fatal("declared pools must activate the handler")
	}
	if handler.Declared(slots.Pool{SlotAssets: slots.SlotAssets{InfrastructureIdentities: &slots.LeasedAsset{UnitsPerSlot: 2}}}) {
		t.Fatal("two leased bundles per slot must be rejected before acquisition")
	}
	registry, err := assets.NewRegistry(handler)
	if err != nil {
		t.Fatal(err)
	}
	pool := slots.Pool{SlotAssets: slots.SlotAssets{InfrastructureIdentities: &slots.LeasedAsset{UnitsPerSlot: 2}}}
	if err := registry.ValidateRequirements([]slots.Pool{pool}); err == nil || !strings.Contains(err.Error(), "does not accept demanded asset") {
		t.Fatalf("registry accepted unsupported bundle count: %v", err)
	}
}

func TestAcquireLeaseResolvesOneBundle(t *testing.T) {
	t.Parallel()
	request := assets.LeaseRequest{AcquiredSlotState: &slots.AcquiredSlotState{
		Leases: slots.LeaseSet{Assets: map[slots.AssetKind][]slots.Lease{
			slots.KindInfrastructureIdentities: {{ResourceType: "bundle-type", ResourceName: "bundle-03"}},
		}},
	}}
	if err := NewHandler().AcquireLease(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	got := request.AcquiredSlotState.Slot.Assets.InfrastructureIdentities
	if got == nil || got.Allocation != slots.AllocationLeased || !reflect.DeepEqual(got.ResourceGroups, []string{"bundle-03"}) {
		t.Fatalf("resolved asset = %+v", got)
	}
	if len(got.Identities) != 30 {
		t.Fatalf("expected complete identity inventory, got %d names", len(got.Identities))
	}
}

func TestAcquireLeaseRejectsWrongUnitCount(t *testing.T) {
	t.Parallel()
	handler := NewHandler()
	for _, leases := range [][]slots.Lease{nil, {{ResourceName: "a"}, {ResourceName: "b"}}} {
		request := assets.LeaseRequest{AcquiredSlotState: &slots.AcquiredSlotState{
			Leases: slots.LeaseSet{Assets: map[slots.AssetKind][]slots.Lease{slots.KindInfrastructureIdentities: leases}},
		}}
		if err := handler.AcquireLease(context.Background(), request); err == nil || !strings.Contains(err.Error(), "exactly one leased bundle") {
			t.Fatalf("expected unit-count error, got %v", err)
		}
	}
}

func TestPublishLeaseOwnsBundleExport(t *testing.T) {
	t.Parallel()
	request := assets.LeaseRequest{AcquiredSlotState: &slots.AcquiredSlotState{Slot: slots.ExpandedSlot{
		Assets: slots.ResolvedAssets{InfrastructureIdentities: &slots.ResolvedInfrastructureIdentities{
			Allocation: slots.AllocationLeased, ResourceGroups: []string{"bundle-03"}, Identities: []string{"service-control-plane"},
		}},
	}}}
	contract := slots.NewRuntimeContractBuilder()
	if err := NewHandler().PublishLease(context.Background(), request, contract); err != nil {
		t.Fatal(err)
	}
	if err := contract.Add("other", leaseenv.InfrastructureIdentityBundle, ""); err == nil {
		t.Fatal("bundle export ownership must not be reassigned")
	}
	sharedDir := t.TempDir()
	if err := slots.WriteRuntimeContract(sharedDir, contract); err != nil {
		t.Fatal(err)
	}
	envFile, err := slots.EnvFile(sharedDir)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-eu", "-c", `. "$1"; printf "%s" "$LEASED_INFRASTRUCTURE_IDENTITY_BUNDLE"`, "sh", envFile)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sourcing contract: %v", err)
	}
	var got leaseenv.InfrastructureIdentityBundlePayload
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("published bundle %q: %v", output, err)
	}
	want, err := DefaultTopology().Bundle("bundle-03")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("published bundle %+v, want %+v", got, want)
	}
}

func TestHandlerCleanupWorkersOption(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		entries []string
		want    int
		wantErr string
	}{
		{name: "default is serial", want: 1},
		{name: "set", entries: []string{"infrastructure_identities.cleanup-workers=3"}, want: 3},
		{name: "zero", entries: []string{"infrastructure_identities.cleanup-workers=0"}, want: 1, wantErr: "must be at least 1"},
		{name: "negative", entries: []string{"infrastructure_identities.cleanup-workers=-3"}, want: 1, wantErr: "must be at least 1"},
		{name: "not a number", entries: []string{"infrastructure_identities.cleanup-workers=two"}, want: 1, wantErr: "invalid integer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			handler := NewHandler()
			registry, err := assets.NewRegistry(handler)
			if err != nil {
				t.Fatal(err)
			}
			err = registry.ApplyOptions(tc.entries)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if handler.cleanupWorkers != tc.want {
				t.Fatalf("expected %d cleanup workers, got %d", tc.want, handler.cleanupWorkers)
			}
		})
	}
}

func TestPoolHandlersSkipUnmanagedUnlessSelected(t *testing.T) {
	t.Parallel()
	managed := slots.AssetInventory{AssetPool: slots.AssetPool{
		Name: "managed", Kind: slots.KindInfrastructureIdentities, Provisioning: slots.AssetProvisioningManaged,
		InfrastructureIdentities: &slots.InfrastructureIdentitiesConfig{ProvisioningRegion: "westus3"},
	}, SubscriptionName: "infra"}
	unmanaged := slots.AssetInventory{AssetPool: slots.AssetPool{
		Name: "unmanaged", Kind: slots.KindInfrastructureIdentities, Provisioning: slots.IdentityProvisioningUnmanaged,
		InfrastructureIdentities: &slots.InfrastructureIdentitiesConfig{ProvisioningRegion: "westus3"},
	}, SubscriptionName: "infra"}
	stop := errors.New("stop before Azure operation")
	for _, test := range []struct {
		name             string
		inventories      []slots.AssetInventory
		includeUnmanaged bool
		want             string
	}{
		{"default skips unmanaged", []slots.AssetInventory{unmanaged, managed}, false, "infra"},
		{"explicit selection includes unmanaged", []slots.AssetInventory{unmanaged}, true, "infra"},
		{"only unmanaged default", []slots.AssetInventory{unmanaged}, false, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolved := ""
			handler := NewHandler(WithPoolDependencies(func() (azcore.TokenCredential, subscriptionIDResolverFunc, error) {
				return nil, func(_ context.Context, name string) (string, error) {
					resolved = name
					return "", stop
				}, nil
			}))
			err := handler.ApplyPools(context.Background(), assets.PoolRequest{
				Environment: "dev", AssetInventories: test.inventories, IncludeUnmanaged: test.includeUnmanaged,
			})
			if resolved != test.want {
				t.Fatalf("resolved subscription %q, want %q", resolved, test.want)
			}
			if test.want == "" {
				if err == nil || !strings.Contains(err.Error(), `no infrastructure identity pools matched environment "dev"`) {
					t.Fatalf("expected no-selection error, got %v", err)
				}
			} else if !errors.Is(err, stop) {
				t.Fatalf("expected resolver failure before Azure operations, got %v", err)
			}
		})
	}
}

func TestLeaseCredentialUsesInfrastructureSubscription(t *testing.T) {
	t.Setenv("AZURE_CLIENT_ID", "")
	t.Setenv("AZURE_TENANT_ID", "")
	t.Setenv("AZURE_CLIENT_SECRET", "")
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
			Subscriptions: slots.ResolvedSubscriptions{
				E2E:            slots.ResolvedSubscription{ID: "customer-subscription"},
				Infrastructure: slots.ResolvedSubscription{ID: "infra-subscription"},
			},
		}},
	}
	_, subscription, err := leaseCredential(request)
	if err != nil {
		t.Fatal(err)
	}
	if subscription != "infra-subscription" {
		t.Fatalf("admission must use the infrastructure subscription, got %q", subscription)
	}
}
