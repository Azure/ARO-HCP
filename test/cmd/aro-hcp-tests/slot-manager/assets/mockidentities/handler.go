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
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	msgraph "github.com/microsoftgraph/msgraph-sdk-go"
	"github.com/spf13/pflag"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/catalog"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/clusterprofile"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/lease"
	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/tooling/entra-app-credentials/pkg/graph"
	"github.com/Azure/ARO-HCP/tooling/entra-app-credentials/pkg/owners"
)

const VaultURL = "https://aro-hcp-dev-svc-kv.vault.azure.net"

// ExternalSubscriptionIDEnv carries the ID of an externally owned subscription
// for the pool commands. The ARO-HCP operator cannot see that subscription, so
// its owner provides the ID instead of the operator resolving the display name.
const ExternalSubscriptionIDEnv = "EXTERNAL_SUBSCRIPTION_ID"

type identityStore interface {
	Resolve(context.Context, string, bool) (leaseenv.MockPrincipal, error)
	Certificate(context.Context, string, bool) error
	Roles(context.Context, string, leaseenv.MockIdentityBundlePayload, bool) error
	// Owners makes the application and service principal named name owned by
	// exactly desired (apply) or reports any difference (!apply).
	Owners(ctx context.Context, name string, principal leaseenv.MockPrincipal, desired []string, apply bool) error
}

type Handler struct {
	newStore func(azcore.TokenCredential) (identityStore, error)
	// owners supplies the authoritative owner list used by the Owner
	// apply/validate pool commands. Lease admission never touches owners.
	owners func() (ownerSettings, error)
	// checkOwnerGroup rejects an owner list that drifted from the Entra group.
	checkOwnerGroup func(context.Context, azcore.TokenCredential, ownerSettings) error
	// azure supplies the Azure credential and the subscription name resolver for
	// the pool and subscription role commands.
	azure func() (azcore.TokenCredential, subscriptionIDResolver, error)
	// externalSubscriptionID supplies the ID of the externally owned subscription
	// for the pool commands; the ARO-HCP operator cannot read that subscription.
	externalSubscriptionID func() string
}

type subscriptionIDResolver func(ctx context.Context, name string) (string, error)

func NewHandler() *Handler {
	return &Handler{newStore: newAzureStore, owners: ownerSettingsFromEnv, checkOwnerGroup: checkOwnerGroup, azure: azureDependencies, externalSubscriptionID: func() string { return os.Getenv(ExternalSubscriptionIDEnv) }}
}

func azureDependencies() (azcore.TokenCredential, subscriptionIDResolver, error) {
	testContext := framework.NewTestContext()
	credential, err := testContext.AzureCredential()
	if err != nil {
		return nil, nil, err
	}
	factory, err := testContext.GetARMSubscriptionsClientFactory()
	if err != nil {
		return nil, nil, err
	}
	return credential, func(ctx context.Context, name string) (string, error) {
		return framework.GetSubscriptionID(ctx, factory.NewClient(), name)
	}, nil
}

func checkOwnerGroup(ctx context.Context, credential azcore.TokenCredential, settings ownerSettings) error {
	directory, err := owners.NewGraphDirectory(credential)
	if err != nil {
		return err
	}
	client, err := msgraph.NewGraphServiceClientWithCredentials(credential, []string{graph.Scope})
	if err != nil {
		return err
	}
	return owners.Check(ctx, strictOwnerDirectory{Directory: directory, store: &azureStore{graph: client}}, settings.GroupID, settings.IDs)
}

func (*Handler) Kind() assets.Kind                                       { return catalog.KindMockIdentities }
func (*Handler) AttachOptions(*pflag.FlagSet)                            {}
func (*Handler) Declared(pool catalog.Pool) bool                         { return pool.SlotAssets.MockIdentities != nil }
func (*Handler) ReleaseLease(context.Context, assets.LeaseRequest) error { return nil }

func IdentityNames(slot catalog.ExpandedSlot) []string {
	identity := uuid.NewSHA1(uuid.NameSpaceURL, []byte(strings.ToLower(slot.Subscriptions.E2E.ID)+"/"+slot.ResourceName)).String()
	prefix := "aro-hcp-slot-" + identity
	return []string{prefix + "-msi", prefix + "-backend", prefix + "-cs"}
}

func validateSlot(slot catalog.ExpandedSlot) error {
	if slot.MockIdentityConfig == nil || slot.MockIdentityConfig.Allocation != catalog.AllocationDedicated {
		return fmt.Errorf("mock identities require a dedicated slot declaration")
	}
	if _, err := uuid.Parse(slot.Subscriptions.E2E.ID); err != nil || slot.ResourceName == "" {
		return fmt.Errorf("mock identities require a resolved customer subscription and slot")
	}
	return nil
}

