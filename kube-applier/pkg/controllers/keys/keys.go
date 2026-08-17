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

// Package keys defines typed workqueue keys for the kube-applier *Desire
// controllers. Mirrors backend's HCPClusterKey / HCPNodePoolKey pattern: a
// small comparable struct that the controller can use to look the desire
// up directly through its lister rather than scanning the cache.
package keys

import (
	"fmt"
	"path"
	"strings"

	"github.com/go-logr/logr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ApplyDesireKey identifies a single ApplyDesire by its parent resource ID and name.
type ApplyDesireKey struct {
	// ParentResourceID is the canonical lowercased resource ID string of the desire's parent.
	ParentResourceID string
	Name             string
}

// CRUD returns the right per-scope CRUD for this key's parent.
func (k ApplyDesireKey) CRUD(client kubeappliercosmosstorage.KubeApplierApplyDesireCRUD) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire], error) {
	parent, err := parseDesireScope(k.ParentResourceID)
	if err != nil {
		return nil, err
	}
	return client.ApplyDesiresFor(parent)
}

// GetResourceID returns the desire's full resource ID.
func (k ApplyDesireKey) GetResourceID() *azcorearm.ResourceID {
	s := strings.ToLower(path.Join(k.ParentResourceID, kubeapplierapi.ApplyDesireResourceTypeName, k.Name))
	return metadataapi.Must(azcorearm.ParseResourceID(s))
}

// AddLoggerValues implements utils.LoggableKey so the generic worker loop seeds
// per-reconcile logger fields straight from the resource ID — same key set the
// backend uses (subscription_id, resource_group, resource_name, resource_id,
// hcp_cluster_name).
func (k ApplyDesireKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(utils.LogValues{}.AddLogValuesForResourceID(k.GetResourceID())...)
}

// ReadDesireKey identifies a single ReadDesire.
type ReadDesireKey struct {
	// ParentResourceID is the canonical lowercased resource ID string of the desire's parent
	ParentResourceID string
	Name             string
}

// CRUD returns a parent-scoped CRUD for this key.
func (k ReadDesireKey) CRUD(client kubeappliercosmosstorage.KubeApplierReadDesireCRUD) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire], error) {
	parent, err := parseDesireScope(k.ParentResourceID)
	if err != nil {
		return nil, err
	}
	return client.ReadDesiresFor(parent)
}

// GetResourceID returns the desire's full resource ID.
func (k ReadDesireKey) GetResourceID() *azcorearm.ResourceID {
	s := strings.ToLower(path.Join(k.ParentResourceID, kubeapplierapi.ReadDesireResourceTypeName, k.Name))
	return metadataapi.Must(azcorearm.ParseResourceID(s))
}

// AddLoggerValues implements utils.LoggableKey so the generic worker loop seeds
// per-reconcile logger fields straight from the resource ID.
func (k ReadDesireKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(utils.LogValues{}.AddLogValuesForResourceID(k.GetResourceID())...)
}

// ApplyDesireKeyFromResourceID parses an ApplyDesireKey out of a *Desire's
// resource ID. The desire is the leaf; everything above it is the parent.
func ApplyDesireKeyFromResourceID(id *azcorearm.ResourceID) (ApplyDesireKey, error) {
	parts, err := parseDesireParts(id)
	if err != nil {
		return ApplyDesireKey{}, err
	}
	return ApplyDesireKey(parts), nil
}

// ReadDesireKeyFromResourceID is the ReadDesire parallel of ApplyDesireKeyFromResourceID.
func ReadDesireKeyFromResourceID(id *azcorearm.ResourceID) (ReadDesireKey, error) {
	parts, err := parseDesireParts(id)
	if err != nil {
		return ReadDesireKey{}, err
	}
	return ReadDesireKey(parts), nil
}

// desireParts is the shared shape of every desire key. Defining it as a private
// type lets us do clean conversions to the kind-specific exported keys without
// reflection.
type desireParts struct {
	ParentResourceID string
	Name             string
}

func parseDesireParts(id *azcorearm.ResourceID) (desireParts, error) {
	if id == nil {
		return desireParts{}, fmt.Errorf("resource ID is nil")
	}
	if id.Parent == nil {
		return desireParts{}, fmt.Errorf("desire %q has no parent in its resource ID", id.String())
	}
	return desireParts{
		ParentResourceID: strings.ToLower(id.Parent.String()),
		Name:             id.Name,
	}, nil
}

// parseDesireScope parses the stored parent resource ID string and categorizes
// it into a known-valid DesireScope, rejecting any parent that is not allowed
// to own desires.
func parseDesireScope(parentResourceID string) (kubeappliercosmosstorage.DesireScope, error) {
	id, err := azcorearm.ParseResourceID(parentResourceID)
	if err != nil {
		return kubeappliercosmosstorage.DesireScope{}, err
	}
	return kubeappliercosmosstorage.ParseDesireScope(id)
}
