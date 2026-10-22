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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/azure/roleassignment"
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

type mockFPAConfirmationConflictDB struct {
	corecosmosstorage.ResourcesDBClient
}

func (database mockFPAConfirmationConflictDB) ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName string) cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster] {
	return mockFPAConfirmationConflictCRUD{ResourceCRUD: database.ResourcesDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName)}
}

type mockFPAConfirmationConflictCRUD struct {
	cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster]
}

func (storage mockFPAConfirmationConflictCRUD) Replace(ctx context.Context, replacement *coreapi.ServiceProviderCluster, options *azcosmos.ItemOptions) (*coreapi.ServiceProviderCluster, error) {
	if replacement.Status.AzureResources.MockFPARoleAssignment.AzureResource != nil {
		return nil, &azcore.ResponseError{StatusCode: http.StatusPreconditionFailed}
	}
	return storage.ResourceCRUD.Replace(ctx, replacement, options)
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

func TestMockFPARoleAssignmentInvalidScopeClearsReadiness(t *testing.T) {
	for _, name := range []string{"", "/"} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			cluster := newTestCluster(false)
			spc := newTestServiceProviderCluster(t, true, false, false, false, coreapi.AzureMultiReference{})
			expectedID, err := mockFPARoleAssignmentID(cluster, testMockFPAPrincipalID)
			require.NoError(t, err)
			spc.Status.AzureResources.MockFPARoleAssignment.AzureResource = expectedID
			deadline := metav1.NewTime(testFixedNow().Add(time.Hour))
			spc.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{MockFPARoleAssignmentControllerName: &deadline}
			cluster.CustomerProperties.Platform.ManagedResourceGroup = name
			database, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, spc})
			require.NoError(t, err)
			syncer := &mockFPARoleAssignmentSyncer{
				resourcesDBClient:            database,
				clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: database},
				serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: database},
				principalID:                  testMockFPAPrincipalID, clock: clocktesting.NewFakeClock(testFixedNow()),
			}
			require.Error(t, syncer.SyncOnce(ctx, testHCPClusterKey))
			stored, err := database.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			require.Equal(t, coreapi.AzureReference{}, stored.Status.AzureResources.MockFPARoleAssignment)
			require.Nil(t, stored.Spec.EarliestRecheckTimesByController[MockFPARoleAssignmentControllerName])
			require.False(t, MockFPARoleAssignmentReady(cluster, stored, testMockFPAPrincipalID))
			require.Equal(t, spc.Status.AzureResources.ManagedResourceGroup, stored.Status.AzureResources.ManagedResourceGroup)
		})
	}
}

