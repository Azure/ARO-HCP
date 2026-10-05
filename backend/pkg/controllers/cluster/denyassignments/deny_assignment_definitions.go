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

package denyassignments

import (
	"fmt"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
)

const (
	operatorClusterAPIAzure        = "cluster-api-azure"
	operatorCloudControllerManager = "cloud-controller-manager"
	operatorDiskCSIDriver          = "disk-csi-driver"
	operatorControlPlane           = "control-plane"
	operatorImageRegistry          = "image-registry"
	operatorFileCSIDriver          = "file-csi-driver"
	operatorKMS                    = "kms"
	operatorIngress                = "ingress"
	operatorCloudNetworkConfig     = "cloud-network-config"
	operatorAutoNode               = "autonode"

	denyAssignmentSuffixResources                = "resources-deny-assignment"
	denyAssignmentSuffixDenyAllOtherRPs          = "deny-all-other-rps-deny-assignment"
	denyAssignmentSuffixCompute                  = "compute-deny-assignment"
	denyAssignmentSuffixResourceHealth           = "resourcehealth-deny-assignment"
	denyAssignmentSuffixAPIManagement            = "apimanagement-deny-assignment"
	denyAssignmentSuffixStorage                  = "storage-deny-assignment"
	denyAssignmentSuffixManagedIdentity          = "managedidentity-deny-assignment"
	denyAssignmentSuffixKeyVault                 = "keyvault-deny-assignment"
	denyAssignmentSuffixContainerService         = "containerservice-deny-assignment"
	denyAssignmentSuffixNetworkVnetMgmt          = "network-vnet-mgmt-deny-assignment"
	denyAssignmentSuffixNetworkVnetJoin          = "network-vnet-join-deny-assignment"
	denyAssignmentSuffixNetworkLoadBalancing     = "network-loadbalancing-deny-assignment"
	denyAssignmentSuffixNetworkPrivateConn       = "network-privateconn-deny-assignment"
	denyAssignmentSuffixNetworkSecurityGroups    = "network-securitygroups-deny-assignment"
	denyAssignmentSuffixNetworkAppSecurityGroups = "network-appsecuritygroups-deny-assignment"
	denyAssignmentSuffixNetworkInterfaces        = "network-interfaces-deny-assignment"
	denyAssignmentSuffixNetworkPoliciesServices  = "network-policies-services-deny-assignment"
	denyAssignmentSuffixNetworkBastionHosts      = "network-bastionhosts-deny-assignment"

	denyAssignmentNamespaceUUID   = "f75040b8-d8aa-4311-bda6-ba8af06db258"
	denyAssignmentAzureAPIVersion = "2022-04-01"
	allPrincipalsGUID             = "00000000-0000-0000-0000-000000000000"
)

type denyAssignmentDefinition struct {
	denyAssignmentType      string
	controlPlaneOperators   []string
	dataPlaneOperators      []string
	includeServiceManagedID bool
	actions                 []string
	notActions              []string
	dataActions             []string
	conditionalKMS          bool
	conditionalAutoNode     bool
}

