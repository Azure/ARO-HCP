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
	"strings"
	"testing"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// TestCosmosGenericToInternalRecoversResourceID verifies that a legacy Cosmos
// document whose ResourceID field is nil (it predates that field) has its
// ResourceID recovered from the old pipe-delimited cosmos ID, the same way
// CosmosToInternal already does for the bare TypedDocument. Without this, the
// change feed errors on the document, never advances its continuation token,
// and stalls every informer built on it. Regression guard for AROSLSRE-1521.
func TestCosmosGenericToInternalRecoversResourceID(t *testing.T) {
	const resourceIDStr = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/nodePools/np"
	// Legacy documents stored the ARM path in the cosmos ID with "/" replaced
	// by "|" and carried no ResourceID field.
	legacyCosmosID := strings.ReplaceAll(resourceIDStr, "/", "|")

	preExistingDoc := &GenericDocument[api.HCPOpenShiftClusterNodePool]{
		TypedDocument: TypedDocument{
			BaseDocument: BaseDocument{ID: legacyCosmosID},
			// ResourceID intentionally nil — this is the poison-doc shape.
			ResourceID: nil,
		},
		Content: api.HCPOpenShiftClusterNodePool{
			CosmosMetadata: arm.CosmosMetadata{
				// ResourceID intentionally nil.
				ResourceID: nil,
			},
			Properties: api.HCPOpenShiftClusterNodePoolProperties{
				ProvisioningState: arm.ProvisioningStateSucceeded,
			},
			ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
				ClusterServiceID: ptr.To(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/test-cluster/node_pools/test-np"))),
			},
		},
	}

	internalNodePool, err := CosmosGenericToInternal(preExistingDoc)
	if err != nil {
		t.Fatalf("CosmosGenericToInternal returned error for recoverable legacy document: %v", err)
	}

	got := internalNodePool.GetResourceID()
	if got == nil {
		t.Fatalf("expected recovered ResourceID, got nil")
	}
	want := api.Must(azcorearm.ParseResourceID(resourceIDStr))
	if !strings.EqualFold(got.String(), want.String()) {
		t.Errorf("recovered ResourceID = %q, want %q", got.String(), want.String())
	}
}

// TestCosmosGenericToInternalUnrecoverableResourceID verifies that a document
// with neither a ResourceID nor a parseable legacy cosmos ID still returns an
// error, so genuinely malformed data is surfaced rather than silently accepted.
func TestCosmosGenericToInternalUnrecoverableResourceID(t *testing.T) {
	preExistingDoc := &GenericDocument[api.HCPOpenShiftClusterNodePool]{
		TypedDocument: TypedDocument{
			BaseDocument: BaseDocument{ID: "not-a-parseable-resource-id"},
			ResourceID:   nil,
		},
		Content: api.HCPOpenShiftClusterNodePool{
			CosmosMetadata: arm.CosmosMetadata{
				ResourceID: nil,
			},
			Properties: api.HCPOpenShiftClusterNodePoolProperties{
				ProvisioningState: arm.ProvisioningStateSucceeded,
			},
			ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
				ClusterServiceID: ptr.To(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/test-cluster/node_pools/test-np"))),
			},
		},
	}

	if _, err := CosmosGenericToInternal(preExistingDoc); err == nil {
		t.Fatalf("expected error for unrecoverable document, got nil")
	}
}
