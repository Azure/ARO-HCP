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

package types

import (
	"fmt"
	"slices"
	"strings"
)

// WellFormedChecker allows introspection of how well-formed this step is over inputs.
type WellFormedChecker interface {
	// IsWellFormedOverInputs determines if, given the same (visible) inputs, this step will have the same outputs.
	// Implicit inputs on the filesystem outside the purview of any configuration make a step ill-formed.
	IsWellFormedOverInputs() bool
}

// Step divulges common metadata about a step.
type Step interface {
	WellFormedChecker
	StepName() string
	ActionType() string
	Description() string
	Dependencies() []StepDependency
	RequiredInputs() []StepDependency
	ExternalDependencies() []ExternalStepDependency
	AutomatedRetries() *AutomatedRetry
	ConsideredForServiceGroupCompletion() bool
}

type ValidationStep interface {
	Step
	Validations() []string
}

// StepMeta contains metadata for a steps.
type StepMeta struct {
	Name              string                   `json:"name"`
	Action            string                   `json:"action"`
	AutomatedRetry    *AutomatedRetry          `json:"automatedRetry,omitempty"`
	DependsOn         []StepDependency         `json:"dependsOn,omitempty"`
	ExternalDependsOn []ExternalStepDependency `json:"externalDependsOn,omitempty"`

	// OmitFromServiceGroupCompletion influences how this step is treated in execution graphs containing multiple
	// service groups - by default, *all* leaf nodes of a service group must finish before *any* root nodes of
	// dependent service groups begin. Setting this flag will omit this particular step from that set of leaf nodes
	// that block execution of dependent steps.
	OmitFromServiceGroupCompletion bool `json:"omitFromServiceGroupCompletion,omitempty"`
}

// StepDependency describes a step that must run before the dependent step may begin.
type StepDependency struct {
	// ResourceGroup is the (semantic/display) name of the group to which the step belongs.
	ResourceGroup string `json:"resourceGroup"`
	// Step is the name of the step being depended on.
	Step string `json:"step"`
}

type ExternalStepDependency struct {
	// ServiceGroup declares which service group the external dependency belongs to.
	ServiceGroup   string `json:"serviceGroup"`
	StepDependency `json:",inline"`
}

// AutomatedRetry configures automated retry for failed steps.
type AutomatedRetry struct {
	// ErrorContainsAny determines when a retry should run - if the output of a step contains any of
	// the strings in this array, matching in a case-insensitive manner, a retry will fire.
	// Must contain 16 or fewer items; the total encoded length of this array may not be more than 1KB.
	ErrorContainsAny []string `json:"errorContainsAny,omitempty"`

	// MaximumRetryCount is the maximum number of retires that should fire. Between 1 and 10, defaults to 1.
	MaximumRetryCount int `json:"maximumRetryCount,omitempty"`

	// DurationBetweenRetries is the amount of time to wait between retries. Must be between 1 minute and 3 hours.
	// Formatted using Go's time.Duration syntax.
	DurationBetweenRetries string `json:"durationBetweenRetries,omitempty"`

	// HonorRetryBudgetWhenFailingFast retains the configured retry count and duration
	// when a stage uses fail-fast behavior.
	HonorRetryBudgetWhenFailingFast bool `json:"honorRetryBudgetWhenFailingFast,omitempty"`
}

func SortDependencies(a, b StepDependency) int {
	if cmp := strings.Compare(a.ResourceGroup, b.ResourceGroup); cmp != 0 {
		return cmp
	}
	return strings.Compare(a.Step, b.Step)
}

func (m *StepMeta) StepName() string {
	return m.Name
}

func (m *StepMeta) ActionType() string {
	return m.Action
}

func (m *StepMeta) AutomatedRetries() *AutomatedRetry {
	return m.AutomatedRetry
}

// Dependencies exposes the dependencies this step has on other steps for the same service group.
func (m *StepMeta) Dependencies() []StepDependency {
	return m.DependsOn
}

// ExternalDependencies exposes the dependencies this step has on steps in *other* service groups. When provided,
// this will add to the default behavior of depending on "all" leaf outputs from the parent service group as
// configured in the topology. Be careful when using this to encode intent directly. Depending on steps from many
// service groups is supported. In single-service-group contexts, these dependencies are ignored.
func (m *StepMeta) ExternalDependencies() []ExternalStepDependency {
	return m.ExternalDependsOn
}

func (m *StepMeta) IsWellFormedOverInputs() bool {
	return true
}

func (m *StepMeta) ConsideredForServiceGroupCompletion() bool {
	return !m.OmitFromServiceGroupCompletion
}

type GenericStep struct {
	StepMeta `json:",inline"`
}

