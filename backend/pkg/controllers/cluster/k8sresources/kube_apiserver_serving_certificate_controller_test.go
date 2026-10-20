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

package k8sresources

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	ocmv1 "open-cluster-management.io/api/work/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	apisconfigv1 "github.com/Azure/ARO-HCP/backend/pkg/apis/config/v1"
	azureconfig "github.com/Azure/ARO-HCP/backend/pkg/azure/config"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
)

type kubeAPIServerCertificateFixture struct {
	*certificateFixture
	syncer *kubeAPIServerServingCertificateSyncer
}

func kubeAPIServerTestReference() *coreapi.AzureTLSCertificateReference {
	return &coreapi.AzureTLSCertificateReference{KeyVaultURL: "https://cluster-secrets.vault.azure.net/", CertificateName: "kube-apiserver-tls-cert-abc123"}
}

func newKubeAPIServerCertificateFixture(test *testing.T) *kubeAPIServerCertificateFixture {
	test.Helper()
	fixture := newCertificateFixture(test)
	fixture.serviceProvider.Status.AzureResources.IngressCertificate = nil
	fixture.serviceProvider.Status.AzureResources.KubeAPIServerCertificate = &coreapi.TLSCertificate{AzureReference: kubeAPIServerTestReference()}
	return &kubeAPIServerCertificateFixture{certificateFixture: fixture, syncer: &kubeAPIServerServingCertificateSyncer{
		clusterLister:                fixture.syncer.clusterLister,
		serviceProviderClusterLister: fixture.syncer.serviceProviderClusterLister,
		managementClusterLister:      fixture.syncer.managementClusterLister,
		kubeApplierDBClients:         fixture.syncer.kubeApplierDBClients,
		applyDesireLister:            fixture.syncer.applyDesireLister,
		readDesireLister:             fixture.syncer.readDesireLister,
		serviceTenantID:              fixture.syncer.serviceTenantID,
		cloudName:                    fixture.syncer.cloudName,
	}}
}

func TestKubeAPIServerServingCertificateContentsAndIdempotence(test *testing.T) {
	fixture := newKubeAPIServerCertificateFixture(test)
	require.NoError(test, fixture.syncer.SyncOnce(test.Context(), fixture.key))
	applies := fixture.applies(test)
	reads := fixture.reads(test)
	require.Len(test, applies, 2)
	require.Len(test, reads, 2)
	for _, desire := range applies {
		require.Equal(test, kubeapplierapi.ApplyDesireTypeServerSideApply, desire.Spec.Type)
		require.NotNil(test, desire.Spec.ServerSideApply.FieldManager)
		require.Equal(test, ocmv1.DefaultFieldManager, *desire.Spec.ServerSideApply.FieldManager)
		require.NotEqual(test, "open-cluster-management-policies", desire.Spec.TargetItem.Namespace)
		require.Equal(test, fixture.managementCluster.ResourceID, desire.Spec.ManagementCluster)
		require.Equal(test, strings.ToLower(fixture.managementCluster.ResourceID.String()), desire.PartitionKey)
		require.Equal(test, KubeAPIServerServingCertificateControllerName, desire.Tags[kubeapplierapi.TagControllerName])
		require.True(test, strings.EqualFold(fixture.cluster.ResourceID.String(), desire.ResourceID.Parent.String()))
		require.Equal(test, "hosted-cluster-namespace", desire.Spec.TargetItem.Namespace)
		require.Equal(test, kubeAPIServerServingCertSecretName, desire.Spec.TargetItem.Name)
		var content map[string]any
		require.NoError(test, json.Unmarshal(desire.Spec.ServerSideApply.KubeContent.Raw, &content))
		require.Equal(test, map[string]any{"name": kubeAPIServerServingCertSecretName, "namespace": "hosted-cluster-namespace"}, content["metadata"])
		spec, err := json.Marshal(content["spec"])
		require.NoError(test, err)
		switch content["kind"] {
		case "SecretProviderClass":
			require.Equal(test, "secrets-store.csi.x-k8s.io/v1", content["apiVersion"])
			require.Equal(test, "secretproviderclasses", desire.Spec.TargetItem.Resource)
			require.JSONEq(test, `{"provider":"azure","parameters":{"cloudName":"AzurePublicCloud","keyvaultName":"cluster-secrets","tenantId":"service-tenant-id","usePodIdentity":"false","useVMManagedIdentity":"true","userAssignedIdentityID":"secrets-identity-client-id","objects":"array:\n  - |\n    objectName: \"kube-apiserver-tls-cert-abc123\"\n    objectType: secret\n"}}`, string(spec))
		case "SecretSync":
			require.Equal(test, "secret-sync.x-k8s.io/v1alpha1", content["apiVersion"])
			require.Equal(test, "secretsyncs", desire.Spec.TargetItem.Resource)
			require.JSONEq(test, `{"secretSyncControllerName":"","serviceAccountName":"default","secretProviderClassName":"`+kubeAPIServerServingCertSecretName+`","secretObject":{"type":"kubernetes.io/tls","data":[{"sourcePath":"kube-apiserver-tls-cert-abc123","targetKey":"tls.key"},{"sourcePath":"kube-apiserver-tls-cert-abc123","targetKey":"tls.crt"}]}}`, string(spec))
		default:
			test.Fatalf("unexpected kind: %v", content["kind"])
		}
		var matchingRead *kubeapplierapi.ReadDesire
		for _, read := range reads {
			if read.ResourceID.Name == desire.ResourceID.Name {
				matchingRead = read
			}
		}
		require.NotNil(test, matchingRead)
		require.Equal(test, desire.Spec.TargetItem, matchingRead.Spec.TargetItem)
		require.Equal(test, desire.Spec.ManagementCluster, matchingRead.Spec.ManagementCluster)
		require.Equal(test, desire.Tags, matchingRead.Tags)
	}
	require.NoError(test, fixture.syncer.SyncOnce(test.Context(), fixture.key))
	require.ElementsMatch(test, applies, fixture.applies(test))
	require.ElementsMatch(test, reads, fixture.reads(test))
}

