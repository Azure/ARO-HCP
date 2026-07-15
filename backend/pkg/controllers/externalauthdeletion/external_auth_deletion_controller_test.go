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

package externalauthdeletion

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestExternalAuthDeletionController_SyncOnce(t *testing.T) {
	fixedNow := time.Now().UTC().Truncate(time.Second)
	readyToDeleteExternalAuthOptsFunc := func(ea *api.HCPOpenShiftClusterExternalAuth) {
		ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
		ea.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-30 * time.Minute)}
		ea.ServiceProviderProperties.ClusterServiceID = nil
	}

	verifyExternalAuthStillExists := func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
		t.Helper()
		_, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).
			ExternalAuth(testClusterName).Get(ctx, testExternalAuthName)
		require.NoError(t, err, "expected external auth to still exist in Cosmos")
	}

	verifyExternalAuthDeleted := func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
		t.Helper()
		_, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).
			ExternalAuth(testClusterName).Get(ctx, testExternalAuthName)
		assert.True(t, database.IsNotFoundError(err), "expected external auth to be deleted from Cosmos")
	}

	testCases := []struct {
		name                 string
		existingExternalAuth *api.HCPOpenShiftClusterExternalAuth
		childResources       []any
		wantErr              bool
		verifyDB             func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
	}{
		{
			name:                 "no DeletionTimestamp -- no-op",
			existingExternalAuth: newTestExternalAuthWithNewDeletionApproach(t, nil),
			verifyDB:             verifyExternalAuthStillExists,
		},
		{
			name: "DeletionTimestamp set but ClusterServiceDeletionTimestamp not -- no-op",
			existingExternalAuth: newTestExternalAuthWithNewDeletionApproach(t, func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
			}),
			verifyDB: verifyExternalAuthStillExists,
		},
		{
			name: "ClusterServiceID still set -- no-op",
			existingExternalAuth: newTestExternalAuthWithNewDeletionApproach(t, func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
				ea.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-30 * time.Minute)}
			}),
			verifyDB: verifyExternalAuthStillExists,
		},
		{
			name:                 "all conditions met, no children -- deletes external auth from Cosmos",
			existingExternalAuth: newTestExternalAuthWithNewDeletionApproach(t, readyToDeleteExternalAuthOptsFunc),
			verifyDB:             verifyExternalAuthDeleted,
		},
		{
			name:                 "all conditions met, only controller children -- deletes external auth",
			existingExternalAuth: newTestExternalAuthWithNewDeletionApproach(t, readyToDeleteExternalAuthOptsFunc),
			childResources:       []any{newTestExternalAuthController(t, "test-controller")},
			verifyDB:             verifyExternalAuthDeleted,
		},
		{
			name:                 "UsesNewExternalAuthDeletionApproach false -- no-op even when all delete conditions met",
			existingExternalAuth: newTestExternalAuthWithOldDeletionApproach(t, readyToDeleteExternalAuthOptsFunc),
			childResources:       []any{newTestExternalAuthController(t, "test-controller")},
			verifyDB:             verifyExternalAuthStillExists,
		},
		{
			name:                 "external auth not found -- no-op",
			existingExternalAuth: nil,
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
			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			externalAuthsForLister := []*api.HCPOpenShiftClusterExternalAuth{}
			if tc.existingExternalAuth != nil {
				externalAuthsForLister = append(externalAuthsForLister, tc.existingExternalAuth)
			}

			syncer := &externalAuthDeletionController{
				externalAuthLister: &listertesting.SliceExternalAuthLister{ExternalAuths: externalAuthsForLister},
				resourcesDBClient:  mockResourcesDBClient,
			}

			key := controllerutils.HCPExternalAuthKey{
				SubscriptionID:      testSubscriptionID,
				ResourceGroupName:   testResourceGroupName,
				HCPClusterName:      testClusterName,
				HCPExternalAuthName: testExternalAuthName,
			}

			err = syncer.SyncOnce(ctx, key)
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
