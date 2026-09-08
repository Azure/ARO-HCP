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
	"net/http"
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

const (
	testSubscriptionID     = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName  = "test-rg"
	testClusterName        = "test-cluster"
	testIdentityResourceID = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity"
	testClientID           = "client-id-123"
	testPrincipalID        = "principal-id-456"
	testLocation           = "test-location"
)

const (
	testCSClusterID       = "cs-cluster-abc"
	testOIDCIssuerBaseURL = "https://oidc.example.com"
	testOIDCIssuerURL     = "https://oidc.example.com/tenant-a/cs-cluster-abc"
	testDiskCSIOperator   = string(azure.ClusterOperatorIdentifierDiskCSIDriver)
	testImageRegistryOp   = string(azure.ClusterOperatorIdentifierImageRegistry)
	testDiskCSINamespace  = "openshift-cluster-csi-drivers"
)

type recordedFICCall struct {
	resourceGroupName string
	identityName      string
	credentialName    string
	credential        armmsi.FederatedIdentityCredential
}

type fakeFederatedIdentityCredentialsClient struct {
	createOrUpdateErr  error
	createOrUpdateErrs map[string]error
	deleteErr          error
	deleteErrs         map[string]error
	getErr             error
	getErrs            map[string]error

	creates  []recordedFICCall
	deletes  []recordedFICCall
	gets     []recordedFICCall
	existing map[string]armmsi.FederatedIdentityCredential
}

func (f *fakeFederatedIdentityCredentialsClient) errFor(name string, perName map[string]error, fallback error) error {
	if err, ok := perName[name]; ok {
		return err
	}
	return fallback
}

var _ azureclient.FederatedIdentityCredentialsClient = (*fakeFederatedIdentityCredentialsClient)(nil)

func (f *fakeFederatedIdentityCredentialsClient) CreateOrUpdate(_ context.Context, resourceGroupName string, resourceName string, federatedIdentityCredentialResourceName string, parameters armmsi.FederatedIdentityCredential, _ *armmsi.FederatedIdentityCredentialsClientCreateOrUpdateOptions) (armmsi.FederatedIdentityCredentialsClientCreateOrUpdateResponse, error) {
	f.creates = append(f.creates, recordedFICCall{
		resourceGroupName: resourceGroupName,
		identityName:      resourceName,
		credentialName:    federatedIdentityCredentialResourceName,
		credential:        parameters,
	})
	if err := f.errFor(federatedIdentityCredentialResourceName, f.createOrUpdateErrs, f.createOrUpdateErr); err != nil {
		return armmsi.FederatedIdentityCredentialsClientCreateOrUpdateResponse{}, err
	}
	return armmsi.FederatedIdentityCredentialsClientCreateOrUpdateResponse{}, nil
}

func (f *fakeFederatedIdentityCredentialsClient) Delete(_ context.Context, resourceGroupName string, resourceName string, federatedIdentityCredentialResourceName string, _ *armmsi.FederatedIdentityCredentialsClientDeleteOptions) (armmsi.FederatedIdentityCredentialsClientDeleteResponse, error) {
	f.deletes = append(f.deletes, recordedFICCall{
		resourceGroupName: resourceGroupName,
		identityName:      resourceName,
		credentialName:    federatedIdentityCredentialResourceName,
	})
	if err := f.errFor(federatedIdentityCredentialResourceName, f.deleteErrs, f.deleteErr); err != nil {
		return armmsi.FederatedIdentityCredentialsClientDeleteResponse{}, err
	}
	return armmsi.FederatedIdentityCredentialsClientDeleteResponse{}, nil
}