func (s *GenericStep) Description() string {
	return fmt.Sprintf("Step %s\n  Kind: %s", s.Name, s.Action)
}

func (s *GenericStep) RequiredInputs() []StepDependency {
	return []StepDependency{}
}

func (s *GenericStep) IsWellFormedOverInputs() bool {
	return false
}

type GenericValidationStep struct {
	StepMeta   `json:",inline"`
	Validation []string `json:"validation,omitempty"`
}

func (s *GenericValidationStep) Description() string {
	return fmt.Sprintf("Step %s\n  Kind: %s", s.Name, s.Action)
}

func (s *GenericValidationStep) RequiredInputs() []StepDependency {
	return []StepDependency{}
}

func (s *GenericValidationStep) Validations() []string {
	return s.Validation
}

func (s *GenericValidationStep) IsWellFormedOverInputs() bool {
	return false
}

type DryRun struct {
	Variables []Variable `json:"variables,omitempty"`
	Command   string     `json:"command,omitempty"`
}

const StepActionSafeFly = "SafeFly"

// SafeFlyStep submits a SafeFly request for the current rollout.
type SafeFlyStep struct {
	StepMeta      `json:",inline"`
	ShellIdentity Value `json:"shellIdentity"`
}

func (s *SafeFlyStep) Description() string {
	return fmt.Sprintf("Step %s\n  Kind: %s\n", s.Name, s.Action)
}

func (s *SafeFlyStep) RequiredInputs() []StepDependency {
	if s.ShellIdentity.Input == nil {
		return nil
	}
	return []StepDependency{s.ShellIdentity.Input.StepDependency}
}

// IsWellFormedOverInputs returns false because the current rollout is an implicit input.
func (s *SafeFlyStep) IsWellFormedOverInputs() bool {
	return false
}

const StepActionDelegateChildZone = "DelegateChildZone"

type DelegateChildZoneStep struct {
	StepMeta       `json:",inline"`
	ParentZone     Value `json:"parentZone,omitempty"`
	ChildZone      Value `json:"childZone,omitempty"`
	DeploymentMode Value `json:"deploymentMode,omitempty"`
	SecretKeyVault Value `json:"secretKeyVault,omitempty"`
	SecretName     Value `json:"secretName,omitempty"`
	DstsHost       Value `json:"dstsHost,omitempty"`
}

func (s *DelegateChildZoneStep) Description() string {
	return fmt.Sprintf("Step %s\n Kind: %s\n", s.Name, s.Action)
}

func (s *DelegateChildZoneStep) RequiredInputs() []StepDependency {
	var deps []StepDependency
	for _, val := range []Value{s.ParentZone, s.ChildZone, s.SecretKeyVault, s.SecretName, s.DstsHost, s.DeploymentMode} {
		if val.Input != nil {
			deps = append(deps, val.Input.StepDependency)
		}
	}
	slices.SortFunc(deps, SortDependencies)
	deps = slices.Compact(deps)
	return deps
}

const StepActionSetCertificateIssuer = "SetCertificateIssuer"

type SetCertificateIssuerStep struct {
	StepMeta       `json:",inline"`
	VaultBaseUrl   Value `json:"vaultBaseUrl,omitempty"`
	Issuer         Value `json:"issuer,omitempty"`
	SecretKeyVault Value `json:"secretKeyVault,omitempty"`
	SecretName     Value `json:"secretName,omitempty"`
	ApplicationId  Value `json:"applicationId,omitempty"`
}

func (s *SetCertificateIssuerStep) Description() string {
	return fmt.Sprintf("Step %s\n Kind: %s\n", s.Name, s.Action)
}

func (s *SetCertificateIssuerStep) RequiredInputs() []StepDependency {
	var deps []StepDependency
	for _, val := range []Value{s.VaultBaseUrl, s.Issuer, s.SecretKeyVault, s.SecretName, s.ApplicationId} {
		if val.Input != nil {
			deps = append(deps, val.Input.StepDependency)
		}
	}
	slices.SortFunc(deps, SortDependencies)
	deps = slices.Compact(deps)
	return deps
}

const StepActionCreateCertificate = "CreateCertificate"

const (
	CertificateManageEnabled  = "Enabled"
	CertificateManageDisabled = "Disabled"
)

type CreateCertificateStep struct {
	StepMeta        `json:",inline"`
	VaultBaseUrl    Value  `json:"vaultBaseUrl,omitempty"`
	CertificateName Value  `json:"certificateName,omitempty"`
	ContentType     Value  `json:"contentType,omitempty"`
	SAN             Value  `json:"san,omitempty"`
	Issuer          Value  `json:"issuer,omitempty"`
	SecretKeyVault  Value  `json:"secretKeyVault,omitempty"`
	SecretName      Value  `json:"secretName,omitempty"`
	ApplicationId   Value  `json:"applicationId,omitempty"`
	CommonName      Value  `json:"commonName,omitempty"`
	Manage          *Value `json:"manage,omitempty"`
}

