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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
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

func TestDataPlaneOIDCFederationSyncer_needsWork(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	future := metav1.NewTime(now.Add(time.Hour))
	since := metav1.NewTime(now)
	sinceElapsed := metav1.NewTime(now.Add(-dataPlaneOIDCFederationDeconfigureDelay))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())
	keyB := "/subscriptions/" + testSubscriptionID + "/resourcegroups/" + testResourceGroupName + "/providers/microsoft.managedidentity/userassignedidentities/identity-b"
	diskCSIFICs := buildTestFICResourceIDs(t, identityA, testDiskCSIOperator, buildTestDiskCSIDriverServiceAccounts(t))

	testCases := []struct {
		name               string
		federation         map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus
		cluster            *coreapi.HCPOpenShiftCluster
		dataPlaneOperators map[string]*azcorearm.ResourceID
		controllerRecheck  *metav1.Time
		expectedNeedsWork  bool
	}{
		{
			name:              "empty federation does not need work",
			expectedNeedsWork: false,
		},
		{
			name: "assignment without EnsuredIdentity needs work",
			federation: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentNotEnsured()),
			},
			expectedNeedsWork: true,
		},
		{
			name: "assignment without EnsuredIdentity does not need work when the cluster service ID is missing",
			federation: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentNotEnsured()),
			},
			cluster:           &coreapi.HCPOpenShiftCluster{},
			expectedNeedsWork: false,
		},
		{
			name: "assignment without DeconfigureTimestamp does not need work when the cluster is being deleted",
			federation: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentNotEnsured()),
			},
			cluster: &coreapi.HCPOpenShiftCluster{
				ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
					DeletionTimestamp: &future,
				},
			},
			expectedNeedsWork: false,
		},
		{
			name: "ensured assignment without DeconfigureTimestamp does not need work when the cluster is being deleted",
			federation: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentEnsured(testOIDCFederationIdentityA, diskCSIFICs)),
			},
			cluster: &coreapi.HCPOpenShiftCluster{
				ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
					DeletionTimestamp: &future,
				},
			},
			expectedNeedsWork: false,
		},
		{
			name: "DeconfigureTimestamp needs work during cluster deletion even when a sibling assignment is not ensured",
			federation: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentNotEnsured()),
				buildTestOIDCAssignmentKey(keyB, testImageRegistryOp): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentDeconfigure(&since, nil, nil)),
			},
			cluster: &coreapi.HCPOpenShiftCluster{
				ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
					DeletionTimestamp: &future,
				},
			},
			expectedNeedsWork: true,
		},
		{
			name: "ensured assignment with nil recheck needs work",
			federation: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentEnsured(testOIDCFederationIdentityA, diskCSIFICs)),
			},
			expectedNeedsWork: true,
		},
		{
			name: "assignment without EnsuredIdentity still needs work when controller recheck is in the future",
			federation: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentNotEnsured()),
			},
			controllerRecheck: &future,
			expectedNeedsWork: true,
		},
		{
			name: "DeconfigureTimestamp within the deconfigure delay does not need work on a cluster that is not being deleted",
			federation: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentDeconfigure(&since, nil, nil)),
			},
			expectedNeedsWork: false,
		},
		{
			name: "DeconfigureTimestamp within the deconfigure delay needs work when the cluster is being deleted",
			federation: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentDeconfigure(&since, nil, nil)),
			},
			cluster: &coreapi.HCPOpenShiftCluster{
				ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
					DeletionTimestamp: &future,
				},
			},
			expectedNeedsWork: true,
		},
		{
			name: "DeconfigureTimestamp after the deconfigure delay needs work",
			federation: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentDeconfigure(&sinceElapsed, nil, nil)),
			},
			expectedNeedsWork: true,
		},
		{
			name: "DeconfigureTimestamp after the deconfigure delay needs work even when controller recheck is in the future",
			federation: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentDeconfigure(&sinceElapsed, nil, nil)),
			},
			controllerRecheck: &future,
			expectedNeedsWork: true,
		},
		{
			name: "ensured assignment with future recheck does not need work when the desired FIC set is unchanged",
			federation: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentEnsured(testOIDCFederationIdentityA, diskCSIFICs)),
			},
			controllerRecheck: &future,
			expectedNeedsWork: false,
		},
		{
			name: "ensured assignment with future recheck needs work when PendingAzureResources has obsolete FIC IDs",
			federation: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, &coreapi.DataplaneOIDCFederationAssignmentStatus{
					EnsuredIdentity:       ptr.To(testOIDCFederationIdentityA),
					AzureResources:        diskCSIFICs,
					PendingAzureResources: []*azcorearm.ResourceID{metadataapi.Must(azcorearm.ParseResourceID(identityA.String() + "/federatedIdentityCredentials/obsolete-pending-fic"))},
				}),
			},
			controllerRecheck: &future,
			expectedNeedsWork: true,
		},
		{
			name: "ensured assignment plus DeconfigureTimestamp within the deconfigure delay with future recheck does not need work",
			federation: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentEnsured(testOIDCFederationIdentityA, diskCSIFICs)),
				buildTestOIDCAssignmentKey(keyB, testImageRegistryOp): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentDeconfigure(&since, nil, nil)),
			},
			controllerRecheck: &future,
			expectedNeedsWork: false,
		},
		{
			name: "ensured assignment plus DeconfigureTimestamp after the deconfigure delay ignores future recheck",
			federation: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentEnsured(testOIDCFederationIdentityA, diskCSIFICs)),
				buildTestOIDCAssignmentKey(keyB, testImageRegistryOp): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentDeconfigure(&sinceElapsed, nil, nil)),
			},
			controllerRecheck: &future,
			expectedNeedsWork: true,
		},
		{
			name: "ensured assignment plus a sibling without EnsuredIdentity ignores future recheck",
			federation: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentEnsured(testOIDCFederationIdentityA, diskCSIFICs)),
				buildTestOIDCAssignmentKey(keyB, testImageRegistryOp): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentNotEnsured()),
			},
			controllerRecheck: &future,
			expectedNeedsWork: true,
		},
		{
			name: "unstamped assignment that left DataPlaneOperators does not need work",
			federation: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentEnsured(testOIDCFederationIdentityA, diskCSIFICs)),
			},
			dataPlaneOperators: map[string]*azcorearm.ResourceID{},
			expectedNeedsWork:  false,
		},
		{
			name: "unstamped leaver plus DeconfigureTimestamp after the delay still needs work",
			federation: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentEnsured(testOIDCFederationIdentityA, diskCSIFICs)),
				buildTestOIDCAssignmentKey(keyB, testImageRegistryOp): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentDeconfigure(&sinceElapsed, nil, nil)),
			},
			dataPlaneOperators: map[string]*azcorearm.ResourceID{},
			expectedNeedsWork:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			syncer := &dataPlaneOIDCFederationSyncer{
				clock:                         clocktesting.NewFakePassiveClock(now),
				clusterScopedIdentitiesConfig: buildTestDataPlaneOIDCFederationIdentitiesConfig(),
			}
			cluster := tc.cluster
			if cluster == nil {
				cluster = &coreapi.HCPOpenShiftCluster{}
				cluster.ServiceProviderProperties.ClusterServiceID = buildTestClusterServiceID()
			}
			seedTestDesiredDataPlaneOperators(cluster, tc.federation)
			if tc.dataPlaneOperators != nil {
				cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators = tc.dataPlaneOperators
			}
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

