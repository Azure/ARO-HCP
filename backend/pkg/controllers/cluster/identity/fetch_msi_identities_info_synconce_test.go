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

package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/msi-dataplane/pkg/dataplane"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

const testMIDataplaneURL = "https://mi-dataplane.example.com/identity"

// errFakeDataplane is returned by the fake client to exercise error propagation.
var errFakeDataplane = errors.New("simulated Managed Identities Data Plane failure")

// fakeManagedIdentitiesDataplaneClient is a test double for
// azureclient.ManagedIdentitiesDataplaneClient. It records the requests it
// receives and returns canned credentials or an error.
type fakeManagedIdentitiesDataplaneClient struct {
	creds     *dataplane.ManagedIdentityCredentials
	err       error
	callCount int
	lastReq   dataplane.UserAssignedIdentitiesRequest
}

func (f *fakeManagedIdentitiesDataplaneClient) GetUserAssignedIdentitiesCredentials(_ context.Context, request dataplane.UserAssignedIdentitiesRequest) (*dataplane.ManagedIdentityCredentials, error) {
	f.callCount++
	f.lastReq = request
	if f.err != nil {
		return nil, f.err
	}
	return f.creds, nil
}

// fakeFPAMIDataplaneClientBuilder is a test double for
// azureclient.FPAMIDataplaneClientBuilder. It records the identity URL it is
// asked to build a client for and hands back a configured client (or error).
type fakeFPAMIDataplaneClientBuilder struct {
	client   azureclient.ManagedIdentitiesDataplaneClient
	buildErr error
	lastURL  string
}

func (b *fakeFPAMIDataplaneClientBuilder) BuilderType() azureclient.FPAMIDataplaneClientBuilderType {
	return azureclient.FPAMIDataplaneClientBuilderTypeValue
}

func (b *fakeFPAMIDataplaneClientBuilder) ManagedIdentitiesDataplane(identityURL string) (azureclient.ManagedIdentitiesDataplaneClient, error) {
	b.lastURL = identityURL
	if b.buildErr != nil {
		return nil, b.buildErr
	}
	return b.client, nil
}

// newTestClusterForFetch builds a stored-in-Cosmos cluster shape carrying the
// CustomerProperties MSI identities that the fetch controller reads.
func newTestClusterForFetch(opts ...func(*coreapi.HCPOpenShiftCluster)) *coreapi.HCPOpenShiftCluster {
	cluster := newTestClusterForClusterIdentitySync()
	cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL = testMIDataplaneURL
	cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities = coreapi.UserAssignedIdentitiesProfile{
		ControlPlaneOperators: map[string]*azcorearm.ResourceID{
			testOperatorName: metadataapi.Must(azcorearm.ParseResourceID(testOperatorIdentityResourceID)),
		},
		ServiceManagedIdentity: metadataapi.Must(azcorearm.ParseResourceID(testServiceManagedIdentityID)),
	}
	for _, opt := range opts {
		opt(cluster)
	}
	return cluster
}

// newTestServiceProviderClusterWithMatchingMSIIdentities returns a ServiceProviderCluster whose
// stored MSI identity set matches newTestClusterForFetch, so
// desiredMSIResourceIDsMatchServiceProviderCluster is true. recheck controls this
// controller's entry in Spec.EarliestRecheckTimesByController.
func newTestServiceProviderClusterWithMatchingMSIIdentities(recheck *metav1.Time) *coreapi.ServiceProviderCluster {
	serviceProviderCluster := newTestServiceProviderCluster()
	lowerOperator := strings.ToLower(testOperatorIdentityResourceID)
	lowerSMI := strings.ToLower(testServiceManagedIdentityID)
	if recheck != nil {
		serviceProviderCluster.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{
			FetchMSIIdentitiesInfoControllerName: recheck,
		}
	}
	serviceProviderCluster.Status.MSIManagedIdentities = coreapi.ServiceProviderClusterMSIManagedIdentities{
		ControlPlaneOperatorsIdentities: map[string]*coreapi.ServiceProviderClusterControlPlaneOperatorIdentity{
			lowerOperator: {
				ResourceID:  metadataapi.Must(azcorearm.ParseResourceID(lowerOperator)),
				ClientID:    ptr.To("existing-op-client"),
				PrincipalID: ptr.To("existing-op-principal"),
			},
		},
		ServiceManagedIdentity: &coreapi.ServiceProviderClusterServiceManagedIdentity{
			ResourceID:  metadataapi.Must(azcorearm.ParseResourceID(lowerSMI)),
			ClientID:    ptr.To("existing-smi-client"),
			PrincipalID: ptr.To("existing-smi-principal"),
		},
	}
	return serviceProviderCluster
}

