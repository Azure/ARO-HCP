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
	"github.com/Azure/ARO-HCP/internal/ocm"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// ExternalAuth represents a customer desired ExternalAuth.
// To transition from our current state using cluster-service as half the source of truth to a state where
// cosmos contains all the desired state and all the observed state, we are basing the schema on ResourceDocument.
type ExternalAuth struct {
	TypedDocument `json:",inline"`
	Properties    ExternalAuthProperties `json:"properties"`
}

var _ DocumentProperties = &ExternalAuth{}

func (doc *ExternalAuth) GetSubscriptionID() string {
	return doc.TypedDocument.PartitionKey
}

func (doc *ExternalAuth) GetResourceID() *azcorearm.ResourceID {
	return doc.Properties.ResourceDocument.ResourceID
}

type ExternalAuthProperties struct {
	ResourceDocument `json:",inline"`
}

func NewExternalAuth(resourceID *azcorearm.ResourceID) *ExternalAuth {
	return &ExternalAuth{
		Properties: ExternalAuthProperties{
			ResourceDocument: ResourceDocument{
				ResourceID: resourceID,
			},
		},
	}
}

func (doc *ExternalAuth) GetTypedDocument() *TypedDocument {
	return &doc.TypedDocument
}

func (doc *ExternalAuth) GetResourceDocument() *ResourceDocument {
	return &doc.Properties.ResourceDocument
}

func (doc *ExternalAuth) GetResourceType() azcorearm.ResourceType {
	return api.ExternalAuthResourceType

}

func (doc *ExternalAuth) GetReportingID() string {
	return doc.GetResourceDocument().ResourceID.String()
}

func (doc *ExternalAuth) SetTypedDocument(in TypedDocument) {
	doc.TypedDocument = in
}

func (doc *ExternalAuth) SetInternalID(in ocm.InternalID) {
	doc.Properties.ResourceDocument.InternalID = in
}

func (doc *ExternalAuth) SetResourceID(resourceID *azcorearm.ResourceID) {
	doc.Properties.ResourceID = resourceID
}