func TestKubeAPIServerServingCertificateCloudName(test *testing.T) {
	for _, cloudName := range []apisconfigv1.CloudEnvironmentName{apisconfigv1.AzurePublicCloud, apisconfigv1.AzureUSGovernmentCloud, apisconfigv1.AzureChinaCloud} {
		test.Run(string(cloudName), func(test *testing.T) {
			environment, err := azureconfig.NewAzureCloudEnvironment(cloudName, nil)
			require.NoError(test, err)
			fixture := newKubeAPIServerCertificateFixture(test)
			fixture.syncer.cloudName = string(environment.Name())
			require.NoError(test, fixture.syncer.SyncOnce(test.Context(), fixture.key))
			for _, desire := range fixture.applies(test) {
				if desire.Spec.TargetItem.Resource != "secretproviderclasses" {
					continue
				}
				var content struct {
					Spec struct {
						Parameters map[string]string `json:"parameters"`
					} `json:"spec"`
				}
				require.NoError(test, json.Unmarshal(desire.Spec.ServerSideApply.KubeContent.Raw, &content))
				require.Equal(test, string(cloudName), content.Spec.Parameters["cloudName"])
				return
			}
			test.Fatal("SecretProviderClass desire not found")
		})
	}
}

func TestKubeAPIServerServingCertificateTeardown(test *testing.T) {
	fixture := newKubeAPIServerCertificateFixture(test)
	ctx := test.Context()
	require.NoError(test, fixture.syncer.SyncOnce(ctx, fixture.key))
	applyCRUD, err := fixture.client.ApplyDesiresForCluster(fixture.key.SubscriptionID, fixture.key.ResourceGroupName, fixture.key.HCPClusterName)
	require.NoError(test, err)
	readCRUD, err := fixture.client.ReadDesiresForCluster(fixture.key.SubscriptionID, fixture.key.ResourceGroupName, fixture.key.HCPClusterName)
	require.NoError(test, err)
	otherApply := fixture.applies(test)[0].DeepCopy()
	otherApply.CosmosMetadata = coreapi.CosmosMetadata{
		ResourceID:   metadataapi.Must(azcorearm.ParseResourceID(kubeapplierapihelpers.ToClusterScopedApplyDesireResourceIDString(fixture.key.SubscriptionID, fixture.key.ResourceGroupName, fixture.key.HCPClusterName, "OtherController"))),
		PartitionKey: otherApply.PartitionKey,
	}
	otherApply.Tags[kubeapplierapi.TagControllerName] = "OtherController"
	_, err = applyCRUD.Create(ctx, otherApply, nil)
	require.NoError(test, err)
	otherRead := fixture.reads(test)[0].DeepCopy()
	otherRead.CosmosMetadata = coreapi.CosmosMetadata{
		ResourceID:   metadataapi.Must(azcorearm.ParseResourceID(kubeapplierapihelpers.ToClusterScopedReadDesireResourceIDString(fixture.key.SubscriptionID, fixture.key.ResourceGroupName, fixture.key.HCPClusterName, "OtherController"))),
		PartitionKey: otherRead.PartitionKey,
	}
	otherRead.Tags[kubeapplierapi.TagControllerName] = "OtherController"
	_, err = readCRUD.Create(ctx, otherRead, nil)
	require.NoError(test, err)
	now := metav1.Now()
	fixture.cluster.ServiceProviderProperties.DeletionTimestamp = &now
	fixture.cluster.ServiceProviderProperties.ClusterServiceID = nil
	fixture.serviceProvider.Status.AzureResources.KubeAPIServerCertificate = &coreapi.TLSCertificate{PendingReference: kubeAPIServerTestReference()}
	fixture.serviceProvider.Status.HostedClusterNamespace = ""
	fixture.syncer.managementClusterLister = &fleetlistertesting.SliceManagementClusterLister{}
	fixture.syncer.serviceTenantID = ""
	for range 2 {
		applyLister, readLister := fixture.syncer.applyDesireLister, fixture.syncer.readDesireLister
		fixture.syncer.applyDesireLister, fixture.syncer.readDesireLister = nil, nil
		require.NoError(test, fixture.syncer.SyncOnce(ctx, fixture.key))
		fixture.syncer.applyDesireLister, fixture.syncer.readDesireLister = applyLister, readLister
		applies := fixture.applies(test)
		reads := fixture.reads(test)
		require.Len(test, applies, 1)
		require.Len(test, reads, 1)
		require.Equal(test, "OtherController", applies[0].Tags[kubeapplierapi.TagControllerName])
		require.Equal(test, kubeapplierapi.ApplyDesireTypeServerSideApply, applies[0].Spec.Type)
		require.Equal(test, "OtherController", reads[0].Tags[kubeapplierapi.TagControllerName])
	}
}

