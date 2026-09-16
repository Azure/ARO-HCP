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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// TestDenyAssignmentDefinitionsSingleComplete pins the collapsed single "complete" deny assignment:
// its type, the classic ARO-RP wildcard actions, the merged notActions (order preserved), the
// excluded operator sets, and that it excludes the service managed identity while leaving
// dataActions empty.
func TestDenyAssignmentDefinitionsSingleComplete(t *testing.T) {
	defs := denyAssignmentDefinitions(newTestCluster())
	require.Len(t, defs, 1, "expected exactly one consolidated deny assignment definition")

	def := defs[0]
	assert.Equal(t, denyAssignmentSuffixComplete, def.denyAssignmentType, "type must be the consolidated complete suffix")
	assert.Equal(t, "complete-deny-assignment", def.denyAssignmentType, "type must match the shared Cluster Service suffix byte-for-byte")
	assert.True(t, def.includeServiceManagedID, "the complete deny assignment must exclude the service managed identity")
	assert.Empty(t, def.dataActions, "the complete deny assignment leaves dataActions empty")

	assert.Equal(t, []string{"*/action", "*/delete", "*/write"}, def.actions, "actions must be the classic ARO-RP wildcard set")

	expectedNotActions := []string{
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
		"Microsoft.Resources/tags/*",
		"Microsoft.PolicyInsights/remediations/write",
		"Microsoft.PolicyInsights/remediations/delete",
		"Microsoft.Authorization/roleAssignments/write",
		"Microsoft.Network/dnszones/CAA/write",
		"Microsoft.Network/dnszones/CAA/delete",
		"Microsoft.Network/dnszones/TXT/write",
		"Microsoft.Network/dnszones/TXT/delete",
		"Microsoft.Compute/virtualMachines/retrieveBootDiagnosticsData/action",
	}
	assert.Equal(t, expectedNotActions, def.notActions, "notActions must be the merged classic ARO-RP list in order")

	// KMS is defined on the default test cluster, so it is excluded (appended after the base set).
	assert.Equal(t, []string{
		operatorClusterAPIAzure,
		operatorCloudControllerManager,
		operatorControlPlane,
		operatorImageRegistry,
		operatorIngress,
		operatorCloudNetworkConfig,
		operatorDiskCSIDriver,
		operatorFileCSIDriver,
		operatorKMS,
	}, def.controlPlaneOperators, "control plane operators must be the base set plus KMS when KMS is defined")

	assert.Equal(t, []string{
		operatorImageRegistry,
		operatorDiskCSIDriver,
		operatorFileCSIDriver,
	}, def.dataPlaneOperators, "data plane operators must match the classic set")
}

// TestDenyAssignmentDefinitionsKMSExclusionPresenceGated verifies the KMS exclusion is driven by
// the identity being DEFINED on the cluster, not by whether KMS etcd encryption is enabled.
func TestDenyAssignmentDefinitionsKMSExclusionPresenceGated(t *testing.T) {
	// KMS defined -> excluded, regardless of etcd encryption mode.
	def := denyAssignmentDefinitions(newTestCluster())[0]
	assert.Contains(t, def.controlPlaneOperators, operatorKMS, "KMS must be excluded whenever it is defined on the cluster")

	// KMS not defined -> not excluded.
	clusterWithoutKMS := newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
		delete(c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators, operatorKMS)
	})
	def = denyAssignmentDefinitions(clusterWithoutKMS)[0]
	assert.NotContains(t, def.controlPlaneOperators, operatorKMS, "KMS must not be excluded when it is not defined on the cluster")
}

// TestDenyAssignmentCompleteExclusionSet verifies the full exclusion set resolved from the single
// complete definition: every control-plane operator (including KMS when defined), every data-plane
// operator, and the service managed identity. Operators present in both the control-plane and
// data-plane maps contribute two distinct excluded principals (distinct resource IDs); the set is
// intentionally not deduplicated.
func TestDenyAssignmentCompleteExclusionSet(t *testing.T) {
	cluster := newTestCluster()
	def := denyAssignmentDefinitions(cluster)[0]

	excluded, err := collectExcludedPrincipalIDs(cluster, def)
	require.NoError(t, err, "collecting excluded identity resource IDs must succeed for the complete definition")

	// 9 control-plane operators (8 base + KMS) + 3 data-plane operators + 1 service managed identity.
	assert.Len(t, excluded, 13, "the complete deny assignment excludes every operator identity plus the service MI")

	cpOps, dpOps, serviceManagedID := testClusterIdentities()
	gotIDs := make(map[string]struct{}, len(excluded))
	for _, id := range excluded {
		gotIDs[strings.ToLower(id.String())] = struct{}{}
	}
	assert.Len(t, gotIDs, 13, "every excluded identity resource ID must be distinct")
	assert.Contains(t, gotIDs, strings.ToLower(cpOps[operatorKMS].String()), "KMS control-plane identity must be excluded")
	assert.Contains(t, gotIDs, strings.ToLower(cpOps[operatorImageRegistry].String()), "control-plane image-registry identity must be excluded")
	assert.Contains(t, gotIDs, strings.ToLower(dpOps[operatorImageRegistry].String()), "data-plane image-registry identity must be excluded (distinct from control-plane)")
	assert.Contains(t, gotIDs, strings.ToLower(serviceManagedID.String()), "service managed identity must be excluded")
}
