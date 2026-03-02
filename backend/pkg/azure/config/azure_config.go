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

package config

import (
	apisconfigv1 "github.com/Azure/ARO-HCP/backend/pkg/apis/config/v1"
)

// AzureConfig represents Azure related configuration used by the service
type AzureConfig struct {
	// Cloud environment where the service is running on
	CloudEnvironment *AzureCloudEnvironment
	// AzureRuntimeConfig holds additional serialized configuration provided
	// to the service via a configuration file. This
	// is useful for pulling direct values from it.
	AzureRuntimeConfig *apisconfigv1.AzureRuntimeConfig

	// Other attributes in the future like the operators managed identities
	// configuration
	// OperatorsManagedIdentitiesConfig AzureOperatorsManagedIdentitiesConfig
}
