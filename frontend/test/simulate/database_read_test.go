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

package simulate

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func TestClusterCRUD(t *testing.T) {
	if os.Getenv("FRONTEND_SIMULATION_TESTING") != "true" {
		t.Skip("Skipping test")
	}
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	_, testInfo, err := NewFrontendFromTestingEnv(ctx, t)
	require.NoError(t, err)
	defer testInfo.Cleanup(context.Background())

	subscriptionID := "00000000-0000-0000-0000-000000000001"

	subscription := &arm.Subscription{
		State: arm.SubscriptionStateRegistered,
	}
	err = testInfo.DBClient.CreateSubscriptionDoc(ctx, subscriptionID, subscription)
	require.NoError(t, err)

	resourceGroup := "fuzzy"
	hcpClusterID := "00000000-0000-0000-0000-000000000010"
	resourceID, err := azcorearm.ParseResourceID(fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.HCP/hcpOpenShiftClusters/%s", subscriptionID, resourceGroup, hcpClusterID))
	require.NoError(t, err)
	// solving '/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/fuzzy/providers/Microsoft.HCP/hcpOpenShiftClusters/00000000-0000-0000-0000-000000000010': invalid type 'microsoft.hcp/hcpopenshiftclusters' for ResourceDocument
	// TODO makes no sense.
	resourceID.ResourceType = api.ClusterResourceType

	systemData := &arm.SystemData{
		CreatedAt: ptr.To(time.Now()),
	}
	hcpCluster := database.NewResourceDocument(resourceID)
	hcpCluster.Identity = &arm.ManagedServiceIdentity{
		PrincipalID:            "the-principal",
		TenantID:               "the-tenant",
		Type:                   "",
		UserAssignedIdentities: nil,
	}
	hcpCluster.Tags = map[string]string{
		"foo": "bar",
	}
	csClusterHREF := "/api/clusters_mgmt/v1/clusters/fixed-value"
	hcpCluster.InternalID, err = ocm.NewInternalID(csClusterHREF)
	require.NoError(t, err)

	transaction := testInfo.DBClient.NewTransaction(azcosmos.NewPartitionKeyString(subscriptionID))

	operationRequest := database.OperationRequestCreate
	correlationData := &arm.CorrelationData{}
	operationDoc := database.NewOperationDocument(operationRequest, hcpCluster.ResourceID, hcpCluster.InternalID, correlationData)
	operationID := transaction.CreateOperationDoc(operationDoc, nil)

	resourceItemID := transaction.CreateResourceDoc(hcpCluster, database.FilterHCPClusterState, nil)

	var patchOperations database.ResourceDocumentPatchOperations

	patchOperations.SetActiveOperationID(&operationID)
	patchOperations.SetProvisioningState(operationDoc.Status)

	// Record the latest system data values form ARM, if present.
	patchOperations.SetSystemData(systemData)

	// Record managed identity type an any system-assigned identifiers.
	// Omit the user-assigned identities map since that is reconstructed
	// from Cluster Service data.
	patchOperations.SetIdentity(&arm.ManagedServiceIdentity{
		PrincipalID: hcpCluster.Identity.PrincipalID,
		TenantID:    hcpCluster.Identity.TenantID,
		Type:        hcpCluster.Identity.Type,
	})

	// Here the difference between a nil map and an empty map is significant.
	// If the Tags map is nil, that means it was omitted from the request body,
	// so we leave any existing tags alone. If the Tags map is non-nil, even if
	// empty, that means it was specified in the request body and should fully
	// replace any existing tags.
	if hcpCluster.Tags != nil {
		patchOperations.SetTags(hcpCluster.Tags)
	}

	transaction.PatchResourceDoc(resourceItemID, patchOperations, nil)

	transactionResult, err := transaction.Execute(ctx, &azcosmos.TransactionalBatchOptions{
		EnableContentResponseOnWrite: true,
	})
	require.NoError(t, err)

	// Read back the resource document so the response body is accurate.
	storedValue, err := transactionResult.GetResourceDoc(resourceItemID)
	require.NoError(t, err)

	t.Log(storedValue)
}
