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

package operatorsmis

import (
	"fmt"
	"iter"
	"maps"

	"github.com/blang/semver/v4"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/utils"
)

// Config represents the managed identities configuration associated with the Cluster's control plane
// and data plane oeprators. This configuration contains the control plane and data plane operator identities
// that are recognized by the service.
type Config struct {
	controlPlaneOperatorsIdentities ControlPlaneOperatorsIdentities
	dataPlaneOperatorsIdentities    DataPlaneOperatorsIdentities
}

// ControlPlaneOperatorIdentityIterator is an iterator for the control plane operator identity
type ControlPlaneOperatorIdentityIterator iter.Seq[ControlPlaneOperatorIdentity]

// DataPlaneOperatorIdentityIterator is an iterator for the data plane operator identity
type DataPlaneOperatorIdentityIterator iter.Seq[DataPlaneOperatorIdentity]

func (c *Config) GetControlPlaneOperatorIdentityConfig(operatorName string) (ControlPlaneOperatorIdentity, bool) {
	controlPlaneOperatorIdentity, ok := c.controlPlaneOperatorsIdentities[operatorName]
	return controlPlaneOperatorIdentity, ok
}

// ControlPlaneOperatorIdentities returns an iterator that can be used to range over
// the control plane identities
func (c *Config) ControlPlaneOperatorIdentities() ControlPlaneOperatorIdentityIterator {
	return ControlPlaneOperatorIdentityIterator(maps.Values(c.controlPlaneOperatorsIdentities))
}

// DataPlaneOperatorIdentities returns an iterator that can be used to range over the data plane identities
func (c *Config) DataPlaneOperatorIdentities() DataPlaneOperatorIdentityIterator {
	return DataPlaneOperatorIdentityIterator(maps.Values(c.dataPlaneOperatorsIdentities))
}

// GetDataPlaneOperatorIdentityConfig retrieves the the data plane operator identity configuration for the given operator name.
// If the data plane operator identity configuration is present for the given operator name it is returned and the boolean is true.
// Otherwise the returned value will be the zero-value and the boolean will be false.
func (c *Config) GetDataPlaneOperatorIdentityConfig(operatorName string) (DataPlaneOperatorIdentity, bool) {
	dataPlaneOperatorIdentity, ok := c.dataPlaneOperatorsIdentities[operatorName]
	return dataPlaneOperatorIdentity, ok
}

// GetAlwaysRequiredDataPlaneOperators retrieves the data plane operators identities that are always required for the given OpenShift version in
// format <number>.<number>.
// The meaning of always required for a given version is that the operator identity is always required for the given version, independently on
// the configuration of the cluster and its derivated resources.
func (c *Config) GetAlwaysRequiredDataPlaneOperators(version string) (DataPlaneOperatorsIdentities, error) {
	var requiredOperators = make(DataPlaneOperatorsIdentities)

	for dataPlaneOperator, identity := range c.dataPlaneOperatorsIdentities {
		required, err := identity.isAlwaysRequiredForOpenshiftVersion(version)
		if err != nil {
			return nil, err
		}
		if required {
			requiredOperators[dataPlaneOperator] = identity
		}
	}
	return requiredOperators, nil
}

// GetAlwaysRequiredControlPlaneOperators retrieves the control plane operators identities that are always required for the given OpenShift version in
// format <number>.<number>.
// The meaning of always required for a given version is that the operator identity is always required for the given version, independently on
// the configuration of the cluster and its derivated resources.
func (c *Config) GetAlwaysRequiredControlPlaneOperators(version string) (
	ControlPlaneOperatorsIdentities, error) {
	var requiredOperators = make(ControlPlaneOperatorsIdentities)

	for controlPlaneOperator, identity := range c.controlPlaneOperatorsIdentities {
		required, err := identity.isAlwaysRequiredForOpenshiftVersion(version)
		if err != nil {
			return nil, err
		}
		if required {
			requiredOperators[controlPlaneOperator] = identity
		}
	}
	return requiredOperators, nil
}

// NewOperatorsManagedIdentitiesConfig builds a new Operators Managed Identities Config.
func NewOperatorsManagedIdentitiesConfig(
	controlPlaneOperatorsIdentities ControlPlaneOperatorsIdentities,
	dataPlaneOperatorsIdentities DataPlaneOperatorsIdentities) Config {
	return Config{
		controlPlaneOperatorsIdentities: controlPlaneOperatorsIdentities,
		dataPlaneOperatorsIdentities:    dataPlaneOperatorsIdentities,
	}
}