func TestKubeAPIServerServingCertificatePrerequisites(test *testing.T) {
	for name, mutate := range map[string]func(*kubeAPIServerCertificateFixture){
		"certificate unobserved": func(fixture *kubeAPIServerCertificateFixture) {
			fixture.serviceProvider.Status.AzureResources.KubeAPIServerCertificate = nil
		},
		"certificate pending": func(fixture *kubeAPIServerCertificateFixture) {
			fixture.serviceProvider.Status.AzureResources.KubeAPIServerCertificate = &coreapi.TLSCertificate{PendingReference: kubeAPIServerTestReference()}
		},
		"cluster absent": func(fixture *kubeAPIServerCertificateFixture) {
			fixture.syncer.clusterLister = &corelistertesting.SliceClusterLister{}
		},
		"service provider absent": func(fixture *kubeAPIServerCertificateFixture) {
			fixture.syncer.serviceProviderClusterLister = &corelistertesting.SliceServiceProviderClusterLister{}
		},
		"placement absent": func(fixture *kubeAPIServerCertificateFixture) {
			fixture.serviceProvider.Status.ManagementClusterResourceID = nil
		},
		"client absent": func(fixture *kubeAPIServerCertificateFixture) {
			fixture.syncer.kubeApplierDBClients = kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
		},
		"cluster service ID absent": func(fixture *kubeAPIServerCertificateFixture) {
			fixture.cluster.ServiceProviderProperties.ClusterServiceID = nil
		},
		"namespace absent": func(fixture *kubeAPIServerCertificateFixture) {
			fixture.serviceProvider.Status.HostedClusterNamespace = ""
		},
		"management cluster absent": func(fixture *kubeAPIServerCertificateFixture) {
			fixture.syncer.managementClusterLister = &fleetlistertesting.SliceManagementClusterLister{}
		},
	} {
		test.Run(name, func(test *testing.T) {
			fixture := newKubeAPIServerCertificateFixture(test)
			mutate(fixture)
			require.NoError(test, fixture.syncer.SyncOnce(test.Context(), fixture.key))
			require.Empty(test, fixture.applies(test))
			require.Empty(test, fixture.reads(test))
		})
	}
}