func (s *CreateCertificateStep) Description() string {
	return fmt.Sprintf("Step %s\n Kind: %s\n", s.Name, s.Action)
}

func (s *CreateCertificateStep) RequiredInputs() []StepDependency {
	var deps []StepDependency
	for _, val := range []Value{s.VaultBaseUrl, s.CertificateName, s.ContentType, s.SAN, s.Issuer, s.SecretKeyVault, s.SecretName, s.ApplicationId, s.CommonName} {
		if val.Input != nil {
			deps = append(deps, val.Input.StepDependency)
		}
	}
	if s.Manage != nil && s.Manage.Input != nil {
		deps = append(deps, s.Manage.Input.StepDependency)
	}
	slices.SortFunc(deps, SortDependencies)
	deps = slices.Compact(deps)
	return deps
}

const StepActionResourceProviderRegistration = "ResourceProviderRegistration"

type ResourceProviderRegistrationStep struct {
	StepMeta                   `json:",inline"`
	ResourceProviderNamespaces Value `json:"resourceProviderNamespaces,omitempty"`
}

func (s *ResourceProviderRegistrationStep) Description() string {
	return fmt.Sprintf("Step %s\n Kind: %s\n", s.Name, s.Action)
}

func (s *ResourceProviderRegistrationStep) RequiredInputs() []StepDependency {
	var deps []StepDependency
	for _, val := range []Value{s.ResourceProviderNamespaces} {
		if val.Input != nil {
			deps = append(deps, val.Input.StepDependency)
		}
	}
	slices.SortFunc(deps, SortDependencies)
	deps = slices.Compact(deps)
	return deps
}

const (
	StepActionRPLogs      = "RPLogsAccount"
	StepActionClusterLogs = "ClusterLogsAccount"
)

type LogsStep struct {
	StepMeta             `json:",inline"`
	RolloutKind          string           `json:"rolloutKind,omitempty"`
	TypeName             Value            `json:"typeName"`
	SecretKeyVault       Value            `json:"secretKeyVault,omitempty"`
	SecretName           Value            `json:"secretName,omitempty"`
	Environment          Value            `json:"environment"`
	AccountName          Value            `json:"accountName"`
	MetricsAccount       Value            `json:"metricsAccount"`
	AdminAlias           Value            `json:"adminAlias"`
	AdminGroup           Value            `json:"adminGroup"`
	SubscriptionId       Value            `json:"subscriptionId,omitempty"`
	Namespace            Value            `json:"namespace,omitempty"`
	CertSAN              Value            `json:"certsan,omitempty"`
	CertDescription      Value            `json:"certdescription,omitempty"`
	ConfigVersion        Value            `json:"configVersion,omitempty"`
	MonikerDefaultRegion Value            `json:"monikerDefaultRegion,omitempty"`
	Database             Value            `json:"database,omitempty"`
	EventSources         map[string]Event `json:"eventSources,omitempty"`
	MdsdConfigFile       *Value           `json:"mdsdConfigFile,omitempty"`
}

type Event struct {
	Name    string `json:"name,omitempty"`
	Account string `json:"account,omitempty"`
}

func (s *LogsStep) Description() string {
	return fmt.Sprintf("Step %s\n Kind: %s\n RolloutKind: %s\n", s.Name, s.Action, s.RolloutKind)
}

func (s *LogsStep) RequiredInputs() []StepDependency {
	var deps []StepDependency
	for _, val := range []Value{s.TypeName, s.SecretKeyVault, s.SecretName, s.Environment, s.AccountName, s.MetricsAccount, s.AdminAlias, s.AdminGroup, s.SubscriptionId, s.Namespace, s.CertSAN, s.CertDescription, s.ConfigVersion, s.MonikerDefaultRegion, s.Database} {
		if val.Input != nil {
			deps = append(deps, val.Input.StepDependency)
		}
	}
	if s.MdsdConfigFile != nil && s.MdsdConfigFile.Input != nil {
		deps = append(deps, s.MdsdConfigFile.Input.StepDependency)
	}
	slices.SortFunc(deps, SortDependencies)
	deps = slices.Compact(deps)
	return deps
}

const StepActionFeatureRegistration = "FeatureRegistration"

type FeatureRegistrationStep struct {
	StepMeta          `json:",inline"`
	SecretKeyVault    Value  `json:"secretKeyVault,omitempty"`
	SecretName        Value  `json:"secretName,omitempty"`
	ProviderConfigRef string `json:"providerConfigRef,omitempty"`
}