// IdentityRequirement represents the requirement for an operator identity.
type IdentityRequirement string

const (
	// AlwaysRequiredIdentityRequirement indicates that the operator identity is always required.
	AlwaysRequiredIdentityRequirement IdentityRequirement = "always"
	// RequiredOnEnablementIdentityRequirement indicates that the operator identity is required only when a functionality that leverages the operator is enabled.
	RequiredOnEnablementIdentityRequirement IdentityRequirement = "onEnablement"
)

// baseOperatorIdentity represents the base configuration for an operator identity.
type baseOperatorIdentity struct {
	// operatorName is the name of the operator associated to this identity.
	operatorName string
	// minOpenShiftVersion is the minimum OpenShift version supported by the operator.
	// The format is <number>.<number>, e.g. 4.19.
	// Not specifying it indicates that the operator is supported for all OpenShift versions,
	// or up to MaxOpenShiftVersion if MaxOpenShiftVersion is specified.
	minOpenShiftVersion string
	// maxOpenShiftVersion is the maximum OpenShift version supported by the operator.
	// The format is <number>.<number>, e.g. 4.19.
	// Not specifying it indicates that the operator is supported in all OpenShift versions,
	// starting from minOpenShiftVersion if minOpenShiftVersion is specified.
	maxOpenShiftVersion string
	// roleDefinitions is a list of Azure Role Definitions needed by the operator.
	// The list cannot be empty and it must have a length of at least one.
	// TODO do we want the list values to be values or pointers?
	roleDefinitions []RoleDefinition
	// required is the requirement for the operator identity.
	requirement IdentityRequirement
}

// GetMinOpenShiftVersion returns the minimum OpenShift version supported by the operator.
// The format is <number>.<number>, e.g. 4.19.
// Empty means that the operator is supported for all OpenShift versions,
// or up to MaxOpenShiftVersion if GetMaxOpenShiftVersion is not empty.
func (b *baseOperatorIdentity) GetMinOpenShiftVersion() string {
	return b.minOpenShiftVersion
}

// GetMaxOpenShiftVersion returns the maximum OpenShift version supported by the operator.
// The format is <number>.<number>, e.g. 4.19.
// Empty means that the operator is supported in all OpenShift versions, starting from GetMinOpenShiftVersion if GetMinOpenShiftVersion is not empty.
func (b *baseOperatorIdentity) GetMaxOpenShiftVersion() string {
	return b.maxOpenShiftVersion
}

// isAlwaysRequiredForOpenshiftVersion returns true if the operator identity is always required for the given OpenShift version.
// The meaning of always required for a given version is that the operator identity is always required for the given version, independently on
// the configuration of the cluster and its derivated resources.
func (b *baseOperatorIdentity) isAlwaysRequiredForOpenshiftVersion(openshiftVersion string) (bool, error) {
	// If the operator is optional, managed identity is not required
	if !b.isAlwaysRequired() {
		return false, nil
	}

	return b.IsSupportedForOpenshiftVersion(openshiftVersion)
}

// isAlwaysRequired returns true if the identity is always required.
// The meaning of always required for a given version is that the operator identity is always required for the given version, independently on
// the configuration of the cluster and its derivated resources.
// This is applies to the range of versions [b.minOpenShiftVersion, b.maxOpenShiftVersion] defined in b.
func (b *baseOperatorIdentity) isAlwaysRequired() bool {
	return b.IdentityRequirement() == AlwaysRequiredIdentityRequirement
}

// IdentityRequirement returns the requirement for the operator identity.
func (b *baseOperatorIdentity) IdentityRequirement() IdentityRequirement {
	return b.requirement
}

// IsSupportedForOpenshiftVersion returns true if the operator identity is supported for the given OpenShift version.
// An operator identity is supported for a given version if the version is within the range of versions [b.minOpenShiftVersion, b.maxOpenShiftVersion] defined in b.
func (b *baseOperatorIdentity) IsSupportedForOpenshiftVersion(openshiftVersion string) (bool, error) {
	// If no version constraints are defined, managed identity is supported
	if b.minOpenShiftVersion == "" && b.maxOpenShiftVersion == "" {
		return true, nil
	}

	semverOpenShiftVersion, err := semver.Parse(openshiftVersion)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("invalid OpenShift version %s: %w", openshiftVersion, err))
	}

	// Check if version satisfies the minimum constraint
	if b.minOpenShiftVersion != "" {
		semverMinOpenShiftVersion := semver.MustParse(b.minOpenShiftVersion)
		if semverOpenShiftVersion.LT(semverMinOpenShiftVersion) {
			return false, nil
		}
	}

	// Check if version satisfies the maximum constraint
	if b.maxOpenShiftVersion != "" {
		semverMaxOpenShiftVersion := semver.MustParse(b.maxOpenShiftVersion)
		if semverOpenShiftVersion.GT(semverMaxOpenShiftVersion) {
			return false, nil
		}
	}

	return true, nil
}

