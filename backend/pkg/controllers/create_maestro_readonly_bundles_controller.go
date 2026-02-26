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
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	workv1 "open-cluster-management.io/api/work/v1"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// createMaestroReadonlyBundlesSyncer is a controller that creates Maestro readonly bundles for the clusters.
// It is responsible for creating the Maestro readonly bundles and storing a reference to them in Cosmos. It does
// not persist the content of the Maestro bundles themselves. That is the responsibility of the
// readAndPersistMaestroReadonlyBundlesContentSyncer controller.
// As of now we support the creation of a Maestro readonly bundle for the Hypershift's HostedCluster CR associated to
// the Cluster.
type createMaestroReadonlyBundlesSyncer struct {
	cooldownChecker controllerutils.CooldownChecker

	activeOperationLister listers.ActiveOperationLister

	cosmosClient database.DBClient

	clusterServiceClient ocm.ClusterServiceClientSpec

	maestroSourceEnvironmentIdentifier string
}

var _ controllerutils.ClusterSyncer = (*createMaestroReadonlyBundlesSyncer)(nil)

func NewCreateMaestroReadonlyBundlesController(
	activeOperationLister listers.ActiveOperationLister,
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	informers informers.BackendInformers,
	maestroSourceEnvironmentIdentifier string,
) controllerutils.Controller {

	syncer := &createMaestroReadonlyBundlesSyncer{
		cooldownChecker:                    controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		cosmosClient:                       cosmosClient,
		clusterServiceClient:               clusterServiceClient,
		activeOperationLister:              activeOperationLister,
		maestroSourceEnvironmentIdentifier: maestroSourceEnvironmentIdentifier,
	}

	controller := controllerutils.NewClusterWatchingController(
		"CreateMaestroReadonlyBundles",
		cosmosClient,
		informers,
		1*time.Minute,
		syncer,
	)

	return controller
}

func (c *createMaestroReadonlyBundlesSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
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

	// The list of Maestro Bundle internal names that are recognized by the controller.
	// Any Maestro Bundle internal name that is not in this list will not be synced by the
	// controller and reported as an error.
	recognizedMaestroBundles := []api.MaestroBundleInternalName{
		api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster,
	}

	var maestroBundlesToSync []api.MaestroBundleInternalName
	// We first check if there's any recognized Maestro Bundle reference that needs to be synced.
	for _, maestroBundleInternalName := range recognizedMaestroBundles {
		currentMaestroBundleReference, err := existingServiceProviderCluster.Status.MaestroReadonlyBundles.Get(maestroBundleInternalName)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to get Maestro Bundle reference: %w", err))
		}

		if currentMaestroBundleReference == nil {
			maestroBundlesToSync = append(maestroBundlesToSync, maestroBundleInternalName)
			continue
		}
		if currentMaestroBundleReference.MaestroAPIMaestroBundleName == "" {
			maestroBundlesToSync = append(maestroBundlesToSync, maestroBundleInternalName)
			continue
		}
		if currentMaestroBundleReference.MaestroAPIMaestroBundleID == "" {
			maestroBundlesToSync = append(maestroBundlesToSync, maestroBundleInternalName)
			continue
		}
	}
	if len(maestroBundlesToSync) == 0 {
		return nil
	}

	serviceProviderClustersDBClient := c.cosmosClient.ServiceProviderClusters(
		key.SubscriptionID,
		key.ResourceGroupName,
		key.HCPClusterName,
	)

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

	csCluster, err := c.clusterServiceClient.GetCluster(ctx, existingCluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster from Cluster Service: %w", err))
	}
	csClusterDomainPrefix := csCluster.DomainPrefix()

	// We sync the Maestro Bundles that need to be synced.
	// We pass the latest existingServiceProviderCluster into each iteration and use the returned
	// updated SPC for the next, so that multiple bundles see persisted updates from previous iterations.
	// We always apply updatedSPC (even on error) so in-memory state stays in sync with Cosmos
	// when syncMaestroBundle persisted a partial change before failing.
	var syncErrors []error
	for _, maestroBundleInternalName := range maestroBundlesToSync {
		updatedSPC, syncErr := c.syncMaestroBundle(
			ctx, maestroBundleInternalName, existingServiceProviderCluster, existingCluster, maestroClient,
			serviceProviderClustersDBClient, clusterProvisionShard, csClusterDomainPrefix,
		)
		existingServiceProviderCluster = updatedSPC
		if syncErr != nil {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to sync Maestro Bundle %q: %w", maestroBundleInternalName, syncErr)))
		}
	}

	return utils.TrackError(errors.Join(syncErrors...))
}

