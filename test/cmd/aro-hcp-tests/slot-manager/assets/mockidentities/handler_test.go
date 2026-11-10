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
	"testing"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
)

type fakeStore struct {
	principals   map[string]leaseenv.MockPrincipal
	certificates map[string]bool
	roles        bool
	writes       int
	fail         string
	owner        bool
}

func (store *fakeStore) AuthorizeOwner(context.Context, string) error {
	if !store.owner {
		return errors.New("not Owner")
	}
	return nil
}
func (store *fakeStore) Resolve(_ context.Context, name string, apply bool) (leaseenv.MockPrincipal, error) {
	if store.fail == name {
		return leaseenv.MockPrincipal{}, errors.New("ambiguous Graph result")
	}
	if principal, found := store.principals[name]; found {
		return principal, nil
	}
	if !apply {
		return leaseenv.MockPrincipal{}, errors.New("not found")
	}
	principal := leaseenv.MockPrincipal{ClientID: uuid.NewString(), PrincipalID: uuid.NewString(), CertificateName: name}
	store.principals[name] = principal
	store.writes++
	return principal, nil
}
func (store *fakeStore) Certificate(_ context.Context, name string, apply bool) error {
	if store.certificates[name] {
		return nil
	}
	if !apply {
		return errors.New("missing certificate")
	}
	store.certificates[name] = true
	store.writes++
	return nil
}
func (store *fakeStore) Roles(_ context.Context, subscription string, bundle leaseenv.MockIdentityBundlePayload, apply bool) error {
	if subscription != bundle.SubscriptionID {
		return errors.New("wrong scope")
	}
	if store.roles {
		return nil
	}
	if !apply {
		return errors.New("missing roles")
	}
	store.roles = true
	store.writes += len(bindings(bundle, "msi-role"))
	return nil
}
func testSlot() slots.ExpandedSlot {
	return slots.ExpandedSlot{ResourceName: "aro-hcp-dev-shard0-slot-00", MockIdentityConfig: &slots.MockIdentitiesAsset{Allocation: slots.AllocationDedicated}, Subscriptions: slots.ResolvedSubscriptions{E2E: slots.ResolvedSubscription{ID: "11111111-1111-1111-1111-111111111111"}}, AssetRequirements: []slots.AssetRequirement{{Kind: slots.KindMockIdentities, Allocation: slots.AllocationDedicated, UnitsPerSlot: 1}}}
}

func TestPersistentLifecycle(t *testing.T) {
	ctx := context.Background()
	slot := testSlot()
	store := &fakeStore{principals: map[string]leaseenv.MockPrincipal{}, certificates: map[string]bool{}}
	if _, err := reconcile(ctx, store, slot, true); err == nil || store.writes != 0 {
		t.Fatal("non-Owner wrote resources")
	}
	store.owner = true
	if _, err := reconcile(ctx, store, slot, false); err == nil || store.writes != 0 {
		t.Fatal("admission repaired missing resources")
	}
	names := IdentityNames(slot)
	store.fail = names[1]
	if _, err := reconcile(ctx, store, slot, true); err == nil {
		t.Fatal("partial failure ignored")
	}
	store.fail = ""
	bundle, err := reconcile(ctx, store, slot, true)
	if err != nil {
		t.Fatal(err)
	}
	if store.writes != 12 || len(store.principals) != 3 {
		t.Fatalf("expected 3 principals, 3 certificates and 6 roles: %+v", store)
	}
	for _, apply := range []bool{true, false, false} {
		actual, err := reconcile(ctx, store, slot, apply)
		if err != nil || *actual != *bundle || store.writes != 12 {
			t.Fatalf("non-idempotent lifecycle: %v", err)
		}
	}
	slot.Assets.MockIdentities = bundle
	data, err := yaml.Marshal(slot)
	if err != nil {
		t.Fatal(err)
	}
	var restored slots.ExpandedSlot
	if err := yaml.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(slot, restored) {
		t.Fatal("state round trip lost bundle")
	}
	if err := restored.ValidateResolvedAssets(); err != nil {
		t.Fatal(err)
	}
	request := assets.LeaseRequest{AcquiredSlotState: &slots.AcquiredSlotState{Slot: restored}}
	if err := NewHandler().PublishLease(ctx, request, slots.NewRuntimeContractBuilder()); err != nil {
		t.Fatal(err)
	}
	if err := NewHandler().ReleaseLease(ctx, request); err != nil || store.writes != 12 {
		t.Fatal("release changed persistent resources")
	}
	request.AcquiredSlotState.Slot.Assets.MockIdentities = nil
	if err := NewHandler().PublishLease(ctx, request, slots.NewRuntimeContractBuilder()); err == nil {
		t.Fatal("published unresolved bundle")
	}
	store.certificates[names[0]] = false
	if _, err := reconcile(ctx, store, slot, false); err == nil || store.writes != 12 {
		t.Fatal("admission repaired certificate")
	}
}

func TestDeterministicIsolation(t *testing.T) {
	slot := testSlot()
	names := IdentityNames(slot)
	if !reflect.DeepEqual(names, IdentityNames(slot)) {
		t.Fatal("unstable names")
	}
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			t.Fatal("duplicate name")
		}
		seen[name] = true
	}
	other := slot
	other.Subscriptions.E2E.ID = uuid.NewString()
	if reflect.DeepEqual(names, IdentityNames(other)) {
		t.Fatal("cross-subscription name collision")
	}
	other = slot
	other.ResourceName += "-other"
	if reflect.DeepEqual(names, IdentityNames(other)) {
		t.Fatal("cross-slot name collision")
	}
	slot.MockIdentityConfig.Allocation = slots.AllocationLeased
	if _, err := reconcile(context.Background(), nil, slot, false); err == nil {
		t.Fatal("accepted leased allocation")
	}
}
