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
	"net/http"
	"strings"
	"time"

	workv1 "open-cluster-management.io/api/work/v1"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// readAndPersistMaestroReadonlyBundlesContentSyncer is a controller that reads the Maestro readonly bundles
// references stored in the ServiceProviderCluster resource, retrieves the Maestro readonly bundles using those
// references, extracts the content of the Maestro readonly bundles and persists it in Cosmos.
// It is not responsible for creating the Maestro readonly bundles themselves. That is the responsibility of
// the createMaestroReadonlyBundlesSyncer controller.
// Right now we only support reading the content of the Maestro readonly bundle for HostedCluster associated to the cluster.
// In the future we might want to support reading the content of the Maestro readonly bundle for other resources.
type readAndPersistMaestroReadonlyBundlesContentSyncer struct {
	cooldownChecker controllerutils.CooldownChecker

	activeOperationLister listers.ActiveOperationLister

	cosmosClient database.DBClient

	clusterServiceClient ocm.ClusterServiceClientSpec

	maestroSourceEnvironmentIdentifier string
}

var _ controllerutils.ClusterSyncer = (*readAndPersistMaestroReadonlyBundlesContentSyncer)(nil)

func NewReadAndPersistMaestroReadonlyBundlesContentController(
	activeOperationLister listers.ActiveOperationLister,
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	clusterInformer cache.SharedIndexInformer,
	maestroSourceEnvironmentIdentifier string,
) controllerutils.Controller {

	syncer := &readAndPersistMaestroReadonlyBundlesContentSyncer{
		cooldownChecker:                    controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		cosmosClient:                       cosmosClient,
		clusterServiceClient:               clusterServiceClient,
		activeOperationLister:              activeOperationLister,
		maestroSourceEnvironmentIdentifier: maestroSourceEnvironmentIdentifier,
	}

	controller := controllerutils.NewClusterWatchingController(
		"ReadAndPersistMaestroReadonlyBundlesContent",
		cosmosClient,
		clusterInformer,
		1*time.Minute,
		syncer,
	)

	return controller
}

func (c *readAndPersistMaestroReadonlyBundlesContentSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil // cluster doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}

	existingServiceProviderCluster, err := controllerutils.GetOrCreateServiceProviderCluster(ctx, c.cosmosClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}

	// We get the provision shard (management cluster) the CS cluster is allocated to.
	// As of now in CS the shard allocation occurs synchronously during aro-hcp cluster creation call in CS API so
	// we are guaranteed to have a shard allocated for the cluster. If this changes in the future
	// we would need to change the logic in controllers to check that the retrieved cluster has a
	// shard allocated.
	clusterProvisionShard, err := c.clusterServiceClient.GetClusterProvisionShard(ctx, existingCluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster Provision Shard from Cluster Service: %w", err))
	}
	maestroClient, err := c.createSimpleMaestroClient(ctx, clusterProvisionShard)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create Simple Maestro client: %w", err))
	}

	// TODO in the future we might want to process the list in general and not just the hosted cluster. In that case
	// a switch based on the different recognized names could be used. However, we would need to decide what to do
	// if an unrecognized name is found, as well as what happens if in the middle of the processing one of them has
	// not fully reconciled or returned an error.
	hostedClusterMaestroBundleReference := existingServiceProviderCluster.MaestroReadonlyBundles.Get(api.MaestroBundleInternalNameHypershiftHostedCluster)
	if hostedClusterMaestroBundleReference == nil {
		return nil // hosted cluster maestro bundle reference not found, no work to do
	}
	if hostedClusterMaestroBundleReference.MaestroAPIMaestroBundleID == "" {
		// TODO This means the bundle entry was created in Cosmos but the ID not persisted (for example if backend crashes).
		// If that's the case, eventually in a next reconcile cycle this will be set as the CreateMaestroReadonlyBundlesController
		// will persist the ID. Do we want to return an error in this case or do we want to continue as succeeded?
		return nil
	}

	err = c.readAndPersistHostedCluster(ctx, existingCluster, hostedClusterMaestroBundleReference, maestroClient)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to read and persist hosted cluster: %w", err))
	}

	return nil
}