func (f *fakeFederatedIdentityCredentialsClient) Get(_ context.Context, resourceGroupName string, resourceName string, federatedIdentityCredentialResourceName string, _ *armmsi.FederatedIdentityCredentialsClientGetOptions) (armmsi.FederatedIdentityCredentialsClientGetResponse, error) {
	f.gets = append(f.gets, recordedFICCall{
		resourceGroupName: resourceGroupName,
		identityName:      resourceName,
		credentialName:    federatedIdentityCredentialResourceName,
	})
	if err := f.errFor(federatedIdentityCredentialResourceName, f.getErrs, f.getErr); err != nil {
		return armmsi.FederatedIdentityCredentialsClientGetResponse{}, err
	}
	if cred, ok := f.existing[federatedIdentityCredentialResourceName]; ok {
		return armmsi.FederatedIdentityCredentialsClientGetResponse{FederatedIdentityCredential: cred}, nil
	}
	return armmsi.FederatedIdentityCredentialsClientGetResponse{}, ficResourceNotFoundErr()
}

func ficResourceNotFoundErr() error {
	return &azcore.ResponseError{
		ErrorCode:  "NotFound",
		StatusCode: http.StatusNotFound,
	}
}

type fakeManagedIdentitiesDataplaneClient struct {
	creds *dataplane.ManagedIdentityCredentials
	err   error
}

func (f *fakeManagedIdentitiesDataplaneClient) GetUserAssignedIdentitiesCredentials(_ context.Context, _ dataplane.UserAssignedIdentitiesRequest) (*dataplane.ManagedIdentityCredentials, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.creds, nil
}

type fakeFPAMIDataplaneClientBuilder struct {
	client   azureclient.ManagedIdentitiesDataplaneClient
	buildErr error
}

func (b *fakeFPAMIDataplaneClientBuilder) BuilderType() azureclient.FPAMIDataplaneClientBuilderType {
	return azureclient.FPAMIDataplaneClientBuilderTypeValue
}

func (b *fakeFPAMIDataplaneClientBuilder) ManagedIdentitiesDataplane(_ string) (azureclient.ManagedIdentitiesDataplaneClient, error) {
	if b.buildErr != nil {
		return nil, b.buildErr
	}
	return b.client, nil
}

func testSMIDataplaneBuilder(smiResourceID *azcorearm.ResourceID, exists bool) azureclient.FPAMIDataplaneClientBuilder {
	cred := dataplane.UserAssignedIdentityCredentials{
		ResourceID: ptr.To(smiResourceID.String()),
	}
	if exists {
		cred.ClientID = ptr.To("smi-client-id")
		cred.ClientSecret = ptr.To("smi-client-secret")
		cred.TenantID = ptr.To("smi-tenant-id")
		cred.AuthenticationEndpoint = ptr.To("https://login.microsoftonline.com/")
	}
	return &fakeFPAMIDataplaneClientBuilder{
		client: &fakeManagedIdentitiesDataplaneClient{
			creds: &dataplane.ManagedIdentityCredentials{
				ExplicitIdentities: []dataplane.UserAssignedIdentityCredentials{cred},
			},
		},
	}
}

func testClusterServiceID() *metadataapi.InternalID {
	id := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/" + testCSClusterID))
	return &id
}

func testDataPlaneOIDCFederationIdentitiesConfig() *azure.ClusterScopedIdentitiesConfig {
	return azure.NewClusterScopedIdentitiesConfig(azure.RoleDefinitionConfigSetNameDev)
}

func expectedFICResourceIDs(t *testing.T, identity *azcorearm.ResourceID, operatorName string, serviceAccounts []*azure.KubernetesServiceAccount) []*azcorearm.ResourceID {
	t.Helper()
	var ids []*azcorearm.ResourceID
	for _, sa := range serviceAccounts {
		ficResourceID, err := federatedidentitycredential.GenerateFederatedIdentityCredentialResourceID(
			identity,
			testCSClusterID,
			operatorName,
			sa.Namespace,
			sa.Name,
		)
		require.NoError(t, err)
		ids = append(ids, ficResourceID)
	}
	return ids
}

func diskCSIDriverServiceAccounts(t *testing.T) []*azure.KubernetesServiceAccount {
	t.Helper()
	operator := testDataPlaneOIDCFederationIdentitiesConfig().DataPlaneOperatorsIdentities[azure.ClusterOperatorIdentifierDiskCSIDriver]
	require.NotNil(t, operator)
	return operator.KubernetesServiceAccounts
}

