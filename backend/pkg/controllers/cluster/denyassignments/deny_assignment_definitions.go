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

	// denyAssignmentSuffixComplete is intentionally the SAME suffix Cluster Service
	// uses for its consolidated deny assignment (see
	// https://github.com/openshift-online/aro-hcp-clusters-service/blob/730252528ea634920f5ed029d8b7f95347569a83/pkg/azure/denyassignmentcreator/deny_assignment_creator.go#L62).
	// The suffix feeds the shared deterministic UUID (generateDenyAssignmentUUID), so
	// it must stay byte-for-byte identical to CS.
	denyAssignmentSuffixComplete = "complete-deny-assignment"

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
}

// denyAssignmentDefinitions returns the single consolidated "complete" deny assignment: the
// AllPrincipals system-defined principal, every operator managed identity in ExcludePrincipals, and
// IsSystemProtected set by the controller. It is kept in lockstep with Cluster Service, which writes
// the same consolidated assignment under the same suffix (see denyAssignmentSuffixComplete).
func denyAssignmentDefinitions(cluster *coreapi.HCPOpenShiftCluster) []denyAssignmentDefinition {
	def := denyAssignmentDefinition{
		denyAssignmentType: denyAssignmentSuffixComplete,
		controlPlaneOperators: []string{
			operatorClusterAPIAzure,
			operatorCloudControllerManager,
			operatorControlPlane,
			operatorImageRegistry,
			operatorIngress,
			operatorCloudNetworkConfig,
			operatorDiskCSIDriver,
			operatorFileCSIDriver,
		},
		dataPlaneOperators: []string{
			operatorImageRegistry,
			operatorDiskCSIDriver,
			operatorFileCSIDriver,
		},
		// The service managed identity is always excluded from the complete deny assignment.
		includeServiceManagedID: true,
		actions: []string{
			"*/action",
			"*/delete",
			"*/write",
		},
		notActions: []string{
			"Microsoft.Compute/disks/beginGetAccess/action",
			"Microsoft.Compute/disks/endGetAccess/action",
			"Microsoft.Compute/disks/write",
			"Microsoft.Insights/ActionGroups/write",
			"Microsoft.Insights/ActionGroups/delete",
			"Microsoft.Insights/MetricAlerts/write",
			"Microsoft.Insights/MetricAlerts/delete",
			"Microsoft.Insights/ActivityLogAlerts/write",
			"Microsoft.Insights/ActivityLogAlerts/delete",
			"Microsoft.Compute/snapshots/beginGetAccess/action",
			"Microsoft.Compute/snapshots/delete",
			"Microsoft.Compute/snapshots/endGetAccess/action",
			"Microsoft.Compute/snapshots/write",
			"Microsoft.Network/networkInterfaces/effectiveRouteTable/action",
			"Microsoft.Network/networkSecurityGroups/join/action",
			"Microsoft.Resources/tags/*", // Enable tagging for Resources RP only
			"Microsoft.PolicyInsights/remediations/write",
			"Microsoft.PolicyInsights/remediations/delete",
			"Microsoft.Authorization/roleAssignments/write",
			"Microsoft.Network/dnszones/CAA/write",
			"Microsoft.Network/dnszones/CAA/delete",
			"Microsoft.Network/dnszones/TXT/write",
			"Microsoft.Network/dnszones/TXT/delete",
			"Microsoft.Compute/virtualMachines/retrieveBootDiagnosticsData/action",
		},
	}

	// Include the KMS managed identity in the exclusions only when KMS etcd encryption is enabled.
	if isKMSEncryptionEnabled(cluster) {
		def.controlPlaneOperators = append(def.controlPlaneOperators, operatorKMS)
	}

	return []denyAssignmentDefinition{def}
}

func allDenyAssignmentReferences(cluster *coreapi.HCPOpenShiftCluster) ([]coreapi.DenyAssignmentReference, error) {
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

func isKMSEncryptionEnabled(cluster *coreapi.HCPOpenShiftCluster) bool {
	return cluster.CustomerProperties.Etcd.DataEncryption.KeyManagementMode == metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged &&
		cluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged != nil &&
		cluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms != nil
}