func TestDataPlaneOIDCFederationSyncer_serviceManagedIdentityExists(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	smiResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))

	testCases := []struct {
		name          string
		identities    []dataplane.UserAssignedIdentityCredentials
		dataplaneErr  error
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
		{
			name:          "errors when Managed Identities Data Plane request fails",
			dataplaneErr:  errors.New("simulated mi dataplane get failure"),
			wantErrSubstr: "failed to get ServiceManagedIdentity credentials from Managed Identities Data Plane",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			syncer := &dataPlaneOIDCFederationSyncer{
				fpaMIdataplaneClientBuilder: &fakeFPAMIDataplaneClientBuilder{
					client: &fakeManagedIdentitiesDataplaneClient{
						creds: &dataplane.ManagedIdentityCredentials{ExplicitIdentities: tc.identities},
						err:   tc.dataplaneErr,
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

func TestDataPlaneOIDCFederationSyncer_generateClusterOIDCIssuerURL(t *testing.T) {
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

func TestDataPlaneOIDCFederationSyncer_clusterTenantID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	testCases := []struct {
		name          string
		subscription  *coreapi.Subscription
		want          string
		wantErrSubstr string
	}{
		{
			name:         "returns tenant ID",
			subscription: buildTestOIDCFederationSubscription(),
			want:         testClusterTenantID,
		},
		{
			name:          "errors when subscription is missing",
			wantErrSubstr: "failed to get Subscription",
		},
		{
			name: "errors when properties are nil",
			subscription: func() *coreapi.Subscription {
				subscription := buildTestOIDCFederationSubscription()
				subscription.Properties = nil
				return subscription
			}(),
			wantErrSubstr: "has no properties",
		},
		{
			name: "errors when tenantId is nil",
			subscription: func() *coreapi.Subscription {
				subscription := buildTestOIDCFederationSubscription()
				subscription.Properties.TenantId = nil
				return subscription
			}(),
			wantErrSubstr: "has nil tenantId",
		},
		{
			name: "errors when tenantId is empty",
			subscription: func() *coreapi.Subscription {
				subscription := buildTestOIDCFederationSubscription()
				subscription.Properties.TenantId = ptr.To("")
				return subscription
			}(),
			wantErrSubstr: "has empty tenantId",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var subscriptions []*coreapi.Subscription
			if tc.subscription != nil {
				subscriptions = []*coreapi.Subscription{tc.subscription}
			}
			syncer := &dataPlaneOIDCFederationSyncer{
				subscriptionLister: &corelistertesting.SliceSubscriptionLister{Subscriptions: subscriptions},
			}
			got, err := syncer.clusterTenantID(ctx, testSubscriptionID)
			if tc.wantErrSubstr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErrSubstr)
				assert.Empty(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestDataPlaneOIDCFederationSyncer_needsWork_ensuredDesiredSetChanged covers
// assignmentNeedsWork's desired-FIC-set comparison. EarliestRecheckTime stays
// in the future for every assertion so idle recheck cannot explain needsWork.
//
// The assignment is already marked ensured (EnsuredIdentity equals
// TargetIdentity, DeconfigureTimestamp is nil). AzureResources is empty
// while disk CSI still has desired service-account FICs, so the desired set
// differs and needsWork is true. The same assignment does not need work once
// the cluster has DeletionTimestamp: configure drift is skipped during
// deletion unless DeconfigureTimestamp is already set. A stamp still inside
// the 24h wait does not need work on a live cluster; deletion ignores that wait.
func TestDataPlaneOIDCFederationSyncer_needsWork_ensuredDesiredSetChanged(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	earliestRecheckAt := metav1.NewTime(now.Add(time.Hour))
	deletionTimestamp := metav1.NewTime(now.Add(-2 * time.Minute))
	// Stamped 1h ago, so deconfigure becomes ready at now+23h. Recheck is at
	// now+1h. Neither gate has fired yet.
	deconfigureRequestedAt := metav1.NewTime(now.Add(-time.Hour))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())

	cluster := buildTestClusterWithIdentities(t, testClusterName, nil, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = buildTestClusterServiceID()

	syncer := &dataPlaneOIDCFederationSyncer{
		clock:                         clocktesting.NewFakePassiveClock(now),
		clusterScopedIdentitiesConfig: buildTestDataPlaneOIDCFederationIdentitiesConfig(),
	}
	serviceProviderCluster := &coreapi.ServiceProviderCluster{}
	serviceProviderCluster.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{
		DataPlaneOIDCFederationControllerName: &earliestRecheckAt,
	}
	assignment := buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentEnsured(testOIDCFederationIdentityA, nil))
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
		buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): assignment,
	}
	assert.True(t, syncer.needsWork(cluster, serviceProviderCluster), "ensured assignment still needs work when AzureResources does not match the desired FIC set, even if recheck is in the future")

	cluster.ServiceProviderProperties.DeletionTimestamp = &deletionTimestamp
	assert.False(t, syncer.needsWork(cluster, serviceProviderCluster), "ensured assignment desired-set drift is skipped while the cluster is being deleted and no DeconfigureTimestamp is set")

	cluster.ServiceProviderProperties.DeletionTimestamp = nil
	assignment.DeconfigureTimestamp = &deconfigureRequestedAt
	assert.False(t, syncer.needsWork(cluster, serviceProviderCluster), "DeconfigureTimestamp inside the 24h wait does not need work when recheck is in the future and the cluster is not being deleted")

	cluster.ServiceProviderProperties.DeletionTimestamp = &deletionTimestamp
	assert.True(t, syncer.needsWork(cluster, serviceProviderCluster), "DeconfigureTimestamp needs work during cluster deletion even when the 24h wait has not elapsed and recheck is in the future")
}

func TestDataPlaneOIDCFederationSyncer_ensureOIDCFederationForOperator(t *testing.T) {
	t.Parallel()

	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())
	diskSAs := buildTestDiskCSIDriverServiceAccounts(t)
	require.GreaterOrEqual(t, len(diskSAs), 2)
	diskCSIFICs := buildTestFICResourceIDs(t, identityA, testDiskCSIOperator, diskSAs)
	diskCSIFICNames := ficNames(diskCSIFICs)
	failingSA := diskSAs[len(diskSAs)-1]
	failingName := federatedidentitycredential.GenerateFederatedIdentityCredentialName(testCSClusterID, testDiskCSIOperator, failingSA.Namespace, failingSA.Name)
	succeedingIDs := buildTestFICResourceIDs(t, identityA, testDiskCSIOperator, diskSAs[:len(diskSAs)-1])
	failingIDs := buildTestFICResourceIDs(t, identityA, testDiskCSIOperator, []*azure.KubernetesServiceAccount{failingSA})
	existingFailSA := diskSAs[0]
	existingFailName := federatedidentitycredential.GenerateFederatedIdentityCredentialName(testCSClusterID, testDiskCSIOperator, existingFailSA.Namespace, existingFailSA.Name)
	extraFIC, err := federatedidentitycredential.GenerateFederatedIdentityCredentialResourceID(
		identityA,
		testCSClusterID,
		testImageRegistryOp,
		"openshift-image-registry",
		"cluster-image-registry-operator",
	)
	require.NoError(t, err)
	trackedWithExtra := append(append([]*azcorearm.ResourceID{}, diskCSIFICs...), extraFIC)

	assignedCluster := buildTestClusterWithIdentities(t, testClusterName, nil, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	unassignedCluster := buildTestClusterWithIdentities(t, testClusterName, nil, nil)
	pending := buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentNotEnsured())
	ensured := buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentEnsured(testOIDCFederationIdentityA, diskCSIFICs))

	syncer := &dataPlaneOIDCFederationSyncer{
		clusterScopedIdentitiesConfig: buildTestDataPlaneOIDCFederationIdentitiesConfig(),
	}

	testCases := []struct {
		name          string
		cluster       *coreapi.HCPOpenShiftCluster
		status        *coreapi.DataplaneOIDCFederationAssignmentStatus
		ficClient     func() *fakeFederatedIdentityCredentialsClient
		wantCreates   []string
		wantDeletes   []string
		wantErrSubstr string
		want          *coreapi.DataplaneOIDCFederationAssignmentStatus
	}{
		{
			name:        "assignment without EnsuredIdentity creates every desired FIC and stamps EnsuredIdentity",
			cluster:     assignedCluster,
			status:      pending,
			wantCreates: diskCSIFICNames,
			want:        ensured,
		},
		{
			name:    "matching Get does not CreateOrUpdate",
			cluster: assignedCluster,
			status:  ensured,
			ficClient: func() *fakeFederatedIdentityCredentialsClient {
				return &fakeFederatedIdentityCredentialsClient{existing: buildTestMatchingDiskCSIFICs(t)}
			},
			want: ensured,
		},
		{
			name:        "Get not found CreateOrUpdates every desired FIC",
			cluster:     assignedCluster,
			status:      ensured,
			wantCreates: diskCSIFICNames,
			want:        ensured,
		},
		{
			name:    "drifted issuer CreateOrUpdates every desired FIC",
			cluster: assignedCluster,
			status:  ensured,
			ficClient: func() *fakeFederatedIdentityCredentialsClient {
				existing := buildTestMatchingDiskCSIFICs(t)
				for name, cred := range existing {
					cred.Properties.Issuer = ptr.To("https://oidc.example.com/other-tenant/other-cluster")
					existing[name] = cred
				}
				return &fakeFederatedIdentityCredentialsClient{existing: existing}
			},
			wantCreates: diskCSIFICNames,
			want:        ensured,
		},
		{
			name:    "create failure of a new FIC clears EnsuredIdentity and keeps PendingAzureResources",
			cluster: assignedCluster,
			status:  pending,
			ficClient: func() *fakeFederatedIdentityCredentialsClient {
				return &fakeFederatedIdentityCredentialsClient{createOrUpdateErr: errors.New("simulated azure create failure")}
			},
			wantCreates:   diskCSIFICNames,
			wantErrSubstr: "simulated azure create failure",
			want: &coreapi.DataplaneOIDCFederationAssignmentStatus{
				TargetIdentity:        testOIDCFederationIdentityA,
				PendingAzureResources: diskCSIFICs,
			},
		},
		{
			name:    "partial create success records AzureResources and PendingAzureResources separately",
			cluster: assignedCluster,
			status:  pending,
			ficClient: func() *fakeFederatedIdentityCredentialsClient {
				return &fakeFederatedIdentityCredentialsClient{
					createOrUpdateErrs: map[string]error{failingName: errors.New("simulated azure create failure for one fic")},
				}
			},
			wantCreates:   diskCSIFICNames,
			wantErrSubstr: "simulated azure create failure for one fic",
			want: &coreapi.DataplaneOIDCFederationAssignmentStatus{
				TargetIdentity:        testOIDCFederationIdentityA,
				AzureResources:        succeedingIDs,
				PendingAzureResources: failingIDs,
			},
		},
		{
			name:    "new desired FIC create failure clears EnsuredIdentity",
			cluster: assignedCluster,
			status:  buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentEnsured(testOIDCFederationIdentityA, succeedingIDs)),
			ficClient: func() *fakeFederatedIdentityCredentialsClient {
				existing := buildTestMatchingDiskCSIFICs(t)
				delete(existing, failingName)
				return &fakeFederatedIdentityCredentialsClient{
					existing:           existing,
					createOrUpdateErrs: map[string]error{failingName: errors.New("simulated azure create failure for new fic")},
				}
			},
			wantCreates:   []string{failingName},
			wantErrSubstr: "simulated azure create failure for new fic",
			want: &coreapi.DataplaneOIDCFederationAssignmentStatus{
				TargetIdentity:        testOIDCFederationIdentityA,
				AzureResources:        succeedingIDs,
				PendingAzureResources: failingIDs,
			},
		},
		{
			name:    "failed recheck of an already confirmed FIC keeps EnsuredIdentity",
			cluster: assignedCluster,
			status:  ensured,
			ficClient: func() *fakeFederatedIdentityCredentialsClient {
				existing := buildTestMatchingDiskCSIFICs(t)
				delete(existing, existingFailName)
				return &fakeFederatedIdentityCredentialsClient{
					existing:           existing,
					createOrUpdateErrs: map[string]error{existingFailName: errors.New("simulated azure create failure for existing fic")},
				}
			},
			wantCreates:   []string{existingFailName},
			wantErrSubstr: "simulated azure create failure for existing fic",
			want:          ensured,
		},
		{
			name:    "tracked FICs that are no longer desired are deleted",
			cluster: assignedCluster,
			status:  buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentEnsured(testOIDCFederationIdentityA, trackedWithExtra)),
			ficClient: func() *fakeFederatedIdentityCredentialsClient {
				return &fakeFederatedIdentityCredentialsClient{existing: buildTestMatchingDiskCSIFICs(t)}
			},
			wantDeletes: []string{extraFIC.Name},
			want:        ensured,
		},
		{
			name:    "create failure still deletes extras and keeps EnsuredIdentity when desired FICs were already confirmed",
			cluster: assignedCluster,
			status:  buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentEnsured(testOIDCFederationIdentityA, trackedWithExtra)),
			ficClient: func() *fakeFederatedIdentityCredentialsClient {
				return &fakeFederatedIdentityCredentialsClient{createOrUpdateErr: errors.New("simulated azure create failure")}
			},
			wantCreates:   diskCSIFICNames,
			wantDeletes:   []string{extraFIC.Name},
			wantErrSubstr: "simulated azure create failure",
			want:          ensured,
		},
		{
			name:    "failed extra delete stays on PendingAzureResources",
			cluster: assignedCluster,
			status:  buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentEnsured(testOIDCFederationIdentityA, trackedWithExtra)),
			ficClient: func() *fakeFederatedIdentityCredentialsClient {
				return &fakeFederatedIdentityCredentialsClient{
					existing:   buildTestMatchingDiskCSIFICs(t),
					deleteErrs: map[string]error{extraFIC.Name: errors.New("simulated azure delete failure")},
				}
			},
			wantDeletes:   []string{extraFIC.Name},
			wantErrSubstr: "simulated azure delete failure",
			want: &coreapi.DataplaneOIDCFederationAssignmentStatus{
				TargetIdentity:        testOIDCFederationIdentityA,
				EnsuredIdentity:       ptr.To(testOIDCFederationIdentityA),
				AzureResources:        trackedWithExtra,
				PendingAzureResources: []*azcorearm.ResourceID{extraFIC},
			},
		},
		{
			name:    "Get error that is not NotFound does not CreateOrUpdate and keeps EnsuredIdentity",
			cluster: assignedCluster,
			status:  ensured,
			ficClient: func() *fakeFederatedIdentityCredentialsClient {
				return &fakeFederatedIdentityCredentialsClient{getErr: errors.New("simulated azure get failure")}
			},
			wantErrSubstr: "simulated azure get failure",
			want:          ensured,
		},
		{
			name:        "operator no longer assigned deletes tracked FICs",
			cluster:     unassignedCluster,
			status:      ensured,
			wantDeletes: diskCSIFICNames,
			want: &coreapi.DataplaneOIDCFederationAssignmentStatus{
				TargetIdentity:  testOIDCFederationIdentityA,
				EnsuredIdentity: ptr.To(testOIDCFederationIdentityA),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fakeClient := &fakeFederatedIdentityCredentialsClient{}
			if tc.ficClient != nil {
				fakeClient = tc.ficClient()
			}
			got := tc.status.DeepCopy()
			err := syncer.ensureOIDCFederationForOperator(
				context.Background(),
				tc.cluster,
				keyA,
				testDiskCSIOperator,
				got,
				testCSClusterID,
				testOIDCIssuerURL,
				fakeClient,
			)
			if tc.wantErrSubstr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErrSubstr)
			} else {
				require.NoError(t, err)
			}
			assert.ElementsMatch(t, tc.wantCreates, createdFICNames(fakeClient.creates))
			assert.ElementsMatch(t, tc.wantDeletes, createdFICNames(fakeClient.deletes))
			assertDataplaneOIDCFederationAssignmentStatus(t, got, tc.want)
		})
	}
}

