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

package k8sresources

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	ocmv1 "open-cluster-management.io/api/work/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const NamespaceControllerName = "NamespaceK8sResources"

// Keep the persisted name so existing ClusterResources desires are adopted in place.
const ControlPlaneNamespaceDesireName = "ControlPlaneNamespace"
const HostedClusterNamespaceDesireName = "HostedClusterNamespace"

type namespaceSyncer struct {
	envIdentifier                string
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	kubeApplierDBClients         kubeappliercosmosstorage.KubeApplierDBClients
	applyDesireLister            kubeapplierlisters.ApplyDesireLister
	readDesireLister             kubeapplierlisters.ReadDesireLister
}

var _ controllerutils.ClusterSyncer = (*namespaceSyncer)(nil)

func NewNamespaceController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	envIdentifier string,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()
	_, applyDesireLister := kubeApplierInformers.ApplyDesires()
	_, readDesireLister := kubeApplierInformers.ReadDesires()
	controller := controllerutils.NewClusterWatchingController(
		NamespaceControllerName,
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		30*time.Second,
		&namespaceSyncer{
			envIdentifier:                envIdentifier,
			clusterLister:                clusterLister,
			serviceProviderClusterLister: serviceProviderClusterLister,
			kubeApplierDBClients:         kubeApplierDBClients,
			applyDesireLister:            applyDesireLister,
			readDesireLister:             readDesireLister,
		},
	)
	controller.AddInterestingResource(coreapi.ClusterResourceType, "*")
	controller.AddInterestingResource(coreapi.ServiceProviderClusterResourceType, coreapi.ServiceProviderClusterResourceName)
	for name := range NamespaceDeletionPreconditionDesireNames().Insert(strings.ToLower(HostedClusterNamespaceDesireName), strings.ToLower(ControlPlaneNamespaceDesireName)) {
		controller.AddInterestingResource(kubeapplierapi.ClusterScopedApplyDesireResourceType, name)
	}
	controller.AddInterestingResource(kubeapplierapi.ClusterScopedReadDesireResourceType, HostedClusterNamespaceDesireName)
	controller.AddInterestingResource(kubeapplierapi.ClusterScopedReadDesireResourceType, ControlPlaneNamespaceDesireName)
	return controller
}

func (syncer *namespaceSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	cluster, err := syncer.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	serviceProviderCluster, err := syncer.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	if !syncer.needsWork(cluster, serviceProviderCluster) {
		return nil
	}
	client := syncer.kubeApplierDBClients.For(ctx, serviceProviderCluster.Status.ManagementClusterResourceID)
	if client == nil {
		return nil
	}
	applyCRUD, err := client.ApplyDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(err)
	}
	readCRUD, err := client.ReadDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(err)
	}
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		err = syncer.deleteNamespaces(ctx, key, client, applyCRUD, readCRUD)
	} else {
		err = syncer.ensureNamespaces(ctx, key, cluster, serviceProviderCluster, applyCRUD, readCRUD)
	}
	if cosmosstorageutils.IsConflictError(err) || cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	return utils.TrackError(err)
}

func (syncer *namespaceSyncer) ensureNamespaces(
	ctx context.Context,
	key controllerutils.HCPClusterKey,
	cluster *coreapi.Cluster,
	serviceProviderCluster *coreapi.ServiceProviderCluster,
	applyCRUD cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire],
	readCRUD cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire],
) error {
	hostedNamespace, err := buildHostedClusterNamespaceDesire(key, cluster, serviceProviderCluster, syncer.envIdentifier)
	if err != nil {
		return err
	}
	desires := []*kubeapplierapi.ApplyDesire{hostedNamespace}
	if len(serviceProviderCluster.Status.ControlPlaneNamespace) > 0 {
		controlPlaneNamespace, err := buildControlPlaneNamespaceDesire(key, cluster, serviceProviderCluster)
		if err != nil {
			return err
		}
		desires = append(desires, controlPlaneNamespace)
	}
	for _, desire := range desires {
		readResourceID, err := azcorearm.ParseResourceID(kubeapplierapihelpers.ToClusterScopedReadDesireResourceIDString(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, desire.ResourceID.Name))
		if err != nil {
			return err
		}
		read := &kubeapplierapi.ReadDesire{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: readResourceID, PartitionKey: desire.PartitionKey},
			Spec:           kubeapplierapi.ReadDesireSpec{ManagementCluster: desire.Spec.ManagementCluster, TargetItem: desire.Spec.TargetItem},
			Tags:           desire.Tags,
		}
		if err := kubeapplierhelpers.EnsureReadDesire(ctx, readCRUD, syncer.readDesireLister, read); err != nil {
			return err
		}
		if err := kubeapplierhelpers.EnsureApplyDesire(ctx, applyCRUD, syncer.applyDesireLister, desire); err != nil {
			return err
		}
	}
	return nil
}