func imageRegistryServiceAccounts(t *testing.T) []*azure.KubernetesServiceAccount {
	t.Helper()
	operator := testDataPlaneOIDCFederationIdentitiesConfig().DataPlaneOperatorsIdentities[azure.ClusterOperatorIdentifierImageRegistry]
	require.NotNil(t, operator)
	return operator.KubernetesServiceAccounts
}

func resourceIDStrings(ids []*azcorearm.ResourceID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, strings.ToLower(id.String()))
	}
	return out
}

func createdFICNames(calls []recordedFICCall) []string {
	out := make([]string, 0, len(calls))
	for _, call := range calls {
		out = append(out, call.credentialName)
	}
	return out
}

func TestDataPlaneOIDCFederationNeedsWork(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	future := metav1.NewTime(now.Add(time.Hour))
	since := metav1.NewTime(now)
	sinceElapsed := metav1.NewTime(now.Add(-dataPlaneOIDCFederationDeconfigureDelay))
	keyA := coreapi.ManagedIdentityDataplaneOIDCFederationKey{
		ResourceID:  "/subscriptions/" + testSubscriptionID + "/resourcegroups/" + testResourceGroupName + "/providers/microsoft.managedidentity/userassignedidentities/identity-a",
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}

	testCases := []struct {
		name              string
		federation        map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus
		cluster           *coreapi.HCPOpenShiftCluster
		expectedNeedsWork bool
	}{
		{
			name:              "empty federation does not need work",
			expectedNeedsWork: false,
		},
		{
			name: "PendingConfigure needs work",
			federation: map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: {Phase: coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure},
			},
			expectedNeedsWork: true,
		},
		{
			name: "PendingDeconfigure with nil DeconfigureTimestamp needs work when the cluster is being deleted",
			federation: map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: {Phase: coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure},
			},
			cluster: &coreapi.HCPOpenShiftCluster{
				ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
					DeletionTimestamp: &future,
				},
			},
			expectedNeedsWork: true,
		},
		{
			name: "Configured with nil recheck needs work",
			federation: map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: {Phase: coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured},
			},
			expectedNeedsWork: true,
		},
		{
			name: "PendingConfigure with future recheck still needs work",
			federation: map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: {
					Phase:               coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure,
					EarliestRecheckTime: &future,
				},
			},
			expectedNeedsWork: true,
		},
		{
			name: "PendingDeconfigure with recent DeconfigureTimestamp does not need work",
			federation: map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: {
					Phase:                coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure,
					DeconfigureTimestamp: &since,
				},
			},
			expectedNeedsWork: false,
		},
		{
			name: "PendingDeconfigure with recent DeconfigureTimestamp needs work when the cluster is being deleted",
			federation: map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: {
					Phase:                coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure,
					DeconfigureTimestamp: &since,
				},
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
			federation: map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: {
					Phase:                coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure,
					DeconfigureTimestamp: &sinceElapsed,
				},
			},
			expectedNeedsWork: true,
		},
		{
			name: "PendingDeconfigure with future EarliestRecheckTime needs work after DeconfigureTimestamp elapsed",
			federation: map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: {
					Phase:                coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure,
					DeconfigureTimestamp: &sinceElapsed,
					EarliestRecheckTime:  &future,
				},
			},
			expectedNeedsWork: true,
		},
		{
			name: "Configured with future recheck does not need work when the desired set is unchanged",
			federation: map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: {
					Phase:               coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured,
					EarliestRecheckTime: &future,
				},
			},
			expectedNeedsWork: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			syncer := &dataPlaneOIDCFederationSyncer{
				clock: clocktesting.NewFakePassiveClock(now),
			}
			cluster := tc.cluster
			if cluster == nil {
				cluster = &coreapi.HCPOpenShiftCluster{}
			}
			serviceProviderCluster := &coreapi.ServiceProviderCluster{}
			serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = tc.federation
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
		{
			name:     "empty base URL returns empty issuer",
			tenantID: "tenant-a",
			csID:     "cs-cluster-abc",
		},
		{
			name:    "empty tenant ID returns empty issuer",
			baseURL: "https://oidc.example.com/",
			csID:    "cs-cluster-abc",
		},
		{
			name:     "empty CS cluster ID returns empty issuer",
			baseURL:  "https://oidc.example.com/",
			tenantID: "tenant-a",
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

func TestDataPlaneOIDCFederationSyncOnceConfiguresFICsForOperatorServiceAccounts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := coreapi.ManagedIdentityDataplaneOIDCFederationKey{
		ResourceID:  strings.ToLower(identityA.String()),
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}

	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()

	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: {Phase: coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure},
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
	assert.Equal(t, coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured, got.Phase)
	assert.Empty(t, got.PendingAzureResources)
	assert.ElementsMatch(t, resourceIDStrings(expectedFICResourceIDs(t, identityA, testDiskCSIOperator, serviceAccounts)), resourceIDStrings(got.AzureResources))
	require.NotNil(t, got.EarliestRecheckTime)
	assert.True(t, got.EarliestRecheckTime.After(now))
}

