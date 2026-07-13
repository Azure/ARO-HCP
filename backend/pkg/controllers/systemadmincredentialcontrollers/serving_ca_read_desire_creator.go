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

package systemadmincredentialcontrollers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/blang/semver/v4"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	// readDesireNameServingCA is the well-known ReadDesire name used to
	// mirror the serving CA bundle ConfigMap from the management cluster.
	readDesireNameServingCA = "systemadmincredential-serving-ca"

	// servingCAConfigMapName is the name of the HyperShift-managed ConfigMap
	// holding the public serving CA bundle (the "ca-bundle.crt" key, with no
	// private key) in the hosted control plane namespace on the management
	// cluster.
	servingCAConfigMapName = "root-ca"

	// minServingCAConfigMapOCPVersion is the minimum OCP version whose hosted
	// control plane publishes the root-ca ConfigMap. The serving CA ReadDesire
	// is only created for clusters at or above this version, and is therefore
	// only consumed for those clusters.
	minServingCAConfigMapOCPVersion = "4.20"
)

type servingCAReadDesireCreator struct {
	cooldownChecker controllerutil.CooldownChecker

	resourcesDBClient    database.ResourcesDBClient
	kubeApplierDBClients database.KubeApplierDBClients

	serviceProviderClusterLister listers.ServiceProviderClusterLister
}

var _ controllerutils.ClusterSyncer = (*servingCAReadDesireCreator)(nil)

// NewServingCAReadDesireCreatorController returns a ClusterWatchingController
// that ensures a ReadDesire exists per cluster pointing at the root-ca
// ConfigMap in the hosted control plane namespace ("clusters-<clusterName>")
// on the management cluster. The kube-applier mirrors the ConfigMap content
// into ReadDesire.Status.KubeContent; controller #8 (CABundleSync) reads the
// public "ca-bundle.crt" bundle from there.
//
// This is a cluster-scoped operation — the serving CA is shared across all
// credential requests for a given cluster. The root-ca ConfigMap only exists
// for OCP 4.20+ hosted control planes, so the ReadDesire is only created for
// clusters at or above that version.
func NewServingCAReadDesireCreatorController(
	activeOperationLister listers.ActiveOperationLister,
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	backendInformers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()

	syncer := &servingCAReadDesireCreator{
		cooldownChecker:              controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient:            resourcesDBClient,
		kubeApplierDBClients:         kubeApplierDBClients,
		serviceProviderClusterLister: serviceProviderClusterLister,
	}

	return controllerutils.NewClusterWatchingController(
		"SystemAdminCredentialServingCAReadDesireCreator",
		resourcesDBClient,
		backendInformers,
		kubeApplierInformers,
		1*time.Minute,
		syncer,
	)
}

func (c *servingCAReadDesireCreator) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *servingCAReadDesireCreator) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	existingCluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}
	if existingCluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}
	if existingCluster.ServiceProviderProperties.ClusterServiceID == nil {
		return nil
	}

	// The root-ca ConfigMap that carries the public serving CA bundle only
	// exists in the hosted control plane namespace for OCP 4.20 and later.
	// Skip clusters below that version so we never create a ReadDesire that
	// could never be fulfilled.
	atLeastMinVersion, err := clusterVersionAtLeast(existingCluster.CustomerProperties.Version.ID, minServingCAConfigMapOCPVersion)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to evaluate cluster version for serving CA ReadDesire: %w", err))
	}
	if !atLeastMinVersion {
		return nil
	}

	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}
	mcResourceID := serviceProviderCluster.Status.ManagementClusterResourceID
	if mcResourceID == nil {
		return nil
	}

	// HyperShift publishes the root-ca ConfigMap into the hosted control plane
	// namespace, which is named "clusters-<clusterName>" where the cluster name
	// is the cluster's domain prefix. The prefix is mirrored from Cluster
	// Service asynchronously, so skip until it is populated and re-trigger on
	// relist.
	clusterName := existingCluster.CustomerProperties.DNS.BaseDomainPrefix
	if len(clusterName) == 0 {
		return nil
	}
	hcpNamespace := hostedControlPlaneNamespace(clusterName)

	target := kubeapplier.ResourceReference{
		Group:     "",
		Version:   "v1",
		Resource:  "configmaps",
		Namespace: hcpNamespace,
		Name:      servingCAConfigMapName,
	}

	desired, err := buildServingCAReadDesire(
		kubeapplier.ToClusterScopedReadDesireResourceIDString(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, readDesireNameServingCA),
		mcResourceID,
		target,
	)
	if err != nil {
		return err
	}

	kubeApplierClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
	if kubeApplierClient == nil {
		return nil
	}

	crud, err := kubeApplierClient.ReadDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ReadDesire CRUD: %w", err))
	}

	existing, err := crud.Get(ctx, readDesireNameServingCA)
	if database.IsNotFoundError(err) {
		existing = nil
	} else if err != nil {
		return utils.TrackError(fmt.Errorf("get ReadDesire: %w", err))
	}

	if existing == nil {
		if _, err := crud.Create(ctx, desired, nil); err != nil && !database.IsConflictError(err) {
			return utils.TrackError(fmt.Errorf("create serving CA ReadDesire: %w", err))
		}
		logger.Info("created serving CA ReadDesire")
		return nil
	}

	// Check if spec needs updating.
	if !controllerutil.ResourceIDsEqual(existing.Spec.ManagementCluster, desired.Spec.ManagementCluster) ||
		existing.Spec.TargetItem != desired.Spec.TargetItem {
		replacement := existing.DeepCopy()
		replacement.Spec = *desired.Spec.DeepCopy()
		if _, err := crud.Replace(ctx, replacement, nil); err != nil {
			return utils.TrackError(fmt.Errorf("replace serving CA ReadDesire: %w", err))
		}
		logger.Info("updated serving CA ReadDesire")
	}

	return nil
}

func buildServingCAReadDesire(resourceIDString string, managementCluster *azcorearm.ResourceID, target kubeapplier.ResourceReference) (*kubeapplier.ReadDesire, error) {
	resourceID, err := azcorearm.ParseResourceID(resourceIDString)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to parse ReadDesire resource ID %q: %w", resourceIDString, err))
	}
	return &kubeapplier.ReadDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(managementCluster.String()),
		},
		Spec: kubeapplier.ReadDesireSpec{
			ManagementCluster: managementCluster,
			TargetItem:        target,
		},
	}, nil
}

// hostedControlPlaneNamespace returns the HyperShift hosted control plane
// namespace on the management cluster for a cluster with the given name.
// HyperShift places the hosted control plane objects — including the root-ca
// ConfigMap that carries the public serving CA bundle — in
// "clusters-<clusterName>".
func hostedControlPlaneNamespace(clusterName string) string {
	return fmt.Sprintf("clusters-%s", clusterName)
}

// clusterVersionAtLeast reports whether the cluster's OCP version is greater
// than or equal to minVersion. An empty version string (not yet populated)
// reports false without error so the caller can skip until the version is
// known. ParseTolerant handles both "4.20" and "4.20.15" style inputs.
func clusterVersionAtLeast(versionID, minVersion string) (bool, error) {
	if len(versionID) == 0 {
		return false, nil
	}
	current, err := semver.ParseTolerant(versionID)
	if err != nil {
		return false, fmt.Errorf("failed to parse cluster version %q: %w", versionID, err)
	}
	minimum, err := semver.ParseTolerant(minVersion)
	if err != nil {
		return false, fmt.Errorf("failed to parse minimum version %q: %w", minVersion, err)
	}
	return current.GE(minimum), nil
}
