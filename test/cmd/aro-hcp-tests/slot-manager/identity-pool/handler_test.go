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

package identitypool

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

	managed := slots.Pool{SubscriptionName: "managed"}
	unmanaged := slots.Pool{
		SubscriptionName:     "unmanaged",
		IdentityProvisioning: slots.IdentityProvisioningUnmanaged,
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
			{"only unmanaged explicit", []slots.Pool{unmanaged}, true, "unmanaged"},
			{"no pools", nil, false, ""},
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
				request := assets.PoolRequest{Environment: "dev", Pools: test.pools, IncludeUnmanaged: test.includeUnmanaged}
				run := handler.ApplyPools
				if operation == "validate" {
					run = handler.ValidatePools
				}
				err := run(context.Background(), request)
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
