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

func TestObjectMetadataForOperation(t *testing.T) {
	// An operation's own ResourceID is subscription/location-scoped and has no resource group.
	operationOwnID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + coreapitesting.TestSubscriptionID +
		"/providers/" + coreapi.ProviderNamespace + "/locations/eastus/hcpOperationStatuses/op-123"))
	if operationOwnID.ResourceGroupName != "" {
		t.Fatalf("precondition: operation own ID unexpectedly has resource group %q", operationOwnID.ResourceGroupName)
	}
	// The ExternalID targets the cluster, which lives in a resource group.
	externalID := metadataapi.Must(azcorearm.ParseResourceID(coreapitesting.TestClusterResourceID))

	t.Run("fills resourceGroup from ExternalID", func(t *testing.T) {
		op := &coreapi.Operation{ExternalID: externalID}
		op.ResourceID = operationOwnID

		md := ObjectMetadataForOperation(op)

		if md.CosmosContainer != "resources" {
			t.Errorf("CosmosContainer = %q, want %q", md.CosmosContainer, "resources")
		}
		if md.ResourceGroup != coreapitesting.TestResourceGroupName {
			t.Errorf("ResourceGroup = %q, want %q (from ExternalID)", md.ResourceGroup, coreapitesting.TestResourceGroupName)
		}
		if md.ClusterResourceID != externalID.String() {
			t.Errorf("ClusterResourceID = %q, want %q (from ExternalID)", md.ClusterResourceID, externalID.String())
		}
		// The snapshot still identifies the operation document itself, not the external target.
		if md.ResourceID != operationOwnID.String() {
			t.Errorf("ResourceID = %q, want operation own ID %q", md.ResourceID, operationOwnID.String())
		}
	})

	t.Run("nil ExternalID leaves resourceGroup and cluster empty", func(t *testing.T) {
		op := &coreapi.Operation{}
		op.ResourceID = operationOwnID

		md := ObjectMetadataForOperation(op)

		if md.ResourceGroup != "" {
			t.Errorf("ResourceGroup = %q, want empty when ExternalID is nil", md.ResourceGroup)
		}
		if md.ClusterResourceID != "" {
			t.Errorf("ClusterResourceID = %q, want empty when ExternalID is nil", md.ClusterResourceID)
		}
	})
}

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
