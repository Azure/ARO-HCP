// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controllers

import (
	"context"
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// kubeContentMaxSizeBytes is the maximum serialized size (in bytes) stored
// as KubeContent in Cosmos. 2 MiB is the per-item Cosmos limit; we leave
// 10% headroom for the rest of the document.
//
// https://learn.microsoft.com/en-us/azure/cosmos-db/concepts-limits#per-item-limits
const kubeContentMaxSizeBytes = 1887436

// calculateManagementClusterContentFromReadDesire builds the desired
// ManagementClusterContent from a kube-applier ReadDesire. Status semantics
// match the older maestro-sourced flow so downstream consumers
// (operation_cluster_create, control_plane_active_version_controller,
// maestrohelpers) do not need to change:
//
//   - ReadDesire absent in cosmos → Degraded=True (ReadDesireNotFound).
//   - ReadDesire present but KubeContent unobserved → Degraded=True
//     (KubeContentNotAvailable).
//   - ReadDesire present and KubeContent over the per-item Cosmos limit →
//     Degraded=True (KubeContentMaxSizeExceeded); the prior content is
//     preserved by the caller.
//   - Otherwise → Degraded=False and Status.KubeContent is a
//     metav1.List{Items: [<raw>]} carrying the observed object.
//
// readDesireGetter is just the kube-applier-container Get for the ReadDesire
// document; the caller wires the per-management-cluster CRUD.
func calculateManagementClusterContentFromReadDesire(
	ctx context.Context,
	parentResourceID *azcorearm.ResourceID,
	readDesireName string,
	readDesireGetter func(ctx context.Context, name string) (*kubeapplier.ReadDesire, error),
) (*api.ManagementClusterContent, error) {
	contentResourceID := controllerutils.ManagementClusterContentResourceIDFromParentResourceID(parentResourceID, api.MaestroBundleInternalName(readDesireName))
	desired := controllerutils.NewInitialManagementClusterContent(contentResourceID)

	readDesire, err := readDesireGetter(ctx, readDesireName)
	if database.IsNotFoundError(err) {
		meta.SetStatusCondition(&desired.Status.Conditions, buildDegradedCondition(
			metav1.ConditionTrue, "ReadDesireNotFound",
			fmt.Sprintf("ReadDesire %q not found in kube-applier container", readDesireName),
		))
		return desired, nil
	}
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get ReadDesire: %w", err))
	}

	if readDesire.Status.KubeContent == nil || len(readDesire.Status.KubeContent.Raw) == 0 {
		meta.SetStatusCondition(&desired.Status.Conditions, buildDegradedCondition(
			metav1.ConditionTrue, "KubeContentNotAvailable",
			"kube-applier has not yet observed the target",
		))
		return desired, nil
	}

	rawBytes := readDesire.Status.KubeContent.Raw

	// Sanity-check that what kube-applier wrote is a single Kubernetes
	// object with a Kind, so downstream consumers (which deserialize into
	// concrete typed objects like HostedCluster) get a useful error
	// surface here rather than a confusing unmarshal failure later.
	unstructuredObj := &unstructured.Unstructured{}
	if err := json.Unmarshal(rawBytes, unstructuredObj); err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to unmarshal object from ReadDesire kubeContent: %w", err))
	}
	kind := unstructuredObj.GetKind()
	if kind == "" {
		return nil, utils.TrackError(fmt.Errorf("ReadDesire kubeContent has empty Kind"))
	}

	if len(rawBytes) > kubeContentMaxSizeBytes {
		meta.SetStatusCondition(&desired.Status.Conditions, buildDegradedCondition(
			metav1.ConditionTrue, "KubeContentMaxSizeExceeded",
			fmt.Sprintf("%s serialized size %.2f MiB exceeds Kube content max size %.2f MiB;",
				kind,
				float64(len(rawBytes))/(1024*1024),
				float64(kubeContentMaxSizeBytes)/(1024*1024),
			),
		))
		return desired, nil
	}

	// One ReadDesire watches exactly one object, so Items always has length 1.
	// The metav1.List wrapper preserves the on-wire shape downstream
	// consumers (which expect KubeContent.Items[0].Raw) already use.
	desired.Status.KubeContent = &metav1.List{
		Items: []runtime.RawExtension{{Raw: append([]byte(nil), rawBytes...)}},
	}
	meta.SetStatusCondition(&desired.Status.Conditions, buildDegradedCondition(
		metav1.ConditionFalse, "NoErrors", "As expected.",
	))

	return desired, nil
}

// persistManagementClusterContentFromReadDesire reads the named ReadDesire
// out of the kube-applier container and replaces / creates the
// corresponding ManagementClusterContent document. The merge logic
// (preserve LastTransitionTime when condition unchanged; keep prior
// KubeContent when the new pass produced none) matches what the
// maestro-sourced flow did.
func persistManagementClusterContentFromReadDesire(
	ctx context.Context,
	parentResourceID *azcorearm.ResourceID,
	readDesireName string,
	readDesireGetter func(ctx context.Context, name string) (*kubeapplier.ReadDesire, error),
	managementClusterContentsDBClient database.ManagementClusterContentCRUD,
) error {
	desired, err := calculateManagementClusterContentFromReadDesire(ctx, parentResourceID, readDesireName, readDesireGetter)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to calculate ManagementClusterContent from ReadDesire: %w", err))
	}

	existing, err := managementClusterContentsDBClient.Get(ctx, desired.ResourceID.Name)
	if err != nil && !database.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to get ManagementClusterContent: %w", err))
	}
	if database.IsNotFoundError(err) {
		if _, err := managementClusterContentsDBClient.Create(ctx, desired, nil); err != nil {
			return utils.TrackError(fmt.Errorf("failed to create ManagementClusterContent: %w", err))
		}
		return nil
	}

	desired.CosmosETag = existing.CosmosETag

	// If we haven't been able to retrieve fresh content this pass but
	// there is already content stored, keep the previous content rather
	// than blanking it. Mirrors the prior maestro-sourced flow.
	if desired.Status.KubeContent == nil && existing.Status.KubeContent != nil {
		desired.Status.KubeContent = existing.Status.KubeContent
	}

	// Preserve LastTransitionTime on unchanged conditions.
	tmpExistingStatus := existing.Status.DeepCopy()
	mergedConditions := make([]metav1.Condition, 0, len(desired.Status.Conditions))
	for _, desiredCondition := range desired.Status.Conditions {
		if meta.FindStatusCondition(tmpExistingStatus.Conditions, desiredCondition.Type) != nil {
			meta.SetStatusCondition(&tmpExistingStatus.Conditions, desiredCondition)
			merged := meta.FindStatusCondition(tmpExistingStatus.Conditions, desiredCondition.Type)
			mergedConditions = append(mergedConditions, *merged)
			continue
		}
		mergedConditions = append(mergedConditions, desiredCondition)
	}
	desired.Status.Conditions = mergedConditions

	if !controllerutils.NeedsUpdate(existing, desired) {
		return nil
	}

	if _, err := managementClusterContentsDBClient.Replace(ctx, desired, nil); err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ManagementClusterContent: %w", err))
	}
	return nil
}