// RoleDefinitions returns an iterator that can be used to range over the RoleDefitions
func (b *baseOperatorIdentity) RoleDefinitions() iter.Seq[RoleDefinition] {
	return func(yield func(RoleDefinition) bool) {
		for _, r := range b.roleDefinitions {
			if !yield(r) {
				return
			}
		}
	}
}

// RoleDefinitionResourceIDs returns all role definition resource IDs
func (b *baseOperatorIdentity) RoleDefinitionResourceIDs() []*azcorearm.ResourceID {
	var ids []*azcorearm.ResourceID
	for rd := range b.RoleDefinitions() {
		ids = append(ids, rd.GetResourceID())
	}
	return ids
}

// ControlPlaneOperatorIdentity represents the configuration for a control plane operator.
type ControlPlaneOperatorIdentity struct {
	baseOperatorIdentity
}

// OperatorName returns the name of the operator name associated to this identity.
func (c *ControlPlaneOperatorIdentity) OperatorName() string {
	return c.operatorName
}

// DataPlaneOperatorIdentity represents the configuration for a data plane operator.
type DataPlaneOperatorIdentity struct {
	baseOperatorIdentity
	// kubernetesServiceAccounts is a list of Kubernetes Service Accounts needed by the operator.
	kubernetesServiceAccounts KubernetesServiceAccountList
}

// OperatorName returns the name of the operator name associated to this identity.
func (d *DataPlaneOperatorIdentity) OperatorName() string {
	return d.operatorName
}

// RoleDefinition represents an Azure Role Definition.
type RoleDefinition struct {
	// name is the name of the Role defined in resourceID. It is purely a friendly/descriptive name of the Azure Role Definition
	name string
	// resourceID is the resource ID of the Azure Role Definition.
	resourceID *azcorearm.ResourceID
}

// NewRoleDefinition builds a new RoleDefinition with the given name and resource ID.
func NewRoleDefinition(name string, resourceID *azcorearm.ResourceID) *RoleDefinition {
	return &RoleDefinition{
		name:       name,
		resourceID: resourceID,
	}
}

// GetName returns the name of the Role defined in GetResourceID. It is purely a friendly/descriptive name of the Azure Role Definition
func (rd *RoleDefinition) GetName() string {
	return rd.name
}

// GetResourceID returns the resource ID of the Azure Role Definition.
func (rd *RoleDefinition) GetResourceID() *azcorearm.ResourceID {
	return rd.resourceID
}

// KubernetesServiceAccount represents a Kubernetes Service Account.
type KubernetesServiceAccount struct {
	// name is the name of the Kubernetes Service Account.
	name string
	// namespace is the namespace of the Kubernetes Service Account.
	namespace string
}

// GetName returns the name of the Kubernetes Service Account.
func (sa KubernetesServiceAccount) GetName() string {
	return sa.name
}

// GetNamespace returns the namespace of the Kubernetes Service Account.
func (sa KubernetesServiceAccount) GetNamespace() string {
	return sa.namespace
}

// AsOidcSubject returns the Kubernetes Service Account as an OIDC subject.
// The format is "system:serviceaccount:<namespace>:<name>".
func (sa KubernetesServiceAccount) AsOIDCSubject() string {
	return fmt.Sprintf("system:serviceaccount:%s:%s", sa.namespace, sa.name)
}

// ControlPlaneOperatorsIdentities represents a map of control plane operator identities.
type ControlPlaneOperatorsIdentities map[string]ControlPlaneOperatorIdentity

// Add adds a control plane operator identity to the map.
func (controlPlaneOperatorsIdentities ControlPlaneOperatorsIdentities) Add(
	controlPlaneOperatorIdentity ControlPlaneOperatorIdentity) {
	controlPlaneOperatorsIdentities[controlPlaneOperatorIdentity.operatorName] = controlPlaneOperatorIdentity
}

