// Copyright 2025 Microsoft Corporation
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

package controllers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	// allPrincipalsGUID is used as the specified principal id in the deny assignment.
	// The zero GUID in Azure when specified for a principal represents "All Principals".
	// This includes all users, groups, service principals and managed identities.
	allPrincipalsGUID = "00000000-0000-0000-0000-000000000000"

	// denyAssignmentNamespaceUUID is the UUID used as the Namespace ID
	// for generating Azure Deny Assignments id using UUIDv5.
	// This value was generated using UUIDv4 and must not be changed
	// as it affects deny assignment ID generation.
	denyAssignmentNamespaceUUID = "f75040b8-d8aa-4311-bda6-ba8af06db258"

	// denyAssignmentResourceIDFormat is the format string for Azure deny assignment resource IDs
	denyAssignmentResourceIDFormat = "/subscriptions/%s/resourceGroups/%s/providers/" +
		"Microsoft.Authorization/denyAssignments/%s"

	// azureAPIVersion is the API version used for Azure Resource Manager operations
	// https://learn.microsoft.com/en-us/rest/api/authorization/deny-assignments
	azureAPIVersion = "2022-04-01"

	// Operator names for managed identity lookups
	operatorClusterAPIAzure        = "cluster-api-azure"
	operatorCloudControllerManager = "cloud-controller-manager"
	operatorDiskCSIDriver          = "disk-csi-driver"
	operatorControlPlane           = "control-plane"
	operatorImageRegistry          = "image-registry"
	operatorFileCSIDriver          = "file-csi-driver"
	operatorKMS                    = "kms"
	operatorIngress                = "ingress"
	operatorCloudNetworkConfig     = "cloud-network-config"
)

// DenyAssignmentReconciler handles the creation of Azure Deny Assignments for HCP clusters
type DenyAssignmentReconciler interface {
	// ReconcileDenyAssignments reconciles all deny assignments are created for the cluster
	ReconcileDenyAssignments(ctx context.Context, cluster *api.HCPOpenShiftCluster) (bool, error)
}

// denyAssignmentSyncer implements the ClusterSyncer interface for deny assignment management
type denyAssignmentSyncer struct {
	cosmosClient database.DBClient
	reconciler   DenyAssignmentReconciler
}

// NewDenyAssignmentController creates a new controller that reconciles deny assignments
// are created for all HCP clusters. The controller periodically scans all clusters
// and creates/updates deny assignments as needed.
func NewDenyAssignmentController(
	cosmosClient database.DBClient,
	subscriptionLister listers.SubscriptionLister,
	credential azcore.TokenCredential,
	resyncDuration time.Duration,
) controllerutils.Controller {
	reconciler := NewDenyAssignmentReconciler(credential)
	syncer := &denyAssignmentSyncer{
		cosmosClient: cosmosClient,
		reconciler:   reconciler,
	}

	return controllerutils.NewClusterWatchingController(
		"DenyAssignment",
		cosmosClient,
		subscriptionLister,
		resyncDuration,
		syncer,
	)
}

// SyncOnce reconciles deny assignments for a single cluster
func (s *denyAssignmentSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	// Fetch the cluster from Cosmos DB
	cluster, err := s.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsResponseError(err, http.StatusNotFound) {
		logger.Debug("cluster not found, skipping deny assignment sync")
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get HCP cluster: %w", err))
	}

	// Only process clusters that are in a stable state
	if !isClusterReadyForDenyAssignments(cluster) {
		logger.Debug("cluster not ready for deny assignments",
			"provisioning_state", cluster.ServiceProviderProperties.ProvisioningState,
		)
		return nil
	}

	// Reconcile deny assignments
	allCreated, err := s.reconciler.ReconcileDenyAssignments(ctx, cluster)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to reconcile deny assignments: %w", err))
	}

	if allCreated {
		logger.Info("all deny assignments are in place")
	} else {
		logger.Info("some deny assignments are still being created")
	}

	return nil
}

// isClusterReadyForDenyAssignments checks if a cluster is in a state where
// deny assignments should be managed
func isClusterReadyForDenyAssignments(cluster *api.HCPOpenShiftCluster) bool {
	// Only process clusters that are Succeeded or Updating
	// Skip clusters that are Creating, Deleting, or Failed
	switch cluster.ServiceProviderProperties.ProvisioningState {
	case arm.ProvisioningStateSucceeded, arm.ProvisioningStateUpdating:
		return true
	default:
		return false
	}
}

