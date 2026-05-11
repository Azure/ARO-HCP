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

	workv1 "open-cluster-management.io/api/work/v1"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	// readonlyBundleManagedByK8sLabelKey is the key of the K8s label that is used to identify the controller that manages the readonly Maestro bundle.
	readonlyBundleManagedByK8sLabelKey = "aro-hcp.azure.com/readonly-bundle-managed-by"
)

// kubeContentMaxSizeBytes is the maximum serialized size (in bytes) stored as KubeContent in Cosmos.
// 2MB is the maximum size of a Cosmos DB item (https://learn.microsoft.com/en-us/azure/cosmos-db/concepts-limits#per-item-limits).
const kubeContentMaxSizeBytes = 1887436 // 2MB * 0.9

// buildInitialReadonlyMaestroBundle builds an initial readonly Maestro Bundle for a given resource specified in obj.
// objResourceIdentifier is the resource identifier of the resource specified in obj.
// maestroBundleNamespacedName is the namespaced name of the Maestro Bundle.
// managedByLabelValue is the value of the readonlyBundleManagedByK8sLabelKey label to apply to the bundle.
// Used to create the readonly Maestro bundle associated to the resource specified in obj. Some controllers consider
// the readonlyBundleManagedByK8sLabelKey label to perform their own filtering of Maestro Bundles.
func buildInitialReadonlyMaestroBundle(maestroBundleNamespacedName types.NamespacedName, objResourceIdentifier workv1.ResourceIdentifier, obj runtime.Object, managedByLabelValue string) *workv1.ManifestWork {
	maestroBundleObjMeta := metav1.ObjectMeta{
		Name:            maestroBundleNamespacedName.Name,
		Namespace:       maestroBundleNamespacedName.Namespace,
		ResourceVersion: "0", // TODO is this needed when creating a maestro bundle?
		Labels: map[string]string{
			// We define it as a K8s label because Maestro supports server-side filtering based on K8s labels.
			// We can define it as a K8s label because for this specific use case we can comply with
			// K8s labels length and charset restrictions https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#syntax-and-character-set.
			readonlyBundleManagedByK8sLabelKey: managedByLabelValue,
		},
	}

	// We build the Maestro Bundle that will contain the resource specified in obj.
	// Aside from putting the resource (manifest) previously built above, we
	// also define a FeedbackRule that will allow us to retrieve the whole content
	// from the management cluster
	maestroBundle := &workv1.ManifestWork{
		ObjectMeta: maestroBundleObjMeta,
		Spec: workv1.ManifestWorkSpec{
			Workload: workv1.ManifestsTemplate{
				Manifests: []workv1.Manifest{
					{
						RawExtension: runtime.RawExtension{
							// We put the resource (manifest) specified in obj.
							// In Maestro only the desired `spec` as defined in the bundle can be retrieved
							// from here when querying the Maestro Bundle.
							// To retrieve another section other than the desired spec Maestro
							// requires defining FeedbackRule(s) in the Maestro bundle.
							// For maestro readonly resources, not even the desired spec can be retrieved from here. For
							// those type of resources it needs to be retrieved via status feedback rule(s) too.
							// For owned resources, here the desired spec can be retrieved but that
							// is not necessarily the actual spec in the management cluster side. If that is
							// desired it is again necessary to get the spec via FeedbackRule(s).
							Object: obj,
						},
					},
				},
			},
			ManifestConfigs: []workv1.ManifestConfigOption{
				// We also need to define the ManifestConfig associated to the resource(manifest)
				// that is being put within the Maestro Bundle.
				{
					// ResourceIdentifier needs to be specified and it is the information
					// associated to the manifest that is being put within the Maestro Bundle.
					ResourceIdentifier: objResourceIdentifier,
					// We need to set the UpdateStrategy to read only. This
					// creates a "readonly maestro bundle".
					UpdateStrategy: &workv1.UpdateStrategy{
						Type: workv1.UpdateStrategyTypeReadOnly,
					},
					// We define a feedbackrule based on JSONPath. We alias the name
					// of this JSONPath as "resource" and its real JSONPath is "@" which
					// signals the whole object is retrieved. This includes both spec
					// and status.
					FeedbackRules: []workv1.FeedbackRule{
						{
							Type: workv1.JSONPathsType,
							JsonPaths: []workv1.JsonPath{
								{
									Name: "resource",
									Path: "@",
								},
							},
						},
					},
				},
			},
		},
	}

	return maestroBundle
}

