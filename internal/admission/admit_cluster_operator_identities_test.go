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

package admission

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/util/validation/field"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/azure"
)

const operatorIdentitiesPath = "properties.platform.operatorsAuthentication.userAssignedIdentities"

const testOperatorIdentityPrefix = "/subscriptions/0465bc32-c654-41b8-8d87-9815d7abe8f6/resourceGroups/identity-resource-group/providers/Microsoft.ManagedIdentity/userAssignedIdentities/"

// operatorIdentitiesAdmissionContext builds the admission context these checks need. A nil config
// selects the shipped dev role set, which yields the same operator requirements as public.
func operatorIdentitiesAdmissionContext(config *azure.ClusterScopedIdentitiesConfig) *ClusterAdmissionContext {
	if config == nil {
		config = azure.NewClusterScopedIdentitiesConfig(azure.RoleDefinitionConfigSetNameDev)
	}
	return &ClusterAdmissionContext{ClusterScopedIdentities: config}
}

// configWithVersionLimitedControlPlaneOperator pins one control plane operator to a version range.
// No operator is version-limited in the shipped configuration, so the version-support checks cannot
// be exercised without this.
func configWithVersionLimitedControlPlaneOperator(t *testing.T, operatorName azure.ClusterOperatorIdentifier, maxVersion string) *azure.ClusterScopedIdentitiesConfig {
	t.Helper()

	config := azure.NewClusterScopedIdentitiesConfig(azure.RoleDefinitionConfigSetNameDev)
	bound := metadataapi.Must(semver.ParseTolerant(maxVersion))
	operatorConfig := *config.ControlPlaneOperatorsIdentities[operatorName]
	operatorConfig.MaxVersionInclusive = &bound
	config.ControlPlaneOperatorsIdentities[operatorName] = &operatorConfig
	return config
}

// configWithVersionLimitedDataPlaneOperator is the data plane counterpart of
// configWithVersionLimitedControlPlaneOperator.
func configWithVersionLimitedDataPlaneOperator(t *testing.T, operatorName azure.ClusterOperatorIdentifier, maxVersion string) *azure.ClusterScopedIdentitiesConfig {
	t.Helper()

	config := azure.NewClusterScopedIdentitiesConfig(azure.RoleDefinitionConfigSetNameDev)
	bound := metadataapi.Must(semver.ParseTolerant(maxVersion))
	operatorConfig := *config.DataPlaneOperatorsIdentities[operatorName]
	operatorConfig.MaxVersionInclusive = &bound
	config.DataPlaneOperatorsIdentities[operatorName] = &operatorConfig
	return config
}

func hasErrorContaining(errs field.ErrorList, message, fieldPath string) bool {
	for _, err := range errs {
		if strings.Contains(err.Error(), message) && strings.Contains(err.Field, fieldPath) {
			return true
		}
	}
	return false
}