func denyAssignmentDefinitions(cluster *coreapi.Cluster) []denyAssignmentDefinition {
	defs := []denyAssignmentDefinition{
		{
			denyAssignmentType:      denyAssignmentSuffixResources,
			controlPlaneOperators:   []string{operatorClusterAPIAzure, operatorControlPlane, operatorImageRegistry, operatorDiskCSIDriver},
			dataPlaneOperators:      []string{operatorImageRegistry, operatorDiskCSIDriver},
			includeServiceManagedID: false,
			actions:                 resourcesActions(),
			notActions:              resourcesNotActions(),
		},
		{
			denyAssignmentType:      denyAssignmentSuffixCompute,
			controlPlaneOperators:   []string{operatorClusterAPIAzure, operatorCloudControllerManager, operatorDiskCSIDriver, operatorCloudNetworkConfig},
			dataPlaneOperators:      []string{operatorDiskCSIDriver},
			includeServiceManagedID: false,
			actions:                 computeActions(),
			notActions:              computeNotActions(),
			conditionalAutoNode:     true,
		},
		{
			denyAssignmentType:    denyAssignmentSuffixResourceHealth,
			controlPlaneOperators: []string{operatorClusterAPIAzure},
			actions:               resourceHealthActions(),
		},
		{
			denyAssignmentType:    denyAssignmentSuffixAPIManagement,
			controlPlaneOperators: []string{operatorClusterAPIAzure},
			actions:               apiManagementActions(),
		},
		{
			denyAssignmentType:      denyAssignmentSuffixStorage,
			controlPlaneOperators:   []string{operatorImageRegistry, operatorFileCSIDriver},
			dataPlaneOperators:      []string{operatorImageRegistry, operatorFileCSIDriver},
			includeServiceManagedID: true,
			actions:                 storageActions(),
			dataActions:             storageDataActions(),
		},
		{
			denyAssignmentType:      denyAssignmentSuffixManagedIdentity,
			controlPlaneOperators:   []string{operatorControlPlane, operatorDiskCSIDriver},
			dataPlaneOperators:      []string{operatorDiskCSIDriver},
			includeServiceManagedID: true,
			actions:                 managedIdentityActions(),
			conditionalAutoNode:     true,
		},
		{
			denyAssignmentType:    denyAssignmentSuffixKeyVault,
			controlPlaneOperators: []string{operatorDiskCSIDriver},
			dataPlaneOperators:    []string{operatorDiskCSIDriver},
			actions:               keyVaultActions(),
			dataActions:           keyVaultDataActions(),
			conditionalKMS:        true,
		},
		{
			denyAssignmentType:    denyAssignmentSuffixContainerService,
			controlPlaneOperators: []string{operatorClusterAPIAzure},
			actions:               containerServiceActions(),
		},
		{
			denyAssignmentType:      denyAssignmentSuffixNetworkVnetMgmt,
			controlPlaneOperators:   []string{operatorClusterAPIAzure, operatorFileCSIDriver, operatorCloudControllerManager},
			dataPlaneOperators:      []string{operatorFileCSIDriver},
			includeServiceManagedID: true,
			actions:                 networkVirtualNetworksManagementActions(),
		},
		{
			denyAssignmentType:    denyAssignmentSuffixNetworkVnetJoin,
			controlPlaneOperators: []string{operatorClusterAPIAzure, operatorCloudControllerManager, operatorImageRegistry, operatorIngress, operatorCloudNetworkConfig, operatorDiskCSIDriver, operatorFileCSIDriver},
			dataPlaneOperators:    []string{operatorImageRegistry, operatorFileCSIDriver, operatorDiskCSIDriver},
			actions:               networkVirtualNetworksJoinActions(),
			conditionalAutoNode:   true,
		},
		{
			denyAssignmentType:      denyAssignmentSuffixNetworkLoadBalancing,
			controlPlaneOperators:   []string{operatorClusterAPIAzure, operatorCloudControllerManager, operatorControlPlane, operatorCloudNetworkConfig, operatorDiskCSIDriver, operatorFileCSIDriver},
			dataPlaneOperators:      []string{operatorDiskCSIDriver, operatorFileCSIDriver},
			includeServiceManagedID: true,
			actions:                 networkLoadBalancingPublicIPAndRouteTablesActions(),
		},
		{
			denyAssignmentType:      denyAssignmentSuffixNetworkPrivateConn,
			controlPlaneOperators:   []string{operatorClusterAPIAzure, operatorImageRegistry, operatorIngress, operatorFileCSIDriver, operatorCloudControllerManager},
			dataPlaneOperators:      []string{operatorImageRegistry, operatorFileCSIDriver},
			includeServiceManagedID: true,
			actions:                 networkPrivateConnectivityActions(),
		},
		{
			// AutoNode's Virtual Machine Contributor role does grant
			// networkSecurityGroups/join/action, needed only if the customer's
			// subnet has an NSG attached to the NICs Karpenter creates. That's
			// speculative rather than a documented requirement (unlike the VM/NIC/
			// disk lifecycle, subnet join, and identity-assign actions excluded
			// below), so this definition is deliberately left without
			// conditionalAutoNode: under-scoping and revisiting later if a real
			// NSG-attached-subnet scenario surfaces is safer than pre-emptively
			// widening the exclusion on a guess.
			denyAssignmentType:      denyAssignmentSuffixNetworkSecurityGroups,
			controlPlaneOperators:   []string{operatorClusterAPIAzure, operatorCloudControllerManager, operatorControlPlane, operatorDiskCSIDriver, operatorFileCSIDriver},
			dataPlaneOperators:      []string{operatorDiskCSIDriver, operatorFileCSIDriver},
			includeServiceManagedID: true,
			actions:                 networkSecurityGroupsAndNatGatewaysActions(),
		},
		{
			denyAssignmentType:    denyAssignmentSuffixNetworkAppSecurityGroups,
			controlPlaneOperators: []string{operatorClusterAPIAzure, operatorCloudControllerManager, operatorControlPlane, operatorDiskCSIDriver},
			dataPlaneOperators:    []string{operatorDiskCSIDriver},
			actions:               applicationSecurityGroupsActions(),
		},
		{
			denyAssignmentType:    denyAssignmentSuffixNetworkInterfaces,
			controlPlaneOperators: []string{operatorClusterAPIAzure, operatorCloudControllerManager, operatorControlPlane, operatorImageRegistry, operatorCloudNetworkConfig, operatorDiskCSIDriver},
			dataPlaneOperators:    []string{operatorImageRegistry, operatorDiskCSIDriver},
			actions:               networkInterfacesActions(),
			notActions:            networkInterfacesNotActions(),
			conditionalAutoNode:   true,
		},
		{
			denyAssignmentType:    denyAssignmentSuffixNetworkPoliciesServices,
			controlPlaneOperators: []string{operatorFileCSIDriver, operatorCloudControllerManager},
			dataPlaneOperators:    []string{operatorFileCSIDriver},
			actions:               networkPoliciesAndServicesActions(),
		},
		{
			denyAssignmentType:    denyAssignmentSuffixNetworkBastionHosts,
			controlPlaneOperators: []string{operatorClusterAPIAzure},
			actions:               bastionHostsActions(),
		},
		{
			denyAssignmentType: denyAssignmentSuffixDenyAllOtherRPs,
			actions:            denyAllOtherRPsActions(),
			notActions:         denyAllOtherRPsNotActions(),
		},
	}

	// For KeyVault, conditionally add KMS operator exclusion
	// For AutoNode, conditionally add Karpenter operator exclusion
	for i := range defs {
		if defs[i].conditionalKMS && isKMSEncryptionEnabled(cluster) {
			defs[i].controlPlaneOperators = append(defs[i].controlPlaneOperators, operatorKMS)
		}
		if defs[i].conditionalAutoNode && isAutoNodeEnabled(cluster) && autoNodeIdentityPresent(cluster) {
			defs[i].dataPlaneOperators = append(defs[i].dataPlaneOperators, operatorAutoNode)
		}
	}

	return defs
}

