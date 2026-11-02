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

// Package azureresourceslisters provides cache.Indexer-backed listers for the
// AzureResources container's resource types (DeploymentStackDesire).
package azureresourceslisters

// ByCluster groups DeploymentStackDesires by the lower-cased resource ID of
// their direct parent Cluster. It does not match desires nested deeper (e.g.
// under a NodePool) A future nested scope should get its own sibling index (e.g. ByNodePool)
// rather than widening this one, unlike kubeapplierinformers' identically-named ByCluster, which walks the
// full parent chain and so also matches node-pool-scoped ApplyDesire/ReadDesire.
// If in the future we need to match desires nested deeper, we should create a new index function and index name like for
// example ByClusterOrDescendant
const ByCluster = "byCluster"
