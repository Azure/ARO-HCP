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

package operationcontrollers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"

	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/database"
	dblistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testStampIdentifier    = "test-stamp-1"
	testKeyVaultURL        = "https://kv-hc-secrets.vault.azure.net"
	testKeyVaultMIClientID = "22222222-2222-2222-2222-222222222222"
)

type fakeKeyVaultSecretClient struct {
	secrets map[string]string
}

func newFakeKeyVaultSecretClient() *fakeKeyVaultSecretClient {
	return &fakeKeyVaultSecretClient{secrets: make(map[string]string)}
}

func (f *fakeKeyVaultSecretClient) GetSecret(_ context.Context, name string, _ string, _ *azsecrets.GetSecretOptions) (azsecrets.GetSecretResponse, error) {
	v, ok := f.secrets[name]
	if !ok {
		return azsecrets.GetSecretResponse{}, fmt.Errorf("secret %q not found", name)
	}
	return azsecrets.GetSecretResponse{Secret: azsecrets.Secret{Value: &v}}, nil
}

func (f *fakeKeyVaultSecretClient) SetSecret(_ context.Context, name string, parameters azsecrets.SetSecretParameters, _ *azsecrets.SetSecretOptions) (azsecrets.SetSecretResponse, error) {
	if parameters.Value != nil {
		f.secrets[name] = *parameters.Value
	}
	return azsecrets.SetSecretResponse{}, nil
}

func (f *fakeKeyVaultSecretClient) DeleteSecret(_ context.Context, name string, _ *azsecrets.DeleteSecretOptions) (azsecrets.DeleteSecretResponse, error) {
	delete(f.secrets, name)
	return azsecrets.DeleteSecretResponse{}, nil
}

func (f *fakeKeyVaultSecretClient) NewListSecretPropertiesPager(_ *azsecrets.ListSecretPropertiesOptions) *runtime.Pager[azsecrets.ListSecretPropertiesResponse] {
	return runtime.NewPager(runtime.PagingHandler[azsecrets.ListSecretPropertiesResponse]{
		More: func(azsecrets.ListSecretPropertiesResponse) bool { return false },
		Fetcher: func(_ context.Context, _ *azsecrets.ListSecretPropertiesResponse) (azsecrets.ListSecretPropertiesResponse, error) {
			return azsecrets.ListSecretPropertiesResponse{}, nil
		},
	})
}

var _ azureclient.KeyVaultSecretClient = (*fakeKeyVaultSecretClient)(nil)

type fakeKeyVaultSecretClientFactory struct {
	client *fakeKeyVaultSecretClient
}

func (f *fakeKeyVaultSecretClientFactory) KeyVaultSecretClient(_ string, _ string) (azureclient.KeyVaultSecretClient, error) {
	return f.client, nil
}

var _ azureclient.KeyVaultSecretClientFactory = (*fakeKeyVaultSecretClientFactory)(nil)

func newTestServiceProviderCluster(clusterResourceID *azcorearm.ResourceID, managementClusterResourceID *azcorearm.ResourceID) *api.ServiceProviderCluster {
	spcResourceID := api.Must(azcorearm.ParseResourceID(fmt.Sprintf("%s/%s/%s",
		clusterResourceID.String(),
		api.ServiceProviderClusterResourceTypeName,
		api.ServiceProviderClusterResourceName,
	)))
	return &api.ServiceProviderCluster{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   spcResourceID,
			PartitionKey: strings.ToLower(spcResourceID.SubscriptionID),
		},
		Status: api.ServiceProviderClusterStatus{
			ManagementClusterResourceID: managementClusterResourceID,
		},
	}
}

