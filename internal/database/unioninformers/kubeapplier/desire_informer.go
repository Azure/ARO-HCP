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

// Package kubeapplier contains union informers for the kube-applier *Desire
// types, peering the union listers in
// internal/database/unionlisters/kubeapplier. Each union holds a set of
// per-management-cluster cache.SharedIndexInformers keyed by management
// cluster resourceID; event handlers registered on the union are propagated
// to every current sub-informer and to any sub-informer Added later.
//
// The union does not own sub-informer lifecycle: callers start each
// SharedIndexInformer themselves (typically against a per-MC ctx) and call
// Remove when that ctx is cancelled. The pattern fits a backend reactor that
// wants a single informer surface spanning every management cluster's
// container as MCs come and go.
package kubeapplier

import (
	"strings"
	"sync"
	"time"

	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// UnionDesireInformer fans event-handler registration out to a set of
// per-management-cluster SharedIndexInformers and reports the worst-case sync
// state. It is the informer peer of unionlisters/kubeapplier.UnionXxxDesireLister
// and is type-agnostic at this layer because cache.SharedIndexInformer is.
//
// All operations are thread-safe. Reads (HasSynced) snapshot the sub set
// under RLock; mutations (Add/Remove and (Add|Remove)EventHandler) hold the
// write lock for the duration of cache.SharedIndexInformer handler calls,
// which are expected to be quick.
type UnionDesireInformer struct {
	mu       sync.RWMutex
	subs     map[string]*subInformer // key = lowercased(rid.String())
	handlers map[*handlerEntry]struct{}
}

// subInformer tracks a registered SharedIndexInformer together with the
// per-handler ResourceEventHandlerRegistrations that the union has installed
// on it. The registrations are how we later RemoveEventHandler.
type subInformer struct {
	informer cache.SharedIndexInformer
	regs     map[*handlerEntry]cache.ResourceEventHandlerRegistration
}

// handlerEntry is the union's canonical record of an installed handler. The
// pointer to it keys the per-sub-informer registrations stored on each
// subInformer; the cache.ResourceEventHandlerRegistration returned to the
// caller is a unionHandlerRegistration that wraps this pointer plus a
// backref to the union (so HasSynced can read across all sub-informers).
type handlerEntry struct {
	handler      cache.ResourceEventHandler
	resyncPeriod time.Duration // 0 means use AddEventHandler instead of AddEventHandlerWithResyncPeriod
}

// unionHandlerRegistration is the registration handle returned to callers.
// HasSynced is true iff every sub-informer's registration of the wrapped
// handler reports HasSynced. With no sub-informers, the handler is vacuously
// synced.
type unionHandlerRegistration struct {
	owner *UnionDesireInformer
	entry *handlerEntry
}

var _ cache.ResourceEventHandlerRegistration = &unionHandlerRegistration{}

func (r *unionHandlerRegistration) HasSynced() bool {
	r.owner.mu.RLock()
	defer r.owner.mu.RUnlock()
	if _, known := r.owner.handlers[r.entry]; !known {
		return false
	}
	for _, sub := range r.owner.subs {
		subReg, ok := sub.regs[r.entry]
		if !ok {
			return false
		}
		if !subReg.HasSynced() {
			return false
		}
	}
	return true
}

// NewUnionDesireInformer returns an empty union; call Add to register
// per-management-cluster sub-informers and AddEventHandler to register
// handlers that fan out to them.
func NewUnionDesireInformer() *UnionDesireInformer {
	return &UnionDesireInformer{
		subs:     map[string]*subInformer{},
		handlers: map[*handlerEntry]struct{}{},
	}
}

// Add registers a SharedIndexInformer under the given management cluster's
// resourceID. Every handler previously registered on the union is installed
// on the new sub-informer before Add returns. A second Add under the same
// resourceID replaces the previous sub-informer, deregistering the union's
// handlers from it first. A nil resourceID is a programming error and is
// ignored.
//
// The caller is responsible for the sub-informer's lifecycle (Run/stop);
// Add only wires handler propagation.
func (u *UnionDesireInformer) Add(managementClusterResourceID *azcorearm.ResourceID, sub cache.SharedIndexInformer) error {
	if managementClusterResourceID == nil || sub == nil {
		return nil
	}
	key := strings.ToLower(managementClusterResourceID.String())

	u.mu.Lock()
	defer u.mu.Unlock()

	// Replace: detach handlers from any pre-existing sub under this key first.
	if old, ok := u.subs[key]; ok {
		u.removeAllHandlersLocked(old)
	}

	entry := &subInformer{
		informer: sub,
		regs:     map[*handlerEntry]cache.ResourceEventHandlerRegistration{},
	}
	for h := range u.handlers {
		reg, err := installHandler(sub, h)
		if err != nil {
			// Roll back: detach any handlers we already installed on this
			// sub before reporting the error, so the caller can retry from
			// a clean state.
			for _, r := range entry.regs {
				_ = sub.RemoveEventHandler(r)
			}
			return err
		}
		entry.regs[h] = reg
	}
	u.subs[key] = entry
	return nil
}

// Remove deregisters every union-installed handler from the sub-informer
// registered under the given management cluster's resourceID and drops it
// from the union. The sub-informer itself is not stopped — that is the
// caller's responsibility. A nil or unregistered resourceID is a no-op.
func (u *UnionDesireInformer) Remove(managementClusterResourceID *azcorearm.ResourceID) {
	if managementClusterResourceID == nil {
		return
	}
	key := strings.ToLower(managementClusterResourceID.String())

	u.mu.Lock()
	defer u.mu.Unlock()

	sub, ok := u.subs[key]
	if !ok {
		return
	}
	u.removeAllHandlersLocked(sub)
	delete(u.subs, key)
}

// AddEventHandler installs handler on every currently-registered sub-informer
// and remembers it so future Adds also receive it. The returned registration
// is opaque; pass it to RemoveEventHandler to detach the handler from all
// sub-informers (current and future).
func (u *UnionDesireInformer) AddEventHandler(handler cache.ResourceEventHandler) (cache.ResourceEventHandlerRegistration, error) {
	return u.addHandler(&handlerEntry{handler: handler})
}

// AddEventHandlerWithResyncPeriod is the resync-period variant; semantics are
// otherwise identical to AddEventHandler. The resync period is recorded so
// future Adds also install the handler with the same resync period.
func (u *UnionDesireInformer) AddEventHandlerWithResyncPeriod(
	handler cache.ResourceEventHandler, resyncPeriod time.Duration,
) (cache.ResourceEventHandlerRegistration, error) {
	return u.addHandler(&handlerEntry{handler: handler, resyncPeriod: resyncPeriod})
}

func (u *UnionDesireInformer) addHandler(h *handlerEntry) (cache.ResourceEventHandlerRegistration, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	// Install on every current sub. If any install fails, roll back the
	// installs we already did and report the error — the handler is not
	// remembered, so future Adds won't see it either.
	installed := map[*subInformer]cache.ResourceEventHandlerRegistration{}
	for _, sub := range u.subs {
		reg, err := installHandler(sub.informer, h)
		if err != nil {
			for s, r := range installed {
				_ = s.informer.RemoveEventHandler(r)
			}
			return nil, err
		}
		installed[sub] = reg
	}
	for sub, reg := range installed {
		sub.regs[h] = reg
	}
	u.handlers[h] = struct{}{}
	return &unionHandlerRegistration{owner: u, entry: h}, nil
}

// RemoveEventHandler detaches the handler from every current sub-informer
// and forgets it so future Adds will not install it again. Passing a
// registration that did not originate from this union is a no-op (mirrors
// the cache.SharedIndexInformer contract).
func (u *UnionDesireInformer) RemoveEventHandler(reg cache.ResourceEventHandlerRegistration) error {
	r, ok := reg.(*unionHandlerRegistration)
	if !ok || r.owner != u {
		return nil
	}
	u.mu.Lock()
	defer u.mu.Unlock()

	if _, known := u.handlers[r.entry]; !known {
		return nil
	}
	delete(u.handlers, r.entry)
	var firstErr error
	for _, sub := range u.subs {
		if subReg, ok := sub.regs[r.entry]; ok {
			if err := sub.informer.RemoveEventHandler(subReg); err != nil && firstErr == nil {
				firstErr = err
			}
			delete(sub.regs, r.entry)
		}
	}
	return firstErr
}

// HasSynced returns true only when every currently-registered sub-informer
// reports HasSynced. An empty union is considered synced (vacuously true);
// callers that need a populated union should gate on their MC count.
func (u *UnionDesireInformer) HasSynced() bool {
	u.mu.RLock()
	subs := make([]cache.SharedIndexInformer, 0, len(u.subs))
	for _, s := range u.subs {
		subs = append(subs, s.informer)
	}
	u.mu.RUnlock()

	for _, s := range subs {
		if !s.HasSynced() {
			return false
		}
	}
	return true
}

// removeAllHandlersLocked detaches every union-installed handler from the
// given sub. Must be called with u.mu held.
func (u *UnionDesireInformer) removeAllHandlersLocked(sub *subInformer) {
	for h, reg := range sub.regs {
		_ = sub.informer.RemoveEventHandler(reg)
		delete(sub.regs, h)
	}
}

// installHandler picks the right cache.SharedIndexInformer.AddEventHandler*
// overload based on whether the entry carries a resync period.
func installHandler(sub cache.SharedIndexInformer, h *handlerEntry) (cache.ResourceEventHandlerRegistration, error) {
	if h.resyncPeriod > 0 {
		return sub.AddEventHandlerWithResyncPeriod(h.handler, h.resyncPeriod)
	}
	return sub.AddEventHandler(h.handler)
}
