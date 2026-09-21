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
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/msi-dataplane/pkg/dataplane"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/azure/federatedidentitycredential"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

const (
	testSubscriptionID    = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName = "test-rg"
	testClusterName       = "test-cluster"
)

const (
	testCSClusterID       = "cs-cluster-abc"
	testClusterTenantID   = "tenant-a"
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
	return armmsi.FederatedIdentityCredentialsClientGetResponse{}, buildTestFICResourceNotFoundErr()
}

func buildTestFICResourceNotFoundErr() error {
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

func buildTestSMIDataplaneBuilder(smiResourceID *azcorearm.ResourceID, exists bool) azureclient.FPAMIDataplaneClientBuilder {
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

func buildTestOIDCFederationSubscription() *coreapi.Subscription {
	rid := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID))
	return &coreapi.Subscription{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   rid,
			PartitionKey: strings.ToLower(rid.SubscriptionID),
		},
		Properties: &coreapi.SubscriptionProperties{TenantId: ptr.To(testClusterTenantID)},
	}
}

func buildTestOIDCFederationSubscriptionLister() corelisters.SubscriptionLister {
	return &corelistertesting.SliceSubscriptionLister{
		Subscriptions: []*coreapi.Subscription{buildTestOIDCFederationSubscription()},
	}
}

func buildTestClusterServiceID() *metadataapi.InternalID {
	id := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/" + testCSClusterID))
	return &id
}

func buildTestDataPlaneOIDCFederationIdentitiesConfig() *azure.ClusterScopedIdentitiesConfig {
	return azure.NewClusterScopedIdentitiesConfig(azure.RoleDefinitionConfigSetNameDev)
}

func buildTestFICResourceIDs(t *testing.T, identity *azcorearm.ResourceID, operatorName string, serviceAccounts []*azure.KubernetesServiceAccount) []*azcorearm.ResourceID {
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

func buildTestDiskCSIDriverServiceAccounts(t *testing.T) []*azure.KubernetesServiceAccount {
	t.Helper()
	operator := buildTestDataPlaneOIDCFederationIdentitiesConfig().DataPlaneOperatorsIdentities[azure.ClusterOperatorIdentifierDiskCSIDriver]
	require.NotNil(t, operator)
	return operator.KubernetesServiceAccounts
}

func buildTestImageRegistryServiceAccounts(t *testing.T) []*azure.KubernetesServiceAccount {
	t.Helper()
	operator := buildTestDataPlaneOIDCFederationIdentitiesConfig().DataPlaneOperatorsIdentities[azure.ClusterOperatorIdentifierImageRegistry]
	require.NotNil(t, operator)
	return operator.KubernetesServiceAccounts
}

func buildTestOIDCAssignmentNotEnsured() *coreapi.DataplaneOIDCFederationAssignmentStatus {
	return &coreapi.DataplaneOIDCFederationAssignmentStatus{}
}

func buildTestOIDCAssignmentEnsured(identity coreapi.DataplaneOIDCFederationIdentityInstance, azure []*azcorearm.ResourceID) *coreapi.DataplaneOIDCFederationAssignmentStatus {
	return &coreapi.DataplaneOIDCFederationAssignmentStatus{
		EnsuredIdentity: ptr.To(identity),
		AzureResources:  azure,
	}
}

func buildTestOIDCAssignmentDeconfigure(ts *metav1.Time, azure, pending []*azcorearm.ResourceID) *coreapi.DataplaneOIDCFederationAssignmentStatus {
	return &coreapi.DataplaneOIDCFederationAssignmentStatus{
		DeconfigureTimestamp:  ts,
		AzureResources:        azure,
		PendingAzureResources: pending,
	}
}

func buildTestOIDCAssignmentKey(identityResourceID, operatorName string) coreapi.DataplaneOIDCFederationAssignmentKey {
	return coreapi.DataplaneOIDCFederationAssignmentKey{
		IdentityResourceID: strings.ToLower(identityResourceID),
		OperatorName:       operatorName,
	}
}

func buildTestOIDCAssignmentWithTarget(target coreapi.DataplaneOIDCFederationIdentityInstance, assignment *coreapi.DataplaneOIDCFederationAssignmentStatus) *coreapi.DataplaneOIDCFederationAssignmentStatus {
	out := assignment.DeepCopy()
	out.TargetIdentity = target
	return out
}

func seedTestDesiredDataPlaneOperators(cluster *coreapi.HCPOpenShiftCluster, federation map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus) {
	if cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators == nil {
		cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators = map[string]*azcorearm.ResourceID{}
	}
	for assignmentKey, status := range federation {
		if status == nil || status.DeconfigureTimestamp != nil {
			continue
		}
		identityResourceID := metadataapi.Must(azcorearm.ParseResourceID(assignmentKey.IdentityResourceID))
		cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators[assignmentKey.OperatorName] = identityResourceID
	}
}

func resourceIDStrings(ids []*azcorearm.ResourceID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, strings.ToLower(id.String()))
	}
	return out
}

func ficNames(ids []*azcorearm.ResourceID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.Name)
	}
	return out
}