func (s *FeatureRegistrationStep) Description() string {
	return fmt.Sprintf("Step %s\n Kind: %s\n", s.Name, s.Action)
}

func (s *FeatureRegistrationStep) RequiredInputs() []StepDependency {
	var deps []StepDependency
	for _, val := range []Value{s.SecretKeyVault, s.SecretName} {
		if val.Input != nil {
			deps = append(deps, val.Input.StepDependency)
		}
	}
	slices.SortFunc(deps, SortDependencies)
	deps = slices.Compact(deps)
	return deps
}

const StepActionProviderFeatureRegistration = "ProviderFeatureRegistration"

type ProviderFeatureRegistrationStep struct {
	StepMeta          `json:",inline"`
	ProviderConfigRef string `json:"providerConfigRef,omitempty"`
	IdentityFrom      Input  `json:"identityFrom,omitempty"`
}

func (s *ProviderFeatureRegistrationStep) Description() string {
	return fmt.Sprintf("Step %s\n Kind: %s\n", s.Name, s.Action)
}

func (s *ProviderFeatureRegistrationStep) RequiredInputs() []StepDependency {
	return []StepDependency{s.IdentityFrom.StepDependency}
}

const StepActionEv2Registration = "Ev2Registration"

type Ev2RegistrationStep struct {
	StepMeta     `json:",inline"`
	IdentityFrom Input `json:"identityFrom,omitempty"`
}

func (s *Ev2RegistrationStep) Description() string {
	return fmt.Sprintf("Step %s\n Kind: %s\n", s.Name, s.Action)
}

func (s *Ev2RegistrationStep) RequiredInputs() []StepDependency {
	return []StepDependency{s.IdentityFrom.StepDependency}
}

const StepActionSecretSync = "SecretSync"

type SecretSyncStep struct {
	StepMeta          `json:",inline"`
	ConfigurationFile string `json:"configurationFile,omitempty"`
	KeyVault          string `json:"keyVault,omitempty"`
	EncryptionKey     string `json:"encryptionKey,omitempty"`
	IdentityFrom      Input  `json:"identityFrom,omitempty"`
}

func (s *SecretSyncStep) Description() string {
	return fmt.Sprintf("Step %s\n Kind: %s\n", s.Name, s.Action)
}

func (s *SecretSyncStep) RequiredInputs() []StepDependency {
	return []StepDependency{s.IdentityFrom.StepDependency}
}

const StepActionKusto = "Kusto"

type KustoStep struct {
	StepMeta         `json:",inline"`
	SecretKeyVault   Value `json:"secretKeyVault,omitempty"`
	SecretName       Value `json:"secretName,omitempty"`
	ApplicationId    Value `json:"applicationId,omitempty"`
	ConnectionString Value `json:"connectionString,omitempty"`
	Command          Value `json:"command,omitempty"`
}

func (s *KustoStep) Description() string {
	return fmt.Sprintf("Step %s\n Kind: %s\n", s.Name, s.Action)
}

func (s *KustoStep) RequiredInputs() []StepDependency {
	var deps []StepDependency
	for _, val := range []Value{s.SecretKeyVault, s.SecretName, s.ApplicationId, s.ConnectionString, s.Command} {
		if val.Input != nil {
			deps = append(deps, val.Input.StepDependency)
		}
	}
	slices.SortFunc(deps, SortDependencies)
	deps = slices.Compact(deps)
	return deps
}

const StepActionPav2 = "Pav2"

type Pav2Step struct {
	StepMeta                   `json:",inline"`
	SecretKeyVault             Value  `json:"secretKeyVault,omitempty"`
	SecretName                 Value  `json:"secretName,omitempty"`
	StorageAccount             Value  `json:"storageAccount,omitempty"`
	SMEEndpointSuffixParameter Value  `json:"smeEndpointSuffixParameter,omitempty"`
	SMEAppidParameter          Value  `json:"smeAppidParameter,omitempty"`
	Operation                  string `json:"operation,omitempty"`
}

func (s *Pav2Step) Description() string {
	return fmt.Sprintf("Step %s\n Kind: %s\n Operation: %s\n", s.Name, s.Action, s.Operation)
}

func (s *Pav2Step) RequiredInputs() []StepDependency {
	var deps []StepDependency
	for _, val := range []Value{s.SecretKeyVault, s.SecretName, s.StorageAccount, s.SMEEndpointSuffixParameter, s.SMEAppidParameter} {
		if val.Input != nil {
			deps = append(deps, val.Input.StepDependency)
		}
	}
	slices.SortFunc(deps, SortDependencies)
	deps = slices.Compact(deps)
	return deps
}

