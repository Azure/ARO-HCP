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

package cosmosstorageutils

import (
	"path"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/coreapi"
)

// Resource IDs shared by this package's tests.
const (
	testSubscriptionID    = "11111111-1111-1111-1111-111111111111"
	testResourceGroupName = "testResourceGroup"
)

var (
	testClusterResourceID      = path.Join("/subscriptions", testSubscriptionID, "resourceGroups", testResourceGroupName, "providers", coreapi.ProviderNamespace, coreapi.ClusterResourceTypeName, "testCluster")
	testNodePoolResourceID     = path.Join(testClusterResourceID, coreapi.NodePoolResourceTypeName, "testNodePool")
	testExternalAuthResourceID = path.Join(testClusterResourceID, coreapi.ExternalAuthResourceTypeName, "testExtAuth")
)
