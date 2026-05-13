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

package database

import (
	"path"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
)

type ServiceProviderNodePoolCRUD interface {
	ResourceCRUD[resourcesapi.ServiceProviderNodePool]
}

func NewNodePoolResourceID(subscriptionID, resourceGroupName, clusterName, nodePoolName string) *azcorearm.ResourceID {
	parts := []string{
		"/subscriptions",
		strings.ToLower(subscriptionID),
		"resourceGroups",
		resourceGroupName,
		"providers",
		resourcesapi.ClusterResourceType.Namespace,
		resourcesapi.ClusterResourceType.Type,
		clusterName,
		resourcesapi.NodePoolResourceTypeName,
		nodePoolName,
	}
	return resourcesapi.Must(azcorearm.ParseResourceID(strings.ToLower(path.Join(parts...))))
}
