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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func expectedTLSCertificate(name string, confirmed bool) coreapi.TLSCertificate {
	reference := coreapi.AzureTLSCertificateReference{KeyVaultURL: "https://certificates.vault.azure.net/", CertificateName: name}
	if confirmed {
		return coreapi.TLSCertificate{AzureReference: reference}
	}
	return coreapi.TLSCertificate{PendingReference: reference}
}

func newObservationFixture(test *testing.T) (*tlsCertificatesSyncer, controllerutils.HCPClusterKey, *coreapi.Cluster) {
	test.Helper()
	cluster := newTestCluster(false)
	clusterServiceID := metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/abc123"))
	cluster.ServiceProviderProperties.ClusterServiceID = &clusterServiceID
	serviceProvider := newTestServiceProviderCluster(coreapi.AzureReference{})
	managementID := metadataapi.Must(azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/stamps/test/managementClusters/default"))
	serviceProvider.Status.ManagementClusterResourceID = managementID
	database, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(test.Context(), []any{cluster, serviceProvider})
	require.NoError(test, err)
	return &tlsCertificatesSyncer{
		resourcesDBClient:            database,
		clusterLister:                &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{cluster}},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: database},
		managementClusterLister: &fleetlistertesting.SliceManagementClusterLister{ManagementClusters: []*fleetapi.ManagementCluster{{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: managementID},
			Status:         fleetapi.ManagementClusterStatus{HostedClustersSecretsKeyVaultURL: "https://certificates.vault.azure.net/"},
		}}},
		observe: func(context.Context, string, string) (bool, error) {
			test.Fatal("unexpected Key Vault read")
			return false, nil
		},
	}, controllerutils.HCPClusterKey{SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: testClusterName}, cluster
}