func TestDataPlaneOIDCFederationSyncOnceConfiguresFICsForMultipleOperatorsSharingIdentity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := coreapi.ManagedIdentityDataplaneOIDCFederationKey{
		ResourceID:  strings.ToLower(identityA.String()),
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}

	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
		testImageRegistryOp: identityA,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()

	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: {Phase: coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure},
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
	assert.Equal(t, coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured, got.Phase)

	expected := append(
		expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskSAs),
		expectedFICResourceIDs(t, identityA, testImageRegistryOp, imageSAs)...,
	)
	assert.ElementsMatch(t, resourceIDStrings(expected), resourceIDStrings(got.AzureResources))
}

func TestDataPlaneOIDCFederationSyncOnceDeconfiguresFICsForOperatorServiceAccounts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := coreapi.ManagedIdentityDataplaneOIDCFederationKey{
		ResourceID:  strings.ToLower(identityA.String()),
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}

	tracked := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))
	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()

	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: {
			Phase:                coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure,
			DeconfigureTimestamp: &metav1.Time{Time: now.Add(-dataPlaneOIDCFederationDeconfigureDelay)},
			AzureResources:       tracked,
		},
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
	require.NotNil(t, got)
	assert.Equal(t, coreapi.ManagedIdentityDataplaneOIDCFederationPhaseDeconfigured, got.Phase)
	assert.Empty(t, got.PendingAzureResources)
	assert.Empty(t, got.AzureResources)
}

func TestDataPlaneOIDCFederationSyncOnceDeconfigureHonorsLiveClusterDelay(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	since := metav1.NewTime(now)
	deletionTimestamp := metav1.NewTime(now)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := coreapi.ManagedIdentityDataplaneOIDCFederationKey{
		ResourceID:  strings.ToLower(identityA.String()),
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}
	tracked := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))

	testCases := []struct {
		name              string
		deleting          bool
		wantDeletes       bool
		wantPhase         coreapi.ManagedIdentityDataplaneOIDCFederationPhase
		wantAzureResource bool
	}{
		{
			name:              "live cluster waits 24h after DeconfigureTimestamp",
			wantPhase:         coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure,
			wantAzureResource: true,
		},
		{
			name:        "cluster deletion deconfigures immediately",
			deleting:    true,
			wantDeletes: true,
			wantPhase:   coreapi.ManagedIdentityDataplaneOIDCFederationPhaseDeconfigured,
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
			serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: {
					Phase:                coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure,
					AzureResources:       tracked,
					DeconfigureTimestamp: &since,
				},
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
			require.NotNil(t, got)
			assert.Equal(t, tc.wantPhase, got.Phase)
			if tc.wantAzureResource {
				assert.ElementsMatch(t, resourceIDStrings(tracked), resourceIDStrings(got.AzureResources))
			} else {
				assert.Empty(t, got.AzureResources)
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
	keyA := coreapi.ManagedIdentityDataplaneOIDCFederationKey{
		ResourceID:  strings.ToLower(identityA.String()),
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}

	tracked := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))
	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, nil)
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()

	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: {
			Phase:                coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure,
			DeconfigureTimestamp: &metav1.Time{Time: now.Add(-dataPlaneOIDCFederationDeconfigureDelay)},
			AzureResources:       tracked,
		},
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
	require.NotNil(t, got)
	assert.Equal(t, coreapi.ManagedIdentityDataplaneOIDCFederationPhaseDeconfigured, got.Phase)
	assert.Empty(t, got.PendingAzureResources)
	assert.Empty(t, got.AzureResources)
}

