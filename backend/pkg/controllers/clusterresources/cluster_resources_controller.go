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
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	ocmv1 "open-cluster-management.io/api/work/v1"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/restmapper"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	ClusterResourcesControllerName = "ClusterResources"
)

// clusterResourcesController polls the Cluster Service SDK endpoint for cluster resources information
type clusterResourcesController struct {
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	nodePoolLister               corelisters.NodePoolLister
	clustersServiceClient        ocm.ClusterServiceClientSpec
	kubeApplierDBClients         kubeappliercosmosstorage.KubeApplierDBClients
	applyDesireLister            kubeapplierlisters.ApplyDesireLister
}

var _ controllerutils.ClusterSyncer = (*clusterResourcesController)(nil)

func NewClusterResourcesController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	clustersServiceClient ocm.ClusterServiceClientSpec,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()
	_, nodePoolLister := informers.NodePools()
	_, applyDesireLister := kubeApplierInformers.ApplyDesires()

	syncer := &clusterResourcesController{
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		nodePoolLister:               nodePoolLister,
		clustersServiceClient:        clustersServiceClient,
		kubeApplierDBClients:         kubeApplierDBClients,
		applyDesireLister:            applyDesireLister,
	}

	return controllerutils.NewClusterWatchingController(
		ClusterResourcesControllerName,
		resourcesDBClient,
		informers,
		nil,
		30*time.Second, // Poll every 30 seconds.
		syncer,
	)
}

// NeedsWork reports whether the controller has work to do for the given cluster.
// It requires the cluster to be placed on a management cluster. Beyond that:
// - Clusters being deleted need ApplyDesire cleanup
// - Clusters with a ClusterServiceID need resource syncing
func (c *clusterResourcesController) NeedsWork(cluster *coreapi.HCPOpenShiftCluster, managementCluster *azcorearm.ResourceID) bool {
	if managementCluster == nil {
		return false
	}

	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return true
	}

	if cluster.ServiceProviderProperties.ClusterServiceID == nil {
		return false
	}

	return true
}

// SyncOnce polls the cluster resources endpoint and updates any relevant state.
// When the cluster is being deleted, it cleans up all ApplyDesires it owns
// instead of polling for resources.
func (c *clusterResourcesController) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from cache: %w", err))
	}

	spc, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster from cache: %w", err))
	}
	managementCluster := spc.Status.ManagementClusterResourceID

	if !c.NeedsWork(cluster, managementCluster) {
		return nil
	}

	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return c.deleteAllOwnedApplyDesires(ctx, key, managementCluster)
	}

	clusterServiceID := *cluster.ServiceProviderProperties.ClusterServiceID
	if err := c.fetchAndProcessClusterResources(ctx, key, managementCluster, clusterServiceID); err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster resources: %w", err))
	}

	return nil
}

// deleteAllOwnedApplyDesires removes all ApplyDesire Cosmos documents owned by
// this controller for the given cluster. Called during cluster deletion — the
// underlying kube resources are cleaned up separately by the
// ClusterChildResourcesCleanupController, so we only need to purge the docs.
func (c *clusterResourcesController) deleteAllOwnedApplyDesires(ctx context.Context, key controllerutils.HCPClusterKey, managementCluster *azcorearm.ResourceID) error {
	logger := utils.LoggerFromContext(ctx)

	existing, err := c.applyDesireLister.ListForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("list ApplyDesires for deletion cleanup: %w", err))
	}

	kubeApplierDBClient := c.kubeApplierDBClients.For(ctx, managementCluster)
	if kubeApplierDBClient == nil {
		return nil
	}

	for _, desire := range existing {
		if desire.Tags == nil ||
			desire.Tags[kubeapplierapi.TagControllerName] != ClusterResourcesControllerName {
			continue
		}
		scope, err := kubeappliercosmosstorage.ParseDesireScope(desire.ResourceID.Parent)
		if err != nil {
			return utils.TrackError(fmt.Errorf("parse scope for ApplyDesire %s: %w", desire.ResourceID.Name, err))
		}
		crud, err := kubeApplierDBClient.ApplyDesiresFor(scope)
		if err != nil {
			return utils.TrackError(fmt.Errorf("get CRUD for ApplyDesire %s: %w", desire.ResourceID.Name, err))
		}
		if err := crud.Delete(ctx, desire.ResourceID.Name); err != nil && !cosmosstorageutils.IsNotFoundError(err) {
			return utils.TrackError(fmt.Errorf("delete ApplyDesire %s: %w", desire.ResourceID.Name, err))
		}
		logger.Info("deleted ApplyDesire document", "desireName", desire.ResourceID.Name)
	}

	return nil
}