type denyAssignmentReconciler struct {
	credential azcore.TokenCredential
}

// NewDenyAssignmentReconciler creates a new deny assignment reconciler
func NewDenyAssignmentReconciler(credential azcore.TokenCredential) DenyAssignmentReconciler {
	return &denyAssignmentReconciler{
		credential: credential,
	}
}

// ReconcileDenyAssignments creates all 21 deny assignments for the cluster.
// This implements a "default deny" security posture with specific exceptions
// for OpenShift managed identities that need access.
//
// The implementation creates 21 separate deny assignments instead of one
// monolithic assignment due to Azure's ≤10 excluded principals limit per deny assignment.
func (c *denyAssignmentReconciler) ReconcileDenyAssignments(ctx context.Context, cluster *api.HCPOpenShiftCluster) (bool, error) {
	logger := utils.LoggerFromContext(ctx)

	// Validate required fields
	if cluster.CustomerProperties.Platform.ManagedResourceGroup == "" {
		return false, utils.TrackError(fmt.Errorf("managed resource group is not set"))
	}

	// Get subscription ID from cluster resource ID
	subscriptionID := cluster.ID.SubscriptionID
	managedRGName := cluster.CustomerProperties.Platform.ManagedResourceGroup

	// Fetch principal IDs for all operator managed identities
	principalIDCache, err := c.fetchPrincipalIDs(ctx, cluster, subscriptionID)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("failed to fetch principal IDs: %w", err))
	}

	// Create resources client
	resourcesClient, err := armresources.NewClient(subscriptionID, c.credential, nil)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("failed to create resources client: %w", err))
	}

	// Define all deny assignment reconcilers
	denyAssignmentReconcilers := []struct {
		suffix              string
		controlPlaneOps     []string
		dataPlaneOps        []string
		addServiceMI        bool
		actions             []string
		notActions          []string
		dataActions         []string
		conditionalOperator string // For KMS
	}{
		{
			suffix:          "resources-deny-assignment",
			controlPlaneOps: []string{operatorClusterAPIAzure, operatorControlPlane, operatorImageRegistry, operatorDiskCSIDriver},
			dataPlaneOps:    []string{operatorImageRegistry, operatorDiskCSIDriver},
			addServiceMI:    false,
			actions: []string{
				"Microsoft.Resources/subscriptions/resourceGroups/delete",
				"Microsoft.Resources/subscriptions/resourceGroups/read",
				"Microsoft.Resources/subscriptions/resourceGroups/write",
				"Microsoft.Resources/deployments/delete",
				"Microsoft.Resources/deployments/write",
			},
			notActions: []string{
				"Microsoft.Resources/tags/*",
			},
		},
		{
			suffix:          "compute-deny-assignment",
			controlPlaneOps: []string{operatorClusterAPIAzure, operatorCloudControllerManager, operatorDiskCSIDriver, operatorCloudNetworkConfig},
			dataPlaneOps:    []string{operatorDiskCSIDriver},
			actions: []string{
				"Microsoft.Compute/availabilitySets/delete",
				"Microsoft.Compute/availabilitySets/write",
				"Microsoft.Compute/disks/beginGetAccess/action",
				"Microsoft.Compute/disks/delete",
				"Microsoft.Compute/disks/endGetAccess/action",
				"Microsoft.Compute/disks/write",
				"Microsoft.Compute/images/delete",
				"Microsoft.Compute/images/write",
				"Microsoft.Compute/snapshots/beginGetAccess/action",
				"Microsoft.Compute/snapshots/delete",
				"Microsoft.Compute/snapshots/endGetAccess/action",
				"Microsoft.Compute/snapshots/write",
				"Microsoft.Compute/availabilitySets/read",
				"Microsoft.Compute/diskEncryptionSets/read",
				"Microsoft.Compute/disks/read",
				"Microsoft.Compute/locations/DiskOperations/read",
				"Microsoft.Compute/locations/operations/read",
				"Microsoft.Compute/snapshots/read",
				"Microsoft.Compute/virtualMachineScaleSets/read",
				"Microsoft.Compute/virtualMachineScaleSets/virtualMachines/read",
				"Microsoft.Compute/virtualMachineScaleSets/virtualMachines/write",
				"Microsoft.Compute/virtualMachines/delete",
				"Microsoft.Compute/virtualMachines/read",
				"Microsoft.Compute/virtualMachines/write",
			},
			notActions: []string{
				"Microsoft.Compute/disks/beginGetAccess/action",
				"Microsoft.Compute/disks/endGetAccess/action",
				"Microsoft.Compute/disks/write",
				"Microsoft.Compute/snapshots/beginGetAccess/action",
				"Microsoft.Compute/snapshots/delete",
				"Microsoft.Compute/snapshots/endGetAccess/action",
				"Microsoft.Compute/snapshots/write",
			},
		},
		{
			suffix:          "resourcehealth-deny-assignment",
			controlPlaneOps: []string{operatorClusterAPIAzure},
			actions: []string{
				"Microsoft.ResourceHealth/events/action",
			},
		},
		{
			suffix:          "apimanagement-deny-assignment",
			controlPlaneOps: []string{operatorClusterAPIAzure},
			actions: []string{
				"Microsoft.ApiManagement/service/groups/delete",
				"Microsoft.ApiManagement/service/groups/read",
				"Microsoft.ApiManagement/service/groups/write",
				"Microsoft.ApiManagement/service/workspaces/tags/read",
				"Microsoft.ApiManagement/service/workspaces/tags/write",
			},
		},
		{
			suffix:          "storage-deny-assignment",
			controlPlaneOps: []string{operatorImageRegistry, operatorFileCSIDriver},
			dataPlaneOps:    []string{operatorImageRegistry, operatorFileCSIDriver},
			addServiceMI:    true,
			actions: []string{
				"Microsoft.Storage/storageAccounts/read",
				"Microsoft.Storage/storageAccounts/write",
				"Microsoft.Storage/storageAccounts/delete",
				"Microsoft.Storage/storageAccounts/listKeys/action",
				"Microsoft.Storage/storageAccounts/regeneratekey/action",
				"Microsoft.Storage/storageAccounts/blobServices/read",
				"Microsoft.Storage/storageAccounts/blobServices/write",
				"Microsoft.Storage/storageAccounts/blobServices/containers/delete",
				"Microsoft.Storage/storageAccounts/blobServices/containers/read",
				"Microsoft.Storage/storageAccounts/blobServices/containers/write",
				"Microsoft.Storage/storageAccounts/blobServices/generateUserDelegationKey/action",
				"Microsoft.Storage/storageAccounts/fileServices/read",
				"Microsoft.Storage/storageAccounts/fileServices/write",
				"Microsoft.Storage/storageAccounts/fileServices/shares/read",
				"Microsoft.Storage/storageAccounts/fileServices/shares/write",
				"Microsoft.Storage/storageAccounts/fileServices/shares/delete",
				"Microsoft.Storage/storageAccounts/PrivateEndpointConnectionsApproval/action",
				"Microsoft.Storage/operations/read",
			},
			dataActions: []string{
				"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read",
				"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/write",
				"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/delete",
				"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/add/action",
				"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/move/action",
				"Microsoft.Storage/storageAccounts/fileServices/fileshares/files/read",
				"Microsoft.Storage/storageAccounts/fileServices/fileshares/files/write",
				"Microsoft.Storage/storageAccounts/fileServices/fileshares/files/delete",
			},
		},
		{
			suffix:          "managedidentity-deny-assignment",
			controlPlaneOps: []string{operatorControlPlane, operatorDiskCSIDriver},
			dataPlaneOps:    []string{operatorDiskCSIDriver},
			addServiceMI:    true,
			actions: []string{
				"Microsoft.ManagedIdentity/userAssignedIdentities/assign/action",
				"Microsoft.ManagedIdentity/userAssignedIdentities/read",
				"Microsoft.ManagedIdentity/userAssignedIdentities/write",
				"Microsoft.ManagedIdentity/userAssignedIdentities/delete",
				"Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/read",
				"Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/write",
				"Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/delete",
			},
		},
		{
			suffix:          "classicstorage-deny-assignment",
			controlPlaneOps: []string{operatorClusterAPIAzure},
			actions: []string{
				"Microsoft.ClassicStorage/storageAccounts/vmImages/read",
				"Microsoft.ClassicStorage/storageAccounts/vmImages/write",
			},
		},
		{
			suffix:              "keyvault-deny-assignment",
			controlPlaneOps:     []string{operatorDiskCSIDriver},
			dataPlaneOps:        []string{operatorDiskCSIDriver},
			conditionalOperator: operatorKMS, // Include KMS only if KMS encryption is enabled
			actions: []string{
				"Microsoft.KeyVault/vaults/deploy/action",
			},
			dataActions: []string{
				"Microsoft.KeyVault/vaults/keys/read",
				"Microsoft.KeyVault/vaults/keys/update/action",
				"Microsoft.KeyVault/vaults/keys/backup/action",
				"Microsoft.KeyVault/vaults/keys/encrypt/action",
				"Microsoft.KeyVault/vaults/keys/decrypt/action",
				"Microsoft.KeyVault/vaults/keys/wrap/action",
				"Microsoft.KeyVault/vaults/keys/unwrap/action",
				"Microsoft.KeyVault/vaults/keys/sign/action",
				"Microsoft.KeyVault/vaults/keys/verify/action",
			},
		},
		{
			suffix:          "containerservice-deny-assignment",
			controlPlaneOps: []string{operatorClusterAPIAzure},
			actions: []string{
				"Microsoft.ContainerService/managedClusters/agentPools/write",
				"Microsoft.ContainerService/managedClusters/delete",
				"Microsoft.ContainerService/managedClusters/write",
			},
		},
		{
			suffix:          "authorization-deny-assignment",
			controlPlaneOps: []string{operatorClusterAPIAzure},
			actions: []string{
				"Microsoft.Authorization/roleAssignments/read",
				"Microsoft.Authorization/roleAssignments/write",
			},
		},
		{
			suffix:          "network-vnet-mgmt-deny-assignment",
			controlPlaneOps: []string{operatorClusterAPIAzure, operatorFileCSIDriver, operatorCloudControllerManager},
			dataPlaneOps:    []string{operatorFileCSIDriver},
			addServiceMI:    true,
			actions: []string{
				"Microsoft.Network/virtualNetworks/delete",
				"Microsoft.Network/virtualNetworks/write",
				"Microsoft.Network/virtualNetworks/subnets/delete",
				"Microsoft.Network/virtualNetworks/subnets/write",
			},
		},
		{
			suffix: "network-vnet-read-deny-assignment",
			controlPlaneOps: []string{operatorClusterAPIAzure, operatorCloudControllerManager, operatorControlPlane,
				operatorImageRegistry, operatorIngress, operatorFileCSIDriver, operatorCloudNetworkConfig},
			dataPlaneOps: []string{operatorImageRegistry, operatorFileCSIDriver},
			addServiceMI: true,
			actions: []string{
				"Microsoft.Network/virtualNetworks/read",
				"Microsoft.Network/virtualNetworks/subnets/read",
				"Microsoft.Network/virtualNetworks/virtualNetworkPeerings/read",
			},
		},
		{
			suffix: "network-vnet-join-deny-assignment",
			controlPlaneOps: []string{operatorClusterAPIAzure, operatorCloudControllerManager, operatorImageRegistry,
				operatorIngress, operatorCloudNetworkConfig, operatorDiskCSIDriver, operatorFileCSIDriver},
			dataPlaneOps: []string{operatorImageRegistry, operatorFileCSIDriver, operatorDiskCSIDriver},
			actions: []string{
				"Microsoft.Network/virtualNetworks/join/action",
				"Microsoft.Network/virtualNetworks/subnets/join/action",
			},
		},
		{
			suffix: "network-loadbalancing-deny-assignment",
			controlPlaneOps: []string{operatorClusterAPIAzure, operatorCloudControllerManager, operatorControlPlane,
				operatorCloudNetworkConfig, operatorDiskCSIDriver, operatorFileCSIDriver},
			dataPlaneOps: []string{operatorDiskCSIDriver, operatorFileCSIDriver},
			addServiceMI: true,
			actions: []string{
				"Microsoft.Network/loadBalancers/inboundNATRules/join/action",
				"Microsoft.Network/loadBalancers/loadBalancingRules/read",
				"Microsoft.Network/loadBalancers/read",
				"Microsoft.Network/loadBalancers/write",
				"Microsoft.Network/loadBalancers/delete",
				"Microsoft.Network/loadBalancers/backendAddressPools/join/action",
				"Microsoft.Network/loadBalancers/frontendIPConfigurations/join/action",
				"Microsoft.Network/loadBalancers/inboundNatRules/join/action",
				"Microsoft.Network/loadBalancers/probes/join/action",
				"Microsoft.Network/publicIPAddresses/read",
				"Microsoft.Network/publicIPAddresses/write",
				"Microsoft.Network/publicIPAddresses/delete",
				"Microsoft.Network/publicIPAddresses/join/action",
				"Microsoft.Network/publicIPPrefixes/join/action",
				"Microsoft.Network/routeTables/read",
				"Microsoft.Network/routeTables/write",
				"Microsoft.Network/routeTables/delete",
				"Microsoft.Network/routeTables/join/action",
			},
		},
		{
			suffix: "network-privateconn-deny-assignment",
			controlPlaneOps: []string{operatorClusterAPIAzure, operatorImageRegistry, operatorIngress,
				operatorFileCSIDriver, operatorCloudControllerManager},
			dataPlaneOps: []string{operatorImageRegistry, operatorFileCSIDriver},
			actions: []string{
				"Microsoft.Network/privatelinkservices/delete",
				"Microsoft.Network/privatelinkservices/read",
				"Microsoft.Network/privatelinkservices/write",
				"Microsoft.Network/privateEndpoints/read",
				"Microsoft.Network/privateEndpoints/write",
				"Microsoft.Network/privateEndpoints/delete",
				"Microsoft.Network/privateDnsOperationStatuses/read",
				"Microsoft.Network/privateDnsZones/join/action",
				"Microsoft.Network/privateDnsZones/read",
				"Microsoft.Network/privateDnsZones/virtualNetworkLinks/read",
				"Microsoft.Network/privateEndpoints/privateDnsZoneGroups/read",
				"Microsoft.Network/privateEndpoints/privateDnsZoneGroups/write",
				"Microsoft.Network/privateDnsZones/write",
				"Microsoft.Network/privateDnsZones/delete",
				"Microsoft.Network/privateDnsZones/A/write",
				"Microsoft.Network/privateDnsZones/A/delete",
				"Microsoft.Network/privateDnsZones/virtualNetworkLinks/write",
				"Microsoft.Network/privateDnsZones/virtualNetworkLinks/delete",
				"Microsoft.Network/dnsZones/write",
				"Microsoft.Network/dnsZones/delete",
				"Microsoft.Network/dnsZones/A/write",
				"Microsoft.Network/dnsZones/A/delete",
				"Microsoft.Network/locations/operations/read",
			},
		},
		{
			suffix: "network-securitygroups-deny-assignment",
			controlPlaneOps: []string{operatorClusterAPIAzure, operatorCloudControllerManager, operatorControlPlane,
				operatorDiskCSIDriver, operatorFileCSIDriver},
			dataPlaneOps: []string{operatorDiskCSIDriver, operatorFileCSIDriver},
			addServiceMI: true,
			actions: []string{
				"Microsoft.Network/networkSecurityGroups/read",
				"Microsoft.Network/networkSecurityGroups/write",
				"Microsoft.Network/networkSecurityGroups/delete",
				"Microsoft.Network/networkSecurityGroups/join/action",
				"Microsoft.Network/natGateways/join/action",
				"Microsoft.Network/natGateways/read",
			},
		},
		{
			suffix:          "network-appsecuritygroups-deny-assignment",
			controlPlaneOps: []string{operatorClusterAPIAzure, operatorCloudControllerManager, operatorControlPlane, operatorDiskCSIDriver},
			dataPlaneOps:    []string{operatorDiskCSIDriver},
			actions: []string{
				"Microsoft.Network/applicationSecurityGroups/read",
				"Microsoft.Network/applicationSecurityGroups/write",
				"Microsoft.Network/applicationSecurityGroups/delete",
				"Microsoft.Network/applicationSecurityGroups/joinNetworkSecurityRule/action",
				"Microsoft.Network/applicationSecurityGroups/joinIpConfiguration/action",
			},
		},
		{
			suffix: "network-interfaces-deny-assignment",
			controlPlaneOps: []string{operatorClusterAPIAzure, operatorCloudControllerManager, operatorControlPlane,
				operatorImageRegistry, operatorCloudNetworkConfig, operatorDiskCSIDriver},
			dataPlaneOps: []string{operatorImageRegistry, operatorDiskCSIDriver},
			actions: []string{
				"Microsoft.Network/networkInterfaces/read",
				"Microsoft.Network/networkInterfaces/write",
				"Microsoft.Network/networkInterfaces/delete",
				"Microsoft.Network/networkInterfaces/join/action",
				"Microsoft.Network/networkInterfaces/loadBalancers/read",
				"Microsoft.Network/networkInterfaces/effectiveRouteTable/action",
			},
			notActions: []string{
				"Microsoft.Network/networkInterfaces/effectiveRouteTable/action",
				"Microsoft.Network/networkSecurityGroups/join/action",
			},
		},
		{
			suffix:          "network-policies-services-deny-assignment",
			controlPlaneOps: []string{operatorFileCSIDriver, operatorCloudControllerManager},
			dataPlaneOps:    []string{operatorFileCSIDriver},
			actions: []string{
				"Microsoft.Network/serviceEndpointPolicies/read",
				"Microsoft.Network/serviceEndpointPolicies/write",
				"Microsoft.Network/serviceEndpointPolicies/delete",
				"Microsoft.Network/serviceEndpointPolicies/join/action",
				"Microsoft.Network/networkIntentPolicies/join/action",
				"Microsoft.Network/networkManagers/ipamPools/associateResourcesToPool/action",
			},
		},
		{
			suffix:          "network-bastionhosts-deny-assignment",
			controlPlaneOps: []string{operatorClusterAPIAzure},
			actions: []string{
				"Microsoft.Network/bastionHosts/write",
				"Microsoft.Network/bastionHosts/delete",
			},
		},
		{
			// This uses a wildcard approach to deny write, delete, and action operations on
			// ALL Azure Resource Providers except those explicitly allowed in notActions.
			// Strategy: Deny all mutating operations (write/delete/action), then carve out
			// exceptions for RPs that OpenShift needs and user-facing RPs (Insights, PolicyInsights).
			suffix: "deny-all-other-rps-deny-assignment",
			actions: []string{
				"*/action", // Deny all action-type operations across all RPs
				"*/delete", // Deny all delete operations across all RPs
				"*/write",  // Deny all write operations across all RPs
			},
			// Lists Resource Providers that are either:
			// 1. Required by OpenShift operators for cluster functionality
			// 2. User-facing RPs for monitoring, compliance, and management
			//
			// Similar to ARO-Classic's approach:
			// github.com/Azure/ARO-RP/blob/master/pkg/cluster/deploybaseresources_additional.go#L56
			notActions: []string{
				"Microsoft.Resources/*",        // Resource groups, deployments, tags
				"Microsoft.Compute/*",          // VMs, disks, snapshots, availability sets
				"Microsoft.Storage/*",          // Storage accounts, blobs, files
				"Microsoft.Network/*",          // VNets, subnets, load balancers, NSGs, NICs, DNS
				"Microsoft.ManagedIdentity/*",  // User-assigned managed identities and federated credentials
				"Microsoft.KeyVault/*",         // Key vaults for encryption (KMS)
				"Microsoft.Authorization/*",    // Role assignments
				"Microsoft.ContainerService/*", // Container service operations
				"Microsoft.ResourceHealth/*",   // Resource health monitoring
				"Microsoft.ApiManagement/*",    // API management operations
				"Microsoft.ClassicStorage/*",   // Legacy storage operations
				"Microsoft.Insights/*",         // Monitoring, alerts, and metrics
				"Microsoft.PolicyInsights/*",   // Policy compliance and remediation
			},
		},
	}

	var errs []error
	allCreated := true

	for _, da := range denyAssignmentReconcilers {
		// Handle conditional KMS operator
		controlPlaneOps := da.controlPlaneOps
		if da.conditionalOperator == operatorKMS && isKMSEncryptionEnabled(cluster) {
			controlPlaneOps = append(controlPlaneOps, operatorKMS)
			logger.Debug("KMS encryption enabled - including KMS managed identity in KeyVault deny assignment")
		}

		created, err := c.reconcileDenyAssignment(
			ctx,
			resourcesClient,
			cluster.ID.String(),
			subscriptionID,
			managedRGName,
			da.suffix,
			controlPlaneOps,
			da.dataPlaneOps,
			da.addServiceMI,
			da.actions,
			da.notActions,
			da.dataActions,
			principalIDCache,
		)
		if err != nil {
			errs = append(errs, utils.TrackError(fmt.Errorf("failed to create %s: %w", da.suffix, err)))
		}
		allCreated = allCreated && created
	}

	if len(errs) > 0 {
		return false, errors.Join(errs...)
	}

	return allCreated, nil
}

