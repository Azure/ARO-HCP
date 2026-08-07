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

package deletion

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func newTestExternalAuthController(t *testing.T, name string) *coreapi.Controller {
	t.Helper()
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/externalAuths/" + testExternalAuthName +
			"/hcpOpenShiftControllers/" + name))
	return &coreapi.Controller{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(resourceID.SubscriptionID)},
		ExternalID: metadataapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testSubscriptionID +
				"/resourceGroups/" + testResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
				"/externalAuths/" + testExternalAuthName)),
		Status: coreapi.ControllerStatus{
			Conditions: []metav1.Condition{},
		},
	}
}

func TestExternalAuthChildResourcesCleanupController_SyncOnce(t *testing.T) {
	fixedNow := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	readyToDeleteExternalAuthOptsFunc := func(ea *coreapi.HCPOpenShiftClusterExternalAuth) {
		ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
		ea.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-30 * time.Minute)}
		ea.ServiceProviderProperties.ClusterServiceID = nil
	}

	// storeNonControllerChild inserts a synthetic non-controller child document
	// under the external auth resource path. At the moment of writing this (2026-06-02) auth has no typed
	// non-controller children, so we store a raw TypedDocument directly.
	storeNonControllerChild := func(t *testing.T, db *corecosmosstoragetesting.MockResourcesDBClient, name string) {
		t.Helper()
		resourceID := metadataapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testSubscriptionID +
				"/resourceGroups/" + testResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
				"/externalAuths/" + testExternalAuthName +
				"/nonControllerChildType/" + name))
		cosmosID, err := coreapi.ResourceIDToCosmosID(resourceID)
		require.NoError(t, err)
		doc := cosmosstorageutils.TypedDocument{
			BaseDocument: cosmosstorageutils.BaseDocument{ID: cosmosID},
			ResourceID:   resourceID,
			ResourceType: "Microsoft.RedHatOpenShift/hcpOpenShiftClusters/externalAuths/nonControllerChildType",
		}
		data, err := json.Marshal(doc)
		require.NoError(t, err)
		db.StoreDocument(cosmosID, data)
	}

	testKey := controllerutils.HCPExternalAuthKey{
		SubscriptionID:      testSubscriptionID,
		ResourceGroupName:   testResourceGroupName,
		HCPClusterName:      testClusterName,
		HCPExternalAuthName: testExternalAuthName,
	}

	testCases := []struct {
		name                    string
		existingExternalAuth    *coreapi.HCPOpenShiftClusterExternalAuth
		childResources          []any
		extraSetupDBTestingMock func(t *testing.T, db *corecosmosstoragetesting.MockResourcesDBClient)
		wantErr                 bool
		verifyDB                func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient)
	}{
		{
			name:                 "when no DeletionTimestamp, no ClusterServiceDeletionTimestamp are set and ClusterServiceID is set performs a no-op",
			existingExternalAuth: newTestExternalAuthWithNewDeletionApproach(t, nil),
			childResources:       []any{newTestExternalAuthController(t, "untouched-controller")},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				controllerCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).
					ExternalAuth(testClusterName).Controllers(testExternalAuthName)
				_, err := controllerCRUD.Get(ctx, "untouched-controller")
				require.NoError(t, err, "expected child resource to still exist")
			},
		},
		{
			name: "when no ClusterServiceDeletionTimestamp is set performs a no-op",
			existingExternalAuth: newTestExternalAuthWithNewDeletionApproach(t, func(ea *coreapi.HCPOpenShiftClusterExternalAuth) {
				ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
				ea.ServiceProviderProperties.ClusterServiceDeletionTimestamp = nil
				ea.ServiceProviderProperties.ClusterServiceID = nil
			}),
			childResources: []any{newTestExternalAuthController(t, "untouched-controller")},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				controllerCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).
					ExternalAuth(testClusterName).Controllers(testExternalAuthName)
				_, err := controllerCRUD.Get(ctx, "untouched-controller")
				require.NoError(t, err, "expected child resource to still exist")
			},
		},
		{
			name: "when ClusterServiceID is set performs a no-op",
			existingExternalAuth: newTestExternalAuthWithNewDeletionApproach(t, func(ea *coreapi.HCPOpenShiftClusterExternalAuth) {
				ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
				ea.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-30 * time.Minute)}
			}),
			childResources: []any{newTestExternalAuthController(t, "untouched-controller")},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				controllerCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).
					ExternalAuth(testClusterName).Controllers(testExternalAuthName)
				_, err := controllerCRUD.Get(ctx, "untouched-controller")
				require.NoError(t, err, "expected child resource to still exist")
			},
		},
		{
			name:                 "when all conditions met and there are no children performs a no-op",
			existingExternalAuth: newTestExternalAuthWithNewDeletionApproach(t, readyToDeleteExternalAuthOptsFunc),
		},
		{
			name:                 "when there is a children resource it deletes it",
			existingExternalAuth: newTestExternalAuthWithNewDeletionApproach(t, readyToDeleteExternalAuthOptsFunc),
			extraSetupDBTestingMock: func(t *testing.T, db *corecosmosstoragetesting.MockResourcesDBClient) {
				storeNonControllerChild(t, db, "test-mcc")
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				externalAuthResourceID := testKey.GetResourceID()
				untypedCRUD, err := db.UntypedCRUD(*externalAuthResourceID)
				require.NoError(t, err)
				childIterator, err := untypedCRUD.ListRecursive(ctx, nil)
				require.NoError(t, err)

				var remainingCount int
				for range childIterator.Items(ctx) {
					remainingCount++
				}
				require.NoError(t, childIterator.GetError())
				assert.Equal(t, 0, remainingCount, "expected no children to remain")
			},
		},
		{
			name:                 "deletion of external auth controllers is skipped",
			existingExternalAuth: newTestExternalAuthWithNewDeletionApproach(t, readyToDeleteExternalAuthOptsFunc),
			childResources:       []any{newTestExternalAuthController(t, "test-controller")},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				externalAuthResourceID := testKey.GetResourceID()
				untypedCRUD, err := db.UntypedCRUD(*externalAuthResourceID)
				require.NoError(t, err)
				childIterator, err := untypedCRUD.ListRecursive(ctx, nil)
				require.NoError(t, err)

				var controllerCount int
				for _, child := range childIterator.Items(ctx) {
					if strings.EqualFold(child.ResourceType, coreapi.ExternalAuthControllerResourceType.String()) {
						controllerCount++
					}
				}
				require.NoError(t, childIterator.GetError())
				assert.Equal(t, 1, controllerCount, "expected controller child to remain")
			},
		},
		{
			name:                 "when there are external auth controller and non controller children it deletes the non controller children",
			existingExternalAuth: newTestExternalAuthWithNewDeletionApproach(t, readyToDeleteExternalAuthOptsFunc),
			childResources:       []any{newTestExternalAuthController(t, "test-controller")},
			extraSetupDBTestingMock: func(t *testing.T, db *corecosmosstoragetesting.MockResourcesDBClient) {
				storeNonControllerChild(t, db, "test-mcc")
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				externalAuthResourceID := testKey.GetResourceID()
				untypedCRUD, err := db.UntypedCRUD(*externalAuthResourceID)
				require.NoError(t, err)
				childIterator, err := untypedCRUD.ListRecursive(ctx, nil)
				require.NoError(t, err)

				var remainingCount int
				var controllerCount int
				for _, child := range childIterator.Items(ctx) {
					remainingCount++
					if strings.EqualFold(child.ResourceType, coreapi.ExternalAuthControllerResourceType.String()) {
						controllerCount++
					}
				}
				require.NoError(t, childIterator.GetError())
				assert.Equal(t, 1, remainingCount, "expected only controller child to remain")
				assert.Equal(t, 1, controllerCount, "expected the remaining child to be a controller")
			},
		},

		{
			name:                 "when the external auth is not found performs a no-op",
			existingExternalAuth: nil,
		},
		{
			name:                 "UsesNewExternalAuthDeletionApproach false -- no-op even when all cleanup conditions met and children exist",
			existingExternalAuth: newTestExternalAuthWithOldDeletionApproach(t, readyToDeleteExternalAuthOptsFunc),
			childResources:       []any{newTestExternalAuthController(t, "untouched-controller")},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				controllerCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).
					ExternalAuth(testClusterName).Controllers(testExternalAuthName)
				_, err := controllerCRUD.Get(ctx, "untouched-controller")
				require.NoError(t, err, "expected child resource to still exist")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			resources := []any{}
			if tc.existingExternalAuth != nil {
				resources = append(resources, tc.existingExternalAuth)
			}
			resources = append(resources, tc.childResources...)
			mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			if tc.extraSetupDBTestingMock != nil {
				tc.extraSetupDBTestingMock(t, mockResourcesDBClient)
			}

			externalAuthsForLister := []*coreapi.HCPOpenShiftClusterExternalAuth{}
			if tc.existingExternalAuth != nil {
				externalAuthsForLister = append(externalAuthsForLister, tc.existingExternalAuth)
			}

			syncer := &externalAuthChildResourcesCleanupController{
				externalAuthLister: &corelistertesting.SliceExternalAuthLister{ExternalAuths: externalAuthsForLister},
				resourcesDBClient:  mockResourcesDBClient,
			}

			err = syncer.SyncOnce(ctx, testKey)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tc.verifyDB != nil {
				tc.verifyDB(t, ctx, mockResourcesDBClient)
			}
		})
	}
}