func TestDataPlaneOIDCFederationSyncer_deconfigureOIDCFederationForOperator(t *testing.T) {
	t.Parallel()

	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())
	diskCSIFICs := buildTestFICResourceIDs(t, identityA, testDiskCSIOperator, buildTestDiskCSIDriverServiceAccounts(t))
	elapsed := metav1.NewTime(time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC).Add(-dataPlaneOIDCFederationDeconfigureDelay))
	deconfiguring := buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentDeconfigure(&elapsed, diskCSIFICs, nil))
	syncer := &dataPlaneOIDCFederationSyncer{}

	testCases := []struct {
		name              string
		status            *coreapi.DataplaneOIDCFederationAssignmentStatus
		smiExists         bool
		ficClient         func() *fakeFederatedIdentityCredentialsClient
		ficClientBuildErr error
		wantDeletes       []string
		wantErrSubstr     string
		want              *coreapi.DataplaneOIDCFederationAssignmentStatus
	}{
		{
			name:      "ServiceManagedIdentity gone skips deletes",
			status:    deconfiguring,
			smiExists: false,
			want:      deconfiguring,
		},
		{
			name:        "deletes tracked FICs",
			status:      deconfiguring,
			smiExists:   true,
			wantDeletes: ficNames(diskCSIFICs),
			want:        deconfiguring,
		},
		{
			name:      "NotFound delete is success",
			status:    deconfiguring,
			smiExists: true,
			ficClient: func() *fakeFederatedIdentityCredentialsClient {
				return &fakeFederatedIdentityCredentialsClient{deleteErr: buildTestFICResourceNotFoundErr()}
			},
			wantDeletes: ficNames(diskCSIFICs),
			want:        deconfiguring,
		},
		{
			name:      "AuthorizationFailed delete is success",
			status:    deconfiguring,
			smiExists: true,
			ficClient: func() *fakeFederatedIdentityCredentialsClient {
				return &fakeFederatedIdentityCredentialsClient{
					deleteErr: &azcore.ResponseError{ErrorCode: "AuthorizationFailed"},
				}
			},
			wantDeletes: ficNames(diskCSIFICs),
			want:        deconfiguring,
		},
		{
			name:      "ParentResourceNotFound delete is success",
			status:    deconfiguring,
			smiExists: true,
			ficClient: func() *fakeFederatedIdentityCredentialsClient {
				return &fakeFederatedIdentityCredentialsClient{
					deleteErr: &azcore.ResponseError{ErrorCode: "ParentResourceNotFound"},
				}
			},
			wantDeletes: ficNames(diskCSIFICs),
			want:        deconfiguring,
		},
		{
			name: "deletes PendingAzureResources as well as AzureResources",
			status: buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentDeconfigure(
				&elapsed,
				[]*azcorearm.ResourceID{diskCSIFICs[0]},
				diskCSIFICs[1:],
			)),
			smiExists:   true,
			wantDeletes: ficNames(diskCSIFICs),
			want: buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentDeconfigure(
				&elapsed,
				[]*azcorearm.ResourceID{diskCSIFICs[0]},
				diskCSIFICs[1:],
			)),
		},
		{
			name:      "partial delete failure keeps remaining AzureResources",
			status:    deconfiguring,
			smiExists: true,
			ficClient: func() *fakeFederatedIdentityCredentialsClient {
				return &fakeFederatedIdentityCredentialsClient{
					deleteErrs: map[string]error{diskCSIFICs[0].Name: errors.New("simulated azure delete failure for one fic")},
				}
			},
			wantDeletes:   ficNames(diskCSIFICs),
			wantErrSubstr: "simulated azure delete failure for one fic",
			want:          buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentDeconfigure(&elapsed, []*azcorearm.ResourceID{diskCSIFICs[0]}, nil)),
		},
		{
			name:              "client build failure keeps tracked FICs",
			status:            deconfiguring,
			smiExists:         true,
			ficClientBuildErr: errors.New("simulated fic client build failure"),
			wantErrSubstr:     "simulated fic client build failure",
			want:              deconfiguring,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fakeClient := &fakeFederatedIdentityCredentialsClient{}
			if tc.ficClient != nil {
				fakeClient = tc.ficClient()
			}
			got := tc.status.DeepCopy()
			err := syncer.deconfigureOIDCFederationForOperator(
				context.Background(),
				keyA,
				got,
				tc.smiExists,
				func() (azureclient.FederatedIdentityCredentialsClient, error) {
					if tc.ficClientBuildErr != nil {
						return nil, tc.ficClientBuildErr
					}
					return fakeClient, nil
				},
			)
			if tc.wantErrSubstr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErrSubstr)
			} else {
				require.NoError(t, err)
			}
			assert.ElementsMatch(t, tc.wantDeletes, createdFICNames(fakeClient.deletes))
			assertDataplaneOIDCFederationAssignmentStatus(t, got, tc.want)
		})
	}
}

