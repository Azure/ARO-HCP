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

// UnionReadDesireLister is the ReadDesire peer of UnionApplyDesireLister;
// see that type's doc for the contract.
type UnionReadDesireLister struct {
	mu         sync.RWMutex
	sublisters map[string]listers.ReadDesireLister
}

var _ listers.ReadDesireLister = &UnionReadDesireLister{}

func NewUnionReadDesireLister() *UnionReadDesireLister {
	return &UnionReadDesireLister{sublisters: map[string]listers.ReadDesireLister{}}
}

func (u *UnionReadDesireLister) Add(managementClusterResourceID *azcorearm.ResourceID, sublister listers.ReadDesireLister) {
	if managementClusterResourceID == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.sublisters[strings.ToLower(managementClusterResourceID.String())] = sublister
}

func (u *UnionReadDesireLister) Remove(managementClusterResourceID *azcorearm.ResourceID) {
	if managementClusterResourceID == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.sublisters, strings.ToLower(managementClusterResourceID.String()))
}

func (u *UnionReadDesireLister) snapshot() []listers.ReadDesireLister {
	u.mu.RLock()
	defer u.mu.RUnlock()
	out := make([]listers.ReadDesireLister, 0, len(u.sublisters))
	for _, s := range u.sublisters {
		out = append(out, s)
	}
	return out
}

func (u *UnionReadDesireLister) lookup(rid *azcorearm.ResourceID) listers.ReadDesireLister {
	if rid == nil {
		return nil
	}
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.sublisters[strings.ToLower(rid.String())]
}

func (u *UnionReadDesireLister) List(ctx context.Context) ([]*kubeapplier.ReadDesire, error) {
	var all []*kubeapplier.ReadDesire
	for _, sub := range u.snapshot() {
		items, err := sub.List(ctx)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
	}
	return all, nil
}

func (u *UnionReadDesireLister) GetForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string,
) (*kubeapplier.ReadDesire, error) {
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

func (u *UnionReadDesireLister) GetForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName, name string,
) (*kubeapplier.ReadDesire, error) {
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

func (u *UnionReadDesireLister) ListForManagementCluster(
	ctx context.Context, managementClusterResourceID *azcorearm.ResourceID,
) ([]*kubeapplier.ReadDesire, error) {
	sub := u.lookup(managementClusterResourceID)
	if sub == nil {
		return nil, nil
	}
	return sub.ListForManagementCluster(ctx, managementClusterResourceID)
}

func (u *UnionReadDesireLister) ListForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName string,
) ([]*kubeapplier.ReadDesire, error) {
	var all []*kubeapplier.ReadDesire
	for _, sub := range u.snapshot() {
		items, err := sub.ListForCluster(ctx, subscriptionID, resourceGroupName, clusterName)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
	}
	return all, nil
}

func (u *UnionReadDesireLister) ListForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string,
) ([]*kubeapplier.ReadDesire, error) {
	var all []*kubeapplier.ReadDesire
	for _, sub := range u.snapshot() {
		items, err := sub.ListForNodePool(ctx, subscriptionID, resourceGroupName, clusterName, nodePoolName)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
	}
	return all, nil
}