// syncMaestroBundle ensures the given Maestro bundle exists in Maestro, as well as a reference to it in ServiceProviderCluster.
// It returns the updated ServiceProviderCluster (after any Replace calls) so the caller can pass it into the next sync.
// On error, the first return value is always the lastest persisted ServiceProviderClass SPC, so the
// caller can keep in-memory state in sync and subsequent bundle syncs in the same run never see stale data.
func (c *createMaestroReadonlyBundlesSyncer) syncMaestroBundle(
	ctx context.Context,
	maestroBundleInternalName api.MaestroBundleInternalName,
	existingServiceProviderCluster *api.ServiceProviderCluster,
	existingCluster *api.HCPOpenShiftCluster,
	maestroClient maestro.Client,
	serviceProviderClustersDBClient database.ServiceProviderClusterCRUD,
	clusterProvisionShard *arohcpv1alpha1.ProvisionShard,
	csClusterDomainPrefix string,
) (*api.ServiceProviderCluster, error) {
	lastPersistedSPC := existingServiceProviderCluster

	existingMaestroBundleRef, err := existingServiceProviderCluster.Status.MaestroReadonlyBundles.Get(maestroBundleInternalName)
	if err != nil {
		return lastPersistedSPC, utils.TrackError(fmt.Errorf("failed to get Maestro Bundle reference: %w", err))
	}
	// If the Maestro Bundle reference does not exist, we create a new Maestro Bundle
	// reference for the Maestro API Maestro Bundle name. When this occurs we also immediately
	// store the content in Cosmos. This ensures that we have the name reserved for it
	// and it makes it resistant to crashes/reboots.
	if existingMaestroBundleRef == nil {
		var err error
		existingMaestroBundleRef, err = c.buildInitialMaestroBundleReference(maestroBundleInternalName)
		if err != nil {
			return lastPersistedSPC, utils.TrackError(fmt.Errorf("failed to build initial Maestro Bundle reference: %w", err))
		}
		err = existingServiceProviderCluster.Status.MaestroReadonlyBundles.Set(existingMaestroBundleRef)
		if err != nil {
			return lastPersistedSPC, utils.TrackError(fmt.Errorf("failed to set internal Maestro Bundle reference: %w", err))
		}
		existingServiceProviderCluster, err = serviceProviderClustersDBClient.Replace(ctx, existingServiceProviderCluster, nil)
		if err != nil {
			return lastPersistedSPC, utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster in database: %w", err))
		}
		lastPersistedSPC = existingServiceProviderCluster
		existingMaestroBundleRef, err = existingServiceProviderCluster.Status.MaestroReadonlyBundles.Get(maestroBundleInternalName)
		if err != nil {
			return lastPersistedSPC, utils.TrackError(fmt.Errorf("failed to get Maestro Bundle reference: %w", err))
		}
		if existingMaestroBundleRef == nil {
			return lastPersistedSPC, utils.TrackError(fmt.Errorf("maestro Bundle reference %q not found in ServiceProviderCluster", maestroBundleInternalName))
		}
	}

	// We ensure that the Maestro Bundle exists using the Maestro API
	maestroBundleNamespacedName := types.NamespacedName{
		Name:      existingMaestroBundleRef.MaestroAPIMaestroBundleName,
		Namespace: clusterProvisionShard.MaestroConfig().ConsumerName(),
	}

	var desiredMaestroBundle *workv1.ManifestWork
	switch maestroBundleInternalName {
	case api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster:
		desiredMaestroBundle = c.buildInitialReadonlyMaestroBundleForHostedCluster(existingCluster, csClusterDomainPrefix, maestroBundleNamespacedName)
	default:
		return lastPersistedSPC, utils.TrackError(fmt.Errorf("unrecognized Maestro Bundle internal name: %s", maestroBundleInternalName))
	}

	resultMaestroBundle, err := c.getOrCreateMaestroBundle(ctx, maestroClient, desiredMaestroBundle)
	if err != nil {
		return lastPersistedSPC, utils.TrackError(fmt.Errorf("failed to get or create Maestro Bundle: %w", err))
	}

	// If the Maestro API MaestroBundle ID is not set we store the returned Maestro Bundle ID in the corresponding Maestro Bundle reference of the ServiceProviderCluster in Cosmos.
	if existingMaestroBundleRef.MaestroAPIMaestroBundleID == "" {
		bundleID := string(resultMaestroBundle.UID)
		existingMaestroBundleRef.MaestroAPIMaestroBundleID = bundleID
		err = existingServiceProviderCluster.Status.MaestroReadonlyBundles.Set(existingMaestroBundleRef)
		if err != nil {
			return lastPersistedSPC, utils.TrackError(fmt.Errorf("failed to set Maestro Bundle reference: %w", err))
		}
		existingServiceProviderCluster, err = serviceProviderClustersDBClient.Replace(ctx, existingServiceProviderCluster, nil)
		if err != nil {
			return lastPersistedSPC, utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster in database: %w", err))
		}
		lastPersistedSPC = existingServiceProviderCluster
	}

	return lastPersistedSPC, nil
}

