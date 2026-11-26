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

package mockidentities

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/catalog"
	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
)

func newFakeStore() *fakeStore {
	return &fakeStore{principals: map[string]leaseenv.MockPrincipal{}, certificates: map[string]bool{}}
}

func preparedStore(t *testing.T, slot catalog.ExpandedSlot) *fakeStore {
	t.Helper()
	store := newFakeStore()
	for _, name := range IdentityNames(slot) {
		store.principals[name] = leaseenv.MockPrincipal{ClientID: uuid.NewString(), PrincipalID: uuid.NewString(), CertificateName: name}
		store.certificates[name] = true
	}
	return store
}

// poolHandler binds the handler to store and resolves every subscription name
// to the test slot's subscription.
func poolHandler(store *fakeStore) *Handler {
	owner := uuid.NewString()
	return &Handler{
		newStore:        func(azcore.TokenCredential) (identityStore, error) { return store, nil },
		owners:          func() (ownerSettings, error) { return ownerSettings{IDs: []string{owner}}, nil },
		checkOwnerGroup: func(context.Context, azcore.TokenCredential, ownerSettings) error { return nil },
		azure: func() (azcore.TokenCredential, subscriptionIDResolver, error) {
			return nil, func(context.Context, string) (string, error) { return testSlot().Subscriptions.E2E.ID, nil }, nil
		},
		externalSubscriptionID: func() string { return testSlot().Subscriptions.E2E.ID },
	}
}

func testPool(unmanaged bool) catalog.Pool {
	pool := catalog.Pool{
		Name:          "test",
		ResourceType:  "aro-hcp-dev-shard0-slot",
		SlotCount:     1,
		Subscriptions: catalog.PoolSubscriptions{E2E: "external"},
		SlotAssets:    catalog.SlotAssets{MockIdentities: &catalog.MockIdentitiesAsset{Allocation: catalog.AllocationDedicated}},
	}
	if unmanaged {
		pool.SlotAssets.E2EIdentities = &catalog.E2EIdentitiesAsset{Provisioning: catalog.IdentityProvisioningUnmanaged}
	}
	return pool
}

func TestReconcileSubscriptionRolesOnlyGrantsToPreparedIdentities(t *testing.T) {
	t.Parallel()
	request := assets.PoolRequest{Environment: "dev", Pools: []catalog.Pool{testPool(true)}}
	slot := catalog.ExpandSlotsForPool("dev", request.Pools[0])[0]
	slot.Subscriptions.E2E.ID = testSlot().Subscriptions.E2E.ID

	store := preparedStore(t, slot)
	handler := poolHandler(store)
	if err := handler.ReconcileSubscriptionRoles(context.Background(), request, false); err == nil || !strings.Contains(err.Error(), "missing roles") {
		t.Fatalf("check mode must report missing grants, got %v", err)
	}
	if store.roles || store.writes != 0 {
		t.Fatal("check mode must not write")
	}
	if err := handler.ReconcileSubscriptionRoles(context.Background(), request, true); err != nil {
		t.Fatal(err)
	}
	if !store.roles {
		t.Fatal("grants were not applied")
	}
	// Only grants were written: no identity, certificate or owner was created.
	if store.writes != len(bindings(leaseenv.MockIdentityBundlePayload{}, "msi-role")) {
		t.Fatalf("unexpected writes: %d", store.writes)
	}
	if len(store.owners) != 0 {
		t.Fatalf("owners must not be touched: %v", store.owners)
	}

	// An unprepared identity is an error pointing at the identity workflow, never created here.
	unprepared := newFakeStore()
	err := poolHandler(unprepared).ReconcileSubscriptionRoles(context.Background(), request, true)
	if err == nil || !strings.Contains(err.Error(), "ASSET=mock_identities") || len(unprepared.principals) != 0 || unprepared.writes != 0 {
		t.Fatalf("unprepared identities must not be created: %v", err)
	}
}

func TestReconcileSubscriptionRolesSkipsPoolsWithoutMockIdentities(t *testing.T) {
	t.Parallel()
	pool := testPool(true)
	pool.SlotAssets.MockIdentities = nil
	store := newFakeStore()
	if err := poolHandler(store).ReconcileSubscriptionRoles(context.Background(), assets.PoolRequest{Environment: "dev", Pools: []catalog.Pool{pool}}, true); err != nil {
		t.Fatal(err)
	}
	if store.roleCalls != 0 || store.reads != 0 {
		t.Fatal("pool without mock identities was reconciled")
	}
}

