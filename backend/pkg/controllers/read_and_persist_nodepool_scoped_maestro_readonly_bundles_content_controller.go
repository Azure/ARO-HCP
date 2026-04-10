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
	"errors"
	"fmt"
	"net/http"
	"time"

	workv1 "open-cluster-management.io/api/work/v1"

	"k8s.io/apimachinery/pkg/api/equality"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer is a controller that reads the Maestro readonly
// bundle references stored in the ServiceProviderNodePool resource, retrieves the Maestro readonly bundles using
// those references, extracts the content and persists them in Cosmos as NodePoolManagementClusterContent documents.
// It is not responsible for creating the Maestro readonly bundles themselves. That is the responsibility of
// the createNodePoolScopedMaestroReadonlyBundlesSyncer controller.
// This controller assumes that it has full ownership of the NodePoolManagementClusterContent resource.
type readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer struct {
	maestroReadonlyBundleHelper

	cooldownChecker controllerutils.CooldownChecker

	activeOperationLister listers.ActiveOperationLister

	cosmosClient database.DBClient

	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ controllerutils.NodePoolSyncer = (*readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer)(nil)

func NewReadAndPersistNodePoolScopedMaestroReadonlyBundlesContentController(
	activeOperationLister listers.ActiveOperationLister,
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	informers informers.BackendInformers,
	maestroSourceEnvironmentIdentifier string,
	maestroClientBuilder maestro.MaestroClientBuilder,
) controllerutils.Controller {

	syncer := &readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer{
		maestroReadonlyBundleHelper: maestroReadonlyBundleHelper{
			maestroSourceEnvironmentIdentifier: maestroSourceEnvironmentIdentifier,
			maestroClientBuilder:               maestroClientBuilder,
		},
		cooldownChecker:       controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		cosmosClient:          cosmosClient,
		clusterServiceClient:  clusterServiceClient,
		activeOperationLister: activeOperationLister,
	}

	controller := controllerutils.NewNodePoolWatchingController(
		"ReadAndPersistNodePoolScopedMaestroReadonlyBundlesContent",
		cosmosClient,
		informers,
		1*time.Minute,
		syncer,
	)

	return controller
}

func (c *readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
	existingNodePool, err := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName).Get(ctx, key.HCPNodePoolName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil // node pool doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get NodePool: %w", err))
	}

	existingServiceProviderNodePool, err := database.GetOrCreateServiceProviderNodePool(ctx, c.cosmosClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderNodePool: %w", err))
	}

	// We return early if there are no Maestro Bundle references to process.
	if len(existingServiceProviderNodePool.Status.MaestroReadonlyBundles) == 0 {
		return nil
	}

	// We need the parent cluster to get the provision shard.
	existingCluster, err := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get parent Cluster: %w", err))
	}

	clusterProvisionShard, err := c.clusterServiceClient.GetClusterProvisionShard(ctx, existingCluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster Provision Shard from Cluster Service: %w", err))
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	maestroClient, err := c.createMaestroClientFromProvisionShard(ctx, clusterProvisionShard)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create Maestro client: %w", err))
	}

	var syncErrors []error
	for _, maestroBundleReference := range existingServiceProviderNodePool.Status.MaestroReadonlyBundles {
		err = c.readAndPersistMaestroBundleContent(ctx, existingNodePool, maestroBundleReference, maestroClient)
		if err != nil {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to read and persist NodePool management cluster content: %w", err)))
		}
	}

	return utils.TrackError(errors.Join(syncErrors...))
}

// calculateManagementClusterContentFromMaestroBundle calculates the desired ManagementClusterContent from the given Maestro Bundle reference.
func (c *readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer) calculateManagementClusterContentFromMaestroBundle(
	ctx context.Context, nodePool *api.HCPOpenShiftClusterNodePool, maestroBundleReference *api.MaestroBundleReference,
	maestroClient maestro.Client,
) (*api.ManagementClusterContent, error) {
	managementClusterContentResourceID := c.managementClusterContentResourceIDFromNodePoolResourceID(nodePool.ID, maestroBundleReference.Name)
	desired := c.newInitialManagementClusterContent(managementClusterContentResourceID)

	existingMaestroBundle, err := maestroClient.Get(ctx, maestroBundleReference.MaestroAPIMaestroBundleName, metav1.GetOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		return nil, utils.TrackError(fmt.Errorf("failed to get Maestro Bundle: %w", err))
	}
	if k8serrors.IsNotFound(err) {
		degradedCondition := c.buildDegradedCondition(api.ConditionTrue, "MaestroBundleNotFound", err.Error())
		controllerutils.SetCondition(&desired.Status.Conditions, degradedCondition)
		return desired, nil
	}

	rawBytes, err := c.getSingleResourceStatusFeedbackRawJSONFromMaestroBundle(existingMaestroBundle)
	if err != nil {
		degradedCondition := c.buildDegradedCondition(api.ConditionTrue, "MaestroBundleStatusFeedbackNotAvailable", err.Error())
		controllerutils.SetCondition(&desired.Status.Conditions, degradedCondition)
		return desired, nil
	}

	kubeContentMaxSizeExceeded := len(rawBytes) > c.kubeContentMaxSizeBytes()
	var kubeContextMaxSizeExceededConditionMessage string
	unstructuredObj := &unstructured.Unstructured{}
	err = json.Unmarshal(rawBytes, unstructuredObj)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to unmarshal object from status feedback value: %w", err))
	}
	kind := unstructuredObj.GetKind()
	if kind == "" {
		return nil, utils.TrackError(fmt.Errorf("expected kind to be not empty"))
	}

	objs, err := c.buildObjectsFromUnstructuredObj(unstructuredObj)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to build objects from unstructured object: %w", err))
	}
	var degradedCondition api.Condition
	if !kubeContentMaxSizeExceeded {
		desired.Status.KubeContent = &metav1.List{Items: objs}
		degradedCondition = c.buildDegradedCondition(api.ConditionFalse, "NoErrors", "As expected.")
	} else {
		kubeContextMaxSizeExceededConditionMessage = fmt.Sprintf("%s serialized size %.2f MiB exceeds Kube content max size %.2f MiB;", kind, float64(len(rawBytes))/(1024*1024), float64(c.kubeContentMaxSizeBytes())/(1024*1024))
		degradedCondition = c.buildDegradedCondition(api.ConditionTrue, "KubeContentMaxSizeExceeded", kubeContextMaxSizeExceededConditionMessage)
	}
	controllerutils.SetCondition(&desired.Status.Conditions, degradedCondition)

	return desired, nil
}

