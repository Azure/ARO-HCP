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
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/google/uuid"
	"github.com/spf13/pflag"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
	"github.com/Azure/ARO-HCP/test/util/framework"
)

const VaultURL = "https://aro-hcp-dev-svc-kv.vault.azure.net"

type identityStore interface {
	AuthorizeOwner(context.Context, string) error
	Resolve(context.Context, string, bool) (leaseenv.MockPrincipal, error)
	Certificate(context.Context, string, bool) error
	Roles(context.Context, string, leaseenv.MockIdentityBundlePayload, bool) error
}

type Handler struct {
	newStore func(azcore.TokenCredential) (identityStore, error)
}

func NewHandler() *Handler                                               { return &Handler{newStore: newAzureStore} }
func (*Handler) Kind() assets.Kind                                       { return slots.KindMockIdentities }
func (*Handler) AttachOptions(*pflag.FlagSet)                            {}
func (*Handler) Declared(pool slots.Pool) bool                           { return pool.SlotAssets.MockIdentities != nil }
func (*Handler) ReleaseLease(context.Context, assets.LeaseRequest) error { return nil }

func IdentityNames(slot slots.ExpandedSlot) []string {
	identity := uuid.NewSHA1(uuid.NameSpaceURL, []byte(strings.ToLower(slot.Subscriptions.E2E.ID)+"/"+slot.ResourceName)).String()
	prefix := "aro-hcp-slot-" + identity
	return []string{prefix + "-msi", prefix + "-backend", prefix + "-cs"}
}

func reconcile(ctx context.Context, store identityStore, slot slots.ExpandedSlot, apply bool) (*leaseenv.MockIdentityBundlePayload, error) {
	if slot.MockIdentityConfig == nil || slot.MockIdentityConfig.Allocation != slots.AllocationDedicated {
		return nil, fmt.Errorf("mock identities require a dedicated slot declaration")
	}
	if _, err := uuid.Parse(slot.Subscriptions.E2E.ID); err != nil || slot.ResourceName == "" {
		return nil, fmt.Errorf("mock identities require a resolved customer subscription and slot")
	}
	if apply {
		if err := store.AuthorizeOwner(ctx, slot.Subscriptions.E2E.ID); err != nil {
			return nil, err
		}
	}
	bundle := &leaseenv.MockIdentityBundlePayload{SlotName: slot.ResourceName, SubscriptionID: slot.Subscriptions.E2E.ID}
	principals := []*leaseenv.MockPrincipal{&bundle.MSI, &bundle.Backend, &bundle.ClustersService}
	for index, name := range IdentityNames(slot) {
		principal, err := store.Resolve(ctx, name, apply)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", name, err)
		}
		if err := store.Certificate(ctx, name, apply); err != nil {
			return nil, fmt.Errorf("certificate %s: %w", name, err)
		}
		*principals[index] = principal
	}
	if err := bundle.Validate(); err != nil {
		return nil, err
	}
	if err := store.Roles(ctx, slot.Subscriptions.E2E.ID, *bundle, apply); err != nil {
		return nil, err
	}
	return bundle, nil
}

func ownerRepair(slot slots.ExpandedSlot, err error) error {
	return fmt.Errorf("mock identity bundle for %s is not ready: %w; ask the customer-subscription Owner with Entra application and certificate permissions to run: make -C test apply-pool-assets ENVIRONMENT=%s POOL=%s ASSET=mock_identities SUBSCRIPTION='%s'; CI never repairs identities or rotates credentials", slot.ResourceName, err, slot.Environment, slot.PoolName, slot.SubscriptionName)
}

func (handler *Handler) checkLease(ctx context.Context, request assets.LeaseRequest) error {
	if request.AcquiredSlotState == nil {
		return fmt.Errorf("missing acquired slot state")
	}
	slot := &request.AcquiredSlotState.Slot
	credential, err := slots.NewClusterProfileCredential(request.SelectedClusterProfileDir, nil)
	if err != nil {
		return ownerRepair(*slot, err)
	}
	store, err := handler.newStore(credential)
	if err != nil {
		return ownerRepair(*slot, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	bundle, err := reconcile(ctx, store, *slot, false)
	if err != nil {
		slot.Assets.MockIdentities = nil
		return ownerRepair(*slot, err)
	}
	if previous := slot.Assets.MockIdentities; previous != nil && *previous != *bundle {
		return ownerRepair(*slot, fmt.Errorf("identity bundle changed while the primary slot lease was held"))
	}
	slot.Assets.MockIdentities = bundle
	return nil
}

func (handler *Handler) AcquireLease(ctx context.Context, request assets.LeaseRequest) error {
	return handler.checkLease(ctx, request)
}
func (handler *Handler) AdmitLease(ctx context.Context, request assets.LeaseRequest) error {
	return handler.checkLease(ctx, request)
}

func (*Handler) PublishLease(_ context.Context, request assets.LeaseRequest, contract *slots.RuntimeContractBuilder) error {
	if request.AcquiredSlotState == nil {
		return fmt.Errorf("missing acquired slot state")
	}
	slot := request.AcquiredSlotState.Slot
	if slot.Assets.MockIdentities == nil {
		return fmt.Errorf("unresolved mock identity bundle")
	}
	if err := slot.ValidateResolvedAssets(); err != nil {
		return err
	}
	payload, err := json.Marshal(slot.Assets.MockIdentities)
	if err != nil {
		return err
	}
	return contract.Add(string(slots.KindMockIdentities), leaseenv.MockIdentityBundle, string(payload))
}

func (handler *Handler) ApplyPools(ctx context.Context, request assets.PoolRequest) error {
	return handler.pools(ctx, request, true)
}
func (handler *Handler) ValidatePools(ctx context.Context, request assets.PoolRequest) error {
	return handler.pools(ctx, request, false)
}

func (handler *Handler) pools(ctx context.Context, request assets.PoolRequest, apply bool) error {
	testContext := framework.NewTestContext()
	credential, err := testContext.AzureCredential()
	if err != nil {
		return err
	}
	factory, err := testContext.GetARMSubscriptionsClientFactory()
	if err != nil {
		return err
	}
	store, err := handler.newStore(credential)
	if err != nil {
		return err
	}
	for _, pool := range request.Pools {
		if !handler.Declared(pool) {
			continue
		}
		if pool.SlotAssets.E2EIdentities != nil && pool.SlotAssets.E2EIdentities.Provisioning == slots.IdentityProvisioningUnmanaged && !request.IncludeUnmanaged {
			continue
		}
		subscriptionID, err := framework.GetSubscriptionID(ctx, factory.NewClient(), pool.Subscriptions.E2E)
		if err != nil {
			return err
		}
		for _, slot := range slots.ExpandSlotsForPool(request.Environment, pool) {
			slot.Subscriptions.E2E.ID = subscriptionID
			bounded, cancel := context.WithTimeout(ctx, 5*time.Minute)
			_, err := reconcile(bounded, store, slot, apply)
			cancel()
			if err != nil {
				return ownerRepair(slot, err)
			}
		}
	}
	return nil
}
