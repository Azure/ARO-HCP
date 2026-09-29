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

package coreapihelpers

import (
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// DenyAssignmentStatusEnsured reports whether ClusterDenyAssignment has
// applied this deny assignment type on the managed resource group. A nil
// status, a type being deconfigured, or a desired principal that is not yet
// on ExcludePrincipals is not ensured.
func DenyAssignmentStatusEnsured(status *coreapi.DenyAssignmentStatus) bool {
	if status == nil || status.DeconfigureTimestamp != nil {
		return false
	}
	if status.PendingAzureResource != nil || status.AzureResource == nil || status.EnsuredPermissions == nil {
		return false
	}
	for principalID, identity := range status.ExcludedIdentities {
		if identity == nil || identity.DeconfigureTimestamp != nil {
			continue
		}
		if identity.EnsuredIdentity == nil || identity.EnsuredIdentity.PrincipalID != principalID {
			return false
		}
	}
	return true
}

// DenyAssignmentStatusIdentityExcluded reports whether this deny assignment
// exists on the managed resource group and the given principal is on its
// last successful ExcludePrincipals set. A draining type or identity is not
// excluded.
func DenyAssignmentStatusIdentityExcluded(status *coreapi.DenyAssignmentStatus, principalID string) bool {
	if status == nil || status.AzureResource == nil || status.DeconfigureTimestamp != nil {
		return false
	}
	row := status.ExcludedIdentities[principalID]
	if row == nil || row.DeconfigureTimestamp != nil || row.EnsuredIdentity == nil {
		return false
	}
	return row.EnsuredIdentity.PrincipalID == principalID
}
