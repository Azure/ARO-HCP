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
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/listertestingutils"
)

// SliceApplyDesireLister implements kubeapplierlisters.ApplyDesireLister backed by a slice.
// Tests can populate Desires directly and the lister scans on every call.
type SliceApplyDesireLister struct {
	Desires []*kubeapplier.ApplyDesire
}

var _ kubeapplierlisters.ApplyDesireLister = &SliceApplyDesireLister{}

func (l *SliceApplyDesireLister) List(ctx context.Context) ([]*kubeapplier.ApplyDesire, error) {
	return l.Desires, nil
}

func (l *SliceApplyDesireLister) GetForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string,
) (*kubeapplier.ApplyDesire, error) {
	want := kubeapplier.ToClusterScopedApplyDesireResourceIDString(subscriptionID, resourceGroupName, clusterName, name)
	for _, d := range l.Desires {
		id := listertestingutils.ResourceIDOf(d)
		if id != nil && strings.EqualFold(id.String(), want) {
			return d, nil
		}
	}
	return nil, cosmosstorageutils.NewNotFoundError()
}

func (l *SliceApplyDesireLister) GetForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName, name string,
) (*kubeapplier.ApplyDesire, error) {
	want := kubeapplier.ToNodePoolScopedApplyDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, nodePoolName, name,
	)
	for _, d := range l.Desires {
		id := listertestingutils.ResourceIDOf(d)
		if id != nil && strings.EqualFold(id.String(), want) {
			return d, nil
		}
	}
	return nil, cosmosstorageutils.NewNotFoundError()
}

func (l *SliceApplyDesireLister) GetForSystemAdminCredentialRequest(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, credentialRequestName, name string,
) (*kubeapplier.ApplyDesire, error) {
	want := kubeapplier.ToSystemAdminCredentialRequestScopedApplyDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, credentialRequestName, name,
	)
	for _, d := range l.Desires {
		id := listertestingutils.ResourceIDOf(d)
		if id != nil && strings.EqualFold(id.String(), want) {
			return d, nil
		}
	}
	return nil, cosmosstorageutils.NewNotFoundError()
}

func (l *SliceApplyDesireLister) GetForSystemAdminCredentialRevocation(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, revocationName, name string,
) (*kubeapplier.ApplyDesire, error) {
	want := kubeapplier.ToSystemAdminCredentialRevocationScopedApplyDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, revocationName, name,
	)
	for _, d := range l.Desires {
		id := listertestingutils.ResourceIDOf(d)
		if id != nil && strings.EqualFold(id.String(), want) {
			return d, nil
		}
	}
	return nil, cosmosstorageutils.NewNotFoundError()
}

func (l *SliceApplyDesireLister) ListForManagementCluster(
	ctx context.Context, managementClusterResourceID *azcorearm.ResourceID,
) ([]*kubeapplier.ApplyDesire, error) {
	if managementClusterResourceID == nil {
		return nil, nil
	}
	want := managementClusterResourceID.String()
	var out []*kubeapplier.ApplyDesire
	for _, d := range l.Desires {
		if mc := d.GetManagementCluster(); mc != nil && strings.EqualFold(mc.String(), want) {
			out = append(out, d)
		}
	}
	return out, nil
}

func (l *SliceApplyDesireLister) ListForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName string,
) ([]*kubeapplier.ApplyDesire, error) {
	var out []*kubeapplier.ApplyDesire
	for _, d := range l.Desires {
		if listertestingutils.UnderCluster(listertestingutils.ResourceIDOf(d), subscriptionID, resourceGroupName, clusterName) {
			out = append(out, d)
		}
	}
	return out, nil
}

func (l *SliceApplyDesireLister) ListForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string,
) ([]*kubeapplier.ApplyDesire, error) {
	var out []*kubeapplier.ApplyDesire
	for _, d := range l.Desires {
		if listertestingutils.UnderNodePool(listertestingutils.ResourceIDOf(d), subscriptionID, resourceGroupName, clusterName, nodePoolName) {
			out = append(out, d)
		}
	}
	return out, nil
}

