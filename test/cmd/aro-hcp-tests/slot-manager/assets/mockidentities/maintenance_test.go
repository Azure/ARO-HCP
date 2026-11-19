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
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
)

type maintenanceTestStore struct {
	*fakeStore
	resolved       []string
	identityWrites int
	ownerChecks    int
	prepareError   map[string]error
}

func (store *maintenanceTestStore) Resolve(ctx context.Context, name string, apply bool) (leaseenv.MockPrincipal, error) {
	store.resolved = append(store.resolved, name)
	if apply {
		store.identityWrites++
	}
	return store.fakeStore.Resolve(ctx, name, apply)
}

func (store *maintenanceTestStore) Certificate(ctx context.Context, name string, apply bool) error {
	if apply {
		store.identityWrites++
	}
	return store.fakeStore.Certificate(ctx, name, apply)
}

func (store *maintenanceTestStore) Owners(ctx context.Context, name string, principal leaseenv.MockPrincipal, desired []string, apply bool) error {
	store.ownerChecks++
	if apply {
		store.identityWrites++
	}
	return store.fakeStore.Owners(ctx, name, principal, desired, apply)
}

func (store *maintenanceTestStore) PreflightIdentity(_ context.Context, name string, desired []string) (bool, error) {
	if err := store.prepareError[name]; err != nil {
		return false, err
	}
	_, exists := store.principals[name]
	if !exists && !store.certificates[name] {
		return true, nil
	}
	if !exists || !store.certificates[name] || !slices.Equal(store.owners[name], desired) {
		return false, errors.New("existing credential or owner drift requires drained workflow")
	}
	return false, nil
}

func (store *maintenanceTestStore) PrepareIdentity(ctx context.Context, name string, desired []string) (leaseenv.MockPrincipal, error) {
	initialize, err := store.PreflightIdentity(ctx, name, desired)
	if err != nil {
		return leaseenv.MockPrincipal{}, err
	}
	if !initialize {
		return store.Resolve(ctx, name, false)
	}
	principal, err := store.Resolve(ctx, name, true)
	if err != nil {
		return principal, err
	}
	if err := store.Owners(ctx, name, principal, desired, true); err != nil {
		return principal, err
	}
	return principal, store.Certificate(ctx, name, true)
}

func readyMaintenanceStore(selected []slots.ExpandedSlot) *maintenanceTestStore {
	store := &maintenanceTestStore{fakeStore: &fakeStore{principals: map[string]leaseenv.MockPrincipal{}, certificates: map[string]bool{}, owners: map[string][]string{}, owner: true}}
	for _, slot := range selected {
		for _, name := range IdentityNames(slot) {
			store.principals[name] = leaseenv.MockPrincipal{ClientID: uuid.NewString(), PrincipalID: uuid.NewString(), CertificateName: name}
			store.certificates[name] = true
		}
	}
	return store
}

func maintenanceHandler(store *maintenanceTestStore) *Handler {
	return &Handler{
		newStore: func(azcore.TokenCredential) (identityStore, error) { return store, nil },
		owners: func() (ownerSettings, error) {
			return ownerSettings{IDs: []string{"22222222-2222-2222-2222-222222222222"}}, nil
		},
		checkOwnerGroup: func(context.Context, azcore.TokenCredential, ownerSettings) error { return nil },
	}
}

func TestExternalSubscriptionRoutineRepairsOnlyGrants(t *testing.T) {
	slot := testSlot()
	store := readyMaintenanceStore([]slots.ExpandedSlot{slot})
	handler := maintenanceHandler(store)
	// Routine repair must not even read the authoritative owners configuration.
	handler.owners = func() (ownerSettings, error) { t.Fatal("routine repair loaded owners"); return ownerSettings{}, nil }
	handler.checkOwnerGroup = func(context.Context, azcore.TokenCredential, ownerSettings) error {
		t.Fatal("routine repair queried owner group")
		return nil
	}
	before := store.principals[IdentityNames(slot)[0]]
	if err := handler.PreflightExternalSubscription(t.Context(), nil, slot.Subscriptions.E2E.ID, []slots.ExpandedSlot{slot}, false); err != nil {
		t.Fatal(err)
	}
	if store.writes != 0 {
		t.Fatal("preflight mutated assets")
	}
	if err := handler.ApplyExternalSubscription(t.Context(), nil, slot.Subscriptions.E2E.ID, []slots.ExpandedSlot{slot}, false); err != nil {
		t.Fatal(err)
	}
	if store.identityWrites != 0 || store.ownerChecks != 0 || store.writes != len(bindings(leaseenv.MockIdentityBundlePayload{}, "custom-role")) {
		t.Fatalf("routine repair changed something besides subscription grants: %+v", store)
	}
	if store.principals[IdentityNames(slot)[0]] != before {
		t.Fatal("routine repair changed the principal")
	}
	writes := store.writes
	if err := handler.ApplyExternalSubscription(t.Context(), nil, slot.Subscriptions.E2E.ID, []slots.ExpandedSlot{slot}, false); err != nil || store.writes != writes {
		t.Fatalf("repeat apply was not a no-op: writes %d -> %d, error %v", writes, store.writes, err)
	}
}

