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

// Package kubeapplier contains union listers for the kube-applier *Desire
// types. UnionDesireLister[T] fans every read out to a configurable set of
// per-management-cluster sublisters keyed by management-cluster resourceID
// and merges the results, satisfying the same listers.<Type>DesireLister
// interface that any single-MC sublister satisfies. Add/Remove maintain the
// sublister set under a mutex; lookups take a snapshot under RLock so reads
// never block writes for the full duration of a Cosmos call.
//
// Use these when the backend needs a single lister surface that spans every
// management cluster's container. The simplest sublister to plug in is the
// indexer-backed listers.NewXxxDesireLister sitting on top of one
// informers.KubeApplierInformers per management cluster.
package kubeapplier

import (
	"context"
	"strings"
	"sync"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/database/listers"
)

// DesireLister is the type-parameterized contract satisfied by per-MC listers
// for any of the kube-applier *Desire types. listers.ApplyDesireLister
// and listers.ReadDesireLister each satisfy DesireLister[<corresponding type>]
// structurally.
type DesireLister[T any] interface {
	List(ctx context.Context) ([]*T, error)
	GetForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string) (*T, error)
	GetForNodePool(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName, name string) (*T, error)
	ListForManagementCluster(ctx context.Context, managementClusterResourceID *azcorearm.ResourceID) ([]*T, error)
	ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*T, error)
	ListForNodePool(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string) ([]*T, error)
}

// UnionDesireLister fans every read out to a set of DesireLister[T]
// sublisters keyed by management-cluster resourceID and merges the results.
// Add and Remove are thread-safe; reads snapshot the sublister set under
// RLock so they never block writers for the full duration of a downstream
// call.
type UnionDesireLister[T any] struct {
	mu         sync.RWMutex
	sublisters map[string]DesireLister[T] // key = lowercased(rid.String())
}

// Compile-time checks: the two concrete listers.<Type>DesireLister
// interfaces are each satisfied by *UnionDesireLister[<corresponding type>].
var (
	_ listers.ApplyDesireLister = (*UnionDesireLister[kubeapplier.ApplyDesire])(nil)
	_ listers.ReadDesireLister  = (*UnionDesireLister[kubeapplier.ReadDesire])(nil)
)

// NewUnionDesireLister returns an empty union; call Add to register
// per-management-cluster sublisters.
func NewUnionDesireLister[T any]() *UnionDesireLister[T] {
	return &UnionDesireLister[T]{sublisters: map[string]DesireLister[T]{}}
}

// Add registers a sublister under the given management cluster's resourceID.
// Calling Add a second time with the same resourceID replaces the previous
// sublister. A nil resourceID is a programming error and is ignored.
func (u *UnionDesireLister[T]) Add(managementClusterResourceID *azcorearm.ResourceID, sublister DesireLister[T]) {
	if managementClusterResourceID == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.sublisters[strings.ToLower(managementClusterResourceID.String())] = sublister
}

// Remove drops the sublister registered under the given management cluster's
// resourceID. A nil or unknown resourceID is a no-op.
func (u *UnionDesireLister[T]) Remove(managementClusterResourceID *azcorearm.ResourceID) {
	if managementClusterResourceID == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.sublisters, strings.ToLower(managementClusterResourceID.String()))
}

// snapshot returns the currently-registered sublisters under RLock. Callers
// iterate the snapshot without holding the lock so writers aren't blocked
// for the full duration of downstream calls.
func (u *UnionDesireLister[T]) snapshot() []DesireLister[T] {
	u.mu.RLock()
	defer u.mu.RUnlock()
	out := make([]DesireLister[T], 0, len(u.sublisters))
	for _, s := range u.sublisters {
		out = append(out, s)
	}
	return out
}

// lookup returns the sublister registered under the given resourceID, or nil
// if the resourceID is nil or unregistered.
func (u *UnionDesireLister[T]) lookup(rid *azcorearm.ResourceID) DesireLister[T] {
	if rid == nil {
		return nil
	}
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.sublisters[strings.ToLower(rid.String())]
}

// List concatenates every sublister's List output. The first downstream
// error short-circuits.
func (u *UnionDesireLister[T]) List(ctx context.Context) ([]*T, error) {
	var all []*T
	for _, sub := range u.snapshot() {
		items, err := sub.List(ctx)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
	}
	return all, nil
}

// GetForCluster tries each sublister in turn. First hit wins; NotFound is
// treated as "try the next one", any other error short-circuits.
func (u *UnionDesireLister[T]) GetForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string,
) (*T, error) {
	for _, sub := range u.snapshot() {
		d, err := sub.GetForCluster(ctx, subscriptionID, resourceGroupName, clusterName, name)
		if err == nil {
			return d, nil
		}
		if !database.IsNotFoundError(err) {
			return nil, err
		}
	}
	return nil, database.NewNotFoundError()
}

// GetForNodePool tries each sublister in turn. First hit wins.
func (u *UnionDesireLister[T]) GetForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName, name string,
) (*T, error) {
	for _, sub := range u.snapshot() {
		d, err := sub.GetForNodePool(ctx, subscriptionID, resourceGroupName, clusterName, nodePoolName, name)
		if err == nil {
			return d, nil
		}
		if !database.IsNotFoundError(err) {
			return nil, err
		}
	}
	return nil, database.NewNotFoundError()
}

// ListForManagementCluster delegates to the single sublister registered under
// the given management cluster's resourceID, if any. Returns (nil, nil) when
// no sublister is registered for that MC.
func (u *UnionDesireLister[T]) ListForManagementCluster(
	ctx context.Context, managementClusterResourceID *azcorearm.ResourceID,
) ([]*T, error) {
	sub := u.lookup(managementClusterResourceID)
	if sub == nil {
		return nil, nil
	}
	return sub.ListForManagementCluster(ctx, managementClusterResourceID)
}

// ListForCluster concatenates every sublister's ListForCluster output.
func (u *UnionDesireLister[T]) ListForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName string,
) ([]*T, error) {
	var all []*T
	for _, sub := range u.snapshot() {
		items, err := sub.ListForCluster(ctx, subscriptionID, resourceGroupName, clusterName)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
	}
	return all, nil
}

// ListForNodePool concatenates every sublister's ListForNodePool output.
func (u *UnionDesireLister[T]) ListForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string,
) ([]*T, error) {
	var all []*T
	for _, sub := range u.snapshot() {
		items, err := sub.ListForNodePool(ctx, subscriptionID, resourceGroupName, clusterName, nodePoolName)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
	}
	return all, nil
}
