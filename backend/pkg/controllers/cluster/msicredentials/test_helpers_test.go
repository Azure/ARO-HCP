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

package msicredentials

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	"github.com/Azure/msi-dataplane/pkg/dataplane"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/azure"
)

const (
	testSubscriptionID    = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName = "test-rg"
	testClusterName       = "test-cluster"
	testManagedRGName     = "test-managed-rg"
	testCSClusterID       = "cs-cluster-abc"
	testStampIdentifier   = "stamp-1"
	testKeyVaultURL       = "https://kv-mi.vault.azure.net/"
	testIdentityURL       = "https://identity.example.com/"
	testOperatorName      = string(azure.ClusterOperatorIdentifierControlPlane)
	testPrincipalID       = "cp-principal-11111111-1111-1111-1111-111111111111"
	testClientID          = "cp-client-11111111-1111-1111-1111-111111111111"
	testTenantID          = "tenant"
	testHardcodedSecret   = "c2VjcmV0"
)

func testIdentityResourceID() *azcorearm.ResourceID {
	return metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.ManagedIdentity/userAssignedIdentities/cp-identity"))
}

func testClusterServiceID() *metadataapi.InternalID {
	id := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/" + testCSClusterID))
	return &id
}

func testClusterResourceID() *azcorearm.ResourceID {
	return metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName))
}

func testSPCResourceID() *azcorearm.ResourceID {
	return metadataapi.Must(azcorearm.ParseResourceID(
		testClusterResourceID().String() + "/" + coreapi.ServiceProviderClusterResourceTypeName + "/" + coreapi.ServiceProviderClusterResourceName))
}

func testManagementClusterResourceID() *azcorearm.ResourceID {
	return metadataapi.Must(fleetapi.ToManagementClusterResourceID(testStampIdentifier))
}

func testConfig() *azure.ClusterScopedIdentitiesConfig {
	return azure.NewClusterScopedIdentitiesConfig(azure.RoleDefinitionConfigSetNameDev)
}

func testCluster() *coreapi.HCPOpenShiftCluster {
	resourceID := testClusterResourceID()
	cluster := &coreapi.HCPOpenShiftCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(testSubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: testClusterName,
				Type: resourceID.ResourceType.String(),
			},
		},
	}
	cluster.CustomerProperties.Platform.ManagedResourceGroup = testManagedRGName
	cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators = map[string]*azcorearm.ResourceID{
		testOperatorName: testIdentityResourceID(),
	}
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()
	cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL = testIdentityURL
	cluster.ServiceProviderProperties.UsesNewClusterDeletionApproach = true
	return cluster
}

func testServiceProviderCluster(roleAssignmentIDs []*azcorearm.ResourceID) *coreapi.ServiceProviderCluster {
	identityID := testIdentityResourceID()
	return &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   testSPCResourceID(),
			PartitionKey: strings.ToLower(testSubscriptionID),
		},
		Status: coreapi.ServiceProviderClusterStatus{
			ManagementClusterResourceID: testManagementClusterResourceID(),
			ManagedIdentityDetails: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityID.String()): {
					ResourceID: identityID,
					MetadataFromManagedIdentitiesDataplaneService: &coreapi.IdentityMetadataValue{
						ClientID:    ptr.To(testClientID),
						PrincipalID: ptr.To(testPrincipalID),
						TenantID:    ptr.To(testTenantID),
					},
				},
			},
			AzureResources: coreapi.AzureResources{
				ManagedResourceGroup: coreapi.AzureReference{
					AzureResource: metadataapi.Must(coreapi.ToResourceGroupResourceID(testSubscriptionID, testManagedRGName)),
				},
				RoleAssignments: coreapi.AzureMultiReference{
					AzureResources: roleAssignmentIDs,
				},
			},
		},
	}
}

func testServiceProviderClusterWithHardcodedIdentity(roleAssignmentIDs []*azcorearm.ResourceID) *coreapi.ServiceProviderCluster {
	spc := testServiceProviderCluster(roleAssignmentIDs)
	identityID := testIdentityResourceID()
	spc.Status.ManagedIdentityDetails[strings.ToLower(identityID.String())] = &coreapi.ManagedIdentityMetadata{
		ResourceID: identityID,
		MetadataFromHardcodedIdentity: &coreapi.IdentityMetadataValue{
			ClientID:    ptr.To(testClientID),
			PrincipalID: ptr.To(testPrincipalID),
			TenantID:    ptr.To(testTenantID),
		},
	}
	return spc
}

func testHardcodedIdentity() *azureclient.HardcodedIdentity {
	return &azureclient.HardcodedIdentity{
		ClientID:     testClientID,
		ClientSecret: testHardcodedSecret,
		PrincipalID:  testPrincipalID,
		TenantID:     testTenantID,
	}
}

func testManagementCluster() *fleetapi.ManagementCluster {
	return &fleetapi.ManagementCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID: testManagementClusterResourceID(),
		},
		Status: fleetapi.ManagementClusterStatus{
			HostedClustersManagedIdentitiesKeyVaultURL: testKeyVaultURL,
		},
	}
}