// fetchAndProcessClusterResources calls the Cluster Service SDK to get cluster resources information
// and processes the resources.
func (c *clusterResourcesController) fetchAndProcessClusterResources(ctx context.Context,
	key controllerutils.HCPClusterKey, managementCluster *azcorearm.ResourceID, clusterServiceID metadataapi.InternalID) error {
	// Get cluster resources from the Cluster Service SDK
	resources, err := c.clustersServiceClient.GetClusterResources(ctx, clusterServiceID)
	if err != nil {
		return utils.TrackError(err)
	}

	if resources != nil {
		if err := c.processClusterResources(ctx, key, managementCluster, resources); err != nil {
			return utils.TrackError(fmt.Errorf("failed to process cluster resources: %w", err))
		}
	}

	return nil
}

// processClusterResources converts each resource to ApplyDesire documents
func (c *clusterResourcesController) processClusterResources(ctx context.Context, key controllerutils.HCPClusterKey,
	managementCluster *azcorearm.ResourceID, resources *arohcpv1alpha1.ClusterResources) error {

	kubeApplierDBClient := c.kubeApplierDBClients.For(ctx, managementCluster)
	if kubeApplierDBClient == nil {
		return nil
	}
	applyDesireCRUD, err := kubeApplierDBClient.ApplyDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get kube-applier CRUD: %w", err))
	}

	tags := map[string]string{kubeapplierapi.TagControllerName: ClusterResourcesControllerName}

	resourceMap := resources.Resources()
	desiredResourceIDs := make(map[string]bool, len(resourceMap))
	var errs []error
	for resourceKey, resourceValue := range resourceMap {
		var unstructuredObj unstructured.Unstructured
		if err := json.Unmarshal([]byte(resourceValue), &unstructuredObj); err != nil {
			errs = append(errs, utils.TrackError(fmt.Errorf("failed to unmarshal resource %s: %w", resourceKey, err)))
			continue
		}

		gvr, err := restmapper.ResourceFor(unstructuredObj.GroupVersionKind())
		if err != nil {
			errs = append(errs, utils.TrackError(fmt.Errorf("failed to resolve resource %s: %w", resourceKey, err)))
			continue
		}

		classified, err := classifyClusterResource(&unstructuredObj)
		if err != nil {
			errs = append(errs, utils.TrackError(err))
			continue
		}

		target := kubeapplierapi.ResourceReference{
			Group:     gvr.Group,
			Version:   gvr.Version,
			Resource:  gvr.Resource,
			Name:      unstructuredObj.GetName(),
			Namespace: unstructuredObj.GetNamespace(),
		}

		var desire *kubeapplierapi.ApplyDesire
		var crud cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire]

		switch {
		case len(classified.nodePoolName) != 0:
			np, npErr := c.nodePoolLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, classified.nodePoolName)
			if npErr != nil && !cosmosstorageutils.IsNotFoundError(npErr) {
				errs = append(errs, utils.TrackError(fmt.Errorf("failed to get nodepool %s: %w", classified.nodePoolName, npErr)))
				continue
			}
			if np == nil || np.ServiceProviderProperties.DeletionTimestamp != nil {
				continue
			}

			crud, err = kubeApplierDBClient.ApplyDesiresForNodePool(
				key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, classified.nodePoolName,
			)
			if err != nil {
				errs = append(errs, utils.TrackError(fmt.Errorf("failed to get node pool kube-applier CRUD for %s: %w", classified.nodePoolName, err)))
				continue
			}

			desire, err = buildNodePoolResourceApplyDesire(
				key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName,
				classified.nodePoolName, classified.desireName,
				managementCluster, target, &unstructuredObj, tags,
			)
		default:
			crud = applyDesireCRUD

			desire, err = buildClusterResourceApplyDesire(
				key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName,
				classified.desireName, managementCluster, target, &unstructuredObj, tags,
			)
		}

		if err != nil {
			errs = append(errs, err)
			continue
		}

		desiredResourceIDs[strings.ToLower(desire.ResourceID.String())] = true

		if err := kubeapplierhelpers.EnsureApplyDesire(ctx, crud, c.applyDesireLister, desire); err != nil {
			errs = append(errs, err)
		}
	}
	if err := c.deleteStaleApplyDesires(ctx, key, managementCluster, desiredResourceIDs); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

type classifiedResource struct {
	desireName   string
	nodePoolName string
}

