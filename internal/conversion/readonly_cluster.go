// Copyright 2025 Microsoft Corporation
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

package conversion

import (
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func CopyReadOnlyTrackedResourceValues(dest, src *arm.TrackedResource) {
	dest.ID = src.ID
	dest.Name = src.Name
	dest.Type = src.Type
	dest.Location = src.Location
	dest.SystemData = src.SystemData.DeepCopy()
}

func CopyReadOnlyClusterValues(dest, src *api.HCPOpenShiftCluster) {
	CopyReadOnlyTrackedResourceValues(&dest.TrackedResource, &src.TrackedResource)

	switch {
	case hasClusterIdentityToSet(src.Identity) && dest.Identity == nil:
		dest.Identity = &arm.ManagedServiceIdentity{}
	case src.Identity == nil && dest.Identity != nil:
		dest.Identity = nil
	}
	if hasClusterIdentityToSet(src.Identity) {
		copyReadOnlyManagedServiceIdentityValues(dest.Identity, src.Identity)
	}

	dest.ServiceProviderProperties = *src.ServiceProviderProperties.DeepCopy()
}

func copyReadOnlyManagedServiceIdentityValues(dest, src *arm.ManagedServiceIdentity) {
	dest.PrincipalID = src.PrincipalID
	dest.TenantID = src.TenantID

	// even though only the value is marked, the original code would force new map entries, but only if the value was non-nil.
	// it doesn't appear to match the comments, but I matched the behavior.
	for key, srcVal := range src.UserAssignedIdentities {
		if srcVal == nil {
			continue
		}
		if (srcVal.ClientID == nil || len(*srcVal.ClientID) == 0) &&
			(srcVal.PrincipalID == nil || len(*srcVal.PrincipalID) == 0) {
			continue
		}
		if dest.UserAssignedIdentities == nil {
			dest.UserAssignedIdentities = make(map[string]*arm.UserAssignedIdentity)
		}
		dest.UserAssignedIdentities[key] = srcVal.DeepCopy()
	}
}

func hasClusterIdentityToSet(src *arm.ManagedServiceIdentity) bool {
	if src == nil {
		return false
	}
	if len(src.PrincipalID) > 0 {
		return true
	}
	if len(src.TenantID) > 0 {
		return true
	}
	for _, v := range src.UserAssignedIdentities {
		if v != nil && (v.ClientID != nil || v.PrincipalID != nil) {
			return true
		}
	}

	return false
}
