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

package dataplaneworkloads

import (
	"context"
	"errors"
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
	"github.com/Azure/msi-dataplane/pkg/dataplane"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/azure/federatedidentitycredential"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

var testOIDCFederationIdentityA = coreapi.DataplaneOIDCFederationIdentityInstance{
	ClientID:    "client-a",
	PrincipalID: "principal-a",
	TenantID:    "tenant-a",
}

func TestDataPlaneOIDCFederationNeedsWork(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	future := metav1.NewTime(now.Add(time.Hour))
	since := metav1.NewTime(now)
	sinceElapsed := metav1.NewTime(now.Add(-dataPlaneOIDCFederationDeconfigureDelay))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())
	keyB := "/subscriptions/" + testSubscriptionID + "/resourcegroups/" + testResourceGroupName + "/providers/microsoft.managedidentity/userassignedidentities/identity-b"
	diskCSIFICs := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))

	testCases := []struct {
		name              string
		federation        map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus
		cluster           *coreapi.HCPOpenShiftCluster
		controllerRecheck *metav1.Time
		expectedNeedsWork bool
	}{
		{
			name:              "empty federation does not need work",
			expectedNeedsWork: false,
		},
		{
			name: "PendingConfigure needs work",
			federation: map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorPending()),
			},
			expectedNeedsWork: true,
		},
		{
			name: "PendingConfigure does not need work when the cluster service ID is missing",
			federation: map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorPending()),
			},
			cluster:           &coreapi.HCPOpenShiftCluster{},
			expectedNeedsWork: false,
		},
		{
			name: "PendingConfigure does not need work when the cluster is being deleted",
			federation: map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorPending()),
			},
			cluster: &coreapi.HCPOpenShiftCluster{
				ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
					DeletionTimestamp: &future,
				},
			},
			expectedNeedsWork: false,
		},
		{
			name: "Configured does not need work when the cluster is being deleted",
			federation: map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorEnsured(testOIDCFederationIdentityA, diskCSIFICs)),
			},
			cluster: &coreapi.HCPOpenShiftCluster{
				ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
					DeletionTimestamp: &future,
				},
			},
			expectedNeedsWork: false,
		},
		{
			name: "PendingDeconfigure still needs work during cluster deletion when another identity is PendingConfigure",
			federation: map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorPending()),
				keyB: oidcIdentityStatus(testOIDCFederationIdentityA, testImageRegistryOp, oidcOperatorDeconfigure(&since, nil, nil)),
			},
			cluster: &coreapi.HCPOpenShiftCluster{
				ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
					DeletionTimestamp: &future,
				},
			},
			expectedNeedsWork: true,
		},
		{
			name: "identity with nil DeconfigureTimestamp does not need deconfigure work when the cluster is being deleted",
			federation: map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorPending()),
			},
			cluster: &coreapi.HCPOpenShiftCluster{
				ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
					DeletionTimestamp: &future,
				},
			},
			expectedNeedsWork: false,
		},
		{
			name: "Configured with nil recheck needs work",
			federation: map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorEnsured(testOIDCFederationIdentityA, diskCSIFICs)),
			},
			expectedNeedsWork: true,
		},
		{
			name: "PendingConfigure with future controller recheck still needs work",
			federation: map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorPending()),
			},
			controllerRecheck: &future,
			expectedNeedsWork: true,
		},
		{
			name: "PendingDeconfigure with recent DeconfigureTimestamp does not need work",
			federation: map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorDeconfigure(&since, nil, nil)),
			},
			expectedNeedsWork: false,
		},
		{
			name: "PendingDeconfigure with recent DeconfigureTimestamp needs work when the cluster is being deleted",
			federation: map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorDeconfigure(&since, nil, nil)),
			},
			cluster: &coreapi.HCPOpenShiftCluster{
				ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
					DeletionTimestamp: &future,
				},
			},
			expectedNeedsWork: true,
		},
		{
			name: "PendingDeconfigure with elapsed DeconfigureTimestamp needs work",
			federation: map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorDeconfigure(&sinceElapsed, nil, nil)),
			},
			expectedNeedsWork: true,
		},
		{
			name: "PendingDeconfigure with future controller recheck needs work after DeconfigureTimestamp elapsed",
			federation: map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorDeconfigure(&sinceElapsed, nil, nil)),
			},
			controllerRecheck: &future,
			expectedNeedsWork: true,
		},
		{
			name: "Configured with future controller recheck does not need work when the desired set is unchanged",
			federation: map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorEnsured(testOIDCFederationIdentityA, diskCSIFICs)),
			},
			controllerRecheck: &future,
			expectedNeedsWork: false,
		},
		{
			name: "Configured with future controller recheck needs work when pending has obsolete FIC IDs",
			federation: map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, &coreapi.DataplaneOIDCFederationOperatorStatus{
					EnsuredIdentity:       ptr.To(testOIDCFederationIdentityA),
					AzureResources:        diskCSIFICs,
					PendingAzureResources: []*azcorearm.ResourceID{metadataapi.Must(azcorearm.ParseResourceID(identityA.String() + "/federatedIdentityCredentials/obsolete-pending-fic"))},
				}),
			},
			controllerRecheck: &future,
			expectedNeedsWork: true,
		},
		{
			name: "Configured plus waiting PendingDeconfigure with future controller recheck does not need work",
			federation: map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorEnsured(testOIDCFederationIdentityA, diskCSIFICs)),
				keyB: oidcIdentityStatus(testOIDCFederationIdentityA, testImageRegistryOp, oidcOperatorDeconfigure(&since, nil, nil)),
			},
			controllerRecheck: &future,
			expectedNeedsWork: false,
		},
		{
			name: "Configured plus ready PendingDeconfigure ignores future controller recheck",
			federation: map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorEnsured(testOIDCFederationIdentityA, diskCSIFICs)),
				keyB: oidcIdentityStatus(testOIDCFederationIdentityA, testImageRegistryOp, oidcOperatorDeconfigure(&sinceElapsed, nil, nil)),
			},
			controllerRecheck: &future,
			expectedNeedsWork: true,
		},
		{
			name: "Configured plus PendingConfigure ignores future controller recheck",
			federation: map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorEnsured(testOIDCFederationIdentityA, diskCSIFICs)),
				keyB: oidcIdentityStatus(testOIDCFederationIdentityA, testImageRegistryOp, oidcOperatorPending()),
			},
			controllerRecheck: &future,
			expectedNeedsWork: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			syncer := &dataPlaneOIDCFederationSyncer{
				clock:                         clocktesting.NewFakePassiveClock(now),
				clusterScopedIdentitiesConfig: testDataPlaneOIDCFederationIdentitiesConfig(),
			}
			cluster := tc.cluster
			if cluster == nil {
				cluster = &coreapi.HCPOpenShiftCluster{}
				cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()
			}
			seedDesiredDataPlaneOperators(cluster, tc.federation)
			serviceProviderCluster := &coreapi.ServiceProviderCluster{}
			serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = tc.federation
			if tc.controllerRecheck != nil {
				serviceProviderCluster.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{
					DataPlaneOIDCFederationControllerName: tc.controllerRecheck,
				}
			}
			assert.Equal(t, tc.expectedNeedsWork, syncer.needsWork(cluster, serviceProviderCluster))
		})
	}
}