func newTestManagementClusterForCredential() *fleet.ManagementCluster {
	resourceID := api.Must(fleet.ToManagementClusterResourceID(testStampIdentifier))
	return &fleet.ManagementCluster{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(testStampIdentifier),
		},
		ResourceID: resourceID,
		Status: fleet.ManagementClusterStatus{
			HostedClustersSecretsKeyVaultURL:                     testKeyVaultURL,
			HostedClustersSecretsKeyVaultManagedIdentityClientID: testKeyVaultMIClientID,
		},
	}
}

func TestOperationRequestCredential_ShouldProcess(t *testing.T) {
	tests := []struct {
		name              string
		operationOverride func(*api.Operation)
		expectedResult    bool
	}{
		{
			name:              "Accepted status should be processed",
			operationOverride: func(o *api.Operation) { o.Status = arm.ProvisioningStateAccepted },
			expectedResult:    true,
		},
		{
			name:              "Terminal ProvisioningState should not be processed",
			operationOverride: func(o *api.Operation) { o.Status = arm.ProvisioningStateSucceeded },
			expectedResult:    false,
		},
		{
			name:              "Wrong operation request type should not be processed",
			operationOverride: func(o *api.Operation) { o.Request = database.OperationRequestRevokeCredentials },
			expectedResult:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))

			fixture := newClusterTestFixture()
			operation := fixture.newOperation(database.OperationRequestRequestCredential)
			operation.Status = arm.ProvisioningStateAccepted
			if tt.operationOverride != nil {
				tt.operationOverride(operation)
			}

			controller := &operationRequestCredential{}
			result := controller.ShouldProcess(ctx, operation)
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

func TestOperationRequestCredential_SynchronizeOperation(t *testing.T) {
	managementClusterResourceID := api.Must(fleet.ToManagementClusterResourceID(testStampIdentifier))
	managementCluster := newTestManagementClusterForCredential()

	tests := []struct {
		name                       string
		operationOverride          func(*api.Operation)
		breakGlassCredentialStatus cmv1.BreakGlassCredentialStatus
		getBreakGlassCredentialErr error
		expectError                bool
		expectCSMockCalled         bool
		verify                     func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture, kvClient *fakeKeyVaultSecretClient)
	}{
		{
			name:                       "created credential updates operation status to provisioning",
			breakGlassCredentialStatus: cmv1.BreakGlassCredentialStatusCreated,
			expectError:                false,
			expectCSMockCalled:         true,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture, kvClient *fakeKeyVaultSecretClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateProvisioning, op.Status)
			},
		},
		{
			name:                       "failed credential updates operation status to failed",
			breakGlassCredentialStatus: cmv1.BreakGlassCredentialStatusFailed,
			expectError:                false,
			expectCSMockCalled:         true,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture, kvClient *fakeKeyVaultSecretClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, op.Status)
				assert.Equal(t, arm.CloudErrorCodeInternalServerError, op.Error.Code)
			},
		},
		{
			name:                       "issued credential creates SystemAdminCredentialRequest and stores kubeconfig in Key Vault",
			breakGlassCredentialStatus: cmv1.BreakGlassCredentialStatusIssued,
			expectError:                false,
			expectCSMockCalled:         true,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture, kvClient *fakeKeyVaultSecretClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)

				credName := strings.ToLower(testOperationName)
				credResourceID := api.Must(api.ToSystemAdminCredentialRequestResourceID(
					testSubscriptionID, testResourceGroupName, testClusterName, credName,
				))
				creds := db.SystemAdminCredentialRequests(testSubscriptionID, testResourceGroupName, testClusterName)
				cred, err := creds.Get(ctx, credName)
				require.NoError(t, err, "SystemAdminCredentialRequest should exist in Cosmos")
				assert.NotEmpty(t, cred.Status.KeyVaultSecretName, "KeyVaultSecretName should be set")

				expectedSecretName := keyVaultSecretNameForCredential(credResourceID)
				assert.Equal(t, expectedSecretName, cred.Status.KeyVaultSecretName, "KeyVaultSecretName should match hash of resource ID")

				storedKubeconfig, ok := kvClient.secrets[expectedSecretName]
				assert.True(t, ok, "kubeconfig should be stored in Key Vault")
				assert.Equal(t, cred.Status.Kubeconfig, storedKubeconfig, "kubeconfig in Key Vault should match Cosmos")
			},
		},
		{
			name:                       "unhandled BreakGlassCredentialStatus leads to error",
			breakGlassCredentialStatus: "CompleteFantasy",
			expectError:                true,
			expectCSMockCalled:         true,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture, kvClient *fakeKeyVaultSecretClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status) // no state change
			},
		},
		{
			name:                       "GetBreakGlassCredential failure leads to error",
			breakGlassCredentialStatus: cmv1.BreakGlassCredentialStatusIssued,
			getBreakGlassCredentialErr: errors.New("something went wrong"),
			expectError:                true,
			expectCSMockCalled:         true,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture, kvClient *fakeKeyVaultSecretClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status) // no state change
			},
		},
		{
			name:               "ShouldProcess returns false for terminal status and no state change occurs",
			operationOverride:  func(o *api.Operation) { o.Status = arm.ProvisioningStateSucceeded },
			expectError:        false,
			expectCSMockCalled: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture, kvClient *fakeKeyVaultSecretClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status) // no state change
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			fixture := newClusterTestFixture()
			cluster := fixture.newCluster(nil)
			operation := fixture.newOperation(database.OperationRequestRequestCredential)
			if tt.operationOverride != nil {
				tt.operationOverride(operation)
			}

			serviceProviderCluster := newTestServiceProviderCluster(fixture.clusterResourceID, managementClusterResourceID)

			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, operation, serviceProviderCluster})
			require.NoError(t, err)

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)

			if tt.expectCSMockCalled {
				breakGlassCredential, err := cmv1.NewBreakGlassCredential().
					Status(tt.breakGlassCredentialStatus).
					Kubeconfig("test-kubeconfig-data").
					Build()
				require.NoError(t, err)

				mockCSClient.EXPECT().
					GetBreakGlassCredential(gomock.Any(), fixture.clusterInternalID).
					Return(breakGlassCredential, tt.getBreakGlassCredentialErr)
			}

			fakeKVClient := newFakeKeyVaultSecretClient()

			controller := &operationRequestCredential{
				clock:                 utilsclock.RealClock{},
				resourcesDBClient:     mockResourcesDBClient,
				clustersServiceClient: mockCSClient,
				managementClusterLister: &dblistertesting.SliceManagementClusterLister{
					ManagementClusters: []*fleet.ManagementCluster{managementCluster},
				},
				keyVaultSecretClientFactory: &fakeKeyVaultSecretClientFactory{client: fakeKVClient},
			}

			err = controller.SynchronizeOperation(ctx, fixture.operationKey())

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tt.verify != nil {
				tt.verify(t, ctx, mockResourcesDBClient, fixture, fakeKVClient)
			}
		})
	}
}

func TestKeyVaultSecretNameForCredential(t *testing.T) {
	credResourceID1 := api.Must(api.ToSystemAdminCredentialRequestResourceID(
		"sub1", "rg1", "cluster1", "cred1",
	))
	credResourceID2 := api.Must(api.ToSystemAdminCredentialRequestResourceID(
		"sub1", "rg1", "cluster1", "cred2",
	))

	name1 := keyVaultSecretNameForCredential(credResourceID1)
	name2 := keyVaultSecretNameForCredential(credResourceID2)

	assert.NotEqual(t, name1, name2, "different resource IDs should produce different secret names")
	assert.Len(t, name1, 64, "sha256 hex should be 64 characters")
	assert.Regexp(t, `^[a-f0-9]+$`, name1, "secret name should be hex-encoded")

	name1Again := keyVaultSecretNameForCredential(credResourceID1)
	assert.Equal(t, name1, name1Again, "same resource ID should produce the same secret name")
}
