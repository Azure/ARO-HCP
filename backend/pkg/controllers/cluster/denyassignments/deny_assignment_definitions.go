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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/utils"
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
	denyAssignmentSuffixNetworkVnetRead          = "network-vnet-read-deny-assignment"
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
			denyAssignmentType:      denyAssignmentSuffixNetworkVnetRead,
			controlPlaneOperators:   []string{operatorClusterAPIAzure, operatorCloudControllerManager, operatorControlPlane, operatorImageRegistry, operatorIngress, operatorFileCSIDriver, operatorCloudNetworkConfig},
			dataPlaneOperators:      []string{operatorImageRegistry, operatorFileCSIDriver},
			includeServiceManagedID: true,
			actions:                 networkVirtualNetworksReadActions(),
		},
		{
			denyAssignmentType:    denyAssignmentSuffixNetworkVnetJoin,
			controlPlaneOperators: []string{operatorClusterAPIAzure, operatorCloudControllerManager, operatorImageRegistry, operatorIngress, operatorCloudNetworkConfig, operatorDiskCSIDriver, operatorFileCSIDriver},
			dataPlaneOperators:    []string{operatorImageRegistry, operatorFileCSIDriver, operatorDiskCSIDriver},
			actions:               networkVirtualNetworksJoinActions(),
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
	for i := range defs {
		if defs[i].conditionalKMS && isKMSEncryptionEnabled(cluster) {
			defs[i].controlPlaneOperators = append(defs[i].controlPlaneOperators, operatorKMS)
		}
	}

	return defs
}

// RequiredDenyAssignmentTypes returns the deny assignment type names ClusterDenyAssignment
// must create for cluster. The set is every deny assignment definition.
func RequiredDenyAssignmentTypes(cluster *coreapi.Cluster) map[string]struct{} {
	defs := denyAssignmentDefinitions(cluster)
	required := make(map[string]struct{}, len(defs))
	for _, definition := range defs {
		required[definition.denyAssignmentType] = struct{}{}
	}
	return required
}

func denyAssignmentDefinitionsByType(cluster *coreapi.Cluster) map[string]*denyAssignmentDefinition {
	defs := denyAssignmentDefinitions(cluster)
	byType := make(map[string]*denyAssignmentDefinition, len(defs))
	for i := range defs {
		byType[defs[i].denyAssignmentType] = &defs[i]
	}
	return byType
}

// PendingRequiredExcludedIdentities reports which identities that
// denyAssignmentType's definition requires excluded are not yet resolved and
// ensured on serviceProviderCluster's status. A nil, non-error result means
// every required identity is ensured.
//
// Every required identity is checked, even after the first miss, so the
// result names every identity still pending rather than only the first one
// found. This resolves each identity individually (the same resolution
// ClusterDenyAssignmentIntent uses), rather than asking whether every
// principal currently in DenyAssignmentStatus.ExcludedIdentities is ensured:
// ClusterDenyAssignmentIntent only adds a principal once that identity's
// metadata resolves, so an identity whose metadata has not resolved yet has
// no entry there at all. A check that only looks at entries already present
// would wrongly report the deny assignment type as fully ensured before
// every required identity has even been considered.
func PendingRequiredExcludedIdentities(
	cluster *coreapi.Cluster,
	serviceProviderCluster *coreapi.ServiceProviderCluster,
	denyAssignmentType string,
	managedIdentitiesDataPlaneServiceAvailable bool,
) ([]string, error) {
	definition, ok := denyAssignmentDefinitionsByType(cluster)[denyAssignmentType]
	if !ok {
		return nil, utils.TrackError(fmt.Errorf("no deny assignment definition for type %q", denyAssignmentType))
	}
	status := serviceProviderCluster.Status.DenyAssignmentsOverManagedResourceGroup[denyAssignmentType]

	identities := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities
	identityEnsured := func(target *coreapi.DenyAssignmentTargetIdentity, resolved bool) bool {
		return resolved && coreapihelpers.DenyAssignmentStatusIdentityExcluded(status, target.PrincipalID)
	}

	var pending []string

	for _, operatorName := range definition.controlPlaneOperators {
		resourceID, ok := identities.ControlPlaneOperators[operatorName]
		if !ok {
			return nil, utils.TrackError(fmt.Errorf("control plane operator %q not found in cluster identity configuration", operatorName))
		}
		target, resolved, err := resolveMSIBasedExcludedTargetIdentity(serviceProviderCluster, resourceID, managedIdentitiesDataPlaneServiceAvailable)
		if err != nil {
			return nil, err
		}
		if !identityEnsured(target, resolved) {
			pending = append(pending, fmt.Sprintf("control-plane operator %q (%s)", operatorName, resourceID))
		}
	}

	for _, operatorName := range definition.dataPlaneOperators {
		resourceID, ok := identities.DataPlaneOperators[operatorName]
		if !ok {
			return nil, utils.TrackError(fmt.Errorf("data plane operator %q not found in cluster identity configuration", operatorName))
		}
		target, resolved, err := resolveDataPlaneExcludedTargetIdentity(serviceProviderCluster, resourceID)
		if err != nil {
			return nil, err
		}
		if !identityEnsured(target, resolved) {
			pending = append(pending, fmt.Sprintf("data-plane operator %q (%s)", operatorName, resourceID))
		}
	}

	if definition.includeServiceManagedID {
		resourceID := identities.ServiceManagedIdentity
		target, resolved, err := resolveMSIBasedExcludedTargetIdentity(serviceProviderCluster, resourceID, managedIdentitiesDataPlaneServiceAvailable)
		if err != nil {
			return nil, err
		}
		if !identityEnsured(target, resolved) {
			pending = append(pending, fmt.Sprintf("service managed identity (%s)", resourceID))
		}
	}

	return pending, nil
}

func generateDenyAssignmentResourceID(cluster *coreapi.Cluster, denyAssignmentType string) (*azcorearm.ResourceID, error) {
	daUUID := generateDenyAssignmentUUID(controllerutils.ClusterServiceIDForCluster(cluster), denyAssignmentType)
	azureResourceID, err := coreapihelpers.ToDenyAssignmentResourceID(cluster.ID.SubscriptionID, cluster.CustomerProperties.Platform.ManagedResourceGroup, daUUID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to build deny assignment resource ID for %s: %w", denyAssignmentType, err))
	}
	return azureResourceID, nil
}

func isKMSEncryptionEnabled(cluster *coreapi.Cluster) bool {
	return cluster.CustomerProperties.Etcd.DataEncryption.KeyManagementMode == metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged &&
		cluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged != nil &&
		cluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms != nil
}
