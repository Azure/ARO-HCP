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

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// readAndPersistMaestroReadonlyBundlesContentSyncer is a controller that reads the Maestro readonly bundles
// references stored in the ServiceProviderCluster resource, retrieves the Maestro readonly bundles using those
// references, extracts the content of the Maestro readonly bundles and persists them in Cosmos.
// It is not responsible for creating the Maestro readonly bundles themselves. That is the responsibility of
// the createMaestroReadonlyBundlesSyncer controller.
// As of now we support reading the content of the Maestro readonly bundle of the Hypershift's HostedCluster associated
// to the Cluster.
// This controller assumes that it has full ownership of the ManagementClusterContent resource.
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
	informers informers.BackendInformers,
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
		informers,
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

	// We return early if there are no Maestro Bundle references to process.
	if len(existingServiceProviderCluster.Status.MaestroReadonlyBundles) == 0 {
		return nil
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
	maestroClient, err := c.createMaestroClientFromProvisionShard(ctx, clusterProvisionShard)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create Maestro client: %w", err))
	}

	var syncErrors []error
	for _, maestroBundleReference := range existingServiceProviderCluster.Status.MaestroReadonlyBundles {
		err = c.readAndPersistMaestroBundleContent(ctx, existingCluster, maestroBundleReference, maestroClient)
		if err != nil {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to read and persist HostedCluster: %w", err)))
		}

	}

	return utils.TrackError(errors.Join(syncErrors...))
}

// calculateManagementClusterContentFromMaestroBundle calculates the desired ManagementClusterContent from the given Maestro Bundle reference.
// It returns the desired ManagementClusterContent or an error if the calculation fails.
func (c *readAndPersistMaestroReadonlyBundlesContentSyncer) calculateManagementClusterContentFromMaestroBundle(
	ctx context.Context, cluster *api.HCPOpenShiftCluster, hostedClusterMaestroBundleReference *api.MaestroBundleReference,
	maestroClient maestro.Client,
) (*api.ManagementClusterContent, error) {
	managementClusterContentResourceID := c.managementClusterContentResourceIDFromClusterResourceID(cluster.ID, hostedClusterMaestroBundleReference.Name)
	desired := c.newInitialManagementClusterContent(managementClusterContentResourceID)

	existingMaestroBundle, err := maestroClient.Get(ctx, hostedClusterMaestroBundleReference.MaestroAPIMaestroBundleName, metav1.GetOptions{})
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

	objs, err := c.buildObjectsFromUnstructuredObj(unstructuredObj)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to build objects from unstructured object: %w", err))
	}
	var degradedCondition api.Condition
	if !kubeContentMaxSizeExceeded {
		// TODO is ListMeta or TypeMeta required at the metav1.List level?
		desired.KubeContent = &metav1.List{Items: objs}
		degradedCondition = c.buildDegradedCondition(api.ConditionFalse, "", "")
	} else {
		kubeContextMaxSizeExceededConditionMessage = fmt.Sprintf("%s serialized size %.2f MiB exceeds Kube content max size %.2f MiB;", kind, float64(len(rawBytes))/(1024*1024), float64(c.kubeContentMaxSizeBytes())/(1024*1024))
		degradedCondition = c.buildDegradedCondition(api.ConditionTrue, "KubeContentMaxSizeExceeded", kubeContextMaxSizeExceededConditionMessage)
	}
	controllerutils.SetCondition(&desired.Status.Conditions, degradedCondition)

	return desired, nil
}