const StepActionHelm = "Helm"

type HelmStep struct {
	StepMeta `json:",inline"`

	// AKSCluster is the name of the AKS cluster onto which this Helm release will be deployed.
	AKSCluster string `json:"aksCluster"`
	// SubnetName is an optional specifier for the name of the subnet to which the deployer must connect to before
	// accessing the AKS cluster. Provide this value when deploying to private clusters.
	SubnetName string `json:"subnetName,omitempty"`

	// ReleaseName is the semantically-meaningful name of the Helm release. The first deployment for a given name
	// will install the Helm chart, further deployments to the same name will upgrade it.
	ReleaseName string `json:"releaseName"`
	// ReleaseNamespace is the name of the namespace in which the Helm release should be deployed, analogous to the
	// --namespace flag for the Helm CLI. This namespace will be created before the Helm release is installed.
	ReleaseNamespace string `json:"releaseNamespace"`
	// NamespaceFiles specify namespaces that must be created before the Helm release is installed. It is *not* required
	// to specify the release namespace manifest here, but it may be present if a complex configuration (with labels,
	// annotations, spec, etc.) is required. By default, the release namespace will be created with no additional fields
	// set if no additional manifests are specified in this field.
	// NOTE: These files will be pre-processed as Go templates to resolve configuration fields and input variables.
	NamespaceFiles []string `json:"namespaceFiles,omitempty"`
	// ChartDir is the relative path from the pipeline configuration to the chart being deployed.
	ChartDir string `json:"chartDir"`
	// ValuesFile is the path to the Helm values file to use when deploying the Helm release.
	// NOTE: This file will be pre-processed as a Go template to resolve configuration fields and input variables.
	ValuesFile string `json:"valuesFile,omitempty"`

	// KustoDatabase is the name of the Kusto database within the cluster that holds the logs for this Helm deployment.
	KustoDatabase string `json:"kustoDatabase,omitempty"`
	// KustoTable is the name of the Kusto table that holds the logs for this Helm deployment within the appropriate cluster/database.
	KustoTable string `json:"kustoTable,omitempty"`
	// KustoEndpoint is the input that provides the Kusto endpoint URI for this Helm deployment.
	KustoEndpoint *Input `json:"kustoEndpoint,omitempty"`

	// InputVariables records a mapping from variable names to the output variable that provides the value.
	// For some input variable like:
	//     inputVariables:
	//       someImportantThing:
	//         resourceGroup: regional
	//         step: output
	//         name: outputVariableName
	// Refer to this value in the namespace files or values.yaml with __someImportantThing__.
	InputVariables map[string]Input `json:"inputVariables,omitempty"`

	// IdentityFrom specifies the managed identity with which this deployment will run in Ev2.
	IdentityFrom Input `json:"identityFrom,omitempty"`

	// Timeout is the amount of time to wait for the Helm release to be deployed.
	Timeout string `json:"timeout,omitempty"`

	// RollbackOnFailure indicates whether to rollback to previous version after upgrade failure and uninstall on install failure.
	RollbackOnFailure bool `json:"rollbackOnFailure,omitempty"`
}

func (s *HelmStep) Description() string {
	return fmt.Sprintf("Step %s\n Kind: %s\n", s.Name, s.Action)
}

func (s *HelmStep) RequiredInputs() []StepDependency {
	deps := []StepDependency{s.IdentityFrom.StepDependency}
	if s.KustoEndpoint != nil {
		deps = append(deps, s.KustoEndpoint.StepDependency)
	}
	for _, val := range s.InputVariables {
		deps = append(deps, val.StepDependency)
	}
	slices.SortFunc(deps, SortDependencies)
	deps = slices.Compact(deps)
	return deps
}

const StepActionRunGenevaAction = "RunGenevaAction"

type RunGenevaActionStep struct {
	StepMeta          `json:",inline"`
	SecretKeyVault    Value            `json:"secretKeyVault,omitempty"`
	SecretName        Value            `json:"secretName,omitempty"`
	GAExtensionName   Value            `json:"gaExtensionName,omitempty"`
	GAEndpoint        Value            `json:"gaEndpoint,omitempty"`
	GAOperationId     Value            `json:"gaOperationId,omitempty"`
	MaxExecutionTime  string           `json:"maxExecutionTime,omitempty"`
	PayloadProperties map[string]Value `json:"payloadProperties,omitempty"`
}

func (s *RunGenevaActionStep) Description() string {
	return fmt.Sprintf("Step %s\n  Kind: %s\n  Extension: %s\n  Operation: %s\n", s.Name, s.Action, s.GAExtensionName.String(), s.GAOperationId.String())
}

