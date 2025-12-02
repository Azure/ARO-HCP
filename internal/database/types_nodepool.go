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
	"fmt"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
)

type NodePool struct {
	TypedDocument `json:",inline"`

	NodePoolProperties `json:"properties"`
}

var _ ResourceProperties = &NodePool{}

type NodePoolProperties struct {
	ResourceDocument `json:",inline"`

	// TODO we may need look-aside data that we want to store in the same place.  Build the nesting to allow it
	InternalState NodePoolInternalState `json:"internalState"`
}

type NodePoolInternalState struct {
	InternalAPI api.HCPOpenShiftClusterNodePool `json:"internalAPI"`
}

func (o *NodePool) ValidateResourceType() error {
	if o.ResourceType != api.NodePoolResourceType.String() {
		return fmt.Errorf("invalid resource type: %s", o.ResourceType)
	}
	return nil
}

func (o *NodePool) GetTypedDocument() *TypedDocument {
	return &o.TypedDocument
}

func (o *NodePool) SetResourceID(newResourceID *azcorearm.ResourceID) {
	o.ResourceDocument.SetResourceID(newResourceID)
}
