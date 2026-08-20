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

package informerutils

import (
	"testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
)

func TestChangeFeedItemObjectMetadata(t *testing.T) {
	// An operation's own ResourceID is subscription/location-scoped and has no resource group.
	operationOwnID := mustParseResourceIDForTest(t, "/subscriptions/"+coreapitesting.TestSubscriptionID+
		"/providers/"+coreapi.ProviderNamespace+"/locations/eastus/hcpOperationStatuses/op-123")
	if operationOwnID.ResourceGroupName != "" {
		t.Fatalf("precondition: operation own ID unexpectedly has resource group %q", operationOwnID.ResourceGroupName)
	}
	clusterID := mustParseResourceIDForTest(t, coreapitesting.TestClusterResourceID)

	t.Run("operation with ExternalID derives resourceGroup and cluster from it", func(t *testing.T) {
		op := &coreapi.Operation{ExternalID: clusterID}
		op.ResourceID = operationOwnID

		md := changeFeedItemObjectMetadata("operations", op, op.GetResourceID())

		// Operations share the resources container (via ObjectMetadataForOperation).
		if md.CosmosContainer != "resources" {
			t.Errorf("CosmosContainer = %q, want %q", md.CosmosContainer, "resources")
		}
		// The snapshot still identifies the operation document itself, not the external target.
		if md.ResourceID != operationOwnID.String() {
			t.Errorf("ResourceID = %q, want operation own ID %q", md.ResourceID, operationOwnID.String())
		}
		// ...but resourceGroup and clusterResourceID come from the ExternalID (the targeted cluster).
		if md.ResourceGroup != coreapitesting.TestResourceGroupName {
			t.Errorf("ResourceGroup = %q, want %q (from ExternalID)", md.ResourceGroup, coreapitesting.TestResourceGroupName)
		}
		if md.ClusterResourceID != clusterID.String() {
			t.Errorf("ClusterResourceID = %q, want %q (from ExternalID)", md.ClusterResourceID, clusterID.String())
		}
	})

	t.Run("operation with nil ExternalID leaves resourceGroup and cluster empty", func(t *testing.T) {
		op := &coreapi.Operation{}
		op.ResourceID = operationOwnID

		md := changeFeedItemObjectMetadata("operations", op, op.GetResourceID())

		if md.ResourceGroup != "" {
			t.Errorf("ResourceGroup = %q, want empty when ExternalID is nil", md.ResourceGroup)
		}
		if md.ClusterResourceID != "" {
			t.Errorf("ClusterResourceID = %q, want empty when ExternalID is nil", md.ClusterResourceID)
		}
	})

	t.Run("non-operation document derives its cluster from its own ResourceID", func(t *testing.T) {
		// An HCPOpenShiftCluster is not an *Operation, so the metadata (including clusterResourceID)
		// comes entirely from its own ResourceID.
		cluster := &coreapi.HCPOpenShiftCluster{}

		md := changeFeedItemObjectMetadata("resources", cluster, clusterID)

		if md.CosmosContainer != "resources" {
			t.Errorf("CosmosContainer = %q, want %q", md.CosmosContainer, "resources")
		}
		if md.ResourceGroup != coreapitesting.TestResourceGroupName {
			t.Errorf("ResourceGroup = %q, want %q (from own ResourceID)", md.ResourceGroup, coreapitesting.TestResourceGroupName)
		}
		if md.ResourceID != clusterID.String() {
			t.Errorf("ResourceID = %q, want %q", md.ResourceID, clusterID.String())
		}
		if md.ClusterResourceID != clusterID.String() {
			t.Errorf("ClusterResourceID = %q, want %q (from own ResourceID)", md.ClusterResourceID, clusterID.String())
		}
	})
}

func mustParseResourceIDForTest(t *testing.T, resourceID string) *azcorearm.ResourceID {
	t.Helper()

	id, err := azcorearm.ParseResourceID(resourceID)
	if err != nil {
		t.Fatalf("parse resource ID %q: %v", resourceID, err)
	}
	return id
}