// buildClusterEmptyHostedCluster returns an empty hosted cluster representing the Cluster's Hypershift HostedCluster resource.
// It strictly contains the type information and the object meta information necessary to identify the resource in the management cluster.
// It can be used to provide as the input of a Maestro resource bundle.
func (c *createMaestroReadonlyBundlesSyncer) buildClusterEmptyHostedCluster(csClusterID string, csClusterDomainPrefix string) *hsv1beta1.HostedCluster {
	// TODO To calculate the HostedCluster namespace we pass the maestro source ID because it turns out to have the same
	// value as the envName in CS. This is not accurate but it is good enough.
	// I would decouple what is the maestro source ID envname part from the envname. The reason being that they are
	// conceptually different things, they just happen to have the same value for the envName part.
	// I am hesitant to provide a generic "environment name" deployment parameter to backend because people might introduce conditional logic based
	// on the environment name which is fragile. The options I see are:
	// * Provide a deployment parameter to backend that is named something concrete like "k8s-names-calculations-env-name" or similar to indicate
	//   that is something that is used to calculate names/namespaces of some k8s resources.
	// * Expose in the CS API Cluster payload the "CDNamespace" associated to the cluster and start storing it in cosmos. This would allow to fully
	//   decouple from this concept of CDNamespace and we would use the stored value when needed. However, if we want to
	//   create resources in the same namespace as the old ones then we would still need to keep forever the concept of "env name part used to calculate
	//   some k8s resource names/namespaces".
	hostedClusterNamespace := c.getHostedClusterNamespace(c.maestroSourceEnvironmentIdentifier, csClusterID)
	hostedClusterName := csClusterDomainPrefix

	// We first build the resource (manifest) that we want to put within the Maestro Bundle.
	// The resource is empty and it only has the type information and the object meta
	// information necessary to identify the resource in the management cluster.
	hostedCluster := &hsv1beta1.HostedCluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       "HostedCluster",
			APIVersion: hsv1beta1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      hostedClusterName,
			Namespace: hostedClusterNamespace,
		},
	}

	return hostedCluster
}