func TestDataPlaneOIDCFederationSyncOnceConfigureErrorIsReturned(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := coreapi.ManagedIdentityDataplaneOIDCFederationKey{
		ResourceID:  strings.ToLower(identityA.String()),
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}

	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()

	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: {Phase: coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure},
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
	assert.Equal(t, coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure, got.Phase)
	assert.Empty(t, got.AzureResources)
	assert.ElementsMatch(t, resourceIDStrings(expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))), resourceIDStrings(got.PendingAzureResources))
}

func TestDataPlaneOIDCFederationSyncOnceConfigurePersistsPartialAzureSuccess(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := coreapi.ManagedIdentityDataplaneOIDCFederationKey{
		ResourceID:  strings.ToLower(identityA.String()),
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}

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
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: {Phase: coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure},
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
	assert.Equal(t, coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure, got.Phase)
	assert.Nil(t, got.EarliestRecheckTime)
	assert.ElementsMatch(t, resourceIDStrings(succeedingIDs), resourceIDStrings(got.AzureResources))
	assert.ElementsMatch(t, resourceIDStrings(failingIDs), resourceIDStrings(got.PendingAzureResources))
}

func TestDataPlaneOIDCFederationSyncOnceConfigureContinuesDeletesAfterCreateFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := coreapi.ManagedIdentityDataplaneOIDCFederationKey{
		ResourceID:  strings.ToLower(identityA.String()),
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}

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
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: {
			Phase:          coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured,
			AzureResources: tracked,
		},
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
	assert.Equal(t, coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured, got.Phase)
	assert.Nil(t, got.EarliestRecheckTime)
	assert.Empty(t, got.PendingAzureResources)
	assert.ElementsMatch(t, resourceIDStrings(desired), resourceIDStrings(got.AzureResources))
}

func TestDataPlaneOIDCFederationSyncOnceDeconfigurePersistsPartialAzureDeletes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := coreapi.ManagedIdentityDataplaneOIDCFederationKey{
		ResourceID:  strings.ToLower(identityA.String()),
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}

	tracked := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))
	require.GreaterOrEqual(t, len(tracked), 2)
	failingName := tracked[0].Name
	remaining := []*azcorearm.ResourceID{tracked[0]}

	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, nil)
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: {
			Phase:                coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure,
			DeconfigureTimestamp: &metav1.Time{Time: now.Add(-dataPlaneOIDCFederationDeconfigureDelay)},
			AzureResources:       tracked,
		},
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
	assert.Equal(t, coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure, got.Phase)
	assert.Empty(t, got.PendingAzureResources)
	assert.ElementsMatch(t, resourceIDStrings(remaining), resourceIDStrings(got.AzureResources))
}

func TestDataPlaneOIDCFederationSyncOncePersistsPendingBeforeConfigureClientBuildFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := coreapi.ManagedIdentityDataplaneOIDCFederationKey{
		ResourceID:  strings.ToLower(identityA.String()),
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}

	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()

	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: {Phase: coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure},
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
	assert.Equal(t, coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure, got.Phase)
	assert.ElementsMatch(t, resourceIDStrings(expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))), resourceIDStrings(got.PendingAzureResources))
}

