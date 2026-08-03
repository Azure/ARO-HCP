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

// Package listers provides cache.Indexer-backed listers for the backend's
// cross-partition views of the ARO-HCP resource documents.
//
// The generic store/indexer helpers (ListAll, GetByKey, ListFromIndex) live in
// the shared internal/database/listers/listerutils package and are used
// directly by the listers in this package.
package listers

type BackendListers struct {
	SubscriptionLister                    SubscriptionLister
	ActiveOperationLister                 ActiveOperationLister
	HCPOpenShiftClusterLister             ClusterLister
	HCPOpenShiftClusterNodePoolLister     NodePoolLister
	HCPOpenShiftClusterExternalAuthLister ExternalAuthLister
	ServiceProviderClusterLister          ServiceProviderClusterLister
	ServiceProviderNodePoolLister         ServiceProviderNodePoolLister
	ControllerLister                      ControllerLister
	BillingLister                         BillingLister
}

const (
	ByResourceGroup = "byResourceGroup"
	ByCluster       = "byCluster"
	ByNodePool      = "byNodePool"
	ByExternalAuth  = "byExternalAuth"
	BySubscription  = "bySubscription"
)
