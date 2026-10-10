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
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
)

func newKubeAPIServerObservationFixture(test *testing.T) (*tlsCertificateSyncer, controllerutils.HCPClusterKey, *coreapi.Cluster) {
	return newTLSCertificateObservationFixture(test, kubeAPIServerTLSCertificateField)
}

func TestKubeAPIServerTLSCertificateReusesClient(test *testing.T) {
	syncer, key, cluster := newKubeAPIServerObservationFixture(test)
	client := fakeClientFor(test, syncer)
	require.NoError(test, syncer.SyncOnce(test.Context(), key)) // sets PendingReference, no client calls yet
	require.Zero(test, client.operationCalls)
	require.NoError(test, syncer.SyncOnce(test.Context(), key)) // observes: still inProgress
	require.Equal(test, 1, client.operationCalls)
	require.Equal(test, []string{"kube-apiserver-tls-cert-abc123"}, client.observed)
	client.status = ptr.To("completed")
	require.NoError(test, syncer.SyncOnce(test.Context(), key)) // observes: now completed, confirms
	require.Equal(test, 2, client.operationCalls)
	require.NoError(test, syncer.SyncOnce(test.Context(), key)) // already confirmed: no further calls
	require.Equal(test, 2, client.operationCalls)
	now := metav1.Now()
	cluster.ServiceProviderProperties.DeletionTimestamp = &now
	require.NoError(test, syncer.SyncOnce(test.Context(), key)) // deletion observation uses GetCertificate, not GetCertificateOperation
	require.Equal(test, 2, client.operationCalls)
}

