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

package kubeapplierlistertesting

import (
	"context"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/listertestingutils"
)

// managementClusterResourceIDs queries the provided lister and projects each
// management cluster to its resourceID. Used by the per-Type *Desire listers to
// fan out across every configured management cluster.
func managementClusterResourceIDs(ctx context.Context, lister kubeappliercosmosstorage.ManagementClusterLister) ([]*azcorearm.ResourceID, error) {
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

// DBApplyDesireLister implements kubeapplierlisters.ApplyDesireLister backed by a real
// kubeappliercosmosstorage.KubeApplierDBClients. Each call iterates the configured management
// clusters and aggregates per-container results — exercising the registry's
// thread-safe lookup path and per-MC kubeapplierlisters.
type DBApplyDesireLister struct {
	Clients kubeappliercosmosstorage.KubeApplierDBClients
	Lister  kubeappliercosmosstorage.ManagementClusterLister
}

var _ kubeapplierlisters.ApplyDesireLister = &DBApplyDesireLister{}

func (l *DBApplyDesireLister) List(ctx context.Context) ([]*kubeapplierapi.ApplyDesire, error) {
	rids, err := managementClusterResourceIDs(ctx, l.Lister)
	if err != nil {
		return nil, err
	}
	var all []*kubeapplierapi.ApplyDesire
	for _, rid := range rids {
		client := l.Clients.For(ctx, rid)
		if client == nil {
			continue
		}
		iter, err := client.Listers().ApplyDesires().List(ctx, nil)
		if err != nil {
			return nil, err
		}
		items, err := listertestingutils.CollectFromIterator(ctx, iter)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
	}
	return all, nil
}

func (l *DBApplyDesireLister) GetForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string,
) (*kubeapplierapi.ApplyDesire, error) {
	return findClusterDesireInAnyClient(ctx, l.Clients, l.Lister, name,
		func(c kubeappliercosmosstorage.KubeApplierDBClient) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire], error) {
			return c.ApplyDesiresForCluster(subscriptionID, resourceGroupName, clusterName)
		})
}

func (l *DBApplyDesireLister) GetForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName, name string,
) (*kubeapplierapi.ApplyDesire, error) {
	return findClusterDesireInAnyClient(ctx, l.Clients, l.Lister, name,
		func(c kubeappliercosmosstorage.KubeApplierDBClient) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire], error) {
			return c.ApplyDesiresForNodePool(subscriptionID, resourceGroupName, clusterName, nodePoolName)
		})
}

func (l *DBApplyDesireLister) GetForSystemAdminCredentialRequest(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, credentialRequestName, name string,
) (*kubeapplierapi.ApplyDesire, error) {
	return findClusterDesireInAnyClient(ctx, l.Clients, l.Lister, name,
		func(c kubeappliercosmosstorage.KubeApplierDBClient) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire], error) {
			return c.ApplyDesiresForSystemAdminCredentialRequest(subscriptionID, resourceGroupName, clusterName, credentialRequestName)
		})
}

func (l *DBApplyDesireLister) GetForSystemAdminCredentialRevocation(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, revocationName, name string,
) (*kubeapplierapi.ApplyDesire, error) {
	return findClusterDesireInAnyClient(ctx, l.Clients, l.Lister, name,
		func(c kubeappliercosmosstorage.KubeApplierDBClient) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire], error) {
			return c.ApplyDesiresForSystemAdminCredentialRevocation(subscriptionID, resourceGroupName, clusterName, revocationName)
		})
}

func (l *DBApplyDesireLister) GetForManagementCluster(
	ctx context.Context, stampIdentifier, name string,
) (*kubeapplierapi.ApplyDesire, error) {
	return findClusterDesireInAnyClient(ctx, l.Clients, l.Lister, name,
		func(c kubeappliercosmosstorage.KubeApplierDBClient) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire], error) {
			return c.ApplyDesiresForManagementCluster(stampIdentifier)
		})
}

func (l *DBApplyDesireLister) ListForManagementCluster(
	ctx context.Context, managementClusterResourceID *azcorearm.ResourceID,
) ([]*kubeapplierapi.ApplyDesire, error) {
	client := l.Clients.For(ctx, managementClusterResourceID)
	if client == nil {
		return nil, nil
	}
	iter, err := client.Listers().ApplyDesires().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return listertestingutils.CollectFromIterator(ctx, iter)
}

func (l *DBApplyDesireLister) ListForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName string,
) ([]*kubeapplierapi.ApplyDesire, error) {
	all, err := l.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []*kubeapplierapi.ApplyDesire
	for _, d := range all {
		if listertestingutils.UnderCluster(listertestingutils.ResourceIDOf(d), subscriptionID, resourceGroupName, clusterName) {
			out = append(out, d)
		}
	}
	return out, nil
}

func (l *DBApplyDesireLister) ListForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string,
) ([]*kubeapplierapi.ApplyDesire, error) {
	all, err := l.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []*kubeapplierapi.ApplyDesire
	for _, d := range all {
		if listertestingutils.UnderNodePool(listertestingutils.ResourceIDOf(d), subscriptionID, resourceGroupName, clusterName, nodePoolName) {
			out = append(out, d)
		}
	}
	return out, nil
}