func (syncer *namespaceSyncer) deleteNamespaces(
	ctx context.Context,
	key controllerutils.HCPClusterKey,
	client kubeappliercosmosstorage.KubeApplierDBClient,
	applyCRUD cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire],
	readCRUD cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire],
) error {
	ready, err := syncer.deletePreconditionApplyDesireRemovalChainDrained(ctx, key, client)
	if err != nil || !ready {
		return err
	}
	iterator, err := applyCRUD.List(ctx, nil)
	if err != nil {
		return err
	}
	var desires []*kubeapplierapi.ApplyDesire
	for _, desire := range iterator.Items(ctx) {
		desires = append(desires, desire)
	}
	if err := iterator.GetError(); err != nil {
		return err
	}
	done, err := kubeapplierhelpers.EnsureMatchingApplyDesiresRemoved(ctx, client, NamespaceControllerName, desires, func(desire *kubeapplierapi.ApplyDesire) bool {
		return isNamespaceDesire(desire.ResourceID.Name)
	})
	if err != nil || !done {
		return err
	}
	// Retry orphan ReadDesire cleanup even if its ApplyDesire was removed during
	// an earlier reconcile whose paired read deletion failed.
	for _, name := range []string{HostedClusterNamespaceDesireName, ControlPlaneNamespaceDesireName} {
		if err := readCRUD.Delete(ctx, strings.ToLower(name)); err != nil && !cosmosstorageutils.IsNotFoundError(err) {
			return err
		}
	}
	return nil
}

func isNamespaceDesire(name string) bool {
	return strings.EqualFold(name, HostedClusterNamespaceDesireName) || strings.EqualFold(name, ControlPlaneNamespaceDesireName)
}

// NamespaceDeletionPreconditionDesireNames is the complete set of desires that
// must disappear before namespace deletion. These are the final three resource
// deletions in the ClusterResources teardown chain.
func NamespaceDeletionPreconditionDesireNames() sets.Set[string] {
	names := sets.New[string]("HostedCluster", "PodNetworkInstance", "PodNetwork")
	lowered := sets.New[string]()
	for name := range names {
		lowered.Insert(strings.ToLower(name))
	}
	return lowered
}

func (syncer *namespaceSyncer) deletePreconditionApplyDesireRemovalChainDrained(ctx context.Context, key controllerutils.HCPClusterKey, client kubeappliercosmosstorage.KubeApplierDBClient) (bool, error) {
	crud, err := client.UntypedCRUD(*key.GetResourceID())
	if err != nil {
		return false, err
	}
	iterator, err := crud.ListRecursive(ctx, nil)
	if err != nil {
		return false, err
	}
	prerequisites := NamespaceDeletionPreconditionDesireNames()
	for _, child := range iterator.Items(ctx) {
		if child.ResourceID == nil {
			return false, fmt.Errorf("desire document %s has no resource ID", child.ID)
		}
		if strings.HasSuffix(strings.ToLower(child.ResourceType), "/"+strings.ToLower(kubeapplierapi.ApplyDesireResourceTypeName)) && prerequisites.Has(strings.ToLower(child.ResourceID.Name)) {
			utils.LoggerFromContext(ctx).Info("waiting for teardown before namespace deletion", "applyDesire", child.ResourceID.String())
			return false, nil
		}
	}
	if err := iterator.GetError(); err != nil {
		return false, err
	}
	return true, nil
}

func (syncer *namespaceSyncer) needsWork(cluster *coreapi.Cluster, serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	if serviceProviderCluster.Status.ManagementClusterResourceID == nil {
		return false
	}
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return true
	}
	return cluster.ServiceProviderProperties.ClusterServiceID != nil
}

