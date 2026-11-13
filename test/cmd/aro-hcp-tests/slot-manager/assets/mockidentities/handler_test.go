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
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

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
	reads        int
	owners       map[string][]string
}

func (store *fakeStore) Owners(_ context.Context, name string, _ leaseenv.MockPrincipal, desired []string, apply bool) error {
	if slices.Equal(store.owners[name], desired) {
		return nil
	}
	if !apply {
		return errors.New("owner drift")
	}
	if store.owners == nil {
		store.owners = map[string][]string{}
	}
	store.owners[name] = desired
	store.writes++
	return nil
}

func (store *fakeStore) AuthorizeOwner(context.Context, string) error {
	if !store.owner {
		return errors.New("not Owner")
	}
	return nil
}
func (store *fakeStore) Resolve(_ context.Context, name string, apply bool) (leaseenv.MockPrincipal, error) {
	store.reads++
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
	store.reads++
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
	store.reads++
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

func TestRegistryAdmissionOnlyDiscovery(t *testing.T) {
	store := &fakeStore{principals: map[string]leaseenv.MockPrincipal{}, certificates: map[string]bool{}, owner: true}
	slot := testSlot()
	if _, err := reconcile(t.Context(), store, slot, true); err != nil {
		t.Fatal(err)
	}
	store.reads, store.writes = 0, 0
	profile := t.TempDir()
	for name, value := range map[string]string{"tenant": uuid.NewString(), "client-id": uuid.NewString(), "client-secret": "test-placeholder"} {
		if err := os.WriteFile(filepath.Join(profile, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	handler := &Handler{newStore: func(azcore.TokenCredential) (identityStore, error) { return store, nil }}
	registry, err := assets.NewRegistry(handler)
	if err != nil {
		t.Fatal(err)
	}
	request := assets.LeaseRequest{AcquiredSlotState: &slots.AcquiredSlotState{Slot: slot}, SelectedClusterProfileDir: profile}
	if err := registry.AcquireLeases(t.Context(), request, nil); err != nil {
		t.Fatal(err)
	}
	if store.reads != 0 || request.AcquiredSlotState.Slot.Assets.MockIdentities != nil {
		t.Fatal("acquisition performed discovery")
	}
	if err := registry.PublishLease(t.Context(), request, slots.NewRuntimeContractBuilder()); err == nil {
		t.Fatal("published before admission")
	}
	for admission := 1; admission <= 2; admission++ {
		if err := registry.AdmitLease(t.Context(), request); err != nil {
			t.Fatal(err)
		}
		if store.reads != admission*7 || store.writes != 0 {
			t.Fatalf("expected exactly three Graph and certificate checks and one RBAC check per admission: %+v", store)
		}
	}
	if err := registry.PublishLease(t.Context(), request, slots.NewRuntimeContractBuilder()); err != nil {
		t.Fatal(err)
	}
	if err := handler.ReleaseLease(t.Context(), request); err != nil || store.reads != 14 || store.writes != 0 {
		t.Fatalf("release contacted remote services: %v", err)
	}
	for _, failure := range []string{"credential", "client", "identity drift"} {
		t.Run(failure, func(t *testing.T) {
			previous := *request.AcquiredSlotState.Slot.Assets.MockIdentities
			failedRequest := request
			originalFactory := handler.newStore
			name := IdentityNames(slot)[0]
			originalPrincipal := store.principals[name]
			switch failure {
			case "credential":
				failedRequest.SelectedClusterProfileDir = t.TempDir()
			case "client":
				handler.newStore = func(azcore.TokenCredential) (identityStore, error) { return nil, errors.New("client unavailable") }
			case "identity drift":
				changed := originalPrincipal
				changed.ClientID = uuid.NewString()
				store.principals[name] = changed
			}
			if err := registry.AdmitLease(t.Context(), failedRequest); err == nil || store.writes != 0 {
				t.Fatal("readmission accepted or repaired invalid input")
			}
			if err := registry.PublishLease(t.Context(), request, slots.NewRuntimeContractBuilder()); err == nil {
				t.Error("published stale bundle after failed readmission")
			}
			handler.newStore = originalFactory
			store.principals[name] = originalPrincipal
			request.AcquiredSlotState.Slot.Assets.MockIdentities = &previous
		})
	}
	delete(store.certificates, IdentityNames(slot)[0])
	if err := registry.AdmitLease(t.Context(), request); err == nil || store.writes != 0 {
		t.Fatal("readmission repaired or accepted missing certificate")
	}
	if err := registry.PublishLease(t.Context(), request, slots.NewRuntimeContractBuilder()); err == nil {
		t.Fatal("published stale bundle after failed readmission")
	}
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

func TestPublishRejectsMismatchedSlotBundle(t *testing.T) {
	slot := testSlot()
	store := &fakeStore{principals: map[string]leaseenv.MockPrincipal{}, certificates: map[string]bool{}, owner: true}
	bundle, err := reconcile(t.Context(), store, slot, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*leaseenv.MockIdentityBundlePayload){
		func(bundle *leaseenv.MockIdentityBundlePayload) { bundle.SubscriptionID = uuid.NewString() },
		func(bundle *leaseenv.MockIdentityBundlePayload) { bundle.SlotName += "-other" },
	} {
		invalid := *bundle
		mutate(&invalid)
		slot.Assets.MockIdentities = &invalid
		request := assets.LeaseRequest{AcquiredSlotState: &slots.AcquiredSlotState{Slot: slot}}
		if err := NewHandler().PublishLease(t.Context(), request, slots.NewRuntimeContractBuilder()); err == nil || !strings.Contains(err.Error(), "does not match slot customer subscription") {
			t.Fatalf("expected slot isolation failure, got %v", err)
		}
	}
	message := ownerRepair(slot, errors.New("missing persistent role")).Error()
	for _, expected := range []string{"missing persistent role", "Owner", "make -C test apply-pool-assets", "ASSET=mock_identities", "CI never repairs identities or rotates credentials"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("repair instruction missing %q: %s", expected, message)
		}
	}
}