// uaCred is a small constructor for a dataplane user-assigned identity credential.
func uaCred(resourceID string, clientID, objectID *string) dataplane.UserAssignedIdentityCredentials {
	return dataplane.UserAssignedIdentityCredentials{
		ResourceID: ptr.To(resourceID),
		ClientID:   clientID,
		ObjectID:   objectID,
	}
}

func TestFetchMSIIdentitiesInfoSyncer_SyncOnce(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	futureRecheck := metav1.NewTime(now.Add(6 * time.Hour))
	pastRecheck := metav1.NewTime(now.Add(-time.Hour))

	lowerOperator := strings.ToLower(testOperatorIdentityResourceID)

	testCases := []struct {
		name                   string
		cluster                *coreapi.HCPOpenShiftCluster    // exposed via the cluster lister; nil = not present
		serviceProviderCluster *coreapi.ServiceProviderCluster // seeded in cosmos + lister; nil = not present
		dataplaneCreds         *dataplane.ManagedIdentityCredentials
		dataplaneErr           error
		expectError            bool
		expectDataplaneCalls   int
		verify                 func(t *testing.T, serviceProviderCluster *coreapi.ServiceProviderCluster)
	}{
		{
			name:                   "happy path resolves client and principal IDs and sets recheck time",
			cluster:                newTestClusterForFetch(),
			serviceProviderCluster: newTestServiceProviderCluster(),
			dataplaneCreds: &dataplane.ManagedIdentityCredentials{
				// Returned with upper-cased resource IDs and in reverse request
				// order to exercise case-insensitive, order-independent matching.
				ExplicitIdentities: []dataplane.UserAssignedIdentityCredentials{
					uaCred(strings.ToUpper(testServiceManagedIdentityID), ptr.To("smi-client"), ptr.To("smi-principal")),
					uaCred(strings.ToUpper(testOperatorIdentityResourceID), ptr.To("op-client"), ptr.To("op-principal")),
				},
			},
			expectDataplaneCalls: 1,
			verify: func(t *testing.T, serviceProviderCluster *coreapi.ServiceProviderCluster) {
				msi := serviceProviderCluster.Status.MSIManagedIdentities

				require.Contains(t, msi.ControlPlaneOperatorsIdentities, lowerOperator, "control plane operator identity should be keyed by lowercased resource ID")
				op := msi.ControlPlaneOperatorsIdentities[lowerOperator]
				require.NotNil(t, op, "control plane operator identity should not be nil")
				require.NotNil(t, op.ResourceID, "control plane operator resource ID should be set")
				require.NotNil(t, op.ClientID, "control plane operator client ID should be set")
				assert.Equal(t, "op-client", *op.ClientID)
				require.NotNil(t, op.PrincipalID, "control plane operator principal ID should be set")
				assert.Equal(t, "op-principal", *op.PrincipalID)

				require.NotNil(t, msi.ServiceManagedIdentity, "service managed identity should be set")
				require.NotNil(t, msi.ServiceManagedIdentity.ClientID, "service managed identity client ID should be set")
				assert.Equal(t, "smi-client", *msi.ServiceManagedIdentity.ClientID)
				require.NotNil(t, msi.ServiceManagedIdentity.PrincipalID, "service managed identity principal ID should be set")
				assert.Equal(t, "smi-principal", *msi.ServiceManagedIdentity.PrincipalID)

				recheck := serviceProviderCluster.Spec.EarliestRecheckTimesByController[FetchMSIIdentitiesInfoControllerName]
				require.NotNil(t, recheck, "earliest recheck time should be set after a successful fetch")
				assert.True(t, recheck.After(now), "earliest recheck time should be in the future")
			},
		},
		{
			name:                   "identity not found in azure persists nil client and principal IDs",
			cluster:                newTestClusterForFetch(),
			serviceProviderCluster: newTestServiceProviderCluster(),
			dataplaneCreds: &dataplane.ManagedIdentityCredentials{
				ExplicitIdentities: []dataplane.UserAssignedIdentityCredentials{
					uaCred(testOperatorIdentityResourceID, nil, nil),
					uaCred(testServiceManagedIdentityID, nil, nil),
				},
			},
			expectDataplaneCalls: 1,
			verify: func(t *testing.T, serviceProviderCluster *coreapi.ServiceProviderCluster) {
				msi := serviceProviderCluster.Status.MSIManagedIdentities
				op := msi.ControlPlaneOperatorsIdentities[lowerOperator]
				require.NotNil(t, op, "control plane operator identity should still be recorded")
				assert.Nil(t, op.ClientID, "client ID should be nil when the identity does not exist in Azure")
				assert.Nil(t, op.PrincipalID, "principal ID should be nil when the identity does not exist in Azure")
				require.NotNil(t, msi.ServiceManagedIdentity, "service managed identity should still be recorded")
				assert.Nil(t, msi.ServiceManagedIdentity.ClientID, "service managed identity client ID should be nil")
				assert.Nil(t, msi.ServiceManagedIdentity.PrincipalID, "service managed identity principal ID should be nil")
				require.NotNil(t, serviceProviderCluster.Spec.EarliestRecheckTimesByController[FetchMSIIdentitiesInfoControllerName], "recheck time should still be set")
			},
		},
		{
			name: "deduplicates control plane operators sharing an identity",
			cluster: newTestClusterForFetch(func(c *coreapi.HCPOpenShiftCluster) {
				// A second operator references the SAME identity as testOperatorName.
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators["second-operator"] =
					metadataapi.Must(azcorearm.ParseResourceID(testOperatorIdentityResourceID))
			}),
			serviceProviderCluster: newTestServiceProviderCluster(),
			dataplaneCreds: &dataplane.ManagedIdentityCredentials{
				// Only two identities are requested (the shared operator identity
				// and the service managed identity), so only two are returned.
				ExplicitIdentities: []dataplane.UserAssignedIdentityCredentials{
					uaCred(testOperatorIdentityResourceID, ptr.To("op-client"), ptr.To("op-principal")),
					uaCred(testServiceManagedIdentityID, ptr.To("smi-client"), ptr.To("smi-principal")),
				},
			},
			expectDataplaneCalls: 1,
			verify: func(t *testing.T, serviceProviderCluster *coreapi.ServiceProviderCluster) {
				msi := serviceProviderCluster.Status.MSIManagedIdentities
				assert.Len(t, msi.ControlPlaneOperatorsIdentities, 1, "operators sharing one identity should be de-duplicated to a single entry")
				op := msi.ControlPlaneOperatorsIdentities[lowerOperator]
				require.NotNil(t, op)
				require.NotNil(t, op.ClientID)
				assert.Equal(t, "op-client", *op.ClientID)
			},
		},
		{
			name: "deleting cluster does no work",
			cluster: newTestClusterForFetch(func(c *coreapi.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: now}
			}),
			serviceProviderCluster: newTestServiceProviderCluster(),
			expectDataplaneCalls:   0,
			verify: func(t *testing.T, serviceProviderCluster *coreapi.ServiceProviderCluster) {
				assert.Empty(t, serviceProviderCluster.Status.MSIManagedIdentities.ControlPlaneOperatorsIdentities, "no control plane operator identities should be written for a deleting cluster")
				assert.Nil(t, serviceProviderCluster.Status.MSIManagedIdentities.ServiceManagedIdentity, "no service managed identity should be written for a deleting cluster")
			},
		},
		{
			name: "empty managed identities data plane url does no work",
			cluster: newTestClusterForFetch(func(c *coreapi.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL = ""
			}),
			serviceProviderCluster: newTestServiceProviderCluster(),
			expectDataplaneCalls:   0,
			verify: func(t *testing.T, serviceProviderCluster *coreapi.ServiceProviderCluster) {
				assert.Empty(t, serviceProviderCluster.Status.MSIManagedIdentities.ControlPlaneOperatorsIdentities, "no work should be done when the dataplane identity URL is empty")
				assert.Nil(t, serviceProviderCluster.Status.MSIManagedIdentities.ServiceManagedIdentity)
			},
		},
		{
			name:                   "future recheck with matching identities skips dataplane",
			cluster:                newTestClusterForFetch(),
			serviceProviderCluster: newTestServiceProviderClusterWithMatchingMSIIdentities(&futureRecheck),
			expectDataplaneCalls:   0,
			verify: func(t *testing.T, serviceProviderCluster *coreapi.ServiceProviderCluster) {
				op := serviceProviderCluster.Status.MSIManagedIdentities.ControlPlaneOperatorsIdentities[lowerOperator]
				require.NotNil(t, op, "existing control plane operator identity should be preserved")
				require.NotNil(t, op.ClientID, "existing control plane operator client ID should be preserved")
				assert.Equal(t, "existing-op-client", *op.ClientID, "existing values should be untouched while recheck is in the future")
				recheck := serviceProviderCluster.Spec.EarliestRecheckTimesByController[FetchMSIIdentitiesInfoControllerName]
				require.NotNil(t, recheck, "recheck time should be preserved")
				assert.True(t, recheck.After(now), "recheck time should still be in the future when work is skipped")
			},
		},
		{
			name:                   "past recheck requeries and updates",
			cluster:                newTestClusterForFetch(),
			serviceProviderCluster: newTestServiceProviderClusterWithMatchingMSIIdentities(&pastRecheck),
			dataplaneCreds: &dataplane.ManagedIdentityCredentials{
				ExplicitIdentities: []dataplane.UserAssignedIdentityCredentials{
					uaCred(testOperatorIdentityResourceID, ptr.To("new-op-client"), ptr.To("new-op-principal")),
					uaCred(testServiceManagedIdentityID, ptr.To("new-smi-client"), ptr.To("new-smi-principal")),
				},
			},
			expectDataplaneCalls: 1,
			verify: func(t *testing.T, serviceProviderCluster *coreapi.ServiceProviderCluster) {
				op := serviceProviderCluster.Status.MSIManagedIdentities.ControlPlaneOperatorsIdentities[lowerOperator]
				require.NotNil(t, op, "control plane operator identity should be present")
				require.NotNil(t, op.ClientID, "control plane operator client ID should be updated")
				assert.Equal(t, "new-op-client", *op.ClientID, "stale value should be replaced with the freshly fetched one")
				recheck := serviceProviderCluster.Spec.EarliestRecheckTimesByController[FetchMSIIdentitiesInfoControllerName]
				require.NotNil(t, recheck, "recheck time should be set")
				assert.True(t, recheck.After(now), "recheck time should be pushed into the future after a refetch")
			},
		},
		{
			name:                   "unexpected credential count returns error",
			cluster:                newTestClusterForFetch(),
			serviceProviderCluster: newTestServiceProviderCluster(),
			dataplaneCreds: &dataplane.ManagedIdentityCredentials{
				ExplicitIdentities: []dataplane.UserAssignedIdentityCredentials{
					uaCred(testOperatorIdentityResourceID, ptr.To("op-client"), ptr.To("op-principal")),
				},
			},
			expectError:          true,
			expectDataplaneCalls: 1,
			verify: func(t *testing.T, serviceProviderCluster *coreapi.ServiceProviderCluster) {
				assert.Empty(t, serviceProviderCluster.Status.MSIManagedIdentities.ControlPlaneOperatorsIdentities, "ServiceProviderCluster must not be mutated when the fetch errors")
			},
		},
		{
			name:                   "credential with nil resource ID returns error",
			cluster:                newTestClusterForFetch(),
			serviceProviderCluster: newTestServiceProviderCluster(),
			dataplaneCreds: &dataplane.ManagedIdentityCredentials{
				ExplicitIdentities: []dataplane.UserAssignedIdentityCredentials{
					uaCred(testOperatorIdentityResourceID, ptr.To("op-client"), ptr.To("op-principal")),
					{ResourceID: nil, ClientID: ptr.To("smi-client"), ObjectID: ptr.To("smi-principal")},
				},
			},
			expectError:          true,
			expectDataplaneCalls: 1,
		},
		{
			name:                   "requested identity missing from response returns error",
			cluster:                newTestClusterForFetch(),
			serviceProviderCluster: newTestServiceProviderCluster(),
			dataplaneCreds: &dataplane.ManagedIdentityCredentials{
				// The credential count matches, but the control plane operator
				// identity is absent (both entries are the service managed identity).
				ExplicitIdentities: []dataplane.UserAssignedIdentityCredentials{
					uaCred(testServiceManagedIdentityID, ptr.To("smi-client"), ptr.To("smi-principal")),
					uaCred(testServiceManagedIdentityID, ptr.To("smi-client"), ptr.To("smi-principal")),
				},
			},
			expectError:          true,
			expectDataplaneCalls: 1,
		},
		{
			name:                   "dataplane error is propagated",
			cluster:                newTestClusterForFetch(),
			serviceProviderCluster: newTestServiceProviderCluster(),
			dataplaneErr:           errFakeDataplane,
			expectError:            true,
			expectDataplaneCalls:   1,
		},
		{
			name:                   "missing ServiceProviderCluster does no work",
			cluster:                newTestClusterForFetch(),
			serviceProviderCluster: nil,
			expectDataplaneCalls:   0,
		},
		{
			name:                   "missing cluster does no work",
			cluster:                nil,
			serviceProviderCluster: newTestServiceProviderCluster(),
			expectDataplaneCalls:   0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
			serviceProviderClusterCRUD := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)

			var clusterListerItems []*coreapi.HCPOpenShiftCluster
			if tc.cluster != nil {
				clusterListerItems = append(clusterListerItems, tc.cluster)
			}
			sliceClusterLister := &corelistertesting.SliceClusterLister{Clusters: clusterListerItems}

			var serviceProviderClusterListerItems []*coreapi.ServiceProviderCluster
			if tc.serviceProviderCluster != nil {
				_, err := serviceProviderClusterCRUD.Create(ctx, tc.serviceProviderCluster, nil)
				require.NoError(t, err, "failed to seed ServiceProviderCluster")
				// Read back so the cached copy carries the stored etag used by Replace.
				storedServiceProviderCluster, err := serviceProviderClusterCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				serviceProviderClusterListerItems = append(serviceProviderClusterListerItems, storedServiceProviderCluster)
			}
			sliceServiceProviderClusterLister := &corelistertesting.SliceServiceProviderClusterLister{
				ServiceProviderClusters: serviceProviderClusterListerItems,
			}

			fakeClient := &fakeManagedIdentitiesDataplaneClient{creds: tc.dataplaneCreds, err: tc.dataplaneErr}
			fakeBuilder := &fakeFPAMIDataplaneClientBuilder{client: fakeClient}

			syncer := &fetchMSIIdentitiesInfoSyncer{
				clock:                        clocktesting.NewFakePassiveClock(now),
				clusterLister:                sliceClusterLister,
				serviceProviderClusterLister: sliceServiceProviderClusterLister,
				resourcesDBClient:            mockDB,
				fpaMIdataplaneClientBuilder:  fakeBuilder,
			}

			key := controllerutils.HCPClusterKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    testClusterName,
			}
			err := syncer.SyncOnce(ctx, key)
			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tc.expectDataplaneCalls, fakeClient.callCount, "unexpected number of Managed Identities Data Plane calls")
			if tc.expectDataplaneCalls > 0 {
				assert.Equal(t, testMIDataplaneURL, fakeBuilder.lastURL, "builder should receive the cluster's MI dataplane identity URL")
			}

			if tc.verify != nil {
				require.NotNil(t, tc.serviceProviderCluster, "verify requires a seeded ServiceProviderCluster")
				updatedServiceProviderCluster, getErr := serviceProviderClusterCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, getErr, "failed to read ServiceProviderCluster after sync")
				tc.verify(t, updatedServiceProviderCluster)
			}
		})
	}
}