// buildObjectsFromUnstructuredObj builds the list of objects from the given unstructured object.
// If the unstructured object is a list, it flattens the list of objects from the list of items. Nested lists are not flattened.
// If the unstructured object is not a list, it returns a list with a single item being the single object.
func (c *readAndPersistMaestroReadonlyBundlesContentSyncer) buildObjectsFromUnstructuredObj(unstructuredObj *unstructured.Unstructured) ([]runtime.RawExtension, error) {
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

func (c *readAndPersistMaestroReadonlyBundlesContentSyncer) buildDegradedCondition(conditionStatus api.ConditionStatus, conditionReason string, conditionMessage string) api.Condition {
	return api.Condition{
		Type:    "Degraded",
		Status:  conditionStatus,
		Reason:  conditionReason,
		Message: conditionMessage,
	}
}

// readAndPersistMaestroBundleContent reads the Maestro Bundle content from the given Maestro Bundle reference
// and persists it in Cosmos.
// To achieve that, it gets the Maestro readonly bundle pointing to the Cluster's HostedCluster, it extracts the
// returned content by Maestro by taking it from the Maestro bundles's status feedback rule that contains the whole object and then it persists it
// in Cosmos.
func (c *readAndPersistMaestroReadonlyBundlesContentSyncer) readAndPersistMaestroBundleContent(
	ctx context.Context, cluster *api.HCPOpenShiftCluster, hostedClusterMaestroBundleReference *api.MaestroBundleReference,
	maestroClient maestro.Client,
) error {

	desired, err := c.calculateManagementClusterContentFromMaestroBundle(ctx, cluster, hostedClusterMaestroBundleReference, maestroClient)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to calculate ManagementClusterContent from Maestro Bundle: %w", err))
	}

	managementClusterContentsDBClient := c.cosmosClient.ManagementClusterContents(
		cluster.ID.SubscriptionID,
		cluster.ID.ResourceGroupName,
		cluster.ID.Name,
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

	// We set the Cosmos ETag to the existing one to avoid conflicts when replacing the document
	// unless someone else has modified the document since we last read it.
	desired.CosmosETag = existing.CosmosETag

	// If we haven't been able to retrieve the content but there was already content
	// stored we keep the previously existing stored content.
	if desired.KubeContent == nil && existing.KubeContent != nil {
		desired.KubeContent = existing.KubeContent
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
// 2MB is the maximum size of a Cosmos DB item (https://learn.microsoft.com/en-us/azure/cosmos-db/concepts-limits#per-item-limits).
func (c *readAndPersistMaestroReadonlyBundlesContentSyncer) kubeContentMaxSizeBytes() int {
	return 1887436 // 2MB * 0.9
}

// getSingleResourceStatusFeedbackRawJSONFromMaestroBundle gets the single resource status feedback raw JSON from a Maestro Bundle.
// Used to extract the content of the resource from the Maestro Bundle.
// An error is returned if the Maestro Bundle does not contain a single resource or if the resource does not contain a single status feedback value
// with its name being "resource" and its type being JsonRaw.
func (c *readAndPersistMaestroReadonlyBundlesContentSyncer) getSingleResourceStatusFeedbackRawJSONFromMaestroBundle(maestroBundle *workv1.ManifestWork) (json.RawMessage, error) {
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

func (c *readAndPersistMaestroReadonlyBundlesContentSyncer) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}

// createMaestroClientFromProvisionShard creates a Maestro client for the given cluster provision shard.
// the client is scoped to the Consumer Name associated to the provision shard, and to
// the source ID associated to the provision shard and the environment specified
// in c.maestroSourceEnvironmentIdentifier, which is a configuration parameter at
// deployment time.
func (c *readAndPersistMaestroReadonlyBundlesContentSyncer) createMaestroClientFromProvisionShard(
	ctx context.Context, clusterProvisionShard *arohcpv1alpha1.ProvisionShard,
) (maestro.Client, error) {
	provisionShardMaestroConsumerName := clusterProvisionShard.MaestroConfig().ConsumerName()
	provisionShardMaestroRESTAPIEndpoint := clusterProvisionShard.MaestroConfig().RestApiConfig().Url()
	provisionShardMaestroGRPCAPIEndpoint := clusterProvisionShard.MaestroConfig().GrpcApiConfig().Url()
	// This allows us to be able to have visibility on the Maestro Bundles owned by the same source ID for a given
	// provision shard and environment. This should have the same source ID as what CS has in each corresponding environment
	// because otherwise we would not have visibility on the Maestro Bundles owned
	maestroSourceID := maestro.GenerateMaestroSourceID(c.maestroSourceEnvironmentIdentifier, clusterProvisionShard.ID())

	maestroClient, err := maestro.NewClient(ctx, provisionShardMaestroRESTAPIEndpoint, provisionShardMaestroGRPCAPIEndpoint, provisionShardMaestroConsumerName, maestroSourceID)

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