// fetchPrincipalIDs fetches principal IDs for all operator managed identities
func (c *denyAssignmentReconciler) fetchPrincipalIDs(ctx context.Context, cluster *api.HCPOpenShiftCluster, subscriptionID string) (map[string]string, error) {
	logger := utils.LoggerFromContext(ctx)
	principalIDs := make(map[string]string)

	msiClient, err := armmsi.NewUserAssignedIdentitiesClient(subscriptionID, c.credential, nil)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create MSI client: %w", err))
	}

	operatorsAuth := cluster.CustomerProperties.Platform.OperatorsAuthentication
	if operatorsAuth.UserAssignedIdentities.ControlPlaneOperators == nil {
		return nil, utils.TrackError(fmt.Errorf("control plane operators not configured"))
	}

	// Fetch control plane operator principal IDs
	for opName, resourceID := range operatorsAuth.UserAssignedIdentities.ControlPlaneOperators {
		if resourceID == nil {
			continue
		}
		principalID, err := c.fetchPrincipalID(ctx, msiClient, resourceID)
		if err != nil {
			logger.Warn("failed to fetch principal ID for control plane operator",
				"operator", opName, "error", err)
			continue
		}
		// Store with "cp-" prefix for control plane
		principalIDs["cp-"+opName] = principalID
	}

	// Fetch data plane operator principal IDs
	for opName, resourceID := range operatorsAuth.UserAssignedIdentities.DataPlaneOperators {
		if resourceID == nil {
			continue
		}
		principalID, err := c.fetchPrincipalID(ctx, msiClient, resourceID)
		if err != nil {
			logger.Warn("failed to fetch principal ID for data plane operator",
				"operator", opName, "error", err)
			continue
		}
		// Store with "dp-" prefix for data plane
		principalIDs["dp-"+opName] = principalID
	}

	// Fetch service managed identity principal ID
	if operatorsAuth.UserAssignedIdentities.ServiceManagedIdentity != nil {
		principalID, err := c.fetchPrincipalID(ctx, msiClient, operatorsAuth.UserAssignedIdentities.ServiceManagedIdentity)
		if err != nil {
			logger.Warn("failed to fetch principal ID for service managed identity", "error", err)
		} else {
			principalIDs["service"] = principalID
		}
	}

	return principalIDs, nil
}