func TestCollectMSIBasedIdentitiesToFetch(t *testing.T) {
	t.Parallel()

	syncer := &fetchMSIIdentitiesInfoSyncer{}

	t.Run("returns error when service managed identity is nil", func(t *testing.T) {
		t.Parallel()
		cluster, _ := newMatchingClusterAndServiceProviderCluster()
		cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity = nil
		_, err := syncer.collectMSIBasedIdentitiesToFetch(cluster)
		require.Error(t, err, "a nil service managed identity should be rejected")
	})

	t.Run("returns error when a control plane operator identity is nil", func(t *testing.T) {
		t.Parallel()
		cluster, _ := newMatchingClusterAndServiceProviderCluster()
		cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators[testOperatorName] = nil
		_, err := syncer.collectMSIBasedIdentitiesToFetch(cluster)
		require.Error(t, err, "a nil control plane operator identity should be rejected")
	})

	t.Run("collects control plane operators and service managed identity", func(t *testing.T) {
		t.Parallel()
		cluster, _ := newMatchingClusterAndServiceProviderCluster()
		got, err := syncer.collectMSIBasedIdentitiesToFetch(cluster)
		require.NoError(t, err, "a well-formed cluster should collect without error")
		require.Len(t, got.controlPlaneOperators, 1, "the single control plane operator should be collected")
		require.NotNil(t, got.serviceManagedIdentity, "the service managed identity should be collected")
		assert.Len(t, got.resourceIDStrings(), 2, "the request should include the operator and the service managed identity")
	})

	t.Run("deduplicates control plane operators sharing an identity", func(t *testing.T) {
		t.Parallel()
		cluster, _ := newMatchingClusterAndServiceProviderCluster()
		cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators["second-operator"] =
			metadataapi.Must(azcorearm.ParseResourceID(strings.ToUpper(testOperatorIdentityResourceID)))
		got, err := syncer.collectMSIBasedIdentitiesToFetch(cluster)
		require.NoError(t, err, "sharing an identity should not error")
		require.Len(t, got.controlPlaneOperators, 1, "operators sharing one identity should be de-duplicated")
		assert.Len(t, got.resourceIDStrings(), 2, "the request should include the shared operator identity once and the service managed identity")
	})
}
