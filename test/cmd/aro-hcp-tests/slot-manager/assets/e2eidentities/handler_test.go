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
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
)

func TestPoolHandlersShareSelectionPolicy(t *testing.T) {
	t.Parallel()

	managed := slots.Pool{
		Subscriptions: slots.PoolSubscriptions{E2E: "managed"},
		SlotAssets:    slots.SlotAssets{E2EIdentities: &slots.E2EIdentitiesAsset{}},
	}
	unmanaged := slots.Pool{
		Subscriptions: slots.PoolSubscriptions{E2E: "unmanaged"},
		SlotAssets: slots.SlotAssets{E2EIdentities: &slots.E2EIdentitiesAsset{
			Provisioning: slots.IdentityProvisioningUnmanaged,
		}},
	}
	for _, operation := range []string{"apply", "validate"} {
		for _, test := range []struct {
			name             string
			pools            []slots.Pool
			includeUnmanaged bool
			wantSubscription string
		}{
			{"default skips unmanaged", []slots.Pool{unmanaged, managed}, false, "managed"},
			{"explicit selection includes unmanaged", []slots.Pool{unmanaged, managed}, true, "unmanaged"},
			{"only unmanaged default", []slots.Pool{unmanaged}, false, ""},
		} {
			t.Run(operation+"/"+test.name, func(t *testing.T) {
				// Stop at subscription resolution so neither operation can reach Azure.
				stop := errors.New("stop before Azure operation")
				resolved := ""
				handler := &Handler{poolDependencies: func() (azcore.TokenCredential, subscriptionIDResolverFunc, error) {
					return nil, func(_ context.Context, name string) (string, error) {
						resolved = name
						return "", stop
					}, nil
				}}
				registry, err := assets.NewRegistry(handler)
				if err != nil {
					t.Fatal(err)
				}
				request := assets.PoolRequest{Environment: "dev", Pools: test.pools, IncludeUnmanaged: test.includeUnmanaged}
				run := registry.ApplyPools
				if operation == "validate" {
					run = registry.ValidatePools
				}
				err = run(context.Background(), request)
				if resolved != test.wantSubscription {
					t.Fatalf("resolved subscription %q, want %q", resolved, test.wantSubscription)
				}
				if test.wantSubscription == "" {
					if err == nil || !strings.Contains(err.Error(), `no E2E identity pools matched environment "dev"`) {
						t.Fatalf("expected no-selection error, got %v", err)
					}
				} else if !errors.Is(err, stop) {
					t.Fatalf("expected resolver failure before Azure operations, got %v", err)
				}
			})
		}
	}

}

func TestHandlerPublishesIdentityGroups(t *testing.T) {
	t.Parallel()

	request := assets.LeaseRequest{AcquiredSlotState: &slots.AcquiredSlotState{Slot: slots.ExpandedSlot{
		Assets: slots.ResolvedAssets{E2EIdentities: &slots.ResolvedE2EIdentitiesAsset{
			ResourceGroups: []string{"identity-rg-02", "identity-rg-01"},
		}},
	}}}
	request.AcquiredSlotState.AdmittedIdentityContainers = []string{"identity-rg-02"}
	contract := slots.NewRuntimeContractBuilder()
	if err := NewHandler().PublishLease(context.Background(), request, contract); err != nil {
		t.Fatalf("publishing identity groups: %v", err)
	}
	if got, want := string(contract.MarshalShell()), "export ARO_HCP_IDENTITY_CONSUMER_GUARD='enforce'\nexport LEASED_MSI_CONTAINERS='identity-rg-02'\n"; got != want {
		t.Fatalf("identity export = %q, want %q", got, want)
	}
	if err := contract.Add("other", "LEASED_MSI_CONTAINERS", ""); err == nil || !strings.Contains(err.Error(), `already owned by "e2e_identities"`) {
		t.Fatalf("identity export must be owned by e2e_identities, got %v", err)
	}
	request.IdentityConsumerGuardMode = "audit"
	contract = slots.NewRuntimeContractBuilder()
	if err := NewHandler().PublishLease(context.Background(), request, contract); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contract.MarshalShell()), "export ARO_HCP_IDENTITY_CONSUMER_GUARD='audit'\n") {
		t.Fatal("audit mode was not propagated to the E2E test process")
	}
	request.AcquiredSlotState.AdmittedIdentityContainers = nil
	if err := NewHandler().PublishLease(context.Background(), request, slots.NewRuntimeContractBuilder()); err == nil {
		t.Fatal("unadmitted containers were published")
	}
}
