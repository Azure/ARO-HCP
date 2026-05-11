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
	"fmt"
	"strings"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// createClusterScopedReadDesiresSyncer ensures a ReadDesire exists per
// HCPCluster pointing at the cluster's Hypershift HostedCluster object in
// the management cluster. The kube-applier sidecar on the management cluster
// observes the HostedCluster via that ReadDesire and writes the observed
// state into ReadDesire.Status.KubeContent. The "read+persist" controller
// then mirrors that into ManagementClusterContent.
//
// Replaces createClusterScopedMaestroReadonlyBundlesSyncer, which used
// Maestro to mirror the same content.
type createClusterScopedReadDesiresSyncer struct {
	cooldownChecker controllerutil.CooldownChecker

	activeOperationLister listers.ActiveOperationLister

	resourcesDBClient    database.ResourcesDBClient
	kubeApplierDBClients database.KubeApplierDBClients

	clusterServiceClient ocm.ClusterServiceClientSpec

	// hostedClusterNamespaceEnvIdentifier is the "envName" segment of the
	// CDNamespace (ocm-<envName>-<csClusterID>). Historically the maestro
	// source identifier doubled as this value; we keep the same parameter
	// name for continuity with the deployment config.
	hostedClusterNamespaceEnvIdentifier string
}

var _ controllerutils.ClusterSyncer = (*createClusterScopedReadDesiresSyncer)(nil)

// NewCreateClusterScopedReadDesiresController wires the per-cluster
// ReadDesire creator. It reuses NewClusterWatchingController so the cadence
// (informer relist + cooldown) matches the rest of the cluster-scoped
// pipeline.
func NewCreateClusterScopedReadDesiresController(
	activeOperationLister listers.ActiveOperationLister,
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	informers informers.BackendInformers,
	hostedClusterNamespaceEnvIdentifier string,
) controllerutils.Controller {
	syncer := &createClusterScopedReadDesiresSyncer{
		cooldownChecker:                     controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		activeOperationLister:               activeOperationLister,
		resourcesDBClient:                   resourcesDBClient,
		kubeApplierDBClients:                kubeApplierDBClients,
		clusterServiceClient:                clusterServiceClient,
		hostedClusterNamespaceEnvIdentifier: hostedClusterNamespaceEnvIdentifier,
	}

	return controllerutils.NewClusterWatchingController(
		"CreateClusterScopedReadDesires",
		resourcesDBClient,
		informers,
		nil,
		1*time.Minute,
		syncer,
	)
}

func (c *createClusterScopedReadDesiresSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
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
	spc, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}
	mcResourceID := spc.Status.ManagementClusterResourceID
	if mcResourceID == nil {
		return nil
	}

	csCluster, err := c.clusterServiceClient.GetCluster(ctx, *existingCluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster from Cluster Service: %w", err))
	}
	csClusterDomainPrefix := csCluster.DomainPrefix()
	csClusterID := existingCluster.ServiceProviderProperties.ClusterServiceID.ID()

	target := hostedClusterTarget(c.hostedClusterNamespaceEnvIdentifier, csClusterID, csClusterDomainPrefix)
	desired := buildReadDesire(
		kubeapplier.ToClusterScopedReadDesireResourceIDString(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, readDesireNameReadonlyHostedCluster),
		mcResourceID,
		target,
	)

	kaClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
	if kaClient == nil {
		// Registry doesn't have an entry yet for this MC (e.g. the fleet
		// lister hasn't caught up). Skip and rely on retrigger.
		return nil
	}
	parent := database.ResourceParent{
		SubscriptionID:    key.SubscriptionID,
		ResourceGroupName: key.ResourceGroupName,
		ClusterName:       key.HCPClusterName,
	}
	crud, err := kaClient.ReadDesires(parent)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ReadDesire CRUD: %w", err))
	}
	return ensureReadDesire(ctx, crud, desired)
}

func (c *createClusterScopedReadDesiresSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

// readDesireNameReadonlyHostedCluster is the well-known ReadDesire name
// the backend uses for the HostedCluster mirror. It matches the existing
// MaestroBundleInternalName in lowercase so the downstream
// ManagementClusterContent document path stays stable across the migration.
var readDesireNameReadonlyHostedCluster = strings.ToLower(string(api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster))

// hostedClusterTarget builds the ResourceReference that points at the
// cluster's HostedCluster object in the management cluster. The naming
// rules (namespace = "ocm-<env>-<csClusterID>", name = csClusterDomainPrefix)
// match what CS itself uses; see the corresponding pre-migration code in
// createClusterScopedMaestroReadonlyBundlesSyncer.buildClusterEmptyHostedCluster
// for the original derivation.
func hostedClusterTarget(envIdentifier, csClusterID, csClusterDomainPrefix string) kubeapplier.ResourceReference {
	return kubeapplier.ResourceReference{
		Group:     hsv1beta1.SchemeGroupVersion.Group,
		Version:   hsv1beta1.SchemeGroupVersion.Version,
		Resource:  "hostedclusters",
		Namespace: fmt.Sprintf("ocm-%s-%s", envIdentifier, csClusterID),
		Name:      csClusterDomainPrefix,
	}
}

// buildReadDesire produces the desired-state ReadDesire for ensureReadDesire.
// The status section is intentionally zero — the kube-applier owns status.
func buildReadDesire(resourceIDString string, managementCluster *azcorearm.ResourceID, target kubeapplier.ResourceReference) *kubeapplier.ReadDesire {
	resourceID, _ := azcorearm.ParseResourceID(resourceIDString) // resourceIDString is built from helpers and always parses
	return &kubeapplier.ReadDesire{
		CosmosMetadata: api.CosmosMetadata{ResourceID: resourceID},
		Spec: kubeapplier.ReadDesireSpec{
			ManagementCluster: managementCluster,
			TargetItem:        target,
		},
	}
}

// ensureReadDesire is the create-or-no-op helper. Spec is small and the
// backend is the only writer, so we don't bother updating an existing
// ReadDesire when the spec already matches — that would only churn etags.
// If anything in the spec differs we Replace.
func ensureReadDesire(
	ctx context.Context,
	crud database.ResourceCRUD[kubeapplier.ReadDesire],
	desired *kubeapplier.ReadDesire,
) error {
	existing, err := crud.Get(ctx, desired.ResourceID.Name)
	if database.IsNotFoundError(err) {
		if _, err := crud.Create(ctx, desired, nil); err != nil {
			return utils.TrackError(fmt.Errorf("create ReadDesire: %w", err))
		}
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ReadDesire: %w", err))
	}
	if managementClusterEqual(existing.Spec.ManagementCluster, desired.Spec.ManagementCluster) &&
		existing.Spec.TargetItem == desired.Spec.TargetItem {
		return nil
	}
	desired.CosmosETag = existing.CosmosETag
	desired.Status = existing.Status
	if _, err := crud.Replace(ctx, desired, nil); err != nil {
		return utils.TrackError(fmt.Errorf("replace ReadDesire: %w", err))
	}
	return nil
}

// managementClusterEqual compares two ReadDesire.Spec.ManagementCluster pointers
// for equality. Both may be nil; non-nil values compare by string form.
func managementClusterEqual(a, b *azcorearm.ResourceID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.String() == b.String()
}
