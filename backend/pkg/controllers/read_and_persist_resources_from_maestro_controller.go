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
	"time"

	"github.com/google/uuid"
	workv1 "open-cluster-management.io/api/work/v1"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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
)

type readAndPersistResourcesFromMaestroSyncer struct {
	cooldownChecker controllerutils.CooldownChecker

	activeOperationLister listers.ActiveOperationLister

	cosmosClient database.DBClient

	clusterServiceClient ocm.ClusterServiceClientSpec

	maestroSourceEnvironmentIdentifier string
}

var _ controllerutils.ClusterSyncer = (*readAndPersistResourcesFromMaestroSyncer)(nil)

func NewReadAndPersistResourcesFromMaestroController(
	activeOperationLister listers.ActiveOperationLister,
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	clusterInformer cache.SharedIndexInformer,
	maestroSourceEnvironmentIdentifier string,
) controllerutils.Controller {

	syncer := &readAndPersistResourcesFromMaestroSyncer{
		cooldownChecker:                    controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		cosmosClient:                       cosmosClient,
		clusterServiceClient:               clusterServiceClient,
		activeOperationLister:              activeOperationLister,
		maestroSourceEnvironmentIdentifier: maestroSourceEnvironmentIdentifier,
	}

	controller := controllerutils.NewClusterWatchingController(
		"ReadAndPersistResourcesFromMaestro",
		cosmosClient,
		clusterInformer,
		1*time.Minute,
		syncer,
	)

	return controller
}

func (c *readAndPersistResourcesFromMaestroSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
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

	maestroClient, err := c.createSimpleMaestroClient(ctx, clusterProvisionShard)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create Simple Maestro client: %w", err))
	}

	csCluster, err := c.clusterServiceClient.GetCluster(ctx, existingCluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster from Cluster Service: %w", err))
	}
	csClusterDomainPrefix := csCluster.DomainPrefix()

	// TODO this has been intentionally commented out because when we store the HostedCluster ManifestWork that contains K8s Secrets so it would
	// be potentially logging in the CI run logs so we can't enable it as of now until we stop sending those (in the ARO-HCP project backlog but
	// not tacklet yet).
	// err = c.readAndPersistHostedClusterManifestWork(ctx, maestroClient, clusterProvisionShard.MaestroConfig().ConsumerName(), existingServiceProviderCluster, serviceProviderClustersDBClient, existingCluster)
	// if err != nil {
	// 	return utils.TrackError(fmt.Errorf("failed to read and persist hosted cluster manifest work: %w", err))
	// }

	// TODO the retrieval of the CS Cluster Domain Prefix by querying the CS API is a hack because we just found out
	// that it is not being persisted in the corresponding Cluster Cosmos entry when CS applies defaulting to
	// it even though it is returned by CS in the initial cluster create response.
	err = c.readAndPersistHostedCluster(ctx, maestroClient, clusterProvisionShard.MaestroConfig().ConsumerName(), existingServiceProviderCluster, serviceProviderClustersDBClient, existingCluster, csClusterDomainPrefix)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to read and persist hosted cluster: %w", err))
	}

	return nil
}

// buildClusterEmptyHostedCluster returns an empty hosted cluster representing the Cluster's Hypershift HostedCluster resource.
// It strictly contains the type information and the object meta information necessary to identify the resource in the management cluster.
// It can be used to provide as the input of a Maestro resource bundle.
func (c *readAndPersistResourcesFromMaestroSyncer) buildClusterEmptyHostedCluster(csClusterID string, csClusterDomainPrefix string) *hsv1beta1.HostedCluster {
	// TODO To calculate the HostedCluster namespace we pass the maestro source ID because it turns out to have the same
	// value as the envName in CS. This is not correct but works to showcase. I would decouple what is the maestro source ID envname part
	// from the envname. The reason being that they are conceptually different things, they just happen to have the same value for the envName part.
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

// buildClusterEmptyHostedClusterManifestWork returns an empty hosted cluster manifest work representing the Cluster's Hypershift HostedClusterManifestWork resource.
// It strictly contains the type information and the object meta information necessary to identify the resource in the management cluster.
// It can be used to provide as the input of a Maestro resource bundle.
// nolint:unused
func (c *readAndPersistResourcesFromMaestroSyncer) buildClusterEmptyHostedClusterManifestWork(cluster *api.HCPOpenShiftCluster) *workv1.ManifestWork {
	// We first build the resource (manifest) that we want to put within the Maestro Bundle.
	// The resource is empty and it only has the type information and the object meta
	// information necessary to identify the resource in the management cluster.
	hostedClusterManifestWork := &workv1.ManifestWork{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ManifestWork",
			APIVersion: workv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cluster.ServiceProviderProperties.ClusterServiceID.ID(),
			Namespace: "local-cluster",
		},
	}

	return hostedClusterManifestWork
}