func allDenyAssignmentReferences(cluster *coreapi.Cluster) ([]coreapi.DenyAssignmentReference, error) {
	defs := denyAssignmentDefinitions(cluster)
	csClusterID := controllerutils.ClusterServiceIDForCluster(cluster)
	subscriptionID := cluster.ID.SubscriptionID
	managedResourceGroup := cluster.CustomerProperties.Platform.ManagedResourceGroup

	denyAssignmentReferences := make([]coreapi.DenyAssignmentReference, 0, len(defs))
	for _, d := range defs {
		daUUID := generateDenyAssignmentUUID(csClusterID, d.denyAssignmentType)
		azureResourceID, err := coreapihelpers.ToDenyAssignmentResourceID(subscriptionID, managedResourceGroup, daUUID)
		if err != nil {
			return nil, fmt.Errorf("failed to build deny assignment resource ID for %s: %w", d.denyAssignmentType, err)
		}
		denyAssignmentReferences = append(denyAssignmentReferences, coreapi.DenyAssignmentReference{
			DenyAssignmentType:       d.denyAssignmentType,
			DenyAssignmentResourceID: azureResourceID,
		})
	}
	return denyAssignmentReferences, nil
}

func isKMSEncryptionEnabled(cluster *coreapi.Cluster) bool {
	return cluster.CustomerProperties.Etcd.DataEncryption.KeyManagementMode == metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged &&
		cluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged != nil &&
		cluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms != nil
}

func isAutoNodeEnabled(cluster *coreapi.Cluster) bool {
	return cluster.ServiceProviderProperties.ExperimentalFeatures.AutoNode == coreapi.AutoNode
}

func autoNodeIdentityPresent(cluster *coreapi.Cluster) bool {
	resourceID, ok := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators[operatorAutoNode]
	return ok && resourceID != nil
}