func (s *RunGenevaActionStep) RequiredInputs() []StepDependency {
	var deps []StepDependency
	for _, val := range []Value{s.SecretKeyVault, s.SecretName, s.GAExtensionName, s.GAEndpoint, s.GAOperationId} {
		if val.Input != nil {
			deps = append(deps, val.Input.StepDependency)
		}
	}
	for _, val := range s.PayloadProperties {
		if val.Input != nil {
			deps = append(deps, val.Input.StepDependency)
		}
	}
	slices.SortFunc(deps, SortDependencies)
	deps = slices.Compact(deps)
	return deps
}

const StepActionPublishGenevaAction = "PublishGenevaAction"

type PublishGenevaActionStep struct {
	StepMeta        `json:",inline"`
	SecretKeyVault  Value  `json:"secretKeyVault,omitempty"`
	SecretName      Value  `json:"secretName,omitempty"`
	GAExtensionName Value  `json:"gaExtensionName,omitempty"`
	GAPackagePath   string `json:"gaPackagePath,omitempty"`
	UseBetaEndpoint bool   `json:"useBetaEndpoint,omitempty"`

	GenevaActionArtifact AdoArtifactDownloadPipelineReference `json:"genevaActionArtifact,omitempty"`
}

func (s *PublishGenevaActionStep) Description() string {
	return fmt.Sprintf("Step %s\n  Kind: %s\n  Action: %s\n", s.Name, s.Action, s.GAExtensionName.Value)
}

func (s *PublishGenevaActionStep) RequiredInputs() []StepDependency {
	var deps []StepDependency
	for _, val := range []Value{s.SecretKeyVault, s.SecretName, s.GAExtensionName} {
		if val.Input != nil {
			deps = append(deps, val.Input.StepDependency)
		}
	}

	slices.SortFunc(deps, SortDependencies)
	deps = slices.Compact(deps)
	return deps
}

func (s *PublishGenevaActionStep) IsWellFormedOverInputs() bool {
	return true
}

const StepActionGenevaHealth = "GenevaHealth"

type GenevaHealthStep struct {
	StepMeta                  `json:",inline"`
	SecretKeyVault            Value                                `json:"secretKeyVault,omitempty"`
	SecretName                Value                                `json:"secretName,omitempty"`
	MonitoringAccountName     Value                                `json:"monitoringAccountName,omitempty"`
	MonitorConfigPath         string                               `json:"monitorConfigPath,omitempty"`
	TopologyConfigPath        string                               `json:"topologyConfigPath,omitempty"`
	ConfigPackagePath         string                               `json:"configPackagePath,omitempty"`
	MonitorV2ScopeBindingFile string                               `json:"monitorV2ScopeBindingFile,omitempty"`
	AdditionalScopeBindings   map[string]Value                     `json:"additionalScopeBindings,omitempty"`
	GenevaConfigsArtifact     AdoArtifactDownloadPipelineReference `json:"genevaConfigsArtifact,omitempty"`
}

func (s *GenevaHealthStep) Description() string {
	return fmt.Sprintf("Step %s\n  Kind: %s\n  Account: %s\n", s.Name, s.Action, s.MonitoringAccountName.Value)
}

func (s *GenevaHealthStep) RequiredInputs() []StepDependency {
	var deps []StepDependency
	for _, val := range []Value{s.SecretKeyVault, s.SecretName, s.MonitoringAccountName} {
		if val.Input != nil {
			deps = append(deps, val.Input.StepDependency)
		}
	}
	for _, val := range s.AdditionalScopeBindings {
		if val.Input != nil {
			deps = append(deps, val.Input.StepDependency)
		}
	}
	slices.SortFunc(deps, SortDependencies)
	deps = slices.Compact(deps)
	return deps
}

func (s *GenevaHealthStep) IsWellFormedOverInputs() bool {
	return true
}

const StepActionPublishGenevaAutomation = "PublishGenevaAutomation"

type PublishGenevaAutomationStep struct {
	StepMeta                   `json:",inline"`
	SecretKeyVault             Value  `json:"secretKeyVault,omitempty"`
	KustoClientSecretName      Value  `json:"kustoClientSecretName,omitempty"`
	GenevaAutomationSecretName Value  `json:"genevaAutomationSecretName,omitempty"`
	IcmServiceId               Value  `json:"icmServiceId,omitempty"`
	IcmTeamId                  *Value `json:"icmTeamId,omitempty"`
	WorkflowPath               string `json:"workflowPath,omitempty"`

	GenevaAutomationArtifact AdoArtifactDownloadPipelineReference `json:"genevaAutomationArtifact,omitempty"`
}