// TestAdmitClusterRequiresEachOperatorIdentity drops one required operator identity at a time and
// asserts the cluster is rejected for exactly that operator.
//
// Without a case per operator, deleting either requirement loop would go unnoticed: every fixture
// supplies every identity, so nothing else fails when the check stops running. The operator names
// are derived from the identity config rather than hardcoded because
// TestRequiredOperatorIdentitiesContract in internal/azure already pins them.
func TestAdmitClusterRequiresEachOperatorIdentity(t *testing.T) {
	ctx := context.Background()
	op := operation.Operation{Type: operation.Create}
	config := azure.NewClusterScopedIdentitiesConfig(azure.RoleDefinitionConfigSetNameDev)
	version := metadataapi.Must(semver.ParseTolerant(coreapitesting.MinimumValidClusterTestCase().CustomerProperties.Version.ID))

	controlPlane := config.AlwaysRequiredControlPlaneOperators(&version)
	require.NotEmpty(t, controlPlane, "no always-required control plane operators for this version")
	for operatorName := range controlPlane {
		t.Run("control plane "+string(operatorName), func(t *testing.T) {
			cluster := coreapitesting.MinimumValidClusterTestCase()
			delete(cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators, string(operatorName))

			errs := AdmitCluster(ctx, operatorIdentitiesAdmissionContext(nil), op, cluster, nil)
			require.True(t, hasErrorContaining(errs,
				fmt.Sprintf("a user-assigned identity for the %q control plane operator is required", operatorName),
				fmt.Sprintf("%s.controlPlaneOperators[%s]", operatorIdentitiesPath, operatorName)),
				"expected the requirement error for %s, got: %v", operatorName, errs)
		})
	}

	dataPlane := config.AlwaysRequiredDataPlaneOperators(&version)
	require.NotEmpty(t, dataPlane, "no always-required data plane operators for this version")
	for operatorName := range dataPlane {
		t.Run("data plane "+string(operatorName), func(t *testing.T) {
			cluster := coreapitesting.MinimumValidClusterTestCase()
			delete(cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators, string(operatorName))

			errs := AdmitCluster(ctx, operatorIdentitiesAdmissionContext(nil), op, cluster, nil)
			require.True(t, hasErrorContaining(errs,
				fmt.Sprintf("a user-assigned identity for the %q data plane operator is required", operatorName),
				fmt.Sprintf("%s.dataPlaneOperators[%s]", operatorIdentitiesPath, operatorName)),
				"expected the requirement error for %s, got: %v", operatorName, errs)
		})
	}

	t.Run("a complete identity set is accepted", func(t *testing.T) {
		errs := AdmitCluster(ctx, operatorIdentitiesAdmissionContext(nil), op, coreapitesting.MinimumValidClusterTestCase(), nil)
		for _, err := range errs {
			require.NotContains(t, err.Error(), "operator is required",
				"a complete identity set must not be rejected")
		}
	})

	t.Run("identities are enforced on update", func(t *testing.T) {
		// operatorsAuthentication is immutable, so an update carries the same identity set the
		// create was admitted against. A complete set must still pass.
		errs := AdmitCluster(ctx, operatorIdentitiesAdmissionContext(nil), operation.Operation{Type: operation.Update},
			coreapitesting.MinimumValidClusterTestCase(), coreapitesting.MinimumValidClusterTestCase())
		for _, err := range errs {
			require.NotContains(t, err.Error(), "operator is required",
				"a complete identity set must not be rejected on update")
		}

		newCluster := coreapitesting.MinimumValidClusterTestCase()
		delete(newCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators, "ingress")

		errs = AdmitCluster(ctx, operatorIdentitiesAdmissionContext(nil), operation.Operation{Type: operation.Update},
			newCluster, coreapitesting.MinimumValidClusterTestCase())
		require.True(t, hasErrorContaining(errs, "control plane operator is required",
			operatorIdentitiesPath+".controlPlaneOperators[ingress]"),
			"dropping an identity must be rejected on update, got: %v", errs)
	})
}

// TestAdmitClusterRejectsMissingKMSIdentity reproduces the customer-reported gap: etcd is
// customer-managed but no "kms" control plane operator identity is supplied. Both identity lists
// agree with each other, so the static cross-check passes; only the conditional requirement
// catches it.
func TestAdmitClusterRejectsMissingKMSIdentity(t *testing.T) {
	ctx := context.Background()
	op := operation.Operation{Type: operation.Create}

	cluster := coreapitesting.MinimumValidClusterTestCase()
	require.Equal(t, metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
		cluster.CustomerProperties.Etcd.DataEncryption.KeyManagementMode,
		"fixture must use customer-managed etcd for the kms identity to be required")
	delete(cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators, "kms")

	errs := AdmitCluster(ctx, operatorIdentitiesAdmissionContext(nil), op, cluster, nil)
	require.True(t, hasErrorContaining(errs,
		`a user-assigned identity for the "kms" control plane operator is required when properties.etcd.dataEncryption.keyManagementMode is CustomerManaged`,
		operatorIdentitiesPath+".controlPlaneOperators[kms]"),
		"expected the conditional kms requirement, got: %v", errs)
}

func TestAdmitClusterRejectsUnrecognizedOperatorNames(t *testing.T) {
	ctx := context.Background()
	op := operation.Operation{Type: operation.Create}

	for _, tt := range []struct {
		name      string
		mutate    func(*coreapi.HCPOpenShiftCluster)
		fieldPath string
	}{
		{
			// The backend looks operators up by exact name, so "KMS" is both an unrecognized name
			// and leaves the required "kms" identity missing.
			name: "mis-cased control plane operator name",
			mutate: func(c *coreapi.HCPOpenShiftCluster) {
				operators := c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators
				operators["KMS"] = operators["kms"]
				delete(operators, "kms")
			},
			fieldPath: operatorIdentitiesPath + ".controlPlaneOperators[KMS]",
		},
		{
			name: "unrecognized data plane operator name",
			mutate: func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators["not-an-operator"] =
					metadataapi.Must(azcorearm.ParseResourceID(testOperatorIdentityPrefix + "extra-dataplane-identity"))
			},
			fieldPath: operatorIdentitiesPath + ".dataPlaneOperators[not-an-operator]",
		},
		{
			// A control plane operator name is not automatically a data plane one.
			name: "control plane operator name supplied on the data plane",
			mutate: func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators["ingress"] =
					metadataapi.Must(azcorearm.ParseResourceID(testOperatorIdentityPrefix + "cross-plane-identity"))
			},
			fieldPath: operatorIdentitiesPath + ".dataPlaneOperators[ingress]",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cluster := coreapitesting.MinimumValidClusterTestCase()
			tt.mutate(cluster)

			errs := AdmitCluster(ctx, operatorIdentitiesAdmissionContext(nil), op, cluster, nil)
			require.True(t, hasErrorContaining(errs, "unrecognized operator name", tt.fieldPath),
				"expected an unrecognized-name error at %s, got: %v", tt.fieldPath, errs)
			// An unrecognized operator does not exist for any version, so it is reported twice.
			require.True(t, hasErrorContaining(errs, "does not exist for OpenShift version", tt.fieldPath),
				"expected an unsupported-version error at %s, got: %v", tt.fieldPath, errs)
		})
	}
}

