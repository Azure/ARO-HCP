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

package roleassignments

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

const testMockFPAPrincipalID = "f14419d4-70d0-47b6-bb08-e6a8cc9564a6"

type mockFPAClientBuilderFunc func(string) (azureclient.RoleAssignmentsClient, error)

func (builder mockFPAClientBuilderFunc) RoleAssignmentsClient(subscriptionID string) (azureclient.RoleAssignmentsClient, error) {
	return builder(subscriptionID)
}

type mockFPAFailingReplaceDB struct {
	corecosmosstorage.ResourcesDBClient
}

func (database mockFPAFailingReplaceDB) ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName string) cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster] {
	return mockFPAFailingReplaceCRUD{ResourceCRUD: database.ResourcesDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName)}
}

type mockFPAFailingReplaceCRUD struct {
	cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster]
}

func (mockFPAFailingReplaceCRUD) Replace(context.Context, *coreapi.ServiceProviderCluster, *azcosmos.ItemOptions) (*coreapi.ServiceProviderCluster, error) {
	return nil, errors.New("database unavailable")
}

func TestMockFPARoleAssignmentMissingResources(t *testing.T) {
	for _, clusters := range [][]*coreapi.Cluster{nil, {newTestCluster(false)}} {
		syncer := &mockFPARoleAssignmentSyncer{
			clusterLister:                &corelistertesting.SliceClusterLister{Clusters: clusters},
			serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{},
		}
		require.NoError(t, syncer.SyncOnce(context.Background(), testHCPClusterKey))
	}
}

func TestMockFPARoleAssignmentDatabaseError(t *testing.T) {
	ctx := context.Background()
	cluster := newTestCluster(false)
	spc := newTestServiceProviderCluster(t, true, false, false, false, coreapi.AzureMultiReference{})
	database, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, spc})
	require.NoError(t, err)
	syncer := &mockFPARoleAssignmentSyncer{
		resourcesDBClient:            mockFPAFailingReplaceDB{ResourcesDBClient: database},
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: database},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: database},
		principalID:                  testMockFPAPrincipalID, clock: clocktesting.NewFakeClock(testFixedNow()),
	}
	require.ErrorContains(t, syncer.SyncOnce(ctx, testHCPClusterKey), "database unavailable")
}

