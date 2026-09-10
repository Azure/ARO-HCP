// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/msi-dataplane/pkg/dataplane"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

const (
	testDataPlaneOperatorName       = "ingress"
	testDataPlaneOperatorResourceID = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/ingress"
)

type fakeUserAssignedIdentitiesClientByName struct {
	getByName map[string]armmsi.UserAssignedIdentitiesClientGetResponse
	errByName map[string]error
}

var _ azureclient.UserAssignedIdentitiesClient = (*fakeUserAssignedIdentitiesClientByName)(nil)

func (f *fakeUserAssignedIdentitiesClientByName) Get(_ context.Context, _, name string, _ *armmsi.UserAssignedIdentitiesClientGetOptions) (armmsi.UserAssignedIdentitiesClientGetResponse, error) {
	key := strings.ToLower(name)
	if err, ok := f.errByName[key]; ok {
		return armmsi.UserAssignedIdentitiesClientGetResponse{}, err
	}
	if resp, ok := f.getByName[key]; ok {
		return resp, nil
	}
	return armmsi.UserAssignedIdentitiesClientGetResponse{}, fmt.Errorf("unexpected Get for identity %s", name)
}

func (f *fakeUserAssignedIdentitiesClientByName) CreateOrUpdate(_ context.Context, _ string, _ string, _ armmsi.Identity, _ *armmsi.UserAssignedIdentitiesClientCreateOrUpdateOptions) (armmsi.UserAssignedIdentitiesClientCreateOrUpdateResponse, error) {
	panic("CreateOrUpdate not implemented in fakeUserAssignedIdentitiesClientByName")
}

func (f *fakeUserAssignedIdentitiesClientByName) Delete(_ context.Context, _ string, _ string, _ *armmsi.UserAssignedIdentitiesClientDeleteOptions) (armmsi.UserAssignedIdentitiesClientDeleteResponse, error) {
	panic("Delete not implemented in fakeUserAssignedIdentitiesClientByName")
}

func uaIdentityResp(clientID, principalID, tenantID string) armmsi.UserAssignedIdentitiesClientGetResponse {
	return armmsi.UserAssignedIdentitiesClientGetResponse{
		Identity: armmsi.Identity{
			Properties: &armmsi.UserAssignedIdentityProperties{
				ClientID:    ptr.To(clientID),
				PrincipalID: ptr.To(principalID),
				TenantID:    ptr.To(tenantID),
			},
		},
	}
}

func newTestClusterForManagedIdentities(opts ...func(*coreapi.HCPOpenShiftCluster)) *coreapi.HCPOpenShiftCluster {
	cluster := newTestClusterForFetch()
	cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators = map[string]*azcorearm.ResourceID{
		testDataPlaneOperatorName: metadataapi.Must(azcorearm.ParseResourceID(testDataPlaneOperatorResourceID)),
	}
	for _, opt := range opts {
		opt(cluster)
	}
	return cluster
}

