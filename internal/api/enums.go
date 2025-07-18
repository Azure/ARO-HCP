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

package api

import "github.com/Azure/ARO-HCP/internal/api/arm"

// DiskStorageAccountType represents supported Azure storage account types.
type DiskStorageAccountType string

const (
	DiskStorageAccountTypePremium_LRS     DiskStorageAccountType = "Premium_LRS"
	DiskStorageAccountTypeStandardSSD_LRS DiskStorageAccountType = "StandardSSD_LRS"
	DiskStorageAccountTypeStandard_LRS    DiskStorageAccountType = "Standard_LRS"
)

// NetworkType represents an OpenShift cluster network plugin.
type NetworkType string

const (
	NetworkTypeOVNKubernetes NetworkType = "OVNKubernetes"
	NetworkTypeOther         NetworkType = "Other"
)

// OutboundType represents a routing strategy to provide egress to the Internet.
type OutboundType string

const (
	OutboundTypeLoadBalancer OutboundType = "LoadBalancer"
)

// Visibility represents the visibility of an API endpoint.
type Visibility string

const (
	VisibilityPublic  Visibility = "Public"
	VisibilityPrivate Visibility = "Private"
)

type Effect string

const (
	// EffectNoExecute - NoExecute taint effect
	EffectNoExecute Effect = "NoExecute"
	// EffectNoSchedule - NoSchedule taint effect
	EffectNoSchedule Effect = "NoSchedule"
	// EffectPreferNoSchedule - PreferNoSchedule taint effect
	EffectPreferNoSchedule Effect = "PreferNoSchedule"
)

type CustomerManagedEncryptionType string

const (
	// CustomerManagedEncryptionTypeKMS - KMS encryption type.
	CustomerManagedEncryptionTypeKMS CustomerManagedEncryptionType = "KMS"
)

type EtcdDataEncryptionKeyManagementModeType string

const (
	// EtcdDataEncryptionKeyManagementModeTypeCustomerManaged - Customer managed encryption key management mode type.
	EtcdDataEncryptionKeyManagementModeTypeCustomerManaged EtcdDataEncryptionKeyManagementModeType = "CustomerManaged"
	// EtcdDataEncryptionKeyManagementModeTypePlatformManaged - Platform managed encryption key management mode type.
	EtcdDataEncryptionKeyManagementModeTypePlatformManaged EtcdDataEncryptionKeyManagementModeType = "PlatformManaged"
)

// ClusterImageRegistryProfileState - state indicates the desired ImageStream-backed cluster image registry installation mode.
// This can only be set during cluster creation and cannot be changed after cluster creation. Enabled means the
// ImageStream-backed image registry will be run as pods on worker nodes in the cluster. Disabled means the ImageStream-backed
// image registry will not be present in the cluster. The default is Enabled.
type ClusterImageRegistryProfileState string

const (
	ClusterImageRegistryProfileStateDisabled ClusterImageRegistryProfileState = "Disabled"
	ClusterImageRegistryProfileStateEnabled  ClusterImageRegistryProfileState = "Enabled"
)

type TokenValidationRuleType string

const (
	// TokenValidationRuleTypeRequiredClaim - the Kubernetes API server will be configured to validate that the
	// incoming JWT contains the required claim and that its value matches the required value.
	TokenValidationRuleTypeRequiredClaim TokenValidationRuleType = "RequiredClaim"
)

type ExternalAuthProvisionState arm.ProvisioningState

const (
	// ProvisioningStateAwaitingSecret - A non-terminal state indicating that a client
	// is awaiting a secret.
	ProvisioningStateAwaitingSecret ExternalAuthProvisionState = "AwaitingSecret"
)

type ExternalAuthClientType string

const (
	// ExternalAuthClientTypeConfidential - the client is confidential.
	ExternalAuthClientTypeConfidential ExternalAuthClientType = "Confidential"
	// ExternalAuthClientTypePublic - the client is public.
	ExternalAuthClientTypePublic ExternalAuthClientType = "Public"
)

type ExternalAuthConditionType string

const (
	// ExternalAuthConditionTypeAvailable - the resource is in an available state.
	ExternalAuthConditionTypeAvailable ExternalAuthConditionType = "Available"
	// ExternalAuthConditionType - the resource is in a degraded state.
	ExternalAuthConditionTypeDegraded ExternalAuthConditionType = "Degraded"
	// ExternalAuthConditionTypeProgressing - the resource is in a progressing state.
	ExternalAuthConditionTypeProgressing ExternalAuthConditionType = "Progressing"
)

type ConditionStatusType string

const (
	// ConditionStatusType - the condition status is true.
	ConditionStatusTypeTrue ConditionStatusType = "True"
	// ExternalAuthConditionTypeFalse - the condition status is false.
	ConditionStatusTypeFalse ConditionStatusType = "False"
	// ConditionStatusTypeUnknown - the condition status is unknown.
	ConditionStatusTypeUnknown ConditionStatusType = "Unknown"
)