// buildInitialReadonlyMaestroBundleForHostedCluster builds an initial readonly Maestro Bundle for the Cluster's Hypershift HostedCluster.
// Used to create the readonly Maestro bundle associated to it.
func (c *readAndPersistResourcesFromMaestroSyncer) buildInitialReadonlyMaestroBundleForHostedCluster(cluster *api.HCPOpenShiftCluster, csClusterDomainPrefix string, maestroBundleNamespacedName types.NamespacedName) *workv1.ManifestWork {
	hostedCluster := c.buildClusterEmptyHostedCluster(cluster.ServiceProviderProperties.ClusterServiceID.ID(), csClusterDomainPrefix)
	maestroBundleResourceIdentifier := workv1.ResourceIdentifier{
		Group:     hsv1beta1.SchemeGroupVersion.Group,
		Resource:  "hostedclusters",
		Name:      hostedCluster.Name,
		Namespace: hostedCluster.Namespace,
	}

	return c.buildInitialReadonlyMaestroBundle(maestroBundleNamespacedName, maestroBundleResourceIdentifier, hostedCluster)
}

// buildInitialReadonlyMaestroBundleForHostedClusterManifestWork builds an initial readonly Maestro Bundle for the Cluster's HostedCluster ManifestWork.
// Used to create the readonly Maestro bundle associated to it.
// nolint:unused
func (c *readAndPersistResourcesFromMaestroSyncer) buildInitialReadonlyMaestroBundleForHostedClusterManifestWork(cluster *api.HCPOpenShiftCluster, maestroBundleNamespacedName types.NamespacedName) *workv1.ManifestWork {
	hostedClusterManifestWork := c.buildClusterEmptyHostedClusterManifestWork(cluster)
	maestroBundleResourceIdentifier := workv1.ResourceIdentifier{
		Group:     workv1.SchemeGroupVersion.Group,
		Resource:  "manifestworks",
		Name:      hostedClusterManifestWork.Name,
		Namespace: hostedClusterManifestWork.Namespace,
	}

	return c.buildInitialReadonlyMaestroBundle(maestroBundleNamespacedName, maestroBundleResourceIdentifier, hostedClusterManifestWork)
}