// TestAdmitClusterTreatsNilIdentityAsMissing pins that a key present with a nil value does not
// count as supplied. Conversion drops such entries today, so only this test covers the branch.
func TestAdmitClusterTreatsNilIdentityAsMissing(t *testing.T) {
	ctx := context.Background()
	op := operation.Operation{Type: operation.Create}

	cluster := coreapitesting.MinimumValidClusterTestCase()
	operators := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators
	operators["KMS"] = operators["kms"]
	operators["kms"] = nil

	errs := AdmitCluster(ctx, operatorIdentitiesAdmissionContext(nil), op, cluster, nil)

	require.True(t, hasErrorContaining(errs, "unrecognized operator name",
		operatorIdentitiesPath+".controlPlaneOperators[KMS]"),
		"expected the mis-cased name to be rejected, got: %v", errs)
	require.True(t, hasErrorContaining(errs, `a user-assigned identity for the "kms" control plane operator is required`,
		operatorIdentitiesPath+".controlPlaneOperators[kms]"),
		"a nil identity must not count as supplied, got: %v", errs)
}

// TestAdmitClusterIgnoresEmptyOperatorName pins that an empty key is left to the static identity
// validation rather than being reported twice.
func TestAdmitClusterIgnoresEmptyOperatorName(t *testing.T) {
	ctx := context.Background()
	op := operation.Operation{Type: operation.Create}

	cluster := coreapitesting.MinimumValidClusterTestCase()
	cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators[""] =
		metadataapi.Must(azcorearm.ParseResourceID(testOperatorIdentityPrefix + "empty-name-identity"))
	cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators[""] =
		metadataapi.Must(azcorearm.ParseResourceID(testOperatorIdentityPrefix + "empty-name-dataplane-identity"))

	errs := AdmitCluster(ctx, operatorIdentitiesAdmissionContext(nil), op, cluster, nil)

	for _, err := range errs {
		require.NotContains(t, err.Error(), "unrecognized operator name",
			"an empty operator name must not be reported here, got: %v", errs)
	}
}

func TestAdmitClusterRejectsOperatorUnsupportedForVersion(t *testing.T) {
	ctx := context.Background()
	op := operation.Operation{Type: operation.Create}

	t.Run("control plane operator that does not exist for the version", func(t *testing.T) {
		config := configWithVersionLimitedControlPlaneOperator(t, azure.ClusterOperatorIdentifierIngress, "4.19")

		errs := AdmitCluster(ctx, operatorIdentitiesAdmissionContext(config), op, coreapitesting.MinimumValidClusterTestCase(), nil)
		require.True(t, hasErrorContaining(errs, "does not exist for OpenShift version",
			operatorIdentitiesPath+".controlPlaneOperators[ingress]"),
			"expected the unsupported-version error for ingress, got: %v", errs)
	})

	t.Run("data plane operator that does not exist for the version", func(t *testing.T) {
		// image-registry exists on both planes, so this also pins that the data plane check reads
		// the data plane config rather than the control plane one.
		config := configWithVersionLimitedDataPlaneOperator(t, azure.ClusterOperatorIdentifierImageRegistry, "4.19")

		errs := AdmitCluster(ctx, operatorIdentitiesAdmissionContext(config), op, coreapitesting.MinimumValidClusterTestCase(), nil)
		require.True(t, hasErrorContaining(errs, "does not exist for OpenShift version",
			operatorIdentitiesPath+".dataPlaneOperators[image-registry]"),
			"expected the unsupported-version error for image-registry, got: %v", errs)
	})

	t.Run("unrecognized names are still rejected when the version is unparseable", func(t *testing.T) {
		cluster := coreapitesting.MinimumValidClusterTestCase()
		cluster.CustomerProperties.Version.ID = "not-a-version"
		cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators["not-an-operator"] =
			metadataapi.Must(azcorearm.ParseResourceID(testOperatorIdentityPrefix + "spurious-identity"))

		errs := AdmitCluster(ctx, operatorIdentitiesAdmissionContext(nil), op, cluster, nil)
		require.True(t, hasErrorContaining(errs, "unrecognized operator name",
			operatorIdentitiesPath+".controlPlaneOperators[not-an-operator]"),
			"name checks must not depend on a parseable version, got: %v", errs)
	})
}