// readAndPersistHostedCluster reads the Cluster's Hypershift HostedCluster resource using the
// Maestro readonly bundle reference and persists it in Cosmos.
// To achieve that, it gets the Maestro readonly bundle pointing to the Cluster's HostedCluster, it extracts the
// returned content by Maestro by taking it from the Maestro bundles's status feedback rule that contains the whole object and then it persists it
// in Cosmos.
func (c *readAndPersistMaestroReadonlyBundlesContentSyncer) readAndPersistHostedCluster(
	ctx context.Context, cluster *api.HCPOpenShiftCluster, hostedClusterMaestroBundleReference *api.MaestroBundleReference,
	maestroClient maestro.SimpleMaestroClient,
) error {

	managementClusterContentsClient := c.cosmosClient.ManagementClusterContents(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name)
	existingManagementClusterContent, err := controllerutils.GetOrCreateManagementClusterContent(ctx, c.cosmosClient, cluster.ID, hostedClusterMaestroBundleReference.Name)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ManagementClusterContent: %w", err))
	}

	existingMaestroBundle, err := maestroClient.GetMaestroBundle(ctx, hostedClusterMaestroBundleReference.MaestroAPIMaestroBundleName, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		maestroBundleExistsCondition := c.buildMaestroBundleExistsCondition(false)
		controllerutils.SetCondition(&existingManagementClusterContent.Status.Conditions, maestroBundleExistsCondition)
		// TODO should we deal with other contents/conditions or we leave them as they are?
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Maestro Bundle: %w", err))
	}
	maestroBundleExistsCondition := c.buildMaestroBundleExistsCondition(true)
	controllerutils.SetCondition(&existingManagementClusterContent.Status.Conditions, maestroBundleExistsCondition)

	// TODO it can take some time for the Maestro Bundle content in the status feedback to be available.
	// How do we want to handle this? do we return error like now or do we want to differentiate?
	rawBytes, err := c.getSingleResourceStatusFeedbackRawJSONFromMaestroBundle(existingMaestroBundle)
	if err != nil {
		maestroBundleStatusFeedbackAvailableCondition := c.buildMaestroBundleStatusFeedbackAvailableCondition(false, err.Error())
		controllerutils.SetCondition(&existingManagementClusterContent.Status.Conditions, maestroBundleStatusFeedbackAvailableCondition)
		// TODO should we deal with other contents/conditions or we leave them as they are?
		return nil
	}
	maestroBundleStatusFeedbackAvailableCondition := c.buildMaestroBundleStatusFeedbackAvailableCondition(true, "")
	controllerutils.SetCondition(&existingManagementClusterContent.Status.Conditions, maestroBundleStatusFeedbackAvailableCondition)

	// Build desiredManagementClusterContent state from a deep copy so we can compare and only Replace when something changed (scales with new fields).
	desiredManagementClusterContent := existingManagementClusterContent.DeepCopy()

	kubeContentMaxSizeExceededCondition := c.buildKubeContentMaxSizeExceededCondition(len(rawBytes), "HostedCluster")
	// We only persist the retrieved content if it is within the size limit. If it
	// is outside the limit, we do not persist the content but we also do not unset
	// the kubeContent field. This is, when kubeContent is present it holds the
	// last successfully stored content.
	// TODO on size limit exceeded do we want to return an error aside from setting
	// the condition or do we just set the condition and continue as succeeded?
	if kubeContentMaxSizeExceededCondition.Status == api.ConditionFalse {
		hostedCluster := &hsv1beta1.HostedCluster{}
		err = json.Unmarshal(rawBytes, hostedCluster)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to unmarshal hosted cluster from status feedback value: %w", err))
		}
		// TODO is ListMeta or TypeMeta required at the metav1.List level?
		desiredManagementClusterContent.KubeContent = &metav1.List{
			Items: []runtime.RawExtension{
				{
					Object: hostedCluster,
				},
			},
		}
	}
	controllerutils.SetCondition(&desiredManagementClusterContent.Status.Conditions, kubeContentMaxSizeExceededCondition)

	// TODO do we want this or reflect.DeepEqual? what happens with empty vs nil map/slice? are there some cases where
	// we might want to know that distinction and at the same time use part of what Semantic Deepequal provides?
	if equality.Semantic.DeepEqual(existingManagementClusterContent, desiredManagementClusterContent) {
		return nil // nothing changed, skip write
	}

	_, err = managementClusterContentsClient.Replace(ctx, desiredManagementClusterContent, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ManagementClusterContent: %w", err))
	}

	return nil
}

