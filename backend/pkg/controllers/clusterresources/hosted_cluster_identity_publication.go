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

package clusterresources

import (
	"context"
	"fmt"
	"maps"
	"strconv"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// Overlay only the three leaves. CS still supplies the rest of the manifest,
// including any data plane fields introduced by a newer HyperShift.
func overlayDataPlaneClientIDs(obj *unstructured.Unstructured, desired *hsv1beta1.HostedCluster) error {
	if desired == nil || desired.Spec.Platform.Azure == nil {
		return nil
	}
	mi := desired.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities
	if mi == nil {
		return nil
	}
	ids := mi.DataPlane
	if len(ids.ImageRegistryMSIClientID) == 0 || len(ids.DiskMSIClientID) == 0 || len(ids.FileMSIClientID) == 0 {
		return nil
	}
	path := []string{"spec", "platform", "azure", "azureAuthenticationConfig"}
	kind, _, err := unstructured.NestedString(obj.Object, append(path, "azureAuthenticationConfigType")...)
	if err != nil {
		return utils.TrackError(err)
	}
	if kind != string(hsv1beta1.AzureAuthenticationTypeManagedIdentities) {
		return nil
	}
	path = append(path, "managedIdentities", "dataPlane")
	for field, id := range map[string]string{
		"imageRegistryMSIClientID": ids.ImageRegistryMSIClientID,
		"diskMSIClientID":          ids.DiskMSIClientID,
		"fileMSIClientID":          ids.FileMSIClientID,
	} {
		if err := unstructured.SetNestedField(obj.Object, id, append(path, field)...); err != nil {
			return utils.TrackError(err)
		}
	}
	return nil
}

const hostedClusterSourceVersionTag = "hostedClusterSourceInstanceVersion"

// The SPC and the ApplyDesire are separate Cosmos documents. Compare the SPC
// version before replacing the desire with its ETag so an older snapshot
// cannot undo a newer identity publication. The workqueue retries conflicts.
func (c *clusterResourcesController) ensureHostedClusterDesire(
	ctx context.Context,
	crud cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire],
	desire *kubeapplierapi.ApplyDesire,
	version int64,
) error {
	if version <= 0 {
		return utils.TrackError(fmt.Errorf("invalid ServiceProviderCluster instance version %d", version))
	}
	existing, err := c.applyDesireLister.GetByResourceID(ctx, desire.ResourceID.String())
	// A cluster-scoped desire keeps its logical ID across a placement change.
	// If the union cache returned the other placement, use the target partition.
	if err == nil && existing != nil && !controllerutil.ResourceIDsEqual(existing.Spec.ManagementCluster, desire.Spec.ManagementCluster) {
		existing, err = crud.Get(ctx, desire.ResourceID.Name)
	}
	if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
		return utils.TrackError(err)
	}
	if existing != nil {
		if existing.Tags[kubeapplierapi.TagControllerName] != ClusterResourcesControllerName {
			return utils.TrackError(fmt.Errorf("HostedCluster desire owned by %q", existing.Tags[kubeapplierapi.TagControllerName]))
		}
		if raw, ok := existing.Tags[hostedClusterSourceVersionTag]; ok {
			previous, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || previous <= 0 {
				return utils.TrackError(fmt.Errorf("invalid HostedCluster source version %q", raw))
			}
			if previous > version {
				return nil
			}
		}
	}
	// A fallback to CS also supersedes older SPC snapshots, even before the
	// first complete set of RP identities has been published.
	desire.Tags = maps.Clone(desire.Tags)
	desire.Tags[hostedClusterSourceVersionTag] = strconv.FormatInt(version, 10)
	if existing == nil {
		_, err = crud.Create(ctx, desire, nil)
	} else {
		specChanged := !kubeapplierhelpers.ApplyDesireSpecEqual(existing.Spec, desire.Spec)
		if !specChanged && maps.Equal(existing.Tags, desire.Tags) {
			return nil
		}
		next := existing.DeepCopy()
		next.Tags = desire.Tags
		if specChanged {
			next.Spec = desire.Spec
			next.Status = kubeapplierapi.ApplyDesireStatus{}
		}
		_, err = crud.Replace(ctx, next, nil)
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("publish HostedCluster desire: %w", err))
	}
	return nil
}
