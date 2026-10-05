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

package azure

import (
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/require"
)

// minSupportedVersion matches the floor that cluster create validation enforces. The required
// operator sets below are asserted at this version rather than at the config's own 4.19 floor,
// so that lowering the validation floor surfaces here instead of silently weakening the check.
var minSupportedVersion = semver.MustParse("4.20.0")

// TestRequiredOperatorIdentitiesContract pins which operator identities a cluster create must
// supply.
//
// This test exists because cluster create validation AND its test fixtures both derive from this
// config. Without a hardcoded expectation, a wrong entry here would be invisible: the fixtures
// would mutate in lockstep with the validator and every test would still pass, while real creates
// were rejected. Deriving the fixtures keeps them from going stale; this test keeps the thing they
// derive from honest.
//
// These values are corroborated by Cluster Service's own runtime config at
// cluster-service/deploy/templates/azure-operators-managed-identities-config.configmap.yaml,
// which is what actually decides whether a cluster can be provisioned. Editing this test should
// mean the requirement genuinely changed there too.
func TestRequiredOperatorIdentitiesContract(t *testing.T) {
	config := NewClusterScopedIdentitiesConfig(RoleDefinitionConfigSetNameDev)

	t.Run("always required control plane operators", func(t *testing.T) {
		require.ElementsMatch(t, []string{
			"cloud-controller-manager",
			"cloud-network-config",
			"cluster-api-azure",
			"control-plane",
			"disk-csi-driver",
			"file-csi-driver",
			"image-registry",
			"ingress",
		}, controlPlaneOperatorNames(config.AlwaysRequiredControlPlaneOperators(&minSupportedVersion)))
	})

	t.Run("always required data plane operators", func(t *testing.T) {
		require.ElementsMatch(t, []string{
			"disk-csi-driver",
			"file-csi-driver",
			"image-registry",
		}, dataPlaneOperatorNames(config.AlwaysRequiredDataPlaneOperators(&minSupportedVersion)))
	})

	t.Run("kms is required only on enablement", func(t *testing.T) {
		kms, ok := config.ControlPlaneOperatorsIdentities[ClusterOperatorIdentifierKMS]
		require.True(t, ok, "kms control plane operator identity is missing from the config")
		require.Equal(t, IdentityRequirementTypeOnEnablement, kms.Requirement.Type,
			"kms must stay OnEnablement; validation supplies the enablement condition separately")
		require.NotContains(t, controlPlaneOperatorNames(config.AlwaysRequiredControlPlaneOperators(&minSupportedVersion)),
			string(ClusterOperatorIdentifierKMS))
	})

	// Cluster create validation builds this config with a hardcoded set name. That is only safe
	// while the requirement metadata is identical across sets; this turns that assumption into an
	// enforced invariant rather than a comment.
	t.Run("requirement metadata is identical across role definition config sets", func(t *testing.T) {
		public := NewClusterScopedIdentitiesConfig(RoleDefinitionConfigSetNamePublic)

		require.ElementsMatch(t,
			controlPlaneOperatorNames(config.AlwaysRequiredControlPlaneOperators(&minSupportedVersion)),
			controlPlaneOperatorNames(public.AlwaysRequiredControlPlaneOperators(&minSupportedVersion)))
		require.ElementsMatch(t,
			dataPlaneOperatorNames(config.AlwaysRequiredDataPlaneOperators(&minSupportedVersion)),
			dataPlaneOperatorNames(public.AlwaysRequiredDataPlaneOperators(&minSupportedVersion)))

		// Name validation rejects anything outside the recognized set, so an operator present in
		// only one config set would be valid at runtime and rejected here, or the reverse.
		require.ElementsMatch(t,
			controlPlaneOperatorNames(config.ControlPlaneOperatorsIdentities),
			controlPlaneOperatorNames(public.ControlPlaneOperatorsIdentities),
			"the recognized control plane operators differ between config sets")
		require.ElementsMatch(t,
			dataPlaneOperatorNames(config.DataPlaneOperatorsIdentities),
			dataPlaneOperatorNames(public.DataPlaneOperatorsIdentities),
			"the recognized data plane operators differ between config sets")

		for operatorName, devOperator := range config.ControlPlaneOperatorsIdentities {
			publicOperator, ok := public.ControlPlaneOperatorsIdentities[operatorName]
			require.True(t, ok, "control plane operator %q is missing from the public config set", operatorName)
			require.Equal(t, devOperator.Requirement.Type, publicOperator.Requirement.Type,
				"requirement type for control plane operator %q differs between config sets", operatorName)
			require.Equal(t, devOperator.MinVersionInclusive, publicOperator.MinVersionInclusive,
				"minimum version for control plane operator %q differs between config sets", operatorName)
			require.Equal(t, devOperator.MaxVersionInclusive, publicOperator.MaxVersionInclusive,
				"maximum version for control plane operator %q differs between config sets", operatorName)
		}

		for operatorName, devOperator := range config.DataPlaneOperatorsIdentities {
			publicOperator, ok := public.DataPlaneOperatorsIdentities[operatorName]
			require.True(t, ok, "data plane operator %q is missing from the public config set", operatorName)
			require.Equal(t, devOperator.Requirement.Type, publicOperator.Requirement.Type,
				"requirement type for data plane operator %q differs between config sets", operatorName)
			require.Equal(t, devOperator.MinVersionInclusive, publicOperator.MinVersionInclusive,
				"minimum version for data plane operator %q differs between config sets", operatorName)
			require.Equal(t, devOperator.MaxVersionInclusive, publicOperator.MaxVersionInclusive,
				"maximum version for data plane operator %q differs between config sets", operatorName)
		}
	})
}

func controlPlaneOperatorNames(operators ControlPlaneOperatorsIdentities) []string {
	names := make([]string, 0, len(operators))
	for operatorName := range operators {
		names = append(names, string(operatorName))
	}
	return names
}

func dataPlaneOperatorNames(operators DataPlaneOperatorsIdentities) []string {
	names := make([]string, 0, len(operators))
	for operatorName := range operators {
		names = append(names, string(operatorName))
	}
	return names
}