// buildKubeContentMaxSizeExceededCondition builds the condition for the given raw content size and a boolean indicating
// if the content is within the limit (condition status False).
// The content is considered within the limit if its size is less than or equal to 90% of the maximum size of a Cosmos DB item (2MB).
// 2MB is the maximum size of a Cosmos DB item (https://learn.microsoft.com/en-us/azure/cosmos-db/concepts-limits#per-item-limits).
func (c *readAndPersistMaestroReadonlyBundlesContentSyncer) buildKubeContentMaxSizeExceededCondition(rawSizeBytes int, contentDescription string) api.Condition {
	kubeContentMaxSizeBytes := 1887436 // 2MB * 0.9
	withinLimit := rawSizeBytes <= kubeContentMaxSizeBytes

	condition := api.Condition{
		Type: "KubeContentMaxSizeExceeded",
	}

	if withinLimit {
		condition.Status = api.ConditionFalse
		condition.Reason = "WithinLimit"
	} else {
		condition.Status = api.ConditionTrue
		condition.Reason = "MaxSizeExceeded"
		condition.Message = fmt.Sprintf("%s serialized size %.2f MiB exceeds Kube content max size %.2f MiB; current content was not persisted.", contentDescription, float64(rawSizeBytes)/(1024*1024), float64(kubeContentMaxSizeBytes)/(1024*1024))
	}

	return condition
}

// buildMaestroBundleExistsCondition builds the condition for the given boolean indicating
// if the Maestro bundle exists (condition status True) or not (condition status False).
func (c *readAndPersistMaestroReadonlyBundlesContentSyncer) buildMaestroBundleExistsCondition(exists bool) api.Condition {
	condition := api.Condition{
		Type: "MaestroBundleExists",
	}

	if exists {
		condition.Status = api.ConditionTrue
		condition.Reason = "Exists"
	} else {
		condition.Status = api.ConditionFalse
		condition.Reason = "DoesNotExist"
	}

	return condition
}

// buildMaestroBundleStatusFeedbackAvailableCondition builds the condition for the given boolean indicating
// if the feedback is available (condition status True) or not (condition status False).
// The notAvailableMessage is used to set the condition message when the feedback is not available.
func (c *readAndPersistMaestroReadonlyBundlesContentSyncer) buildMaestroBundleStatusFeedbackAvailableCondition(available bool, notAvailableMessage string) api.Condition {
	condition := api.Condition{
		Type: "MaestroBundleStatusFeedbackAvailable",
	}

	if available {
		condition.Status = api.ConditionTrue
		condition.Reason = "Available"
	} else {
		condition.Status = api.ConditionFalse
		condition.Reason = "NotAvailable"
		condition.Message = notAvailableMessage
	}

	return condition
}

// getSingleResourceStatusFeedbackRawJSONFromMaestroBundle gets the single resource status feedback raw JSON from a Maestro Bundle.
// Used to extract the content of the resource from the Maestro Bundle.
// An error is returned if the Maestro Bundle does not contain a single resource or if the resource does not contain a single status feedback value
// with its name being "resource" and its type being JsonRaw.
func (c *readAndPersistMaestroReadonlyBundlesContentSyncer) getSingleResourceStatusFeedbackRawJSONFromMaestroBundle(maestroBundle *workv1.ManifestWork) (json.RawMessage, error) {
	resourceStatusManifests := maestroBundle.Status.ResourceStatus.Manifests
	if len(resourceStatusManifests) == 0 {
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

	// The following conditions could help telling giving some insights:
	// meta.IsStatusConditionTrue(resultMaestroBundle.Status.Conditions, "Applied")
	// meta.IsStatusConditionTrue(resultMaestroBundle.Status.Conditions, "Available")
	// meta.IsStatusConditionTrue(resultMaestroBundle.Status.Conditions, "StatusFeedbackApplied")
	// There are also `.version`, `.status.ObservedVersion` as well as some generation/version related fields in the bundle
	// as well as manifests within it, together with other inner levels of K8s conditions that could be explored.

	return []byte(*statusFeedbackValue.Value.JsonRaw), nil
}

func (c *readAndPersistMaestroReadonlyBundlesContentSyncer) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}

// createSimpleMaestroClient creates a Simple Maestro client for the given cluster provision shard.
// the client is scoped to the Consumer Name associated to the provision shard, and to
// the source ID associated to the provision shard and the environment specified
// in c.maestroSourceEnvironmentIdentifier, which is a configuration parameter at
// deployment time.
func (c *readAndPersistMaestroReadonlyBundlesContentSyncer) createSimpleMaestroClient(
	ctx context.Context, clusterProvisionShard *arohcpv1alpha1.ProvisionShard,
) (maestro.SimpleMaestroClient, error) {
	provisionShardMaestroConsumerName := clusterProvisionShard.MaestroConfig().ConsumerName()
	provisionShardMaestroRESTAPIEndpoint := clusterProvisionShard.MaestroConfig().RestApiConfig().Url()
	provisionShardMaestroGRPCAPIEndpoint := clusterProvisionShard.MaestroConfig().GrpcApiConfig().Url()
	// This allows us to be able to have visibility on the Maestro Bundles owned by the same source ID for a given
	// provision shard and environment. This should have the same source ID as what CS has in each corresponding environment
	// because otherwise we would not have visibility on the Maestro Bundles owned
	maestroSourceID := maestro.GenerateMaestroSourceID(c.maestroSourceEnvironmentIdentifier, clusterProvisionShard.ID())

	maestroClient, err := maestro.NewSimpleMaestroClient(ctx, provisionShardMaestroRESTAPIEndpoint, provisionShardMaestroGRPCAPIEndpoint, provisionShardMaestroConsumerName, maestroSourceID)

	return maestroClient, err
}

