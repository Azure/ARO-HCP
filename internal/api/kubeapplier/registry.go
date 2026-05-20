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

package kubeapplier

import (
	"path/filepath"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
)

const (
	ApplyDesireResourceTypeName  = "applyDesires"
	DeleteDesireResourceTypeName = "deleteDesires"
	ReadDesireResourceTypeName   = "readDesires"
)

// nestedResourceType is a small wrapper over azcorearm.NewResourceType that joins the
// nested path segments under our provider namespace, keeping the var declarations below
// short enough to read at a glance.
func nestedResourceType(parts ...string) azcorearm.ResourceType {
	return azcorearm.NewResourceType(api.ProviderNamespace, filepath.Join(parts...))
}

var (
	// ClusterScopedApplyDesireResourceType is applyDesires nested directly under a Cluster.
	ClusterScopedApplyDesireResourceType = nestedResourceType(api.ClusterResourceTypeName, ApplyDesireResourceTypeName)
	// NodePoolScopedApplyDesireResourceType is applyDesires nested under a NodePool under a Cluster.
	NodePoolScopedApplyDesireResourceType = nestedResourceType(api.ClusterResourceTypeName, api.NodePoolResourceTypeName, ApplyDesireResourceTypeName)

	// ClusterScopedDeleteDesireResourceType is deleteDesires nested directly under a Cluster.
	ClusterScopedDeleteDesireResourceType = nestedResourceType(api.ClusterResourceTypeName, DeleteDesireResourceTypeName)
	// NodePoolScopedDeleteDesireResourceType is deleteDesires nested under a NodePool under a Cluster.
	NodePoolScopedDeleteDesireResourceType = nestedResourceType(api.ClusterResourceTypeName, api.NodePoolResourceTypeName, DeleteDesireResourceTypeName)

	// ClusterScopedReadDesireResourceType is readDesires nested directly under a Cluster.
	ClusterScopedReadDesireResourceType = nestedResourceType(api.ClusterResourceTypeName, ReadDesireResourceTypeName)
	// NodePoolScopedReadDesireResourceType is readDesires nested under a NodePool under a Cluster.
	NodePoolScopedReadDesireResourceType = nestedResourceType(api.ClusterResourceTypeName, api.NodePoolResourceTypeName, ReadDesireResourceTypeName)
)
