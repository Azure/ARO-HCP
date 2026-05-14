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
	"strings"

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

// DBApplyDesireLister implements listers.ApplyDesireLister backed by a real
// database.KubeApplierDBClient. Each call hits the underlying client; this is a
// useful test double for asserting interactions with both the cosmos
// (production) and mock clients.
type DBApplyDesireLister struct {
	Client database.KubeApplierDBClient
}

var _ listers.ApplyDesireLister = &DBApplyDesireLister{}

func (l *DBApplyDesireLister) List(ctx context.Context) ([]*kubeapplier.ApplyDesire, error) {
	iter, err := l.Client.GlobalListers().ApplyDesires().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return collectFromIterator(ctx, iter)
}

func (l *DBApplyDesireLister) GetForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string,
) (*kubeapplier.ApplyDesire, error) {
	mgmt, err := l.findManagementCluster(ctx, kubeapplier.ToClusterScopedApplyDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, name))
	if err != nil {
		return nil, err
	}
	parent := database.ResourceParent{
		SubscriptionID: subscriptionID, ResourceGroupName: resourceGroupName, ClusterName: clusterName,
	}
	crud, err := l.Client.KubeApplier(mgmt).ApplyDesires(parent)
	if err != nil {
		return nil, err
	}
	return crud.Get(ctx, name)
}

func (l *DBApplyDesireLister) GetForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName, name string,
) (*kubeapplier.ApplyDesire, error) {
	mgmt, err := l.findManagementCluster(ctx, kubeapplier.ToNodePoolScopedApplyDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, nodePoolName, name))
	if err != nil {
		return nil, err
	}
	parent := database.ResourceParent{
		SubscriptionID:    subscriptionID,
		ResourceGroupName: resourceGroupName,
		ClusterName:       clusterName,
		NodePoolName:      nodePoolName,
	}
	crud, err := l.Client.KubeApplier(mgmt).ApplyDesires(parent)
	if err != nil {
		return nil, err
	}
	return crud.Get(ctx, name)
}

func (l *DBApplyDesireLister) ListForManagementCluster(
	ctx context.Context, managementCluster string,
) ([]*kubeapplier.ApplyDesire, error) {
	iter, err := l.Client.PartitionListers(managementCluster).ApplyDesires().List(ctx, nil)
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

// findManagementCluster scans the cross-partition view for a single ApplyDesire whose
// resource ID matches and returns its management cluster. Used by the Get* helpers,
// which need a partition value to address the document. Unlike production code that
// would already know the partition, the test lister discovers it by matching the
// resource ID — fine for backend-style tests that hold container-wide credentials.
func (l *DBApplyDesireLister) findManagementCluster(ctx context.Context, resourceIDString string) (string, error) {
	iter, err := l.Client.GlobalListers().ApplyDesires().List(ctx, nil)
	if err != nil {
		return "", err
	}
	for _, d := range iter.Items(ctx) {
		id := d.GetResourceID()
		if id != nil && strings.EqualFold(id.String(), resourceIDString) {
			mc := d.GetManagementCluster()
			if mc == nil {
				return "", database.NewNotFoundError()
			}
			return strings.ToLower(mc.String()), nil
		}
	}
	if err := iter.GetError(); err != nil {
		return "", err
	}
	return "", database.NewNotFoundError()
}

// DBDeleteDesireLister implements listers.DeleteDesireLister backed by a real
// database.KubeApplierDBClient.
type DBDeleteDesireLister struct {
	Client database.KubeApplierDBClient
}

var _ listers.DeleteDesireLister = &DBDeleteDesireLister{}

func (l *DBDeleteDesireLister) List(ctx context.Context) ([]*kubeapplier.DeleteDesire, error) {
	iter, err := l.Client.GlobalListers().DeleteDesires().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return collectFromIterator(ctx, iter)
}

func (l *DBDeleteDesireLister) GetForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string,
) (*kubeapplier.DeleteDesire, error) {
	mgmt, err := l.findManagementCluster(ctx, kubeapplier.ToClusterScopedDeleteDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, name))
	if err != nil {
		return nil, err
	}
	parent := database.ResourceParent{
		SubscriptionID: subscriptionID, ResourceGroupName: resourceGroupName, ClusterName: clusterName,
	}
	crud, err := l.Client.KubeApplier(mgmt).DeleteDesires(parent)
	if err != nil {
		return nil, err
	}
	return crud.Get(ctx, name)
}