// reconcile creates or verifies the slot's identities and their roles on the
// slot's customer subscription.
func reconcile(ctx context.Context, store identityStore, slot catalog.ExpandedSlot, apply bool) (*leaseenv.MockIdentityBundlePayload, error) {
	bundle, err := reconcileIdentities(ctx, store, slot, apply)
	if err != nil {
		return nil, err
	}
	if err := reconcileSubscriptionRoles(ctx, store, slot, bundle, apply); err != nil {
		return nil, err
	}
	return bundle, nil
}

// reconcileSubscriptionRoles grants or verifies the bundle's roles on the
// slot's customer subscription.
func reconcileSubscriptionRoles(ctx context.Context, store identityStore, slot catalog.ExpandedSlot, bundle *leaseenv.MockIdentityBundlePayload, apply bool) error {
	return store.Roles(ctx, slot.Subscriptions.E2E.ID, *bundle, apply)
}

// forEachSlot calls fn for every slot of the pools with the slot's customer
// subscription ID resolved. It is the walk shared by pool reconciliation and
// the subscription role reconciliation.
func forEachSlot(ctx context.Context, resolveSubscriptionID subscriptionIDResolver, environment string, pools []catalog.Pool, fn func(catalog.Pool, catalog.ExpandedSlot) error) error {
	for _, pool := range pools {
		subscriptionID, err := resolveSubscriptionID(ctx, pool.Subscriptions.E2E)
		if err != nil {
			return fmt.Errorf("resolve subscription %q: %w", pool.Subscriptions.E2E, err)
		}
		for _, slot := range catalog.ExpandSlotsForPool(environment, pool) {
			slot.Subscriptions.E2E.ID = subscriptionID
			if err := fn(pool, slot); err != nil {
				return err
			}
		}
	}
	return nil
}

// reconcileIdentities creates or verifies only the slot's Entra applications,
// service principals and certificates. It needs no access to the subscription.
func reconcileIdentities(ctx context.Context, store identityStore, slot catalog.ExpandedSlot, apply bool) (*leaseenv.MockIdentityBundlePayload, error) {
	if err := validateSlot(slot); err != nil {
		return nil, err
	}
	return resolveBundle(ctx, store, slot, apply)
}

// resolveBundle is the identity and certificate core shared by lease admission,
// pool reconciliation and external maintenance. Authorization, roles and owner
// mutation belong to the caller.
func resolveBundle(ctx context.Context, store identityStore, slot catalog.ExpandedSlot, apply bool) (*leaseenv.MockIdentityBundlePayload, error) {
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
		return nil, fmt.Errorf("validate mock identities for %s: %w", slot.ResourceName, err)
	}
	return bundle, nil
}

// reconcileBundleOwners enforces the owner list on every application and
// service principal of the slot's bundle.
func reconcileBundleOwners(ctx context.Context, store identityStore, slot catalog.ExpandedSlot, bundle *leaseenv.MockIdentityBundlePayload, desired []string, apply bool) error {
	principals := []leaseenv.MockPrincipal{bundle.MSI, bundle.Backend, bundle.ClustersService}
	for index, name := range IdentityNames(slot) {
		if err := store.Owners(ctx, name, principals[index], desired, apply); err != nil {
			return fmt.Errorf("owners %s: %w", name, err)
		}
	}
	return nil
}

func ownerRepair(slot catalog.ExpandedSlot, err error) error {
	var subscriptionArg string
	if id, parseErr := uuid.Parse(slot.Subscriptions.E2E.ID); parseErr == nil && id != uuid.Nil {
		subscriptionArg = " " + ExternalSubscriptionIDEnv + "=" + id.String()
	}
	return fmt.Errorf("mock identity bundle for %s is not ready: %w; ask an ARO-HCP operator with Entra application and certificate permissions to run: make -C test apply-pool-assets ENVIRONMENT=%s POOL=%s ASSET=mock_identities SUBSCRIPTION='%s'%s; roles on a subscription owned by another team are granted by that subscription's Owner with reconcile-external-subscription; CI never repairs identities or rotates credentials", slot.ResourceName, err, slot.Environment, slot.PoolName, slot.SubscriptionName, subscriptionArg)
}