// findClusterDesireInAnyClient tries Get on each configured per-MC client; first
// hit wins. Stops on the first non-NotFound error. crudFor lets the caller
// pick which per-scope CRUD method (ForCluster vs ForNodePool) to invoke.
func findClusterDesireInAnyClient[T any, P coreapi.CosmosMetadataAccessorPtr[T]](
	ctx context.Context, clients kubeappliercosmosstorage.KubeApplierDBClients, lister kubeappliercosmosstorage.ManagementClusterLister,
	name string, crudFor func(kubeappliercosmosstorage.KubeApplierDBClient) (cosmosstorageutils.ResourceCRUD[T, P], error),
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
		if !cosmosstorageutils.IsNotFoundError(err) {
			return nil, err
		}
	}
	return nil, cosmosstorageutils.NewNotFoundError()
}

// DBReadDesireLister implements kubeapplierlisters.ReadDesireLister backed by a real
// kubeappliercosmosstorage.KubeApplierDBClients.
type DBReadDesireLister struct {
	Clients kubeappliercosmosstorage.KubeApplierDBClients
	Lister  kubeappliercosmosstorage.ManagementClusterLister
}

var _ kubeapplierlisters.ReadDesireLister = &DBReadDesireLister{}

func (l *DBReadDesireLister) List(ctx context.Context) ([]*kubeapplierapi.ReadDesire, error) {
	rids, err := managementClusterResourceIDs(ctx, l.Lister)
	if err != nil {
		return nil, err
	}
	var all []*kubeapplierapi.ReadDesire
	for _, rid := range rids {
		client := l.Clients.For(ctx, rid)
		if client == nil {
			continue
		}
		iter, err := client.Listers().ReadDesires().List(ctx, nil)
		if err != nil {
			return nil, err
		}
		items, err := listertestingutils.CollectFromIterator(ctx, iter)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
	}
	return all, nil
}

func (l *DBReadDesireLister) GetForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string,
) (*kubeapplierapi.ReadDesire, error) {
	return findClusterDesireInAnyClient(ctx, l.Clients, l.Lister, name,
		func(c kubeappliercosmosstorage.KubeApplierDBClient) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire], error) {
			return c.ReadDesiresForCluster(subscriptionID, resourceGroupName, clusterName)
		})
}

func (l *DBReadDesireLister) GetForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName, name string,
) (*kubeapplierapi.ReadDesire, error) {
	return findClusterDesireInAnyClient(ctx, l.Clients, l.Lister, name,
		func(c kubeappliercosmosstorage.KubeApplierDBClient) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire], error) {
			return c.ReadDesiresForNodePool(subscriptionID, resourceGroupName, clusterName, nodePoolName)
		})
}

func (l *DBReadDesireLister) GetForSystemAdminCredentialRequest(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, credentialRequestName, name string,
) (*kubeapplierapi.ReadDesire, error) {
	return findClusterDesireInAnyClient(ctx, l.Clients, l.Lister, name,
		func(c kubeappliercosmosstorage.KubeApplierDBClient) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire], error) {
			return c.ReadDesiresForSystemAdminCredentialRequest(subscriptionID, resourceGroupName, clusterName, credentialRequestName)
		})
}

func (l *DBReadDesireLister) GetForSystemAdminCredentialRevocation(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, revocationName, name string,
) (*kubeapplierapi.ReadDesire, error) {
	return findClusterDesireInAnyClient(ctx, l.Clients, l.Lister, name,
		func(c kubeappliercosmosstorage.KubeApplierDBClient) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire], error) {
			return c.ReadDesiresForSystemAdminCredentialRevocation(subscriptionID, resourceGroupName, clusterName, revocationName)
		})
}

func (l *DBReadDesireLister) GetForManagementCluster(
	ctx context.Context, stampIdentifier, name string,
) (*kubeapplierapi.ReadDesire, error) {
	return findClusterDesireInAnyClient(ctx, l.Clients, l.Lister, name,
		func(c kubeappliercosmosstorage.KubeApplierDBClient) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire], error) {
			return c.ReadDesiresForManagementCluster(stampIdentifier)
		})
}

func (l *DBReadDesireLister) ListForManagementCluster(
	ctx context.Context, managementClusterResourceID *azcorearm.ResourceID,
) ([]*kubeapplierapi.ReadDesire, error) {
	client := l.Clients.For(ctx, managementClusterResourceID)
	if client == nil {
		return nil, nil
	}
	iter, err := client.Listers().ReadDesires().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return listertestingutils.CollectFromIterator(ctx, iter)
}

func (l *DBReadDesireLister) ListForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName string,
) ([]*kubeapplierapi.ReadDesire, error) {
	all, err := l.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []*kubeapplierapi.ReadDesire
	for _, d := range all {
		if listertestingutils.UnderCluster(listertestingutils.ResourceIDOf(d), subscriptionID, resourceGroupName, clusterName) {
			out = append(out, d)
		}
	}
	return out, nil
}

func (l *DBReadDesireLister) ListForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string,
) ([]*kubeapplierapi.ReadDesire, error) {
	all, err := l.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []*kubeapplierapi.ReadDesire
	for _, d := range all {
		if listertestingutils.UnderNodePool(listertestingutils.ResourceIDOf(d), subscriptionID, resourceGroupName, clusterName, nodePoolName) {
			out = append(out, d)
		}
	}
	return out, nil
}
