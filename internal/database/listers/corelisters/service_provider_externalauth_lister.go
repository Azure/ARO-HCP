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
	"github.com/Azure/ARO-HCP/internal/database/listers/listerutils"
)

// ServiceProviderExternalAuthLister lists and gets ServiceProviderExternalAuths from an informer's indexer.
type ServiceProviderExternalAuthLister interface {
	List(ctx context.Context) ([]*coreapi.ServiceProviderExternalAuth, error)
	Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, externalAuthName string) (*coreapi.ServiceProviderExternalAuth, error)
	ListForExternalAuth(ctx context.Context, subscriptionName, resourceGroupName, clusterName, externalAuthName string) ([]*coreapi.ServiceProviderExternalAuth, error)
}

type serviceProviderExternalAuthLister struct {
	indexer cache.Indexer
}

func NewServiceProviderExternalAuthLister(indexer cache.Indexer) ServiceProviderExternalAuthLister {
	return &serviceProviderExternalAuthLister{indexer: indexer}
}

func (l *serviceProviderExternalAuthLister) List(ctx context.Context) ([]*coreapi.ServiceProviderExternalAuth, error) {
	return listerutils.ListAll[coreapi.ServiceProviderExternalAuth](l.indexer)
}

func (l *serviceProviderExternalAuthLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, externalAuthName string) (*coreapi.ServiceProviderExternalAuth, error) {
	key := coreapi.ToServiceProviderExternalAuthResourceIDString(subscriptionID, resourceGroupName, clusterName, externalAuthName)
	return listerutils.GetByKey[coreapi.ServiceProviderExternalAuth](l.indexer, key)
}

func (l *serviceProviderExternalAuthLister) ListForExternalAuth(ctx context.Context, subscriptionName, resourceGroupName, clusterName, externalAuthName string) ([]*coreapi.ServiceProviderExternalAuth, error) {
	key := coreapi.ToExternalAuthResourceIDString(subscriptionName, resourceGroupName, clusterName, externalAuthName)
	return listerutils.ListFromIndex[coreapi.ServiceProviderExternalAuth](l.indexer, ByExternalAuth, key)
}
