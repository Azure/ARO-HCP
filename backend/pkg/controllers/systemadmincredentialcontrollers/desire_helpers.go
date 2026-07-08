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

package systemadmincredentialcontrollers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/systemadmincredential"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ensureApplyDesire creates the named cluster-scoped ApplyDesire (a server-side
// apply of obj) unless a matching desire already exists. It consults the
// ApplyDesire lister first so an already-correct desire is never rewritten, and
// logs whenever it writes a new desire. It is shared by the desires-creator and
// revocation-desires controllers.
func ensureApplyDesire(
	ctx context.Context,
	crud database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire],
	applyDesireLister dblisters.ApplyDesireLister,
	subscriptionID, resourceGroupName, hcpClusterName, desireName string,
	managementCluster *azcorearm.ResourceID,
	target kubeapplier.ResourceReference,
	obj systemadmincredential.KubeObject,
) error {
	logger := utils.LoggerFromContext(ctx)

	resourceIDStr := kubeapplier.ToClusterScopedApplyDesireResourceIDString(subscriptionID, resourceGroupName, hcpClusterName, desireName)
	resourceID, err := azcorearm.ParseResourceID(resourceIDStr)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to parse ApplyDesire resource ID %q: %w", resourceIDStr, err))
	}

	rawJSON, err := json.Marshal(obj)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to marshal kube object: %w", err))
	}

	desire := &kubeapplier.ApplyDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(managementCluster.String()),
		},
		Spec: kubeapplier.ApplyDesireSpec{
			ManagementCluster: managementCluster,
			Type:              kubeapplier.ApplyDesireTypeServerSideApply,
			TargetItem:        target,
			ServerSideApply: &kubeapplier.ServerSideApplyConfig{
				KubeContent: &runtime.RawExtension{Raw: rawJSON},
			},
		},
	}

	// Consult the lister first: if an ApplyDesire already exists with the desired
	// content there is nothing to do, and we skip the Cosmos write.
	existing, err := applyDesireLister.GetForCluster(ctx, subscriptionID, resourceGroupName, hcpClusterName, strings.ToLower(desireName))
	if err != nil && !database.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("get ApplyDesire %s from lister: %w", desireName, err))
	}
	if existing != nil && applyDesireSpecEqual(existing.Spec, desire.Spec) {
		return nil
	}

	if _, err := crud.Create(ctx, desire, nil); err != nil && !database.IsConflictError(err) {
		return utils.TrackError(fmt.Errorf("create ApplyDesire %s: %w", desireName, err))
	}
	logger.Info("created ApplyDesire", "desire", desireName, "targetResource", target.Resource, "targetName", target.Name)
	return nil
}

// ensureReadDesire creates the named cluster-scoped ReadDesire unless a matching
// desire already exists. It consults the ReadDesire lister first so an
// already-correct desire is never rewritten, and logs whenever it writes a new
// desire. It is shared by the desires-creator and revocation-desires controllers.
func ensureReadDesire(
	ctx context.Context,
	crud database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire],
	readDesireLister dblisters.ReadDesireLister,
	subscriptionID, resourceGroupName, hcpClusterName, desireName string,
	managementCluster *azcorearm.ResourceID,
	target kubeapplier.ResourceReference,
) error {
	logger := utils.LoggerFromContext(ctx)

	resourceIDStr := kubeapplier.ToClusterScopedReadDesireResourceIDString(subscriptionID, resourceGroupName, hcpClusterName, desireName)
	resourceID, err := azcorearm.ParseResourceID(resourceIDStr)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to parse ReadDesire resource ID %q: %w", resourceIDStr, err))
	}

	desire := &kubeapplier.ReadDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(managementCluster.String()),
		},
		Spec: kubeapplier.ReadDesireSpec{
			ManagementCluster: managementCluster,
			TargetItem:        target,
		},
	}

	// Consult the lister first: if a ReadDesire already exists with the desired
	// content there is nothing to do, and we skip the Cosmos write.
	existing, err := readDesireLister.GetForCluster(ctx, subscriptionID, resourceGroupName, hcpClusterName, strings.ToLower(desireName))
	if err != nil && !database.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("get ReadDesire %s from lister: %w", desireName, err))
	}
	if existing != nil && readDesireSpecEqual(existing.Spec, desire.Spec) {
		return nil
	}

	if _, err := crud.Create(ctx, desire, nil); err != nil && !database.IsConflictError(err) {
		return utils.TrackError(fmt.Errorf("create ReadDesire %s: %w", desireName, err))
	}
	logger.Info("created ReadDesire", "desire", desireName, "targetResource", target.Resource, "targetName", target.Name)
	return nil
}

// applyDesireSpecEqual reports whether an existing ApplyDesire spec already
// matches the desired spec (same management cluster, target, and rendered
// content), so callers can avoid a redundant Cosmos write.
func applyDesireSpecEqual(existing, desired kubeapplier.ApplyDesireSpec) bool {
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
	return bytes.Equal(existingRaw, desiredRaw)
}

// readDesireSpecEqual reports whether an existing ReadDesire spec already matches
// the desired spec (same management cluster and target), so callers can avoid a
// redundant Cosmos write.
func readDesireSpecEqual(existing, desired kubeapplier.ReadDesireSpec) bool {
	return controllerutil.ResourceIDsEqual(existing.ManagementCluster, desired.ManagementCluster) &&
		existing.TargetItem == desired.TargetItem
}

// targetRefForKubeObject builds a kube-applier ResourceReference for a typed
// Kubernetes object by deriving the resource name from its kind.
func targetRefForKubeObject(obj systemadmincredential.KubeObject) kubeapplier.ResourceReference {
	gvk := obj.GetObjectKind().GroupVersionKind()
	resource := strings.ToLower(gvk.Kind) + "s"
	return kubeapplier.ResourceReference{
		Group:     gvk.Group,
		Version:   gvk.Version,
		Resource:  resource,
		Namespace: obj.GetNamespace(),
		Name:      obj.GetName(),
	}
}
