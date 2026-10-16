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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	apisconfigv1 "github.com/Azure/ARO-HCP/backend/pkg/apis/config/v1"
	azureconfig "github.com/Azure/ARO-HCP/backend/pkg/azure/config"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
)

type certificateFixture struct {
	syncer            *ingressCertificateSyncer
	key               controllerutils.HCPClusterKey
	cluster           *coreapi.Cluster
	serviceProvider   *coreapi.ServiceProviderCluster
	managementCluster *fleetapi.ManagementCluster
	client            *kubeappliercosmosstoragetesting.MockKubeApplierDBClient
}

func ingressTestReference() *coreapi.AzureTLSCertificateReference {
	return &coreapi.AzureTLSCertificateReference{KeyVaultURL: "https://cluster-secrets.vault.azure.net/", CertificateName: "ingress-tls-cert-abc123"}
}

func newCertificateFixture(test *testing.T) *certificateFixture {
	test.Helper()
	key := controllerutils.HCPClusterKey{
		SubscriptionID: "00000000-0000-0000-0000-000000000001", ResourceGroupName: "group", HCPClusterName: "cluster",
	}
	clusterID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + key.SubscriptionID + "/resourceGroups/group/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"))
	serviceProviderID := metadataapi.Must(azcorearm.ParseResourceID(clusterID.String() + "/serviceProviderClusters/" + coreapi.ServiceProviderClusterResourceName))
	managementClusterID := metadataapi.Must(azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/stamps/test/managementClusters/default"))
	csID := metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/abc123"))
	cluster := &coreapi.Cluster{
		CosmosMetadata:            coreapi.CosmosMetadata{ResourceID: clusterID, PartitionKey: key.SubscriptionID},
		TrackedResource:           coreapi.TrackedResource{Resource: coreapi.Resource{ID: clusterID}},
		ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{ClusterServiceID: &csID},
	}
	serviceProvider := &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: serviceProviderID, PartitionKey: key.SubscriptionID},
		Status: coreapi.ServiceProviderClusterStatus{
			AzureResources:              coreapi.AzureResources{IngressCertificate: &coreapi.TLSCertificate{AzureReference: ingressTestReference()}},
			ManagementClusterResourceID: managementClusterID,
			HostedClusterNamespace:      "hosted-cluster-namespace",
			ControlPlaneNamespace:       "not-the-hosted-cluster-namespace",
		},
	}
	managementCluster := &fleetapi.ManagementCluster{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: managementClusterID},
		Status: fleetapi.ManagementClusterStatus{
			HostedClustersSecretsKeyVaultURL:                     "https://cluster-secrets.vault.azure.net/",
			HostedClustersSecretsKeyVaultManagedIdentityClientID: "secrets-identity-client-id",
		},
	}
	database, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(test.Context(), []any{cluster, serviceProvider})
	require.NoError(test, err)
	serviceProvider, err = database.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Get(test.Context(), coreapi.ServiceProviderClusterResourceName)
	require.NoError(test, err)
	client := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
	clients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	clients.Register(managementClusterID, client)
	managementClusters := &fleetlistertesting.SliceManagementClusterLister{ManagementClusters: []*fleetapi.ManagementCluster{managementCluster}}
	return &certificateFixture{
		key: key, cluster: cluster, serviceProvider: serviceProvider, managementCluster: managementCluster, client: client,
		syncer: &ingressCertificateSyncer{
			resourcesDBClient:            database,
			clusterLister:                &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{cluster}},
			serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{serviceProvider}},
			managementClusterLister:      managementClusters,
			kubeApplierDBClients:         clients,
			applyDesireLister:            &kubeapplierlistertesting.DBApplyDesireLister{Clients: clients, Lister: managementClusters},
			readDesireLister:             &kubeapplierlistertesting.DBReadDesireLister{Clients: clients, Lister: managementClusters},
			serviceTenantID:              "service-tenant-id",
			cloudName:                    "AzurePublicCloud",
		},
	}
}

func (fixture *certificateFixture) applies(test *testing.T) []*kubeapplierapi.ApplyDesire {
	test.Helper()
	desires, err := fixture.syncer.applyDesireLister.ListForCluster(test.Context(), fixture.key.SubscriptionID, fixture.key.ResourceGroupName, fixture.key.HCPClusterName)
	require.NoError(test, err)
	return desires
}