func TestKubeAPIServerTLSCertificateClientError(test *testing.T) {
	syncer, key, _ := newKubeAPIServerObservationFixture(test)
	require.NoError(test, syncer.SyncOnce(test.Context(), key))
	clientError := errors.New("client unavailable")
	syncer.certificatesClient = func(string) (tlsCertificatesClient, error) { return nil, clientError }
	require.ErrorIs(test, syncer.SyncOnce(test.Context(), key), clientError)
	status, err := syncer.serviceProviderClusterLister.Get(test.Context(), key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	require.NoError(test, err)
	require.Equal(test, expectedTLSCertificate("kube-apiserver-tls-cert-abc123", false), status.Status.AzureResources.KubeAPIServerCertificate)
}

// TestKubeAPIServerTLSCertificateDeletionClearsReference covers the full deletion
// observation lifecycle: the reference is retained while the certificate still
// exists in Key Vault, a transient error observing it must not clear the reference,
// and only a confirmed 404 clears it.
func TestKubeAPIServerTLSCertificateDeletionClearsReference(test *testing.T) {
	syncer, key, cluster := newKubeAPIServerObservationFixture(test)
	client := fakeClientFor(test, syncer)
	client.status = ptr.To("completed")
	require.NoError(test, syncer.SyncOnce(test.Context(), key)) // sets PendingReference
	require.NoError(test, syncer.SyncOnce(test.Context(), key)) // confirms AzureReference
	status, err := syncer.serviceProviderClusterLister.Get(test.Context(), key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	require.NoError(test, err)
	require.Equal(test, expectedTLSCertificate("kube-apiserver-tls-cert-abc123", true), status.Status.AzureResources.KubeAPIServerCertificate)
	status.Status.HostedClusterNamespace = "preserve-unrelated-status"
	_, err = syncer.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(test.Context(), status, nil)
	require.NoError(test, err)
	now := metav1.Now()
	cluster.ServiceProviderProperties.DeletionTimestamp = &now
	cluster.ServiceProviderProperties.ClusterServiceID = nil

	// The certificate still exists in Key Vault, so the deletion branch must leave the
	// reference in place until it's actually confirmed gone.
	require.NoError(test, syncer.SyncOnce(test.Context(), key))
	status, err = syncer.serviceProviderClusterLister.Get(test.Context(), key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	require.NoError(test, err)
	require.Equal(test, expectedTLSCertificate("kube-apiserver-tls-cert-abc123", true), status.Status.AzureResources.KubeAPIServerCertificate)
	require.True(test, syncer.needsWork(cluster, status))

	// A transient error observing the certificate must not clear its reference.
	client.getError = errors.New("vault unavailable")
	require.ErrorContains(test, syncer.SyncOnce(test.Context(), key), "vault unavailable")
	status, err = syncer.serviceProviderClusterLister.Get(test.Context(), key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	require.NoError(test, err)
	require.Equal(test, expectedTLSCertificate("kube-apiserver-tls-cert-abc123", true), status.Status.AzureResources.KubeAPIServerCertificate)

	// Once Key Vault reports the certificate as deleted (404), the reference is cleared.
	client.getError = &azcore.ResponseError{StatusCode: http.StatusNotFound}
	require.NoError(test, syncer.SyncOnce(test.Context(), key))
	status, err = syncer.serviceProviderClusterLister.Get(test.Context(), key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	require.NoError(test, err)
	require.Nil(test, status.Status.AzureResources.KubeAPIServerCertificate)
	require.Equal(test, "preserve-unrelated-status", status.Status.HostedClusterNamespace)
	require.False(test, syncer.needsWork(cluster, status))
	require.NoError(test, syncer.SyncOnce(test.Context(), key))
}

func TestKubeAPIServerTLSCertificateMissingVaultIsError(test *testing.T) {
	for _, vaultURL := range []string{"", "https://%"} {
		test.Run(vaultURL, func(test *testing.T) {
			syncer, key, _ := newKubeAPIServerObservationFixture(test)
			syncer.managementClusterLister.(*fleetlistertesting.SliceManagementClusterLister).ManagementClusters[0].Status.HostedClustersSecretsKeyVaultURL = vaultURL
			require.ErrorContains(test, syncer.SyncOnce(test.Context(), key), "Key Vault URL")
		})
	}
}

func TestKubeAPIServerTLSCertificateTrustsValidatedVaultURL(test *testing.T) {
	for _, vaultURL := range []string{"not-a-url", "http://vault.example/", "https://"} {
		test.Run(vaultURL, func(test *testing.T) {
			syncer, key, _ := newKubeAPIServerObservationFixture(test)
			syncer.managementClusterLister.(*fleetlistertesting.SliceManagementClusterLister).ManagementClusters[0].Status.HostedClustersSecretsKeyVaultURL = vaultURL
			require.NoError(test, syncer.SyncOnce(test.Context(), key))
		})
	}
}

func TestKubeAPIServerTLSCertificatePendingToDone(test *testing.T) {
	syncer, key, cluster := newKubeAPIServerObservationFixture(test)
	readStatus := func() *coreapi.ServiceProviderCluster {
		status, err := syncer.serviceProviderClusterLister.Get(test.Context(), key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
		require.NoError(test, err)
		return status
	}
	require.NoError(test, syncer.SyncOnce(test.Context(), key))
	pending := readStatus()
	require.Equal(test, expectedTLSCertificate("kube-apiserver-tls-cert-abc123", false), pending.Status.AzureResources.KubeAPIServerCertificate)

	client := fakeClientFor(test, syncer)
	client.status = ptr.To("completed")
	require.NoError(test, syncer.SyncOnce(test.Context(), key))
	done := readStatus()
	require.Equal(test, expectedTLSCertificate("kube-apiserver-tls-cert-abc123", true), done.Status.AzureResources.KubeAPIServerCertificate)
	require.False(test, syncer.needsWork(cluster, done))

	client.observed = nil
	require.NoError(test, syncer.SyncOnce(test.Context(), key))
	require.Empty(test, client.observed)
}

func TestKubeAPIServerTLSCertificateObserveErrorPreservesPendingReference(test *testing.T) {
	syncer, key, _ := newKubeAPIServerObservationFixture(test)
	require.NoError(test, syncer.SyncOnce(test.Context(), key))
	client := fakeClientFor(test, syncer)
	client.errorName = "kube-apiserver-tls-cert-abc123"
	require.ErrorContains(test, syncer.SyncOnce(test.Context(), key), "forbidden")
	status, err := syncer.serviceProviderClusterLister.Get(test.Context(), key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	require.NoError(test, err)
	require.Equal(test, expectedTLSCertificate("kube-apiserver-tls-cert-abc123", false), status.Status.AzureResources.KubeAPIServerCertificate)
}

func TestKubeAPIServerTLSCertificateNeedsWork(test *testing.T) {
	syncer, _, cluster := newKubeAPIServerObservationFixture(test)
	status := &coreapi.ServiceProviderCluster{}
	require.True(test, syncer.needsWork(cluster, status))
	status.Status.AzureResources.KubeAPIServerCertificate = expectedTLSCertificate("kube-apiserver-tls-cert-abc123", true)
	require.False(test, syncer.needsWork(cluster, status))
	status.Status.AzureResources.KubeAPIServerCertificate = expectedTLSCertificate("kube-apiserver-tls-cert-abc123", false)
	require.True(test, syncer.needsWork(cluster, status))
	now := metav1.Now()
	cluster.ServiceProviderProperties.DeletionTimestamp = &now
	require.True(test, syncer.needsWork(cluster, status))
	cluster.ServiceProviderProperties.DeletionTimestamp = nil
	cluster.ServiceProviderProperties.ClusterServiceID = nil
	require.False(test, syncer.needsWork(cluster, status))
}

func TestKubeAPIServerTLSCertificateConflict(test *testing.T) {
	syncer, key, _ := newKubeAPIServerObservationFixture(test)
	require.NoError(test, syncer.SyncOnce(test.Context(), key))
	stale, err := syncer.serviceProviderClusterLister.Get(test.Context(), key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	require.NoError(test, err)
	concurrent := stale.DeepCopy()
	concurrent.Status.HostedClusterNamespace = "concurrent-update"
	_, err = syncer.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(test.Context(), concurrent, nil)
	require.NoError(test, err)
	syncer.serviceProviderClusterLister = &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{stale}}
	fakeClientFor(test, syncer).status = ptr.To("completed")
	require.NoError(test, syncer.SyncOnce(test.Context(), key)) // observes against stale lister; write conflicts and is dropped
	syncer.serviceProviderClusterLister = &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: syncer.resourcesDBClient}
	actual, err := syncer.serviceProviderClusterLister.Get(test.Context(), key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	require.NoError(test, err)
	require.Equal(test, "concurrent-update", actual.Status.HostedClusterNamespace)
	require.Equal(test, expectedTLSCertificate("kube-apiserver-tls-cert-abc123", false), actual.Status.AzureResources.KubeAPIServerCertificate)
	require.NoError(test, syncer.SyncOnce(test.Context(), key))
	actual, err = syncer.serviceProviderClusterLister.Get(test.Context(), key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	require.NoError(test, err)
	require.Equal(test, expectedTLSCertificate("kube-apiserver-tls-cert-abc123", true), actual.Status.AzureResources.KubeAPIServerCertificate)
	require.Equal(test, "concurrent-update", actual.Status.HostedClusterNamespace)
}

func TestKubeAPIServerTLSCertificatePrerequisites(test *testing.T) {
	for name, mutate := range map[string]func(*tlsCertificateSyncer, *coreapi.Cluster){
		"deleting": func(_ *tlsCertificateSyncer, cluster *coreapi.Cluster) {
			now := metav1.Now()
			cluster.ServiceProviderProperties.DeletionTimestamp = &now
		},
		"no CS ID": func(_ *tlsCertificateSyncer, cluster *coreapi.Cluster) {
			cluster.ServiceProviderProperties.ClusterServiceID = nil
		},
		"no cluster": func(syncer *tlsCertificateSyncer, _ *coreapi.Cluster) {
			syncer.clusterLister = &corelistertesting.SliceClusterLister{}
		},
		"no service provider": func(syncer *tlsCertificateSyncer, _ *coreapi.Cluster) {
			syncer.serviceProviderClusterLister = &corelistertesting.SliceServiceProviderClusterLister{}
		},
		"no management cluster": func(syncer *tlsCertificateSyncer, _ *coreapi.Cluster) {
			syncer.managementClusterLister = &fleetlistertesting.SliceManagementClusterLister{}
		},
	} {
		test.Run(name, func(test *testing.T) {
			syncer, key, cluster := newKubeAPIServerObservationFixture(test)
			mutate(syncer, cluster)
			require.NoError(test, syncer.SyncOnce(test.Context(), key))
			require.NoError(test, syncer.SyncOnce(test.Context(), key))
		})
	}
}