// buildInitialReadonlyMaestroBundleForHostedCluster builds an initial readonly Maestro Bundle for the Cluster's Hypershift HostedCluster.
// Used to create the readonly Maestro bundle associated to it.
func (c *createMaestroReadonlyBundlesSyncer) buildInitialReadonlyMaestroBundleForHostedCluster(cluster *api.HCPOpenShiftCluster, csClusterDomainPrefix string, maestroBundleNamespacedName types.NamespacedName) *workv1.ManifestWork {
	hostedCluster := c.buildClusterEmptyHostedCluster(cluster.ServiceProviderProperties.ClusterServiceID.ID(), csClusterDomainPrefix)
	maestroBundleResourceIdentifier := workv1.ResourceIdentifier{
		Group:     hsv1beta1.SchemeGroupVersion.Group,
		Resource:  "hostedclusters",
		Name:      hostedCluster.Name,
		Namespace: hostedCluster.Namespace,
	}

	return c.buildInitialReadonlyMaestroBundle(maestroBundleNamespacedName, maestroBundleResourceIdentifier, hostedCluster)
}

// buildInitialReadonlyMaestroBundle builds an initial readonly Maestro Bundle for a given resource specified in obj.
// objResourceIdentifier is the resource identifier of the resource specified in obj.
// maestroBundleNamespacedName is the namespaced name of the Maestro Bundle.
// Used to create the readonly Maestro bundle associated to the resource specified in obj.
func (c *createMaestroReadonlyBundlesSyncer) buildInitialReadonlyMaestroBundle(maestroBundleNamespacedName types.NamespacedName, objResourceIdentifier workv1.ResourceIdentifier, obj runtime.Object) *workv1.ManifestWork {
	maestroBundleObjMeta := metav1.ObjectMeta{
		Name:            maestroBundleNamespacedName.Name,
		Namespace:       maestroBundleNamespacedName.Namespace,
		ResourceVersion: "0", // TODO is this needed when creating a maestro bundle?
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
func (c *createMaestroReadonlyBundlesSyncer) buildInitialMaestroBundleReference(internalName api.MaestroBundleInternalName) (*api.MaestroBundleReference, error) {
	maestroAPIMaestroBundleName, err := c.generateNewMaestroAPIMaestroBundleName()
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to generate Maestro API Maestro Bundle name: %w", err))
	}
	hostedClusterMWMaestroBundleReference := &api.MaestroBundleReference{
		Name:                        internalName,
		MaestroAPIMaestroBundleName: maestroAPIMaestroBundleName,
		MaestroAPIMaestroBundleID:   "",
	}

	return hostedClusterMWMaestroBundleReference, nil
}

// generateNewMaestroAPIMaestroBundleName generates a new Maestro API Maestro Bundle name.
// Used to generate a new Maestro API Maestro Bundle name for a new Maestro Bundle reference.
// The generated name is a UUIDv4.
func (c *createMaestroReadonlyBundlesSyncer) generateNewMaestroAPIMaestroBundleName() (string, error) {
	newUUIDForMaestroAPIMaestroBundleName, err := uuid.NewRandom()
	if err != nil {
		return "", utils.TrackError(fmt.Errorf("failed to generate UUIDv4 for Maestro API Maestro Bundle name: %w", err))
	}
	return newUUIDForMaestroAPIMaestroBundleName.String(), nil
}

// getHostedClusterNamespace gets the namespace for the hosted cluster based on the environment name and the cluster service ID
// The namespace is of the format ocm-<envName>-<csClusterID>. This is how CS calculates Hypershift's HostedCluster namespace.
// Internally in CS this is the "CDNamespace" attribute associated to the cluster.
func (c *createMaestroReadonlyBundlesSyncer) getHostedClusterNamespace(envName string, csClusterID string) string {
	return fmt.Sprintf("ocm-%s-%s", envName, csClusterID)
}

// getOrCreateMaestroBundle gets (or creates if it does not exist) a Maestro Bundle for a given Maestro Bundle namespaced name.
func (c *createMaestroReadonlyBundlesSyncer) getOrCreateMaestroBundle(ctx context.Context, maestroClient maestro.Client, maestroBundle *workv1.ManifestWork) (*workv1.ManifestWork, error) {
	logger := utils.LoggerFromContext(ctx)
	existingMaestroBundle, err := maestroClient.Get(ctx, maestroBundle.Name, metav1.GetOptions{})
	if err == nil {
		logger.Info(fmt.Sprintf("retrieved maestro bundle name %s with resource name %s", maestroBundle.Name, maestroBundle.Spec.ManifestConfigs[0].ResourceIdentifier.Name))
		return existingMaestroBundle, nil
	}
	if !k8serrors.IsNotFound(err) {
		logger.Error(err, "failed to get Maestro Bundle and it is not already exists error")
		return nil, utils.TrackError(fmt.Errorf("failed to get Maestro Bundle: %w", err))
	}

	logger.Info(fmt.Sprintf("attempting to create maestro bundle name %s with resource name %s", maestroBundle.Name, maestroBundle.Spec.ManifestConfigs[0].ResourceIdentifier.Name))
	existingMaestroBundle, err = maestroClient.Create(ctx, maestroBundle, metav1.CreateOptions{})
	if err == nil {
		logger.Info(fmt.Sprintf("created maestro bundle name %s with resource name %s", maestroBundle.Name, maestroBundle.Spec.ManifestConfigs[0].ResourceIdentifier.Name))
		return existingMaestroBundle, nil
	}
	if !k8serrors.IsAlreadyExists(err) {
		logger.Error(err, "failed to create Maestro Bundle and it is not already exists error")
		return nil, utils.TrackError(fmt.Errorf("failed to create Maestro Bundle: %w", err))
	}
	logger.Error(err, "failed to create Maestro Bundle because it returned already exists error. Attempting to get it again")
	existingMaestroBundle, err = maestroClient.Get(ctx, maestroBundle.Name, metav1.GetOptions{})
	return existingMaestroBundle, err
}

func (c *createMaestroReadonlyBundlesSyncer) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}