func testUACredentials(resourceID string) dataplane.UserAssignedIdentityCredentials {
	now := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	return dataplane.UserAssignedIdentityCredentials{
		ClientID:               ptr.To(testClientID),
		ClientSecret:           ptr.To("c2VjcmV0"),
		TenantID:               ptr.To(testTenantID),
		ResourceID:             ptr.To(resourceID),
		AuthenticationEndpoint: ptr.To("https://login.microsoftonline.com/"),
		ClientSecretURL:        ptr.To("https://example.com/secret"),
		NotBefore:              ptr.To(now.Add(-time.Hour).Format(time.RFC3339)),
		NotAfter:               ptr.To(now.Add(24 * time.Hour).Format(time.RFC3339)),
		RenewAfter:             ptr.To(now.Add(12 * time.Hour).Format(time.RFC3339)),
		CannotRenewAfter:       ptr.To(now.Add(48 * time.Hour).Format(time.RFC3339)),
		ObjectID:               ptr.To(testPrincipalID),
	}
}

func expectedOperatorRoleAssignmentIDs(t *testing.T) []*azcorearm.ResourceID {
	t.Helper()
	ids, err := expectedControlPlaneOperatorRoleAssignmentIDs(
		testCluster(),
		testConfig(),
		testOperatorName,
		testPrincipalID,
	)
	require.NoError(t, err)
	require.NotEmpty(t, ids)
	return ids
}

func expectedSecretName() string {
	return dataplane.IdentifierForUserAssignedIdentityCredentials(secretNameIdentifier(testCSClusterID, testOperatorName))
}

type fakeManagedIdentitiesDataplaneClient struct {
	creds *dataplane.ManagedIdentityCredentials
	err   error
}

func (f *fakeManagedIdentitiesDataplaneClient) GetUserAssignedIdentitiesCredentials(_ context.Context, _ dataplane.UserAssignedIdentitiesRequest) (*dataplane.ManagedIdentityCredentials, error) {
	return f.creds, f.err
}

type fakeFPAMIDataplaneClientBuilder struct {
	client azureclient.ManagedIdentitiesDataplaneClient
	err    error
}

func (b *fakeFPAMIDataplaneClientBuilder) BuilderType() azureclient.FPAMIDataplaneClientBuilderType {
	return azureclient.FPAMIDataplaneClientBuilderTypeValue
}

func (b *fakeFPAMIDataplaneClientBuilder) ManagedIdentitiesDataplane(_ string) (azureclient.ManagedIdentitiesDataplaneClient, error) {
	if b.err != nil {
		return nil, b.err
	}
	return b.client, nil
}

type recordedSecretCall struct {
	name       string
	parameters azsecrets.SetSecretParameters
}

type fakeKeyVaultSecretsClient struct {
	setCalls     []recordedSecretCall
	deleteCalls  []string
	recoverCalls []string
	setErr       error
	deleteErr    error
}

func (f *fakeKeyVaultSecretsClient) SetSecret(_ context.Context, name string, parameters azsecrets.SetSecretParameters, _ *azsecrets.SetSecretOptions) (azsecrets.SetSecretResponse, error) {
	f.setCalls = append(f.setCalls, recordedSecretCall{name: name, parameters: parameters})
	if f.setErr != nil {
		return azsecrets.SetSecretResponse{}, f.setErr
	}
	return azsecrets.SetSecretResponse{}, nil
}

func (f *fakeKeyVaultSecretsClient) DeleteSecret(_ context.Context, name string, _ *azsecrets.DeleteSecretOptions) (azsecrets.DeleteSecretResponse, error) {
	f.deleteCalls = append(f.deleteCalls, name)
	if f.deleteErr != nil {
		return azsecrets.DeleteSecretResponse{}, f.deleteErr
	}
	return azsecrets.DeleteSecretResponse{}, nil
}

func (f *fakeKeyVaultSecretsClient) GetSecret(_ context.Context, _ string, _ string, _ *azsecrets.GetSecretOptions) (azsecrets.GetSecretResponse, error) {
	return azsecrets.GetSecretResponse{}, nil
}

func (f *fakeKeyVaultSecretsClient) RecoverDeletedSecret(_ context.Context, name string, _ *azsecrets.RecoverDeletedSecretOptions) (azsecrets.RecoverDeletedSecretResponse, error) {
	f.recoverCalls = append(f.recoverCalls, name)
	return azsecrets.RecoverDeletedSecretResponse{}, nil
}

type fakeKeyVaultSecretsClientBuilder struct {
	client azureclient.KeyVaultSecretsClient
	err    error
}

func (b *fakeKeyVaultSecretsClientBuilder) BuilderType() azureclient.KeyVaultSecretsClientBuilderType {
	return azureclient.KeyVaultSecretsClientBuilderTypeValue
}

func (b *fakeKeyVaultSecretsClientBuilder) SecretsClient(_ string) (azureclient.KeyVaultSecretsClient, error) {
	if b.err != nil {
		return nil, b.err
	}
	return b.client, nil
}

func testNow() time.Time {
	return time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
}

func futureTime() *metav1.Time {
	t := metav1.NewTime(testNow().Add(time.Hour))
	return &t
}

func pastTime() *metav1.Time {
	t := metav1.NewTime(testNow().Add(-time.Hour))
	return &t
}