func TestServiceManagedIdentityExists(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	smiResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))

	testCases := []struct {
		name          string
		identities    []dataplane.UserAssignedIdentityCredentials
		wantExists    bool
		wantErrSubstr string
	}{
		{
			name: "exists when credential fields are set",
			identities: []dataplane.UserAssignedIdentityCredentials{{
				ResourceID:             ptr.To(smiResourceID.String()),
				ClientID:               ptr.To("smi-client-id"),
				ClientSecret:           ptr.To("smi-client-secret"),
				TenantID:               ptr.To("smi-tenant-id"),
				AuthenticationEndpoint: ptr.To("https://login.microsoftonline.com/"),
			}},
			wantExists: true,
		},
		{
			name: "does not exist when required credential fields are unset",
			identities: []dataplane.UserAssignedIdentityCredentials{{
				ResourceID: ptr.To(smiResourceID.String()),
			}},
		},
		{
			name:          "errors when no credentials are returned",
			wantErrSubstr: "returned no credentials",
		},
		{
			name: "errors when more than one credential is returned",
			identities: []dataplane.UserAssignedIdentityCredentials{
				{ResourceID: ptr.To(smiResourceID.String()), ClientID: ptr.To("a"), ObjectID: ptr.To("b")},
				{ResourceID: ptr.To(smiResourceID.String()), ClientID: ptr.To("c"), ObjectID: ptr.To("d")},
			},
			wantErrSubstr: "expected 1",
		},
		{
			name: "errors when ResourceID is nil",
			identities: []dataplane.UserAssignedIdentityCredentials{{
				ClientID: ptr.To("smi-client-id"),
				ObjectID: ptr.To("smi-object-id"),
			}},
			wantErrSubstr: "Resource ID is nil",
		},
		{
			name: "errors when ResourceID does not match the requested identity",
			identities: []dataplane.UserAssignedIdentityCredentials{{
				ResourceID:             ptr.To("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/other"),
				ClientID:               ptr.To("smi-client-id"),
				ClientSecret:           ptr.To("smi-client-secret"),
				TenantID:               ptr.To("smi-tenant-id"),
				AuthenticationEndpoint: ptr.To("https://login.microsoftonline.com/"),
			}},
			wantErrSubstr: "does not match requested ServiceManagedIdentity",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			syncer := &dataPlaneOIDCFederationSyncer{
				fpaMIdataplaneClientBuilder: &fakeFPAMIDataplaneClientBuilder{
					client: &fakeManagedIdentitiesDataplaneClient{
						creds: &dataplane.ManagedIdentityCredentials{ExplicitIdentities: tc.identities},
					},
				},
			}
			exists, err := syncer.serviceManagedIdentityExists(ctx, "https://identity.example.com", smiResourceID)
			if tc.wantErrSubstr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErrSubstr)
				assert.False(t, exists)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantExists, exists)
		})
	}
}

func TestDataPlaneOIDCIssuerURL(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		baseURL  string
		tenantID string
		csID     string
		want     string
	}{
		{
			name:     "adds trailing slash to base URL",
			baseURL:  "https://oidc.example.com",
			tenantID: "tenant-a",
			csID:     "cs-cluster-abc",
			want:     "https://oidc.example.com/tenant-a/cs-cluster-abc",
		},
		{
			name:     "keeps existing trailing slash on base URL",
			baseURL:  "https://oidc.example.com/",
			tenantID: "tenant-a",
			csID:     "cs-cluster-abc",
			want:     "https://oidc.example.com/tenant-a/cs-cluster-abc",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			syncer := &dataPlaneOIDCFederationSyncer{oidcIssuerBaseURL: tc.baseURL}
			assert.Equal(t, tc.want, syncer.generateClusterOIDCIssuerURL(tc.tenantID, tc.csID))
		})
	}
}

func TestDataPlaneOIDCFederationSyncOnceUsesClusterSubscriptionTenantForIssuer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())

	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()

	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorPending()),
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	fakeClient := &fakeFederatedIdentityCredentialsClient{}
	ctrl := gomock.NewController(t)
	smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
	smiClientBuilder.EXPECT().
		FederatedIdentityCredentialsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(fakeClient, nil)

	syncer := &dataPlaneOIDCFederationSyncer{
		clock:                         clocktesting.NewFakePassiveClock(now),
		clusterLister:                 &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		subscriptionLister:            testOIDCFederationSubscriptionLister(),
		serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:             mockResourcesDB,
		smiClientBuilder:              smiClientBuilder,
		fpaMIdataplaneClientBuilder:   testSMIDataplaneBuilder(serviceManagedIdentity, true),
		clusterScopedIdentitiesConfig: testDataPlaneOIDCFederationIdentitiesConfig(),
		oidcIssuerBaseURL:             testOIDCIssuerBaseURL,
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.NoError(t, err)
	require.NotEmpty(t, fakeClient.creates)
	for _, call := range fakeClient.creates {
		assert.Equal(t, testOIDCIssuerURL, ptr.Deref(call.credential.Properties.Issuer, ""))
	}
}

func TestDataPlaneOIDCFederationSyncOnceConfiguresFICsForOperatorServiceAccounts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())

	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()

	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorPending()),
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	fakeClient := &fakeFederatedIdentityCredentialsClient{}
	ctrl := gomock.NewController(t)
	smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
	smiClientBuilder.EXPECT().
		FederatedIdentityCredentialsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(fakeClient, nil)

	syncer := &dataPlaneOIDCFederationSyncer{
		clock:                         clocktesting.NewFakePassiveClock(now),
		clusterLister:                 &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		subscriptionLister:            testOIDCFederationSubscriptionLister(),
		serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:             mockResourcesDB,
		smiClientBuilder:              smiClientBuilder,
		fpaMIdataplaneClientBuilder:   testSMIDataplaneBuilder(serviceManagedIdentity, true),
		clusterScopedIdentitiesConfig: testDataPlaneOIDCFederationIdentitiesConfig(),
		oidcIssuerBaseURL:             testOIDCIssuerBaseURL,
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.NoError(t, err)

	serviceAccounts := diskCSIDriverServiceAccounts(t)
	require.Len(t, serviceAccounts, 2)
	assert.Len(t, fakeClient.creates, 2)

	expectedNames := []string{
		federatedidentitycredential.GenerateFederatedIdentityCredentialName(testCSClusterID, testDiskCSIOperator, testDiskCSINamespace, "azure-disk-csi-driver-controller-sa"),
		federatedidentitycredential.GenerateFederatedIdentityCredentialName(testCSClusterID, testDiskCSIOperator, testDiskCSINamespace, "azure-disk-csi-driver-operator"),
	}
	assert.ElementsMatch(t, expectedNames, createdFICNames(fakeClient.creates))

	createdByName := map[string]recordedFICCall{}
	for _, call := range fakeClient.creates {
		createdByName[call.credentialName] = call
		assert.Equal(t, testOIDCIssuerURL, ptr.Deref(call.credential.Properties.Issuer, ""))
		assert.Equal(t, []string{dataPlaneOIDCFederationAudience}, derefStrings(call.credential.Properties.Audiences))
	}
	assert.Equal(t, "system:serviceaccount:openshift-cluster-csi-drivers:azure-disk-csi-driver-operator", ptr.Deref(createdByName[expectedNames[1]].credential.Properties.Subject, ""))
	assert.Equal(t, "system:serviceaccount:openshift-cluster-csi-drivers:azure-disk-csi-driver-controller-sa", ptr.Deref(createdByName[expectedNames[0]].credential.Properties.Subject, ""))

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	got := updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyA]
	require.NotNil(t, got)
	assert.True(t, got.TargetIdentityEnsured(), "expected TargetIdentity to be ensured")
	gotOperator := requireOperatorStatus(t, got, testDiskCSIOperator)
	assert.Empty(t, gotOperator.PendingAzureResources)
	assert.ElementsMatch(t, resourceIDStrings(expectedFICResourceIDs(t, identityA, testDiskCSIOperator, serviceAccounts)), resourceIDStrings(gotOperator.AzureResources))
	assertOIDCFederationRecheckScheduled(t, updated, now)
}

