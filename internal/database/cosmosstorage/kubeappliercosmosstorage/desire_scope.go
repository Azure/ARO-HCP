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

package kubeappliercosmosstorage

import (
	"errors"
	"fmt"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

// Ancestry is the broad ARM-rooting family of a desire's parent. It selects the
// resource-ID path builder: different ancestry families build resource IDs
// differently.
type Ancestry string

const (
	// ClusterAncestry is subscription-rooted (…/hcpOpenShiftClusters/…).
	// Built with ClusterNestedResourceIDBuilder.
	ClusterAncestry Ancestry = "cluster"
	// StampAncestry is provider-rooted (/providers/…/stamps/…/managementClusters/…).
	// Built with FleetResourceIDBuilder.
	StampAncestry Ancestry = "stamp"
)

// DesireScope is a validated parent resource that scopes a set of desires. Its
// resourceID is unexported and only set by the constructors below or by
// ParseDesireScope, so an invalid scope cannot be represented.
type DesireScope struct {
	ancestry   Ancestry
	resourceID *azcorearm.ResourceID
}

// Ancestry reports the scope's rooting family.
func (s DesireScope) Ancestry() Ancestry { return s.ancestry }

// ResourceID returns the scope's resource ID.
func (s DesireScope) ResourceID() *azcorearm.ResourceID { return s.resourceID }

// ResourceIDBuilder returns the resource-ID path builder for this scope's ancestry.
func (s DesireScope) ResourceIDBuilder() cosmosstorageutils.ResourceIDBuilder {
	switch s.ancestry {
	case ClusterAncestry:
		return cosmosstorageutils.ClusterNestedResourceIDBuilder{}
	case StampAncestry:
		return cosmosstorageutils.FleetResourceIDBuilder{}
	default:
		panic(fmt.Errorf("coding error: unknown kube-applier ancestry %q", s.ancestry))
	}
}

// ClusterScope returns a DesireScope for a cluster parent.
func ClusterScope(subscriptionID, resourceGroupName, clusterName string) (DesireScope, error) {
	id, err := coreapi.ToClusterResourceID(subscriptionID, resourceGroupName, clusterName)
	if err != nil {
		return DesireScope{}, err
	}
	return ParseDesireScope(id)
}

// NodePoolScope returns a DesireScope for a node pool parent.
func NodePoolScope(subscriptionID, resourceGroupName, clusterName, nodePoolName string) (DesireScope, error) {
	id, err := coreapi.ToNodePoolResourceID(subscriptionID, resourceGroupName, clusterName, nodePoolName)
	if err != nil {
		return DesireScope{}, err
	}
	return ParseDesireScope(id)
}

// CredentialRequestScope returns a DesireScope for a SystemAdminCredentialRequest parent.
func CredentialRequestScope(subscriptionID, resourceGroupName, clusterName, credentialRequestName string) (DesireScope, error) {
	id, err := coreapi.ToSystemAdminCredentialRequestResourceID(subscriptionID, resourceGroupName, clusterName, credentialRequestName)
	if err != nil {
		return DesireScope{}, err
	}
	return ParseDesireScope(id)
}

// CredentialRevocationScope returns a DesireScope for a SystemAdminCredentialRevocation parent.
func CredentialRevocationScope(subscriptionID, resourceGroupName, clusterName, revocationName string) (DesireScope, error) {
	id, err := coreapi.ToSystemAdminCredentialRevocationResourceID(subscriptionID, resourceGroupName, clusterName, revocationName)
	if err != nil {
		return DesireScope{}, err
	}
	return ParseDesireScope(id)
}

// ManagementClusterScope returns a DesireScope for a management cluster parent.
func ManagementClusterScope(stampIdentifier string) (DesireScope, error) {
	id, err := fleetapi.ToManagementClusterResourceID(stampIdentifier)
	if err != nil {
		return DesireScope{}, err
	}
	return ParseDesireScope(id)
}

// ParseDesireScope categorizes a raw parent resource ID into a DesireScope,
// rejecting any resource type that is not allowed to scope desires. The
// kube-applier controllers pass their generic key's parent through here, so an
// unknown parent is turned into an error rather than a silently-written orphan.
func ParseDesireScope(id *azcorearm.ResourceID) (DesireScope, error) {
	if id == nil {
		return DesireScope{}, errors.New("desire scope resource ID is nil")
	}
	ancestry, ok := ancestryForParentType(id.ResourceType)
	if !ok {
		return DesireScope{}, fmt.Errorf("resource type %q is not a valid desire scope", id.ResourceType)
	}
	return DesireScope{ancestry: ancestry, resourceID: id}, nil
}

// ancestryForParentType is the registry of resource types allowed to scope desires,
// and the ancestry each belongs to. It is the single source of truth shared by
// ParseDesireScope (validity) and ResourceIDBuilder (path builder).
func ancestryForParentType(rt azcorearm.ResourceType) (Ancestry, bool) {
	switch {
	case metadataapi.ResourceTypeEqual(rt, coreapi.ClusterResourceType),
		metadataapi.ResourceTypeEqual(rt, coreapi.NodePoolResourceType),
		metadataapi.ResourceTypeEqual(rt, coreapi.SystemAdminCredentialRequestResourceType),
		metadataapi.ResourceTypeEqual(rt, coreapi.SystemAdminCredentialRevocationResourceType):
		return ClusterAncestry, true
	case metadataapi.ResourceTypeEqual(rt, fleetapi.ManagementClusterResourceType):
		return StampAncestry, true
	default:
		return "", false
	}
}
