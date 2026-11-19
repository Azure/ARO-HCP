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
	"fmt"
	"strings"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ManagedIdentityMetadata looks up status.ManagedIdentityDetails for
// identityResourceID. ok is false when there is no entry for it at all. An
// entry present but nil is an error: FetchManagedIdentitiesInfo never writes
// a nil value.
func ManagedIdentityMetadata(status *coreapi.ServiceProviderClusterStatus, identityResourceID *azcorearm.ResourceID) (*coreapi.ManagedIdentityMetadata, bool, error) {
	key := strings.ToLower(identityResourceID.String())
	metadata, hasMetadata := status.ManagedIdentityDetails[key]
	if !hasMetadata {
		return nil, false, nil
	}
	if metadata == nil {
		return nil, false, utils.TrackError(fmt.Errorf("ManagedIdentityDetails has a nil metadata entry for resource ID %s", key))
	}
	return metadata, true, nil
}

// RoleAssignmentTargetIdentityFromMetadataValue builds the RoleAssignmentTargetIdentity
// snapshot for identityResourceID from value, one source of ManagedIdentityMetadata.
// ok is false when value is nil or not yet fully resolved.
func RoleAssignmentTargetIdentityFromMetadataValue(value *coreapi.IdentityMetadataValue, identityResourceID *azcorearm.ResourceID) (*coreapi.RoleAssignmentTargetIdentity, bool) {
	if value == nil || !IdentityMetadataValueHasResolvedIdentityInformation(value) {
		return nil, false
	}
	return &coreapi.RoleAssignmentTargetIdentity{
		ResourceID:  identityResourceID,
		ClientID:    ptr.Deref(value.ClientID, ""),
		TenantID:    ptr.Deref(value.TenantID, ""),
		PrincipalID: ptr.Deref(value.PrincipalID, ""),
	}, true
}

// ResolveMSIBasedRoleAssignmentTargetIdentity returns the identity generation
// snapshot for an MSI-based role assignment (control-plane operators and the
// service managed identity). Which Cosmos source applies is
// managedIdentitiesDataPlaneServiceAvailable, the same environment signal
// FetchManagedIdentitiesInfo uses: MetadataFromManagedIdentitiesDataplaneService
// when true, otherwise MetadataFromHardcodedIdentity. A nil pointer or
// unresolved value on that source means Fetch has not resolved it yet. The
// other MSI source is not consulted. ARM is ignored even when the same UAMI
// is also a data-plane operator. ok is false when the entry is missing or the
// chosen source is not fully resolved.
func ResolveMSIBasedRoleAssignmentTargetIdentity(status *coreapi.ServiceProviderClusterStatus, identityResourceID *azcorearm.ResourceID, managedIdentitiesDataPlaneServiceAvailable bool) (*coreapi.RoleAssignmentTargetIdentity, bool, error) {
	metadata, ok, err := ManagedIdentityMetadata(status, identityResourceID)
	if err != nil || !ok {
		return nil, false, err
	}
	source := metadata.MetadataFromHardcodedIdentity
	if managedIdentitiesDataPlaneServiceAvailable {
		source = metadata.MetadataFromManagedIdentitiesDataplaneService
	}
	target, ok := RoleAssignmentTargetIdentityFromMetadataValue(source, identityResourceID)
	return target, ok, nil
}

// ResolveDataPlaneRoleAssignmentTargetIdentity returns the identity
// generation snapshot for a data-plane operator role assignment from ARM
// User Assigned Identities. Dataplane and hardcoded metadata are ignored
// even when the same UAMI is also a control-plane operator. ok is false when
// the entry is missing or ARM is not fully resolved.
func ResolveDataPlaneRoleAssignmentTargetIdentity(status *coreapi.ServiceProviderClusterStatus, identityResourceID *azcorearm.ResourceID) (*coreapi.RoleAssignmentTargetIdentity, bool, error) {
	metadata, ok, err := ManagedIdentityMetadata(status, identityResourceID)
	if err != nil || !ok {
		return nil, false, err
	}
	target, ok := RoleAssignmentTargetIdentityFromMetadataValue(metadata.MetadataFromARMUserAssignedIdentitiesAPI, identityResourceID)
	return target, ok, nil
}