// fetchPrincipalID fetches the principal ID for a managed identity
func (c *denyAssignmentReconciler) fetchPrincipalID(ctx context.Context, client *armmsi.UserAssignedIdentitiesClient, resourceID *azcorearm.ResourceID) (string, error) {
	resp, err := client.Get(ctx, resourceID.ResourceGroupName, resourceID.Name, nil)
	if err != nil {
		return "", utils.TrackError(fmt.Errorf("failed to get managed identity %s: %w", resourceID.String(), err))
	}

	if resp.Properties == nil || resp.Properties.PrincipalID == nil {
		return "", utils.TrackError(fmt.Errorf("principal ID not found for managed identity %s", resourceID.String()))
	}

	return *resp.Properties.PrincipalID, nil
}

// reconcileDenyAssignment creates or verifies a deny assignment exists
func (c *denyAssignmentReconciler) reconcileDenyAssignment(
	ctx context.Context,
	resourcesClient *armresources.Client,
	clusterID string,
	subscriptionID string,
	managedRGName string,
	assignmentSuffix string,
	controlPlaneOperators []string,
	dataPlaneOperators []string,
	addServiceMI bool,
	actions []string,
	notActions []string,
	dataActions []string,
	principalIDCache map[string]string,
) (bool, error) {
	logger := utils.LoggerFromContext(ctx)

	// Generate deterministic deny assignment ID
	denyAssignmentID := generateDenyAssignmentID(clusterID, assignmentSuffix)
	denyAssignmentResourceID := fmt.Sprintf(
		denyAssignmentResourceIDFormat,
		subscriptionID,
		managedRGName,
		denyAssignmentID,
	)

	// Check if deny assignment already exists
	_, err := resourcesClient.GetByID(ctx, denyAssignmentResourceID, azureAPIVersion, nil)
	if err == nil {
		logger.Debug("deny assignment already exists", "suffix", assignmentSuffix)
		return true, nil
	}

	// If not a 404 error, return the error
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) && respErr.StatusCode != http.StatusNotFound {
		return false, utils.TrackError(fmt.Errorf("failed to check deny assignment: %w", err))
	}

	// Collect principal IDs for excluded operators
	excludedPrincipalIDs, err := c.collectPrincipalIDs(
		controlPlaneOperators,
		dataPlaneOperators,
		addServiceMI,
		principalIDCache,
	)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("failed to collect principal IDs: %w", err))
	}

	// Build the deny assignment resource
	managedRGResourceID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionID, managedRGName)
	genericResource := createDenyAssignmentResource(
		managedRGResourceID,
		denyAssignmentID,
		excludedPrincipalIDs,
		actions,
		notActions,
		dataActions,
	)

	// Create the deny assignment
	poller, err := resourcesClient.BeginCreateOrUpdateByID(ctx, denyAssignmentResourceID, azureAPIVersion, genericResource, nil)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("failed to create deny assignment: %w", err))
	}

	// Poll for completion
	pollerRes, err := poller.Poll(ctx)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("failed to poll deny assignment creation: %w", err))
	}
	defer pollerRes.Body.Close()

	if !poller.Done() {
		logger.Info("deny assignment creation not yet complete", "suffix", assignmentSuffix)
		return false, nil
	}

	_, err = poller.Result(ctx)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("deny assignment creation failed: %w", err))
	}

	logger.Info("deny assignment created successfully", "suffix", assignmentSuffix)
	return true, nil
}

