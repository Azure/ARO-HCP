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

package kubeapplierhelpers

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// DesireParent identifies the resource a *Desire is nested under. It is built
// via one of the scope constructors below, each of which captures how to derive
// the parent resource's ARM resource ID from the enclosing cluster's
// coordinates. Resource-ID, CRUD, and lister-key construction are then derived
// generically from that parent resource ID (via the kube-applier DesireScope
// abstraction), so the ensure*/delete helpers stay scope-agnostic and any new
// parent level (node pool today, management-cluster-scoped in the future) is a
// one-line constructor rather than a new case in a fan of switch statements.
//
// The zero value (DesireParent{}) has no scope: every accessor on it returns an
// error rather than silently guessing a scope, because a desire with no declared
// parent is a programming error, not a cluster-scoped desire.
type DesireParent struct {
	// toParentResourceIDString builds the ARM resource ID string of the parent
	// resource the desires nest under, given the enclosing cluster coordinates.
	toParentResourceIDString func(subscriptionID, resourceGroupName, clusterName string) string
}

// ClusterDesireParent returns a DesireParent that nests desires directly under
// the cluster.
func ClusterDesireParent() DesireParent {
	return DesireParent{toParentResourceIDString: func(subscriptionID, resourceGroupName, clusterName string) string {
		return coreapi.ToClusterResourceIDString(subscriptionID, resourceGroupName, clusterName)
	}}
}

// NodePoolDesireParent returns a DesireParent that nests desires under the named
// node pool.
func NodePoolDesireParent(nodePoolName string) DesireParent {
	return DesireParent{toParentResourceIDString: func(subscriptionID, resourceGroupName, clusterName string) string {
		return coreapi.ToNodePoolResourceIDString(subscriptionID, resourceGroupName, clusterName, nodePoolName)
	}}
}

// CredentialRequestDesireParent returns a DesireParent that nests desires under
// the named SystemAdminCredentialRequest.
func CredentialRequestDesireParent(credentialRequestName string) DesireParent {
	return DesireParent{toParentResourceIDString: func(subscriptionID, resourceGroupName, clusterName string) string {
		return coreapi.ToSystemAdminCredentialRequestResourceIDString(subscriptionID, resourceGroupName, clusterName, credentialRequestName)
	}}
}

// RevocationDesireParent returns a DesireParent that nests desires under the
// named SystemAdminCredentialRevocation.
func RevocationDesireParent(revocationName string) DesireParent {
	return DesireParent{toParentResourceIDString: func(subscriptionID, resourceGroupName, clusterName string) string {
		return coreapi.ToSystemAdminCredentialRevocationResourceIDString(subscriptionID, resourceGroupName, clusterName, revocationName)
	}}
}

// parentResourceIDString builds the parent resource's ARM resource ID string for
// the given cluster. A zero-value DesireParent (nil builder) has no scope and
// returns an error: defaulting it to a cluster (or any other scope) would be
// nonsensical, so callers must construct the parent explicitly via one of the
// DesireParent constructors.
func (p DesireParent) parentResourceIDString(subscriptionID, resourceGroupName, clusterName string) (string, error) {
	if p.toParentResourceIDString == nil {
		return "", utils.TrackError(fmt.Errorf("uninitialized DesireParent: build it with one of the DesireParent constructors (ClusterDesireParent, NodePoolDesireParent, CredentialRequestDesireParent, RevocationDesireParent)"))
	}
	return p.toParentResourceIDString(subscriptionID, resourceGroupName, clusterName), nil
}