func TestDataPlaneOIDCFederationSyncOnceConfiguresFICsForMultipleOperatorsSharingIdentity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())

	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
		testImageRegistryOp: identityA,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()

	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: oidcIdentityStatusOperators(testOIDCFederationIdentityA, map[string]*coreapi.DataplaneOIDCFederationOperatorStatus{
			testDiskCSIOperator: oidcOperatorPending(),
			testImageRegistryOp: oidcOperatorPending(),
		}),
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	fakeClient := &fakeFederatedIdentityCredentialsClient{}
	ctrl := gomock.NewController(t)
	smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
	smiClientBuilder.EXPECT().
		FederatedIdentityCredentialsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(fakeClient, nil)

	syncer := &dataPlaneOIDCFederationSyncer{
		clock:                         clocktesting.NewFakePassiveClock(now),
		clusterLister:                 &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		subscriptionLister:            testOIDCFederationSubscriptionLister(),
		serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:             mockResourcesDB,
		smiClientBuilder:              smiClientBuilder,
		fpaMIdataplaneClientBuilder:   testSMIDataplaneBuilder(serviceManagedIdentity, true),
		clusterScopedIdentitiesConfig: testDataPlaneOIDCFederationIdentitiesConfig(),
		oidcIssuerBaseURL:             testOIDCIssuerBaseURL,
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.NoError(t, err)

	diskSAs := diskCSIDriverServiceAccounts(t)
	imageSAs := imageRegistryServiceAccounts(t)
	assert.Len(t, fakeClient.creates, len(diskSAs)+len(imageSAs))

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	got := updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyA]
	require.NotNil(t, got)
	assert.True(t, got.TargetIdentityEnsured(), "expected TargetIdentity to be ensured")
	diskOperator := requireOperatorStatus(t, got, testDiskCSIOperator)
	imageOperator := requireOperatorStatus(t, got, testImageRegistryOp)
	assert.Empty(t, diskOperator.PendingAzureResources)
	assert.Empty(t, imageOperator.PendingAzureResources)
	assert.ElementsMatch(t, resourceIDStrings(expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskSAs)), resourceIDStrings(diskOperator.AzureResources))
	assert.ElementsMatch(t, resourceIDStrings(expectedFICResourceIDs(t, identityA, testImageRegistryOp, imageSAs)), resourceIDStrings(imageOperator.AzureResources))
}

func TestDataPlaneOIDCFederationSyncOnceDeconfiguresFICsForOperatorServiceAccounts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())

	tracked := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))
	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()

	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorDeconfigure(&metav1.Time{Time: now.Add(-dataPlaneOIDCFederationDeconfigureDelay)}, tracked, nil)),
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	fakeClient := &fakeFederatedIdentityCredentialsClient{}
	ctrl := gomock.NewController(t)
	smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
	smiClientBuilder.EXPECT().
		FederatedIdentityCredentialsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(fakeClient, nil)

	syncer := &dataPlaneOIDCFederationSyncer{
		clock:                         clocktesting.NewFakePassiveClock(now),
		clusterLister:                 &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		subscriptionLister:            testOIDCFederationSubscriptionLister(),
		serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:             mockResourcesDB,
		smiClientBuilder:              smiClientBuilder,
		fpaMIdataplaneClientBuilder:   testSMIDataplaneBuilder(serviceManagedIdentity, true),
		clusterScopedIdentitiesConfig: testDataPlaneOIDCFederationIdentitiesConfig(),
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.NoError(t, err)
	assert.Len(t, fakeClient.deletes, len(tracked))

	expectedNames := []string{
		federatedidentitycredential.GenerateFederatedIdentityCredentialName(testCSClusterID, testDiskCSIOperator, testDiskCSINamespace, "azure-disk-csi-driver-controller-sa"),
		federatedidentitycredential.GenerateFederatedIdentityCredentialName(testCSClusterID, testDiskCSIOperator, testDiskCSINamespace, "azure-disk-csi-driver-operator"),
	}
	assert.ElementsMatch(t, expectedNames, createdFICNames(fakeClient.deletes))

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	got := updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyA]
	assert.Nil(t, got)
	assert.Empty(t, updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation)
	assert.Nil(t, updated.Spec.EarliestRecheckTimesByController[DataPlaneOIDCFederationControllerName])
}

func TestDataPlaneOIDCFederationSyncOnceDeconfigureHonorsLiveClusterDelay(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	since := metav1.NewTime(now)
	deletionTimestamp := metav1.NewTime(now)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())
	tracked := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))

	testCases := []struct {
		name              string
		deleting          bool
		wantDeletes       bool
		wantRemoved       bool
		wantAzureResource bool
	}{
		{
			name:              "live cluster waits 24h after DeconfigureTimestamp",
			wantAzureResource: true,
		},
		{
			name:        "cluster deletion deconfigures immediately",
			deleting:    true,
			wantDeletes: true,
			wantRemoved: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, nil)
			cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()
			if tc.deleting {
				cluster.ServiceProviderProperties.DeletionTimestamp = &deletionTimestamp
			}

			serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
			serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorDeconfigure(&since, tracked, nil)),
			}

			mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
			require.NoError(t, err)

			fakeClient := &fakeFederatedIdentityCredentialsClient{}
			ctrl := gomock.NewController(t)
			smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
			if tc.wantDeletes {
				smiClientBuilder.EXPECT().
					FederatedIdentityCredentialsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					Return(fakeClient, nil)
			} else {
				smiClientBuilder.EXPECT().
					FederatedIdentityCredentialsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					Times(0)
			}

			syncer := &dataPlaneOIDCFederationSyncer{
				clock:                        clocktesting.NewFakePassiveClock(now),
				clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
				subscriptionLister:           testOIDCFederationSubscriptionLister(),
				serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
				resourcesDBClient:            mockResourcesDB,
				smiClientBuilder:             smiClientBuilder,
				fpaMIdataplaneClientBuilder:  testSMIDataplaneBuilder(serviceManagedIdentity, true),
			}

			err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    testClusterName,
			})
			require.NoError(t, err)
			if tc.wantDeletes {
				assert.Len(t, fakeClient.deletes, len(tracked))
			} else {
				assert.Empty(t, fakeClient.deletes)
			}

			updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			got := updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyA]
			if tc.wantRemoved {
				assert.Nil(t, got)
				assert.Empty(t, updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation)
				return
			}
			require.NotNil(t, got)
			gotOperator := requireOperatorStatus(t, got, testDiskCSIOperator)
			assert.True(t, gotOperator.DeconfigureTimestamp != nil, "expected deconfigure requested")
			if tc.wantAzureResource {
				assert.ElementsMatch(t, resourceIDStrings(tracked), resourceIDStrings(gotOperator.AzureResources))
			} else {
				assert.Empty(t, gotOperator.AzureResources)
			}
		})
	}
}