// Get returns a control plane operator identity from the map.
func (controlPlaneOperatorsIdentities ControlPlaneOperatorsIdentities) Get(
	operatorName string) (ControlPlaneOperatorIdentity, bool) {
	controlPlaneOperatorIdentity, ok := controlPlaneOperatorsIdentities[operatorName]
	return controlPlaneOperatorIdentity, ok
}

// NewControlPlaneOperatorsIdentities builds a new map of control plane operator identities.
func NewControlPlaneOperatorsIdentities() ControlPlaneOperatorsIdentities {
	return make(ControlPlaneOperatorsIdentities)
}

// DataPlaneOperatorsIdentities represents a map of data plane operator identities.
type DataPlaneOperatorsIdentities map[string]DataPlaneOperatorIdentity

// Add adds a data plane operator identity to the map.
func (dataPlaneOperatorsIdentities DataPlaneOperatorsIdentities) Add(
	dataPlaneOperatorIdentity DataPlaneOperatorIdentity) {
	dataPlaneOperatorsIdentities[dataPlaneOperatorIdentity.operatorName] = dataPlaneOperatorIdentity
}

// Get returns a data plane operator identity from the map.
func (dataPlaneOperatorsIdentities DataPlaneOperatorsIdentities) Get(
	operatorName string) (DataPlaneOperatorIdentity, bool) {
	dataPlaneOperatorIdentity, ok := dataPlaneOperatorsIdentities[operatorName]
	return dataPlaneOperatorIdentity, ok
}

// KubernetesServiceAccounts return an iterator that can be used to range over the k8s
// service accounts
func (d *DataPlaneOperatorIdentity) KubernetesServiceAccounts() iter.Seq[KubernetesServiceAccount] {
	return func(yield func(KubernetesServiceAccount) bool) {
		for _, s := range d.kubernetesServiceAccounts {
			if !yield(s) {
				return
			}
		}
	}
}

// NewDataPlaneOperatorsIdentities builds a new map of data plane operator identities.
func NewDataPlaneOperatorsIdentities() DataPlaneOperatorsIdentities {
	return make(DataPlaneOperatorsIdentities)
}

// NewKubernetesServiceAccount builds a new Kubernetes Service Account.
func NewKubernetesServiceAccount(name, namespace string) KubernetesServiceAccount {
	return KubernetesServiceAccount{
		name:      name,
		namespace: namespace,
	}
}

// KubernetesServiceAccountList represents a list of Kubernetes Service Accounts.
type KubernetesServiceAccountList []KubernetesServiceAccount

// AddKubernetesServiceAccount adds a Kubernetes Service Account to the list.
func (serviceAccountList *KubernetesServiceAccountList) AddKubernetesServiceAccount(
	serviceAccount KubernetesServiceAccount) {
	*serviceAccountList = append(*serviceAccountList, serviceAccount)
}

// NewControlPlaneOperatorIdentity builds a new Control Plane Operator Identity.
func NewControlPlaneOperatorIdentity(
	operatorName string,
	minOpenShiftVersion string,
	maxOpenShiftVersion string,
	roleDefinitions []RoleDefinition,
	identityRequirement string) ControlPlaneOperatorIdentity {

	baseOperatorIdentity := baseOperatorIdentity{
		operatorName:        operatorName,
		minOpenShiftVersion: minOpenShiftVersion,
		maxOpenShiftVersion: maxOpenShiftVersion,
		roleDefinitions:     roleDefinitions,
		requirement:         IdentityRequirement(identityRequirement),
	}
	return ControlPlaneOperatorIdentity{
		baseOperatorIdentity: baseOperatorIdentity,
	}
}

// NewDataPlaneOperatorIdentity builds a new Data Plane Operator Identity.
func NewDataPlaneOperatorIdentity(
	operatorName string,
	minOpenShiftVersion string,
	maxOpenShiftVersion string,
	roleDefinitions []RoleDefinition,
	identityRequirement string, kubernetesServiceAccounts []KubernetesServiceAccount) DataPlaneOperatorIdentity {

	baseOperatorIdentity := baseOperatorIdentity{
		operatorName:        operatorName,
		minOpenShiftVersion: minOpenShiftVersion,
		maxOpenShiftVersion: maxOpenShiftVersion,
		roleDefinitions:     roleDefinitions,
		requirement:         IdentityRequirement(identityRequirement),
	}
	return DataPlaneOperatorIdentity{
		baseOperatorIdentity:      baseOperatorIdentity,
		kubernetesServiceAccounts: kubernetesServiceAccounts,
	}
}
