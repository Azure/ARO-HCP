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

package dataplaneworkloads

import (
	"context"
	"fmt"
	"strings"
	"time"

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	internalazure "github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const HostedClusterDataPlaneIdentitiesControllerName = "HostedClusterDataPlaneIdentities"

type hostedClusterDataPlaneIdentitySyncer struct {
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
}

func NewHostedClusterDataPlaneIdentitiesController(
	db corecosmosstorage.ResourcesDBClient,
	informers coreinformers.BackendInformers,
) controllerutils.Controller {
	_, clusters := informers.Clusters()
	_, spcs := informers.ServiceProviderClusters()
	syncer := &hostedClusterDataPlaneIdentitySyncer{
		resourcesDBClient:            db,
		clusterLister:                clusters,
		serviceProviderClusterLister: spcs,
	}
	controller := controllerutils.NewClusterWatchingController(
		HostedClusterDataPlaneIdentitiesControllerName, db, informers, nil, time.Minute, syncer,
	)
	controller.AddInterestingResource(coreapi.ClusterResourceType, "*")
	controller.AddInterestingResource(coreapi.ServiceProviderClusterResourceType, coreapi.ServiceProviderClusterResourceName)
	return controller
}

func (c *hostedClusterDataPlaneIdentitySyncer) needsWork(cluster *coreapi.Cluster, spc *coreapi.ServiceProviderCluster) bool {
	return cluster.ServiceProviderProperties.DeletionTimestamp == nil &&
		cluster.ServiceProviderProperties.ClusterServiceID != nil && spc.Status.ManagementClusterResourceID != nil
}

func (c *hostedClusterDataPlaneIdentitySyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	spc, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	if !c.needsWork(cluster, spc) {
		return nil
	}

	// Publish one complete set. Pending metadata or federation leaves the last
	// complete intent in place until a later informer update.
	ids := hsv1beta1.DataPlaneManagedIdentities{
		ImageRegistryMSIClientID: ensuredOperatorClientID(cluster, spc, internalazure.ClusterOperatorIdentifierImageRegistry),
		DiskMSIClientID:          ensuredOperatorClientID(cluster, spc, internalazure.ClusterOperatorIdentifierDiskCSIDriver),
		FileMSIClientID:          ensuredOperatorClientID(cluster, spc, internalazure.ClusterOperatorIdentifierFileCSIDriver),
	}
	if len(ids.ImageRegistryMSIClientID) == 0 || len(ids.DiskMSIClientID) == 0 || len(ids.FileMSIClientID) == 0 {
		return nil
	}

	hc := spc.Spec.DesiredHostedCluster
	if hc != nil && hc.Spec.Platform.Azure != nil {
		if mi := hc.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities; mi != nil && mi.DataPlane == ids {
			return nil
		}
	}
	replacement := spc.DeepCopy()
	if replacement.Spec.DesiredHostedCluster == nil {
		replacement.Spec.DesiredHostedCluster = &hsv1beta1.HostedCluster{}
	}
	platform := &replacement.Spec.DesiredHostedCluster.Spec.Platform
	if platform.Azure == nil {
		platform.Azure = &hsv1beta1.AzurePlatformSpec{}
	}
	auth := &platform.Azure.AzureAuthenticationConfig
	if auth.ManagedIdentities == nil {
		auth.ManagedIdentities = &hsv1beta1.AzureResourceManagedIdentities{}
	}
	auth.ManagedIdentities.DataPlane = ids
	_, err = c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, replacement, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("replace desired HostedCluster data plane identities: %w", err))
	}
	return nil
}

func ensuredOperatorClientID(cluster *coreapi.Cluster, spc *coreapi.ServiceProviderCluster, operator internalazure.ClusterOperatorIdentifier) string {
	id := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators[string(operator)]
	if id == nil {
		return ""
	}
	key := strings.ToLower(id.String())
	metadata := spc.Status.ManagedIdentityDetails[key]
	if metadata == nil {
		return ""
	}
	arm := metadata.MetadataFromARMUserAssignedIdentitiesAPI
	if arm == nil || !coreapihelpers.IdentityMetadataValueHasResolvedIdentityInformation(arm) {
		return ""
	}
	assignment := spc.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[coreapi.DataplaneOIDCFederationAssignmentKey{
		OperatorName: string(operator), IdentityResourceID: key,
	}]
	target := coreapi.DataplaneOIDCFederationIdentityInstance{
		ClientID:    *arm.ClientID,
		PrincipalID: *arm.PrincipalID,
		TenantID:    *arm.TenantID,
	}
	if assignment == nil || assignment.TargetIdentity != target || !coreapihelpers.IsDataplaneOIDCFederationAssignmentTargetIdentityEnsured(assignment) {
		return ""
	}
	return *arm.ClientID
}