// buildInitialReadonlyMaestroBundle builds an initial readonly Maestro Bundle for a given resource specified in obj.
// objResourceIdentifier is the resource identifier of the resource specified in obj.
// maestroBundleNamespacedName is the namespaced name of the Maestro Bundle.
// Used to create the readonly Maestro bundle associated to the resource specified in obj.
func (c *readAndPersistResourcesFromMaestroSyncer) buildInitialReadonlyMaestroBundle(maestroBundleNamespacedName types.NamespacedName, objResourceIdentifier workv1.ResourceIdentifier, obj runtime.Object) *workv1.ManifestWork {
	maestroBundleObjMeta := metav1.ObjectMeta{
		Name:            maestroBundleNamespacedName.Name,
		Namespace:       maestroBundleNamespacedName.Namespace,
		ResourceVersion: "0", // TODO is this needed when creating a maestro bundle?
	}

	// We then build the Maestro Bundle that will contain the resource.
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
							// We put the resource (manifest) that we previously built above.
							// In Maestro only the desired spec can be retrieved from here when
							// later doing queries to Maestro.
							// To retrieve another section other than the desired spec Maestro
							// requires defining FeedbackRule(s) in the Maestro bundle.
							// For maestro readonly resources, not even the desired spec can be retrieved. For
							// those type of resources it needs to be retrieved via status feedback rule(s) too
							// For owned resources, here the desired spec can be retrieved but that
							// is not necessarily the actual spec in the management cluster side. If that is
							// desired it is again necessary to get the spec via feedbackrules
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
					// creates a "read only maestro bundle".
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

// readAndPersistHostedCluster reads the Cluster's Hypershift HostedCluster resource from the management cluster and persists it in Cosmos.
// To achieve that, it gets (or creates if it does not exist) the Maestro readonly bundle pointing to the Cluster's HostedCluster, it extracts the
// returned content by Maestro by taking it from the Maestro bundles's status feedback rule that contains the whole object and then it persists it
// in Cosmos.
func (c *readAndPersistResourcesFromMaestroSyncer) readAndPersistHostedCluster(
	ctx context.Context, maestroClient maestro.SimpleMaestroClient, maestroConsumerName string,
	serviceProviderCluster *api.ServiceProviderCluster, serviceProviderClustersDBClient database.ServiceProviderClusterCRUD,
	cluster *api.HCPOpenShiftCluster, csClusterDomainPrefix string,
) error {

	hostedClusterMaestroBundleInternalName := api.MaestroBundleInternalNameHypershiftHostedCluster
	hostedClusterMaestroBundleReference := serviceProviderCluster.MaestroReadonlyBundles.Get(hostedClusterMaestroBundleInternalName)
	// If the Maestro Bundle reference does not exist, we create a new Maestro Bundle
	// reference for the Maestro API Maestro Bundle name.
	// When this occurs we also store the content in Cosmos. This ensures that we have
	// the name reserved for it and it makes it resistant to crashes/reboots.
	// TODO do we need to consider collisions? UUIDv4 has very small chance but it could
	// technically happen that you end up with two entries with the same name in Cosmos.
	if hostedClusterMaestroBundleReference == nil {
		var err error
		hostedClusterMaestroBundleReference, err = c.buildInitialMaestroBundleReference(hostedClusterMaestroBundleInternalName)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to build initial Maestro Bundle reference: %w", err))
		}
		serviceProviderCluster.MaestroReadonlyBundles = append(serviceProviderCluster.MaestroReadonlyBundles, *hostedClusterMaestroBundleReference)
		_, err = serviceProviderClustersDBClient.Replace(ctx, serviceProviderCluster, nil)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster in database: %w", err))
		}
	}

	maestroBundleNamespacedName := types.NamespacedName{
		Name:      hostedClusterMaestroBundleReference.MaestroAPIMaestroBundleName,
		Namespace: maestroConsumerName,
	}
	maestroBundle := c.buildInitialReadonlyMaestroBundleForHostedCluster(cluster, csClusterDomainPrefix, maestroBundleNamespacedName)
	resultMaestroBundle, err := c.getOrCreateMaestroBundle(ctx, maestroClient, maestroBundle)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create Maestro Bundle: %w", err))
	}

	rawBytes, err := c.getSingleResourceStatusFeedbackRawJSONFromMaestroBundle(resultMaestroBundle)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get single resource status feedback raw JSON from Maestro Bundle: %w", err))
	}
	hostedCluster := &hsv1beta1.HostedCluster{}
	err = json.Unmarshal(rawBytes, hostedCluster)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to unmarshal hosted cluster from status feedback value: %w", err))
	}

	managementClusterContentsClient := c.cosmosClient.ManagementClusterContents(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name)
	managementClusterContent, err := controllerutils.GetOrCreateManagementClusterContent(ctx, c.cosmosClient, cluster.ID, hostedClusterMaestroBundleInternalName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ManagementClusterContent: %w", err))
	}
	contentJSON, err := json.Marshal(hostedCluster)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to marshal hosted cluster for ManagementClusterContent: %w", err))
	}
	managementClusterContent.Content = contentJSON
	_, err = managementClusterContentsClient.Replace(ctx, managementClusterContent, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ManagementClusterContent: %w", err))
	}

	return nil
}

