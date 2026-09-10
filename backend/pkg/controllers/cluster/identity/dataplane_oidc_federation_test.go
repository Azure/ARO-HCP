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
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"

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
	createOrUpdateErr error
	deleteErr         error
	getErr            error

	creates []recordedFICCall
	deletes []recordedFICCall
	gets    []recordedFICCall
}

var _ azureclient.FederatedIdentityCredentialsClient = (*fakeFederatedIdentityCredentialsClient)(nil)

func (f *fakeFederatedIdentityCredentialsClient) CreateOrUpdate(_ context.Context, resourceGroupName string, resourceName string, federatedIdentityCredentialResourceName string, parameters armmsi.FederatedIdentityCredential, _ *armmsi.FederatedIdentityCredentialsClientCreateOrUpdateOptions) (armmsi.FederatedIdentityCredentialsClientCreateOrUpdateResponse, error) {
	f.creates = append(f.creates, recordedFICCall{
		resourceGroupName: resourceGroupName,
		identityName:      resourceName,
		credentialName:    federatedIdentityCredentialResourceName,
		credential:        parameters,
	})
	if f.createOrUpdateErr != nil {
		return armmsi.FederatedIdentityCredentialsClientCreateOrUpdateResponse{}, f.createOrUpdateErr
	}
	return armmsi.FederatedIdentityCredentialsClientCreateOrUpdateResponse{}, nil
}

func (f *fakeFederatedIdentityCredentialsClient) Delete(_ context.Context, resourceGroupName string, resourceName string, federatedIdentityCredentialResourceName string, _ *armmsi.FederatedIdentityCredentialsClientDeleteOptions) (armmsi.FederatedIdentityCredentialsClientDeleteResponse, error) {
	f.deletes = append(f.deletes, recordedFICCall{
		resourceGroupName: resourceGroupName,
		identityName:      resourceName,
		credentialName:    federatedIdentityCredentialResourceName,
	})
	if f.deleteErr != nil {
		return armmsi.FederatedIdentityCredentialsClientDeleteResponse{}, f.deleteErr
	}
	return armmsi.FederatedIdentityCredentialsClientDeleteResponse{}, nil
}

func (f *fakeFederatedIdentityCredentialsClient) Get(_ context.Context, resourceGroupName string, resourceName string, federatedIdentityCredentialResourceName string, _ *armmsi.FederatedIdentityCredentialsClientGetOptions) (armmsi.FederatedIdentityCredentialsClientGetResponse, error) {
	f.gets = append(f.gets, recordedFICCall{
		resourceGroupName: resourceGroupName,
		identityName:      resourceName,
		credentialName:    federatedIdentityCredentialResourceName,
	})
	if f.getErr != nil {
		return armmsi.FederatedIdentityCredentialsClientGetResponse{}, f.getErr
	}
	return armmsi.FederatedIdentityCredentialsClientGetResponse{}, nil
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
	keyA := coreapi.ManagedIdentityDataplaneOIDCFederationKey{
		ResourceID:  "/subscriptions/" + testSubscriptionID + "/resourcegroups/" + testResourceGroupName + "/providers/microsoft.managedidentity/userassignedidentities/identity-a",
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}

	testCases := []struct {
		name              string
		federation        map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus
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
			name: "PendingDeconfigure needs work",
			federation: map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: {Phase: coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure},
			},
			expectedNeedsWork: true,
		},
		{
			name: "Configured does not need work",
			federation: map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: {Phase: coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured},
			},
			expectedNeedsWork: false,
		},
		{
			name: "PendingConfigure with future recheck does not need work",
			federation: map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: {
					Phase:               coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure,
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
			serviceProviderCluster := &coreapi.ServiceProviderCluster{}
			serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = tc.federation
			assert.Equal(t, tc.expectedNeedsWork, syncer.needsWork(serviceProviderCluster))
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
	assert.Len(t, fakeClient.gets, 2)

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
	assert.Len(t, fakeClient.gets, len(diskSAs)+len(imageSAs))

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

	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		testDiskCSIOperator: identityA,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()

	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: {Phase: coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure},
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
		clusterScopedIdentitiesConfig: testDataPlaneOIDCFederationIdentitiesConfig(),
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.NoError(t, err)
	assert.Len(t, fakeClient.deletes, 2)
	assert.Empty(t, fakeClient.gets)

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
			Phase:          coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure,
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
		clusterScopedIdentitiesConfig: testDataPlaneOIDCFederationIdentitiesConfig(),
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.NoError(t, err)
	assert.Len(t, fakeClient.deletes, len(tracked))
	assert.Empty(t, fakeClient.gets)

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

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	got := updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyA]
	require.NotNil(t, got)
	assert.Equal(t, coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure, got.Phase)
	assert.ElementsMatch(t, resourceIDStrings(expectedFICResourceIDs(t, identityA, testDiskCSIOperator, diskCSIDriverServiceAccounts(t))), resourceIDStrings(got.PendingAzureResources))
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

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	got := updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyA]
	require.NotNil(t, got)
	assert.Equal(t, coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure, got.Phase)
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

func derefStrings(values []*string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, ptr.Deref(value, ""))
	}
	return out
}
