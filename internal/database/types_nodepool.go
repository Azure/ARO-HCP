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
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// NodePool represents a customer desired NodePool.
// To transition from our current state using cluster-service as half the source of truth to a state where
// cosmos contains all the desired state and all the observed state, we are basing the schema on ResourceDocument.
type NodePool struct {
	TypedDocument `json:",inline"`
	Properties    NodePoolProperties `json:"properties"`
}

type NodePoolProperties struct {
	ResourceDocument `json:",inline"`
}

var _ DocumentProperties = &NodePool{}

func NewNodePool(resourceID *azcorearm.ResourceID) *NodePool {
	return &NodePool{
		Properties: NodePoolProperties{
			ResourceDocument: ResourceDocument{
				ResourceID: resourceID,
			},
		},
	}
}

func (doc *NodePool) GetTypedDocument() *TypedDocument {
	return &doc.TypedDocument
}

func (doc *NodePool) GetResourceDocument() *ResourceDocument {
	return &doc.Properties.ResourceDocument
}

func (doc *NodePool) GetReportingID() string {
	return doc.GetResourceDocument().ResourceID.String()
}

func (doc *NodePool) GetResourceType() azcorearm.ResourceType {
	return api.NodePoolResourceType

}

func (doc *NodePool) SetTypedDocument(in TypedDocument) {
	doc.TypedDocument = in
}

func (doc *NodePool) SetResourceID(resourceID *azcorearm.ResourceID) {
	doc.Properties.ResourceID = resourceID
}