// buildInitialMaestroBundleReference builds an initial Maestro Bundle reference for a given maestro bundle internal name.
func (c *readAndPersistResourcesFromMaestroSyncer) buildInitialMaestroBundleReference(internalName api.MaestroBundleInternalName) (*api.MaestroBundleReference, error) {
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
func (c *readAndPersistResourcesFromMaestroSyncer) generateNewMaestroAPIMaestroBundleName() (string, error) {
	newUUIDForMaestroAPIMaestroBundleName, err := uuid.NewRandom()
	if err != nil {
		return "", utils.TrackError(fmt.Errorf("failed to generate UUIDv4 for Maestro API Maestro Bundle name: %w", err))
	}
	return newUUIDForMaestroAPIMaestroBundleName.String(), nil
}

// readAndPersistHostedClusterManifestWork reads the Cluster's Hypershift HostedCluster ManifestWork resource from the management cluster and persists it in Cosmos.
// To achieve that, it gets (or creates if it does not exist) the Maestro readonly bundle pointing to the Cluster's HostedCluster ManifestWork, it extracts the
// returned content by Maestro by taking it from the Maestro bundles's status feedback rule that contains the whole object and then it persists it
// in Cosmos.
// nolint:unused
func (c *readAndPersistResourcesFromMaestroSyncer) readAndPersistHostedClusterManifestWork(
	ctx context.Context, maestroClient maestro.SimpleMaestroClient, maestroConsumerName string,
	serviceProviderCluster *api.ServiceProviderCluster, serviceProviderClustersDBClient database.ServiceProviderClusterCRUD,
	cluster *api.HCPOpenShiftCluster,
) error {

	hostedClusterMWMaestroBundleInternalName := api.MaestroBundleInternalNameHypershiftHostedClusterManifestWork
	hostedClusterMWMaestroBundleReference := serviceProviderCluster.MaestroReadonlyBundles.Get(hostedClusterMWMaestroBundleInternalName)
	// If the Maestro Bundle reference does not exist, we create a new Maestro Bundle
	// reference generating a new UUIDv4 for the Maestro API Maestro Bundle name.
	// When this occurs we also store the content in Cosmos. This ensures that we have
	// the name reserved for it and it makes it resistant to crashes/reboots.
	// TODO do we need to consider collisions? UUIDv4 has very small chance but it could
	// technically happen that you end up with two entries with the same name in Cosmos.
	if hostedClusterMWMaestroBundleReference == nil {
		hostedClusterMWMaestroBundleReference, err := c.buildInitialMaestroBundleReference(hostedClusterMWMaestroBundleInternalName)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to build initial Maestro Bundle reference: %w", err))
		}
		serviceProviderCluster.MaestroReadonlyBundles = append(serviceProviderCluster.MaestroReadonlyBundles, *hostedClusterMWMaestroBundleReference)
		_, err = serviceProviderClustersDBClient.Replace(ctx, serviceProviderCluster, nil)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster in database: %w", err))
		}
	}

	// TODO this is a very naive approach where we create/get the Maestro Bundle, extract the content from the StatusFeedback and
	// persist it in Cosmos. The reason this is very naive is because when you create the bundle you don't have the rsource as it takes
	// time to propagate to Maestro, the management cluster and for maestro to fill it back with the feedback. This means that as of now
	// the controller throws errors because of expected elements not being there (feedback not set yet, etc.) until it eventually ends up having it.
	// It also doesn't consider that the data could not be fresh, nor that something could go wrong with the Maestro Bundle creation or feedback reporting
	// from Maestro side. It doesn't check any kind of K8s condition either.
	// All of this generates at least temporary errors and logs that could confuse developers.
	maestroBundleNamespacedName := types.NamespacedName{
		Name:      hostedClusterMWMaestroBundleReference.MaestroAPIMaestroBundleName,
		Namespace: maestroConsumerName,
	}
	maestroBundle := c.buildInitialReadonlyMaestroBundleForHostedClusterManifestWork(cluster, maestroBundleNamespacedName)
	resultMaestroBundle, err := c.getOrCreateMaestroBundle(ctx, maestroClient, maestroBundle)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create Maestro Bundle: %w", err))
	}

	rawBytes, err := c.getSingleResourceStatusFeedbackRawJSONFromMaestroBundle(resultMaestroBundle)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get single resource status feedback raw JSON from Maestro Bundle: %w", err))
	}
	hostedClusterManifestWork := &workv1.ManifestWork{}
	err = json.Unmarshal(rawBytes, hostedClusterManifestWork)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to unmarshal hosted cluster manifest work from status feedback value: %w", err))
	}

	managementClusterContentsClient := c.cosmosClient.ManagementClusterContents(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name)
	managementClusterContent, err := controllerutils.GetOrCreateManagementClusterContent(ctx, c.cosmosClient, cluster.ID, hostedClusterMWMaestroBundleInternalName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ManagementClusterContent: %w", err))
	}
	contentJSON, err := json.Marshal(hostedClusterManifestWork)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to marshal hosted cluster manifest work for ManagementClusterContent: %w", err))
	}
	managementClusterContent.Content = contentJSON
	_, err = managementClusterContentsClient.Replace(ctx, managementClusterContent, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ManagementClusterContent: %w", err))
	}

	return nil
}

