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

var _ ResourceProperties = &HCPCluster{}

type HCPClusterProperties struct {
	ResourceDocument `json:",inline"`

	// TODO we may need look-aside data that we want to store in the same place.  Build the nesting to allow it
	InternalState ClusterInternalState `json:"internalState"`
}

type ClusterInternalState struct {
	InternalAPI api.HCPOpenShiftCluster `json:"internalAPI"`
}

func (o *HCPCluster) ValidateResourceType() error {
	if o.ResourceType != api.ClusterResourceType.String() {
		return fmt.Errorf("invalid resource type: %s", o.ResourceType)
	}
	return nil
}

func (o *HCPCluster) GetTypedDocument() *TypedDocument {
	return &o.TypedDocument
}

func (o *HCPCluster) GetResourceDocument() *ResourceDocument {
	return &o.ResourceDocument
}

var FilterHCPClusterState ResourceDocumentStateFilter = newJSONRoundTripFilterer(
	func() any { return &ClusterInternalState{} },
)

type jsonRoundTripFilterer struct {
	newInternalStateFn func() any
}

func newJSONRoundTripFilterer(newInternalStateFn func() any) ResourceDocumentStateFilter {
	return jsonRoundTripFilterer{
		newInternalStateFn: newInternalStateFn,
	}
}

func (r jsonRoundTripFilterer) RemoveUnknownFields(toMutate *ResourceDocument) error {
	filteredInternalState, err := superExpensiveButSimpleRoundFilterForUnknownFields(toMutate.InternalState, r.newInternalStateFn())
	if err != nil {
		return err
	}
	toMutate.InternalState = filteredInternalState

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