func TestKubeAPIServerServingCertificateInvalidConfiguration(test *testing.T) {
	for name, mutate := range map[string]func(*kubeAPIServerCertificateFixture){
		"vault absent": func(fixture *kubeAPIServerCertificateFixture) {
			fixture.managementCluster.Status.HostedClustersSecretsKeyVaultURL = ""
		},
		"identity absent": func(fixture *kubeAPIServerCertificateFixture) {
			fixture.managementCluster.Status.HostedClustersSecretsKeyVaultManagedIdentityClientID = ""
		},
	} {
		test.Run(name, func(test *testing.T) {
			fixture := newKubeAPIServerCertificateFixture(test)
			mutate(fixture)
			require.ErrorContains(test, fixture.syncer.SyncOnce(test.Context(), fixture.key), "Key Vault URL")
			require.Empty(test, fixture.applies(test))
		})
	}
	for _, vaultURL := range []string{"https://%"} {
		test.Run(vaultURL, func(test *testing.T) {
			fixture := newKubeAPIServerCertificateFixture(test)
			fixture.serviceProvider.Status.AzureResources.KubeAPIServerCertificate.AzureReference.KeyVaultURL = vaultURL
			require.ErrorContains(test, fixture.syncer.SyncOnce(test.Context(), fixture.key), "failed to parse observed kube-apiserver serving certificate KeyVaultURL")
			require.Empty(test, fixture.applies(test))
			require.Empty(test, fixture.reads(test))
		})
	}
	fixture := newKubeAPIServerCertificateFixture(test)
	fixture.syncer.serviceTenantID = ""
	require.ErrorContains(test, fixture.syncer.SyncOnce(context.Background(), fixture.key), "service tenant ID")
}

func TestKubeAPIServerServingCertificateNeedsWork(test *testing.T) {
	for _, placed := range []bool{false, true} {
		for _, deleting := range []bool{false, true} {
			for _, namespace := range []string{"", "hosted-cluster-namespace"} {
				for _, certificate := range []*coreapi.TLSCertificate{nil, {}, {PendingReference: kubeAPIServerTestReference()}, {AzureReference: kubeAPIServerTestReference()}} {
					fixture := newKubeAPIServerCertificateFixture(test)
					if !placed {
						fixture.serviceProvider.Status.ManagementClusterResourceID = nil
					}
					if deleting {
						now := metav1.Now()
						fixture.cluster.ServiceProviderProperties.DeletionTimestamp = &now
					}
					fixture.serviceProvider.Status.HostedClusterNamespace = namespace
					fixture.serviceProvider.Status.AzureResources.KubeAPIServerCertificate = certificate
					expected := placed && (deleting || namespace != "" && certificate != nil && certificate.AzureReference != nil)
					require.Equal(test, expected, fixture.syncer.needsWork(fixture.cluster, fixture.serviceProvider), "placed=%t deleting=%t namespace=%q certificate=%+v", placed, deleting, namespace, certificate)
				}
			}
		}
	}
}

