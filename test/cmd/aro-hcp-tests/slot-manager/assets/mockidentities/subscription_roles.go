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

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/catalog"
)

// ReconcileSubscriptionRoles grants (apply) or verifies (!apply) the mock
// identities' roles on the subscriptions of the request's pools. It is the
// counterpart of the identities-only pool apply for subscriptions owned by
// another team: it only needs the rights to assign roles there. The identities
// must already exist and have their certificates pinned, and no application,
// owner or certificate is created or changed. The subscription's mock role
// definitions must already be deployed.
func (handler *Handler) ReconcileSubscriptionRoles(ctx context.Context, request assets.PoolRequest, apply bool) error {
	credential, resolveSubscriptionID, err := handler.azure()
	if err != nil {
		return err
	}
	store, err := handler.newStore(credential)
	if err != nil {
		return fmt.Errorf("create mock identity client: %w", err)
	}
	var declared []catalog.Pool
	for _, pool := range request.Pools {
		if handler.Declared(pool) {
			declared = append(declared, pool)
		}
	}
	return forEachSlot(ctx, resolveSubscriptionID, request.Environment, declared, func(_ catalog.Pool, slot catalog.ExpandedSlot) error {
		bundle, err := reconcileIdentities(ctx, store, slot, false)
		if err != nil {
			return ownerRepair(slot, err)
		}
		if err := reconcileSubscriptionRoles(ctx, store, slot, bundle, apply); err != nil {
			return fmt.Errorf("mock identity grants for %s: %w", slot.ResourceName, err)
		}
		return nil
	})
}