func TestDataPlaneOIDCFederationSyncer_federatedIdentityCredentialsForOperator(t *testing.T) {
	t.Parallel()

	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	identityB := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-b"))
	diskSAs := buildTestDiskCSIDriverServiceAccounts(t)
	diskCSIFICs := buildTestFICResourceIDs(t, identityA, testDiskCSIOperator, diskSAs)
	syncer := &dataPlaneOIDCFederationSyncer{
		clusterScopedIdentitiesConfig: buildTestDataPlaneOIDCFederationIdentitiesConfig(),
	}

	testCases := []struct {
		name         string
		cluster      *coreapi.HCPOpenShiftCluster
		identity     *azcorearm.ResourceID
		operatorName string
		wantIDs      []*azcorearm.ResourceID
		wantSubjects []string
	}{
		{
			name: "assigned operator returns one credential per service account",
			cluster: buildTestClusterWithIdentities(t, testClusterName, nil, map[string]*azcorearm.ResourceID{
				testDiskCSIOperator: identityA,
			}),
			identity:     identityA,
			operatorName: testDiskCSIOperator,
			wantIDs:      diskCSIFICs,
			wantSubjects: func() []string {
				out := make([]string, 0, len(diskSAs))
				for _, sa := range diskSAs {
					out = append(out, sa.AsOIDCSubject())
				}
				return out
			}(),
		},
		{
			name:         "operator missing from DataPlaneOperators returns no credentials",
			cluster:      buildTestClusterWithIdentities(t, testClusterName, nil, nil),
			identity:     identityA,
			operatorName: testDiskCSIOperator,
		},
		{
			name: "operator assigned to a different identity returns no credentials",
			cluster: buildTestClusterWithIdentities(t, testClusterName, nil, map[string]*azcorearm.ResourceID{
				testDiskCSIOperator: identityB,
			}),
			identity:     identityA,
			operatorName: testDiskCSIOperator,
		},
		{
			name: "unknown operator name returns no credentials",
			cluster: buildTestClusterWithIdentities(t, testClusterName, nil, map[string]*azcorearm.ResourceID{
				"not-a-cluster-scoped-operator": identityA,
			}),
			identity:     identityA,
			operatorName: "not-a-cluster-scoped-operator",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := syncer.federatedIdentityCredentialsForOperator(tc.cluster, tc.identity, tc.operatorName, testCSClusterID)
			require.NoError(t, err)
			var gotIDs []*azcorearm.ResourceID
			var gotSubjects []string
			for _, credential := range got {
				gotIDs = append(gotIDs, credential.resourceID)
				gotSubjects = append(gotSubjects, credential.subject)
			}
			assert.ElementsMatch(t, resourceIDStrings(tc.wantIDs), resourceIDStrings(gotIDs))
			assert.ElementsMatch(t, tc.wantSubjects, gotSubjects)
		})
	}
}