// A subscription owned by another team gets its identities from the ARO-HCP
// operator, who has no rights there, and its roles from the subscription owner.
func TestUnmanagedPoolPoolCommandsNeverTouchSubscriptionRoles(t *testing.T) {
	t.Parallel()
	request := assets.PoolRequest{Environment: "dev", Pools: []catalog.Pool{testPool(true)}, IncludeUnmanaged: true}
	store := newFakeStore()
	handler := poolHandler(store)
	if err := handler.ApplyPools(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(store.principals) != 3 || len(store.certificates) != 3 || len(store.owners) != 3 {
		t.Fatalf("identities were not fully created: %d principals, %d certificates, %d owners", len(store.principals), len(store.certificates), len(store.owners))
	}
	if err := handler.ValidatePools(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if store.roleCalls != 0 {
		t.Fatalf("unmanaged pool apply and validate touched subscription roles %d times", store.roleCalls)
	}
}

func TestManagedPoolPoolCommandsGrantSubscriptionRoles(t *testing.T) {
	t.Parallel()
	request := assets.PoolRequest{Environment: "dev", Pools: []catalog.Pool{testPool(false)}}
	store := newFakeStore()
	if err := poolHandler(store).ApplyPools(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if !store.roles {
		t.Fatal("managed pool apply must grant subscription roles")
	}
}

// The ARO-HCP operator cannot read an externally owned subscription: its pools
// are prepared with the ID the owner gave and never resolve the display name.
func TestUnmanagedPoolUsesSuppliedSubscriptionIDWithoutResolvingName(t *testing.T) {
	t.Parallel()
	request := assets.PoolRequest{Environment: "dev", Pools: []catalog.Pool{testPool(true)}, IncludeUnmanaged: true}
	store := newFakeStore()
	handler := poolHandler(store)
	handler.azure = func() (azcore.TokenCredential, subscriptionIDResolver, error) {
		return nil, func(_ context.Context, name string) (string, error) {
			return "", errors.New("operator cannot read subscription " + name)
		}, nil
	}
	if err := handler.ApplyPools(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	for _, name := range IdentityNames(testSlot()) {
		if _, found := store.principals[name]; !found {
			t.Fatalf("identity %s was not created for the supplied subscription ID", name)
		}
	}
}

func TestUnmanagedPoolRequiresValidSubscriptionID(t *testing.T) {
	t.Parallel()
	request := assets.PoolRequest{Environment: "dev", Pools: []catalog.Pool{testPool(true)}, IncludeUnmanaged: true}
	for name, id := range map[string]string{"missing": "", "not a UUID": "external", "nil UUID": uuid.Nil.String()} {
		handler := poolHandler(newFakeStore())
		handler.externalSubscriptionID = func() string { return id }
		if err := handler.ApplyPools(context.Background(), request); err == nil {
			t.Fatalf("%s: apply must refuse an unusable subscription ID", name)
		}
	}
}

func TestManagedPoolStillResolvesSubscriptionName(t *testing.T) {
	t.Parallel()
	request := assets.PoolRequest{Environment: "dev", Pools: []catalog.Pool{testPool(false)}}
	handler := poolHandler(newFakeStore())
	handler.externalSubscriptionID = func() string { return "ignored" }
	resolved := 0
	handler.azure = func() (azcore.TokenCredential, subscriptionIDResolver, error) {
		return nil, func(context.Context, string) (string, error) { resolved++; return testSlot().Subscriptions.E2E.ID, nil }, nil
	}
	if err := handler.ApplyPools(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if resolved != 1 {
		t.Fatalf("managed pool resolved the subscription %d times, want 1", resolved)
	}
}

// Identity names hash the subscription ID's text, so every spelling of the
// owner's ID must prepare the identities the owner's reconciliation will look up.
func TestUnmanagedPoolSubscriptionIDSpellingsPrepareTheSameIdentities(t *testing.T) {
	t.Parallel()
	canonical := testSlot().Subscriptions.E2E.ID
	request := assets.PoolRequest{Environment: "dev", Pools: []catalog.Pool{testPool(true)}, IncludeUnmanaged: true}
	for name, id := range map[string]string{
		"braced":       "{" + canonical + "}",
		"upper case":   strings.ToUpper(canonical),
		"unhyphenated": strings.ReplaceAll(canonical, "-", ""),
	} {
		store := newFakeStore()
		handler := poolHandler(store)
		handler.externalSubscriptionID = func() string { return id }
		if err := handler.ApplyPools(context.Background(), request); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, identity := range IdentityNames(testSlot()) {
			if _, found := store.principals[identity]; !found {
				t.Fatalf("%s: identity %s for the canonical subscription ID was not created", name, identity)
			}
		}
	}
}
