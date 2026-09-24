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

package corelisters

import (
	"context"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/listers/listerutils"
)

// ExternalAuthLister lists and gets ExternalAuths from an informer's indexer.
type ExternalAuthLister interface {
	List(ctx context.Context) ([]*coreapi.ExternalAuth, error)
	Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, externalAuthName string) (*coreapi.ExternalAuth, error)
	ListForResourceGroup(ctx context.Context, subscriptionName, resourceGroupName string) ([]*coreapi.ExternalAuth, error)
	ListForCluster(ctx context.Context, subscriptionName, resourceGroupName, clusterName string) ([]*coreapi.ExternalAuth, error)
}

// externalAuthLister implements ExternalAuthLister backed by a SharedIndexInformer.
type externalAuthLister struct {
	indexer cache.Indexer
}

// NewExternalAuthLister creates an ExternalAuthLister from a SharedIndexInformer's indexer.
func NewExternalAuthLister(indexer cache.Indexer) ExternalAuthLister {
	return &externalAuthLister{
		indexer: indexer,
	}
}

func (l *externalAuthLister) List(ctx context.Context) ([]*coreapi.ExternalAuth, error) {
	return listerutils.ListAll[coreapi.ExternalAuth](l.indexer)
}

// Get retrieves a single ExternalAuth by subscription ID, resource group name, cluster name, and external auth name.
// The store key is the lowercased ResourceID string:
//
//	/subscriptions/<sub>/resourcegroups/<rg>/providers/microsoft.redhatopenshift/hcpopenshiftclusters/<cluster>/externalauths/<name>
func (l *externalAuthLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, externalAuthName string) (*coreapi.ExternalAuth, error) {
	key := coreapihelpers.ToExternalAuthResourceIDString(subscriptionID, resourceGroupName, clusterName, externalAuthName)
	return listerutils.GetByKey[coreapi.ExternalAuth](l.indexer, key)
}

func (l *externalAuthLister) ListForResourceGroup(ctx context.Context, subscriptionName, resourceGroupName string) ([]*coreapi.ExternalAuth, error) {
	key := coreapihelpers.ToResourceGroupResourceIDString(subscriptionName, resourceGroupName)
	return listerutils.ListFromIndex[coreapi.ExternalAuth](l.indexer, ByResourceGroup, key)
}

func (l *externalAuthLister) ListForCluster(ctx context.Context, subscriptionName, resourceGroupName, clusterName string) ([]*coreapi.ExternalAuth, error) {
	key := coreapihelpers.ToClusterResourceIDString(subscriptionName, resourceGroupName, clusterName)
	return listerutils.ListFromIndex[coreapi.ExternalAuth](l.indexer, ByCluster, key)
}
