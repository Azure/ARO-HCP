// Copyright 2026 Microsoft Corporation
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

// Package leaseenv defines the environment contract shared by lease publishers
// and consumers, without depending on the CLI, catalogs, or Azure SDK.
package leaseenv

const (
	// DeployEnvironment selects the configuration overlay environment. It is not
	// RuntimeDeployEnvironment, which is published by slot acquisition.
	DeployEnvironment            = "DEPLOY_ENV"
	RuntimeDeployEnvironment     = "ARO_HCP_DEPLOY_ENV"
	MSIMockSP                    = "LEASED_MSI_MOCK_SP"
	ARMHelperSP                  = "LEASED_ARM_HELPER_SP"
	MSIContainers                = "LEASED_MSI_CONTAINERS"
	InfrastructureIdentityBundle = "LEASED_INFRASTRUCTURE_IDENTITY_BUNDLE"
)

// InfrastructureIdentityBundlePayload is the JSON value carried by
// InfrastructureIdentityBundle. Management maps are keyed by decimal stamp.
// Consumers validate required fields and selected resource names before use.
type InfrastructureIdentityBundlePayload struct {
	ServiceResourceGroup       string            `json:"serviceResourceGroup"`
	ServiceAksIdentityName     string            `json:"serviceAksIdentityName"`
	ManagementResourceGroups   map[string]string `json:"managementResourceGroups"`
	ManagementAksIdentityNames map[string]string `json:"managementAksIdentityNames"`
}
