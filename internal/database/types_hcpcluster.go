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

type HCPCluster struct {
	TypedDocument `json:",inline"`

	HCPClusterProperties `json:"properties"`
}

type HCPClusterProperties struct {
	*ResourceDocument `json:",inline"`

	// IntermediateResourceDoc exists so that we can stop inlining the resource document so that we can directly
	// embed the InternalAPIType which has colliding serialization fields.
	IntermediateResourceDoc *ResourceDocument `json:"intermediateResourceDoc"`

	// TODO we may need look-aside data that we want to store in the same place.  Build the nesting to allow it
	InternalState ClusterInternalState `json:"internalState"`
}

type ClusterInternalState struct {
	InternalAPI api.HCPOpenShiftCluster `json:"internalAPI"`
}

func (o *HCPCluster) GetTypedDocument() *TypedDocument {
	return &o.TypedDocument
}