// SliceReadDesireLister implements kubeapplierlisters.ReadDesireLister backed by a slice.
type SliceReadDesireLister struct {
	Desires []*kubeapplier.ReadDesire
}

var _ kubeapplierlisters.ReadDesireLister = &SliceReadDesireLister{}

func (l *SliceReadDesireLister) List(ctx context.Context) ([]*kubeapplier.ReadDesire, error) {
	return l.Desires, nil
}

func (l *SliceReadDesireLister) GetForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string,
) (*kubeapplier.ReadDesire, error) {
	want := kubeapplier.ToClusterScopedReadDesireResourceIDString(subscriptionID, resourceGroupName, clusterName, name)
	for _, d := range l.Desires {
		id := listertestingutils.ResourceIDOf(d)
		if id != nil && strings.EqualFold(id.String(), want) {
			return d, nil
		}
	}
	return nil, cosmosstorageutils.NewNotFoundError()
}

func (l *SliceReadDesireLister) GetForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName, name string,
) (*kubeapplier.ReadDesire, error) {
	want := kubeapplier.ToNodePoolScopedReadDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, nodePoolName, name,
	)
	for _, d := range l.Desires {
		id := listertestingutils.ResourceIDOf(d)
		if id != nil && strings.EqualFold(id.String(), want) {
			return d, nil
		}
	}
	return nil, cosmosstorageutils.NewNotFoundError()
}

func (l *SliceReadDesireLister) GetForSystemAdminCredentialRequest(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, credentialRequestName, name string,
) (*kubeapplier.ReadDesire, error) {
	want := kubeapplier.ToSystemAdminCredentialRequestScopedReadDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, credentialRequestName, name,
	)
	for _, d := range l.Desires {
		id := listertestingutils.ResourceIDOf(d)
		if id != nil && strings.EqualFold(id.String(), want) {
			return d, nil
		}
	}
	return nil, cosmosstorageutils.NewNotFoundError()
}

func (l *SliceReadDesireLister) GetForSystemAdminCredentialRevocation(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, revocationName, name string,
) (*kubeapplier.ReadDesire, error) {
	want := kubeapplier.ToSystemAdminCredentialRevocationScopedReadDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, revocationName, name,
	)
	for _, d := range l.Desires {
		id := listertestingutils.ResourceIDOf(d)
		if id != nil && strings.EqualFold(id.String(), want) {
			return d, nil
		}
	}
	return nil, cosmosstorageutils.NewNotFoundError()
}

func (l *SliceReadDesireLister) ListForManagementCluster(
	ctx context.Context, managementClusterResourceID *azcorearm.ResourceID,
) ([]*kubeapplier.ReadDesire, error) {
	if managementClusterResourceID == nil {
		return nil, nil
	}
	want := managementClusterResourceID.String()
	var out []*kubeapplier.ReadDesire
	for _, d := range l.Desires {
		if mc := d.GetManagementCluster(); mc != nil && strings.EqualFold(mc.String(), want) {
			out = append(out, d)
		}
	}
	return out, nil
}

func (l *SliceReadDesireLister) ListForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName string,
) ([]*kubeapplier.ReadDesire, error) {
	var out []*kubeapplier.ReadDesire
	for _, d := range l.Desires {
		if listertestingutils.UnderCluster(listertestingutils.ResourceIDOf(d), subscriptionID, resourceGroupName, clusterName) {
			out = append(out, d)
		}
	}
	return out, nil
}

func (l *SliceReadDesireLister) ListForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string,
) ([]*kubeapplier.ReadDesire, error) {
	var out []*kubeapplier.ReadDesire
	for _, d := range l.Desires {
		if listertestingutils.UnderNodePool(listertestingutils.ResourceIDOf(d), subscriptionID, resourceGroupName, clusterName, nodePoolName) {
			out = append(out, d)
		}
	}
	return out, nil
}
