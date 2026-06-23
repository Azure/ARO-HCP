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
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
)

// ApplyDesireKey identifies a single ApplyDesire by the parts of its resource ID
// that map to the lister's GetForCluster / GetForNodePool helpers.
type ApplyDesireKey struct {
	SubscriptionID    string
	ResourceGroupName string
	ClusterName       string
	NodePoolName      string // empty for cluster-scoped
	Name              string
}

// IsNodePoolScoped reports whether this key targets a node-pool-scoped desire.
func (k ApplyDesireKey) IsNodePoolScoped() bool { return len(k.NodePoolName) > 0 }

// CRUD returns the right per-scope CRUD for this key's parent.
func (k ApplyDesireKey) CRUD(client database.KubeApplierApplyDesireCRUD) (database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire], error) {
	if k.IsNodePoolScoped() {
		return client.ApplyDesiresForNodePool(k.SubscriptionID, k.ResourceGroupName, k.ClusterName, k.NodePoolName)
	}
	return client.ApplyDesiresForCluster(k.SubscriptionID, k.ResourceGroupName, k.ClusterName)
}

// DeleteDesireKey identifies a single DeleteDesire.
type DeleteDesireKey struct {
	SubscriptionID    string
	ResourceGroupName string
	ClusterName       string
	NodePoolName      string
	Name              string
}

// IsNodePoolScoped reports whether this key targets a node-pool-scoped desire.
func (k DeleteDesireKey) IsNodePoolScoped() bool { return len(k.NodePoolName) > 0 }

// CRUD returns the right per-scope CRUD for this key's parent.
func (k DeleteDesireKey) CRUD(client database.KubeApplierDeleteDesireCRUD) (database.ResourceCRUD[kubeapplier.DeleteDesire, *kubeapplier.DeleteDesire], error) {
	if k.IsNodePoolScoped() {
		return client.DeleteDesiresForNodePool(k.SubscriptionID, k.ResourceGroupName, k.ClusterName, k.NodePoolName)
	}
	return client.DeleteDesiresForCluster(k.SubscriptionID, k.ResourceGroupName, k.ClusterName)
}

// ReadDesireKey identifies a single ReadDesire.
type ReadDesireKey struct {
	SubscriptionID    string
	ResourceGroupName string
	ClusterName       string
	NodePoolName      string
	Name              string
}

// IsNodePoolScoped reports whether this key targets a node-pool-scoped desire.
func (k ReadDesireKey) IsNodePoolScoped() bool { return len(k.NodePoolName) > 0 }

// CRUD returns the right per-scope CRUD for this key's parent.
func (k ReadDesireKey) CRUD(client database.KubeApplierReadDesireCRUD) (database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire], error) {
	if k.IsNodePoolScoped() {
		return client.ReadDesiresForNodePool(k.SubscriptionID, k.ResourceGroupName, k.ClusterName, k.NodePoolName)
	}
	return client.ReadDesiresForCluster(k.SubscriptionID, k.ResourceGroupName, k.ClusterName)
}

// FromResourceID parses an ApplyDesireKey out of a *Desire's resource ID. The
// caller is expected to have a desire whose resource ID is one of:
//
//	.../hcpOpenShiftClusters/<c>/applyDesires/<n>
//	.../hcpOpenShiftClusters/<c>/nodePools/<np>/applyDesires/<n>
//
// It is the same parser for all three desire kinds; we just expose typed
// constructors per kind so callers don't accidentally cross-wire keys.
func ApplyDesireKeyFromResourceID(id *azcorearm.ResourceID) (ApplyDesireKey, error) {
	parts, err := parseDesireParts(id)
	if err != nil {
		return ApplyDesireKey{}, err
	}
	return ApplyDesireKey(parts), nil
}

// DeleteDesireKeyFromResourceID is the DeleteDesire parallel of ApplyDesireKeyFromResourceID.
func DeleteDesireKeyFromResourceID(id *azcorearm.ResourceID) (DeleteDesireKey, error) {
	parts, err := parseDesireParts(id)
	if err != nil {
		return DeleteDesireKey{}, err
	}
	return DeleteDesireKey(parts), nil
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
	SubscriptionID    string
	ResourceGroupName string
	ClusterName       string
	NodePoolName      string
	Name              string
}

func parseDesireParts(id *azcorearm.ResourceID) (desireParts, error) {
	if id == nil {
		return desireParts{}, fmt.Errorf("resource ID is nil")
	}
	out := desireParts{
		SubscriptionID:    id.SubscriptionID,
		ResourceGroupName: id.ResourceGroupName,
		Name:              id.Name,
	}
	// Walk the parent chain to find the containing cluster (and optionally the
	// containing nodepool). The desire itself is the leaf; its parent is either
	// the cluster (cluster-scoped) or the nodepool (nodepool-scoped).
	parent := id.Parent
	if parent == nil {
		return desireParts{}, fmt.Errorf("desire %q has no parent in its resource ID", id.String())
	}
	if matchesType(parent.ResourceType, api.NodePoolResourceType) {
		out.NodePoolName = parent.Name
		if parent.Parent == nil {
			return desireParts{}, fmt.Errorf(
				"nodepool-scoped desire %q has no grandparent cluster", id.String(),
			)
		}
		out.ClusterName = parent.Parent.Name
		return out, nil
	}
	if matchesType(parent.ResourceType, api.ClusterResourceType) {
		out.ClusterName = parent.Name
		return out, nil
	}
	return desireParts{}, fmt.Errorf(
		"desire %q has unsupported parent resource type %s", id.String(), parent.ResourceType,
	)
}

func matchesType(got, want azcorearm.ResourceType) bool {
	return strings.EqualFold(got.Namespace, want.Namespace) &&
		strings.EqualFold(got.Type, want.Type)
}
