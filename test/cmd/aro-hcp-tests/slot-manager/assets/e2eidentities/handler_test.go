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
	"os/exec"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
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
				handler := NewHandler(WithPoolDependencies(func() (azcore.TokenCredential, subscriptionIDResolverFunc, error) {
					return nil, func(_ context.Context, name string) (string, error) {
						resolved = name
						return "", stop
					}, nil
				}))
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

func TestHandlerOwnsIdentityRuntimeExports(t *testing.T) {
	t.Setenv("ARO_HCP_SUITE_PARALLELISM", "99")

	request := assets.LeaseRequest{AcquiredSlotState: &slots.AcquiredSlotState{Slot: slots.ExpandedSlot{
		DeployEnvironment:      "int",
		IdentityContainerCount: 10,
		Assets: slots.ResolvedAssets{E2EIdentities: &slots.ResolvedE2EIdentitiesAsset{
			ResourceGroups: []string{"identity-rg-02", "identity-rg-01"},
		}},
	}}}
	contract := slots.NewRuntimeContractBuilder()
	if err := NewHandler().PublishLease(context.Background(), request, contract); err != nil {
		t.Fatalf("publishing identity groups: %v", err)
	}
	if err := contract.Add("other", leaseenv.MSIContainers, ""); err == nil {
		t.Fatal("identity export ownership must not be reassigned")
	}
	sharedDir := t.TempDir()
	if err := slots.WriteRuntimeContract(sharedDir, contract); err != nil {
		t.Fatal(err)
	}
	envFile, err := slots.EnvFile(sharedDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, setup, want string
	}{
		{"unset", "unset ARO_HCP_SUITE_PARALLELISM", ":\n:"},
		{"exported caller override", "export ARO_HCP_SUITE_PARALLELISM=7", "x:7\nx:7"},
		{"unexported caller override", "ARO_HCP_SUITE_PARALLELISM=7", "x:7\n:"},
		{"explicit empty", "export ARO_HCP_SUITE_PARALLELISM=''", "x:\nx:"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cmd := exec.Command("sh", "-eu", "-c", `unset ARO_HCP_SUITE_PARALLELISM; `+test.setup+`; LEASED_MSI_CONTAINERS=stale; . "$1"; printf "%s\n%s:%s\n" "$LEASED_MSI_CONTAINERS" "${ARO_HCP_SUITE_PARALLELISM+x}" "${ARO_HCP_SUITE_PARALLELISM-}"; sh -eu -c 'printf "%s:%s" "${ARO_HCP_SUITE_PARALLELISM+x}" "${ARO_HCP_SUITE_PARALLELISM-}"'`, "sh", envFile)
			output, err := cmd.CombinedOutput()
			want := "identity-rg-02 identity-rg-01\n" + test.want
			if err != nil || string(output) != want {
				t.Fatalf("sourced identity contract = %q, want %q: %v", output, want, err)
			}
		})
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
		{name: "set", entries: []string{"e2e_identities.cleanup-workers=2"}, want: 2},
		{name: "zero", entries: []string{"e2e_identities.cleanup-workers=0"}, want: 1, wantErr: "must be at least 1"},
		{name: "negative", entries: []string{"e2e_identities.cleanup-workers=-3"}, want: 1, wantErr: "must be at least 1"},
		{name: "not a number", entries: []string{"e2e_identities.cleanup-workers=two"}, want: 1, wantErr: "invalid integer"},
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
