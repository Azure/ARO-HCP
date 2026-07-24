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

	"github.com/go-logr/logr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ApplyDesireKey identifies a single ApplyDesire by the parts of its resource ID
// that map to the lister's GetFor* helpers.
//
// A desire is nested under exactly one parent scope. At most one of
// NodePoolName / CredentialRequestName / RevocationName is non-empty; when all
// three are empty the desire is cluster-scoped.
type ApplyDesireKey struct {
	SubscriptionID        string
	ResourceGroupName     string
	ClusterName           string
	NodePoolName          string // set for node-pool-scoped desires
	CredentialRequestName string // set for credential-request-scoped desires
	RevocationName        string // set for revocation-scoped desires
	Name                  string
}

// IsNodePoolScoped reports whether this key targets a node-pool-scoped desire.
func (k ApplyDesireKey) IsNodePoolScoped() bool { return len(k.NodePoolName) > 0 }

// IsCredentialRequestScoped reports whether this key targets a
// credential-request-scoped desire.
func (k ApplyDesireKey) IsCredentialRequestScoped() bool { return len(k.CredentialRequestName) > 0 }

// IsRevocationScoped reports whether this key targets a revocation-scoped desire.
func (k ApplyDesireKey) IsRevocationScoped() bool { return len(k.RevocationName) > 0 }

// CRUD returns the right per-scope CRUD for this key's parent.
func (k ApplyDesireKey) CRUD(client database.KubeApplierApplyDesireCRUD) (database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire], error) {
	switch {
	case k.IsNodePoolScoped():
		return client.ApplyDesiresForNodePool(k.SubscriptionID, k.ResourceGroupName, k.ClusterName, k.NodePoolName)
	case k.IsCredentialRequestScoped():
		return client.ApplyDesiresForCredentialRequest(k.SubscriptionID, k.ResourceGroupName, k.ClusterName, k.CredentialRequestName)
	case k.IsRevocationScoped():
		return client.ApplyDesiresForRevocation(k.SubscriptionID, k.ResourceGroupName, k.ClusterName, k.RevocationName)
	default:
		return client.ApplyDesiresForCluster(k.SubscriptionID, k.ResourceGroupName, k.ClusterName)
	}
}

// GetResourceID returns the desire's full resource ID, using the builder that
// matches the key's parent scope.
func (k ApplyDesireKey) GetResourceID() *azcorearm.ResourceID {
	var s string
	switch {
	case k.IsNodePoolScoped():
		s = kubeapplier.ToNodePoolScopedApplyDesireResourceIDString(
			k.SubscriptionID, k.ResourceGroupName, k.ClusterName, k.NodePoolName, k.Name)
	case k.IsCredentialRequestScoped():
		s = kubeapplier.ToCredentialRequestScopedApplyDesireResourceIDString(
			k.SubscriptionID, k.ResourceGroupName, k.ClusterName, k.CredentialRequestName, k.Name)
	case k.IsRevocationScoped():
		s = kubeapplier.ToRevocationScopedApplyDesireResourceIDString(
			k.SubscriptionID, k.ResourceGroupName, k.ClusterName, k.RevocationName, k.Name)
	default:
		s = kubeapplier.ToClusterScopedApplyDesireResourceIDString(
			k.SubscriptionID, k.ResourceGroupName, k.ClusterName, k.Name)
	}
	return api.Must(azcorearm.ParseResourceID(s))
}

// AddLoggerValues implements utils.LoggableKey so the generic worker loop seeds
// per-reconcile logger fields straight from the resource ID — same key set the
// backend uses (subscription_id, resource_group, resource_name, resource_id,
// hcp_cluster_name).
func (k ApplyDesireKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(utils.LogValues{}.AddLogValuesForResourceID(k.GetResourceID())...)
}

// ReadDesireKey identifies a single ReadDesire.
//
// It mirrors ApplyDesireKey: at most one of NodePoolName /
// CredentialRequestName / RevocationName is non-empty, and an all-empty set
// means cluster-scoped.
type ReadDesireKey struct {
	SubscriptionID        string
	ResourceGroupName     string
	ClusterName           string
	NodePoolName          string // set for node-pool-scoped desires
	CredentialRequestName string // set for credential-request-scoped desires
	RevocationName        string // set for revocation-scoped desires
	Name                  string
}

// IsNodePoolScoped reports whether this key targets a node-pool-scoped desire.
func (k ReadDesireKey) IsNodePoolScoped() bool { return len(k.NodePoolName) > 0 }

// IsCredentialRequestScoped reports whether this key targets a
// credential-request-scoped desire.
func (k ReadDesireKey) IsCredentialRequestScoped() bool { return len(k.CredentialRequestName) > 0 }

// IsRevocationScoped reports whether this key targets a revocation-scoped desire.
func (k ReadDesireKey) IsRevocationScoped() bool { return len(k.RevocationName) > 0 }

