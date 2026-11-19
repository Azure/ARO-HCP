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
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
)

// identityPreparationStore deliberately separates privileged identity preparation
// from the read-only discovery and subscription-only grants used routinely.
type identityPreparationStore interface {
	PreflightIdentity(context.Context, string, []string) (bool, error)
	PrepareIdentity(context.Context, string, []string) (leaseenv.MockPrincipal, error)
}

// externalSlots validates the entire selection before contacting Azure and gives
// every operation the same deterministic, duplicate-free order.
func externalSlots(subscriptionID string, selected []slots.ExpandedSlot) ([]slots.ExpandedSlot, error) {
	id, err := uuid.Parse(subscriptionID)
	if err != nil || id == uuid.Nil {
		return nil, fmt.Errorf("mock identity maintenance requires an explicit subscription UUID")
	}
	ordered := slices.Clone(selected)
	slices.SortFunc(ordered, func(left, right slots.ExpandedSlot) int {
		return strings.Compare(left.ResourceName, right.ResourceName)
	})
	result := ordered[:0]
	for _, slot := range ordered {
		if !strings.EqualFold(slot.Subscriptions.E2E.ID, subscriptionID) {
			return nil, fmt.Errorf("slot %s belongs to subscription %s, not selected subscription %s", slot.ResourceName, slot.Subscriptions.E2E.ID, subscriptionID)
		}
		if slot.ResourceName == "" || slot.MockIdentityConfig == nil || slot.MockIdentityConfig.Allocation != slots.AllocationDedicated {
			return nil, fmt.Errorf("mock identity maintenance requires a resolved dedicated slot declaration")
		}
		slot.Subscriptions.E2E.ID = id.String()
		if len(result) > 0 && result[len(result)-1].ResourceName == slot.ResourceName {
			continue
		}
		result = append(result, slot)
	}
	return result, nil
}

func discoverExternalBundle(ctx context.Context, store identityStore, slot slots.ExpandedSlot) (*leaseenv.MockIdentityBundlePayload, error) {
	bundle, err := resolveBundle(ctx, store, slot, false)
	if err != nil {
		return nil, fmt.Errorf("%w; run reconcile-external-subscription --subscription-id %s --apply --prepare-identities for initial preparation only; maintenance never replaces or repins existing credentials; use the documented drained identity rotation workflow", err, slot.Subscriptions.E2E.ID)
	}
	return bundle, nil
}

func preflightExternal(ctx context.Context, store identityStore, selected []slots.ExpandedSlot, prepare bool, desired []string) error {
	if !prepare {
		for _, slot := range selected {
			if _, err := discoverExternalBundle(ctx, store, slot); err != nil {
				return err
			}
		}
		return nil
	}
	preparation, ok := store.(identityPreparationStore)
	if !ok {
		return fmt.Errorf("mock identity store does not support safe initial preparation")
	}
	for _, slot := range selected {
		for _, name := range IdentityNames(slot) {
			if _, err := preparation.PreflightIdentity(ctx, name, desired); err != nil {
				return fmt.Errorf("prepare %s: %w", name, err)
			}
		}
	}
	return nil
}

func (handler *Handler) externalStore(ctx context.Context, credential azcore.TokenCredential, subscriptionID string, selected []slots.ExpandedSlot, prepare bool) (identityStore, ownerSettings, error) {
	store, err := handler.newStore(credential)
	if err != nil {
		return nil, ownerSettings{}, fmt.Errorf("create mock identity maintenance client: %w", err)
	}
	if err := store.AuthorizeOwner(ctx, subscriptionID); err != nil {
		return nil, ownerSettings{}, fmt.Errorf("authorize mock identity subscription grants: %w", err)
	}
	var settings ownerSettings
	if prepare {
		settings, err = handler.owners()
		if err != nil {
			return nil, settings, err
		}
		if err := handler.checkOwnerGroup(ctx, credential, settings); err != nil {
			return nil, settings, fmt.Errorf("validate mock identity preparation owners: %w", err)
		}
	}
	if err := preflightExternal(ctx, store, selected, prepare, settings.IDs); err != nil {
		return nil, settings, err
	}
	return store, settings, nil
}

// PreflightExternalSubscription is read-only. It checks Owner authorization and
// every identity prerequisite before any subscription or identity repair starts.
// Missing grants and the Bicep-managed custom role are intentionally not required
// here: they are the repair operation's outputs.
func (handler *Handler) PreflightExternalSubscription(ctx context.Context, credential azcore.TokenCredential, subscriptionID string, selected []slots.ExpandedSlot, prepareIdentities bool) error {
	ordered, err := externalSlots(subscriptionID, selected)
	if err != nil || len(ordered) == 0 {
		return err
	}
	_, _, err = handler.externalStore(ctx, credential, subscriptionID, ordered, prepareIdentities)
	return err
}

// CheckExternalSubscription validates readiness without requiring Owner access,
// authoritative owner settings, or Graph owner/group reads. It never writes.
func (handler *Handler) CheckExternalSubscription(ctx context.Context, credential azcore.TokenCredential, subscriptionID string, selected []slots.ExpandedSlot) error {
	ordered, err := externalSlots(subscriptionID, selected)
	if err != nil || len(ordered) == 0 {
		return err
	}
	store, err := handler.newStore(credential)
	if err != nil {
		return fmt.Errorf("create mock identity readiness client: %w", err)
	}
	for _, slot := range ordered {
		bundle, err := discoverExternalBundle(ctx, store, slot)
		if err != nil {
			return err
		}
		if err := store.Roles(ctx, subscriptionID, *bundle, false); err != nil {
			return fmt.Errorf("mock identity grants for %s: %w; run reconcile-external-subscription --subscription-id %s --apply", slot.ResourceName, err, subscriptionID)
		}
	}
	return nil
}

// ApplyExternalSubscription repairs only subscription grants by default. Explicit
// preparation can initialize missing identities but cannot rotate credentials,
// remove owners, or reconcile drift on already initialized identities. The full
// selection is preflighted again before the first write, even when the caller has
// already performed a global command preflight.
func (handler *Handler) ApplyExternalSubscription(ctx context.Context, credential azcore.TokenCredential, subscriptionID string, selected []slots.ExpandedSlot, prepareIdentities bool) error {
	ordered, err := externalSlots(subscriptionID, selected)
	if err != nil || len(ordered) == 0 {
		return err
	}
	store, settings, err := handler.externalStore(ctx, credential, subscriptionID, ordered, prepareIdentities)
	if err != nil {
		return err
	}
	if prepareIdentities {
		preparation := store.(identityPreparationStore)
		for _, slot := range ordered {
			for _, name := range IdentityNames(slot) {
				if _, err := preparation.PrepareIdentity(ctx, name, settings.IDs); err != nil {
					return fmt.Errorf("initialize mock identity %s: %w", name, err)
				}
			}
		}
	}
	for _, slot := range ordered {
		bundle, err := discoverExternalBundle(ctx, store, slot)
		if err != nil {
			return err
		}
		if err := store.Roles(ctx, subscriptionID, *bundle, true); err != nil {
			return fmt.Errorf("repair subscription-only mock identity grants for %s: %w", slot.ResourceName, err)
		}
	}
	return nil
}