// newInitialManagementClusterContent returns a new ManagementClusterContent with
// the given resource ID as its parent. The resource ID is assumed to be a
// cluster resource ID.
// The returned value can be used to consistently initialize a new ManagementClusterContent
func (c *readAndPersistMaestroReadonlyBundlesContentSyncer) newInitialManagementClusterContent(managementClusterContentResourceID *azcorearm.ResourceID) *api.ManagementClusterContent {
	return &api.ManagementClusterContent{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: managementClusterContentResourceID,
		},
		ResourceID: *managementClusterContentResourceID,
	}
}

// managementClusterContentResourceIDFromClusterResourceID returns the resource ID for the
// ManagementClusterContent associated to the given cluster resource ID and maestro bundle internal name.
func (c *readAndPersistMaestroReadonlyBundlesContentSyncer) managementClusterContentResourceIDFromClusterResourceID(clusterResourceID *azcorearm.ResourceID, maestroBundleInternalName api.MaestroBundleInternalName) *azcorearm.ResourceID {
	return api.Must(azcorearm.ParseResourceID(fmt.Sprintf("%s/%s/%s", clusterResourceID.String(), api.ManagementClusterContentResourceTypeName, maestroBundleInternalName)))
}

// GetOrCreateManagementClusterContent gets the ManagementClusterContent
// instance for the given cluster resource ID.
// If it doesn't exist, it creates a new one.
// clusterResourceID is assumed to be a cluster resource ID.
func (c *readAndPersistMaestroReadonlyBundlesContentSyncer) GetOrCreateManagementClusterContent(
	ctx context.Context, dbClient database.DBClient, clusterResourceID *azcorearm.ResourceID, maestroBundleInternalName api.MaestroBundleInternalName,
) (*api.ManagementClusterContent, error) {
	// Azure resource types are case-insensitive; ToClusterResourceIDString lowercases the path so parsed IDs may have lowercase type.
	if !strings.EqualFold(clusterResourceID.ResourceType.String(), api.ClusterResourceType.String()) {
		return nil, utils.TrackError(fmt.Errorf("expected resource type %s, got %s", api.ClusterResourceType, clusterResourceID.ResourceType))
	}

	managementClusterContentsDBClient := dbClient.ManagementClusterContents(
		clusterResourceID.SubscriptionID,
		clusterResourceID.ResourceGroupName,
		clusterResourceID.Name,
	)

	resourceID := c.managementClusterContentResourceIDFromClusterResourceID(clusterResourceID, maestroBundleInternalName)
	managementClusterContentName := string(maestroBundleInternalName)
	existingManagementClusterContent, err := managementClusterContentsDBClient.Get(ctx, managementClusterContentName)
	if err == nil {
		return existingManagementClusterContent, nil
	}

	if !database.IsResponseError(err, http.StatusNotFound) {
		return nil, utils.TrackError(fmt.Errorf("failed to get ManagementClusterContent: %w", err))
	}

	initialManagementClusterContent := c.newInitialManagementClusterContent(resourceID)
	existingManagementClusterContent, err = managementClusterContentsDBClient.Create(ctx, initialManagementClusterContent, nil)
	if err == nil {
		return existingManagementClusterContent, nil
	}

	// We optimize here and if creation failed because it already exists, we try
	// to get again one last time.
	// According to the Cosmos DB API documentation, a HTTP 409 Conflict error
	// is returned when the item already exists: https://learn.microsoft.com/en-us/rest/api/cosmos-db/create-a-document#status-codes
	if !database.IsResponseError(err, http.StatusConflict) {
		return nil, utils.TrackError(fmt.Errorf("failed to create ManagementClusterContent: %w", err))
	}

	existingManagementClusterContent, err = managementClusterContentsDBClient.Get(ctx, managementClusterContentName)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get ManagementClusterContent: %w", err))
	}

	return existingManagementClusterContent, nil
}