func TestExternalSubscriptionCheckReadOnlyWithoutOwner(t *testing.T) {
	slot := testSlot()
	store := readyMaintenanceStore([]slots.ExpandedSlot{slot})
	store.owner = false
	store.roles = true
	handler := maintenanceHandler(store)
	handler.owners = func() (ownerSettings, error) { t.Fatal("check loaded owner settings"); return ownerSettings{}, nil }
	if err := handler.CheckExternalSubscription(t.Context(), nil, slot.Subscriptions.E2E.ID, []slots.ExpandedSlot{slot}); err != nil {
		t.Fatal(err)
	}
	if store.writes != 0 || store.identityWrites != 0 || store.ownerChecks != 0 {
		t.Fatalf("readiness check mutated or inspected owners: %+v", store)
	}
	if err := handler.PreflightExternalSubscription(t.Context(), nil, slot.Subscriptions.E2E.ID, []slots.ExpandedSlot{slot}, false); err == nil || !strings.Contains(err.Error(), "not Owner") {
		t.Fatalf("apply preflight accepted a non-Owner: %v", err)
	}
}

func TestExternalSubscriptionReadinessFailuresAreActionable(t *testing.T) {
	for _, test := range []struct {
		name, expected string
		mutate         func(*maintenanceTestStore, string)
	}{
		{name: "missing principal", expected: "--prepare-identities", mutate: func(store *maintenanceTestStore, name string) { delete(store.principals, name) }},
		{name: "changed certificate", expected: "drained identity rotation", mutate: func(store *maintenanceTestStore, name string) { store.certificates[name] = false }},
		{name: "missing grants", expected: "--apply", mutate: func(store *maintenanceTestStore, _ string) { store.roles = false }},
	} {
		t.Run(test.name, func(t *testing.T) {
			slot := testSlot()
			store := readyMaintenanceStore([]slots.ExpandedSlot{slot})
			store.roles = true
			test.mutate(store, IdentityNames(slot)[1])
			err := maintenanceHandler(store).CheckExternalSubscription(t.Context(), nil, slot.Subscriptions.E2E.ID, []slots.ExpandedSlot{slot})
			if err == nil || !strings.Contains(err.Error(), test.expected) || store.writes != 0 {
				t.Fatalf("readiness failure = %v, writes = %d", err, store.writes)
			}
		})
	}
}

func TestExternalSubscriptionPreflightsEverySlotBeforeWrites(t *testing.T) {
	first, second := testSlot(), testSlot()
	second.ResourceName += "-later"
	selected := []slots.ExpandedSlot{first, second}
	for _, prepare := range []bool{false, true} {
		t.Run(map[bool]string{false: "routine", true: "privileged"}[prepare], func(t *testing.T) {
			store := readyMaintenanceStore(selected)
			for _, slot := range selected {
				for _, name := range IdentityNames(slot) {
					store.owners[name] = []string{"22222222-2222-2222-2222-222222222222"}
				}
			}
			if prepare {
				store.prepareError = map[string]error{IdentityNames(second)[2]: errors.New("changed pin requires drain")}
			} else {
				delete(store.principals, IdentityNames(second)[2])
			}
			err := maintenanceHandler(store).ApplyExternalSubscription(t.Context(), nil, first.Subscriptions.E2E.ID, selected, prepare)
			if err == nil || store.writes != 0 || store.identityWrites != 0 {
				t.Fatalf("later-slot failure did not prevent all writes: error %v, store %+v", err, store)
			}
		})
	}
}

func TestExternalSubscriptionExplicitPreparationIsIdempotent(t *testing.T) {
	slot := testSlot()
	store := readyMaintenanceStore(nil)
	handler := maintenanceHandler(store)
	if err := handler.PreflightExternalSubscription(t.Context(), nil, slot.Subscriptions.E2E.ID, []slots.ExpandedSlot{slot}, true); err != nil || store.writes != 0 {
		t.Fatalf("initial preflight failed or wrote: %v, writes %d", err, store.writes)
	}
	if err := handler.ApplyExternalSubscription(t.Context(), nil, slot.Subscriptions.E2E.ID, []slots.ExpandedSlot{slot}, true); err != nil {
		t.Fatal(err)
	}
	if err := handler.CheckExternalSubscription(t.Context(), nil, slot.Subscriptions.E2E.ID, []slots.ExpandedSlot{slot}); err != nil {
		t.Fatal(err)
	}
	writes, identityWrites := store.writes, store.identityWrites
	if err := handler.ApplyExternalSubscription(t.Context(), nil, slot.Subscriptions.E2E.ID, []slots.ExpandedSlot{slot}, true); err != nil {
		t.Fatal(err)
	}
	if store.writes != writes || store.identityWrites != identityWrites {
		t.Fatal("repeat explicit preparation repinned credentials or replaced owners")
	}
}

func TestExternalSubscriptionSelectionIsBoundedAndDeterministic(t *testing.T) {
	first, second := testSlot(), testSlot()
	second.ResourceName += "-later"
	selected := []slots.ExpandedSlot{second, first, second}
	store := readyMaintenanceStore(selected)
	store.roles = true
	if err := maintenanceHandler(store).CheckExternalSubscription(t.Context(), nil, first.Subscriptions.E2E.ID, selected); err != nil {
		t.Fatal(err)
	}
	expected := append(IdentityNames(first), IdentityNames(second)...)
	if !reflect.DeepEqual(store.resolved, expected) {
		t.Fatalf("principal discovery = %v, want %v", store.resolved, expected)
	}
	for _, subscription := range []string{"", "display-name", uuid.Nil.String(), uuid.NewString()} {
		t.Run(subscription, func(t *testing.T) {
			handler := &Handler{newStore: func(azcore.TokenCredential) (identityStore, error) {
				t.Fatal("invalid subscription selection contacted Azure")
				return nil, nil
			}}
			if err := handler.ApplyExternalSubscription(t.Context(), nil, subscription, selected, false); err == nil {
				t.Fatal("accepted invalid or cross-subscription selection")
			}
		})
	}
}