func TestMockFPARoleAssignmentSyncOnce(t *testing.T) {
	for _, testCase := range []struct {
		name                                                            string
		noMRG, deleting, confirmed, create, skip, wantError, dbConflict bool
		recheck                                                         *time.Duration
		getError, createError, builderError                             error
		mutateProperties                                                func(*armauthorization.RoleAssignmentProperties)
	}{
		{name: "absent managed group", noMRG: true, skip: true},
		{name: "deleting without managed group", noMRG: true, deleting: true, skip: true},
		{name: "existing assignment"},
		{name: "subscription qualified Owner", mutateProperties: func(properties *armauthorization.RoleAssignmentProperties) {
			properties.RoleDefinitionID = ptr.To("/subscriptions/" + testSubscriptionID + mockFPAOwnerRoleDefinitionID)
		}},
		{name: "deleting cluster still reconciles", deleting: true},
		{name: "create then confirm", create: true},
		{name: "deleting cluster creates assignment", create: true, deleting: true},
		{name: "recreate disappeared assignment", confirmed: true, create: true, recheck: ptr.To(-time.Minute)},
		{name: "nil recheck reconciles", confirmed: true},
		{name: "expired recheck", confirmed: true, recheck: ptr.To(-time.Minute)},
		{name: "equal recheck", confirmed: true, recheck: ptr.To(time.Duration(0))},
		{name: "future recheck skips", confirmed: true, recheck: ptr.To(time.Hour), skip: true},
		{name: "deleting respects future recheck", confirmed: true, deleting: true, recheck: ptr.To(time.Hour), skip: true},
		{name: "incomplete ignores future recheck", recheck: ptr.To(time.Hour)},
		{name: "get failure", getError: errors.New("read failed"), wantError: true},
		{name: "create failure", create: true, createError: errors.New("create failed"), wantError: true},
		{name: "unrelated conflict", create: true, createError: &azcore.ResponseError{StatusCode: http.StatusConflict, ErrorCode: "RoleAssignmentExists"}, wantError: true},
		{name: "credential failure", builderError: errors.New("credential failed"), wantError: true},
		{name: "database conflict", dbConflict: true, skip: true},
		{name: "wrong principal", mutateProperties: func(properties *armauthorization.RoleAssignmentProperties) {
			properties.PrincipalID = ptr.To("different-principal")
		}, wantError: true},
		{name: "wrong role", mutateProperties: func(properties *armauthorization.RoleAssignmentProperties) {
			properties.RoleDefinitionID = ptr.To("different-role")
		}, wantError: true},
		{name: "wrong scope", mutateProperties: func(properties *armauthorization.RoleAssignmentProperties) {
			properties.Scope = ptr.To("different-scope")
		}, wantError: true},
		{name: "wrong principal type", mutateProperties: func(properties *armauthorization.RoleAssignmentProperties) {
			properties.PrincipalType = ptr.To(armauthorization.PrincipalTypeUser)
		}, wantError: true},
		{name: "conditional Owner is insufficient", mutateProperties: func(properties *armauthorization.RoleAssignmentProperties) {
			properties.Condition = ptr.To("restricted")
		}, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			cluster := newTestCluster(testCase.deleting)
			spc := newTestServiceProviderCluster(t, true, false, false, false, coreapi.AzureMultiReference{})
			expectedID, err := mockFPARoleAssignmentID(spc, testMockFPAPrincipalID)
			require.NoError(t, err)
			otherRecheck := metav1.NewTime(testFixedNow().Add(24 * time.Hour))
			spc.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{"another-controller": &otherRecheck}
			if testCase.confirmed {
				spc.Status.AzureResources.MockFPARoleAssignment.AzureResource = expectedID
			}
			if testCase.recheck != nil {
				recheck := metav1.NewTime(testFixedNow().Add(*testCase.recheck))
				spc.Spec.EarliestRecheckTimesByController[MockFPARoleAssignmentControllerName] = &recheck
			}
			if testCase.noMRG {
				spc.Status.AzureResources.ManagedResourceGroup = coreapi.AzureReference{PendingAzureResource: testManagedResourceGroupID(t)}
			}
			database, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, spc})
			require.NoError(t, err)
			client := azureclient.NewMockRoleAssignmentsClient(gomock.NewController(t))
			properties := &armauthorization.RoleAssignmentProperties{
				PrincipalID: ptr.To(testMockFPAPrincipalID), RoleDefinitionID: ptr.To(mockFPAOwnerRoleDefinitionID),
				Scope: ptr.To(testManagedResourceGroupScope(t)), PrincipalType: ptr.To(armauthorization.PrincipalTypeServicePrincipal),
			}
			if testCase.mutateProperties != nil {
				testCase.mutateProperties(properties)
			}
			response := armauthorization.RoleAssignmentsClientGetByIDResponse{RoleAssignment: armauthorization.RoleAssignment{Properties: properties}}
			if !testCase.skip && testCase.builderError == nil {
				if testCase.create {
					client.EXPECT().GetByID(gomock.Any(), expectedID.String(), nil).Return(armauthorization.RoleAssignmentsClientGetByIDResponse{}, roleAssignmentNotFoundError())
					client.EXPECT().Create(gomock.Any(), testManagedResourceGroupScope(t), expectedID.Name, gomock.Any(), nil).
						DoAndReturn(func(_ context.Context, _, _ string, parameters armauthorization.RoleAssignmentCreateParameters, _ *armauthorization.RoleAssignmentsClientCreateOptions) (armauthorization.RoleAssignmentsClientCreateResponse, error) {
							stored, getErr := database.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
							require.NoError(t, getErr)
							require.Equal(t, expectedID, stored.Status.AzureResources.MockFPARoleAssignment.PendingAzureResource)
							require.Nil(t, stored.Status.AzureResources.MockFPARoleAssignment.AzureResource)
							require.Equal(t, testMockFPAPrincipalID, *parameters.Properties.PrincipalID)
							require.Equal(t, mockFPAOwnerRoleDefinitionID, *parameters.Properties.RoleDefinitionID)
							require.Equal(t, armauthorization.PrincipalTypeServicePrincipal, *parameters.Properties.PrincipalType)
							return armauthorization.RoleAssignmentsClientCreateResponse{}, testCase.createError
						})
				} else {
					client.EXPECT().GetByID(gomock.Any(), expectedID.String(), nil).Return(response, testCase.getError)
				}
			}
			syncer := &mockFPARoleAssignmentSyncer{
				resourcesDBClient:            database,
				clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: database},
				serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: database},
				principalID:                  testMockFPAPrincipalID, clock: clocktesting.NewFakeClock(testFixedNow()),
				clientBuilder: mockFPAClientBuilderFunc(func(subscriptionID string) (azureclient.RoleAssignmentsClient, error) {
					require.False(t, testCase.skip, "skipped reconciles must not retrieve credentials")
					require.Equal(t, testSubscriptionID, subscriptionID)
					return client, testCase.builderError
				}),
			}
			if testCase.dbConflict {
				syncer.resourcesDBClient = preconditionFailingReplaceDB{ResourcesDBClient: database}
			}
			err = syncer.SyncOnce(ctx, testHCPClusterKey)
			if testCase.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			stored, err := database.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			require.True(t, otherRecheck.Equal(stored.Spec.EarliestRecheckTimesByController["another-controller"]))
			if testCase.skip {
				require.Equal(t, spc.Status, stored.Status)
				return
			}
			if testCase.wantError || testCase.create {
				require.Nil(t, stored.Spec.EarliestRecheckTimesByController[MockFPARoleAssignmentControllerName])
				require.False(t, MockFPARoleAssignmentReady(stored, testMockFPAPrincipalID))
				require.NotNil(t, stored.Status.AzureResources.MockFPARoleAssignment.PendingAzureResource)
			}
			if testCase.wantError {
				return
			}
			if testCase.create {
				client.EXPECT().GetByID(gomock.Any(), expectedID.String(), nil).Return(response, nil)
				require.NoError(t, syncer.SyncOnce(ctx, testHCPClusterKey))
				stored, err = database.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
			}
			require.True(t, MockFPARoleAssignmentReady(stored, testMockFPAPrincipalID))
			require.False(t, MockFPARoleAssignmentReady(stored, "another-principal"))
			recheck := stored.Spec.EarliestRecheckTimesByController[MockFPARoleAssignmentControllerName]
			require.GreaterOrEqual(t, recheck.Sub(testFixedNow()), 6*time.Hour)
			require.Less(t, recheck.Sub(testFixedNow()), 9*time.Hour)
		})
	}
}
