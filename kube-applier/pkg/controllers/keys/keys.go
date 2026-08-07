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

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ApplyDesireKey identifies a single ApplyDesire by the parts of its resource ID
// that map to the lister's CRUD helpers.
type ApplyDesireKey struct {
	SubscriptionID    string
	ResourceGroupName string
	ClusterName       string
	SubResourceType   string // lowercased leaf type name of the intermediate parent (empty for cluster-scoped)
	SubResourceName   string // name of the intermediate parent (empty for cluster-scoped)
	Name              string
}

// CRUD returns the right per-scope CRUD for this key's parent.
func (k ApplyDesireKey) CRUD(client kubeappliercosmosstorage.KubeApplierApplyDesireCRUD) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire], error) {
	return applyDesireCRUD(k.SubscriptionID, k.ResourceGroupName, k.ClusterName, k.SubResourceType, k.SubResourceName, client)
}

// GetResourceID returns the desire's full resource ID.
func (k ApplyDesireKey) GetResourceID() *azcorearm.ResourceID {
	s := desireResourceIDString(k.SubscriptionID, k.ResourceGroupName, k.ClusterName, k.SubResourceType, k.SubResourceName, kubeapplierapi.ApplyDesireResourceTypeName, k.Name)
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
	SubscriptionID    string
	ResourceGroupName string
	ClusterName       string
	SubResourceType   string
	SubResourceName   string
	Name              string
}

// CRUD returns the right per-scope CRUD for this key's parent.
func (k ReadDesireKey) CRUD(client kubeappliercosmosstorage.KubeApplierReadDesireCRUD) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire], error) {
	return readDesireCRUD(k.SubscriptionID, k.ResourceGroupName, k.ClusterName, k.SubResourceType, k.SubResourceName, client)
}

// GetResourceID returns the desire's full resource ID.
func (k ReadDesireKey) GetResourceID() *azcorearm.ResourceID {
	s := desireResourceIDString(k.SubscriptionID, k.ResourceGroupName, k.ClusterName, k.SubResourceType, k.SubResourceName, kubeapplierapi.ReadDesireResourceTypeName, k.Name)
	return metadataapi.Must(azcorearm.ParseResourceID(s))
}

// AddLoggerValues implements utils.LoggableKey so the generic worker loop seeds
// per-reconcile logger fields straight from the resource ID.
func (k ReadDesireKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(utils.LogValues{}.AddLogValuesForResourceID(k.GetResourceID())...)
}

// FromResourceID parses an ApplyDesireKey out of a *Desire's resource ID. The
// caller is expected to have a desire whose resource ID follows one of these
// patterns (or analogous patterns for other cluster sub-resource types):
//
//	.../hcpOpenShiftClusters/<c>/applyDesires/<n>
//	.../hcpOpenShiftClusters/<c>/nodePools/<np>/applyDesires/<n>
//	.../hcpOpenShiftClusters/<c>/systemAdminCredentialRequests/<cr>/applyDesires/<n>
//	.../hcpOpenShiftClusters/<c>/systemAdminCredentialRevocations/<rev>/applyDesires/<n>
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
	SubscriptionID    string
	ResourceGroupName string
	ClusterName       string
	SubResourceType   string
	SubResourceName   string
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
	// Walk the parent chain to find the containing cluster. The desire itself
	// is the leaf; its parent is either the cluster (cluster-scoped) or an
	// intermediate sub-resource type (e.g. nodePool, credentialRequest).
	parent := id.Parent
	if parent == nil {
		return desireParts{}, fmt.Errorf("desire %q has no parent in its resource ID", id.String())
	}

	if matchesType(parent.ResourceType, coreapi.ClusterResourceType) {
		out.ClusterName = parent.Name
		return out, nil
	}

	// The immediate parent is an intermediate sub-resource. Record it and
	// look one more level up for the cluster.
	out.SubResourceType = strings.ToLower(leafTypeName(parent.ResourceType))
	out.SubResourceName = parent.Name

	grandparent := parent.Parent
	if grandparent == nil {
		return desireParts{}, fmt.Errorf(
			"desire %q has intermediate parent %s but no grandparent cluster", id.String(), parent.ResourceType,
		)
	}
	if !matchesType(grandparent.ResourceType, coreapi.ClusterResourceType) {
		return desireParts{}, fmt.Errorf(
			"desire %q grandparent is %s, not a cluster", id.String(), grandparent.ResourceType,
		)
	}
	out.ClusterName = grandparent.Name
	return out, nil
}

