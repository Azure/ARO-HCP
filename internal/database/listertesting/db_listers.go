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

package listertesting

import (
	"context"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/database/listers"
)

// collectFromIterator drains a database.DBClientIterator into a slice and propagates
// any iterator-level error.
func collectFromIterator[T any](ctx context.Context, iter database.DBClientIterator[T]) ([]*T, error) {
	var out []*T
	for _, v := range iter.Items(ctx) {
		out = append(out, v)
	}
	if err := iter.GetError(); err != nil {
		return nil, err
	}
	return out, nil
}

// managementClusterResourceIDs queries the provided lister and projects each
// management cluster to its resourceID. Used by the per-Type *Desire listers to
// fan out across every configured management cluster.
func managementClusterResourceIDs(ctx context.Context, lister database.ManagementClusterLister) ([]*azcorearm.ResourceID, error) {
	mcs, err := lister.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*azcorearm.ResourceID, 0, len(mcs))
	for _, mc := range mcs {
		rid := mc.ResourceID
		if rid == nil {
			rid = mc.CosmosMetadata.ResourceID
		}
		if rid == nil {
			continue
		}
		out = append(out, rid)
	}
	return out, nil
}

// DBApplyDesireLister implements listers.ApplyDesireLister backed by a real
// database.KubeApplierDBClients. Each call iterates the configured management
// clusters and aggregates per-container results — exercising the registry's
// thread-safe lookup path and per-MC listers.
type DBApplyDesireLister struct {
	Clients database.KubeApplierDBClients
	Lister  database.ManagementClusterLister
}

var _ listers.ApplyDesireLister = &DBApplyDesireLister{}

func (l *DBApplyDesireLister) List(ctx context.Context) ([]*kubeapplier.ApplyDesire, error) {
	rids, err := managementClusterResourceIDs(ctx, l.Lister)
	if err != nil {
		return nil, err
	}
	var all []*kubeapplier.ApplyDesire
	for _, rid := range rids {
		client := l.Clients.For(ctx, rid)
		if client == nil {
			continue
		}
		iter, err := client.Listers().ApplyDesires().List(ctx, nil)
		if err != nil {
			return nil, err
		}
		items, err := collectFromIterator(ctx, iter)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
	}
	return all, nil
}

func (l *DBApplyDesireLister) GetForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string,
) (*kubeapplier.ApplyDesire, error) {
	return findClusterDesireInAnyClient(ctx, l.Clients, l.Lister, name,
		func(c database.KubeApplierDBClient) (database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire], error) {
			return c.ApplyDesiresForCluster(subscriptionID, resourceGroupName, clusterName)
		})
}

func (l *DBApplyDesireLister) GetForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName, name string,
) (*kubeapplier.ApplyDesire, error) {
	return findClusterDesireInAnyClient(ctx, l.Clients, l.Lister, name,
		func(c database.KubeApplierDBClient) (database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire], error) {
			return c.ApplyDesiresForNodePool(subscriptionID, resourceGroupName, clusterName, nodePoolName)
		})
}

func (l *DBApplyDesireLister) ListForManagementCluster(
	ctx context.Context, managementClusterResourceID *azcorearm.ResourceID,
) ([]*kubeapplier.ApplyDesire, error) {
	client := l.Clients.For(ctx, managementClusterResourceID)
	if client == nil {
		return nil, nil
	}
	iter, err := client.Listers().ApplyDesires().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return collectFromIterator(ctx, iter)
}

func (l *DBApplyDesireLister) ListForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName string,
) ([]*kubeapplier.ApplyDesire, error) {
	all, err := l.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []*kubeapplier.ApplyDesire
	for _, d := range all {
		if underCluster(resourceIDOf(d), subscriptionID, resourceGroupName, clusterName) {
			out = append(out, d)
		}
	}
	return out, nil
}

func (l *DBApplyDesireLister) ListForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string,
) ([]*kubeapplier.ApplyDesire, error) {
	all, err := l.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []*kubeapplier.ApplyDesire
	for _, d := range all {
		if underNodePool(resourceIDOf(d), subscriptionID, resourceGroupName, clusterName, nodePoolName) {
			out = append(out, d)
		}
	}
	return out, nil
}

// findClusterDesireInAnyClient tries Get on each configured per-MC client; first
// hit wins. Stops on the first non-NotFound error. crudFor lets the caller
// pick which per-scope CRUD method (ForCluster vs ForNodePool) to invoke.
func findClusterDesireInAnyClient[T any, P arm.CosmosMetadataAccessorPtr[T]](
	ctx context.Context, clients database.KubeApplierDBClients, lister database.ManagementClusterLister,
	name string, crudFor func(database.KubeApplierDBClient) (database.ResourceCRUD[T, P], error),
) (*T, error) {
	rids, err := managementClusterResourceIDs(ctx, lister)
	if err != nil {
		return nil, err
	}
	for _, rid := range rids {
		client := clients.For(ctx, rid)
		if client == nil {
			continue
		}
		crud, err := crudFor(client)
		if err != nil {
			return nil, err
		}
		d, err := crud.Get(ctx, name)
		if err == nil {
			return d, nil
		}
		if !database.IsNotFoundError(err) {
			return nil, err
		}
	}
	return nil, database.NewNotFoundError()
}

// DBDeleteDesireLister implements listers.DeleteDesireLister backed by a real
// database.KubeApplierDBClients.
type DBDeleteDesireLister struct {
	Clients database.KubeApplierDBClients
	Lister  database.ManagementClusterLister
}

var _ listers.DeleteDesireLister = &DBDeleteDesireLister{}