func TestMockFPARoleAssignmentSyncOnce(t *testing.T) {
	for _, testCase := range []struct {
		name                                                                        string
		noMRG, pendingMRG, deleting, confirmed, create, skip, wantError, dbConflict bool
		recheck                                                                     *time.Duration
		getError, createError, builderError                                         error
		rgError, rgBuilderError                                                     error
		rgState                                                                     string
		noSubscription, noTenant, noSubscriptionProperties, noRGState               bool
		mutateProperties                                                            func(*armauthorization.RoleAssignmentProperties)
		confirmationConflict                                                        bool
	}{
		{name: "existing managed group without tracking", noMRG: true, create: true},
		{name: "existing managed group with pending tracking", pendingMRG: true, create: true},
		{name: "deleting cluster without managed group tracking", noMRG: true, deleting: true, create: true},
		{name: "deleting cluster with pending managed group tracking", pendingMRG: true, deleting: true, create: true},
		{name: "creating managed group", rgState: "Creating"},
		{name: "updating managed group", rgState: "Updating"},
		{name: "absent Azure managed group clears stale readiness", confirmed: true, recheck: ptr.To(-time.Minute), rgError: &azcore.ResponseError{StatusCode: http.StatusNotFound, ErrorCode: "ResourceGroupNotFound"}},
		{name: "deleting Azure managed group clears stale readiness", confirmed: true, recheck: ptr.To(-time.Minute), rgState: "Deleting"},
		{name: "incomplete absent group ignores future recheck", recheck: ptr.To(time.Hour), rgError: &azcore.ResponseError{StatusCode: http.StatusNotFound, ErrorCode: "ResourceGroupNotFound"}},
		{name: "resource group GET failure clears stale readiness", confirmed: true, recheck: ptr.To(-time.Minute), rgError: errors.New("get failed"), wantError: true},
		{name: "unrelated resource group not found error", rgError: &azcore.ResponseError{StatusCode: http.StatusNotFound, ErrorCode: "SubscriptionNotFound"}, wantError: true},
		{name: "resource group credentials fail", confirmed: true, recheck: ptr.To(-time.Minute), rgBuilderError: errors.New("credentials failed"), wantError: true},
		{name: "subscription missing", confirmed: true, recheck: ptr.To(-time.Minute), noSubscription: true, wantError: true},
		{name: "tenant missing", confirmed: true, recheck: ptr.To(-time.Minute), noTenant: true, wantError: true},
		{name: "subscription properties missing", confirmed: true, recheck: ptr.To(-time.Minute), noSubscriptionProperties: true, wantError: true},
		{name: "resource group state missing", confirmed: true, recheck: ptr.To(-time.Minute), noRGState: true, wantError: true},
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
		{name: "confirmation conflict leaves incomplete readiness", confirmationConflict: true, confirmed: true, recheck: ptr.To(-time.Minute)},
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
			expectedID, err := mockFPARoleAssignmentID(cluster, testMockFPAPrincipalID)
			require.NoError(t, err)
			require.Equal(t, roleassignment.ManagedResourceGroupScopedRoleAssignmentResourceID(testManagedResourceGroupScope(t), testMockFPAPrincipalID, mockFPAOwnerRoleDefinitionID), expectedID.String())
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
				spc.Status.AzureResources.ManagedResourceGroup = coreapi.AzureReference{}
			}
			if testCase.pendingMRG {
				spc.Status.AzureResources.ManagedResourceGroup = coreapi.AzureReference{PendingAzureResource: testManagedResourceGroupID(t)}
			}
			database, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, spc})
			require.NoError(t, err)
			client := azureclient.NewMockRoleAssignmentsClient(gomock.NewController(t))
			fpaBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(gomock.NewController(t))
			rgClient := azureclient.NewMockResourceGroupsClient(gomock.NewController(t))
			subscription := newTestSubscription(ptr.To(testTenantID))
			if testCase.noTenant {
				subscription.Properties.TenantId = nil
			}
			if testCase.noSubscriptionProperties {
				subscription.Properties = nil
			}
			subscriptions := []*coreapi.Subscription{subscription}
			if testCase.noSubscription {
				subscriptions = nil
			}
			rgBlocked := testCase.rgError != nil || testCase.rgBuilderError != nil || testCase.rgState == "Deleting" || testCase.noRGState || testCase.noSubscription || testCase.noTenant || testCase.noSubscriptionProperties
			if !testCase.skip && !testCase.noSubscription && !testCase.noTenant && !testCase.noSubscriptionProperties {
				calls := 1
				if testCase.create && !testCase.wantError && !rgBlocked {
					calls++
				}
				fpaBuilder.EXPECT().ResourceGroupsClient(testTenantID, testSubscriptionID).Return(rgClient, testCase.rgBuilderError).Times(calls)
				if testCase.rgBuilderError == nil {
					state := testCase.rgState
					if state == "" {
						state = "Succeeded"
					}
					group := armresources.ResourceGroup{Properties: &armresources.ResourceGroupProperties{ProvisioningState: ptr.To(state)}}
					if testCase.noRGState {
						group.Properties = nil
					}
					rgClient.EXPECT().Get(gomock.Any(), cluster.CustomerProperties.Platform.ManagedResourceGroup, nil).Return(armresources.ResourceGroupsClientGetResponse{ResourceGroup: group}, testCase.rgError).Times(calls)
				}
			}
			properties := &armauthorization.RoleAssignmentProperties{
				PrincipalID: ptr.To(testMockFPAPrincipalID), RoleDefinitionID: ptr.To(mockFPAOwnerRoleDefinitionID),
				Scope: ptr.To(testManagedResourceGroupScope(t)), PrincipalType: ptr.To(armauthorization.PrincipalTypeServicePrincipal),
			}
			if testCase.mutateProperties != nil {
				testCase.mutateProperties(properties)
			}
			response := armauthorization.RoleAssignmentsClientGetByIDResponse{RoleAssignment: armauthorization.RoleAssignment{Properties: properties}}
			if !testCase.skip && !rgBlocked && testCase.builderError == nil {
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
				subscriptionLister:           &corelistertesting.SliceSubscriptionLister{Subscriptions: subscriptions},
				fpaClientBuilder:             fpaBuilder,
				principalID:                  testMockFPAPrincipalID, clock: clocktesting.NewFakeClock(testFixedNow()),
				clientBuilder: mockFPAClientBuilderFunc(func(subscriptionID string) (azureclient.RoleAssignmentsClient, error) {
					require.False(t, testCase.skip, "skipped reconciles must not retrieve credentials")
					require.False(t, rgBlocked, "unavailable resource groups must not retrieve assignment credentials")
					require.Equal(t, testSubscriptionID, subscriptionID)
					return client, testCase.builderError
				}),
			}
			if testCase.dbConflict {
				syncer.resourcesDBClient = preconditionFailingReplaceDB{ResourcesDBClient: database}
			}
			if testCase.confirmationConflict {
				syncer.resourcesDBClient = mockFPAConfirmationConflictDB{ResourcesDBClient: database}
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
			require.Equal(t, spc.Status.AzureResources.ManagedResourceGroup, stored.Status.AzureResources.ManagedResourceGroup)
			if testCase.skip {
				require.Equal(t, spc.Status, stored.Status)
				if testCase.dbConflict {
					require.False(t, MockFPARoleAssignmentReady(cluster, stored, testMockFPAPrincipalID))
				}
				return
			}
			if testCase.wantError || testCase.create || rgBlocked || testCase.confirmationConflict {
				require.Nil(t, stored.Spec.EarliestRecheckTimesByController[MockFPARoleAssignmentControllerName])
				require.False(t, MockFPARoleAssignmentReady(cluster, stored, testMockFPAPrincipalID))
				require.Nil(t, stored.Status.AzureResources.MockFPARoleAssignment.AzureResource)
				require.NotNil(t, stored.Status.AzureResources.MockFPARoleAssignment.PendingAzureResource)
			}
			if testCase.wantError || rgBlocked || testCase.confirmationConflict {
				return
			}
			if testCase.create {
				client.EXPECT().GetByID(gomock.Any(), expectedID.String(), nil).Return(response, nil)
				require.NoError(t, syncer.SyncOnce(ctx, testHCPClusterKey))
				stored, err = database.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
			}
			require.True(t, MockFPARoleAssignmentReady(cluster, stored, testMockFPAPrincipalID))
			require.False(t, MockFPARoleAssignmentReady(cluster, stored, "another-principal"))
			otherCluster := cluster.DeepCopy()
			otherCluster.CustomerProperties.Platform.ManagedResourceGroup = "another-group"
			require.False(t, MockFPARoleAssignmentReady(otherCluster, stored, testMockFPAPrincipalID))
			recheck := stored.Spec.EarliestRecheckTimesByController[MockFPARoleAssignmentControllerName]
			require.GreaterOrEqual(t, recheck.Sub(testFixedNow()), 6*time.Hour)
			require.Less(t, recheck.Sub(testFixedNow()), 9*time.Hour)
		})
	}
}
