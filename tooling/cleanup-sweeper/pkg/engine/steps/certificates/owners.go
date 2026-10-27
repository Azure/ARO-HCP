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

package certificates

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"

	cleanuppolicy "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/policy"
)

// ownerMaxAge bounds how stale an owner inventory may be when a certificate is
// deleted on its behalf.
const ownerMaxAge = 30 * time.Second

var errOwnerInventory = errors.New("owner inventory unavailable")

// OwnerInventoryFn returns the lower-cased owner tokens currently present in
// resource-group names, and the time the inventory started.
type OwnerInventoryFn func(ctx context.Context) (sets.Set[string], time.Time, error)

// NewOwnerInventory builds an OwnerInventoryFn that tokenizes the resource group
// names of every listed subscription. A missing subscription, a nameless group,
// a failed page, or an inventory slower than ownerMaxAge fails the whole inventory.
func NewOwnerInventory(subscriptions []string, groups map[string]ResourceGroupLister, now func() time.Time) OwnerInventoryFn {
	return func(ctx context.Context) (sets.Set[string], time.Time, error) {
		started := now()
		owners := sets.New[string]()
		for _, subscription := range subscriptions {
			client := groups[subscription]
			if client == nil {
				return nil, started, fmt.Errorf("missing required infrastructure subscription %s", subscription)
			}
			pager := client.NewListPager(nil)
			for pager.More() {
				page, err := pager.NextPage(ctx)
				if err != nil {
					return nil, started, fmt.Errorf("list owner resource groups in %s: %w", subscription, err)
				}
				for _, group := range page.Value {
					if group == nil || group.Name == nil || *group.Name == "" {
						return nil, started, fmt.Errorf("resource group missing name in %s", subscription)
					}
					// Any token in any RG name can veto a rule that extracts the same
					// owner token, including deleting resource groups.
					for _, token := range cleanuppolicy.OwnerTokens(*group.Name) {
						owners.Insert(token)
					}
				}
			}
		}
		if now().Sub(started) >= ownerMaxAge {
			return nil, started, fmt.Errorf("owner inventory took at least %s; refusing stale guard", ownerMaxAge)
		}
		return owners, started, nil
	}
}

// ownerGuard serves a shared owner inventory, refreshing it at most every
// ownerMaxAge. A failed refresh is sticky so no later delete proceeds on a stale guard.
type ownerGuard struct {
	mu     sync.Mutex
	owners OwnerInventoryFn
	now    func() time.Time
	set    sets.Set[string]
	at     time.Time
	failed error
}

func (g *ownerGuard) current(ctx context.Context) (sets.Set[string], time.Time, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.failed != nil {
		return nil, time.Time{}, g.failed
	}
	if g.at.IsZero() || g.now().Sub(g.at) >= ownerMaxAge {
		owners, at, err := g.owners(ctx)
		if err != nil {
			g.failed = fmt.Errorf("%w: %v", errOwnerInventory, err)
			return nil, time.Time{}, g.failed
		}
		g.set, g.at = owners, at
	}
	return g.set, g.at, nil
}