func buildControlPlaneNamespaceDesire(key controllerutils.HCPClusterKey, cluster *coreapi.Cluster, serviceProviderCluster *coreapi.ServiceProviderCluster) (*kubeapplierapi.ApplyDesire, error) {
	resourceID, err := azcorearm.ParseResourceID(kubeapplierapihelpers.ToClusterScopedApplyDesireResourceIDString(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, ControlPlaneNamespaceDesireName))
	if err != nil {
		return nil, err
	}
	namespace := &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
		ObjectMeta: metav1.ObjectMeta{
			Name: serviceProviderCluster.Status.ControlPlaneNamespace,
			Annotations: map[string]string{
				controllerutil.HcpClusterAzureResourceIdAnnotation: cluster.ID.String(),
			},
			Labels: map[string]string{
				"app.kubernetes.io/managed-by":    "aro-hcp-clusters-service",
				"hypershift.openshift.io/cluster": cluster.ServiceProviderProperties.ClusterServiceID.ID(),
			},
		},
	}
	content, err := json.Marshal(namespace)
	if err != nil {
		return nil, fmt.Errorf("marshal control plane namespace: %w", err)
	}
	managementCluster := serviceProviderCluster.Status.ManagementClusterResourceID
	return &kubeapplierapi.ApplyDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(managementCluster.String()),
		},
		Spec: kubeapplierapi.ApplyDesireSpec{
			ManagementCluster: managementCluster,
			Type:              kubeapplierapi.ApplyDesireTypeServerSideApply,
			TargetItem:        kubeapplierapi.ResourceReference{Version: "v1", Resource: "namespaces", Name: namespace.Name},
			ServerSideApply: &kubeapplierapi.ServerSideApplyConfig{
				KubeContent:  &runtime.RawExtension{Raw: content},
				FieldManager: ptr.To(ocmv1.DefaultFieldManager),
			},
		},
		Tags: map[string]string{kubeapplierapi.TagControllerName: NamespaceControllerName},
	}, nil
}

func buildHostedClusterNamespaceDesire(key controllerutils.HCPClusterKey, cluster *coreapi.Cluster, serviceProviderCluster *coreapi.ServiceProviderCluster, envIdentifier string) (*kubeapplierapi.ApplyDesire, error) {
	if len(envIdentifier) == 0 {
		return nil, fmt.Errorf("environment identifier is required for the HostedCluster namespace")
	}
	csID := cluster.ServiceProviderProperties.ClusterServiceID.ID()
	namespaceName := controllerutils.HostedClusterNamespace(envIdentifier, csID)
	resourceID, err := azcorearm.ParseResourceID(kubeapplierapihelpers.ToClusterScopedApplyDesireResourceIDString(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, HostedClusterNamespaceDesireName))
	if err != nil {
		return nil, err
	}
	namespace := &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
		ObjectMeta: metav1.ObjectMeta{
			Name: namespaceName,
			Annotations: map[string]string{
				controllerutil.HcpClusterAzureResourceIdAnnotation: cluster.ID.String(),
			},
			Labels: map[string]string{
				"api.openshift.com/environment": envIdentifier,
				"api.openshift.com/id":          csID,
				"api.openshift.com/name":        key.HCPClusterName,
			},
		},
	}
	content, err := json.Marshal(namespace)
	if err != nil {
		return nil, err
	}
	managementCluster := serviceProviderCluster.Status.ManagementClusterResourceID
	return &kubeapplierapi.ApplyDesire{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(managementCluster.String())},
		Spec: kubeapplierapi.ApplyDesireSpec{
			ManagementCluster: managementCluster,
			Type:              kubeapplierapi.ApplyDesireTypeServerSideApply,
			TargetItem:        kubeapplierapi.ResourceReference{Version: "v1", Resource: "namespaces", Name: namespaceName},
			ServerSideApply:   &kubeapplierapi.ServerSideApplyConfig{KubeContent: &runtime.RawExtension{Raw: content}, FieldManager: ptr.To(ocmv1.DefaultFieldManager)},
		},
		Tags: map[string]string{kubeapplierapi.TagControllerName: NamespaceControllerName},
	}, nil
}