func buildTestOIDCFederationCluster(t *testing.T, serviceManagedIdentity *azcorearm.ResourceID, operators map[string]*azcorearm.ResourceID, opts ...func(*coreapi.HCPOpenShiftCluster)) *coreapi.HCPOpenShiftCluster {
	t.Helper()
	cluster := buildTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, operators)
	cluster.ServiceProviderProperties.ClusterServiceID = buildTestClusterServiceID()
	for _, opt := range opts {
		opt(cluster)
	}
	return cluster
}

func buildTestOIDCFederationServiceProviderCluster(
	federation map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus,
	opts ...func(*coreapi.ServiceProviderCluster),
) *coreapi.ServiceProviderCluster {
	serviceProviderCluster := buildTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	if federation != nil {
		copied := make(map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus, len(federation))
		for key, status := range federation {
			copied[key] = status.DeepCopy()
		}
		serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = copied
	}
	for _, opt := range opts {
		opt(serviceProviderCluster)
	}
	return serviceProviderCluster
}

func withTestNoClusterServiceID(cluster *coreapi.HCPOpenShiftCluster) {
	cluster.ServiceProviderProperties.ClusterServiceID = nil
}

func withTestClusterDeletionTimestamp(ts *metav1.Time) func(*coreapi.HCPOpenShiftCluster) {
	return func(cluster *coreapi.HCPOpenShiftCluster) {
		cluster.ServiceProviderProperties.DeletionTimestamp = ts
	}
}

func withTestNilServiceManagedIdentity(cluster *coreapi.HCPOpenShiftCluster) {
	cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity = nil
}

func withTestEarliestOIDCFederationRecheck(ts *metav1.Time) func(*coreapi.ServiceProviderCluster) {
	return func(serviceProviderCluster *coreapi.ServiceProviderCluster) {
		serviceProviderCluster.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{
			DataPlaneOIDCFederationControllerName: ts,
		}
	}
}

func assertDataplaneOIDCFederationAssignments(
	t *testing.T,
	got, want map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus,
) {
	t.Helper()
	if want == nil {
		assert.Nil(t, got)
		return
	}
	require.NotNil(t, got)
	require.Len(t, got, len(want))
	for key, wantStatus := range want {
		require.Contains(t, got, key)
		assertDataplaneOIDCFederationAssignmentStatus(t, got[key], wantStatus)
	}
}

func assertDataplaneOIDCFederationAssignmentStatus(t *testing.T, got, want *coreapi.DataplaneOIDCFederationAssignmentStatus) {
	t.Helper()
	if want == nil {
		assert.Nil(t, got)
		return
	}
	require.NotNil(t, got)
	assert.Equal(t, want.TargetIdentity, got.TargetIdentity)
	if want.EnsuredIdentity == nil {
		assert.Nil(t, got.EnsuredIdentity)
	} else {
		require.NotNil(t, got.EnsuredIdentity)
		assert.Equal(t, *want.EnsuredIdentity, *got.EnsuredIdentity)
	}
	if want.DeconfigureTimestamp == nil {
		assert.Nil(t, got.DeconfigureTimestamp)
	} else {
		require.NotNil(t, got.DeconfigureTimestamp)
		assert.True(t, got.DeconfigureTimestamp.Time.Equal(want.DeconfigureTimestamp.Time))
	}
	assert.ElementsMatch(t, resourceIDStrings(want.AzureResources), resourceIDStrings(got.AzureResources))
	assert.ElementsMatch(t, resourceIDStrings(want.PendingAzureResources), resourceIDStrings(got.PendingAzureResources))
}

func assertOIDCFederationRecheckScheduled(t *testing.T, serviceProviderCluster *coreapi.ServiceProviderCluster, now time.Time) {
	t.Helper()
	recheck := serviceProviderCluster.Spec.EarliestRecheckTimesByController[DataPlaneOIDCFederationControllerName]
	require.NotNil(t, recheck)
	assert.True(t, recheck.After(now))
}

func createdFICNames(calls []recordedFICCall) []string {
	out := make([]string, 0, len(calls))
	for _, call := range calls {
		out = append(out, call.credentialName)
	}
	return out
}

func buildTestMatchingDiskCSIFICs(t *testing.T) map[string]armmsi.FederatedIdentityCredential {
	t.Helper()
	existing := map[string]armmsi.FederatedIdentityCredential{}
	for _, sa := range buildTestDiskCSIDriverServiceAccounts(t) {
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

// buildTestClusterWithIdentities builds an HCPOpenShiftCluster addressable by the mock
// ResourcesDBClient with the supplied ServiceManagedIdentity and data plane operator
// identities on its CustomerProperties.
func buildTestClusterWithIdentities(t *testing.T, clusterName string, serviceManagedIdentity *azcorearm.ResourceID, dataPlaneOperators map[string]*azcorearm.ResourceID) *coreapi.HCPOpenShiftCluster {
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

// buildTestServiceProviderClusterWithIdentities builds a ServiceProviderCluster addressable
// by the mock ResourcesDBClient with the supplied resolved identities and recheck time.
func buildTestServiceProviderClusterWithIdentities(clusterName string, identities map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity, _ *metav1.Time) *coreapi.ServiceProviderCluster {
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

	return serviceProviderCluster
}