// getHostedClusterNamespace gets the namespace for the hosted cluster based on the environment name and the cluster service ID
// The namespace is of the format ocm-<envName>-<csClusterID>. This is how CS calculates Hypershift's HostedCluster namespace.
// Internally in CS this is the "CDNamespace" attribute associated to the cluster.
func (c *readAndPersistResourcesFromMaestroSyncer) getHostedClusterNamespace(envName string, csClusterID string) string {
	return fmt.Sprintf("ocm-%s-%s", envName, csClusterID)
}

// getOrCreateMaestroBundle gets (or creates if it does not exist) a Maestro Bundle for a given Maestro Bundle namespaced name.
func (c *readAndPersistResourcesFromMaestroSyncer) getOrCreateMaestroBundle(ctx context.Context, maestroClient maestro.SimpleMaestroClient, maestroBundle *workv1.ManifestWork) (*workv1.ManifestWork, error) {
	logger := utils.LoggerFromContext(ctx)
	existingMaestroBundle, err := maestroClient.GetMaestroBundle(ctx, maestroBundle.Name, metav1.GetOptions{})
	if err == nil {
		logger.Info(fmt.Sprintf("retrieved maestro bundle name %s with resource name %s", maestroBundle.Name, maestroBundle.Spec.ManifestConfigs[0].ResourceIdentifier.Name))
		return existingMaestroBundle, nil
	}
	if !errors.IsNotFound(err) {
		logger.Error(err, "failed to get Maestro Bundle and it is not already exists error")
		return nil, utils.TrackError(fmt.Errorf("failed to get Maestro Bundle: %w", err))
	}

	logger.Info(fmt.Sprintf("attempting to create maestro bundle name %s with resource name %s", maestroBundle.Name, maestroBundle.Spec.ManifestConfigs[0].ResourceIdentifier.Name))
	existingMaestroBundle, err = maestroClient.CreateMaestroBundle(ctx, maestroBundle, metav1.CreateOptions{})
	if err == nil {
		logger.Info("created maestro bundle name %s with resource name %s", maestroBundle.Name, maestroBundle.Spec.ManifestConfigs[0].ResourceIdentifier.Name)
		return existingMaestroBundle, nil
	}
	if !errors.IsAlreadyExists(err) {
		logger.Error(err, "failed to create Maestro Bundle and it is not already exists error")
		return nil, utils.TrackError(fmt.Errorf("failed to create Maestro Bundle: %w", err))
	}
	logger.Error(err, "failed to create Maestro Bundle because it returned already exists error. Attempting to get it again")
	existingMaestroBundle, err = maestroClient.GetMaestroBundle(ctx, maestroBundle.Name, metav1.GetOptions{})
	return existingMaestroBundle, err
}

// getSingleResourceStatusFeedbackRawJSONFromMaestroBundle gets the single resource status feedback raw JSON from a Maestro Bundle.
// Used to extract the content of the resource from the Maestro Bundle.
// An error is returned if the Maestro Bundle does not contain a single resource or if the resource does not contain a single status feedback value
// with its name being "resource" and its type being JsonRaw.
func (c *readAndPersistResourcesFromMaestroSyncer) getSingleResourceStatusFeedbackRawJSONFromMaestroBundle(maestroBundle *workv1.ManifestWork) (json.RawMessage, error) {
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

func (c *readAndPersistResourcesFromMaestroSyncer) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}

// createSimpleMaestroClient creates a Simple Maestro client for the given cluster provision shard.
// the client is scoped to the Consumer Name associated to the provision shard, and to
// the source ID associated to the provision shard and the environment specified
// in c.maestroSourceEnvironmentIdentifier, which is a configuration parameter at
// deployment time.
func (c *readAndPersistResourcesFromMaestroSyncer) createSimpleMaestroClient(
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