// buildObjectsFromUnstructuredObj builds the list of objects from the given unstructured object.
func (c *readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer) buildObjectsFromUnstructuredObj(unstructuredObj *unstructured.Unstructured) ([]runtime.RawExtension, error) {
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

func (c *readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer) buildDegradedCondition(conditionStatus api.ConditionStatus, conditionReason string, conditionMessage string) api.Condition {
	return api.Condition{
		Type:    "Degraded",
		Status:  conditionStatus,
		Reason:  conditionReason,
		Message: conditionMessage,
	}
}

// readAndPersistMaestroBundleContent reads the Maestro Bundle content from the given reference and persists it in Cosmos.
func (c *readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer) readAndPersistMaestroBundleContent(
	ctx context.Context, nodePool *api.HCPOpenShiftClusterNodePool, maestroBundleReference *api.MaestroBundleReference,
	maestroClient maestro.Client,
) error {

	desired, err := c.calculateManagementClusterContentFromMaestroBundle(ctx, nodePool, maestroBundleReference, maestroClient)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to calculate ManagementClusterContent from Maestro Bundle: %w", err))
	}

	managementClusterContentsDBClient := c.cosmosClient.NodePoolManagementClusterContents(
		nodePool.ID.SubscriptionID,
		nodePool.ID.ResourceGroupName,
		nodePool.ID.Parent.Name,
		nodePool.ID.Name,
	)

	existing, err := managementClusterContentsDBClient.Get(ctx, desired.CosmosMetadata.ResourceID.Name)
	if err != nil && !database.IsResponseError(err, http.StatusNotFound) {
		return utils.TrackError(fmt.Errorf("failed to get ManagementClusterContent: %w", err))
	}
	if database.IsResponseError(err, http.StatusNotFound) {
		_, err := managementClusterContentsDBClient.Create(ctx, desired, nil)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to create ManagementClusterContent: %w", err))
		}
		return nil
	}

	desired.CosmosETag = existing.CosmosETag

	if desired.Status.KubeContent == nil && existing.Status.KubeContent != nil {
		desired.Status.KubeContent = existing.Status.KubeContent
	}

	if equality.Semantic.DeepEqual(existing, desired) {
		return nil
	}

	_, err = managementClusterContentsDBClient.Replace(ctx, desired, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ManagementClusterContent: %w", err))
	}

	return nil
}

// kubeContentMaxSizeBytes returns the maximum size of a Cosmos DB item in bytes.
func (c *readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer) kubeContentMaxSizeBytes() int {
	return 1887436 // 2MB * 0.9
}

// getSingleResourceStatusFeedbackRawJSONFromMaestroBundle gets the single resource status feedback raw JSON from a Maestro Bundle.
func (c *readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer) getSingleResourceStatusFeedbackRawJSONFromMaestroBundle(maestroBundle *workv1.ManifestWork) (json.RawMessage, error) {
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

func (c *readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}

// newInitialManagementClusterContent returns a new ManagementClusterContent with the given resource ID.
func (c *readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer) newInitialManagementClusterContent(managementClusterContentResourceID *azcorearm.ResourceID) *api.ManagementClusterContent {
	return &api.ManagementClusterContent{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: managementClusterContentResourceID,
		},
		ResourceID: *managementClusterContentResourceID,
	}
}

// managementClusterContentResourceIDFromNodePoolResourceID returns the resource ID for the
// ManagementClusterContent associated to the given node pool resource ID and maestro bundle internal name.
func (c *readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer) managementClusterContentResourceIDFromNodePoolResourceID(nodePoolResourceID *azcorearm.ResourceID, maestroBundleInternalName api.MaestroBundleInternalName) *azcorearm.ResourceID {
	return api.Must(azcorearm.ParseResourceID(fmt.Sprintf("%s/%s/%s", nodePoolResourceID.String(), api.ManagementClusterContentResourceTypeName, maestroBundleInternalName)))
}