func TestCollectIdentitiesToResolve(t *testing.T) {
	t.Parallel()

	syncer := &fetchManagedIdentitiesInfoSyncer{}
	lowerOperator := strings.ToLower(testOperatorIdentityResourceID)
	lowerSMI := strings.ToLower(testServiceManagedIdentityID)
	lowerDataPlane := strings.ToLower(testDataPlaneOperatorResourceID)

	t.Run("collects control plane, data plane, and service managed identity", func(t *testing.T) {
		t.Parallel()
		got, errs := syncer.collectIdentitiesToResolve(newTestClusterForManagedIdentities())
		require.Empty(t, errs)
		require.Len(t, got, 3)

		require.Contains(t, got, lowerOperator)
		assert.True(t, got[lowerOperator].isControlPlaneOperatorIdentity)
		assert.False(t, got[lowerOperator].isDataPlaneOperatorIdentity)
		assert.False(t, got[lowerOperator].isServiceManagedIdentity)

		require.Contains(t, got, lowerSMI)
		assert.False(t, got[lowerSMI].isControlPlaneOperatorIdentity)
		assert.False(t, got[lowerSMI].isDataPlaneOperatorIdentity)
		assert.True(t, got[lowerSMI].isServiceManagedIdentity)

		require.Contains(t, got, lowerDataPlane)
		assert.False(t, got[lowerDataPlane].isControlPlaneOperatorIdentity)
		assert.True(t, got[lowerDataPlane].isDataPlaneOperatorIdentity)
		assert.False(t, got[lowerDataPlane].isServiceManagedIdentity)
	})

	t.Run("deduplicates shared resource IDs and marks both control-plane and data-plane when the same UAMI is used for both", func(t *testing.T) {
		t.Parallel()
		cluster := newTestClusterForManagedIdentities()
		cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators["shared"] =
			metadataapi.Must(azcorearm.ParseResourceID(strings.ToUpper(testOperatorIdentityResourceID)))
		got, errs := syncer.collectIdentitiesToResolve(cluster)
		require.Empty(t, errs)
		require.Len(t, got, 3)
		assert.True(t, got[lowerOperator].isControlPlaneOperatorIdentity)
		assert.True(t, got[lowerOperator].isDataPlaneOperatorIdentity)
		assert.False(t, got[lowerOperator].isServiceManagedIdentity)
	})

	t.Run("nil service managed identity is accumulated and other identities are still collected", func(t *testing.T) {
		t.Parallel()
		cluster := newTestClusterForManagedIdentities(func(c *coreapi.HCPOpenShiftCluster) {
			c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity = nil
		})
		got, errs := syncer.collectIdentitiesToResolve(cluster)
		require.Len(t, errs, 1)
		assert.Contains(t, errs[0].Error(), "service managed identity")
		require.Len(t, got, 2)
		assert.Contains(t, got, lowerOperator)
		assert.Contains(t, got, lowerDataPlane)
	})

	t.Run("nil control plane operator identity is accumulated and other identities are still collected", func(t *testing.T) {
		t.Parallel()
		cluster := newTestClusterForManagedIdentities(func(c *coreapi.HCPOpenShiftCluster) {
			c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators[testOperatorName] = nil
		})
		got, errs := syncer.collectIdentitiesToResolve(cluster)
		require.Len(t, errs, 1)
		assert.Contains(t, errs[0].Error(), testOperatorName)
		require.Len(t, got, 2)
		assert.Contains(t, got, lowerSMI)
		assert.Contains(t, got, lowerDataPlane)
	})
}

func TestFetchManagedIdentitiesInfoNeedsWork(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	lowerOperator := strings.ToLower(testOperatorIdentityResourceID)
	matchingDesired := map[string]*identityToResolve{
		lowerOperator: {
			resourceID:                     metadataapi.Must(azcorearm.ParseResourceID(testOperatorIdentityResourceID)),
			isControlPlaneOperatorIdentity: true,
		},
	}
	matchingStored := map[string]*coreapi.ManagedIdentityMetadata{
		lowerOperator: {
			ResourceID: metadataapi.Must(azcorearm.ParseResourceID(testOperatorIdentityResourceID)),
		},
	}

	testCases := []struct {
		name                string
		desired             map[string]*identityToResolve
		stored              map[string]*coreapi.ManagedIdentityMetadata
		earliestRecheckTime *metav1.Time
		want                bool
	}{
		{
			name:                "matching identities with future recheck skips work",
			desired:             matchingDesired,
			stored:              matchingStored,
			earliestRecheckTime: &metav1.Time{Time: now.Add(time.Hour)},
			want:                false,
		},
		{
			name:                "matching identities with past recheck needs work",
			desired:             matchingDesired,
			stored:              matchingStored,
			earliestRecheckTime: &metav1.Time{Time: now.Add(-time.Hour)},
			want:                true,
		},
		{
			name:    "matching identities with nil recheck needs work",
			desired: matchingDesired,
			stored:  matchingStored,
			want:    true,
		},
		{
			name: "mismatched identities ignore future recheck",
			desired: map[string]*identityToResolve{
				strings.ToLower(testDataPlaneOperatorResourceID): {
					resourceID: metadataapi.Must(azcorearm.ParseResourceID(testDataPlaneOperatorResourceID)),
				},
			},
			stored:              matchingStored,
			earliestRecheckTime: &metav1.Time{Time: now.Add(time.Hour)},
			want:                true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			syncer := &fetchManagedIdentitiesInfoSyncer{
				clock: clocktesting.NewFakePassiveClock(now),
			}
			serviceProviderCluster := &coreapi.ServiceProviderCluster{}
			serviceProviderCluster.Status.ManagedIdentityDetails = tc.stored
			serviceProviderCluster.Status.ManagedIdentitiesEarliestRecheckTime = tc.earliestRecheckTime
			assert.Equal(t, tc.want, syncer.needsWork(serviceProviderCluster, tc.desired))
		})
	}
}

