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

package kubeapplierlisters

import (
	"context"
	"strings"

	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/listers/listerutils"
)

// ReadDesireLister lists and gets ReadDesires from an informer's indexer.
type ReadDesireLister interface {
	List(ctx context.Context) ([]*kubeapplierapi.ReadDesire, error)
	GetForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string) (*kubeapplierapi.ReadDesire, error)
	GetForNodePool(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName, name string) (*kubeapplierapi.ReadDesire, error)
	GetForSystemAdminCredentialRequest(ctx context.Context, subscriptionID, resourceGroupName, clusterName, credentialRequestName, name string) (*kubeapplierapi.ReadDesire, error)
	GetForSystemAdminCredentialRevocation(ctx context.Context, subscriptionID, resourceGroupName, clusterName, revocationName, name string) (*kubeapplierapi.ReadDesire, error)
	GetForManagementCluster(ctx context.Context, stampIdentifier, name string) (*kubeapplierapi.ReadDesire, error)
	ListForManagementCluster(ctx context.Context, managementClusterResourceID *azcorearm.ResourceID) ([]*kubeapplierapi.ReadDesire, error)
	ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*kubeapplierapi.ReadDesire, error)
	ListForNodePool(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string) ([]*kubeapplierapi.ReadDesire, error)
}

type readDesireLister struct {
	indexer cache.Indexer
}

func (l *readDesireLister) List(ctx context.Context) ([]*kubeapplierapi.ReadDesire, error) {
	return listerutils.ListAll[kubeapplierapi.ReadDesire](l.indexer)
}

// NewReadDesireLister creates a ReadDesireLister from a SharedIndexInformer's indexer.
func NewReadDesireLister(indexer cache.Indexer) ReadDesireLister {
	return &readDesireLister{indexer: indexer}
}

func (l *readDesireLister) GetForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string,
) (*kubeapplierapi.ReadDesire, error) {
	key := kubeapplierapi.ToClusterScopedReadDesireResourceIDString(subscriptionID, resourceGroupName, clusterName, name)
	return listerutils.GetByKey[kubeapplierapi.ReadDesire](l.indexer, key)
}

func (l *readDesireLister) GetForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName, name string,
) (*kubeapplierapi.ReadDesire, error) {
	key := kubeapplierapi.ToNodePoolScopedReadDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, nodePoolName, name,
	)
	return listerutils.GetByKey[kubeapplierapi.ReadDesire](l.indexer, key)
}

func (l *readDesireLister) GetForSystemAdminCredentialRequest(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, credentialRequestName, name string,
) (*kubeapplierapi.ReadDesire, error) {
	key := kubeapplierapi.ToSystemAdminCredentialRequestScopedReadDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, credentialRequestName, name,
	)
	return listerutils.GetByKey[kubeapplierapi.ReadDesire](l.indexer, key)
}

func (l *readDesireLister) GetForSystemAdminCredentialRevocation(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, revocationName, name string,
) (*kubeapplierapi.ReadDesire, error) {
	key := kubeapplierapi.ToSystemAdminCredentialRevocationScopedReadDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, revocationName, name,
	)
	return listerutils.GetByKey[kubeapplierapi.ReadDesire](l.indexer, key)
}

func (l *readDesireLister) GetForManagementCluster(
	ctx context.Context, stampIdentifier, name string,
) (*kubeapplierapi.ReadDesire, error) {
	key := kubeapplierapi.ToManagementClusterScopedReadDesireResourceIDString(stampIdentifier, name)
	return listerutils.GetByKey[kubeapplierapi.ReadDesire](l.indexer, key)
}

func (l *readDesireLister) ListForManagementCluster(
	ctx context.Context, managementClusterResourceID *azcorearm.ResourceID,
) ([]*kubeapplierapi.ReadDesire, error) {
	if managementClusterResourceID == nil {
		return nil, nil
	}
	return listerutils.ListFromIndex[kubeapplierapi.ReadDesire](l.indexer, ByManagementCluster, strings.ToLower(managementClusterResourceID.String()))
}

func (l *readDesireLister) ListForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName string,
) ([]*kubeapplierapi.ReadDesire, error) {
	return listerutils.ListFromIndex[kubeapplierapi.ReadDesire](
		l.indexer, ByCluster, listerutils.ClusterIndexKey(subscriptionID, resourceGroupName, clusterName),
	)
}

func (l *readDesireLister) ListForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string,
) ([]*kubeapplierapi.ReadDesire, error) {
	return listerutils.ListFromIndex[kubeapplierapi.ReadDesire](
		l.indexer, ByNodePool, listerutils.NodePoolIndexKey(subscriptionID, resourceGroupName, clusterName, nodePoolName),
	)
}