func TestDataPlaneOIDCFederationSyncOnceDeconfiguresTrackedFICsWhenIdentityNoLongerAssigned(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())

	tracked := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))
	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, nil)
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()

	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorDeconfigure(&metav1.Time{Time: now.Add(-dataPlaneOIDCFederationDeconfigureDelay)}, tracked, nil)),
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	fakeClient := &fakeFederatedIdentityCredentialsClient{}
	ctrl := gomock.NewController(t)
	smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
	smiClientBuilder.EXPECT().
		FederatedIdentityCredentialsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(fakeClient, nil)

	syncer := &dataPlaneOIDCFederationSyncer{
		clock:                         clocktesting.NewFakePassiveClock(now),
		clusterLister:                 &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		subscriptionLister:            testOIDCFederationSubscriptionLister(),
		serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:             mockResourcesDB,
		smiClientBuilder:              smiClientBuilder,
		fpaMIdataplaneClientBuilder:   testSMIDataplaneBuilder(serviceManagedIdentity, true),
		clusterScopedIdentitiesConfig: testDataPlaneOIDCFederationIdentitiesConfig(),
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.NoError(t, err)
	assert.Len(t, fakeClient.deletes, len(tracked))

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	got := updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyA]
	assert.Nil(t, got)
	assert.Empty(t, updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation)
}

func TestDataPlaneOIDCFederationSyncOnceConfigureErrorIsReturned(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())

	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()

	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorPending()),
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	fakeClient := &fakeFederatedIdentityCredentialsClient{
		createOrUpdateErr: errors.New("simulated azure create failure"),
	}
	ctrl := gomock.NewController(t)
	smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
	smiClientBuilder.EXPECT().
		FederatedIdentityCredentialsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(fakeClient, nil)

	syncer := &dataPlaneOIDCFederationSyncer{
		clock:                         clocktesting.NewFakePassiveClock(now),
		clusterLister:                 &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		subscriptionLister:            testOIDCFederationSubscriptionLister(),
		serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:             mockResourcesDB,
		smiClientBuilder:              smiClientBuilder,
		fpaMIdataplaneClientBuilder:   testSMIDataplaneBuilder(serviceManagedIdentity, true),
		clusterScopedIdentitiesConfig: testDataPlaneOIDCFederationIdentitiesConfig(),
		oidcIssuerBaseURL:             testOIDCIssuerBaseURL,
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "simulated azure create failure")
	assert.Len(t, fakeClient.creates, len(diskCSIDriverServiceAccounts(t)))

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	got := updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyA]
	require.NotNil(t, got)
	assert.False(t, got.TargetIdentityEnsured(), "expected TargetIdentity not yet ensured")
	gotOperator := requireOperatorStatus(t, got, testDiskCSIOperator)
	assert.Nil(t, gotOperator.DeconfigureTimestamp)
	assert.Empty(t, gotOperator.AzureResources)
	assert.ElementsMatch(t, resourceIDStrings(expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))), resourceIDStrings(gotOperator.PendingAzureResources))
}

func TestDataPlaneOIDCFederationSyncOnceConfigurePersistsPartialAzureSuccess(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())

	serviceAccounts := diskCSIDriverServiceAccounts(t)
	require.GreaterOrEqual(t, len(serviceAccounts), 2)
	failingSA := serviceAccounts[len(serviceAccounts)-1]
	failingName := federatedidentitycredential.GenerateFederatedIdentityCredentialName(
		testCSClusterID, testDiskCSIOperator, failingSA.Namespace, failingSA.Name,
	)
	succeedingIDs := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, serviceAccounts[:len(serviceAccounts)-1])
	failingIDs := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, []*azure.KubernetesServiceAccount{failingSA})

	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()

	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorPending()),
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	fakeClient := &fakeFederatedIdentityCredentialsClient{
		createOrUpdateErrs: map[string]error{
			failingName: errors.New("simulated azure create failure for one fic"),
		},
	}
	ctrl := gomock.NewController(t)
	smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
	smiClientBuilder.EXPECT().
		FederatedIdentityCredentialsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(fakeClient, nil)

	syncer := &dataPlaneOIDCFederationSyncer{
		clock:                         clocktesting.NewFakePassiveClock(now),
		clusterLister:                 &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		subscriptionLister:            testOIDCFederationSubscriptionLister(),
		serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:             mockResourcesDB,
		smiClientBuilder:              smiClientBuilder,
		fpaMIdataplaneClientBuilder:   testSMIDataplaneBuilder(serviceManagedIdentity, true),
		clusterScopedIdentitiesConfig: testDataPlaneOIDCFederationIdentitiesConfig(),
		oidcIssuerBaseURL:             testOIDCIssuerBaseURL,
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "simulated azure create failure for one fic")
	assert.Len(t, fakeClient.creates, len(serviceAccounts))

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	got := updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyA]
	require.NotNil(t, got)
	assert.False(t, got.TargetIdentityEnsured(), "expected TargetIdentity not yet ensured")
	gotOperator := requireOperatorStatus(t, got, testDiskCSIOperator)
	assert.Nil(t, gotOperator.DeconfigureTimestamp)
	assert.Nil(t, updated.Spec.EarliestRecheckTimesByController[DataPlaneOIDCFederationControllerName])
	assert.ElementsMatch(t, resourceIDStrings(succeedingIDs), resourceIDStrings(gotOperator.AzureResources))
	assert.ElementsMatch(t, resourceIDStrings(failingIDs), resourceIDStrings(gotOperator.PendingAzureResources))
}

func TestDataPlaneOIDCFederationSyncOnceConfigureContinuesDeletesAfterCreateFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())

	desired := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))
	extra, err := federatedidentitycredential.GenerateFederatedIdentityCredentialResourceID(
		identityA,
		testCSClusterID,
		testImageRegistryOp,
		"openshift-image-registry",
		"cluster-image-registry-operator",
	)
	require.NoError(t, err)
	tracked := append(append([]*azcorearm.ResourceID{}, desired...), extra)

	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorEnsured(testOIDCFederationIdentityA, tracked)),
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	fakeClient := &fakeFederatedIdentityCredentialsClient{
		createOrUpdateErr: errors.New("simulated azure create failure"),
	}
	ctrl := gomock.NewController(t)
	smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
	smiClientBuilder.EXPECT().
		FederatedIdentityCredentialsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(fakeClient, nil)

	syncer := &dataPlaneOIDCFederationSyncer{
		clock:                         clocktesting.NewFakePassiveClock(now),
		clusterLister:                 &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		subscriptionLister:            testOIDCFederationSubscriptionLister(),
		serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:             mockResourcesDB,
		smiClientBuilder:              smiClientBuilder,
		fpaMIdataplaneClientBuilder:   testSMIDataplaneBuilder(serviceManagedIdentity, true),
		clusterScopedIdentitiesConfig: testDataPlaneOIDCFederationIdentitiesConfig(),
		oidcIssuerBaseURL:             testOIDCIssuerBaseURL,
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "simulated azure create failure")
	assert.Len(t, fakeClient.creates, len(desired))
	require.Len(t, fakeClient.deletes, 1)
	assert.Equal(t, extra.Name, fakeClient.deletes[0].credentialName)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	got := updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyA]
	require.NotNil(t, got)
	assert.True(t, got.TargetIdentityEnsured(), "expected TargetIdentity to be ensured")
	gotOperator := requireOperatorStatus(t, got, testDiskCSIOperator)
	assert.Nil(t, updated.Spec.EarliestRecheckTimesByController[DataPlaneOIDCFederationControllerName])
	assert.Empty(t, gotOperator.PendingAzureResources)
	assert.ElementsMatch(t, resourceIDStrings(desired), resourceIDStrings(gotOperator.AzureResources))
}