func TestFetchManagedIdentitiesInfoSyncOnce(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	lowerOperator := strings.ToLower(testOperatorIdentityResourceID)
	lowerSMI := strings.ToLower(testServiceManagedIdentityID)
	lowerDataPlane := strings.ToLower(testDataPlaneOperatorResourceID)
	operatorName := metadataapi.Must(azcorearm.ParseResourceID(testOperatorIdentityResourceID)).Name
	dataPlaneName := metadataapi.Must(azcorearm.ParseResourceID(testDataPlaneOperatorResourceID)).Name

	testHardcodedIdentity := &azureclient.HardcodedIdentity{
		ClientID:    "hardcoded-client",
		PrincipalID: "hardcoded-principal",
		TenantID:    "hardcoded-tenant",
	}

	t.Run("real dataplane and ARM fill applicable sources and skip ARM for SMI", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()

		cluster := newTestClusterForManagedIdentities()
		serviceProviderCluster := newTestServiceProviderCluster()

		mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
		serviceProviderClusterCRUD := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
		_, err := serviceProviderClusterCRUD.Create(ctx, serviceProviderCluster, nil)
		require.NoError(t, err)
		storedServiceProviderCluster, err := serviceProviderClusterCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)

		fakeDataplaneClient := &fakeManagedIdentitiesDataplaneClient{
			creds: &dataplane.ManagedIdentityCredentials{
				ExplicitIdentities: []dataplane.UserAssignedIdentityCredentials{
					uaCred(strings.ToUpper(testOperatorIdentityResourceID), ptr.To("op-dp-client"), ptr.To("op-dp-principal")),
					uaCred(strings.ToUpper(testServiceManagedIdentityID), ptr.To("smi-dp-client"), ptr.To("smi-dp-principal")),
				},
			},
		}
		fakeDataplaneClient.creds.ExplicitIdentities[0].TenantID = ptr.To("op-dp-tenant")
		fakeDataplaneClient.creds.ExplicitIdentities[1].TenantID = ptr.To("smi-dp-tenant")

		fakeUAIClient := &fakeUserAssignedIdentitiesClientByName{
			getByName: map[string]armmsi.UserAssignedIdentitiesClientGetResponse{
				strings.ToLower(operatorName):  uaIdentityResp("op-arm-client", "op-arm-principal", "op-arm-tenant"),
				strings.ToLower(dataPlaneName): uaIdentityResp("dp-arm-client", "dp-arm-principal", "dp-arm-tenant"),
			},
		}

		ctrl := gomock.NewController(t)
		smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiClientBuilder.EXPECT().
			UserAssignedIdentitiesClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(fakeUAIClient, nil).
			Times(1)

		syncer := &fetchManagedIdentitiesInfoSyncer{
			clock:                        clocktesting.NewFakePassiveClock(now),
			clusterLister:                &corelistertesting.SliceClusterLister{Clusters: []*coreapi.HCPOpenShiftCluster{cluster}},
			serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{storedServiceProviderCluster}},
			resourcesDBClient:            mockDB,
			fpaMIdataplaneClientBuilder:  &fakeFPAMIDataplaneClientBuilder{client: fakeDataplaneClient},
			smiClientBuilder:             smiClientBuilder,
		}

		err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
			SubscriptionID:    testSubscriptionID,
			ResourceGroupName: testResourceGroupName,
			HCPClusterName:    testClusterName,
		})
		require.NoError(t, err)
		assert.Equal(t, 1, fakeDataplaneClient.callCount)

		updated, err := serviceProviderClusterCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		require.NotNil(t, updated.Status.ManagedIdentitiesEarliestRecheckTime)
		assert.True(t, updated.Status.ManagedIdentitiesEarliestRecheckTime.After(now))

		require.Len(t, updated.Status.ManagedIdentityDetails, 3)

		op := updated.Status.ManagedIdentityDetails[lowerOperator]
		require.NotNil(t, op)
		require.NotNil(t, op.MetadataFromManagedIdentitiesDataplaneService)
		assert.Equal(t, "op-dp-client", *op.MetadataFromManagedIdentitiesDataplaneService.ClientID)
		assert.Equal(t, "op-dp-principal", *op.MetadataFromManagedIdentitiesDataplaneService.PrincipalID)
		assert.Equal(t, "op-dp-tenant", *op.MetadataFromManagedIdentitiesDataplaneService.TenantID)
		require.NotNil(t, op.MetadataFromARMUserAssignedIdentitiesAPI)
		assert.Equal(t, "op-arm-client", *op.MetadataFromARMUserAssignedIdentitiesAPI.ClientID)
		assert.Nil(t, op.MetadataFromHardcodedIdentity)

		smi := updated.Status.ManagedIdentityDetails[lowerSMI]
		require.NotNil(t, smi)
		require.NotNil(t, smi.MetadataFromManagedIdentitiesDataplaneService)
		assert.Equal(t, "smi-dp-client", *smi.MetadataFromManagedIdentitiesDataplaneService.ClientID)
		assert.Nil(t, smi.MetadataFromARMUserAssignedIdentitiesAPI, "SMI must not be fetched via ARM")
		assert.Nil(t, smi.MetadataFromHardcodedIdentity)

		dp := updated.Status.ManagedIdentityDetails[lowerDataPlane]
		require.NotNil(t, dp)
		assert.Nil(t, dp.MetadataFromManagedIdentitiesDataplaneService, "data plane operator identities are not queried from MI dataplane")
		require.NotNil(t, dp.MetadataFromARMUserAssignedIdentitiesAPI)
		assert.Equal(t, "dp-arm-client", *dp.MetadataFromARMUserAssignedIdentitiesAPI.ClientID)
		assert.Nil(t, dp.MetadataFromHardcodedIdentity)
	})

	t.Run("hardcoded environment fills hardcoded metadata and still queries ARM", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()

		cluster := newTestClusterForManagedIdentities()
		serviceProviderCluster := newTestServiceProviderCluster()

		mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
		serviceProviderClusterCRUD := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
		_, err := serviceProviderClusterCRUD.Create(ctx, serviceProviderCluster, nil)
		require.NoError(t, err)
		storedServiceProviderCluster, err := serviceProviderClusterCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)

		fakeUAIClient := &fakeUserAssignedIdentitiesClientByName{
			getByName: map[string]armmsi.UserAssignedIdentitiesClientGetResponse{
				strings.ToLower(operatorName):  uaIdentityResp("op-arm-client", "op-arm-principal", "op-arm-tenant"),
				strings.ToLower(dataPlaneName): uaIdentityResp("dp-arm-client", "dp-arm-principal", "dp-arm-tenant"),
			},
		}

		ctrl := gomock.NewController(t)
		smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiClientBuilder.EXPECT().
			UserAssignedIdentitiesClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(fakeUAIClient, nil).
			Times(1)

		syncer := &fetchManagedIdentitiesInfoSyncer{
			clock:                        clocktesting.NewFakePassiveClock(now),
			clusterLister:                &corelistertesting.SliceClusterLister{Clusters: []*coreapi.HCPOpenShiftCluster{cluster}},
			serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{storedServiceProviderCluster}},
			resourcesDBClient:            mockDB,
			hardcodedIdentity:            testHardcodedIdentity,
			smiClientBuilder:             smiClientBuilder,
		}

		err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
			SubscriptionID:    testSubscriptionID,
			ResourceGroupName: testResourceGroupName,
			HCPClusterName:    testClusterName,
		})
		require.NoError(t, err)

		updated, err := serviceProviderClusterCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)

		op := updated.Status.ManagedIdentityDetails[lowerOperator]
		require.NotNil(t, op)
		require.NotNil(t, op.MetadataFromHardcodedIdentity)
		assert.Equal(t, "hardcoded-client", *op.MetadataFromHardcodedIdentity.ClientID)
		assert.Equal(t, "hardcoded-principal", *op.MetadataFromHardcodedIdentity.PrincipalID)
		assert.Equal(t, "hardcoded-tenant", *op.MetadataFromHardcodedIdentity.TenantID)
		assert.Nil(t, op.MetadataFromManagedIdentitiesDataplaneService)
		require.NotNil(t, op.MetadataFromARMUserAssignedIdentitiesAPI)

		smi := updated.Status.ManagedIdentityDetails[lowerSMI]
		require.NotNil(t, smi)
		require.NotNil(t, smi.MetadataFromHardcodedIdentity)
		assert.Nil(t, smi.MetadataFromARMUserAssignedIdentitiesAPI)

		dp := updated.Status.ManagedIdentityDetails[lowerDataPlane]
		require.NotNil(t, dp)
		assert.Nil(t, dp.MetadataFromHardcodedIdentity, "data plane operator identities are not filled from the hardcoded identity")
		require.NotNil(t, dp.MetadataFromARMUserAssignedIdentitiesAPI)
	})

	t.Run("dataplane failure still persists ARM metadata and returns the error", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()

		cluster := newTestClusterForManagedIdentities()
		serviceProviderCluster := newTestServiceProviderCluster()

		mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
		serviceProviderClusterCRUD := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
		_, err := serviceProviderClusterCRUD.Create(ctx, serviceProviderCluster, nil)
		require.NoError(t, err)
		storedServiceProviderCluster, err := serviceProviderClusterCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)

		fakeDataplaneClient := &fakeManagedIdentitiesDataplaneClient{err: errFakeDataplane}
		fakeUAIClient := &fakeUserAssignedIdentitiesClientByName{
			getByName: map[string]armmsi.UserAssignedIdentitiesClientGetResponse{
				strings.ToLower(operatorName):  uaIdentityResp("op-arm-client", "op-arm-principal", "op-arm-tenant"),
				strings.ToLower(dataPlaneName): uaIdentityResp("dp-arm-client", "dp-arm-principal", "dp-arm-tenant"),
			},
		}

		ctrl := gomock.NewController(t)
		smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiClientBuilder.EXPECT().
			UserAssignedIdentitiesClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(fakeUAIClient, nil).
			Times(1)

		syncer := &fetchManagedIdentitiesInfoSyncer{
			clock:                        clocktesting.NewFakePassiveClock(now),
			clusterLister:                &corelistertesting.SliceClusterLister{Clusters: []*coreapi.HCPOpenShiftCluster{cluster}},
			serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{storedServiceProviderCluster}},
			resourcesDBClient:            mockDB,
			fpaMIdataplaneClientBuilder:  &fakeFPAMIDataplaneClientBuilder{client: fakeDataplaneClient},
			smiClientBuilder:             smiClientBuilder,
		}

		err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
			SubscriptionID:    testSubscriptionID,
			ResourceGroupName: testResourceGroupName,
			HCPClusterName:    testClusterName,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "simulated Managed Identities Data Plane failure")

		updated, err := serviceProviderClusterCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		assert.Nil(t, updated.Status.ManagedIdentitiesEarliestRecheckTime)

		op := updated.Status.ManagedIdentityDetails[lowerOperator]
		require.NotNil(t, op)
		require.NotNil(t, op.MetadataFromManagedIdentitiesDataplaneService, "dataplane still applies; an empty value means this pass did not resolve it")
		assert.False(t, op.MetadataFromManagedIdentitiesDataplaneService.HasResolvedIdentityInformation())
		assert.Nil(t, op.MetadataFromManagedIdentitiesDataplaneService.ClientID)
		assert.Nil(t, op.MetadataFromManagedIdentitiesDataplaneService.PrincipalID)
		assert.Nil(t, op.MetadataFromManagedIdentitiesDataplaneService.TenantID)
		assert.Nil(t, op.MetadataFromManagedIdentitiesDataplaneService.RetrievalError)
		require.NotNil(t, op.MetadataFromARMUserAssignedIdentitiesAPI)
		assert.Equal(t, "op-arm-client", *op.MetadataFromARMUserAssignedIdentitiesAPI.ClientID)

		dp := updated.Status.ManagedIdentityDetails[lowerDataPlane]
		require.NotNil(t, dp)
		require.NotNil(t, dp.MetadataFromARMUserAssignedIdentitiesAPI)
	})

	t.Run("one ARM Get failure still persists the other identities and dataplane metadata", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()

		cluster := newTestClusterForManagedIdentities()
		serviceProviderCluster := newTestServiceProviderCluster()

		mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
		serviceProviderClusterCRUD := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
		_, err := serviceProviderClusterCRUD.Create(ctx, serviceProviderCluster, nil)
		require.NoError(t, err)
		storedServiceProviderCluster, err := serviceProviderClusterCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)

		fakeDataplaneClient := &fakeManagedIdentitiesDataplaneClient{
			creds: &dataplane.ManagedIdentityCredentials{
				ExplicitIdentities: []dataplane.UserAssignedIdentityCredentials{
					uaCred(testOperatorIdentityResourceID, ptr.To("op-dp-client"), ptr.To("op-dp-principal")),
					uaCred(testServiceManagedIdentityID, ptr.To("smi-dp-client"), ptr.To("smi-dp-principal")),
				},
			},
		}

		fakeUAIClient := &fakeUserAssignedIdentitiesClientByName{
			getByName: map[string]armmsi.UserAssignedIdentitiesClientGetResponse{
				strings.ToLower(operatorName): uaIdentityResp("op-arm-client", "op-arm-principal", "op-arm-tenant"),
			},
			errByName: map[string]error{
				strings.ToLower(dataPlaneName): errors.New("simulated azure Get failure"),
			},
		}

		ctrl := gomock.NewController(t)
		smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiClientBuilder.EXPECT().
			UserAssignedIdentitiesClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(fakeUAIClient, nil).
			Times(1)

		syncer := &fetchManagedIdentitiesInfoSyncer{
			clock:                        clocktesting.NewFakePassiveClock(now),
			clusterLister:                &corelistertesting.SliceClusterLister{Clusters: []*coreapi.HCPOpenShiftCluster{cluster}},
			serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{storedServiceProviderCluster}},
			resourcesDBClient:            mockDB,
			fpaMIdataplaneClientBuilder:  &fakeFPAMIDataplaneClientBuilder{client: fakeDataplaneClient},
			smiClientBuilder:             smiClientBuilder,
		}

		err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
			SubscriptionID:    testSubscriptionID,
			ResourceGroupName: testResourceGroupName,
			HCPClusterName:    testClusterName,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "simulated azure Get failure")

		updated, err := serviceProviderClusterCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		assert.Nil(t, updated.Status.ManagedIdentitiesEarliestRecheckTime)

		op := updated.Status.ManagedIdentityDetails[lowerOperator]
		require.NotNil(t, op)
		require.NotNil(t, op.MetadataFromManagedIdentitiesDataplaneService)
		require.NotNil(t, op.MetadataFromARMUserAssignedIdentitiesAPI)

		dp := updated.Status.ManagedIdentityDetails[lowerDataPlane]
		require.NotNil(t, dp)
		require.NotNil(t, dp.MetadataFromARMUserAssignedIdentitiesAPI)
		assert.Nil(t, dp.MetadataFromARMUserAssignedIdentitiesAPI.ClientID)
		assert.Nil(t, dp.MetadataFromARMUserAssignedIdentitiesAPI.PrincipalID)
		assert.Nil(t, dp.MetadataFromARMUserAssignedIdentitiesAPI.TenantID)
		require.NotNil(t, dp.MetadataFromARMUserAssignedIdentitiesAPI.RetrievalError)
		assert.Contains(t, *dp.MetadataFromARMUserAssignedIdentitiesAPI.RetrievalError, "simulated azure Get failure")

		smi := updated.Status.ManagedIdentityDetails[lowerSMI]
		require.NotNil(t, smi)
		require.NotNil(t, smi.MetadataFromManagedIdentitiesDataplaneService)
	})

	t.Run("ARM ResourceNotFound stores RetrievalError and is not a sync failure", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()

		cluster := newTestClusterForManagedIdentities()
		serviceProviderCluster := newTestServiceProviderCluster()

		mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
		serviceProviderClusterCRUD := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
		_, err := serviceProviderClusterCRUD.Create(ctx, serviceProviderCluster, nil)
		require.NoError(t, err)
		storedServiceProviderCluster, err := serviceProviderClusterCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)

		fakeDataplaneClient := &fakeManagedIdentitiesDataplaneClient{
			creds: &dataplane.ManagedIdentityCredentials{
				ExplicitIdentities: []dataplane.UserAssignedIdentityCredentials{
					uaCred(testOperatorIdentityResourceID, ptr.To("op-dp-client"), ptr.To("op-dp-principal")),
					uaCred(testServiceManagedIdentityID, ptr.To("smi-dp-client"), ptr.To("smi-dp-principal")),
				},
			},
		}

		fakeUAIClient := &fakeUserAssignedIdentitiesClientByName{
			getByName: map[string]armmsi.UserAssignedIdentitiesClientGetResponse{
				strings.ToLower(operatorName): uaIdentityResp("op-arm-client", "op-arm-principal", "op-arm-tenant"),
			},
			errByName: map[string]error{
				strings.ToLower(dataPlaneName): resourceNotFoundResponseError(),
			},
		}

		ctrl := gomock.NewController(t)
		smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiClientBuilder.EXPECT().
			UserAssignedIdentitiesClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(fakeUAIClient, nil).
			Times(1)

		syncer := &fetchManagedIdentitiesInfoSyncer{
			clock:                        clocktesting.NewFakePassiveClock(now),
			clusterLister:                &corelistertesting.SliceClusterLister{Clusters: []*coreapi.HCPOpenShiftCluster{cluster}},
			serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{storedServiceProviderCluster}},
			resourcesDBClient:            mockDB,
			fpaMIdataplaneClientBuilder:  &fakeFPAMIDataplaneClientBuilder{client: fakeDataplaneClient},
			smiClientBuilder:             smiClientBuilder,
		}

		err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
			SubscriptionID:    testSubscriptionID,
			ResourceGroupName: testResourceGroupName,
			HCPClusterName:    testClusterName,
		})
		require.NoError(t, err)

		updated, err := serviceProviderClusterCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		require.NotNil(t, updated.Status.ManagedIdentitiesEarliestRecheckTime)

		dp := updated.Status.ManagedIdentityDetails[lowerDataPlane]
		require.NotNil(t, dp)
		require.NotNil(t, dp.MetadataFromARMUserAssignedIdentitiesAPI)
		assert.Nil(t, dp.MetadataFromARMUserAssignedIdentitiesAPI.ClientID)
		assert.Nil(t, dp.MetadataFromARMUserAssignedIdentitiesAPI.PrincipalID)
		assert.Nil(t, dp.MetadataFromARMUserAssignedIdentitiesAPI.TenantID)
		require.NotNil(t, dp.MetadataFromARMUserAssignedIdentitiesAPI.RetrievalError)
		assert.Contains(t, *dp.MetadataFromARMUserAssignedIdentitiesAPI.RetrievalError, "ResourceNotFound")

		op := updated.Status.ManagedIdentityDetails[lowerOperator]
		require.NotNil(t, op)
		require.NotNil(t, op.MetadataFromARMUserAssignedIdentitiesAPI)
		assert.Nil(t, op.MetadataFromARMUserAssignedIdentitiesAPI.RetrievalError)
	})

	t.Run("matching identities with future recheck skip source queries", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()

		cluster := newTestClusterForManagedIdentities()
		futureRecheck := metav1.NewTime(now.Add(6 * time.Hour))
		serviceProviderCluster := newTestServiceProviderCluster()
		serviceProviderCluster.Status.ManagedIdentitiesEarliestRecheckTime = &futureRecheck
		serviceProviderCluster.Status.ManagedIdentityDetails = map[string]*coreapi.ManagedIdentityMetadata{
			lowerOperator:  {ResourceID: metadataapi.Must(azcorearm.ParseResourceID(testOperatorIdentityResourceID))},
			lowerSMI:       {ResourceID: metadataapi.Must(azcorearm.ParseResourceID(testServiceManagedIdentityID))},
			lowerDataPlane: {ResourceID: metadataapi.Must(azcorearm.ParseResourceID(testDataPlaneOperatorResourceID))},
		}

		mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
		serviceProviderClusterCRUD := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
		_, err := serviceProviderClusterCRUD.Create(ctx, serviceProviderCluster, nil)
		require.NoError(t, err)
		storedServiceProviderCluster, err := serviceProviderClusterCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)

		fakeDataplaneClient := &fakeManagedIdentitiesDataplaneClient{}
		ctrl := gomock.NewController(t)
		smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiClientBuilder.EXPECT().
			UserAssignedIdentitiesClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Times(0)

		syncer := &fetchManagedIdentitiesInfoSyncer{
			clock:                        clocktesting.NewFakePassiveClock(now),
			clusterLister:                &corelistertesting.SliceClusterLister{Clusters: []*coreapi.HCPOpenShiftCluster{cluster}},
			serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{storedServiceProviderCluster}},
			resourcesDBClient:            mockDB,
			fpaMIdataplaneClientBuilder:  &fakeFPAMIDataplaneClientBuilder{client: fakeDataplaneClient},
			smiClientBuilder:             smiClientBuilder,
		}

		err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
			SubscriptionID:    testSubscriptionID,
			ResourceGroupName: testResourceGroupName,
			HCPClusterName:    testClusterName,
		})
		require.NoError(t, err)
		assert.Equal(t, 0, fakeDataplaneClient.callCount)
	})

	t.Run("collect errors return without replacing stored identities", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()

		cluster := newTestClusterForManagedIdentities(func(c *coreapi.HCPOpenShiftCluster) {
			c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity = nil
		})
		storedOperator := &coreapi.ManagedIdentityMetadata{
			ResourceID: metadataapi.Must(azcorearm.ParseResourceID(testOperatorIdentityResourceID)),
		}
		serviceProviderCluster := newTestServiceProviderCluster()
		serviceProviderCluster.Status.ManagedIdentityDetails = map[string]*coreapi.ManagedIdentityMetadata{
			lowerOperator: storedOperator,
		}

		mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
		serviceProviderClusterCRUD := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
		_, err := serviceProviderClusterCRUD.Create(ctx, serviceProviderCluster, nil)
		require.NoError(t, err)
		storedServiceProviderCluster, err := serviceProviderClusterCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)

		fakeDataplaneClient := &fakeManagedIdentitiesDataplaneClient{}
		ctrl := gomock.NewController(t)
		smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiClientBuilder.EXPECT().
			UserAssignedIdentitiesClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Times(0)

		syncer := &fetchManagedIdentitiesInfoSyncer{
			clock:                        clocktesting.NewFakePassiveClock(now),
			clusterLister:                &corelistertesting.SliceClusterLister{Clusters: []*coreapi.HCPOpenShiftCluster{cluster}},
			serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{storedServiceProviderCluster}},
			resourcesDBClient:            mockDB,
			fpaMIdataplaneClientBuilder:  &fakeFPAMIDataplaneClientBuilder{client: fakeDataplaneClient},
			smiClientBuilder:             smiClientBuilder,
		}

		err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
			SubscriptionID:    testSubscriptionID,
			ResourceGroupName: testResourceGroupName,
			HCPClusterName:    testClusterName,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "service managed identity")
		assert.Equal(t, 0, fakeDataplaneClient.callCount)

		updated, err := serviceProviderClusterCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		require.Len(t, updated.Status.ManagedIdentityDetails, 1)
		require.Contains(t, updated.Status.ManagedIdentityDetails, lowerOperator)
		assert.Nil(t, updated.Status.ManagedIdentitiesEarliestRecheckTime)
	})
}