func TestDataPlaneOIDCFederationSyncOnceDeconfigureAzureFailureLeavesTrackedResources(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := coreapi.ManagedIdentityDataplaneOIDCFederationKey{
		ResourceID:  strings.ToLower(identityA.String()),
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}

	tracked := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))
	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, nil)
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: {
			Phase:                coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure,
			DeconfigureTimestamp: &metav1.Time{Time: now.Add(-dataPlaneOIDCFederationDeconfigureDelay)},
			AzureResources:       tracked,
		},
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
	assert.Equal(t, coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure, got.Phase)
	assert.Empty(t, got.PendingAzureResources)
	assert.ElementsMatch(t, resourceIDStrings(tracked), resourceIDStrings(got.AzureResources))
}

func TestDataPlaneOIDCFederationSyncOnceSkipsConfigureWhenClusterServiceIDMissing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := coreapi.ManagedIdentityDataplaneOIDCFederationKey{
		ResourceID:  strings.ToLower(identityA.String()),
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}

	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: {Phase: coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure},
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
	assert.Equal(t, coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure, got.Phase)
}

func TestDataPlaneOIDCFederationSyncOnceDeconfiguresTrackedFICsWhenClusterServiceIDMissing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := coreapi.ManagedIdentityDataplaneOIDCFederationKey{
		ResourceID:  strings.ToLower(identityA.String()),
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}

	tracked := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))
	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, nil)
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: {
			Phase:                coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure,
			DeconfigureTimestamp: &metav1.Time{Time: now.Add(-dataPlaneOIDCFederationDeconfigureDelay)},
			AzureResources:       tracked,
		},
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
	require.NotNil(t, got)
	assert.Equal(t, coreapi.ManagedIdentityDataplaneOIDCFederationPhaseDeconfigured, got.Phase)
	assert.Empty(t, got.PendingAzureResources)
	assert.Empty(t, got.AzureResources)
}

func TestDataPlaneOIDCFederationSyncOnceDeconfiguresWhenServiceManagedIdentityMissingInAzure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := coreapi.ManagedIdentityDataplaneOIDCFederationKey{
		ResourceID:  strings.ToLower(identityA.String()),
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}

	tracked := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))
	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, nil)
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: {
			Phase:                coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure,
			DeconfigureTimestamp: &metav1.Time{Time: now.Add(-dataPlaneOIDCFederationDeconfigureDelay)},
			AzureResources:       tracked,
		},
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
	require.NotNil(t, got)
	assert.Equal(t, coreapi.ManagedIdentityDataplaneOIDCFederationPhaseDeconfigured, got.Phase)
	assert.Empty(t, got.PendingAzureResources)
	assert.Empty(t, got.AzureResources)
}

func TestDataPlaneOIDCFederationSyncOnceNilServiceManagedIdentity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := coreapi.ManagedIdentityDataplaneOIDCFederationKey{
		ResourceID:  strings.ToLower(identityA.String()),
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}

	cluster := newTestClusterWithIdentities(t, testClusterName, nil, nil)
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: {Phase: coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure},
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
	keyA := coreapi.ManagedIdentityDataplaneOIDCFederationKey{
		ResourceID:  strings.ToLower(identityA.String()),
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}

	cluster := newTestClusterWithIdentities(t, testClusterName, nil, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()

	syncer := &dataPlaneOIDCFederationSyncer{
		clock:                         clocktesting.NewFakePassiveClock(now),
		clusterScopedIdentitiesConfig: testDataPlaneOIDCFederationIdentitiesConfig(),
	}
	serviceProviderCluster := &coreapi.ServiceProviderCluster{}
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: {
			Phase:               coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured,
			EarliestRecheckTime: &future,
		},
	}
	assert.True(t, syncer.needsWork(cluster, serviceProviderCluster), "desired FIC set differs from empty AzureResources")
}

func TestDataPlaneOIDCFederationSyncOnceConfiguredGetMatchingDoesNotCreate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := coreapi.ManagedIdentityDataplaneOIDCFederationKey{
		ResourceID:  strings.ToLower(identityA.String()),
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}

	tracked := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))
	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: {
			Phase:          coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured,
			AzureResources: tracked,
		},
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
	assert.Equal(t, coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured, got.Phase)
	require.NotNil(t, got.EarliestRecheckTime)
	assert.True(t, got.EarliestRecheckTime.After(now))
}

