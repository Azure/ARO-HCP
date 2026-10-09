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

package azureresources

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
)

// expectedTLSCertificate builds the TLSCertificate value both the
// KubeAPIServerTLSCertificate and IngressTLSCertificate controllers would persist for
// the given certificate name, confirmed via AzureReference or still PendingReference.
func expectedTLSCertificate(name string, confirmed bool) *coreapi.TLSCertificate {
	reference := &coreapi.AzureTLSCertificateReference{KeyVaultURL: "https://certificates.vault.azure.net/", CertificateName: name}
	if confirmed {
		return &coreapi.TLSCertificate{AzureReference: reference}
	}
	return &coreapi.TLSCertificate{PendingReference: reference}
}

// fakeTLSCertificatesClient is a shared fake used by the KubeAPIServerTLSCertificate
// and IngressTLSCertificate controller tests.
type fakeTLSCertificatesClient struct {
	test            *testing.T
	status          *string
	getError        error
	getErrorsByName map[string]error
	operationError  error
	operationCalls  int
	observed        []string
	completedName   string
	errorName       string
}

func (client *fakeTLSCertificatesClient) GetCertificate(_ context.Context, name, version string, _ *azcertificates.GetCertificateOptions) (azcertificates.GetCertificateResponse, error) {
	client.observed = append(client.observed, name)
	require.Empty(client.test, version)
	if name == client.errorName {
		return azcertificates.GetCertificateResponse{}, errors.New("forbidden")
	}
	if err, ok := client.getErrorsByName[name]; ok {
		return azcertificates.GetCertificateResponse{}, err
	}
	return azcertificates.GetCertificateResponse{}, client.getError
}

func (client *fakeTLSCertificatesClient) GetCertificateOperation(_ context.Context, name string, _ *azcertificates.GetCertificateOperationOptions) (azcertificates.GetCertificateOperationResponse, error) {
	client.operationCalls++
	if name == client.completedName {
		return azcertificates.GetCertificateOperationResponse{CertificateOperation: azcertificates.CertificateOperation{Status: ptr.To("completed")}}, nil
	}
	return azcertificates.GetCertificateOperationResponse{CertificateOperation: azcertificates.CertificateOperation{Status: client.status}}, client.operationError
}

// newTLSCertificateObservationFixture builds a tlsCertificateSyncer configured for
// field, addressable by a mock ResourcesDBClient, with certificatesClient configured
// to always return the same fake client regardless of vault URL. Shared by the
// KubeAPIServerTLSCertificate and IngressTLSCertificate controller tests.
func newTLSCertificateObservationFixture(test *testing.T, field tlsCertificateField) (*tlsCertificateSyncer, controllerutils.HCPClusterKey, *coreapi.Cluster) {
	test.Helper()
	cluster := newTestCluster(false)
	clusterServiceID := metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/abc123"))
	cluster.ServiceProviderProperties.ClusterServiceID = &clusterServiceID
	serviceProvider := newTestServiceProviderCluster(coreapi.AzureReference{})
	managementID := metadataapi.Must(azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/stamps/test/managementClusters/default"))
	serviceProvider.Status.ManagementClusterResourceID = managementID
	database, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(test.Context(), []any{cluster, serviceProvider})
	require.NoError(test, err)
	fakeClient := &fakeTLSCertificatesClient{test: test, status: ptr.To("inProgress")}
	return &tlsCertificateSyncer{
		field:                        field,
		resourcesDBClient:            database,
		clusterLister:                &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{cluster}},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: database},
		managementClusterLister: &fleetlistertesting.SliceManagementClusterLister{ManagementClusters: []*fleetapi.ManagementCluster{{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: managementID},
			Status:         fleetapi.ManagementClusterStatus{HostedClustersSecretsKeyVaultURL: "https://certificates.vault.azure.net/"},
		}}},
		certificatesClient: func(string) (tlsCertificatesClient, error) { return fakeClient, nil },
	}, controllerutils.HCPClusterKey{SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: testClusterName}, cluster
}

// fakeClientFor returns the fake certificates client that syncer's certificatesClient
// factory was configured to return (see newTLSCertificateObservationFixture).
func fakeClientFor(test *testing.T, syncer *tlsCertificateSyncer) *fakeTLSCertificatesClient {
	test.Helper()
	client, err := syncer.certificatesClient("https://certificates.vault.azure.net/")
	require.NoError(test, err)
	return client.(*fakeTLSCertificatesClient)
}

