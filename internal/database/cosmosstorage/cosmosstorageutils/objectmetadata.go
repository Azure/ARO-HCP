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

package cosmosstorageutils

import (
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

// ObjectMetadataForTypedDocument builds ObjectMetadata for a stored TypedDocument. The document
// carries its resource type explicitly, so that value is preferred over the one parsed from the
// resource ID.
func ObjectMetadataForTypedDocument(container string, doc *TypedDocument) metadataapi.ObjectMetadata {
	if doc == nil || doc.ResourceID == nil {
		return metadataapi.ObjectMetadata{CosmosContainer: container}
	}
	metadata := metadataapi.ObjectMetadataForResourceID(container, doc.ResourceID)
	metadata.ResourceType = doc.ResourceType
	return metadata
}