// createMaestroClientFromProvisionShard creates a Maestro client for the given provision shard.
// The client is scoped to the Maestro Consumer associated to the provision shard, as well
// as to the the Maestro Source ID associated to the provision shard which is calculated from the provision shard ID and the
// environment specified in c.maestroSourceEnvironmentIdentifier.
func (c *createMaestroReadonlyBundlesSyncer) createMaestroClientFromProvisionShard(
	ctx context.Context, provisionShard *arohcpv1alpha1.ProvisionShard,
) (maestro.Client, error) {
	provisionShardMaestroConsumerName := provisionShard.MaestroConfig().ConsumerName()
	provisionShardMaestroRESTAPIEndpoint := provisionShard.MaestroConfig().RestApiConfig().Url()
	provisionShardMaestroGRPCAPIEndpoint := provisionShard.MaestroConfig().GrpcApiConfig().Url()
	// This allows us to be able to have visibility on the Maestro Bundles owned by the same source ID for a given
	// provision shard and environment. This should have the same source ID as what CS has in each corresponding environment
	// because otherwise we would not have visibility on the Maestro Bundles owned
	// TODO do we want to use the same source ID that CS uses or do we want intentionally a different one? This has consequences
	// on the visibility of the Maestro Bundles, including processing of events sent by Maestro.
	maestroSourceID := maestro.GenerateMaestroSourceID(c.maestroSourceEnvironmentIdentifier, provisionShard.ID())

	maestroClient, err := maestro.NewClient(ctx, provisionShardMaestroRESTAPIEndpoint, provisionShardMaestroGRPCAPIEndpoint, provisionShardMaestroConsumerName, maestroSourceID)

	return maestroClient, err
}