func TestDataPlaneOIDCFederationSyncer_federatedIdentityCredentialNeedsUpdate(t *testing.T) {
	t.Parallel()

	desired := &armmsi.FederatedIdentityCredentialProperties{
		Issuer:    ptr.To(testOIDCIssuerURL),
		Subject:   ptr.To("system:serviceaccount:openshift-cluster-csi-drivers:azure-disk-csi-driver-operator"),
		Audiences: []*string{ptr.To(dataPlaneOIDCFederationAudience)},
	}
	syncer := &dataPlaneOIDCFederationSyncer{}

	testCases := []struct {
		name     string
		existing *armmsi.FederatedIdentityCredentialProperties
		desired  *armmsi.FederatedIdentityCredentialProperties
		want     bool
	}{
		{
			name: "both nil do not need update",
		},
		{
			name:    "nil existing needs update",
			desired: desired,
			want:    true,
		},
		{
			name:     "nil desired needs update",
			existing: desired,
			want:     true,
		},
		{
			name:     "matching properties do not need update",
			existing: desired,
			desired:  desired,
		},
		{
			name: "issuer drift needs update",
			existing: &armmsi.FederatedIdentityCredentialProperties{
				Issuer:    ptr.To("https://oidc.example.com/other-tenant/other-cluster"),
				Subject:   desired.Subject,
				Audiences: desired.Audiences,
			},
			desired: desired,
			want:    true,
		},
		{
			name: "subject drift needs update",
			existing: &armmsi.FederatedIdentityCredentialProperties{
				Issuer:    desired.Issuer,
				Subject:   ptr.To("system:serviceaccount:other:other"),
				Audiences: desired.Audiences,
			},
			desired: desired,
			want:    true,
		},
		{
			name: "audience drift needs update",
			existing: &armmsi.FederatedIdentityCredentialProperties{
				Issuer:    desired.Issuer,
				Subject:   desired.Subject,
				Audiences: []*string{ptr.To("api://AzureADTokenExchange")},
			},
			desired: desired,
			want:    true,
		},
		{
			name: "audience order does not need update",
			existing: &armmsi.FederatedIdentityCredentialProperties{
				Issuer:    desired.Issuer,
				Subject:   desired.Subject,
				Audiences: []*string{ptr.To("openshift"), ptr.To("extra")},
			},
			desired: &armmsi.FederatedIdentityCredentialProperties{
				Issuer:    desired.Issuer,
				Subject:   desired.Subject,
				Audiences: []*string{ptr.To("extra"), ptr.To("openshift")},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, syncer.federatedIdentityCredentialNeedsUpdate(tc.existing, tc.desired))
		})
	}
}