func (s *PublishGenevaAutomationStep) Description() string {
	return fmt.Sprintf("Step %s\n  Kind: %s\n", s.Name, s.Action)
}

func (s *PublishGenevaAutomationStep) RequiredInputs() []StepDependency {
	var deps []StepDependency
	for _, val := range []Value{s.SecretKeyVault, s.KustoClientSecretName, s.GenevaAutomationSecretName, s.IcmServiceId} {
		if val.Input != nil {
			deps = append(deps, val.Input.StepDependency)
		}
	}
	if s.IcmTeamId != nil && s.IcmTeamId.Input != nil {
		deps = append(deps, s.IcmTeamId.Input.StepDependency)
	}

	slices.SortFunc(deps, SortDependencies)
	deps = slices.Compact(deps)
	return deps
}

func (s *PublishGenevaAutomationStep) IsWellFormedOverInputs() bool {
	return true
}

const StepActionProwJob = "ProwJob"

type ProwJobStep struct {
	StepMeta `json:",inline"`

	TokenKeyvault        string `json:"tokenKeyvault"`
	TokenSecret          string `json:"tokenSecret"`
	JobName              string `json:"jobName"`
	GatePromotion        string `json:"gatePromotion"`                  // string-encoded boolean, passed to command as a flag
	AllowedSubscriptions string `json:"allowedSubscriptions,omitempty"` // optional slot-manager subscription allowlist override; see slot-manager docs
	Commit               string `json:"commit,omitempty"`               // optional source commit SHA to pin the Prow job to
	Repo                 string `json:"repo,omitempty"`                 // optional GitHub repo name override (default: ARO-HCP)
	BaseRef              string `json:"baseRef,omitempty"`              // optional Git base ref override (default: main)
	DryRun               Value  `json:"dryRun,omitempty"`

	// IdentityFrom specifies the managed identity with which this deployment will run in Ev2.
	IdentityFrom Input `json:"identityFrom,omitempty"`
}

func (s *ProwJobStep) Description() string {
	return fmt.Sprintf("Step %s\n  Kind: %s\n", s.Name, s.Action)
}

func (s *ProwJobStep) RequiredInputs() []StepDependency {
	var deps []StepDependency
	if s.DryRun.Input != nil {
		deps = append(deps, s.DryRun.Input.StepDependency)
	}
	for _, val := range []Input{s.IdentityFrom} {
		deps = append(deps, val.StepDependency)
	}

	slices.SortFunc(deps, SortDependencies)
	deps = slices.Compact(deps)
	return deps
}

func (s *ProwJobStep) IsWellFormedOverInputs() bool {
	return true
}

// ProwJobValidationStep represents a shell step that is a validation step.
type ProwJobValidationStep struct {
	ProwJobStep `json:",inline"`
	Validation  []string `json:"validation,omitempty"`
}

func (s *ProwJobValidationStep) Validations() []string {
	return s.Validation
}

func (s *ProwJobValidationStep) IsWellFormedOverInputs() bool {
	// raw shell steps capture the whole repository as an archive input, so they are not well-formed
	return false
}

const StepActionGrafanaDashboards = "GrafanaDashboards"

type GrafanaDashboardsStep struct {
	StepMeta `json:",inline"`

	GrafanaName         string `json:"grafanaName"`
	ObservabilityConfig string `json:"observabilityConfig"`
	Timeout             string `json:"timeout,omitempty"`

	// IdentityFrom specifies the managed identity with which this deployment will run in Ev2.
	IdentityFrom Input `json:"identityFrom,omitempty"`
}

func (s *GrafanaDashboardsStep) Description() string {
	return fmt.Sprintf("Step %s\n  Kind: %s\n", s.Name, s.Action)
}

func (s *GrafanaDashboardsStep) RequiredInputs() []StepDependency {
	var deps []StepDependency
	for _, val := range []Input{s.IdentityFrom} {
		deps = append(deps, val.StepDependency)
	}

	slices.SortFunc(deps, SortDependencies)
	deps = slices.Compact(deps)
	return deps
}

func (s *GrafanaDashboardsStep) IsWellFormedOverInputs() bool {
	return true
}

const StepActionGrafanaManage = "GrafanaManage"

type GrafanaADXIntegrations struct {
	Enabled          Value `json:"enabled"`
	Environment      Value `json:"environment,omitempty"`
	Geographies      Value `json:"geographies,omitempty"`
	Scenario         Value `json:"scenario,omitempty"`
	TargetResourceID Value `json:"targetResourceId,omitempty"`
}