// collectPrincipalIDs collects principal IDs for operators from the cache
func (c *denyAssignmentReconciler) collectPrincipalIDs(
	controlPlaneOperators []string,
	dataPlaneOperators []string,
	addServiceMI bool,
	principalIDCache map[string]string,
) ([]string, error) {
	var principalIDs []string

	// Collect control plane operator principal IDs
	for _, opName := range controlPlaneOperators {
		principalID, ok := principalIDCache["cp-"+opName]
		if !ok {
			return nil, utils.TrackError(fmt.Errorf("principal ID not found for control plane operator: %s", opName))
		}
		principalIDs = append(principalIDs, principalID)
	}

	// Collect data plane operator principal IDs
	for _, opName := range dataPlaneOperators {
		principalID, ok := principalIDCache["dp-"+opName]
		if !ok {
			return nil, utils.TrackError(fmt.Errorf("principal ID not found for data plane operator: %s", opName))
		}
		principalIDs = append(principalIDs, principalID)
	}

	// Add service managed identity if requested
	if addServiceMI {
		principalID, ok := principalIDCache["service"]
		if !ok {
			return nil, utils.TrackError(fmt.Errorf("principal ID not found for service managed identity"))
		}
		principalIDs = append(principalIDs, principalID)
	}

	return principalIDs, nil
}

