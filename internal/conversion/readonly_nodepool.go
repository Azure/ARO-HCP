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

func CopyReadOnlyNodePoolValues(dest, src *api.HCPOpenShiftClusterNodePool) {
	// the old code appeared to shallow copies only

	dest.ID = src.ID
	dest.Name = src.Name
	dest.Type = src.Type
	dest.SystemData = src.SystemData

	switch {
	case hasClusterIdentityToSet(src.Identity) && dest.Identity == nil:
		dest.Identity = &arm.ManagedServiceIdentity{}
	case src.Identity == nil && dest.Identity != nil:
		dest.Identity = nil
	}
	if hasClusterIdentityToSet(src.Identity) {
		copyReadOnlyManagedServiceIdentityValues(dest.Identity, src.Identity)
	}

	dest.Properties.ProvisioningState = src.Properties.ProvisioningState
}