func TestTLSCertificatesDeletionClearsReferences(test *testing.T) {
	syncer, key, cluster := newObservationFixture(test)
	require.NoError(test, syncer.SyncOnce(test.Context(), key))
	syncer.observe = func(_ context.Context, _, name string) (bool, error) { return name == "ingress-tls-cert-abc123", nil }
	require.NoError(test, syncer.SyncOnce(test.Context(), key))
	status, err := syncer.serviceProviderClusterLister.Get(test.Context(), key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	require.NoError(test, err)
	require.Equal(test, expectedTLSCertificate("kube-apiserver-tls-cert-abc123", false), status.Status.AzureResources.KubeAPIServerCertificate)
	require.Equal(test, expectedTLSCertificate("ingress-tls-cert-abc123", true), status.Status.AzureResources.IngressCertificate)
	status.Status.HostedClusterNamespace = "preserve-unrelated-status"
	_, err = syncer.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(test.Context(), status, nil)
	require.NoError(test, err)
	now := metav1.Now()
	cluster.ServiceProviderProperties.DeletionTimestamp = &now
	cluster.ServiceProviderProperties.ClusterServiceID = nil
	syncer.managementClusterLister = nil
	syncer.observe = nil
	require.NoError(test, syncer.SyncOnce(test.Context(), key))
	status, err = syncer.serviceProviderClusterLister.Get(test.Context(), key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	require.NoError(test, err)
	require.Equal(test, coreapi.TLSCertificate{}, status.Status.AzureResources.KubeAPIServerCertificate)
	require.Equal(test, coreapi.TLSCertificate{}, status.Status.AzureResources.IngressCertificate)
	require.Equal(test, "preserve-unrelated-status", status.Status.HostedClusterNamespace)
	require.False(test, syncer.NeedsWork(cluster, status))
	require.NoError(test, syncer.SyncOnce(test.Context(), key))
}

func TestTLSCertificatesMissingVaultIsError(test *testing.T) {
	syncer, key, _ := newObservationFixture(test)
	syncer.managementClusterLister.(*fleetlistertesting.SliceManagementClusterLister).ManagementClusters[0].Status.HostedClustersSecretsKeyVaultURL = ""
	require.ErrorContains(test, syncer.SyncOnce(test.Context(), key), "Key Vault URL")
}

type fakeTLSCertificatesClient struct {
	test           *testing.T
	status         *string
	getError       error
	operationError error
	operationCalls int
}

func (client *fakeTLSCertificatesClient) GetCertificate(_ context.Context, name, version string, _ *azcertificates.GetCertificateOptions) (azcertificates.GetCertificateResponse, error) {
	require.Equal(client.test, "certificate", name)
	require.Empty(client.test, version)
	return azcertificates.GetCertificateResponse{}, client.getError
}

func (client *fakeTLSCertificatesClient) GetCertificateOperation(_ context.Context, name string, _ *azcertificates.GetCertificateOperationOptions) (azcertificates.GetCertificateOperationResponse, error) {
	require.Equal(client.test, "certificate", name)
	client.operationCalls++
	return azcertificates.GetCertificateOperationResponse{CertificateOperation: azcertificates.CertificateOperation{Status: client.status}}, client.operationError
}

func TestTLSCertificatesOperationReadiness(test *testing.T) {
	for _, scenario := range []struct {
		name                     string
		status                   *string
		getError, operationError error
		wantReady, wantError     bool
	}{
		{name: "completed", status: ptr.To("completed"), wantReady: true},
		{name: "in progress", status: ptr.To("inProgress")},
		{name: "failed", status: ptr.To("failed"), wantError: true},
		{name: "cancelled", status: ptr.To("cancelled"), wantError: true},
		{name: "unknown", status: ptr.To("unexpected"), wantError: true},
		{name: "nil status", wantError: true},
		{name: "certificate absent", getError: &azcore.ResponseError{StatusCode: http.StatusNotFound}},
		{name: "forbidden", getError: &azcore.ResponseError{StatusCode: http.StatusForbidden}, wantError: true},
		{name: "operation absent", operationError: &azcore.ResponseError{StatusCode: http.StatusNotFound}, wantError: true},
		{name: "operation error", operationError: errors.New("service unavailable"), wantError: true},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			client := &fakeTLSCertificatesClient{test: test, status: scenario.status, getError: scenario.getError, operationError: scenario.operationError}
			ready, err := observeTLSCertificate(test.Context(), client, "certificate")
			require.Equal(test, scenario.wantReady, ready)
			if scenario.wantError {
				require.Error(test, err)
			} else {
				require.NoError(test, err)
			}
			if scenario.getError != nil {
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
	require.JSONEq(test, `{"pendingReference":{"keyVaultURL":"https://certificates.vault.azure.net/","certificateName":"kube-apiserver-tls-cert-abc123"},"azureReference":{"keyVaultURL":"","certificateName":""}}`, string(azureResources["kubeAPIServerCertificate"]))
	require.JSONEq(test, `{"pendingReference":{"keyVaultURL":"","certificateName":""},"azureReference":{"keyVaultURL":"https://certificates.vault.azure.net/","certificateName":"ingress-tls-cert-abc123"}}`, string(azureResources["ingressCertificate"]))
}

func TestTLSCertificatesPendingToDone(test *testing.T) {
	for _, first := range []string{"kube-apiserver-tls-cert-abc123", "ingress-tls-cert-abc123"} {
		test.Run(first, func(test *testing.T) {
			syncer, key, cluster := newObservationFixture(test)
			readStatus := func() *coreapi.ServiceProviderCluster {
				status, err := syncer.serviceProviderClusterLister.Get(test.Context(), key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
				require.NoError(test, err)
				return status
			}
			require.NoError(test, syncer.SyncOnce(test.Context(), key))
			pending := readStatus()
			require.Equal(test, expectedTLSCertificate("kube-apiserver-tls-cert-abc123", false), pending.Status.AzureResources.KubeAPIServerCertificate)
			require.Equal(test, expectedTLSCertificate("ingress-tls-cert-abc123", false), pending.Status.AzureResources.IngressCertificate)
			var names []string
			syncer.observe = func(_ context.Context, vault, name string) (bool, error) {
				require.Equal(test, "https://certificates.vault.azure.net/", vault)
				names = append(names, name)
				return name == first, nil
			}
			require.NoError(test, syncer.SyncOnce(test.Context(), key))
			require.ElementsMatch(test, []string{"kube-apiserver-tls-cert-abc123", "ingress-tls-cert-abc123"}, names)
			partial := readStatus()
			if first == "kube-apiserver-tls-cert-abc123" {
				require.Equal(test, expectedTLSCertificate("kube-apiserver-tls-cert-abc123", true), partial.Status.AzureResources.KubeAPIServerCertificate)
				require.Equal(test, expectedTLSCertificate("ingress-tls-cert-abc123", false), partial.Status.AzureResources.IngressCertificate)
			} else {
				require.Equal(test, expectedTLSCertificate("kube-apiserver-tls-cert-abc123", false), partial.Status.AzureResources.KubeAPIServerCertificate)
				require.Equal(test, expectedTLSCertificate("ingress-tls-cert-abc123", true), partial.Status.AzureResources.IngressCertificate)
			}
			names = nil
			syncer.observe = func(_ context.Context, _, name string) (bool, error) { names = append(names, name); return true, nil }
			require.NoError(test, syncer.SyncOnce(test.Context(), key))
			require.Len(test, names, 1)
			require.NotEqual(test, first, names[0])
			done := readStatus()
			require.Equal(test, expectedTLSCertificate("kube-apiserver-tls-cert-abc123", true), done.Status.AzureResources.KubeAPIServerCertificate)
			require.Equal(test, expectedTLSCertificate("ingress-tls-cert-abc123", true), done.Status.AzureResources.IngressCertificate)
			require.False(test, syncer.NeedsWork(cluster, done))
			require.NoError(test, syncer.SyncOnce(test.Context(), key))
			require.Len(test, names, 1)
		})
	}
}

func TestTLSCertificatesReadErrorPreservesIndependentProgress(test *testing.T) {
	syncer, key, _ := newObservationFixture(test)
	require.NoError(test, syncer.SyncOnce(test.Context(), key))
	syncer.observe = func(_ context.Context, _, name string) (bool, error) {
		if name == "kube-apiserver-tls-cert-abc123" {
			return false, errors.New("forbidden")
		}
		return true, nil
	}
	require.ErrorContains(test, syncer.SyncOnce(test.Context(), key), "forbidden")
	status, err := syncer.serviceProviderClusterLister.Get(test.Context(), key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	require.NoError(test, err)
	require.Equal(test, expectedTLSCertificate("kube-apiserver-tls-cert-abc123", false), status.Status.AzureResources.KubeAPIServerCertificate)
	require.Equal(test, expectedTLSCertificate("ingress-tls-cert-abc123", true), status.Status.AzureResources.IngressCertificate)
}

func TestTLSCertificatesNeedsWork(test *testing.T) {
	syncer, _, cluster := newObservationFixture(test)
	status := &coreapi.ServiceProviderCluster{}
	require.True(test, syncer.NeedsWork(cluster, status))
	status.Status.AzureResources.IngressCertificate = expectedTLSCertificate("ingress-tls-cert-abc123", true)
	require.True(test, syncer.NeedsWork(cluster, status))
	status.Status.AzureResources.KubeAPIServerCertificate = expectedTLSCertificate("kube-apiserver-tls-cert-abc123", true)
	require.False(test, syncer.NeedsWork(cluster, status))
	status.Status.AzureResources.KubeAPIServerCertificate = expectedTLSCertificate("kube-apiserver-tls-cert-abc123", false)
	require.True(test, syncer.NeedsWork(cluster, status))
	now := metav1.Now()
	cluster.ServiceProviderProperties.DeletionTimestamp = &now
	require.True(test, syncer.NeedsWork(cluster, status))
	cluster.ServiceProviderProperties.DeletionTimestamp = nil
	cluster.ServiceProviderProperties.ClusterServiceID = nil
	require.False(test, syncer.NeedsWork(cluster, status))
}

func TestTLSCertificatesConflict(test *testing.T) {
	syncer, key, _ := newObservationFixture(test)
	require.NoError(test, syncer.SyncOnce(test.Context(), key))
	stale, err := syncer.serviceProviderClusterLister.Get(test.Context(), key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	require.NoError(test, err)
	concurrent := stale.DeepCopy()
	concurrent.Status.HostedClusterNamespace = "concurrent-update"
	_, err = syncer.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(test.Context(), concurrent, nil)
	require.NoError(test, err)
	syncer.serviceProviderClusterLister = &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{stale}}
	syncer.observe = func(context.Context, string, string) (bool, error) { return true, nil }
	require.NoError(test, syncer.SyncOnce(test.Context(), key))
	syncer.serviceProviderClusterLister = &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: syncer.resourcesDBClient}
	actual, err := syncer.serviceProviderClusterLister.Get(test.Context(), key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	require.NoError(test, err)
	require.Equal(test, "concurrent-update", actual.Status.HostedClusterNamespace)
	require.Equal(test, expectedTLSCertificate("ingress-tls-cert-abc123", false), actual.Status.AzureResources.IngressCertificate)
	require.NoError(test, syncer.SyncOnce(test.Context(), key))
	actual, err = syncer.serviceProviderClusterLister.Get(test.Context(), key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	require.NoError(test, err)
	require.Equal(test, expectedTLSCertificate("ingress-tls-cert-abc123", true), actual.Status.AzureResources.IngressCertificate)
	require.Equal(test, expectedTLSCertificate("kube-apiserver-tls-cert-abc123", true), actual.Status.AzureResources.KubeAPIServerCertificate)
	require.Equal(test, "concurrent-update", actual.Status.HostedClusterNamespace)
}

func TestTLSCertificatesPrerequisites(test *testing.T) {
	for name, mutate := range map[string]func(*tlsCertificatesSyncer, *coreapi.Cluster){
		"deleting": func(_ *tlsCertificatesSyncer, cluster *coreapi.Cluster) {
			now := metav1.Now()
			cluster.ServiceProviderProperties.DeletionTimestamp = &now
		},
		"no CS ID": func(_ *tlsCertificatesSyncer, cluster *coreapi.Cluster) {
			cluster.ServiceProviderProperties.ClusterServiceID = nil
		},
		"no cluster": func(syncer *tlsCertificatesSyncer, _ *coreapi.Cluster) {
			syncer.clusterLister = &corelistertesting.SliceClusterLister{}
		},
		"no service provider": func(syncer *tlsCertificatesSyncer, _ *coreapi.Cluster) {
			syncer.serviceProviderClusterLister = &corelistertesting.SliceServiceProviderClusterLister{}
		},
		"no management cluster": func(syncer *tlsCertificatesSyncer, _ *coreapi.Cluster) {
			syncer.managementClusterLister = &fleetlistertesting.SliceManagementClusterLister{}
		},
	} {
		test.Run(name, func(test *testing.T) {
			syncer, key, cluster := newObservationFixture(test)
			mutate(syncer, cluster)
			require.NoError(test, syncer.SyncOnce(test.Context(), key))
			require.NoError(test, syncer.SyncOnce(test.Context(), key))
		})
	}
}
