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

// Package fleetlisters provides cache.Indexer-backed listers for the fleet
// resource types (Stamp, ManagementCluster). Each lister is informer-fed: a
// SharedIndexInformer populates the indexer, and these listers expose typed
// Get/List APIs over it. The generic store/indexer helpers live in the shared
// listerutils package.
package fleetlisters

// ByCSProvisionShard groups documents by their Cluster Service provision-shard
// ID. Used by backend controllers that fan out per shard.
const ByCSProvisionShard = "byCSProvisionShard"
