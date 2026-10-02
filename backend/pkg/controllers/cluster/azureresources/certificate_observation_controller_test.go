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
	"errors"
	"testing"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
)

func newObservationFixture(test *testing.T) (*certificateObservationSyncer, controllerutils.HCPClusterKey, *coreapi.Cluster) {
	test.Helper()
	cluster := newTestCluster(false)
	clusterServiceID := metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/abc123"))
	cluster.ServiceProviderProperties.ClusterServiceID = &clusterServiceID
	serviceProvider := newTestServiceProviderCluster(coreapi.AzureReference{})
	managementID := metadataapi.Must(azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/stamps/test/managementClusters/default"))
	serviceProvider.Status.ManagementClusterResourceID = managementID
	database, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(test.Context(), []any{cluster, serviceProvider})
	require.NoError(test, err)
	return &certificateObservationSyncer{
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

func TestCertificateObservationPendingToDone(test *testing.T) {
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
			require.Equal(test, coreapi.CertificateObservationPending, pending.Status.KubeAPIServerCertificate)
			require.Equal(test, coreapi.CertificateObservationPending, pending.Status.IngressCertificate)
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
				require.Equal(test, coreapi.CertificateObservationDone, partial.Status.KubeAPIServerCertificate)
				require.Equal(test, coreapi.CertificateObservationPending, partial.Status.IngressCertificate)
			} else {
				require.Equal(test, coreapi.CertificateObservationPending, partial.Status.KubeAPIServerCertificate)
				require.Equal(test, coreapi.CertificateObservationDone, partial.Status.IngressCertificate)
			}
			names = nil
			syncer.observe = func(_ context.Context, _, name string) (bool, error) { names = append(names, name); return true, nil }
			require.NoError(test, syncer.SyncOnce(test.Context(), key))
			require.Len(test, names, 1)
			require.NotEqual(test, first, names[0])
			done := readStatus()
			require.Equal(test, coreapi.CertificateObservationDone, done.Status.KubeAPIServerCertificate)
			require.Equal(test, coreapi.CertificateObservationDone, done.Status.IngressCertificate)
			require.False(test, syncer.NeedsWork(cluster, done))
			require.NoError(test, syncer.SyncOnce(test.Context(), key))
			require.Len(test, names, 1)
		})
	}
}

func TestCertificateObservationReadErrorPreservesIndependentProgress(test *testing.T) {
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
	require.Equal(test, coreapi.CertificateObservationPending, status.Status.KubeAPIServerCertificate)
	require.Equal(test, coreapi.CertificateObservationDone, status.Status.IngressCertificate)
}

func TestCertificateObservationNeedsWork(test *testing.T) {
	syncer, _, cluster := newObservationFixture(test)
	status := &coreapi.ServiceProviderCluster{}
	require.True(test, syncer.NeedsWork(cluster, status))
	status.Status.IngressCertificate = coreapi.CertificateObservationDone
	require.True(test, syncer.NeedsWork(cluster, status))
	status.Status.KubeAPIServerCertificate = coreapi.CertificateObservationDone
	require.False(test, syncer.NeedsWork(cluster, status))
	status.Status.KubeAPIServerCertificate = coreapi.CertificateObservationPending
	require.True(test, syncer.NeedsWork(cluster, status))
	now := metav1.Now()
	cluster.ServiceProviderProperties.DeletionTimestamp = &now
	require.False(test, syncer.NeedsWork(cluster, status))
	cluster.ServiceProviderProperties.DeletionTimestamp = nil
	cluster.ServiceProviderProperties.ClusterServiceID = nil
	require.False(test, syncer.NeedsWork(cluster, status))
}

func TestCertificateObservationConflict(test *testing.T) {
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
	require.Equal(test, coreapi.CertificateObservationPending, actual.Status.IngressCertificate)
	require.NoError(test, syncer.SyncOnce(test.Context(), key))
	actual, err = syncer.serviceProviderClusterLister.Get(test.Context(), key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	require.NoError(test, err)
	require.Equal(test, coreapi.CertificateObservationDone, actual.Status.IngressCertificate)
	require.Equal(test, coreapi.CertificateObservationDone, actual.Status.KubeAPIServerCertificate)
	require.Equal(test, "concurrent-update", actual.Status.HostedClusterNamespace)
}

func TestCertificateObservationPrerequisites(test *testing.T) {
	for name, mutate := range map[string]func(*certificateObservationSyncer, *coreapi.Cluster){
		"deleting": func(_ *certificateObservationSyncer, cluster *coreapi.Cluster) {
			now := metav1.Now()
			cluster.ServiceProviderProperties.DeletionTimestamp = &now
		},
		"no CS ID": func(_ *certificateObservationSyncer, cluster *coreapi.Cluster) {
			cluster.ServiceProviderProperties.ClusterServiceID = nil
		},
		"no cluster": func(syncer *certificateObservationSyncer, _ *coreapi.Cluster) {
			syncer.clusterLister = &corelistertesting.SliceClusterLister{}
		},
		"no service provider": func(syncer *certificateObservationSyncer, _ *coreapi.Cluster) {
			syncer.serviceProviderClusterLister = &corelistertesting.SliceServiceProviderClusterLister{}
		},
		"no management cluster": func(syncer *certificateObservationSyncer, _ *coreapi.Cluster) {
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

func TestCertificateReady(test *testing.T) {
	now := time.Now()
	for name, mutate := range map[string]func(*azcertificates.Certificate){
		"no certificate": func(cert *azcertificates.Certificate) { cert.CER = nil },
		"no secret":      func(cert *azcertificates.Certificate) { cert.SID = nil },
		"empty secret":   func(cert *azcertificates.Certificate) { cert.SID = ptr.To(azcertificates.ID("")) },
		"no attributes":  func(cert *azcertificates.Certificate) { cert.Attributes = nil },
		"disabled":       func(cert *azcertificates.Certificate) { cert.Attributes.Enabled = ptr.To(false) },
		"not yet valid":  func(cert *azcertificates.Certificate) { cert.Attributes.NotBefore = ptr.To(now.Add(time.Hour)) },
		"expired":        func(cert *azcertificates.Certificate) { cert.Attributes.Expires = ptr.To(now) },
	} {
		test.Run(name, func(test *testing.T) {
			cert := azcertificates.Certificate{CER: []byte("public certificate"), SID: ptr.To(azcertificates.ID("https://vault/secrets/cert/version")), Attributes: &azcertificates.CertificateAttributes{Enabled: ptr.To(true)}}
			require.True(test, certificateReady(cert, now))
			mutate(&cert)
			require.False(test, certificateReady(cert, now))
		})
	}
}