// classifyClusterResource maps a kube object from the Cluster Service
// GetClusterResources response to an intent-based desire name. Every
// recognized resource gets a stable, human-readable name; unrecognized
// resources return an error so new resource types surface immediately
// instead of getting an opaque auto-derived name.
//
// Recognized resources (group / kind → desireName):
//
//	hypershift.openshift.io    HostedCluster          → HostedCluster
//	hypershift.openshift.io    NodePool               → NodePool                 (nodePoolName set, scoped under nodepool)
//	core/v1                    Namespace              → HostedClusterNamespace | ControlPlaneNamespace
//	core/v1                    ConfigMap              → DefaultIngressConfigMap
//	core/v1                    Secret                 → OCPPullSecret
//	multitenancy.acn.azure.com PodNetwork             → PodNetwork
//	multitenancy.acn.azure.com PodNetworkInstance      → PodNetworkInstance
//	secret-sync.x-k8s.io      SecretSync             → {BoundServiceAccountSigningKey,DefaultIngressWildcardCert,KubeAPIServerServingCert}SecretSync
//	secrets-store.csi.x-k8s.io SecretProviderClass    → {BoundServiceAccountSigningKey,DefaultIngressWildcardCert,KubeAPIServerServingCert}SecretProviderClass
func classifyClusterResource(obj *unstructured.Unstructured) (classifiedResource, error) {
	gvk := obj.GroupVersionKind()
	name := obj.GetName()

	switch gvk.Kind {
	case "HostedCluster":
		return classifiedResource{desireName: "HostedCluster"}, nil

	case "NodePool":
		// HyperShift names kube NodePool CRs as "<spec.clusterName>-<armName>".
		// Strip the prefix to recover the ARM nodepool name used in Cosmos.
		clusterName, _, _ := unstructured.NestedString(obj.Object, "spec", "clusterName")
		armName := strings.TrimPrefix(name, clusterName+"-")
		return classifiedResource{desireName: "NodePool", nodePoolName: armName}, nil

	case "Namespace":
		// CS returns two Namespaces: the HostedCluster namespace
		// (e.g. "ocm-arohcppers-2sdm6b8jke9sm3h8ukc8mbaahngnre5c") and the
		// ControlPlane namespace
		// (e.g. "ocm-arohcppers-2sdm6b8jke9sm3h8ukc8mbaahngnre5c-j7h3t4w0u1t3b4b").
		// The ControlPlane namespace carries the "hypershift.openshift.io/cluster" label.
		if _, ok := obj.GetLabels()["hypershift.openshift.io/cluster"]; ok {
			return classifiedResource{desireName: "ControlPlaneNamespace"}, nil
		}
		return classifiedResource{desireName: "HostedClusterNamespace"}, nil

	case "ConfigMap":
		return classifiedResource{desireName: "DefaultIngressConfigMap"}, nil

	case "Secret":
		return classifiedResource{desireName: "OCPPullSecret"}, nil

	case "PodNetwork":
		return classifiedResource{desireName: "PodNetwork"}, nil

	case "PodNetworkInstance":
		return classifiedResource{desireName: "PodNetworkInstance"}, nil

	case "SecretSync":
		switch {
		case strings.Contains(name, "signing-key"):
			return classifiedResource{desireName: "BoundServiceAccountSigningKeySecretSync"}, nil
		case strings.Contains(name, "default-ingress"):
			return classifiedResource{desireName: "DefaultIngressWildcardCertSecretSync"}, nil
		case strings.Contains(name, "kube-apiserver"):
			return classifiedResource{desireName: "KubeAPIServerServingCertSecretSync"}, nil
		}

	case "SecretProviderClass":
		switch {
		case strings.Contains(name, "signing-key"):
			return classifiedResource{desireName: "BoundServiceAccountSigningKeySecretProviderClass"}, nil
		case strings.Contains(name, "default-ingress"):
			return classifiedResource{desireName: "DefaultIngressWildcardCertSecretProviderClass"}, nil
		case strings.Contains(name, "kube-apiserver"):
			return classifiedResource{desireName: "KubeAPIServerServingCertSecretProviderClass"}, nil
		}
	}

	return classifiedResource{}, fmt.Errorf(
		"unrecognized cluster resource: group=%q version=%q kind=%q namespace=%q name=%q",
		gvk.Group, gvk.Version, gvk.Kind, obj.GetNamespace(), name,
	)
}

