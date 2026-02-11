package controllers

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

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	workv1 "open-cluster-management.io/api/work/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type maestroShowcaseSyncer struct {
	cooldownChecker controllerutils.CooldownChecker

	activeOperationLister listers.ActiveOperationLister

	cosmosClient database.DBClient

	clusterServiceClient ocm.ClusterServiceClientSpec

	maestroSourceEnvironmentIdentifier string
}

var _ controllerutils.ClusterSyncer = (*maestroShowcaseSyncer)(nil)

func NewMaestroShowcaseController(
	activeOperationLister listers.ActiveOperationLister,
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	clusterInformer cache.SharedIndexInformer,
	maestroSourceEnvironmentIdentifier string,
) controllerutils.Controller {

	syncer := &maestroShowcaseSyncer{
		cooldownChecker:                    controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		cosmosClient:                       cosmosClient,
		clusterServiceClient:               clusterServiceClient,
		activeOperationLister:              activeOperationLister,
		maestroSourceEnvironmentIdentifier: maestroSourceEnvironmentIdentifier,
	}

	controller := controllerutils.NewClusterWatchingController(
		"MaestroShowcase",
		cosmosClient,
		clusterInformer,
		1*time.Minute,
		syncer,
	)

	return controller
}

func (c *maestroShowcaseSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

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

	// We always return true in shouldProcess for now. We shouldn't merge this
	// as it's a showcase controller.
	shouldProcess := c.shouldProcess(existingServiceProviderCluster)
	if !shouldProcess {
		return nil // no work to do
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

	// Duplicated in createSimpleMaestroClient but okay to showcase. Just needed to
	// log them here.
	maestroSourceID := maestro.GenerateMaestroSourceID(c.maestroSourceEnvironmentIdentifier, clusterProvisionShard.ID())
	provisionShardMaestroConsumerName := clusterProvisionShard.MaestroConfig().ConsumerName()
	logger = logger.WithValues("maestroSourceID", maestroSourceID, "maestroConsumerName", provisionShardMaestroConsumerName)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("listing maestro bundles...")
	err = c.listAndLogAllMaestroBundlesWithSourceIDAndConsumerName(ctx, maestroClient)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to log all Maestro Bundles with Source ID %s and Maestro Consumer Name %s: %w", maestroSourceID, provisionShardMaestroConsumerName, err))
	}

	manifestWorkGR := schema.GroupResource{Group: "work.open-cluster-management.io", Resource: "manifestworks"}
	logger.Info(fmt.Sprintf("listing maestro bundles with source ID %s, Maestro Consumer Name %s and GroupResource %s", maestroSourceID, provisionShardMaestroConsumerName, manifestWorkGR))
	err = c.listAndLogMaestroBundlesWithSourceIDAndConsumerNameWithManifestConfigsMatchingGR(ctx, maestroClient, manifestWorkGR)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to log Maestro Bundles with Source ID %s and Maestro Consumer Name %s: %w", maestroSourceID, provisionShardMaestroConsumerName, err))
	}

	return nil
}

func (c *maestroShowcaseSyncer) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}

