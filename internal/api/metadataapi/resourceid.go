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

package metadataapi

import (
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// ResourceTypeEqual reports whether two ARM resource types are equal, case-insensitively.
func ResourceTypeEqual(lhs, rhs azcorearm.ResourceType) bool {
	return strings.EqualFold(lhs.String(), rhs.String())
}

// ResourceTypeStringEqual reports whether a resource-type string equals an ARM resource type,
// case-insensitively.
func ResourceTypeStringEqual(s string, rt azcorearm.ResourceType) bool {
	return strings.EqualFold(s, rt.String())
}

// ClusterNameFromResourceID walks up the resource ID parent chain to find an HCP cluster ancestor.
// It returns the cluster's resource ID string if found, or the empty string otherwise. The provider
// namespace and cluster resource type are compared as string literals to avoid an import cycle with
// the coreapi package that defines those constants (coreapi imports metadataapi).
func ClusterNameFromResourceID(resourceID *azcorearm.ResourceID) string {
	if resourceID == nil {
		return ""
	}

	// Check if this resource is in our provider namespace.
	if !strings.EqualFold(resourceID.ResourceType.Namespace, "Microsoft.RedHatOpenShift") {
		return ""
	}
	// Check if this is an HCP cluster resource type.
	if strings.EqualFold(resourceID.ResourceType.Type, "hcpOpenShiftClusters") {
		return resourceID.String()
	}
	// Walk up the parent chain.
	return ClusterNameFromResourceID(resourceID.Parent)
}