func (fixture *certificateFixture) reads(test *testing.T) []*kubeapplierapi.ReadDesire {
	test.Helper()
	desires, err := fixture.syncer.readDesireLister.ListForCluster(test.Context(), fixture.key.SubscriptionID, fixture.key.ResourceGroupName, fixture.key.HCPClusterName)
	require.NoError(test, err)
	return desires
}

func TestIngressCertificateContentsAndIdempotence(test *testing.T) {
	fixture := newCertificateFixture(test)
	require.NoError(test, fixture.syncer.SyncOnce(test.Context(), fixture.key))
	applies := fixture.applies(test)
	reads := fixture.reads(test)
	require.Len(test, applies, 2)
	require.Len(test, reads, 2)
	for _, desire := range applies {
		require.Equal(test, kubeapplierapi.ApplyDesireTypeServerSideApply, desire.Spec.Type)
		require.Nil(test, desire.Spec.ServerSideApply.FieldManager)
		require.NotEqual(test, "open-cluster-management-policies", desire.Spec.TargetItem.Namespace)
		require.Equal(test, fixture.managementCluster.ResourceID, desire.Spec.ManagementCluster)
		require.Equal(test, strings.ToLower(fixture.managementCluster.ResourceID.String()), desire.PartitionKey)
		require.Equal(test, IngressCertificateControllerName, desire.Tags[kubeapplierapi.TagControllerName])
		require.True(test, strings.EqualFold(fixture.cluster.ResourceID.String(), desire.ResourceID.Parent.String()))
		require.Equal(test, "hosted-cluster-namespace", desire.Spec.TargetItem.Namespace)
		require.Equal(test, controllerutils.ServiceProviderDefaultIngressWildcardServingCertName, desire.Spec.TargetItem.Name)
		var content map[string]any
		require.NoError(test, json.Unmarshal(desire.Spec.ServerSideApply.KubeContent.Raw, &content))
		require.Equal(test, map[string]any{"name": controllerutils.ServiceProviderDefaultIngressWildcardServingCertName, "namespace": "hosted-cluster-namespace"}, content["metadata"])
		spec, err := json.Marshal(content["spec"])
		require.NoError(test, err)
		switch content["kind"] {
		case "SecretProviderClass":
			require.Equal(test, "secrets-store.csi.x-k8s.io/v1", content["apiVersion"])
			require.Equal(test, "secretproviderclasses", desire.Spec.TargetItem.Resource)
			require.JSONEq(test, `{"provider":"azure","parameters":{"cloudName":"AzurePublicCloud","keyvaultName":"cluster-secrets","tenantId":"service-tenant-id","usePodIdentity":"false","useVMManagedIdentity":"true","userAssignedIdentityID":"secrets-identity-client-id","objects":"array:\n  - |\n    objectName: \"ingress-tls-cert-abc123\"\n    objectType: secret\n"}}`, string(spec))
		case "SecretSync":
			require.Equal(test, "secret-sync.x-k8s.io/v1alpha1", content["apiVersion"])
			require.Equal(test, "secretsyncs", desire.Spec.TargetItem.Resource)
			require.JSONEq(test, `{"secretSyncControllerName":"","serviceAccountName":"default","secretProviderClassName":"`+controllerutils.ServiceProviderDefaultIngressWildcardServingCertName+`","secretObject":{"type":"kubernetes.io/tls","data":[{"sourcePath":"ingress-tls-cert-abc123","targetKey":"tls.key"},{"sourcePath":"ingress-tls-cert-abc123","targetKey":"tls.crt"}]}}`, string(spec))
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

func TestIngressCertificateCloudName(test *testing.T) {
	for _, cloudName := range []apisconfigv1.CloudEnvironmentName{apisconfigv1.AzurePublicCloud, apisconfigv1.AzureUSGovernmentCloud, apisconfigv1.AzureChinaCloud} {
		test.Run(string(cloudName), func(test *testing.T) {
			environment, err := azureconfig.NewAzureCloudEnvironment(cloudName, nil)
			require.NoError(test, err)
			fixture := newCertificateFixture(test)
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

func TestIngressCertificateTeardown(test *testing.T) {
	fixture := newCertificateFixture(test)
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
	fixture.serviceProvider.Status.AzureResources.IngressCertificate = &coreapi.TLSCertificate{PendingReference: ingressTestReference()}
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

func TestIngressCertificatePrerequisites(test *testing.T) {
	for name, mutate := range map[string]func(*certificateFixture){
		"certificate unobserved": func(fixture *certificateFixture) {
			fixture.serviceProvider.Status.AzureResources.IngressCertificate = nil
		},
		"certificate pending": func(fixture *certificateFixture) {
			fixture.serviceProvider.Status.AzureResources.IngressCertificate = &coreapi.TLSCertificate{PendingReference: ingressTestReference()}
		},
		"cluster absent": func(fixture *certificateFixture) {
			fixture.syncer.clusterLister = &corelistertesting.SliceClusterLister{}
		},
		"service provider absent": func(fixture *certificateFixture) {
			fixture.syncer.serviceProviderClusterLister = &corelistertesting.SliceServiceProviderClusterLister{}
		},
		"placement absent": func(fixture *certificateFixture) { fixture.serviceProvider.Status.ManagementClusterResourceID = nil },
		"client absent": func(fixture *certificateFixture) {
			fixture.syncer.kubeApplierDBClients = kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
		},
		"cluster service ID absent": func(fixture *certificateFixture) { fixture.cluster.ServiceProviderProperties.ClusterServiceID = nil },
		"namespace absent":          func(fixture *certificateFixture) { fixture.serviceProvider.Status.HostedClusterNamespace = "" },
		"management cluster absent": func(fixture *certificateFixture) {
			fixture.syncer.managementClusterLister = &fleetlistertesting.SliceManagementClusterLister{}
		},
	} {
		test.Run(name, func(test *testing.T) {
			fixture := newCertificateFixture(test)
			mutate(fixture)
			require.NoError(test, fixture.syncer.SyncOnce(test.Context(), fixture.key))
			require.Empty(test, fixture.applies(test))
			require.Empty(test, fixture.reads(test))
		})
	}
}

func TestIngressCertificateInvalidConfiguration(test *testing.T) {
	for name, mutate := range map[string]func(*certificateFixture){
		"vault absent": func(fixture *certificateFixture) {
			fixture.managementCluster.Status.HostedClustersSecretsKeyVaultURL = ""
		},
		"identity absent": func(fixture *certificateFixture) {
			fixture.managementCluster.Status.HostedClustersSecretsKeyVaultManagedIdentityClientID = ""
		},
	} {
		test.Run(name, func(test *testing.T) {
			fixture := newCertificateFixture(test)
			mutate(fixture)
			require.ErrorContains(test, fixture.syncer.SyncOnce(test.Context(), fixture.key), "Key Vault URL")
			require.Empty(test, fixture.applies(test))
		})
	}
	for _, vaultURL := range []string{"https://%"} {
		test.Run(vaultURL, func(test *testing.T) {
			fixture := newCertificateFixture(test)
			fixture.serviceProvider.Status.AzureResources.IngressCertificate.AzureReference.KeyVaultURL = vaultURL
			require.ErrorContains(test, fixture.syncer.SyncOnce(test.Context(), fixture.key), "failed to parse observed ingress certificate KeyVaultURL")
			require.Empty(test, fixture.applies(test))
			require.Empty(test, fixture.reads(test))
		})
	}
	fixture := newCertificateFixture(test)
	fixture.syncer.serviceTenantID = ""
	require.ErrorContains(test, fixture.syncer.SyncOnce(context.Background(), fixture.key), "service tenant ID")
}

func TestIngressCertificateNeedsWork(test *testing.T) {
	for _, placed := range []bool{false, true} {
		for _, deleting := range []bool{false, true} {
			for _, namespace := range []string{"", "hosted-cluster-namespace"} {
				for _, certificate := range []*coreapi.TLSCertificate{nil, {}, {PendingReference: ingressTestReference()}, {AzureReference: ingressTestReference()}} {
					fixture := newCertificateFixture(test)
					if !placed {
						fixture.serviceProvider.Status.ManagementClusterResourceID = nil
					}
					if deleting {
						now := metav1.Now()
						fixture.cluster.ServiceProviderProperties.DeletionTimestamp = &now
					}
					fixture.serviceProvider.Status.HostedClusterNamespace = namespace
					fixture.serviceProvider.Status.AzureResources.IngressCertificate = certificate
					expected := placed && (deleting || namespace != "" && certificate != nil && certificate.AzureReference != nil)
					require.Equal(test, expected, fixture.syncer.needsWork(fixture.cluster, fixture.serviceProvider), "placed=%t deleting=%t namespace=%q certificate=%+v", placed, deleting, namespace, certificate)
				}
			}
		}
	}
}

func TestIngressCertificateUsesObservedReference(test *testing.T) {
	fixture := newCertificateFixture(test)
	fixture.serviceProvider.Status.AzureResources.IngressCertificate.AzureReference = &coreapi.AzureTLSCertificateReference{KeyVaultURL: "https://observed-vault.vault.azure.net/", CertificateName: "observed-certificate"}
	require.NoError(test, fixture.syncer.SyncOnce(test.Context(), fixture.key))
	for _, desire := range fixture.applies(test) {
		require.Contains(test, string(desire.Spec.ServerSideApply.KubeContent.Raw), "observed-certificate")
		if desire.Spec.TargetItem.Resource == "secretproviderclasses" {
			require.Contains(test, string(desire.Spec.ServerSideApply.KubeContent.Raw), "observed-vault")
		}
	}
}

func TestIngressCertificateRepairsDrift(test *testing.T) {
	fixture := newCertificateFixture(test)
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

func TestIngressCertificatePersistsDesiredHostedCluster(test *testing.T) {
	for _, state := range []string{"nil", "empty", "operator", "existing"} {
		test.Run(state, func(test *testing.T) {
			fixture := newCertificateFixture(test)
			ctx := test.Context()
			crud := fixture.syncer.resourcesDBClient.ServiceProviderClusters(fixture.key.SubscriptionID, fixture.key.ResourceGroupName, fixture.key.HCPClusterName)
			existing, err := crud.Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(test, err)
			if state != "nil" {
				existing.Spec.DesiredHostedCluster = &hsv1beta1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "preserved"}}
			}
			if state == "operator" || state == "existing" {
				existing.Spec.DesiredHostedCluster.Spec.OperatorConfiguration = &hsv1beta1.OperatorConfiguration{}
			}
			if state == "existing" {
				existing.Spec.DesiredHostedCluster.Spec.OperatorConfiguration.IngressOperator = &hsv1beta1.IngressOperatorSpec{}
				existing.Spec.DesiredHostedCluster.Spec.OperatorConfiguration.IngressOperator.DefaultCertificate.Name = "old-certificate"
			}
			_, err = crud.Replace(ctx, existing, nil)
			require.NoError(test, err)
			fixture.syncer.serviceProviderClusterLister = &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: fixture.syncer.resourcesDBClient}
			before, err := fixture.syncer.serviceProviderClusterLister.Get(ctx, fixture.key.SubscriptionID, fixture.key.ResourceGroupName, fixture.key.HCPClusterName)
			require.NoError(test, err)
			original := before.DeepCopy()
			require.NoError(test, fixture.syncer.SyncOnce(ctx, fixture.key))
			actual, err := crud.Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(test, err)
			require.Equal(test, controllerutils.ServiceProviderDefaultIngressWildcardServingCertName, actual.Spec.DesiredHostedCluster.Spec.OperatorConfiguration.IngressOperator.DefaultCertificate.Name)
			require.Equal(test, original.Status, actual.Status)
			require.Equal(test, original, before)
			if state != "nil" {
				require.Equal(test, "preserved", actual.Spec.DesiredHostedCluster.Name)
			}
			require.NoError(test, fixture.syncer.SyncOnce(ctx, fixture.key))
			repeated, err := crud.Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(test, err)
			require.Equal(test, actual.CosmosETag, repeated.CosmosETag)
			stale := actual.DeepCopy()
			stale.CosmosETag = "stale"
			replacement := stale.DeepCopy()
			replacement.Spec.DesiredHostedCluster.Name = "must-not-overwrite"
			require.NoError(test, fixture.syncer.persistIfChanged(ctx, fixture.key, stale, replacement))
			current, err := crud.Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(test, err)
			require.Equal(test, actual, current)
		})
	}
}