// desireScope resolves the kube-applier DesireScope for this parent under the
// given cluster. It is the single point that turns the parent resource ID into
// a validated scope; the enumerated per-level CRUD accessors are no longer
// needed.
func (p DesireParent) desireScope(subscriptionID, resourceGroupName, clusterName string) (kubeappliercosmosstorage.DesireScope, error) {
	parentResourceIDStr, err := p.parentResourceIDString(subscriptionID, resourceGroupName, clusterName)
	if err != nil {
		return kubeappliercosmosstorage.DesireScope{}, err
	}
	parentID, err := azcorearm.ParseResourceID(parentResourceIDStr)
	if err != nil {
		return kubeappliercosmosstorage.DesireScope{}, utils.TrackError(err)
	}
	return kubeappliercosmosstorage.ParseDesireScope(parentID)
}

// applyDesireCRUD returns the ApplyDesire CRUD for this scope on the given client.
func (p DesireParent) applyDesireCRUD(client kubeappliercosmosstorage.KubeApplierDBClient, subscriptionID, resourceGroupName, clusterName string) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire], error) {
	scope, err := p.desireScope(subscriptionID, resourceGroupName, clusterName)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	return client.ApplyDesiresFor(scope)
}

// readDesireCRUD returns the ReadDesire CRUD for this scope on the given client.
func (p DesireParent) readDesireCRUD(client kubeappliercosmosstorage.KubeApplierDBClient, subscriptionID, resourceGroupName, clusterName string) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire], error) {
	scope, err := p.desireScope(subscriptionID, resourceGroupName, clusterName)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	return client.ReadDesiresFor(scope)
}

// EnsureApplyDesire creates desire, or replaces the stored one when its spec or
// tags have drifted. It consults the ApplyDesire lister first — keyed by the desire's own
// resource ID — so an already-correct desire is never rewritten, and logs
// whenever it writes. The caller constructs the full desire in its own package
// (each controller builds the ApplyDesire it wants), so this function stays a
// pure, scope-agnostic create-or-update. It is shared by the desires-creator,
// revocation-desires, and backup-schedule controllers.
func EnsureApplyDesire(
	ctx context.Context,
	crud cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire],
	applyDesireLister dblisters.ApplyDesireLister,
	desire *kubeapplierapi.ApplyDesire,
) error {
	if _, ok := desire.Tags[kubeapplierapi.TagControllerName]; !ok {
		return utils.TrackError(fmt.Errorf("desire Tags must contain the %q tag key", kubeapplierapi.TagControllerName))
	}

	logger := utils.LoggerFromContext(ctx)
	desireName := desire.ResourceID.Name
	target := desire.Spec.TargetItem

	existing, err := applyDesireLister.GetByResourceID(ctx, desire.ResourceID.String())
	switch {
	case err != nil && !cosmosstorageutils.IsNotFoundError(err):
		return utils.TrackError(fmt.Errorf("get ApplyDesire %s from lister: %w", desireName, err))
	case existing == nil:
		_, err := crud.Create(ctx, desire, nil)
		switch {
		case cosmosstorageutils.IsConflictError(err):
			return nil
		case err != nil:
			return utils.TrackError(fmt.Errorf("create ApplyDesire %s: %w", desireName, err))
		}
		logger.Info("created ApplyDesire", "desire", desireName, "targetResource", target.Resource, "targetName", target.Name)
		return nil
	case !applyDesireSpecEqual(existing.Spec, desire.Spec) || !reflect.DeepEqual(existing.Tags, desire.Tags):
		replacement := existing.DeepCopy()
		replacement.Spec = desire.Spec
		replacement.Tags = desire.Tags
		replacement.Status = kubeapplierapi.ApplyDesireStatus{}
		_, err := crud.Replace(ctx, replacement, nil)
		switch {
		case cosmosstorageutils.IsPreconditionFailedError(err):
			return nil
		case err != nil:
			return utils.TrackError(fmt.Errorf("replace ApplyDesire %s: %w", desireName, err))
		}
		logger.Info("replaced ApplyDesire", "desire", desireName, "targetResource", target.Resource, "targetName", target.Name)
		return nil
	default:
		return nil
	}
}

