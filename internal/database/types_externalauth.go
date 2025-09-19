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

package database

import (
	"github.com/Azure/ARO-HCP/internal/api"
)

type ExternalAuth struct {
	TypedDocument `json:",inline"`

	ExternalAuthProperties `json:"properties"`
}

type ExternalAuthProperties struct {
	ResourceDocument `json:",inline"`

	CustomerDesiredState CustomerDesiredExternalAuthState `json:"customerDesiredState"`
	ServiceProviderState ServiceProviderExternalAuthState `json:"serviceProviderState"`
}

type CustomerDesiredExternalAuthState struct {
	// ExternalAuth contains the desired state from a customer.  It is filtered to only those fields that customers
	// are able to set.
	// We will eventually select specific fields which customers own and blank out everything else.
	// Alternatively, we could choose a different structure, but it's probably easier to re-use this one.
	// There is no validation on this structure.
	ExternalAuth api.HCPOpenShiftClusterExternalAuthProperties `json:"externalAuthProperties"`
}

type ServiceProviderExternalAuthState struct {
	// ExternalAuth contains the service provider owned state.  It is filtered to only those fields that the service provider owns.
	// We will eventually select specific fields which the service provider owns and blank out everything else.
	// Alternatively, we could choose a different structure, but it's probably easier to re-use this one.
	// There is no validation on this structure.
	ExternalAuth api.HCPOpenShiftClusterExternalAuthProperties `json:"externalAuthProperties"`
}

var FilterExternalAuthState ResourceDocumentStateFilter = newJSONRoundTripFilterer(
	func() any { return &CustomerDesiredExternalAuthState{} },
	func() any { return &ServiceProviderExternalAuthState{} },
)