func TestDataPlaneOIDCFederationSyncOnceDeconfigurePersistsPartialAzureDeletes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())

	tracked := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))
	require.GreaterOrEqual(t, len(tracked), 2)
	failingName := tracked[0].Name
	remaining := []*azcorearm.ResourceID{tracked[0]}

	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, nil)
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorDeconfigure(&metav1.Time{Time: now.Add(-dataPlaneOIDCFederationDeconfigureDelay)}, tracked, nil)),
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	fakeClient := &fakeFederatedIdentityCredentialsClient{
		deleteErrs: map[string]error{
			failingName: errors.New("simulated azure delete failure for one fic"),
		},
	}
	ctrl := gomock.NewController(t)
	smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
	smiClientBuilder.EXPECT().
		FederatedIdentityCredentialsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(fakeClient, nil)

	syncer := &dataPlaneOIDCFederationSyncer{
		clock:                         clocktesting.NewFakePassiveClock(now),
		clusterLister:                 &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		subscriptionLister:            testOIDCFederationSubscriptionLister(),
		serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:             mockResourcesDB,
		smiClientBuilder:              smiClientBuilder,
		fpaMIdataplaneClientBuilder:   testSMIDataplaneBuilder(serviceManagedIdentity, true),
		clusterScopedIdentitiesConfig: testDataPlaneOIDCFederationIdentitiesConfig(),
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "simulated azure delete failure for one fic")
	assert.Len(t, fakeClient.deletes, len(tracked))

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	got := updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyA]
	require.NotNil(t, got)
	gotOperator := requireOperatorStatus(t, got, testDiskCSIOperator)
	assert.True(t, gotOperator.DeconfigureTimestamp != nil, "expected deconfigure requested")
	assert.Empty(t, gotOperator.PendingAzureResources)
	assert.ElementsMatch(t, resourceIDStrings(remaining), resourceIDStrings(gotOperator.AzureResources))
}

func TestDataPlaneOIDCFederationSyncOncePersistsPendingBeforeConfigureClientBuildFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())

	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()

	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorPending()),
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	ctrl := gomock.NewController(t)
	smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
	smiClientBuilder.EXPECT().
		FederatedIdentityCredentialsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, errors.New("simulated fic client build failure"))

	syncer := &dataPlaneOIDCFederationSyncer{
		clock:                         clocktesting.NewFakePassiveClock(now),
		clusterLister:                 &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		subscriptionLister:            testOIDCFederationSubscriptionLister(),
		serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:             mockResourcesDB,
		smiClientBuilder:              smiClientBuilder,
		fpaMIdataplaneClientBuilder:   testSMIDataplaneBuilder(serviceManagedIdentity, true),
		clusterScopedIdentitiesConfig: testDataPlaneOIDCFederationIdentitiesConfig(),
		oidcIssuerBaseURL:             testOIDCIssuerBaseURL,
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "simulated fic client build failure")

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	got := updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyA]
	require.NotNil(t, got)
	assert.False(t, got.TargetIdentityEnsured(), "expected TargetIdentity not yet ensured")
	gotOperator := requireOperatorStatus(t, got, testDiskCSIOperator)
	assert.Nil(t, gotOperator.DeconfigureTimestamp)
	assert.ElementsMatch(t, resourceIDStrings(expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))), resourceIDStrings(gotOperator.PendingAzureResources))
}

func TestDataPlaneOIDCFederationSyncOnceDeconfigureAzureFailureLeavesTrackedResources(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())

	tracked := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))
	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, nil)
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorDeconfigure(&metav1.Time{Time: now.Add(-dataPlaneOIDCFederationDeconfigureDelay)}, tracked, nil)),
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	ctrl := gomock.NewController(t)
	smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
	smiClientBuilder.EXPECT().
		FederatedIdentityCredentialsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Times(0)

	syncer := &dataPlaneOIDCFederationSyncer{
		clock:                        clocktesting.NewFakePassiveClock(now),
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		subscriptionLister:           testOIDCFederationSubscriptionLister(),
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:            mockResourcesDB,
		smiClientBuilder:             smiClientBuilder,
		fpaMIdataplaneClientBuilder: &fakeFPAMIDataplaneClientBuilder{
			buildErr: errors.New("simulated mi dataplane build failure"),
		},
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "simulated mi dataplane build failure")

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	got := updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyA]
	require.NotNil(t, got)
	gotOperator := requireOperatorStatus(t, got, testDiskCSIOperator)
	assert.True(t, gotOperator.DeconfigureTimestamp != nil, "expected deconfigure requested")
	assert.Empty(t, gotOperator.PendingAzureResources)
	assert.ElementsMatch(t, resourceIDStrings(tracked), resourceIDStrings(gotOperator.AzureResources))
}

func TestDataPlaneOIDCFederationSyncOnceSkipsConfigureWhenClusterServiceIDMissing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())

	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorPending()),
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	ctrl := gomock.NewController(t)
	smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
	smiClientBuilder.EXPECT().
		FederatedIdentityCredentialsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Times(0)

	syncer := &dataPlaneOIDCFederationSyncer{
		clock:                         clocktesting.NewFakePassiveClock(now),
		clusterLister:                 &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		subscriptionLister:            testOIDCFederationSubscriptionLister(),
		serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:             mockResourcesDB,
		smiClientBuilder:              smiClientBuilder,
		fpaMIdataplaneClientBuilder:   testSMIDataplaneBuilder(serviceManagedIdentity, true),
		clusterScopedIdentitiesConfig: testDataPlaneOIDCFederationIdentitiesConfig(),
		oidcIssuerBaseURL:             testOIDCIssuerBaseURL,
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.NoError(t, err)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	got := updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyA]
	require.NotNil(t, got)
	assert.False(t, got.TargetIdentityEnsured(), "expected TargetIdentity not yet ensured")
	gotOperator := requireOperatorStatus(t, got, testDiskCSIOperator)
	assert.Nil(t, gotOperator.DeconfigureTimestamp)
}