func (handler *Handler) checkLease(ctx context.Context, request assets.LeaseRequest) error {
	if request.AcquiredSlotState == nil {
		return fmt.Errorf("missing acquired slot state")
	}
	slot := &request.AcquiredSlotState.Slot
	previous := slot.Assets.MockIdentities
	slot.Assets.MockIdentities = nil
	credential, err := clusterprofile.NewClusterProfileCredential(request.SelectedClusterProfileDir, nil)
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
		return ownerRepair(*slot, err)
	}
	if previous != nil && *previous != *bundle {
		return ownerRepair(*slot, fmt.Errorf("identity bundle changed while the primary slot lease was held"))
	}
	slot.Assets.MockIdentities = bundle
	return nil
}

func (*Handler) AcquireLease(context.Context, assets.LeaseRequest) error { return nil }
func (handler *Handler) AdmitLease(ctx context.Context, request assets.LeaseRequest) error {
	return handler.checkLease(ctx, request)
}

func (*Handler) PublishLease(_ context.Context, request assets.LeaseRequest, contract *lease.RuntimeContractBuilder) error {
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
	return contract.Add(string(catalog.KindMockIdentities), leaseenv.MockIdentityBundle, string(payload))
}

func (handler *Handler) ApplyPools(ctx context.Context, request assets.PoolRequest) error {
	return handler.pools(ctx, request, true)
}
func (handler *Handler) ValidatePools(ctx context.Context, request assets.PoolRequest) error {
	return handler.pools(ctx, request, false)
}

// externalSubscriptionIDs maps the display name of the unmanaged pools'
// subscription to the ID the owner supplied, so the operator never has to read
// the subscription. Slot identity names are derived from that ID. A wrong ID
// shows up as missing identities when the owner runs reconcile-external-subscription.
func (handler *Handler) externalSubscriptionIDs(pools []catalog.Pool) (map[string]string, error) {
	names := map[string]bool{}
	for _, pool := range pools {
		if pool.IsUnmanaged() {
			names[pool.Subscriptions.E2E] = true
		}
	}
	switch {
	case len(names) == 0:
		return nil, nil
	case len(names) > 1:
		return nil, fmt.Errorf("unmanaged pools of several subscriptions were selected; select one with SUBSCRIPTION=<display name>")
	}
	parsed, err := uuid.Parse(strings.TrimSpace(handler.externalSubscriptionID()))
	if err != nil || parsed == uuid.Nil {
		return nil, fmt.Errorf("%s must be the subscription ID of the externally owned subscription, as given by its owner", ExternalSubscriptionIDEnv)
	}
	// uuid.Parse also accepts braced, URN and unhyphenated forms, and the identity
	// names hash the ID's text, so only the canonical form may be stored.
	id := parsed.String()
	ids := map[string]string{}
	for name := range names {
		ids[name] = id
	}
	return ids, nil
}

func (handler *Handler) pools(ctx context.Context, request assets.PoolRequest, apply bool) error {
	var eligible []catalog.Pool
	for _, pool := range request.Pools {
		if handler.Declared(pool) && (pool.SlotAssets.E2EIdentities == nil || pool.SlotAssets.E2EIdentities.Provisioning != catalog.IdentityProvisioningUnmanaged || request.IncludeUnmanaged) && len(catalog.ExpandSlotsForPool(request.Environment, pool)) > 0 {
			eligible = append(eligible, pool)
		}
	}
	if len(eligible) == 0 {
		return nil
	}
	externalIDs, err := handler.externalSubscriptionIDs(eligible)
	if err != nil {
		return err
	}
	ownerList, err := handler.owners()
	if err != nil {
		return err
	}
	credential, resolveSubscriptionID, err := handler.azure()
	if err != nil {
		return err
	}
	azureResolve := resolveSubscriptionID
	resolveSubscriptionID = func(ctx context.Context, name string) (string, error) {
		if id, found := externalIDs[name]; found {
			return id, nil
		}
		return azureResolve(ctx, name)
	}
	store, err := handler.newStore(credential)
	if err != nil {
		return err
	}
	if err := handler.checkOwnerGroup(ctx, credential, ownerList); err != nil {
		return err
	}
	return forEachSlot(ctx, resolveSubscriptionID, request.Environment, eligible, func(pool catalog.Pool, slot catalog.ExpandedSlot) error {
		bounded, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		var bundle *leaseenv.MockIdentityBundlePayload
		var err error
		if pool.IsUnmanaged() {
			// The subscription is owned elsewhere: only its owner can grant roles
			// there, through ReconcileSubscriptionRoles.
			bundle, err = reconcileIdentities(bounded, store, slot, apply)
		} else {
			bundle, err = reconcile(bounded, store, slot, apply)
		}
		if err == nil {
			err = reconcileBundleOwners(bounded, store, slot, bundle, ownerList.IDs, apply)
		}
		if err != nil {
			return ownerRepair(slot, err)
		}
		return nil
	})
}
