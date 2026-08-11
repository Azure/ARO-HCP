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
	"strings"

	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/systemadmincredential"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// desireParent identifies the resource a *Desire is nested under. Exactly one of
// credentialRequestName / revocationName is set to nest the desire under a
// SystemAdminCredentialRequest or SystemAdminCredentialRevocation respectively;
// when both are empty the desire is cluster-scoped (legacy). It centralizes the
// resource-ID and lister-key construction so the ensure* helpers stay
// scope-agnostic.
type DesireParent struct {
	credentialRequestName string
	revocationName        string
}

// CredentialRequestDesireParent returns a DesireParent that nests desires under
// the named SystemAdminCredentialRequest.
func CredentialRequestDesireParent(credentialRequestName string) DesireParent {
	return DesireParent{credentialRequestName: credentialRequestName}
}

// RevocationDesireParent returns a DesireParent that nests desires under the
// named SystemAdminCredentialRevocation.
func RevocationDesireParent(revocationName string) DesireParent {
	return DesireParent{revocationName: revocationName}
}

// applyDesireCRUD returns the ApplyDesire CRUD for this scope on the given client.
func (p DesireParent) applyDesireCRUD(client kubeappliercosmosstorage.KubeApplierDBClient, subscriptionID, resourceGroupName, clusterName string) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire], error) {
	switch {
	case p.credentialRequestName != "":
		return client.ApplyDesiresForSystemAdminCredentialRequest(subscriptionID, resourceGroupName, clusterName, p.credentialRequestName)
	case p.revocationName != "":
		return client.ApplyDesiresForSystemAdminCredentialRevocation(subscriptionID, resourceGroupName, clusterName, p.revocationName)
	default:
		return client.ApplyDesiresForCluster(subscriptionID, resourceGroupName, clusterName)
	}
}

// readDesireCRUD returns the ReadDesire CRUD for this scope on the given client.
func (p DesireParent) readDesireCRUD(client kubeappliercosmosstorage.KubeApplierDBClient, subscriptionID, resourceGroupName, clusterName string) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire], error) {
	switch {
	case p.credentialRequestName != "":
		return client.ReadDesiresForSystemAdminCredentialRequest(subscriptionID, resourceGroupName, clusterName, p.credentialRequestName)
	case p.revocationName != "":
		return client.ReadDesiresForSystemAdminCredentialRevocation(subscriptionID, resourceGroupName, clusterName, p.revocationName)
	default:
		return client.ReadDesiresForCluster(subscriptionID, resourceGroupName, clusterName)
	}
}

// applyDesireResourceIDString builds the resource-ID string for an ApplyDesire in this scope.
func (p DesireParent) applyDesireResourceIDString(subscriptionID, resourceGroupName, clusterName, desireName string) string {
	switch {
	case p.credentialRequestName != "":
		return kubeapplierapi.ToSystemAdminCredentialRequestScopedApplyDesireResourceIDString(subscriptionID, resourceGroupName, clusterName, p.credentialRequestName, desireName)
	case p.revocationName != "":
		return kubeapplierapi.ToSystemAdminCredentialRevocationScopedApplyDesireResourceIDString(subscriptionID, resourceGroupName, clusterName, p.revocationName, desireName)
	default:
		return kubeapplierapi.ToClusterScopedApplyDesireResourceIDString(subscriptionID, resourceGroupName, clusterName, desireName)
	}
}

// readDesireResourceIDString builds the resource-ID string for a ReadDesire in this scope.
func (p DesireParent) readDesireResourceIDString(subscriptionID, resourceGroupName, clusterName, desireName string) string {
	switch {
	case p.credentialRequestName != "":
		return kubeapplierapi.ToSystemAdminCredentialRequestScopedReadDesireResourceIDString(subscriptionID, resourceGroupName, clusterName, p.credentialRequestName, desireName)
	case p.revocationName != "":
		return kubeapplierapi.ToSystemAdminCredentialRevocationScopedReadDesireResourceIDString(subscriptionID, resourceGroupName, clusterName, p.revocationName, desireName)
	default:
		return kubeapplierapi.ToClusterScopedReadDesireResourceIDString(subscriptionID, resourceGroupName, clusterName, desireName)
	}
}

// getApplyDesire looks the existing ApplyDesire up from the lister using the scope's key.
func (p DesireParent) getApplyDesire(ctx context.Context, lister dblisters.ApplyDesireLister, subscriptionID, resourceGroupName, clusterName, desireName string) (*kubeapplierapi.ApplyDesire, error) {
	switch {
	case p.credentialRequestName != "":
		return lister.GetForSystemAdminCredentialRequest(ctx, subscriptionID, resourceGroupName, clusterName, p.credentialRequestName, strings.ToLower(desireName))
	case p.revocationName != "":
		return lister.GetForSystemAdminCredentialRevocation(ctx, subscriptionID, resourceGroupName, clusterName, p.revocationName, strings.ToLower(desireName))
	default:
		return lister.GetForCluster(ctx, subscriptionID, resourceGroupName, clusterName, strings.ToLower(desireName))
	}
}