// buildInitialMaestroBundleReference builds an initial Maestro Bundle reference for a given maestro bundle internal name.
func buildInitialMaestroBundleReference(internalName api.MaestroBundleInternalName, generator maestro.MaestroAPIMaestroBundleNameGenerator) (*api.MaestroBundleReference, error) {
	maestroAPIMaestroBundleName, err := generator.NewMaestroAPIMaestroBundleName()
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to generate Maestro API Maestro Bundle name: %w", err))
	}
	return &api.MaestroBundleReference{
		Name:                        internalName,
		MaestroAPIMaestroBundleName: maestroAPIMaestroBundleName,
		MaestroAPIMaestroBundleID:   "",
	}, nil
}

// buildObjectsFromUnstructuredObj builds the list of objects from the given unstructured object.
// If the unstructured object is a list, it flattens the list of objects from the list of items. Nested lists are not flattened.
// If the unstructured object is not a list, it returns a list with a single item being the single object.
func buildObjectsFromUnstructuredObj(unstructuredObj *unstructured.Unstructured) ([]runtime.RawExtension, error) {
	if !unstructuredObj.IsList() {
		return []runtime.RawExtension{{Object: unstructuredObj}}, nil
	}

	objs := []runtime.RawExtension{}
	err := unstructuredObj.EachListItem(func(o runtime.Object) error {
		objs = append(objs, runtime.RawExtension{Object: o})
		return nil
	})
	if err != nil {
		return nil, utils.TrackError(err)
	}

	return objs, nil
}

func buildDegradedCondition(conditionStatus metav1.ConditionStatus, conditionReason string, conditionMessage string) metav1.Condition {
	return metav1.Condition{
		Type:    "Degraded",
		Status:  conditionStatus,
		Reason:  conditionReason,
		Message: conditionMessage,
	}
}

// getSingleResourceStatusFeedbackRawJSONFromMaestroBundle gets the single resource status feedback raw JSON from a Maestro Bundle.
// Used to extract the content of the resource from the Maestro Bundle.
// An error is returned if the Maestro Bundle does not contain a single resource or if the resource does not contain a single status feedback value
// with its name being "resource" and its type being JsonRaw.
func getSingleResourceStatusFeedbackRawJSONFromMaestroBundle(maestroBundle *workv1.ManifestWork) (json.RawMessage, error) {
	resourceStatusManifests := maestroBundle.Status.ResourceStatus.Manifests
	if len(resourceStatusManifests) != 1 {
		return nil, utils.TrackError(fmt.Errorf("expected exactly one resource within the Maestro Bundle, got %d", len(resourceStatusManifests)))
	}

	statusFeedbackValues := resourceStatusManifests[0].StatusFeedbacks.Values
	if len(statusFeedbackValues) == 0 {
		return nil, utils.TrackError(fmt.Errorf("expected exactly one status feedback value within the Maestro Bundle resource, got %d", len(statusFeedbackValues)))
	}
	if len(statusFeedbackValues) > 1 {
		return nil, utils.TrackError(fmt.Errorf("expected exactly one status feedback value within the Maestro Bundle resource, got %d", len(statusFeedbackValues)))
	}
	statusFeedbackValue := statusFeedbackValues[0]
	if statusFeedbackValue.Name != "resource" {
		return nil, utils.TrackError(fmt.Errorf("expected status feedback value name to be 'resource', got %s", statusFeedbackValue.Name))
	}
	if statusFeedbackValue.Value.Type != workv1.JsonRaw {
		return nil, utils.TrackError(fmt.Errorf("expected status feedback value type to be JsonRaw, got %s", statusFeedbackValue.Value.Type))
	}
	if statusFeedbackValue.Value.JsonRaw == nil {
		return nil, utils.TrackError(fmt.Errorf("expected status feedback value JsonRaw to be not nil"))
	}

	return []byte(*statusFeedbackValue.Value.JsonRaw), nil
}