func (l *DBDeleteDesireLister) List(ctx context.Context) ([]*kubeapplier.DeleteDesire, error) {
	rids, err := managementClusterResourceIDs(ctx, l.Lister)
	if err != nil {
		return nil, err
	}
	var all []*kubeapplier.DeleteDesire
	for _, rid := range rids {
		client := l.Clients.For(ctx, rid)
		if client == nil {
			continue
		}
		iter, err := client.Listers().DeleteDesires().List(ctx, nil)
		if err != nil {
			return nil, err
		}
		items, err := collectFromIterator(ctx, iter)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
	}
	return all, nil
}

func (l *DBDeleteDesireLister) GetForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string,
) (*kubeapplier.DeleteDesire, error) {
	return findClusterDesireInAnyClient(ctx, l.Clients, l.Lister, name,
		func(c database.KubeApplierDBClient) (database.ResourceCRUD[kubeapplier.DeleteDesire, *kubeapplier.DeleteDesire], error) {
			return c.DeleteDesiresForCluster(subscriptionID, resourceGroupName, clusterName)
		})
}

func (l *DBDeleteDesireLister) GetForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName, name string,
) (*kubeapplier.DeleteDesire, error) {
	return findClusterDesireInAnyClient(ctx, l.Clients, l.Lister, name,
		func(c database.KubeApplierDBClient) (database.ResourceCRUD[kubeapplier.DeleteDesire, *kubeapplier.DeleteDesire], error) {
			return c.DeleteDesiresForNodePool(subscriptionID, resourceGroupName, clusterName, nodePoolName)
		})
}

func (l *DBDeleteDesireLister) ListForManagementCluster(
	ctx context.Context, managementClusterResourceID *azcorearm.ResourceID,
) ([]*kubeapplier.DeleteDesire, error) {
	client := l.Clients.For(ctx, managementClusterResourceID)
	if client == nil {
		return nil, nil
	}
	iter, err := client.Listers().DeleteDesires().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return collectFromIterator(ctx, iter)
}

func (l *DBDeleteDesireLister) ListForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName string,
) ([]*kubeapplier.DeleteDesire, error) {
	all, err := l.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []*kubeapplier.DeleteDesire
	for _, d := range all {
		if underCluster(resourceIDOf(d), subscriptionID, resourceGroupName, clusterName) {
			out = append(out, d)
		}
	}
	return out, nil
}

func (l *DBDeleteDesireLister) ListForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string,
) ([]*kubeapplier.DeleteDesire, error) {
	all, err := l.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []*kubeapplier.DeleteDesire
	for _, d := range all {
		if underNodePool(resourceIDOf(d), subscriptionID, resourceGroupName, clusterName, nodePoolName) {
			out = append(out, d)
		}
	}
	return out, nil
}

// DBReadDesireLister implements listers.ReadDesireLister backed by a real
// database.KubeApplierDBClients.
type DBReadDesireLister struct {
	Clients database.KubeApplierDBClients
	Lister  database.ManagementClusterLister
}

var _ listers.ReadDesireLister = &DBReadDesireLister{}

func (l *DBReadDesireLister) List(ctx context.Context) ([]*kubeapplier.ReadDesire, error) {
	rids, err := managementClusterResourceIDs(ctx, l.Lister)
	if err != nil {
		return nil, err
	}
	var all []*kubeapplier.ReadDesire
	for _, rid := range rids {
		client := l.Clients.For(ctx, rid)
		if client == nil {
			continue
		}
		iter, err := client.Listers().ReadDesires().List(ctx, nil)
		if err != nil {
			return nil, err
		}
		items, err := collectFromIterator(ctx, iter)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
	}
	return all, nil
}

func (l *DBReadDesireLister) GetForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string,
) (*kubeapplier.ReadDesire, error) {
	return findClusterDesireInAnyClient(ctx, l.Clients, l.Lister, name,
		func(c database.KubeApplierDBClient) (database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire], error) {
			return c.ReadDesiresForCluster(subscriptionID, resourceGroupName, clusterName)
		})
}

func (l *DBReadDesireLister) GetForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName, name string,
) (*kubeapplier.ReadDesire, error) {
	return findClusterDesireInAnyClient(ctx, l.Clients, l.Lister, name,
		func(c database.KubeApplierDBClient) (database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire], error) {
			return c.ReadDesiresForNodePool(subscriptionID, resourceGroupName, clusterName, nodePoolName)
		})
}

func (l *DBReadDesireLister) ListForManagementCluster(
	ctx context.Context, managementClusterResourceID *azcorearm.ResourceID,
) ([]*kubeapplier.ReadDesire, error) {
	client := l.Clients.For(ctx, managementClusterResourceID)
	if client == nil {
		return nil, nil
	}
	iter, err := client.Listers().ReadDesires().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return collectFromIterator(ctx, iter)
}

func (l *DBReadDesireLister) ListForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName string,
) ([]*kubeapplier.ReadDesire, error) {
	all, err := l.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []*kubeapplier.ReadDesire
	for _, d := range all {
		if underCluster(resourceIDOf(d), subscriptionID, resourceGroupName, clusterName) {
			out = append(out, d)
		}
	}
	return out, nil
}

func (l *DBReadDesireLister) ListForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string,
) ([]*kubeapplier.ReadDesire, error) {
	all, err := l.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []*kubeapplier.ReadDesire
	for _, d := range all {
		if underNodePool(resourceIDOf(d), subscriptionID, resourceGroupName, clusterName, nodePoolName) {
			out = append(out, d)
		}
	}
	return out, nil
}
