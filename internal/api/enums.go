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
	OutboundTypeLoadBalancer OutboundType = "loadBalancer"
)

// Visibility represents the visibility of an API endpoint.
type Visibility string

const (
	VisibilityPublic  Visibility = "public"
	VisibilityPrivate Visibility = "private"
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

// OptionalClusterCapability - Cluster capabilities that can be disabled.
type OptionalClusterCapability string

const (
	// OptionalClusterCapabilityImageRegistry - Enables the OpenShift internal image registry.
	OptionalClusterCapabilityImageRegistry OptionalClusterCapability = "ImageRegistry"
)