// createSimpleMaestroClient creates a Simple Maestro client for the given cluster provision shard.
// the client is scoped to the Consumer Name associated to the provision shard, and to
// the source ID associated to the provision shard and the environment specified
// in c.maestroSourceEnvironmentIdentifier, which is a configuration parameter at
// deployment time.
func (c *maestroShowcaseSyncer) createSimpleMaestroClient(
	ctx context.Context, clusterProvisionShard *arohcpv1alpha1.ProvisionShard,
) (maestro.SimpleMaestroClient, error) {
	logger := utils.LoggerFromContext(ctx)

	provisionShardMaestroConsumerName := clusterProvisionShard.MaestroConfig().ConsumerName()
	provisionShardMaestroRESTAPIEndpoint := clusterProvisionShard.MaestroConfig().RestApiConfig().Url()
	provisionShardMaestroGRPCAPIEndpoint := clusterProvisionShard.MaestroConfig().GrpcApiConfig().Url()
	// This allows us to be able to have visibility on the Maestro Bundles owned by the same source ID for a given
	// provision shard and environment. This should have the same source ID as what CS has in each corresponding environment
	// because otherwise we would not have visibility on the Maestro Bundles owned
	maestroSourceID := maestro.GenerateMaestroSourceID(c.maestroSourceEnvironmentIdentifier, clusterProvisionShard.ID())
	logger.Info(fmt.Sprintf(
		"creating simple maestro client with source ID %s, Maestro Consumer Name %s, REST API Endpoint %s, GRPC API Endpoint %s",
		maestroSourceID, provisionShardMaestroConsumerName, provisionShardMaestroRESTAPIEndpoint, provisionShardMaestroGRPCAPIEndpoint),
	)

	maestroClient, err := maestro.NewSimpleMaestroClient(ctx, provisionShardMaestroRESTAPIEndpoint, provisionShardMaestroGRPCAPIEndpoint, provisionShardMaestroConsumerName, maestroSourceID)

	return maestroClient, err
}

// shouldProcess returns true when the condition associated to the validation does not exist or when it exists but
// it failed to run successfully in a previous attempt.
func (c *maestroShowcaseSyncer) shouldProcess(serviceProviderCluster *api.ServiceProviderCluster) bool {
	return true
}

// listAllMaestroBundlesWithSourceIDAndConsumerName lists all the Maestro Bundles with the given source ID and consumer name.
func (c *maestroShowcaseSyncer) listAndLogAllMaestroBundlesWithSourceIDAndConsumerName(
	ctx context.Context, maestroClient maestro.SimpleMaestroClient) error {
	return c.listAndLogMaestroBundlesWithSourceIDAndConsumerNameWithListOptions(ctx, maestroClient, metav1.ListOptions{})
}

// listAndLogMaestroBundlesBasedOnGVK lists and logs the Maestro Bundles whose ManifestConfigs
// ResourceIdentifier match the given GroupResource. If a Maestro Bundle contains ACM ManifestWorks,
// resources inside those are not considered. Note: workv1.ResourceIdentifier has Group and
// Resource (plural resource name), not Version or Kind; field selector support depends on
// the ManifestWork CRD's selectableFields.
func (c *maestroShowcaseSyncer) listAndLogMaestroBundlesWithSourceIDAndConsumerNameWithManifestConfigsMatchingGR(ctx context.Context, maestroClient maestro.SimpleMaestroClient, gr schema.GroupResource) error {
	// ResourceIdentifier has: Group, Resource, Name, Namespace (no Version or Kind).
	selectors := []string{
		fmt.Sprintf("spec.manifestConfigs.resourceIdentifier.group=%s", gr.Group),
		fmt.Sprintf("spec.manifestConfigs.resourceIdentifier.resource=%s", gr.Resource),
	}

	listOptions := metav1.ListOptions{
		FieldSelector: strings.Join(selectors, ","),
	}

	return c.listAndLogMaestroBundlesWithSourceIDAndConsumerNameWithListOptions(ctx, maestroClient, listOptions)
}

func (c *maestroShowcaseSyncer) listAndLogMaestroBundlesWithSourceIDAndConsumerNameWithListOptions(ctx context.Context, maestroClient maestro.SimpleMaestroClient, listOptions metav1.ListOptions) error {
	logger := utils.LoggerFromContext(ctx)

	// Although this is triggered for every cluster, here we list all the bundles that are owned by the same source ID so
	// they are not filtered to a particular cluster.
	// It should be possible to reconstruct the bundle name by using the same algorithm that CS uses to generate them but
	// it requires knowing the GVK, K8s Name and K8s Namespace of the resource that is put within the bundle. Knowing those
	// in advance is challenging as they do not follow a consistent pattern.
	logger.Info("listing maestro bundles with list options", "listOptions", listOptions)
	maestroBundlesList, err := maestroClient.ListMaestroBundles(ctx, listOptions)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list Maestro Bundles: %w", err))
	}

	err = c.logMaestroBundles(ctx, maestroBundlesList)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to log Maestro Bundles: %w", err))
	}

	logger.Info("done listing maestro bundles")

	return nil
}