func TestDataPlaneOIDCFederationSyncOnceSkipsConfigureWhileClusterDeleting(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	deletionTimestamp := metav1.NewTime(now)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())
	tracked := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))

	testCases := []struct {
		name    string
		ensured bool
	}{
		{
			name: "not yet ensured",
		},
		{
			name:    "already ensured",
			ensured: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
				testDiskCSIOperator: identityA,
			})
			cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()
			cluster.ServiceProviderProperties.DeletionTimestamp = &deletionTimestamp

			operatorStatus := oidcOperatorPending()
			if tc.ensured {
				operatorStatus = oidcOperatorEnsured(testOIDCFederationIdentityA, tracked)
			}
			status := oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, operatorStatus)
			serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
			serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: status,
			}

			mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
			require.NoError(t, err)

			ctrl := gomock.NewController(t)
			smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
			smiClientBuilder.EXPECT().
				FederatedIdentityCredentialsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Times(0)

			syncer := &dataPlaneOIDCFederationSyncer{
				clock:                         clocktesting.NewFakePassiveClock(now),
				clusterLister:                 &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
				subscriptionLister:            testOIDCFederationSubscriptionLister(),
				serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
				resourcesDBClient:             mockResourcesDB,
				smiClientBuilder:              smiClientBuilder,
				fpaMIdataplaneClientBuilder:   testSMIDataplaneBuilder(serviceManagedIdentity, true),
				clusterScopedIdentitiesConfig: testDataPlaneOIDCFederationIdentitiesConfig(),
				oidcIssuerBaseURL:             testOIDCIssuerBaseURL,
			}

			err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    testClusterName,
			})
			require.NoError(t, err)

			updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			got := updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyA]
			require.NotNil(t, got)
			gotOperator := requireOperatorStatus(t, got, testDiskCSIOperator)
			assert.Equal(t, tc.ensured, got.TargetIdentityEnsured())
			assert.Empty(t, gotOperator.PendingAzureResources)
			if tc.ensured {
				assert.ElementsMatch(t, resourceIDStrings(tracked), resourceIDStrings(gotOperator.AzureResources))
			} else {
				assert.Empty(t, gotOperator.AzureResources)
			}
		})
	}
}

func TestDataPlaneOIDCFederationSyncOnceSkipsConfigureButDeconfiguresWhileClusterDeleting(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	deletionTimestamp := metav1.NewTime(now)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	identityB := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-b"))
	keyA := strings.ToLower(identityA.String())
	keyB := strings.ToLower(identityB.String())
	trackedB := expectedFICResourceIDs(t, identityB, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))

	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()
	cluster.ServiceProviderProperties.DeletionTimestamp = &deletionTimestamp

	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorPending()),
		keyB: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorDeconfigure(&deletionTimestamp, trackedB, nil)),
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	fakeClient := &fakeFederatedIdentityCredentialsClient{}
	ctrl := gomock.NewController(t)
	smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
	smiClientBuilder.EXPECT().
		FederatedIdentityCredentialsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(fakeClient, nil)

	syncer := &dataPlaneOIDCFederationSyncer{
		clock:                         clocktesting.NewFakePassiveClock(now),
		clusterLister:                 &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		subscriptionLister:            testOIDCFederationSubscriptionLister(),
		serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:             mockResourcesDB,
		smiClientBuilder:              smiClientBuilder,
		fpaMIdataplaneClientBuilder:   testSMIDataplaneBuilder(serviceManagedIdentity, true),
		clusterScopedIdentitiesConfig: testDataPlaneOIDCFederationIdentitiesConfig(),
		oidcIssuerBaseURL:             testOIDCIssuerBaseURL,
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.NoError(t, err)
	assert.Empty(t, fakeClient.creates)
	assert.Len(t, fakeClient.deletes, len(trackedB))

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	gotA := updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyA]
	require.NotNil(t, gotA)
	assert.False(t, gotA.TargetIdentityEnsured(), "expected TargetIdentity not yet ensured")
	gotAOperator := requireOperatorStatus(t, gotA, testDiskCSIOperator)
	assert.Nil(t, gotAOperator.DeconfigureTimestamp)
	assert.Empty(t, gotAOperator.PendingAzureResources)
	_, hasB := updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyB]
	assert.False(t, hasB)
}

func TestDataPlaneOIDCFederationSyncOnceDeconfiguresTrackedFICsWhenClusterServiceIDMissing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())

	tracked := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))
	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, nil)
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorDeconfigure(&metav1.Time{Time: now.Add(-dataPlaneOIDCFederationDeconfigureDelay)}, tracked, nil)),
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	fakeClient := &fakeFederatedIdentityCredentialsClient{}
	ctrl := gomock.NewController(t)
	smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
	smiClientBuilder.EXPECT().
		FederatedIdentityCredentialsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(fakeClient, nil)

	syncer := &dataPlaneOIDCFederationSyncer{
		clock:                        clocktesting.NewFakePassiveClock(now),
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		subscriptionLister:           testOIDCFederationSubscriptionLister(),
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:            mockResourcesDB,
		smiClientBuilder:             smiClientBuilder,
		fpaMIdataplaneClientBuilder:  testSMIDataplaneBuilder(serviceManagedIdentity, true),
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.NoError(t, err)
	assert.Len(t, fakeClient.deletes, len(tracked))
	assert.Empty(t, fakeClient.creates)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	got := updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyA]
	assert.Nil(t, got)
	assert.Empty(t, updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation)
}

func TestDataPlaneOIDCFederationSyncOnceDeconfiguresWhenServiceManagedIdentityMissingInAzure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())

	tracked := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))
	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, nil)
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorDeconfigure(&metav1.Time{Time: now.Add(-dataPlaneOIDCFederationDeconfigureDelay)}, tracked, nil)),
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	ctrl := gomock.NewController(t)
	smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
	smiClientBuilder.EXPECT().
		FederatedIdentityCredentialsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Times(0)

	syncer := &dataPlaneOIDCFederationSyncer{
		clock:                        clocktesting.NewFakePassiveClock(now),
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		subscriptionLister:           testOIDCFederationSubscriptionLister(),
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:            mockResourcesDB,
		smiClientBuilder:             smiClientBuilder,
		fpaMIdataplaneClientBuilder:  testSMIDataplaneBuilder(serviceManagedIdentity, false),
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.NoError(t, err)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	got := updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyA]
	assert.Nil(t, got)
	assert.Empty(t, updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation)
}

func TestDataPlaneOIDCFederationSyncOnceNilServiceManagedIdentity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())

	cluster := newTestClusterWithIdentities(t, testClusterName, nil, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorPending()),
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	ctrl := gomock.NewController(t)
	smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
	smiClientBuilder.EXPECT().
		FederatedIdentityCredentialsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Times(0)

	syncer := &dataPlaneOIDCFederationSyncer{
		clock:                        clocktesting.NewFakePassiveClock(now),
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		subscriptionLister:           testOIDCFederationSubscriptionLister(),
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:            mockResourcesDB,
		smiClientBuilder:             smiClientBuilder,
		oidcIssuerBaseURL:            testOIDCIssuerBaseURL,
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ServiceManagedIdentity is nil")
}

func TestDataPlaneOIDCFederationNeedsWorkConfiguredDesiredSetChanged(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	future := metav1.NewTime(now.Add(time.Hour))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())

	cluster := newTestClusterWithIdentities(t, testClusterName, nil, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()

	syncer := &dataPlaneOIDCFederationSyncer{
		clock:                         clocktesting.NewFakePassiveClock(now),
		clusterScopedIdentitiesConfig: testDataPlaneOIDCFederationIdentitiesConfig(),
	}
	serviceProviderCluster := &coreapi.ServiceProviderCluster{}
	serviceProviderCluster.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{
		DataPlaneOIDCFederationControllerName: &future,
	}
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorEnsured(testOIDCFederationIdentityA, nil)),
	}
	assert.True(t, syncer.needsWork(cluster, serviceProviderCluster), "desired FIC set differs from empty AzureResources")

	cluster.ServiceProviderProperties.DeletionTimestamp = &future
	assert.False(t, syncer.needsWork(cluster, serviceProviderCluster), "Configured desired-set drift is skipped while the cluster is being deleted")
}

