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
	"testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
)

func TestObjectMetadataForTypedDocument(t *testing.T) {
	clusterID := metadataapi.Must(azcorearm.ParseResourceID(coreapitesting.TestClusterResourceID))

	t.Run("derives identity from the document and prefers its explicit resource type", func(t *testing.T) {
		doc := &TypedDocument{
			ResourceID:   clusterID,
			ResourceType: coreapi.ClusterResourceType.String(),
		}

		md := ObjectMetadataForTypedDocument("resources", doc)

		if md.CosmosContainer != "resources" {
			t.Errorf("CosmosContainer = %q, want %q", md.CosmosContainer, "resources")
		}
		if md.ResourceGroup != coreapitesting.TestResourceGroupName {
			t.Errorf("ResourceGroup = %q, want %q", md.ResourceGroup, coreapitesting.TestResourceGroupName)
		}
		if md.ResourceType != coreapi.ClusterResourceType.String() {
			t.Errorf("ResourceType = %q, want %q (from the document)", md.ResourceType, coreapi.ClusterResourceType.String())
		}
		if md.ResourceID != clusterID.String() {
			t.Errorf("ResourceID = %q, want %q", md.ResourceID, clusterID.String())
		}
	})

	t.Run("nil document yields only the container", func(t *testing.T) {
		md := ObjectMetadataForTypedDocument("resources", nil)

		if md.CosmosContainer != "resources" {
			t.Errorf("CosmosContainer = %q, want %q", md.CosmosContainer, "resources")
		}
		if md.ResourceID != "" {
			t.Errorf("ResourceID = %q, want empty for a nil document", md.ResourceID)
		}
	})
}
