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
	// uses for its consolidated deny assignment (see aro-hcp-clusters-service
	// pkg/azure/denyassignmentcreator/deny_assignment_creator.go). The suffix feeds
	// the shared deterministic UUID (generateDenyAssignmentUUID), so it must stay
	// byte-for-byte identical to CS.
	// TODO: now that the backend is the sole owner of deny assignments, do we still
	// want to reuse CS's "complete-deny-assignment" suffix, or mint our own? Changing
	// it changes the UUID, so decide before GA.
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

// denyAssignmentDefinitions returns the single "complete" deny assignment modeled on classic
// ARO-RP: AllPrincipals as the (system-defined) principal, every operator managed identity in
// ExcludePrincipals, and IsSystemProtected set by the controller. Collapsing the former per-service
// deny assignments into one keeps the backend in lockstep with Cluster Service, which writes the
// same consolidated assignment under the same suffix (see denyAssignmentSuffixComplete).
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

	// Exclude the KMS managed identity whenever it is DEFINED on the cluster. This mirrors classic
	// ARO-RP, which excludes every operator identity it hands out; it is a presence check and is
	// deliberately NOT gated on whether KMS etcd encryption is currently enabled.
	if _, ok := cluster.CustomerProperties.Platform.OperatorsAuthentication.
		UserAssignedIdentities.ControlPlaneOperators[operatorKMS]; ok {
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
		azureResourceID, err := coreapi.ToDenyAssignmentResourceID(subscriptionID, managedResourceGroup, daUUID)
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