// getReadDesire looks the existing ReadDesire up from the lister using the scope's key.
func (p DesireParent) getReadDesire(ctx context.Context, lister dblisters.ReadDesireLister, subscriptionID, resourceGroupName, clusterName, desireName string) (*kubeapplierapi.ReadDesire, error) {
	switch {
	case p.credentialRequestName != "":
		return lister.GetForSystemAdminCredentialRequest(ctx, subscriptionID, resourceGroupName, clusterName, p.credentialRequestName, strings.ToLower(desireName))
	case p.revocationName != "":
		return lister.GetForSystemAdminCredentialRevocation(ctx, subscriptionID, resourceGroupName, clusterName, p.revocationName, strings.ToLower(desireName))
	default:
		return lister.GetForCluster(ctx, subscriptionID, resourceGroupName, clusterName, strings.ToLower(desireName))
	}
}

// ensureApplyDesire creates the named ApplyDesire (a server-side apply of obj)
// nested under parent unless a matching desire already exists. It consults the
// ApplyDesire lister first so an already-correct desire is never rewritten, and
// logs whenever it writes a new desire. It is shared by the desires-creator and
// revocation-desires controllers.
func EnsureApplyDesire(
	ctx context.Context,
	crud cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire],
	applyDesireLister dblisters.ApplyDesireLister,
	parent DesireParent,
	subscriptionID, resourceGroupName, hcpClusterName, desireName string,
	managementCluster *azcorearm.ResourceID,
	target kubeapplierapi.ResourceReference,
	obj systemadmincredential.KubeObject,
) error {
	logger := utils.LoggerFromContext(ctx)

	resourceIDStr := parent.applyDesireResourceIDString(subscriptionID, resourceGroupName, hcpClusterName, desireName)
	resourceID, err := azcorearm.ParseResourceID(resourceIDStr)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to parse ApplyDesire resource ID %q: %w", resourceIDStr, err))
	}

	rawJSON, err := json.Marshal(obj)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to marshal kube object: %w", err))
	}

	desire := &kubeapplierapi.ApplyDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(managementCluster.String()),
		},
		Spec: kubeapplierapi.ApplyDesireSpec{
			ManagementCluster: managementCluster,
			Type:              kubeapplierapi.ApplyDesireTypeServerSideApply,
			TargetItem:        target,
			ServerSideApply: &kubeapplierapi.ServerSideApplyConfig{
				KubeContent: &runtime.RawExtension{Raw: rawJSON},
			},
		},
	}

	existing, err := parent.getApplyDesire(ctx, applyDesireLister, subscriptionID, resourceGroupName, hcpClusterName, desireName)
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
	case !applyDesireSpecEqual(existing.Spec, desire.Spec):
		replacement := existing.DeepCopy()
		replacement.Spec = desire.Spec
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

// ensureReadDesire creates the named ReadDesire nested under parent unless a
// matching desire already exists. It consults the ReadDesire lister first so an
// already-correct desire is never rewritten, and logs whenever it writes a new
// desire. It is shared by the desires-creator and revocation-desires controllers.
func EnsureReadDesire(
	ctx context.Context,
	crud cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire],
	readDesireLister dblisters.ReadDesireLister,
	parent DesireParent,
	subscriptionID, resourceGroupName, hcpClusterName, desireName string,
	managementCluster *azcorearm.ResourceID,
	target kubeapplierapi.ResourceReference,
) error {
	logger := utils.LoggerFromContext(ctx)

	resourceIDStr := parent.readDesireResourceIDString(subscriptionID, resourceGroupName, hcpClusterName, desireName)
	resourceID, err := azcorearm.ParseResourceID(resourceIDStr)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to parse ReadDesire resource ID %q: %w", resourceIDStr, err))
	}

	desire := &kubeapplierapi.ReadDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(managementCluster.String()),
		},
		Spec: kubeapplierapi.ReadDesireSpec{
			ManagementCluster: managementCluster,
			TargetItem:        target,
		},
	}

	existing, err := parent.getReadDesire(ctx, readDesireLister, subscriptionID, resourceGroupName, hcpClusterName, desireName)
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
	case !readDesireSpecEqual(existing.Spec, desire.Spec):
		replacement := existing.DeepCopy()
		replacement.Spec = desire.Spec
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