// calculateManagementClusterContentFromMaestroBundle builds the desired ManagementClusterContent from a Maestro
// bundle reference. parentResourceID is the ARM ID of the document parent
func calculateManagementClusterContentFromMaestroBundle(
	ctx context.Context,
	parentResourceID *azcorearm.ResourceID,
	maestroBundleReference *api.MaestroBundleReference,
	maestroClient maestro.Client,
) (*api.ManagementClusterContent, error) {
	managementClusterContentResourceID := controllerutils.ManagementClusterContentResourceIDFromParentResourceID(parentResourceID, maestroBundleReference.Name)
	desired := controllerutils.NewInitialManagementClusterContent(managementClusterContentResourceID)

	existingMaestroBundle, err := maestroClient.Get(ctx, maestroBundleReference.MaestroAPIMaestroBundleName, metav1.GetOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		return nil, utils.TrackError(fmt.Errorf("failed to get Maestro Bundle: %w", err))
	}
	if k8serrors.IsNotFound(err) {
		degradedCondition := buildDegradedCondition(metav1.ConditionTrue, "MaestroBundleNotFound", err.Error())
		meta.SetStatusCondition(&desired.Status.Conditions, degradedCondition)
		return desired, nil
	}

	rawBytes, err := getSingleResourceStatusFeedbackRawJSONFromMaestroBundle(existingMaestroBundle)
	if err != nil {
		degradedCondition := buildDegradedCondition(metav1.ConditionTrue, "MaestroBundleStatusFeedbackNotAvailable", err.Error())
		meta.SetStatusCondition(&desired.Status.Conditions, degradedCondition)
		return desired, nil
	}

	kubeContentMaxSizeExceeded := len(rawBytes) > kubeContentMaxSizeBytes
	var kubeContextMaxSizeExceededConditionMessage string
	// We only set the retrieved content if it is within the size limit. If it
	// is outside the limit we set the Degraded condition communicating the issue.
	// We use unstructuredObj.Unstructured to deserialize the content so we can
	// implement logic agnostic to the type of the content being retrieved.
	unstructuredObj := &unstructured.Unstructured{}
	err = json.Unmarshal(rawBytes, unstructuredObj)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to unmarshal object from status feedback value: %w", err))
	}
	kind := unstructuredObj.GetKind()
	if kind == "" {
		return nil, utils.TrackError(fmt.Errorf("expected kind to be not empty"))
	}

	objs, err := buildObjectsFromUnstructuredObj(unstructuredObj)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to build objects from unstructured object: %w", err))
	}
	var degradedCondition metav1.Condition
	if !kubeContentMaxSizeExceeded {
		// TODO is ListMeta or TypeMeta required at the metav1.List level?
		desired.Status.KubeContent = &metav1.List{Items: objs}
		degradedCondition = buildDegradedCondition(metav1.ConditionFalse, "NoErrors", "As expected.")
	} else {
		kubeContextMaxSizeExceededConditionMessage = fmt.Sprintf("%s serialized size %.2f MiB exceeds Kube content max size %.2f MiB;", kind, float64(len(rawBytes))/(1024*1024), float64(kubeContentMaxSizeBytes)/(1024*1024))
		degradedCondition = buildDegradedCondition(metav1.ConditionTrue, "KubeContentMaxSizeExceeded", kubeContextMaxSizeExceededConditionMessage)
	}
	meta.SetStatusCondition(&desired.Status.Conditions, degradedCondition)

	return desired, nil
}

// readAndPersistMaestroReadonlyBundleContent reads a Maestro readonly bundle and creates or updates the corresponding
// ManagementClusterContent in Cosmos. parentResourceID is the resource id of the parent resource of the ManagementClusterContent.
func readAndPersistMaestroReadonlyBundleContent(
	ctx context.Context,
	parentResourceID *azcorearm.ResourceID,
	maestroBundleReference *api.MaestroBundleReference,
	maestroClient maestro.Client,
	managementClusterContentsDBClient database.ManagementClusterContentCRUD,
) error {
	desired, err := calculateManagementClusterContentFromMaestroBundle(ctx, parentResourceID, maestroBundleReference, maestroClient)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to calculate ManagementClusterContent from Maestro Bundle: %w", err))
	}

	existing, err := managementClusterContentsDBClient.Get(ctx, desired.ResourceID.Name)
	if err != nil && !database.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to get ManagementClusterContent: %w", err))
	}
	if database.IsNotFoundError(err) {
		_, err := managementClusterContentsDBClient.Create(ctx, desired, nil)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to create ManagementClusterContent: %w", err))
		}
		return nil
	}

	// We set the Cosmos ETag to the existing one to avoid conflicts when replacing the document
	// unless someone else has modified the document since we last read it.
	desired.CosmosETag = existing.CosmosETag

	// If we haven't been able to retrieve the content but there was already content
	// stored we keep the previously existing stored content.
	if desired.Status.KubeContent == nil && existing.Status.KubeContent != nil {
		desired.Status.KubeContent = existing.Status.KubeContent
	}

	// The existing ManagementClusterContent in Cosmos might include conditions that already exist beforehand and that
	// have been calculated in the new desired content. To preserve the LastTransitionTime of those conditions in the case
	// where the status of them hasn't changed, what we do is:
	// 1. Deep copy of the existing status from Cosmos, which includes the conditions
	// 2. Iterate over the newly calculated desired conditions, and for each condition:
	//   2.1. Check if the condition already exists in the existing status
	//   2.2. If it does, update the condition in the existing status with the new values using SetCondition. This
	//        will update the LastTransitionTime to the current time if there's been a change or keep the existing
	//        LastTransitionTime if the condition hasn't changed its status. Then, use the newly updated condition as
	//        the desired one.
	//   2.3. If it does not, then keep the condition as is
	// 3. Assign the merged conditions to the desired status.
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

	_, err = managementClusterContentsDBClient.Replace(ctx, desired, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ManagementClusterContent: %w", err))
	}

	return nil
}
