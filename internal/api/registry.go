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
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/utils/set"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

var (
	// we have some private fields that cmp.Diff chokes on. This skips them.
	// Use only for error messages unless you know what you're doing.
	CmpDiffOptions = []cmp.Option{
		cmpopts.IgnoreFields(azcorearm.ResourceID{}, "ResourceType"),
		cmpopts.IgnoreFields(azcorearm.ResourceID{}, "isChild"),
		cmpopts.IgnoreFields(azcorearm.ResourceID{}, "stringValue"),
		cmpopts.IgnoreFields(InternalID{}, "path"),
		cmpopts.IgnoreFields(InternalID{}, "kind"),
	}
)

func init() {
	// we need some semantic equalities added for our types and this is the standard location
	Must("",
		equality.Semantic.AddFuncs(
			func(a, b time.Time) bool {
				return a.UTC().Equal(b.UTC())
			},
			func(a, b azcorearm.ResourceID) bool {
				return strings.EqualFold(a.String(), b.String())
			},
			func(a, b azcorearm.ResourceType) bool {
				return strings.EqualFold(a.String(), b.String())
			},
			func(a, b InternalID) bool {
				return a.String() == b.String()
			},
		),
	)
}

const (
	ProviderNamespace               = "Microsoft.RedHatOpenShift"
	ProviderNamespaceDisplay        = "Azure Red Hat OpenShift"
	ClusterResourceTypeName         = "hcpOpenShiftClusters"
	VersionResourceTypeName         = "hcpOpenShiftVersions"
	NodePoolResourceTypeName        = "nodePools"
	ExternalAuthResourceTypeName    = "externalAuths"
	OperationResultResourceTypeName = "hcpOperationResults"
	OperationStatusResourceTypeName = "hcpOperationStatuses"
	ControllerResourceTypeName      = "hcpOpenShiftControllers"
	ResourceTypeDisplay             = "Hosted Control Plane (HCP) OpenShift Clusters"
)

var (
	OperationStatusResourceType        = azcorearm.NewResourceType(ProviderNamespace, OperationStatusResourceTypeName)
	ClusterResourceType                = azcorearm.NewResourceType(ProviderNamespace, ClusterResourceTypeName)
	ServiceProviderClusterResourceType = azcorearm.NewResourceType(ProviderNamespace, ClusterResourceTypeName+"/serviceProviderCluster")
	NodePoolResourceType               = azcorearm.NewResourceType(ProviderNamespace, ClusterResourceTypeName+"/"+NodePoolResourceTypeName)
	ExternalAuthResourceType           = azcorearm.NewResourceType(ProviderNamespace, ClusterResourceTypeName+"/"+ExternalAuthResourceTypeName)
	PreflightResourceType              = azcorearm.NewResourceType(ProviderNamespace, "deployments/preflight")
	VersionResourceType                = azcorearm.NewResourceType(ProviderNamespace, "locations/"+VersionResourceTypeName)
	ClusterControllerResourceType      = azcorearm.NewResourceType(ProviderNamespace, filepath.Join(ClusterResourceTypeName, ControllerResourceTypeName))
	NodePoolControllerResourceType     = azcorearm.NewResourceType(ProviderNamespace, filepath.Join(ClusterResourceTypeName, NodePoolResourceTypeName, ControllerResourceTypeName))
	ExternalAuthControllerResourceType = azcorearm.NewResourceType(ProviderNamespace, filepath.Join(ClusterResourceTypeName, ExternalAuthResourceTypeName, ControllerResourceTypeName))
)

type VersionedResource interface {
}

type VersionedCreatableResource[InternalAPIType any] interface {
	VersionedResource
	NewExternal() any
	SetDefaultValues(any) error
	ConvertToInternal() *InternalAPIType
}

type VersionedHCPOpenShiftCluster VersionedCreatableResource[HCPOpenShiftCluster]
type VersionedHCPOpenShiftClusterNodePool VersionedCreatableResource[HCPOpenShiftClusterNodePool]
type VersionedHCPOpenShiftClusterExternalAuth VersionedCreatableResource[HCPOpenShiftClusterExternalAuth]
type VersionedHCPOpenShiftVersion VersionedResource

// ValidationPathMapperFunc takes an internal path from validation and converts it to the external path
// for this particular version.  This needs to be as close as possible, but perfection isn't required since fields
// could be split or combined.
type ValidationPathMapperFunc func(path string) string

type Version interface {
	fmt.Stringer

	ValidationPathRewriter(obj any) (ValidationPathMapperFunc, error)

	// Resource Types
	// Passing a nil pointer creates a resource with default values.
	NewHCPOpenShiftCluster(*HCPOpenShiftCluster) VersionedHCPOpenShiftCluster
	NewHCPOpenShiftClusterNodePool(*HCPOpenShiftClusterNodePool) VersionedHCPOpenShiftClusterNodePool
	NewHCPOpenShiftClusterExternalAuth(*HCPOpenShiftClusterExternalAuth) VersionedHCPOpenShiftClusterExternalAuth
	NewHCPOpenShiftVersion(*HCPOpenShiftVersion) VersionedHCPOpenShiftVersion

	// Response Marshaling
	MarshalHCPOpenShiftClusterAdminCredential(*HCPOpenShiftClusterAdminCredential) ([]byte, error)
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