func matchesType(got, want azcorearm.ResourceType) bool {
	return strings.EqualFold(got.Namespace, want.Namespace) &&
		strings.EqualFold(got.Type, want.Type)
}

// leafTypeName returns the trailing segment of an ARM ResourceType.
func leafTypeName(rt azcorearm.ResourceType) string {
	return rt.Types[len(rt.Types)-1]
}

// desireResourceIDString builds the lowercased resource ID string for a desire
// from its decomposed parts.
func desireResourceIDString(subscriptionID, resourceGroupName, clusterName, subResourceType, subResourceName, desireTypeName, desireName string) string {
	clusterPath := coreapi.ToClusterResourceIDString(subscriptionID, resourceGroupName, clusterName)
	if len(subResourceType) == 0 {
		return strings.ToLower(path.Join(clusterPath, desireTypeName, desireName))
	}
	return strings.ToLower(path.Join(clusterPath, subResourceType, subResourceName, desireTypeName, desireName))
}

func applyDesireCRUD(subscriptionID, resourceGroupName, clusterName, subResourceType, subResourceName string, client kubeappliercosmosstorage.KubeApplierApplyDesireCRUD) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire], error) {
	switch strings.ToLower(subResourceType) {
	case "":
		return client.ApplyDesiresForCluster(subscriptionID, resourceGroupName, clusterName)
	case strings.ToLower(coreapi.NodePoolResourceTypeName):
		return client.ApplyDesiresForNodePool(subscriptionID, resourceGroupName, clusterName, subResourceName)
	case strings.ToLower(coreapi.SystemAdminCredentialRequestResourceTypeName):
		return client.ApplyDesiresForSystemAdminCredentialRequest(subscriptionID, resourceGroupName, clusterName, subResourceName)
	case strings.ToLower(coreapi.SystemAdminCredentialRevocationResourceTypeName):
		return client.ApplyDesiresForSystemAdminCredentialRevocation(subscriptionID, resourceGroupName, clusterName, subResourceName)
	default:
		return nil, fmt.Errorf("unsupported sub-resource type %q for apply desire CRUD dispatch", subResourceType)
	}
}

func readDesireCRUD(subscriptionID, resourceGroupName, clusterName, subResourceType, subResourceName string, client kubeappliercosmosstorage.KubeApplierReadDesireCRUD) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire], error) {
	switch strings.ToLower(subResourceType) {
	case "":
		return client.ReadDesiresForCluster(subscriptionID, resourceGroupName, clusterName)
	case strings.ToLower(coreapi.NodePoolResourceTypeName):
		return client.ReadDesiresForNodePool(subscriptionID, resourceGroupName, clusterName, subResourceName)
	case strings.ToLower(coreapi.SystemAdminCredentialRequestResourceTypeName):
		return client.ReadDesiresForSystemAdminCredentialRequest(subscriptionID, resourceGroupName, clusterName, subResourceName)
	case strings.ToLower(coreapi.SystemAdminCredentialRevocationResourceTypeName):
		return client.ReadDesiresForSystemAdminCredentialRevocation(subscriptionID, resourceGroupName, clusterName, subResourceName)
	default:
		return nil, fmt.Errorf("unsupported sub-resource type %q for read desire CRUD dispatch", subResourceType)
	}
}