// CRUD returns the right per-scope CRUD for this key's parent.
func (k ReadDesireKey) CRUD(client database.KubeApplierReadDesireCRUD) (database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire], error) {
	switch {
	case k.IsNodePoolScoped():
		return client.ReadDesiresForNodePool(k.SubscriptionID, k.ResourceGroupName, k.ClusterName, k.NodePoolName)
	case k.IsCredentialRequestScoped():
		return client.ReadDesiresForCredentialRequest(k.SubscriptionID, k.ResourceGroupName, k.ClusterName, k.CredentialRequestName)
	case k.IsRevocationScoped():
		return client.ReadDesiresForRevocation(k.SubscriptionID, k.ResourceGroupName, k.ClusterName, k.RevocationName)
	default:
		return client.ReadDesiresForCluster(k.SubscriptionID, k.ResourceGroupName, k.ClusterName)
	}
}

// GetResourceID returns the desire's full resource ID, using the builder that
// matches the key's parent scope.
func (k ReadDesireKey) GetResourceID() *azcorearm.ResourceID {
	var s string
	switch {
	case k.IsNodePoolScoped():
		s = kubeapplier.ToNodePoolScopedReadDesireResourceIDString(
			k.SubscriptionID, k.ResourceGroupName, k.ClusterName, k.NodePoolName, k.Name)
	case k.IsCredentialRequestScoped():
		s = kubeapplier.ToCredentialRequestScopedReadDesireResourceIDString(
			k.SubscriptionID, k.ResourceGroupName, k.ClusterName, k.CredentialRequestName, k.Name)
	case k.IsRevocationScoped():
		s = kubeapplier.ToRevocationScopedReadDesireResourceIDString(
			k.SubscriptionID, k.ResourceGroupName, k.ClusterName, k.RevocationName, k.Name)
	default:
		s = kubeapplier.ToClusterScopedReadDesireResourceIDString(
			k.SubscriptionID, k.ResourceGroupName, k.ClusterName, k.Name)
	}
	return api.Must(azcorearm.ParseResourceID(s))
}

// AddLoggerValues implements utils.LoggableKey so the generic worker loop seeds
// per-reconcile logger fields straight from the resource ID.
func (k ReadDesireKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(utils.LogValues{}.AddLogValuesForResourceID(k.GetResourceID())...)
}

// ApplyDesireKeyFromResourceID parses an ApplyDesireKey out of a *Desire's
// resource ID. The caller is expected to have a desire whose resource ID is one
// of:
//
//	.../hcpOpenShiftClusters/<c>/applyDesires/<n>
//	.../hcpOpenShiftClusters/<c>/nodePools/<np>/applyDesires/<n>
//	.../hcpOpenShiftClusters/<c>/systemAdminCredentialRequests/<cred>/applyDesires/<n>
//	.../hcpOpenShiftClusters/<c>/systemAdminCredentialRevocations/<rev>/applyDesires/<n>
//
// It is the same parser for all desire kinds; we just expose typed constructors
// per kind so callers don't accidentally cross-wire keys.
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
// reflection. Its fields must stay in lock-step with ApplyDesireKey /
// ReadDesireKey so the struct conversions in the constructors above remain valid.
type desireParts struct {
	SubscriptionID        string
	ResourceGroupName     string
	ClusterName           string
	NodePoolName          string
	CredentialRequestName string
	RevocationName        string
	Name                  string
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
	// containing node pool / credential request / revocation). The desire itself
	// is the leaf; its parent is either the cluster (cluster-scoped) or an
	// intermediate resource that is itself a direct child of the cluster.
	parent := id.Parent
	if parent == nil {
		return desireParts{}, fmt.Errorf("desire %q has no parent in its resource ID", id.String())
	}

	// Cluster-scoped: the desire hangs directly off the cluster.
	if matchesType(parent.ResourceType, api.ClusterResourceType) {
		out.ClusterName = parent.Name
		return out, nil
	}

	// Every other supported scope nests the desire one level deeper, under an
	// intermediate resource whose own parent is the cluster, so the cluster is
	// the grandparent.
	switch {
	case matchesType(parent.ResourceType, api.NodePoolResourceType):
		out.NodePoolName = parent.Name
	case matchesType(parent.ResourceType, api.SystemAdminCredentialRequestResourceType):
		out.CredentialRequestName = parent.Name
	case matchesType(parent.ResourceType, api.SystemAdminCredentialRevocationResourceType):
		out.RevocationName = parent.Name
	default:
		return desireParts{}, fmt.Errorf(
			"desire %q has unsupported parent resource type %s", id.String(), parent.ResourceType,
		)
	}
	if parent.Parent == nil {
		return desireParts{}, fmt.Errorf(
			"nested desire %q has no grandparent cluster", id.String(),
		)
	}
	out.ClusterName = parent.Parent.Name
	return out, nil
}

func matchesType(got, want azcorearm.ResourceType) bool {
	return strings.EqualFold(got.Namespace, want.Namespace) &&
		strings.EqualFold(got.Type, want.Type)
}