func TestDataPlaneOIDCFederationSyncOnceConfiguredGetMatchingDoesNotCreate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())

	tracked := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))
	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorEnsured(testOIDCFederationIdentityA, tracked)),
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	fakeClient := &fakeFederatedIdentityCredentialsClient{existing: matchingDiskCSIFICs(t, identityA)}
	ctrl := gomock.NewController(t)
	smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
	smiClientBuilder.EXPECT().
		FederatedIdentityCredentialsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(fakeClient, nil)

	syncer := &dataPlaneOIDCFederationSyncer{
		clock:                         clocktesting.NewFakePassiveClock(now),
		clusterLister:                 &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		subscriptionLister:            testOIDCFederationSubscriptionLister(),
		serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:             mockResourcesDB,
		smiClientBuilder:              smiClientBuilder,
		fpaMIdataplaneClientBuilder:   testSMIDataplaneBuilder(serviceManagedIdentity, true),
		clusterScopedIdentitiesConfig: testDataPlaneOIDCFederationIdentitiesConfig(),
		oidcIssuerBaseURL:             testOIDCIssuerBaseURL,
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.NoError(t, err)
	assert.Len(t, fakeClient.gets, len(tracked))
	assert.Empty(t, fakeClient.creates)
	assert.Empty(t, fakeClient.deletes)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	got := updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyA]
	require.NotNil(t, got)
	assert.True(t, got.TargetIdentityEnsured(), "expected TargetIdentity to be ensured")
	assertOIDCFederationRecheckScheduled(t, updated, now)
}

func TestDataPlaneOIDCFederationSyncOnceConfiguredCreateOrUpdateWhenGetNotFound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())

	tracked := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))
	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorEnsured(testOIDCFederationIdentityA, tracked)),
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	fakeClient := &fakeFederatedIdentityCredentialsClient{}
	ctrl := gomock.NewController(t)
	smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
	smiClientBuilder.EXPECT().
		FederatedIdentityCredentialsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(fakeClient, nil)

	syncer := &dataPlaneOIDCFederationSyncer{
		clock:                         clocktesting.NewFakePassiveClock(now),
		clusterLister:                 &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		subscriptionLister:            testOIDCFederationSubscriptionLister(),
		serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:             mockResourcesDB,
		smiClientBuilder:              smiClientBuilder,
		fpaMIdataplaneClientBuilder:   testSMIDataplaneBuilder(serviceManagedIdentity, true),
		clusterScopedIdentitiesConfig: testDataPlaneOIDCFederationIdentitiesConfig(),
		oidcIssuerBaseURL:             testOIDCIssuerBaseURL,
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.NoError(t, err)
	assert.Len(t, fakeClient.creates, len(tracked))
}

func TestDataPlaneOIDCFederationSyncOnceConfiguredCreateOrUpdateWhenFieldsDrifted(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())

	tracked := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))
	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorEnsured(testOIDCFederationIdentityA, tracked)),
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	existing := matchingDiskCSIFICs(t, identityA)
	for name, cred := range existing {
		cred.Properties.Issuer = ptr.To("https://oidc.example.com/other-tenant/other-cluster")
		existing[name] = cred
	}
	fakeClient := &fakeFederatedIdentityCredentialsClient{existing: existing}
	ctrl := gomock.NewController(t)
	smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
	smiClientBuilder.EXPECT().
		FederatedIdentityCredentialsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(fakeClient, nil)

	syncer := &dataPlaneOIDCFederationSyncer{
		clock:                         clocktesting.NewFakePassiveClock(now),
		clusterLister:                 &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		subscriptionLister:            testOIDCFederationSubscriptionLister(),
		serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:             mockResourcesDB,
		smiClientBuilder:              smiClientBuilder,
		fpaMIdataplaneClientBuilder:   testSMIDataplaneBuilder(serviceManagedIdentity, true),
		clusterScopedIdentitiesConfig: testDataPlaneOIDCFederationIdentitiesConfig(),
		oidcIssuerBaseURL:             testOIDCIssuerBaseURL,
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.NoError(t, err)
	assert.Len(t, fakeClient.creates, len(tracked))
}

func TestDataPlaneOIDCFederationSyncOnceConfiguredFutureRecheckSkipsAzureWhenSetUnchanged(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	future := metav1.NewTime(now.Add(time.Hour))

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())

	tracked := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))
	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{
		DataPlaneOIDCFederationControllerName: &future,
	}
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorEnsured(testOIDCFederationIdentityA, tracked)),
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	ctrl := gomock.NewController(t)
	smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
	smiClientBuilder.EXPECT().
		FederatedIdentityCredentialsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Times(0)

	syncer := &dataPlaneOIDCFederationSyncer{
		clock:                         clocktesting.NewFakePassiveClock(now),
		clusterLister:                 &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		subscriptionLister:            testOIDCFederationSubscriptionLister(),
		serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:             mockResourcesDB,
		smiClientBuilder:              smiClientBuilder,
		clusterScopedIdentitiesConfig: testDataPlaneOIDCFederationIdentitiesConfig(),
		oidcIssuerBaseURL:             testOIDCIssuerBaseURL,
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.NoError(t, err)
}

func TestDataPlaneOIDCFederationSyncOnceConfiguredDeletesTrackedFICsNoLongerDesired(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())

	desired := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))
	extra, err := federatedidentitycredential.GenerateFederatedIdentityCredentialResourceID(
		identityA,
		testCSClusterID,
		testImageRegistryOp,
		"openshift-image-registry",
		"cluster-image-registry-operator",
	)
	require.NoError(t, err)
	tracked := append(append([]*azcorearm.ResourceID{}, desired...), extra)

	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorEnsured(testOIDCFederationIdentityA, tracked)),
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	fakeClient := &fakeFederatedIdentityCredentialsClient{existing: matchingDiskCSIFICs(t, identityA)}
	ctrl := gomock.NewController(t)
	smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
	smiClientBuilder.EXPECT().
		FederatedIdentityCredentialsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(fakeClient, nil)

	syncer := &dataPlaneOIDCFederationSyncer{
		clock:                         clocktesting.NewFakePassiveClock(now),
		clusterLister:                 &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		subscriptionLister:            testOIDCFederationSubscriptionLister(),
		serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:             mockResourcesDB,
		smiClientBuilder:              smiClientBuilder,
		fpaMIdataplaneClientBuilder:   testSMIDataplaneBuilder(serviceManagedIdentity, true),
		clusterScopedIdentitiesConfig: testDataPlaneOIDCFederationIdentitiesConfig(),
		oidcIssuerBaseURL:             testOIDCIssuerBaseURL,
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.NoError(t, err)
	assert.Empty(t, fakeClient.creates)
	require.Len(t, fakeClient.deletes, 1)
	assert.Equal(t, extra.Name, fakeClient.deletes[0].credentialName)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	got := updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyA]
	require.NotNil(t, got)
	gotOperator := requireOperatorStatus(t, got, testDiskCSIOperator)
	assert.ElementsMatch(t, resourceIDStrings(desired), resourceIDStrings(gotOperator.AzureResources))
	assertOIDCFederationRecheckScheduled(t, updated, now)
}

