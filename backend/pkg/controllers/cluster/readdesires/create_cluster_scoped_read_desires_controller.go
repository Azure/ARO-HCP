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

package readdesires

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// createClusterScopedReadDesiresSyncer ensures a ReadDesire exists per
// HCPCluster pointing at the cluster's Hypershift HostedCluster object in
// the management cluster. The kube-applier sidecar on the management cluster
// observes the HostedCluster via that ReadDesire and writes the observed
// state into ReadDesire.Status.KubeContent; consumers read it directly from
// there.
//
// Replaces createClusterScopedMaestroReadonlyBundlesSyncer, which used
// Maestro to mirror the same content.
type createClusterScopedReadDesiresSyncer struct {
	activeOperationLister corelisters.ActiveOperationLister

	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	kubeApplierDBClients         kubeappliercosmosstorage.KubeApplierDBClients
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	readDesireLister             kubeapplierlisters.ReadDesireLister

	// hostedClusterNamespaceEnvIdentifier is the "envName" segment of the
	// CDNamespace (ocm-<envName>-<csClusterID>). Historically the maestro
	// source identifier doubled as this value; we keep the same parameter
	// name for continuity with the deployment config.
	hostedClusterNamespaceEnvIdentifier string
}

var _ controllerutils.ClusterSyncer = (*createClusterScopedReadDesiresSyncer)(nil)

// CreateClusterScopedReadDesiresControllerName is the controller name, recorded
// on every desire this controller authors via kubeapplierapi.TagControllerName.
const CreateClusterScopedReadDesiresControllerName = "CreateClusterScopedReadDesires"

// NewCreateClusterScopedReadDesiresController wires the per-cluster
// ReadDesire creator. It reuses NewClusterWatchingController so the cadence
// (informer relist + cooldown) matches the rest of the cluster-scoped
// pipeline.
func NewCreateClusterScopedReadDesiresController(
	activeOperationLister corelisters.ActiveOperationLister,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients,
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister,
	readDesireLister kubeapplierlisters.ReadDesireLister,
	informers coreinformers.BackendInformers,
	hostedClusterNamespaceEnvIdentifier string,
) controllerutils.Controller {
	syncer := &createClusterScopedReadDesiresSyncer{
		activeOperationLister:               activeOperationLister,
		resourcesDBClient:                   resourcesDBClient,
		kubeApplierDBClients:                kubeApplierDBClients,
		serviceProviderClusterLister:        serviceProviderClusterLister,
		readDesireLister:                    readDesireLister,
		hostedClusterNamespaceEnvIdentifier: hostedClusterNamespaceEnvIdentifier,
	}

	return controllerutils.NewClusterWatchingController(
		CreateClusterScopedReadDesiresControllerName,
		resourcesDBClient,
		informers,
		nil,
		1*time.Minute,
		syncer,
	)
}

func (c *createClusterScopedReadDesiresSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx).WithValues(utils.LogValues{}.
		AddSubscriptionID(key.SubscriptionID).
		AddResourceGroup(key.ResourceGroupName).
		AddHCPClusterName(key.HCPClusterName)...)
	// Inject the cluster-scoped logger so downstream helpers (e.g.
	// kubeApplierDBClients.For) that read utils.LoggerFromContext(ctx)
	// also emit the cluster-identifying fields.
	ctx = utils.ContextWithLogger(ctx, logger)

	existingCluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}
	if existingCluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}
	if existingCluster.ServiceProviderProperties.ClusterServiceID == nil {
		// We don't have a CS reference yet; we'll retrigger once it's set.
		return nil
	}

	// In the per-management-cluster container model, every kube-applier
	// container holds exactly one MC's documents. The placement-sync
	// controller is responsible for writing the resolved MC into
	// ServiceProviderCluster.Status.ManagementClusterResourceID; until that
	// lands we have nowhere to write the ReadDesire, so skip and wait for
	// the next reconcile cycle.
	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		// CreateServiceProviderCluster will populate it; we'll be re-enqueued via the ServiceProviderCluster informer.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}
	mcResourceID := serviceProviderCluster.Status.ManagementClusterResourceID
	if mcResourceID == nil {
		return nil
	}

	// Pull the domain prefix from cosmos rather than Cluster Service: the
	// cluster_base_domain_prefix_sync controller already mirrors CS DomainPrefix into
	// CustomerProperties.DNS.BaseDomainPrefix, so we'd just be re-doing its
	// work. Skip until that sync has happened; we'll retrigger on relist.
	csClusterDomainPrefix := existingCluster.CustomerProperties.DNS.BaseDomainPrefix
	if len(csClusterDomainPrefix) == 0 {
		return nil
	}
	csClusterID := existingCluster.ServiceProviderProperties.ClusterServiceID.ID()

	kaClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
	if kaClient == nil {
		// Registry doesn't have an entry yet for this MC (e.g. the fleet
		// lister hasn't caught up). Skip and rely on retrigger. When the MC
		// document is registered but misconfigured (e.g. missing its
		// kube-applier container name), For() surfaces that loudly.
		return nil
	}
	crud, err := kaClient.ReadDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ReadDesire CRUD: %w", err))
	}

	desiredReadDesires := []desiredReadDesire{
		{readDesireNameReadonlyHostedCluster, hostedClusterTarget(c.hostedClusterNamespaceEnvIdentifier, csClusterID, csClusterDomainPrefix)},
		{kubeapplierhelpers.ReadDesireNameReadonlyHypershiftControlPlaneComponentClusterAutoscaler, clusterAutoscalerTarget(c.hostedClusterNamespaceEnvIdentifier, csClusterID, csClusterDomainPrefix)},
	}

	controlPlaneNamespace := serviceProviderCluster.Status.ControlPlaneNamespace
	if len(controlPlaneNamespace) > 0 {
		desiredReadDesires = append(desiredReadDesires, desiredReadDesire{kubeapplierhelpers.ReadDesireNameServingCA, servingCATarget(controlPlaneNamespace)})
	}

	var errs []error
	for _, desired := range desiredReadDesires {
		readDesire, err := buildClusterReadDesire(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName,
			desired.name, mcResourceID, desired.target)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := kubeapplierhelpers.EnsureReadDesire(ctx, crud, c.readDesireLister, readDesire); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// buildClusterReadDesire builds a cluster-scoped ReadDesire that observes target.
// The cluster ReadDesire controller builds its own desires; the shared
// kubeapplierhelpers.EnsureReadDesire helper then persists them.
func buildClusterReadDesire(
	subscriptionID, resourceGroupName, clusterName, desireName string,
	managementCluster *azcorearm.ResourceID,
	target kubeapplierapi.ResourceReference,
) (*kubeapplierapi.ReadDesire, error) {
	resourceIDStr := kubeapplierapi.ToClusterScopedReadDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, desireName,
	)
	resourceID, err := azcorearm.ParseResourceID(resourceIDStr)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to parse ReadDesire resource ID %q: %w", resourceIDStr, err))
	}

	return &kubeapplierapi.ReadDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(managementCluster.String()),
		},
		Spec: kubeapplierapi.ReadDesireSpec{
			ManagementCluster: managementCluster,
			TargetItem:        target,
		},
		Tags: map[string]string{kubeapplierapi.TagControllerName: CreateClusterScopedReadDesiresControllerName},
	}, nil
}

// desiredReadDesire pairs a ReadDesire's well-known name with the
// management-cluster object it observes.
type desiredReadDesire struct {
	name   string
	target kubeapplierapi.ResourceReference
}

// readDesireNameReadonlyHostedCluster is the well-known ReadDesire name
// the backend uses for the HostedCluster mirror. It matches the existing
// MaestroBundleInternalName in lowercase so the downstream
// ManagementClusterContent document path stays stable across the migration.
var readDesireNameReadonlyHostedCluster = strings.ToLower(string(coreapi.MaestroBundleInternalNameReadonlyHypershiftHostedCluster))

// hostedClusterTarget builds the ResourceReference that points at the
// cluster's HostedCluster object in the management cluster. The naming
// rules (namespace = "ocm-<env>-<csClusterID>", name = csClusterDomainPrefix)
// match what CS itself uses; see the corresponding pre-migration code in
// createClusterScopedMaestroReadonlyBundlesSyncer.buildClusterEmptyHostedCluster
// for the original derivation.
func hostedClusterTarget(envIdentifier, csClusterID, csClusterDomainPrefix string) kubeapplierapi.ResourceReference {
	return kubeapplierapi.ResourceReference{
		Group:     hsv1beta1.SchemeGroupVersion.Group,
		Version:   hsv1beta1.SchemeGroupVersion.Version,
		Resource:  "hostedclusters",
		Namespace: controllerutils.HostedClusterNamespace(envIdentifier, csClusterID),
		Name:      csClusterDomainPrefix,
	}
}

// clusterAutoscalerTarget builds the ResourceReference for the cluster-autoscaler
// ControlPlaneComponent in the HCP control plane namespace.
func clusterAutoscalerTarget(envIdentifier, csClusterID, csClusterDomainPrefix string) kubeapplierapi.ResourceReference {
	return kubeapplierapi.ResourceReference{
		Group:     hsv1beta1.SchemeGroupVersion.Group,
		Version:   hsv1beta1.SchemeGroupVersion.Version,
		Resource:  "controlplanecomponents",
		Namespace: controllerutils.HostedControlPlaneNamespace(envIdentifier, csClusterID, csClusterDomainPrefix),
		Name:      "cluster-autoscaler",
	}
}

const servingCATLSSecretName = "kube-apiserver-tls-cert"

func servingCATarget(controlPlaneNamespace string) kubeapplierapi.ResourceReference {
	return kubeapplierapi.ResourceReference{
		Group:     "",
		Version:   "v1",
		Resource:  "secrets",
		Namespace: controlPlaneNamespace,
		Name:      servingCATLSSecretName,
	}
}