// TestDataPlaneOIDCFederationSyncer_SyncOnce covers SyncOnce gating and
// persistence: pending write-ahead, configure vs deconfigure on the same
// cluster, skip configure while still deconfiguring, EarliestRecheckTime only
// when Azure work succeeds, and assignment removal only after a successful deconfigure.
// Azure FIC create, update, and delete details are tested on
// ensureOIDCFederationForOperator and deconfigureOIDCFederationForOperator.
func TestDataPlaneOIDCFederationSyncer_SyncOnce(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	since := metav1.NewTime(now)
	future := metav1.NewTime(now.Add(time.Hour))
	deletionTimestamp := metav1.NewTime(now)
	elapsed := metav1.NewTime(now.Add(-dataPlaneOIDCFederationDeconfigureDelay))

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	identityB := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-b"))
	keyA := strings.ToLower(identityA.String())
	keyB := strings.ToLower(identityB.String())

	diskSAs := buildTestDiskCSIDriverServiceAccounts(t)
	require.GreaterOrEqual(t, len(diskSAs), 2)
	diskCSIFICsA := buildTestFICResourceIDs(t, identityA, testDiskCSIOperator, diskSAs)
	diskCSIFICsB := buildTestFICResourceIDs(t, identityB, testDiskCSIOperator, diskSAs)
	imageSAs := buildTestImageRegistryServiceAccounts(t)
	imageFICsA := buildTestFICResourceIDs(t, identityA, testImageRegistryOp, imageSAs)
	imageFICsB := buildTestFICResourceIDs(t, identityB, testImageRegistryOp, imageSAs)
	diskCSIFICNamesA := ficNames(diskCSIFICsA)
	imageFICNamesA := ficNames(imageFICsA)
	imageFICNamesB := ficNames(imageFICsB)

	failingSA := diskSAs[len(diskSAs)-1]
	failingName := federatedidentitycredential.GenerateFederatedIdentityCredentialName(testCSClusterID, testDiskCSIOperator, failingSA.Namespace, failingSA.Name)
	succeedingIDs := buildTestFICResourceIDs(t, identityA, testDiskCSIOperator, diskSAs[:len(diskSAs)-1])
	failingIDs := buildTestFICResourceIDs(t, identityA, testDiskCSIOperator, []*azure.KubernetesServiceAccount{failingSA})
	partialDeleteRemaining := []*azcorearm.ResourceID{diskCSIFICsA[0]}

	diskCreateSubjects := map[string]string{}
	for _, sa := range diskSAs {
		name := federatedidentitycredential.GenerateFederatedIdentityCredentialName(testCSClusterID, testDiskCSIOperator, sa.Namespace, sa.Name)
		diskCreateSubjects[name] = sa.AsOIDCSubject()
	}

	operatorsDiskA := map[string]*azcorearm.ResourceID{testDiskCSIOperator: identityA}
	pendingDiskA := map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
		buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentNotEnsured()),
	}
	ensuredDiskA := map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
		buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentEnsured(testOIDCFederationIdentityA, diskCSIFICsA)),
	}
	deconfigureElapsedDiskA := map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
		buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentDeconfigure(&elapsed, diskCSIFICsA, nil)),
	}
	deconfigureNowDiskA := map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
		buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentDeconfigure(&since, diskCSIFICsA, nil)),
	}

	testCases := []struct {
		name                   string
		cluster                *coreapi.HCPOpenShiftCluster
		serviceProviderCluster *coreapi.ServiceProviderCluster
		ficClient              func() *fakeFederatedIdentityCredentialsClient
		ficClientBuildErr      error
		skipFICClient          bool
		smiMissingInAzure      bool
		miDataplaneBuildErr    error
		wantCreates            []string
		wantDeletes            []string
		wantGets               []string
		wantCreateSubjects     map[string]string
		wantCreateIdentityName string
		wantDeleteIdentityName string
		wantErrSubstr          string
		want                   map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus
		wantRecheckScheduled   bool
		wantRecheckCleared     bool
		wantRecheckUnchanged   bool
	}{
		{
			name:                   "configures FICs and sets EarliestRecheckTime",
			cluster:                buildTestOIDCFederationCluster(t, serviceManagedIdentity, operatorsDiskA),
			serviceProviderCluster: buildTestOIDCFederationServiceProviderCluster(pendingDiskA),
			wantCreates:            diskCSIFICNamesA,
			wantCreateSubjects:     diskCreateSubjects,
			want:                   ensuredDiskA,
			wantRecheckScheduled:   true,
		},
		{
			name:    "configures FICs for multiple operators sharing an identity",
			cluster: buildTestOIDCFederationCluster(t, serviceManagedIdentity, map[string]*azcorearm.ResourceID{testDiskCSIOperator: identityA, testImageRegistryOp: identityA}),
			serviceProviderCluster: buildTestOIDCFederationServiceProviderCluster(map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentNotEnsured()),
				buildTestOIDCAssignmentKey(keyA, testImageRegistryOp): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentNotEnsured()),
			}),
			wantCreates: append(append([]string{}, diskCSIFICNamesA...), imageFICNamesA...),
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentEnsured(testOIDCFederationIdentityA, diskCSIFICsA)),
				buildTestOIDCAssignmentKey(keyA, testImageRegistryOp): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentEnsured(testOIDCFederationIdentityA, imageFICsA)),
			},
		},
		{
			name:                   "deconfigure after the delay removes the assignment and clears EarliestRecheckTime",
			cluster:                buildTestOIDCFederationCluster(t, serviceManagedIdentity, nil),
			serviceProviderCluster: buildTestOIDCFederationServiceProviderCluster(deconfigureElapsedDiskA),
			wantDeletes:            diskCSIFICNamesA,
			wantRecheckCleared:     true,
		},
		{
			name:                   "cluster that is not being deleted waits for the deconfigure delay after DeconfigureTimestamp",
			cluster:                buildTestOIDCFederationCluster(t, serviceManagedIdentity, nil),
			serviceProviderCluster: buildTestOIDCFederationServiceProviderCluster(deconfigureNowDiskA),
			skipFICClient:          true,
			want:                   deconfigureNowDiskA,
		},
		{
			name:                   "cluster that is being deleted deconfigures immediately even when DeconfigureTimestamp is inside the delay and EarliestRecheckTime is in the future",
			cluster:                buildTestOIDCFederationCluster(t, serviceManagedIdentity, nil, withTestClusterDeletionTimestamp(&deletionTimestamp)),
			serviceProviderCluster: buildTestOIDCFederationServiceProviderCluster(deconfigureNowDiskA, withTestEarliestOIDCFederationRecheck(&future)),
			wantDeletes:            diskCSIFICNamesA,
			wantRecheckCleared:     true,
		},
		{
			name:                   "persists PendingAzureResources before configure client build failure",
			cluster:                buildTestOIDCFederationCluster(t, serviceManagedIdentity, operatorsDiskA),
			serviceProviderCluster: buildTestOIDCFederationServiceProviderCluster(pendingDiskA),
			ficClientBuildErr:      errors.New("simulated fic client build failure"),
			wantErrSubstr:          "simulated fic client build failure",
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): {
					TargetIdentity:        testOIDCFederationIdentityA,
					PendingAzureResources: diskCSIFICsA,
				},
			},
		},
		{
			name:                   "failed Azure CreateOrUpdate keeps PendingAzureResources",
			cluster:                buildTestOIDCFederationCluster(t, serviceManagedIdentity, operatorsDiskA),
			serviceProviderCluster: buildTestOIDCFederationServiceProviderCluster(pendingDiskA),
			ficClient: func() *fakeFederatedIdentityCredentialsClient {
				return &fakeFederatedIdentityCredentialsClient{createOrUpdateErr: errors.New("simulated azure create failure")}
			},
			wantCreates:   diskCSIFICNamesA,
			wantErrSubstr: "simulated azure create failure",
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): {
					TargetIdentity:        testOIDCFederationIdentityA,
					PendingAzureResources: diskCSIFICsA,
				},
			},
		},
		{
			name:                   "partial configure is persisted and EarliestRecheckTime is not set",
			cluster:                buildTestOIDCFederationCluster(t, serviceManagedIdentity, operatorsDiskA),
			serviceProviderCluster: buildTestOIDCFederationServiceProviderCluster(pendingDiskA),
			ficClient: func() *fakeFederatedIdentityCredentialsClient {
				return &fakeFederatedIdentityCredentialsClient{
					createOrUpdateErrs: map[string]error{failingName: errors.New("simulated azure create failure for one fic")},
				}
			},
			wantCreates:        diskCSIFICNamesA,
			wantErrSubstr:      "simulated azure create failure for one fic",
			wantRecheckCleared: true,
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): {
					TargetIdentity:        testOIDCFederationIdentityA,
					AzureResources:        succeedingIDs,
					PendingAzureResources: failingIDs,
				},
			},
		},
		{
			name:    "new FIC create failure clears EnsuredIdentity after pending was staged",
			cluster: buildTestOIDCFederationCluster(t, serviceManagedIdentity, operatorsDiskA),
			serviceProviderCluster: buildTestOIDCFederationServiceProviderCluster(map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentEnsured(testOIDCFederationIdentityA, succeedingIDs)),
			}),
			ficClient: func() *fakeFederatedIdentityCredentialsClient {
				existing := buildTestMatchingDiskCSIFICs(t)
				delete(existing, failingName)
				return &fakeFederatedIdentityCredentialsClient{
					existing:           existing,
					createOrUpdateErrs: map[string]error{failingName: errors.New("simulated azure create failure for new fic")},
				}
			},
			wantCreates:   []string{failingName},
			wantErrSubstr: "simulated azure create failure for new fic",
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): {
					TargetIdentity:        testOIDCFederationIdentityA,
					AzureResources:        succeedingIDs,
					PendingAzureResources: failingIDs,
				},
			},
		},
		{
			name:                   "partial deconfigure keeps the assignment",
			cluster:                buildTestOIDCFederationCluster(t, serviceManagedIdentity, nil),
			serviceProviderCluster: buildTestOIDCFederationServiceProviderCluster(deconfigureElapsedDiskA),
			ficClient: func() *fakeFederatedIdentityCredentialsClient {
				return &fakeFederatedIdentityCredentialsClient{
					deleteErrs: map[string]error{diskCSIFICsA[0].Name: errors.New("simulated azure delete failure for one fic")},
				}
			},
			wantDeletes:   diskCSIFICNamesA,
			wantErrSubstr: "simulated azure delete failure for one fic",
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentDeconfigure(&elapsed, partialDeleteRemaining, nil)),
			},
		},
		{
			name:                   "deconfigure client failure keeps the assignment",
			cluster:                buildTestOIDCFederationCluster(t, serviceManagedIdentity, nil),
			serviceProviderCluster: buildTestOIDCFederationServiceProviderCluster(deconfigureElapsedDiskA),
			skipFICClient:          true,
			miDataplaneBuildErr:    errors.New("simulated mi dataplane build failure"),
			wantErrSubstr:          "simulated mi dataplane build failure",
			want:                   deconfigureElapsedDiskA,
		},
		{
			name:                   "skips configure when ClusterServiceID is missing",
			cluster:                buildTestOIDCFederationCluster(t, serviceManagedIdentity, operatorsDiskA, withTestNoClusterServiceID),
			serviceProviderCluster: buildTestOIDCFederationServiceProviderCluster(pendingDiskA),
			skipFICClient:          true,
			want:                   pendingDiskA,
		},
		{
			name:                   "skips configure while the cluster is deleting",
			cluster:                buildTestOIDCFederationCluster(t, serviceManagedIdentity, operatorsDiskA, withTestClusterDeletionTimestamp(&deletionTimestamp)),
			serviceProviderCluster: buildTestOIDCFederationServiceProviderCluster(pendingDiskA),
			skipFICClient:          true,
			want:                   pendingDiskA,
		},
		{
			name:    "skips configure but deconfigures a sibling while the cluster is deleting",
			cluster: buildTestOIDCFederationCluster(t, serviceManagedIdentity, operatorsDiskA, withTestClusterDeletionTimestamp(&deletionTimestamp)),
			serviceProviderCluster: buildTestOIDCFederationServiceProviderCluster(map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentNotEnsured()),
				buildTestOIDCAssignmentKey(keyB, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentDeconfigure(&deletionTimestamp, diskCSIFICsB, nil)),
			}),
			wantDeletes: ficNames(diskCSIFICsB),
			want:        pendingDiskA,
		},
		{
			name:                   "deconfigures even when ClusterServiceID is missing",
			cluster:                buildTestOIDCFederationCluster(t, serviceManagedIdentity, nil, withTestNoClusterServiceID),
			serviceProviderCluster: buildTestOIDCFederationServiceProviderCluster(deconfigureElapsedDiskA),
			wantDeletes:            diskCSIFICNamesA,
		},
		{
			name:                   "missing ServiceManagedIdentity drops the deconfiguring assignment",
			cluster:                buildTestOIDCFederationCluster(t, serviceManagedIdentity, nil),
			serviceProviderCluster: buildTestOIDCFederationServiceProviderCluster(deconfigureElapsedDiskA),
			skipFICClient:          true,
			smiMissingInAzure:      true,
		},
		{
			name:                   "nil ServiceManagedIdentity is an error",
			cluster:                buildTestOIDCFederationCluster(t, serviceManagedIdentity, operatorsDiskA, withTestNilServiceManagedIdentity),
			serviceProviderCluster: buildTestOIDCFederationServiceProviderCluster(pendingDiskA),
			skipFICClient:          true,
			wantErrSubstr:          "ServiceManagedIdentity is nil",
			want:                   pendingDiskA,
		},
		{
			name:                   "ensured assignment with future EarliestRecheckTime skips Azure when the desired set is unchanged",
			cluster:                buildTestOIDCFederationCluster(t, serviceManagedIdentity, operatorsDiskA),
			serviceProviderCluster: buildTestOIDCFederationServiceProviderCluster(ensuredDiskA, withTestEarliestOIDCFederationRecheck(&future)),
			skipFICClient:          true,
			want:                   ensuredDiskA,
		},
		{
			name:    "ensured assignment is still reconciled when a sibling is not yet ensured despite future EarliestRecheckTime",
			cluster: buildTestOIDCFederationCluster(t, serviceManagedIdentity, map[string]*azcorearm.ResourceID{testDiskCSIOperator: identityA, testImageRegistryOp: identityB}),
			serviceProviderCluster: buildTestOIDCFederationServiceProviderCluster(map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentEnsured(testOIDCFederationIdentityA, diskCSIFICsA)),
				buildTestOIDCAssignmentKey(keyB, testImageRegistryOp): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentNotEnsured()),
			}, withTestEarliestOIDCFederationRecheck(&future)),
			ficClient: func() *fakeFederatedIdentityCredentialsClient {
				return &fakeFederatedIdentityCredentialsClient{existing: buildTestMatchingDiskCSIFICs(t)}
			},
			wantGets:               append(append([]string{}, diskCSIFICNamesA...), imageFICNamesB...),
			wantCreates:            imageFICNamesB,
			wantCreateIdentityName: identityB.Name,
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentEnsured(testOIDCFederationIdentityA, diskCSIFICsA)),
				buildTestOIDCAssignmentKey(keyB, testImageRegistryOp): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentEnsured(testOIDCFederationIdentityA, imageFICsB)),
			},
			wantRecheckScheduled: true,
		},
		{
			name:    "ensured assignment is still reconciled when a sibling DeconfigureTimestamp delay has elapsed despite future EarliestRecheckTime",
			cluster: buildTestOIDCFederationCluster(t, serviceManagedIdentity, operatorsDiskA),
			serviceProviderCluster: buildTestOIDCFederationServiceProviderCluster(map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentEnsured(testOIDCFederationIdentityA, diskCSIFICsA)),
				buildTestOIDCAssignmentKey(keyB, testImageRegistryOp): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentDeconfigure(&elapsed, imageFICsB, nil)),
			}, withTestEarliestOIDCFederationRecheck(&future)),
			ficClient: func() *fakeFederatedIdentityCredentialsClient {
				return &fakeFederatedIdentityCredentialsClient{existing: buildTestMatchingDiskCSIFICs(t)}
			},
			wantGets:               diskCSIFICNamesA,
			wantDeletes:            imageFICNamesB,
			wantDeleteIdentityName: identityB.Name,
			want:                   ensuredDiskA,
			wantRecheckScheduled:   true,
		},
		{
			name:    "sets EarliestRecheckTime when an ensured assignment has a sibling whose DeconfigureTimestamp is still inside the delay",
			cluster: buildTestOIDCFederationCluster(t, serviceManagedIdentity, operatorsDiskA),
			serviceProviderCluster: buildTestOIDCFederationServiceProviderCluster(map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentEnsured(testOIDCFederationIdentityA, diskCSIFICsA)),
				buildTestOIDCAssignmentKey(keyB, testImageRegistryOp): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentDeconfigure(&since, imageFICsB, nil)),
			}),
			ficClient: func() *fakeFederatedIdentityCredentialsClient {
				return &fakeFederatedIdentityCredentialsClient{existing: buildTestMatchingDiskCSIFICs(t)}
			},
			wantGets: diskCSIFICNamesA,
			want: map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentEnsured(testOIDCFederationIdentityA, diskCSIFICsA)),
				buildTestOIDCAssignmentKey(keyB, testImageRegistryOp): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentDeconfigure(&since, imageFICsB, nil)),
			},
			wantRecheckScheduled: true,
		},
		{
			name:                   "does not delete FICs for an unstamped assignment that left DataPlaneOperators",
			cluster:                buildTestOIDCFederationCluster(t, serviceManagedIdentity, nil),
			serviceProviderCluster: buildTestOIDCFederationServiceProviderCluster(ensuredDiskA),
			skipFICClient:          true,
			want:                   ensuredDiskA,
		},
		{
			name:    "does not delete FICs for an unstamped leaver while deconfiguring a sibling",
			cluster: buildTestOIDCFederationCluster(t, serviceManagedIdentity, nil),
			serviceProviderCluster: buildTestOIDCFederationServiceProviderCluster(map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentEnsured(testOIDCFederationIdentityA, diskCSIFICsA)),
				buildTestOIDCAssignmentKey(keyB, testImageRegistryOp): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentDeconfigure(&elapsed, imageFICsB, nil)),
			}),
			wantDeletes:            imageFICNamesB,
			wantDeleteIdentityName: identityB.Name,
			want:                   ensuredDiskA,
			wantRecheckScheduled:   true,
		},
		{
			name:    "deconfigures a sibling when ClusterServiceID is missing and does not refresh EarliestRecheckTime for a remaining pending assignment",
			cluster: buildTestOIDCFederationCluster(t, serviceManagedIdentity, operatorsDiskA, withTestNoClusterServiceID),
			serviceProviderCluster: buildTestOIDCFederationServiceProviderCluster(map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentNotEnsured()),
				buildTestOIDCAssignmentKey(keyB, testImageRegistryOp): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentDeconfigure(&elapsed, imageFICsB, nil)),
			}, withTestEarliestOIDCFederationRecheck(&future)),
			wantDeletes:            imageFICNamesB,
			wantDeleteIdentityName: identityB.Name,
			want:                   pendingDiskA,
			wantRecheckUnchanged:   true,
		},
		{
			name:    "deconfigures every assignment whose delay has elapsed in one reconcile",
			cluster: buildTestOIDCFederationCluster(t, serviceManagedIdentity, nil),
			serviceProviderCluster: buildTestOIDCFederationServiceProviderCluster(map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{
				buildTestOIDCAssignmentKey(keyA, testDiskCSIOperator): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentDeconfigure(&elapsed, diskCSIFICsA, nil)),
				buildTestOIDCAssignmentKey(keyB, testImageRegistryOp): buildTestOIDCAssignmentWithTarget(testOIDCFederationIdentityA, buildTestOIDCAssignmentDeconfigure(&elapsed, imageFICsB, nil)),
			}),
			wantDeletes:        append(append([]string{}, diskCSIFICNamesA...), imageFICNamesB...),
			wantRecheckCleared: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{tc.cluster, tc.serviceProviderCluster})
			require.NoError(t, err)

			fakeClient := &fakeFederatedIdentityCredentialsClient{}
			if tc.ficClient != nil {
				fakeClient = tc.ficClient()
			}

			ctrl := gomock.NewController(t)
			smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
			switch {
			case tc.ficClientBuildErr != nil:
				smiClientBuilder.EXPECT().
					FederatedIdentityCredentialsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					Return(nil, tc.ficClientBuildErr)
			case tc.skipFICClient:
				smiClientBuilder.EXPECT().
					FederatedIdentityCredentialsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					Times(0)
			default:
				smiClientBuilder.EXPECT().
					FederatedIdentityCredentialsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					Return(fakeClient, nil)
			}

			var dataplaneBuilder azureclient.FPAMIDataplaneClientBuilder
			if tc.miDataplaneBuildErr != nil {
				dataplaneBuilder = &fakeFPAMIDataplaneClientBuilder{buildErr: tc.miDataplaneBuildErr}
			} else if smi := tc.cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity; smi != nil {
				dataplaneBuilder = buildTestSMIDataplaneBuilder(smi, !tc.smiMissingInAzure)
			}

			syncer := &dataPlaneOIDCFederationSyncer{
				clock:                         clocktesting.NewFakePassiveClock(now),
				clusterLister:                 &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
				subscriptionLister:            buildTestOIDCFederationSubscriptionLister(),
				serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
				resourcesDBClient:             mockResourcesDB,
				smiClientBuilder:              smiClientBuilder,
				fpaMIdataplaneClientBuilder:   dataplaneBuilder,
				clusterScopedIdentitiesConfig: buildTestDataPlaneOIDCFederationIdentitiesConfig(),
				oidcIssuerBaseURL:             testOIDCIssuerBaseURL,
			}

			err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    testClusterName,
			})
			if tc.wantErrSubstr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErrSubstr)
			} else {
				require.NoError(t, err)
			}

			assert.ElementsMatch(t, tc.wantCreates, createdFICNames(fakeClient.creates))
			assert.ElementsMatch(t, tc.wantDeletes, createdFICNames(fakeClient.deletes))
			if tc.wantGets != nil {
				assert.ElementsMatch(t, tc.wantGets, createdFICNames(fakeClient.gets))
			}
			for _, call := range fakeClient.creates {
				if call.credential.Properties == nil {
					continue
				}
				assert.Equal(t, testOIDCIssuerURL, ptr.Deref(call.credential.Properties.Issuer, ""))
				assert.Equal(t, []string{dataPlaneOIDCFederationAudience}, derefStrings(call.credential.Properties.Audiences))
				if subject, ok := tc.wantCreateSubjects[call.credentialName]; ok {
					assert.Equal(t, subject, ptr.Deref(call.credential.Properties.Subject, ""))
				}
				if tc.wantCreateIdentityName != "" {
					assert.Equal(t, tc.wantCreateIdentityName, call.identityName)
				}
			}
			for _, call := range fakeClient.deletes {
				if tc.wantDeleteIdentityName != "" {
					assert.Equal(t, tc.wantDeleteIdentityName, call.identityName)
				}
			}

			updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			assertDataplaneOIDCFederationAssignments(t, updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation, tc.want)
			if tc.wantRecheckScheduled {
				assertOIDCFederationRecheckScheduled(t, updated, now)
			}
			if tc.wantRecheckCleared {
				assert.Nil(t, updated.Spec.EarliestRecheckTimesByController[DataPlaneOIDCFederationControllerName])
			}
			if tc.wantRecheckUnchanged {
				inputRecheck := tc.serviceProviderCluster.Spec.EarliestRecheckTimesByController[DataPlaneOIDCFederationControllerName]
				gotRecheck := updated.Spec.EarliestRecheckTimesByController[DataPlaneOIDCFederationControllerName]
				require.NotNil(t, inputRecheck)
				require.NotNil(t, gotRecheck)
				assert.True(t, gotRecheck.Time.Equal(inputRecheck.Time))
			}
		})
	}
}