func TestKubeAPIServerServingCertificateUsesObservedReference(test *testing.T) {
	fixture := newKubeAPIServerCertificateFixture(test)
	fixture.serviceProvider.Status.AzureResources.KubeAPIServerCertificate.AzureReference = &coreapi.AzureTLSCertificateReference{KeyVaultURL: "https://observed-vault.vault.azure.net/", CertificateName: "observed-certificate"}
	require.NoError(test, fixture.syncer.SyncOnce(test.Context(), fixture.key))
	for _, desire := range fixture.applies(test) {
		require.Contains(test, string(desire.Spec.ServerSideApply.KubeContent.Raw), "observed-certificate")
		if desire.Spec.TargetItem.Resource == "secretproviderclasses" {
			require.Contains(test, string(desire.Spec.ServerSideApply.KubeContent.Raw), "observed-vault")
		}
	}
}

func TestKubeAPIServerServingCertificateRepairsDrift(test *testing.T) {
	fixture := newKubeAPIServerCertificateFixture(test)
	require.NoError(test, fixture.syncer.SyncOnce(test.Context(), fixture.key))
	fixture.serviceProvider.Status.HostedClusterNamespace = "updated-hosted-cluster-namespace"
	fixture.managementCluster.Status.HostedClustersSecretsKeyVaultManagedIdentityClientID = "updated-identity"
	require.NoError(test, fixture.syncer.SyncOnce(test.Context(), fixture.key))
	for _, desire := range fixture.applies(test) {
		require.Equal(test, "updated-hosted-cluster-namespace", desire.Spec.TargetItem.Namespace)
		if desire.Spec.TargetItem.Resource == "secretproviderclasses" {
			require.Contains(test, string(desire.Spec.ServerSideApply.KubeContent.Raw), "updated-identity")
		}
	}
	for _, desire := range fixture.reads(test) {
		require.Equal(test, "updated-hosted-cluster-namespace", desire.Spec.TargetItem.Namespace)
	}
}

func TestKubeAPIServerServingCertificateAdoptsExistingDesires(test *testing.T) {
	fixture := newKubeAPIServerCertificateFixture(test)
	ctx := test.Context()
	require.NoError(test, fixture.syncer.SyncOnce(ctx, fixture.key))
	applyCRUD, err := fixture.client.ApplyDesiresForCluster(fixture.key.SubscriptionID, fixture.key.ResourceGroupName, fixture.key.HCPClusterName)
	require.NoError(test, err)
	readCRUD, err := fixture.client.ReadDesiresForCluster(fixture.key.SubscriptionID, fixture.key.ResourceGroupName, fixture.key.HCPClusterName)
	require.NoError(test, err)
	for _, desire := range fixture.applies(test) {
		desire.Tags[kubeapplierapi.TagControllerName] = "ClusterResources"
		desire.Spec.ServerSideApply.KubeContent.Raw = []byte(`{"legacy":"content"}`)
		_, err := applyCRUD.Replace(ctx, desire, nil)
		require.NoError(test, err)
	}
	for _, desire := range fixture.reads(test) {
		desire.Tags[kubeapplierapi.TagControllerName] = "ClusterResources"
		_, err := readCRUD.Replace(ctx, desire, nil)
		require.NoError(test, err)
	}
	require.NoError(test, fixture.syncer.SyncOnce(ctx, fixture.key))
	require.Len(test, fixture.applies(test), 2)
	require.Len(test, fixture.reads(test), 2)
	for _, desire := range fixture.applies(test) {
		require.Contains(test, []string{"kubeapiserverservingcertsecretproviderclass", "kubeapiserverservingcertsecretsync"}, desire.ResourceID.Name)
		require.Equal(test, KubeAPIServerServingCertificateControllerName, desire.Tags[kubeapplierapi.TagControllerName])
		require.Equal(test, kubeapplierapi.ApplyDesireTypeServerSideApply, desire.Spec.Type)
		require.NotContains(test, string(desire.Spec.ServerSideApply.KubeContent.Raw), "legacy")
	}
	for _, desire := range fixture.reads(test) {
		require.Equal(test, KubeAPIServerServingCertificateControllerName, desire.Tags[kubeapplierapi.TagControllerName])
	}
}