func TestDataPlaneOIDCFederationSyncOnceReconcilesConfiguredWhenPendingConfigureRuns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	future := metav1.NewTime(now.Add(time.Hour))

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	identityB := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-b"))
	keyA := strings.ToLower(identityA.String())
	keyB := strings.ToLower(identityB.String())

	trackedA := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))
	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
		testImageRegistryOp: identityB,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{
		DataPlaneOIDCFederationControllerName: &future,
	}
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorEnsured(testOIDCFederationIdentityA, trackedA)),
		keyB: oidcIdentityStatus(testOIDCFederationIdentityA, testImageRegistryOp, oidcOperatorPending()),
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	fakeClient := &fakeFederatedIdentityCredentialsClient{existing: matchingDiskCSIFICs(t, identityA)}
	ctrl := gomock.NewController(t)
	smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
	smiClientBuilder.EXPECT().
		FederatedIdentityCredentialsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(fakeClient, nil)

	syncer := &dataPlaneOIDCFederationSyncer{
		clock:                         clocktesting.NewFakePassiveClock(now),
		clusterLister:                 &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		subscriptionLister:            testOIDCFederationSubscriptionLister(),
		serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:             mockResourcesDB,
		smiClientBuilder:              smiClientBuilder,
		fpaMIdataplaneClientBuilder:   testSMIDataplaneBuilder(serviceManagedIdentity, true),
		clusterScopedIdentitiesConfig: testDataPlaneOIDCFederationIdentitiesConfig(),
		oidcIssuerBaseURL:             testOIDCIssuerBaseURL,
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.NoError(t, err)

	assert.Len(t, fakeClient.gets, len(trackedA)+len(imageRegistryServiceAccounts(t)))
	assert.Len(t, fakeClient.creates, len(imageRegistryServiceAccounts(t)))
	for _, call := range fakeClient.creates {
		assert.Equal(t, identityB.Name, call.identityName, "creates should only be for the identity that was not yet ensured")
	}
	assert.Empty(t, fakeClient.deletes)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.True(t, updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyA].TargetIdentityEnsured(), "keyA TargetIdentity should be ensured")
	require.True(t, updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyB].TargetIdentityEnsured(), "keyB TargetIdentity should be ensured")
	assertOIDCFederationRecheckScheduled(t, updated, now)
}

func TestDataPlaneOIDCFederationSyncOnceReadyPendingDeconfigureIgnoresFutureRecheck(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	future := metav1.NewTime(now.Add(time.Hour))
	sinceElapsed := metav1.NewTime(now.Add(-dataPlaneOIDCFederationDeconfigureDelay))

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	identityB := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-b"))
	keyA := strings.ToLower(identityA.String())
	keyB := strings.ToLower(identityB.String())

	trackedA := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))
	trackedB := expectedFICResourceIDs(t, identityB, testImageRegistryOp, imageRegistryServiceAccounts(t))
	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{
		DataPlaneOIDCFederationControllerName: &future,
	}
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorEnsured(testOIDCFederationIdentityA, trackedA)),
		keyB: oidcIdentityStatus(testOIDCFederationIdentityA, testImageRegistryOp, oidcOperatorDeconfigure(&sinceElapsed, trackedB, nil)),
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	fakeClient := &fakeFederatedIdentityCredentialsClient{existing: matchingDiskCSIFICs(t, identityA)}
	ctrl := gomock.NewController(t)
	smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
	smiClientBuilder.EXPECT().
		FederatedIdentityCredentialsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(fakeClient, nil)

	syncer := &dataPlaneOIDCFederationSyncer{
		clock:                         clocktesting.NewFakePassiveClock(now),
		clusterLister:                 &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		subscriptionLister:            testOIDCFederationSubscriptionLister(),
		serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:             mockResourcesDB,
		smiClientBuilder:              smiClientBuilder,
		fpaMIdataplaneClientBuilder:   testSMIDataplaneBuilder(serviceManagedIdentity, true),
		clusterScopedIdentitiesConfig: testDataPlaneOIDCFederationIdentitiesConfig(),
		oidcIssuerBaseURL:             testOIDCIssuerBaseURL,
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.NoError(t, err)
	assert.Len(t, fakeClient.gets, len(trackedA))
	assert.Empty(t, fakeClient.creates)
	assert.Len(t, fakeClient.deletes, len(trackedB))
	for _, call := range fakeClient.deletes {
		assert.Equal(t, identityB.Name, call.identityName)
	}

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.NotNil(t, updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyA])
	assert.True(t, updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyA].TargetIdentityEnsured(), "keyA TargetIdentity should be ensured")
	assert.Nil(t, updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyB])
	assertOIDCFederationRecheckScheduled(t, updated, now)
}

func TestDataPlaneOIDCFederationSyncOnceSetsRecheckWhenConfiguredPlusWaitingPendingDeconfigure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	since := metav1.NewTime(now)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	identityB := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-b"))
	keyA := strings.ToLower(identityA.String())
	keyB := strings.ToLower(identityB.String())

	trackedA := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))
	trackedB := expectedFICResourceIDs(t, identityB, testImageRegistryOp, imageRegistryServiceAccounts(t))
	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: oidcIdentityStatus(testOIDCFederationIdentityA, testDiskCSIOperator, oidcOperatorEnsured(testOIDCFederationIdentityA, trackedA)),
		keyB: oidcIdentityStatus(testOIDCFederationIdentityA, testImageRegistryOp, oidcOperatorDeconfigure(&since, trackedB, nil)),
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	fakeClient := &fakeFederatedIdentityCredentialsClient{existing: matchingDiskCSIFICs(t, identityA)}
	ctrl := gomock.NewController(t)
	smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
	smiClientBuilder.EXPECT().
		FederatedIdentityCredentialsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(fakeClient, nil)

	syncer := &dataPlaneOIDCFederationSyncer{
		clock:                         clocktesting.NewFakePassiveClock(now),
		clusterLister:                 &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		subscriptionLister:            testOIDCFederationSubscriptionLister(),
		serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:             mockResourcesDB,
		smiClientBuilder:              smiClientBuilder,
		fpaMIdataplaneClientBuilder:   testSMIDataplaneBuilder(serviceManagedIdentity, true),
		clusterScopedIdentitiesConfig: testDataPlaneOIDCFederationIdentitiesConfig(),
		oidcIssuerBaseURL:             testOIDCIssuerBaseURL,
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.NoError(t, err)
	assert.Len(t, fakeClient.gets, len(trackedA))
	assert.Empty(t, fakeClient.deletes)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.True(t, updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyA].TargetIdentityEnsured(), "keyA TargetIdentity should be ensured")
	require.NotNil(t, requireOperatorStatus(t, updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyB], testImageRegistryOp).DeconfigureTimestamp, "keyB should be deconfiguring")
	assertOIDCFederationRecheckScheduled(t, updated, now)
}

func assertOIDCFederationRecheckScheduled(t *testing.T, serviceProviderCluster *coreapi.ServiceProviderCluster, now time.Time) {
	t.Helper()
	recheck := serviceProviderCluster.Spec.EarliestRecheckTimesByController[DataPlaneOIDCFederationControllerName]
	require.NotNil(t, recheck)
	assert.True(t, recheck.After(now))
}