// EnsureReadDesire creates desire, or replaces the stored one when its spec or
// tags have drifted. Like EnsureApplyDesire it consults the lister (keyed by the desire's
// resource ID) and leaves construction to the caller, which builds the ReadDesire
// in its own package. It is shared by the desires-creator, revocation-desires,
// backup-schedule, and read-desire creator controllers.
func EnsureReadDesire(
	ctx context.Context,
	crud cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire],
	readDesireLister dblisters.ReadDesireLister,
	desire *kubeapplierapi.ReadDesire,
) error {
	if _, ok := desire.Tags[kubeapplierapi.TagControllerName]; !ok {
		return utils.TrackError(fmt.Errorf("desire Tags must contain the %q tag key", kubeapplierapi.TagControllerName))
	}

	logger := utils.LoggerFromContext(ctx)
	desireName := desire.ResourceID.Name
	target := desire.Spec.TargetItem

	existing, err := readDesireLister.GetByResourceID(ctx, desire.ResourceID.String())
	switch {
	case err != nil && !cosmosstorageutils.IsNotFoundError(err):
		return utils.TrackError(fmt.Errorf("get ReadDesire %s from lister: %w", desireName, err))
	case existing == nil:
		_, err := crud.Create(ctx, desire, nil)
		switch {
		case cosmosstorageutils.IsConflictError(err):
			return nil
		case err != nil:
			return utils.TrackError(fmt.Errorf("create ReadDesire %s: %w", desireName, err))
		}
		logger.Info("created ReadDesire", "desire", desireName, "targetResource", target.Resource, "targetName", target.Name)
		return nil
	case !readDesireSpecEqual(existing.Spec, desire.Spec) || !reflect.DeepEqual(existing.Tags, desire.Tags):
		replacement := existing.DeepCopy()
		replacement.Spec = desire.Spec
		replacement.Tags = desire.Tags
		replacement.Status = kubeapplierapi.ReadDesireStatus{}
		_, err := crud.Replace(ctx, replacement, nil)
		switch {
		case cosmosstorageutils.IsPreconditionFailedError(err):
			return nil
		case err != nil:
			return utils.TrackError(fmt.Errorf("replace ReadDesire %s: %w", desireName, err))
		}
		logger.Info("replaced ReadDesire", "desire", desireName, "targetResource", target.Resource, "targetName", target.Name)
		return nil
	default:
		return nil
	}
}

// applyDesireSpecEqual reports whether an existing ApplyDesire spec already
// matches the desired spec (same management cluster, target, and rendered
// content), so callers can avoid a redundant Cosmos write.
func applyDesireSpecEqual(existing, desired kubeapplierapi.ApplyDesireSpec) bool {
	if !controllerutil.ResourceIDsEqual(existing.ManagementCluster, desired.ManagementCluster) {
		return false
	}
	if existing.TargetItem != desired.TargetItem {
		return false
	}
	if existing.Type != desired.Type {
		return false
	}
	var existingRaw, desiredRaw []byte
	if existing.ServerSideApply != nil && existing.ServerSideApply.KubeContent != nil {
		existingRaw = existing.ServerSideApply.KubeContent.Raw
	}
	if desired.ServerSideApply != nil && desired.ServerSideApply.KubeContent != nil {
		desiredRaw = desired.ServerSideApply.KubeContent.Raw
	}
	return jsonBytesEqual(existingRaw, desiredRaw)
}

// readDesireSpecEqual reports whether an existing ReadDesire spec already matches
// the desired spec (same management cluster and target), so callers can avoid a
// redundant Cosmos write.
func jsonBytesEqual(a, b []byte) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	var aVal, bVal interface{}
	if err := json.Unmarshal(a, &aVal); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &bVal); err != nil {
		return false
	}
	return reflect.DeepEqual(aVal, bVal)
}

func readDesireSpecEqual(existing, desired kubeapplierapi.ReadDesireSpec) bool {
	return controllerutil.ResourceIDsEqual(existing.ManagementCluster, desired.ManagementCluster) &&
		existing.TargetItem == desired.TargetItem
}
