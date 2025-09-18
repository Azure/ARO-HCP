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
	"encoding/json"
	"fmt"

	"github.com/Azure/ARO-HCP/internal/api"
)

type HCPCluster struct {
	TypedDocument `json:",inline"`

	HCPClusterProperties `json:"properties"`
}

type HCPClusterProperties struct {
	ResourceDocument `json:",inline"`

	CustomerDesiredState CustomerDesiredHCPClusterState `json:"customerDesiredState"`
	ServiceProviderState ServiceProviderHCPClusterState `json:"serviceProviderState"`
}

type CustomerDesiredHCPClusterState struct {
	// HCPOpenShiftCluster contains the desired state from a customer.  It is filtered to only those fields that customers
	// are able to set.
	// We will eventually select specific fields which customers own and blank out everything else.
	// Alternatively, we could choose a different structure, but it's probably easier to re-use this one.
	// There is no validation on this structure.
	HCPOpenShiftCluster api.CustomerClusterProperties `json:"clusterProperties"`
}

type ServiceProviderHCPClusterState struct {
	// HCPOpenShiftCluster contains the service provider owned state.  It is filtered to only those fields that the service provider owns.
	// We will eventually select specific fields which the service provider owns and blank out everything else.
	// Alternatively, we could choose a different structure, but it's probably easier to re-use this one.
	// There is no validation on this structure.
	HCPOpenShiftCluster api.ServiceProviderClusterProperties `json:"clusterProperties"`
}

var FilterHCPClusterState ResourceDocumentStateFilter = newJSONRoundTripFilterer(
	func() any { return &CustomerDesiredHCPClusterState{} },
	func() any { return &ServiceProviderHCPClusterState{} },
)

type jsonRoundTripFilterer struct {
	newCustomerDesiredStateFn func() any
	newServiceProviderStateFn func() any
}

func newJSONRoundTripFilterer(newCustomerDesiredStateFn, newServiceProviderStateFn func() any) ResourceDocumentStateFilter {
	return jsonRoundTripFilterer{
		newCustomerDesiredStateFn: newCustomerDesiredStateFn,
		newServiceProviderStateFn: newServiceProviderStateFn,
	}
}

func (r jsonRoundTripFilterer) RemoveUnknownFields(toMutate *ResourceDocument) error {
	filteredCustomerDesiredState, err := superExpensiveButSimpleRoundFilterForUnknownFields(toMutate.CustomerDesiredState, r.newCustomerDesiredStateFn())
	if err != nil {
		return err
	}
	toMutate.CustomerDesiredState = filteredCustomerDesiredState

	filteredServiceProviderState, err := superExpensiveButSimpleRoundFilterForUnknownFields(toMutate.ServiceProviderState, r.newCustomerDesiredStateFn())
	if err != nil {
		return err
	}
	toMutate.ServiceProviderState = filteredServiceProviderState

	return nil
}

func superExpensiveButSimpleRoundFilterForUnknownFields(startingMap map[string]any, filterObj any) (map[string]any, error) {
	if len(startingMap) == 0 {
		return startingMap, nil
	}
	currBytes, err := json.Marshal(startingMap)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal original: %w", err)
	}
	if err := json.Unmarshal(currBytes, filterObj); err != nil {
		return nil, fmt.Errorf("failed to unmarshal into filterObj: %w", err)
	}
	filteredBytes, err := json.Marshal(filterObj)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal filterObj: %w", err)
	}
	filteredMap := map[string]any{}
	if err := json.Unmarshal(filteredBytes, &filteredMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal into filtered map: %w", err)
	}

	return filteredMap, nil
}
