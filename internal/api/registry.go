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

package api

import (
	"fmt"
	"sync"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/go-playground/validator/v10"
	"k8s.io/utils/set"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

const (
	ProviderNamespace               = "Microsoft.RedHatOpenShift"
	ProviderNamespaceDisplay        = "Azure Red Hat OpenShift"
	ClusterResourceTypeName         = "hcpOpenShiftClusters"
	VersionResourceTypeName         = "hcpOpenShiftVersions"
	NodePoolResourceTypeName        = "nodePools"
	ExternalAuthResourceTypeName    = "externalAuths"
	OperationResultResourceTypeName = "hcpOperationResults"
	OperationStatusResourceTypeName = "hcpOperationStatuses"
	ResourceTypeDisplay             = "Hosted Control Plane (HCP) OpenShift Clusters"
)

var (
	ClusterResourceType      = azcorearm.NewResourceType(ProviderNamespace, ClusterResourceTypeName)
	NodePoolResourceType     = azcorearm.NewResourceType(ProviderNamespace, ClusterResourceTypeName+"/"+NodePoolResourceTypeName)
	ExternalAuthResourceType = azcorearm.NewResourceType(ProviderNamespace, ClusterResourceTypeName+"/"+ExternalAuthResourceTypeName)
	PreflightResourceType    = azcorearm.NewResourceType(ProviderNamespace, "deployments/preflight")
	VersionResourceType      = azcorearm.NewResourceType(ProviderNamespace, "locations/"+VersionResourceTypeName)
)

type VersionedResource interface {
	GetVersion() Version
}

type VersionedCreatableResource[T any] interface {
	VersionedResource
	Normalize(*T)
	GetVisibility(path string) (VisibilityFlags, bool)
	ValidateVisibility(current VersionedCreatableResource[T], updating bool) []arm.CloudErrorBody
}

type VersionedHCPOpenShiftCluster VersionedCreatableResource[HCPOpenShiftCluster]
type VersionedHCPOpenShiftClusterNodePool VersionedCreatableResource[HCPOpenShiftClusterNodePool]
type VersionedHCPOpenShiftClusterExternalAuth VersionedCreatableResource[HCPOpenShiftClusterExternalAuth]
type VersionedHCPOpenShiftVersion VersionedResource

type Version interface {
	fmt.Stringer

	GetValidator() *validator.Validate

	// Resource Types
	// Passing a nil pointer creates a resource with default values.
	NewHCPOpenShiftCluster(*HCPOpenShiftCluster) VersionedHCPOpenShiftCluster
	NewHCPOpenShiftClusterNodePool(*HCPOpenShiftClusterNodePool) VersionedHCPOpenShiftClusterNodePool
	NewHCPOpenShiftClusterExternalAuth(*HCPOpenShiftClusterExternalAuth) VersionedHCPOpenShiftClusterExternalAuth
	NewHCPOpenShiftVersion(*HCPOpenShiftVersion) VersionedHCPOpenShiftVersion

	// Response Marshaling
	MarshalHCPOpenShiftClusterAdminCredential(*HCPOpenShiftClusterAdminCredential) ([]byte, error)
}

func ValidateVersionedHCPOpenShiftCluster(incoming, current VersionedHCPOpenShiftCluster, updating bool) *arm.CloudError {
	var errorDetails []arm.CloudErrorBody

	errorDetails = incoming.ValidateVisibility(current, updating)

	// Proceed with additional validation only if visibility validation has
	// passed. This avoids running further checks on changes we already know
	// to be invalid and prevents the response body from becoming overwhelming.
	if len(errorDetails) == 0 {
		var normalized HCPOpenShiftCluster

		incoming.Normalize(&normalized)

		errorDetails = ValidateRequest(incoming.GetVersion().GetValidator(), &normalized)

		// Proceed with complex, multi-field validation only if single-field
		// validation has passed. This avoids running further checks on data
		// we already know to be invalid and prevents the response body from
		// becoming overwhelming.
		if len(errorDetails) == 0 {
			errorDetails = normalized.Validate()
		}
	}

	// Returns nil if errorDetails is empty.
	return arm.NewContentValidationError(errorDetails)
}

func ValidateVersionedHCPOpenShiftClusterNodePool(incoming, current VersionedHCPOpenShiftClusterNodePool, cluster *HCPOpenShiftCluster, updating bool) *arm.CloudError {
	var errorDetails []arm.CloudErrorBody

	errorDetails = incoming.ValidateVisibility(current, updating)

	// Proceed with additional validation only if visibility validation has
	// passed. This avoids running further checks on changes we already know
	// to be invalid and prevents the response body from becoming overwhelming.
	if len(errorDetails) == 0 {
		var normalized HCPOpenShiftClusterNodePool

		incoming.Normalize(&normalized)

		errorDetails = ValidateRequest(incoming.GetVersion().GetValidator(), &normalized)

		// Proceed with complex, multi-field validation only if single-field
		// validation has passed. This avoids running further checks on data
		// we already know to be invalid and prevents the response body from
		// becoming overwhelming.
		if len(errorDetails) == 0 {
			errorDetails = normalized.Validate(cluster)
		}
	}

	// Returns nil if errorDetails is empty.
	return arm.NewContentValidationError(errorDetails)
}

func ValidateVersionedHCPOpenShiftClusterExternalAuth(incoming, current VersionedHCPOpenShiftClusterExternalAuth, cluster *HCPOpenShiftCluster, updating bool) *arm.CloudError {
	var errorDetails []arm.CloudErrorBody

	errorDetails = incoming.ValidateVisibility(current, updating)

	// Proceed with additional validation only if visibility validation has
	// passed. This avoids running further checks on changes we already know
	// to be invalid and prevents the response body from becoming overwhelming.
	if len(errorDetails) == 0 {
		var normalized HCPOpenShiftClusterExternalAuth

		incoming.Normalize(&normalized)

		errorDetails = ValidateRequest(incoming.GetVersion().GetValidator(), &normalized)

		// Proceed with complex, multi-field validation only if single-field
		// validation has passed. This avoids running further checks on data
		// we already know to be invalid and prevents the response body from
		// becoming overwhelming.
		if len(errorDetails) == 0 {
			errorDetails = normalized.Validate(cluster)
		}
	}

	// Returns nil if errorDetails is empty.
	return arm.NewContentValidationError(errorDetails)
}

// APIRegistry is a way to keep track of versioned interfaces.
// It should always be done per-instance, so we can easily track what registers where and why it does it.
// This construction also gives us a way to unit and integration test different scenarios without impacting a single
// global as we run the tests.
type APIRegistry interface {
	Register(version Version) error
	ListVersions() set.Set[string]
	Lookup(key string) (version Version, ok bool)
}

type apiRegistry struct {
	lock             sync.RWMutex
	versionToDetails map[string]Version
}

func NewAPIRegistry() APIRegistry {
	return &apiRegistry{
		versionToDetails: map[string]Version{},
	}
}

func (a *apiRegistry) Register(version Version) error {
	a.lock.Lock()
	defer a.lock.Unlock()

	if _, exists := a.versionToDetails[version.String()]; exists {
		return fmt.Errorf("version %s already registered", version.String())
	}

	a.versionToDetails[version.String()] = version
	return nil
}

func (a *apiRegistry) ListVersions() set.Set[string] {
	a.lock.RLock()
	defer a.lock.RUnlock()

	return set.KeySet(a.versionToDetails)
}

func (a *apiRegistry) Lookup(key string) (Version, bool) {
	a.lock.RLock()
	defer a.lock.RUnlock()

	version, ok := a.versionToDetails[key]
	return version, ok
}