func buildClusterResourceApplyDesire(
	subscriptionID, resourceGroupName, clusterName, desireName string,
	managementCluster *azcorearm.ResourceID,
	target kubeapplierapi.ResourceReference,
	obj *unstructured.Unstructured,
	tags map[string]string,
) (*kubeapplierapi.ApplyDesire, error) {
	resourceIDStr := kubeapplierapi.ToClusterScopedApplyDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, desireName,
	)
	resourceID, err := azcorearm.ParseResourceID(resourceIDStr)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to parse ApplyDesire resource ID %q: %w", resourceIDStr, err))
	}

	rawJSON, err := json.Marshal(obj)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to marshal kube object: %w", err))
	}

	return &kubeapplierapi.ApplyDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(managementCluster.String()),
		},
		Spec: kubeapplierapi.ApplyDesireSpec{
			ManagementCluster: managementCluster,
			Type:              kubeapplierapi.ApplyDesireTypeServerSideApply,
			TargetItem:        target,
			ServerSideApply: &kubeapplierapi.ServerSideApplyConfig{
				KubeContent: &runtime.RawExtension{Raw: rawJSON},
				// Use "fieldManager": "work-agent" to avoid conflicting declarations due to
				// the eventual consistency nature of the ResourcesController.
				// This way fields that are not claimed will automatically be garbage collected.
				//  TODO: remove this once ACM and Maestro are removed.
				FieldManager: ptr.To(ocmv1.DefaultFieldManager),
			},
		},
		Tags: tags,
	}, nil
}

func buildNodePoolResourceApplyDesire(
	subscriptionID, resourceGroupName, clusterName, nodePoolName, desireName string,
	managementCluster *azcorearm.ResourceID,
	target kubeapplierapi.ResourceReference,
	obj *unstructured.Unstructured,
	tags map[string]string,
) (*kubeapplierapi.ApplyDesire, error) {
	resourceIDStr := kubeapplierapi.ToNodePoolScopedApplyDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, nodePoolName, desireName,
	)
	resourceID, err := azcorearm.ParseResourceID(resourceIDStr)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to parse ApplyDesire resource ID %q: %w", resourceIDStr, err))
	}

	rawJSON, err := json.Marshal(obj)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to marshal kube object: %w", err))
	}

	return &kubeapplierapi.ApplyDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(managementCluster.String()),
		},
		Spec: kubeapplierapi.ApplyDesireSpec{
			ManagementCluster: managementCluster,
			Type:              kubeapplierapi.ApplyDesireTypeServerSideApply,
			TargetItem:        target,
			ServerSideApply: &kubeapplierapi.ServerSideApplyConfig{
				KubeContent: &runtime.RawExtension{Raw: rawJSON},
				// Use "fieldManager": "work-agent" to avoid conflicting declarations due to
				// the eventual consistency nature of the ResourcesController.
				// This way fields that are not claimed will automatically be garbage collected.
				//  TODO: remove this once ACM and Maestro are removed.
				FieldManager: ptr.To(ocmv1.DefaultFieldManager),
			},
		},
		Tags: tags,
	}, nil
}

func (c *clusterResourcesController) deleteStaleApplyDesires(
	ctx context.Context,
	key controllerutils.HCPClusterKey,
	managementCluster *azcorearm.ResourceID,
	currentDesiredResourceIDs map[string]bool,
) error {
	logger := utils.LoggerFromContext(ctx)

	existing, err := c.applyDesireLister.ListForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("list ApplyDesires for stale cleanup: %w", err))
	}

	kubeApplierDBClient := c.kubeApplierDBClients.For(ctx, managementCluster)
	if kubeApplierDBClient == nil {
		return nil
	}

	for _, desire := range existing {
		if desire.Tags == nil ||
			desire.Tags[kubeapplierapi.TagControllerName] != ClusterResourcesControllerName {
			continue
		}
		if currentDesiredResourceIDs[strings.ToLower(desire.ResourceID.String())] {
			continue
		}

		scope, err := kubeappliercosmosstorage.ParseDesireScope(desire.ResourceID.Parent)
		if err != nil {
			return utils.TrackError(fmt.Errorf("parse scope for ApplyDesire %s: %w", desire.ResourceID.Name, err))
		}
		crud, err := kubeApplierDBClient.ApplyDesiresFor(scope)
		if err != nil {
			return utils.TrackError(fmt.Errorf("get CRUD for ApplyDesire %s: %w", desire.ResourceID.Name, err))
		}
		removed, err := kubeapplierhelpers.EnsureApplyDesireRemoved(ctx, desire.ResourceID.Name, crud)
		if err != nil {
			return err
		}
		if removed {
			logger.Info("purged stale ApplyDesire", "desireName", desire.ResourceID.Name)
		} else {
			logger.Info("stale ApplyDesire pending deletion", "desireName", desire.ResourceID.Name)
		}
	}

	return nil
}