func TestDataPlaneOIDCFederationSyncOnceConfiguredCreateOrUpdateWhenGetNotFound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := coreapi.ManagedIdentityDataplaneOIDCFederationKey{
		ResourceID:  strings.ToLower(identityA.String()),
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}

	tracked := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))
	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: {
			Phase:          coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured,
			AzureResources: tracked,
		},
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
	keyA := coreapi.ManagedIdentityDataplaneOIDCFederationKey{
		ResourceID:  strings.ToLower(identityA.String()),
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}

	tracked := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))
	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: {
			Phase:          coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured,
			AzureResources: tracked,
		},
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
	keyA := coreapi.ManagedIdentityDataplaneOIDCFederationKey{
		ResourceID:  strings.ToLower(identityA.String()),
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}

	tracked := expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))
	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: {
			Phase:               coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured,
			AzureResources:      tracked,
			EarliestRecheckTime: &future,
		},
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
	keyA := coreapi.ManagedIdentityDataplaneOIDCFederationKey{
		ResourceID:  strings.ToLower(identityA.String()),
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}

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
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: {
			Phase:          coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured,
			AzureResources: tracked,
		},
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
	assert.ElementsMatch(t, resourceIDStrings(desired), resourceIDStrings(got.AzureResources))
}

func matchingDiskCSIFICs(t *testing.T, identity *azcorearm.ResourceID) map[string]armmsi.FederatedIdentityCredential {
	t.Helper()
	existing := map[string]armmsi.FederatedIdentityCredential{}
	for _, sa := range diskCSIDriverServiceAccounts(t) {
		name := federatedidentitycredential.GenerateFederatedIdentityCredentialName(testCSClusterID, testDiskCSIOperator, sa.Namespace, sa.Name)
		existing[name] = armmsi.FederatedIdentityCredential{
			Properties: &armmsi.FederatedIdentityCredentialProperties{
				Issuer:    ptr.To(testOIDCIssuerURL),
				Subject:   ptr.To(sa.AsOIDCSubject()),
				Audiences: []*string{ptr.To(dataPlaneOIDCFederationAudience)},
			},
		}
	}
	return existing
}

func derefStrings(values []*string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, ptr.Deref(value, ""))
	}
	return out
}

// newTestClusterWithIdentities builds an HCPOpenShiftCluster addressable by the mock
// ResourcesDBClient with the supplied ServiceManagedIdentity and data plane operator
// identities on its CustomerProperties.
func newTestClusterWithIdentities(t *testing.T, clusterName string, serviceManagedIdentity *azcorearm.ResourceID, dataPlaneOperators map[string]*azcorearm.ResourceID) *coreapi.HCPOpenShiftCluster {
	t.Helper()

	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName,
	))

	cluster := &coreapi.HCPOpenShiftCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: clusterName,
				Type: resourceID.ResourceType.String(),
			},
		},
	}
	cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity = serviceManagedIdentity
	cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators = dataPlaneOperators

	return cluster
}

// newTestServiceProviderClusterWithIdentities builds a ServiceProviderCluster addressable
// by the mock ResourcesDBClient with the supplied resolved identities and recheck time.
func newTestServiceProviderClusterWithIdentities(clusterName string, identities map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity, earliestRecheckTime *metav1.Time) *coreapi.ServiceProviderCluster {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/" + coreapi.ServiceProviderClusterResourceTypeName +
			"/" + coreapi.ServiceProviderClusterResourceName,
	))

	serviceProviderCluster := &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
	}
	serviceProviderCluster.Status.DataPlaneOperatorsManagedIdentities.Identities = identities
	serviceProviderCluster.Status.DataPlaneOperatorsManagedIdentities.EarliestRecheckTime = earliestRecheckTime

	return serviceProviderCluster
}