func (c *maestroShowcaseSyncer) logMaestroBundles(ctx context.Context, maestroBundlesList *workv1.ManifestWorkList) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("logging maestro bundles")
	for idx, maestroBundle := range maestroBundlesList.Items {
		logger = logger.WithValues("bundleIndex", idx)
		ctx = utils.ContextWithLogger(ctx, logger)
		logger.Info("logging maestro bundle")
		// Redact sensitive data (e.g. Secrets, nested ManifestWorks containing
		// Secrets, status feedback JsonRaw with serialized Secrets) before any
		// logging to prevent leaking credentials or other confidential material.
		redactedBundle, err := RedactManifestWork(&maestroBundle)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to redact ManifestWork %s/%s: %w",
				maestroBundle.GetNamespace(), maestroBundle.GetName(), err))
		}

		// Log Some Maestro Bundle Information
		maestroBundleName := redactedBundle.GetName()
		maestroBundleNamespace := redactedBundle.GetNamespace()
		maestroBundleUID := redactedBundle.GetUID()
		maestroBundleStatus := redactedBundle.Status
		maestroBundleMetaName := redactedBundle.ObjectMeta.Name
		maestroBundleManifestConfigs := redactedBundle.Spec.ManifestConfigs
		logger = logger.WithValues("bundleName", maestroBundleName, "bundleNamespace", maestroBundleNamespace, "bundleUID", maestroBundleUID, "bundleMetaName", maestroBundleMetaName)
		ctx = utils.ContextWithLogger(ctx, logger)
		logger.Info("maestro logging basic information")

		if maestroBundleStatusJSON, err := json.Marshal(maestroBundleStatus); err != nil {
			logger.Error(err, "failed to marshal maestro bundle status")
		} else {
			logger.Info("maestro bundle status", "status", string(maestroBundleStatusJSON))
		}
		if maestroBundleManifestConfigsJSON, err := json.Marshal(maestroBundleManifestConfigs); err != nil {
			logger.Error(err, "failed to marshal maestro bundle manifest configs")
		} else {
			logger.Info("maestro bundle manifest configs", "manifestConfigs", string(maestroBundleManifestConfigsJSON))
		}

		// In CS it shouldn't be possible to end up with an empty list of resources
		// within the Maestro Bundle because in CS we only allow one and only one
		// resource within the Maestro Bundle
		if len(redactedBundle.Spec.Workload.Manifests) == 0 {
			return nil // no Maestro Bundles found, no work to do
		}

		// In CS we only allow one resource within the Maestro Bundle so getting
		// more than one resource is unexpected as of now
		if len(redactedBundle.Spec.Workload.Manifests) > 1 {
			return utils.TrackError(fmt.Errorf("expected exactly one resource withint the Maestro Bundle, got %d", len(maestroBundlesList.Items)))
		}

		// Log the Resource within the Maestro Bundle (already redacted)
		resource := redactedBundle.Spec.Workload.Manifests[0]

		var resourceGVK schema.GroupVersionKind
		if resource.Object != nil {
			resourceGVK = resource.Object.GetObjectKind().GroupVersionKind()
		} else if len(resource.Raw) > 0 {
			if gvk, ok := detectGVKFromRaw(resource.Raw); ok {
				resourceGVK = gvk
			}
		}
		logger.Info("maestro bundle manifest resource gvk", "gvk", resourceGVK)

		resourceJSON, err := resource.MarshalJSON()
		if err != nil {
			logger.Error(err, "failed to marshal manifest resource to JSON")
		} else {
			logger.Info("maestro bundle manifest resource", "bundleManifestResource", string(resourceJSON))
		}
	}

	logger.Info("done logging maestro bundles")

	return nil
}