type GrafanaManageStep struct {
	StepMeta `json:",inline"`

	GrafanaName              Value                   `json:"grafanaName"`
	Location                 Value                   `json:"location"`
	SKU                      Value                   `json:"sku,omitempty"`
	MajorVersion             Value                   `json:"majorVersion,omitempty"`
	ZoneRedundancy           Value                   `json:"zoneRedundancy,omitempty"`
	PublicNetworkAccess      Value                   `json:"publicNetworkAccess,omitempty"`
	CrossTenantSecurityGroup Value                   `json:"crossTenantSecurityGroup,omitempty"`
	ADX                      *GrafanaADXIntegrations `json:"adx,omitempty"`

	DiscoveryTagKey Value  `json:"discoveryTagKey,omitempty"`
	Timeout         string `json:"timeout,omitempty"`

	// IdentityFrom specifies the managed identity with which this deployment will run in Ev2.
	IdentityFrom Input `json:"identityFrom,omitempty"`
}

func (s *GrafanaManageStep) Description() string {
	return fmt.Sprintf("Step %s\n  Kind: %s\n", s.Name, s.Action)
}

func (s *GrafanaManageStep) RequiredInputs() []StepDependency {
	var deps []StepDependency
	for _, val := range []Value{s.GrafanaName, s.Location, s.SKU, s.MajorVersion, s.ZoneRedundancy, s.PublicNetworkAccess, s.CrossTenantSecurityGroup, s.DiscoveryTagKey} {
		if val.Input != nil {
			deps = append(deps, val.Input.StepDependency)
		}
	}
	if s.ADX != nil {
		for _, val := range []Value{
			s.ADX.Enabled,
			s.ADX.Environment,
			s.ADX.Geographies,
			s.ADX.Scenario,
			s.ADX.TargetResourceID,
		} {
			if val.Input != nil {
				deps = append(deps, val.Input.StepDependency)
			}
		}
	}
	deps = append(deps, s.IdentityFrom.StepDependency)
	slices.SortFunc(deps, SortDependencies)
	deps = slices.Compact(deps)
	return deps
}

func (s *GrafanaManageStep) IsWellFormedOverInputs() bool {
	return true
}

const StepActionGrafanaDatasources = "GrafanaDatasources"

type GrafanaDatasourcesStep struct {
	StepMeta `json:",inline"`

	GrafanaName string `json:"grafanaName"`

	// SkipSync indicates whether to skip syncing datasources. It is intended for prow jobs to skip syncing datasources.
	SkipSync string `json:"skipSync,omitempty"`

	DiscoveryTagKey string `json:"discoveryTagKey,omitempty"`

	Timeout string `json:"timeout,omitempty"`

	// IdentityFrom specifies the managed identity with which this deployment will run in Ev2.
	IdentityFrom Input `json:"identityFrom,omitempty"`
}

func (s *GrafanaDatasourcesStep) Description() string {
	return fmt.Sprintf("Step %s\n  Kind: %s\n", s.Name, s.Action)
}

func (s *GrafanaDatasourcesStep) RequiredInputs() []StepDependency {
	var deps []StepDependency
	for _, val := range []Input{s.IdentityFrom} {
		deps = append(deps, val.StepDependency)
	}

	slices.SortFunc(deps, SortDependencies)
	deps = slices.Compact(deps)
	return deps
}

func (s *GrafanaDatasourcesStep) IsWellFormedOverInputs() bool {
	return true
}

const StepActionKustoEntityGroups = "KustoEntityGroups"

type KustoEntityGroupsStep struct {
	StepMeta `json:",inline"`

	// EntityGroups is a list of entity group definitions in "name:database" format.
	EntityGroups []string `json:"entityGroups"`

	// Environment optionally scopes Kusto cluster discovery to a single ARO-HCP
	// environment (for example int, stg or prod) so each environment gets its own
	// isolated entity group. When empty, discovery spans every cluster carrying
	// the aroHCPPurpose tag (legacy cross-environment behavior).
	Environment string `json:"environment,omitempty"`

	Timeout string `json:"timeout,omitempty"`

	// IdentityFrom specifies the managed identity with which this deployment will run in Ev2.
	IdentityFrom Input `json:"identityFrom,omitempty"`
}

func (s *KustoEntityGroupsStep) Description() string {
	return fmt.Sprintf("Step %s\n  Kind: %s\n", s.Name, s.Action)
}

func (s *KustoEntityGroupsStep) RequiredInputs() []StepDependency {
	var deps []StepDependency
	deps = append(deps, s.IdentityFrom.StepDependency)
	slices.SortFunc(deps, SortDependencies)
	deps = slices.Compact(deps)
	return deps
}

func (s *KustoEntityGroupsStep) IsWellFormedOverInputs() bool {
	return true
}