func TestTLSCertificatesOperationReadiness(test *testing.T) {
	notFound := &azcore.ResponseError{StatusCode: http.StatusNotFound}
	for _, scenario := range []struct {
		name                     string
		status                   *string
		getError, operationError error
		wantReady, wantError     bool
		wantErrorMessage         string
		wantNoOperation          bool
	}{
		{name: "completed", status: ptr.To("completed"), wantReady: true},
		{name: "in progress", status: ptr.To("inProgress")},
		{name: "failed", status: ptr.To("failed"), wantError: true},
		{name: "cancelled", status: ptr.To("cancelled"), wantError: true},
		{name: "unknown", status: ptr.To("unexpected"), wantError: true},
		{name: "nil status", wantError: true},
		{name: "certificate and operation absent", getError: notFound, operationError: notFound},
		{name: "certificate absent operation in progress", getError: notFound, status: ptr.To("inProgress")},
		{name: "certificate absent operation completed", getError: notFound, status: ptr.To("completed")},
		{name: "certificate absent operation failed", getError: notFound, status: ptr.To("failed"), wantError: true, wantErrorMessage: `certificate "certificate" operation failed`},
		{name: "certificate absent operation cancelled", getError: notFound, status: ptr.To("cancelled"), wantError: true, wantErrorMessage: `certificate "certificate" operation cancelled`},
		{name: "certificate absent operation error", getError: notFound, operationError: errors.New("service unavailable"), wantError: true},
		{name: "forbidden", getError: &azcore.ResponseError{StatusCode: http.StatusForbidden}, wantError: true, wantNoOperation: true},
		// Key Vault can return 200 OK from GetCertificate while a concurrent or later
		// GetCertificateOperation call 404s: pending operations are transient resources
		// managed separately from the certificate object, so the operation can disappear
		// (e.g. due to replication lag or cleanup) even though the certificate itself exists.
		// A present certificate must still be treated as ready in that case.
		{name: "certificate present operation absent", operationError: notFound, wantReady: true},
		{name: "operation error", operationError: errors.New("service unavailable"), wantError: true},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			client := &fakeTLSCertificatesClient{test: test, status: scenario.status, getError: scenario.getError, operationError: scenario.operationError}
			ready, err := observeTLSCertificate(test.Context(), client, "certificate")
			require.Equal(test, scenario.wantReady, ready)
			if scenario.wantError {
				require.Error(test, err)
				if scenario.wantErrorMessage != "" {
					require.ErrorContains(test, err, scenario.wantErrorMessage)
				}
			} else {
				require.NoError(test, err)
			}
			if scenario.wantNoOperation {
				require.Zero(test, client.operationCalls)
			} else {
				require.Equal(test, 1, client.operationCalls)
			}
		})
	}
}

func TestTLSCertificateReferenceJSON(test *testing.T) {
	status := coreapi.ServiceProviderClusterStatus{
		AzureResources: coreapi.AzureResources{
			KubeAPIServerCertificate: expectedTLSCertificate("kube-apiserver-tls-cert-abc123", false),
			IngressCertificate:       expectedTLSCertificate("ingress-tls-cert-abc123", true),
		},
	}
	data, err := json.Marshal(status)
	require.NoError(test, err)
	require.Contains(test, string(data), `"keyVaultURL":"https://certificates.vault.azure.net/"`)
	require.Contains(test, string(data), `"certificateName":"ingress-tls-cert-abc123"`)
	var restored coreapi.ServiceProviderClusterStatus
	require.NoError(test, json.Unmarshal(data, &restored))
	require.Equal(test, status, restored)
	var document map[string]json.RawMessage
	require.NoError(test, json.Unmarshal(data, &document))
	require.NotContains(test, document, "kubeAPIServerCertificate")
	require.NotContains(test, document, "ingressCertificate")
	var azureResources map[string]json.RawMessage
	require.NoError(test, json.Unmarshal(document["azureResources"], &azureResources))
	for name, expected := range map[string]*coreapi.TLSCertificate{
		"kubeAPIServerCertificate": status.AzureResources.KubeAPIServerCertificate,
		"ingressCertificate":       status.AzureResources.IngressCertificate,
	} {
		var certificate coreapi.TLSCertificate
		require.NoError(test, json.Unmarshal(azureResources[name], &certificate))
		require.Equal(test, expected, &certificate)
	}
}

func TestTLSCertificateOptionalReferences(test *testing.T) {
	data, err := json.Marshal(coreapi.AzureResources{})
	require.NoError(test, err)
	var document map[string]json.RawMessage
	require.NoError(test, json.Unmarshal(data, &document))
	require.NotContains(test, document, "kubeAPIServerCertificate")
	require.NotContains(test, document, "ingressCertificate")
	original := &coreapi.ServiceProviderCluster{Status: coreapi.ServiceProviderClusterStatus{AzureResources: coreapi.AzureResources{
		KubeAPIServerCertificate: expectedTLSCertificate("kas", false),
		IngressCertificate:       expectedTLSCertificate("ingress", true),
	}}}
	copied := original.DeepCopy()
	require.Equal(test, original, copied)
	copied.Status.AzureResources.KubeAPIServerCertificate.PendingReference.CertificateName = "changed-kas"
	copied.Status.AzureResources.IngressCertificate.AzureReference.CertificateName = "changed-ingress"
	require.Equal(test, "kas", original.Status.AzureResources.KubeAPIServerCertificate.PendingReference.CertificateName)
	require.Equal(test, "ingress", original.Status.AzureResources.IngressCertificate.AzureReference.CertificateName)
}