func (l *DBDeleteDesireLister) GetForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName, name string,
) (*kubeapplier.DeleteDesire, error) {
	mgmt, err := l.findManagementCluster(ctx, kubeapplier.ToNodePoolScopedDeleteDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, nodePoolName, name))
	if err != nil {
		return nil, err
	}
	parent := database.ResourceParent{
		SubscriptionID:    subscriptionID,
		ResourceGroupName: resourceGroupName,
		ClusterName:       clusterName,
		NodePoolName:      nodePoolName,
	}
	crud, err := l.Client.KubeApplier(mgmt).DeleteDesires(parent)
	if err != nil {
		return nil, err
	}
	return crud.Get(ctx, name)
}

func (l *DBDeleteDesireLister) ListForManagementCluster(
	ctx context.Context, managementCluster string,
) ([]*kubeapplier.DeleteDesire, error) {
	iter, err := l.Client.PartitionListers(managementCluster).DeleteDesires().List(ctx, nil)
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

func (l *DBDeleteDesireLister) findManagementCluster(ctx context.Context, resourceIDString string) (string, error) {
	iter, err := l.Client.GlobalListers().DeleteDesires().List(ctx, nil)
	if err != nil {
		return "", err
	}
	for _, d := range iter.Items(ctx) {
		id := d.GetResourceID()
		if id != nil && strings.EqualFold(id.String(), resourceIDString) {
			mc := d.GetManagementCluster()
			if mc == nil {
				return "", database.NewNotFoundError()
			}
			return strings.ToLower(mc.String()), nil
		}
	}
	if err := iter.GetError(); err != nil {
		return "", err
	}
	return "", database.NewNotFoundError()
}

// DBReadDesireLister implements listers.ReadDesireLister backed by a real
// database.KubeApplierDBClient.
type DBReadDesireLister struct {
	Client database.KubeApplierDBClient
}

var _ listers.ReadDesireLister = &DBReadDesireLister{}

func (l *DBReadDesireLister) List(ctx context.Context) ([]*kubeapplier.ReadDesire, error) {
	iter, err := l.Client.GlobalListers().ReadDesires().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return collectFromIterator(ctx, iter)
}

func (l *DBReadDesireLister) GetForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string,
) (*kubeapplier.ReadDesire, error) {
	mgmt, err := l.findManagementCluster(ctx, kubeapplier.ToClusterScopedReadDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, name))
	if err != nil {
		return nil, err
	}
	parent := database.ResourceParent{
		SubscriptionID: subscriptionID, ResourceGroupName: resourceGroupName, ClusterName: clusterName,
	}
	crud, err := l.Client.KubeApplier(mgmt).ReadDesires(parent)
	if err != nil {
		return nil, err
	}
	return crud.Get(ctx, name)
}

func (l *DBReadDesireLister) GetForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName, name string,
) (*kubeapplier.ReadDesire, error) {
	mgmt, err := l.findManagementCluster(ctx, kubeapplier.ToNodePoolScopedReadDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, nodePoolName, name))
	if err != nil {
		return nil, err
	}
	parent := database.ResourceParent{
		SubscriptionID:    subscriptionID,
		ResourceGroupName: resourceGroupName,
		ClusterName:       clusterName,
		NodePoolName:      nodePoolName,
	}
	crud, err := l.Client.KubeApplier(mgmt).ReadDesires(parent)
	if err != nil {
		return nil, err
	}
	return crud.Get(ctx, name)
}

func (l *DBReadDesireLister) ListForManagementCluster(
	ctx context.Context, managementCluster string,
) ([]*kubeapplier.ReadDesire, error) {
	iter, err := l.Client.PartitionListers(managementCluster).ReadDesires().List(ctx, nil)
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

func (l *DBReadDesireLister) findManagementCluster(ctx context.Context, resourceIDString string) (string, error) {
	iter, err := l.Client.GlobalListers().ReadDesires().List(ctx, nil)
	if err != nil {
		return "", err
	}
	for _, d := range iter.Items(ctx) {
		id := d.GetResourceID()
		if id != nil && strings.EqualFold(id.String(), resourceIDString) {
			mc := d.GetManagementCluster()
			if mc == nil {
				return "", database.NewNotFoundError()
			}
			return strings.ToLower(mc.String()), nil
		}
	}
	if err := iter.GetError(); err != nil {
		return "", err
	}
	return "", database.NewNotFoundError()
}
