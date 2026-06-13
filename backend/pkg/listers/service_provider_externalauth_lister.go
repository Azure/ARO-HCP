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

package listers

import (
	"context"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api"
)

// ServiceProviderExternalAuthLister lists and gets ServiceProviderExternalAuths from an informer's indexer.
type ServiceProviderExternalAuthLister interface {
	List(ctx context.Context) ([]*api.ServiceProviderExternalAuth, error)
	Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, externalAuthName string) (*api.ServiceProviderExternalAuth, error)
	ListForExternalAuth(ctx context.Context, subscriptionName, resourceGroupName, clusterName, externalAuthName string) ([]*api.ServiceProviderExternalAuth, error)
}

// serviceProviderExternalAuthLister implements ServiceProviderExternalAuthLister backed by a SharedIndexInformer.
type serviceProviderExternalAuthLister struct {
	indexer cache.Indexer
}

// NewServiceProviderExternalAuthLister creates a ServiceProviderExternalAuthLister from a SharedIndexInformer's indexer.
func NewServiceProviderExternalAuthLister(indexer cache.Indexer) ServiceProviderExternalAuthLister {
	return &serviceProviderExternalAuthLister{
		indexer: indexer,
	}
}

func (l *serviceProviderExternalAuthLister) List(ctx context.Context) ([]*api.ServiceProviderExternalAuth, error) {
	return listAll[api.ServiceProviderExternalAuth](l.indexer)
}

// Get retrieves a single ServiceProviderExternalAuth by subscription ID, resource group name, cluster name, and external auth name.
// ServiceProviderExternalAuth is a singleton resource with the name "default".
func (l *serviceProviderExternalAuthLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, externalAuthName string) (*api.ServiceProviderExternalAuth, error) {
	key := api.ToServiceProviderExternalAuthResourceIDString(subscriptionID, resourceGroupName, clusterName, externalAuthName)
	return getByKey[api.ServiceProviderExternalAuth](l.indexer, key)
}

func (l *serviceProviderExternalAuthLister) ListForExternalAuth(ctx context.Context, subscriptionName, resourceGroupName, clusterName, externalAuthName string) ([]*api.ServiceProviderExternalAuth, error) {
	key := api.ToExternalAuthResourceIDString(subscriptionName, resourceGroupName, clusterName, externalAuthName)
	return listFromIndex[api.ServiceProviderExternalAuth](l.indexer, ByExternalAuth, key)
}