// generateDenyAssignmentID generates a deterministic UUID v5 for the deny assignment
func generateDenyAssignmentID(clusterID, suffix string) string {
	namespace := uuid.MustParse(denyAssignmentNamespaceUUID)
	return uuid.NewSHA1(namespace, []byte(clusterID+suffix)).String()
}

// createDenyAssignmentResource creates the ARM GenericResource for a deny assignment
func createDenyAssignmentResource(
	scope string,
	denyAssignmentID string,
	excludedPrincipalIDs []string,
	actions []string,
	notActions []string,
	dataActions []string,
) armresources.GenericResource {
	excludedPrincipals := make([]any, 0, len(excludedPrincipalIDs))
	for _, principalID := range excludedPrincipalIDs {
		excludedPrincipals = append(excludedPrincipals, map[string]any{
			"id":   principalID,
			"type": "ServicePrincipal",
		})
	}

	return armresources.GenericResource{
		Location: to.Ptr("global"),
		Properties: map[string]any{
			"DenyAssignmentName": denyAssignmentID,
			"Permissions": []any{
				map[string]any{
					"actions":        actions,
					"notActions":     notActions,
					"dataActions":    dataActions,
					"notDataActions": []string{},
				},
			},
			"Scope": scope,
			"Principals": []any{
				map[string]any{
					"id":   allPrincipalsGUID,
					"type": "SystemDefined",
				},
			},
			"ExcludePrincipals": excludedPrincipals,
			"IsSystemProtected": true,
		},
	}
}

// isKMSEncryptionEnabled checks if KMS encryption is enabled for the cluster
func isKMSEncryptionEnabled(cluster *api.HCPOpenShiftCluster) bool {
	etcd := cluster.CustomerProperties.Etcd
	if etcd.DataEncryption.CustomerManaged == nil {
		return false
	}
	return etcd.DataEncryption.CustomerManaged.EncryptionType == api.CustomerManagedEncryptionTypeKMS
}
