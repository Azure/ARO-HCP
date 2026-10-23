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

package launch

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	semver "github.com/blang/semver/v4"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	apisconfigv1 "github.com/Azure/ARO-HCP/backend/pkg/apis/config/v1"
	"github.com/Azure/ARO-HCP/backend/pkg/app"
	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	azureconfig "github.com/Azure/ARO-HCP/backend/pkg/azure/config"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/backups"
	clusterresources "github.com/Azure/ARO-HCP/backend/pkg/controllers/clusterresources"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/metadataapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/billingcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

// TestBackendLegacyIngressWiringByVersion replicates AROSLSRE-2529 at the
// service level: the full backend runs with every controller against mock
// Cosmos, a mock Cluster Service and mock kube-applier databases. It asserts
// that the legacy ACM-based ingress certificate wiring desires are only
// suppressed for control plane versions whose HCCO consumes the
// service-provider ingress default certificate (>= 5.1 natively, 4.20-4.22
// via CPO overrides). Versions without that coverage - such as the 4.23.0-ec
// payloads that predate the backport - must keep the legacy wiring, otherwise
// the guest cluster serves the CPO-generated root-CA ingress certificate.
func TestBackendLegacyIngressWiringByVersion(t *testing.T) {
	defer integrationutils.VerifyNoNewGoLeaks(t)

	integrationutils.WithAndWithoutCosmos(t, func(t *testing.T, withMock bool) {
		for _, scenario := range []struct {
			name                 string
			desiredVersion       string
			legacyDesiresPresent bool
		}{
			{name: "4.23 ec payload keeps legacy ACM ingress wiring", desiredVersion: "4.23.0-ec.1", legacyDesiresPresent: true},
			{name: "4.20 override covered version suppresses legacy ACM ingress wiring", desiredVersion: "4.20.12"},
			{name: "5.1 native version suppresses legacy ACM ingress wiring", desiredVersion: "5.1.0-rc.1"},
		} {
			t.Run(scenario.name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				ctx = utils.ContextWithLogger(ctx, integrationutils.DefaultLogger(t))
				defer cancel()

				var (
					storageIntegrationTestInfo integrationutils.StorageIntegrationTestInfo
					err                        error
				)
				if withMock {
					storageIntegrationTestInfo, err = integrationutils.NewMockCosmosFromTestingEnv(ctx, t)
				} else {
					storageIntegrationTestInfo, err = integrationutils.NewCosmosFromTestingEnv(ctx, t)
				}
				require.NoError(t, err)
				cleanupCtx := utils.ContextWithLogger(context.Background(), integrationutils.DefaultLogger(t))
				defer storageIntegrationTestInfo.Cleanup(cleanupCtx)

				const (
					subscriptionID = "11111111-1111-1111-1111-111111111111"
					rgName         = "ingress-wiring-rg"
					clusterName    = "cluster-1"
				)
				clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
					"/subscriptions/" + subscriptionID + "/resourceGroups/" + rgName +
						"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName))
				mcResourceID := metadataapi.Must(azcorearm.ParseResourceID(
					"/providers/Microsoft.RedHatOpenShift/stamps/1/managementclusters/default"))

				resourcesDBClient := storageIntegrationTestInfo.ResourcesDBClient()
				now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)

				subscription := &coreapi.Subscription{
					CosmosMetadata: coreapi.CosmosMetadata{
						ResourceID:   metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + subscriptionID)),
						PartitionKey: subscriptionID,
					},
					Properties: &coreapi.SubscriptionProperties{
						RegisteredFeatures: &[]coreapi.Feature{{
							Name:  ptr.To(metadataapi.FeatureExperimentalReleaseFeatures),
							State: ptr.To("Registered"),
						}},
					},
				}
				_, err = resourcesDBClient.Subscriptions().Create(ctx, subscription, nil)
				require.NoError(t, err, "failed to create Subscription fixture")

				cluster := &coreapi.Cluster{
					CosmosMetadata: coreapi.CosmosMetadata{
						ResourceID:   clusterResourceID,
						PartitionKey: subscriptionID,
					},
					TrackedResource: coreapi.TrackedResource{
						Resource: coreapi.Resource{
							ID:         clusterResourceID,
							SystemData: &coreapi.SystemData{CreatedAt: &now},
						},
					},
					ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{
						ProvisioningState: coreapi.ProvisioningStateSucceeded,
						ClusterServiceID: metadataapihelpers.Ptr(metadataapi.Must(metadataapi.NewInternalID(
							"/api/clusters_mgmt/v1/clusters/test-cluster"))),
					},
				}
				_, err = resourcesDBClient.HCPClusters(subscriptionID, rgName).Create(ctx, cluster, nil)
				require.NoError(t, err, "failed to create Cluster fixture")

				desiredVersion := semver.MustParse(scenario.desiredVersion)
				serviceProviderCluster := &coreapi.ServiceProviderCluster{
					CosmosMetadata: coreapi.CosmosMetadata{
						ResourceID:   serviceProviderClusterResourceID(clusterResourceID),
						PartitionKey: subscriptionID,
					},
					Spec: coreapi.ServiceProviderClusterSpec{
						ControlPlaneVersion: coreapi.ServiceProviderClusterSpecVersion{
							DesiredVersion: &desiredVersion,
						},
					},
					Status: coreapi.ServiceProviderClusterStatus{
						ManagementClusterResourceID: mcResourceID,
						HostedClusterNamespace:      "hc-" + clusterName,
						AzureResources: coreapi.AzureResources{
							IngressCertificate: &coreapi.TLSCertificate{
								AzureReference: &coreapi.AzureTLSCertificateReference{
									KeyVaultURL:     "https://ingress-wiring-kv.vault.azure.net",
									CertificateName: "cluster-1-ingress",
								},
							},
						},
					},
				}
				_, err = resourcesDBClient.ServiceProviderClusters(subscriptionID, rgName, clusterName).Create(ctx, serviceProviderCluster, nil)
				require.NoError(t, err, "failed to create ServiceProviderCluster fixture")

				fleetDBClient, err := fleetcosmosstoragetesting.NewMockFleetDBClientWithResources(ctx, []any{
					&fleetapi.ManagementCluster{
						CosmosMetadata: coreapi.CosmosMetadata{
							ResourceID:   mcResourceID,
							PartitionKey: strings.ToLower(mcResourceID.Parent.String()),
						},
						Spec: fleetapi.ManagementClusterSpec{
							SchedulingPolicy: fleetapi.ManagementClusterSchedulingPolicySchedulable,
						},
						Status: fleetapi.ManagementClusterStatus{
							AKSResourceID: metadataapi.Must(azcorearm.ParseResourceID(
								"/subscriptions/22222222-2222-2222-2222-222222222222/resourceGroups/mc-rg/providers/Microsoft.ContainerService/managedClusters/mc-1")),
							PublicDNSZoneResourceID: metadataapi.Must(azcorearm.ParseResourceID(
								"/subscriptions/22222222-2222-2222-2222-222222222222/resourceGroups/dns-rg/providers/Microsoft.Network/dnszones/example.com")),
							HostedClustersSecretsKeyVaultURL:                     "https://ingress-wiring-kv.vault.azure.net",
							HostedClustersManagedIdentitiesKeyVaultURL:           "https://ingress-wiring-mi-kv.vault.azure.net",
							HostedClustersSecretsKeyVaultManagedIdentityClientID: "12345678-1234-1234-1234-123456789012",
							MaestroConsumerName:                                  "ingress-wiring-mc",
							MaestroRESTAPIURL:                                    "http://maestro.maestro.svc.cluster.local:8000",
							MaestroGRPCTarget:                                    "maestro-grpc.maestro.svc.cluster.local:8090",
							KubeApplierCosmosContainerName:                       "kube-applier-mc-1",
						},
					},
				})
				require.NoError(t, err, "failed to create fleet fixtures")

				mockKAClient, err := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClientWithResources(ctx, nil)
				require.NoError(t, err, "failed to create kube-applier mock client")
				mockKAClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
				mockKAClients.Register(mcResourceID, mockKAClient)

				clusterServiceMock := integrationutils.NewClusterServiceMock(t, storageIntegrationTestInfo.GetArtifactDir())
				clusterServiceMock.MockClusterServiceClient.EXPECT().ListProvisionShards().
					Return(ocm.NewSimpleProvisionShardListIterator(nil, nil)).AnyTimes()
				clusterServiceMock.MockClusterServiceClient.EXPECT().GetClusterResources(gomock.Any(), gomock.Any()).
					Return(ingressWiringClusterResources(), nil).AnyTimes()
				defer clusterServiceMock.Cleanup(cleanupCtx)

				oldDefaultServeMux := http.DefaultServeMux
				http.DefaultServeMux = http.NewServeMux()
				defer func() { http.DefaultServeMux = oldDefaultServeMux }()

				registry := prometheus.NewRegistry()
				cloudEnvironment, err := azureconfig.NewAzureCloudEnvironment(apisconfigv1.AzurePublicCloud, nil)
				require.NoError(t, err)

				backendOptions := &app.BackendOptions{
					AppShortDescriptionName: "backend",
					AppVersion:              "test",
					AzureLocation:           "fake-location",
					LeaderElectionLock:      newFakeLeaderElectionLock("ingress-wiring-test"),
					StorageFactory: &ingressWiringStorageFactory{
						resources:   resourcesDBClient,
						billing:     storageIntegrationTestInfo.BillingDBClient(),
						fleet:       fleetDBClient,
						kubeApplier: mockKAClients,
					},
					ClustersServiceClient: clusterServiceMock.MockClusterServiceClient,
					VersionProfileSource:  emptyVersionProfileSource{},
					ServiceTenantID:       "fake-service-tenant",
					MetricsRegisterer:     registry,
					MetricsGatherer:       registry,
					TracerProviderShutdownFunc: func(context.Context) error {
						return nil
					},
					MaestroSourceEnvironmentIdentifier: "test",
					ExitOnPanic:                        false,
					CloudEnvironment:                   cloudEnvironment,
					BackupConfig: &backups.BackupConfig{
						BackupScheduleState:  coreapi.BackupScheduleStateEnabled,
						BackupCadenceProfile: backups.BackupCadenceProduction,
					},
					HardcodedIdentity: &azureclient.HardcodedIdentity{
						ClientID:     "test-client-id",
						ClientSecret: "test-client-secret",
						PrincipalID:  "test-principal-id",
						TenantID:     "test-tenant-id",
					},
				}

				backendErrCh := make(chan error, 1)
				go func() {
					backendErrCh <- backendOptions.RunBackend(ctx)
				}()
				defer func() {
					cancel()
					require.NoError(t, <-backendErrCh)
				}()

				legacyDesireNames := []string{
					clusterresources.DesireNameManagedCluster,
					clusterresources.DesireNameDefaultIngressConfigMap,
					clusterresources.DesireNameDefaultIngressWildcardCertSecretSync,
					clusterresources.DesireNameDefaultIngressWildcardCertSecretProviderClass,
				}

				// The HostedCluster desire is never suppressed, so its presence
				// proves the cluster resources controller completed a sync pass;
				// only then is the absence of the legacy desires meaningful.
				require.Eventually(t, func() bool {
					desires := listIngressWiringApplyDesires(t, ctx, mockKAClient, subscriptionID, rgName, clusterName)
					_, hasHostedCluster := desires[strings.ToLower(clusterresources.DesireNameHostedCluster)]
					if !hasHostedCluster {
						return false
					}
					for _, name := range legacyDesireNames {
						_, has := desires[strings.ToLower(name)]
						if has != scenario.legacyDesiresPresent {
							return false
						}
					}
					return true
				}, 90*time.Second, 250*time.Millisecond, "legacy ingress wiring desires (present=%t) never converged for version %s",
					scenario.legacyDesiresPresent, scenario.desiredVersion)
			})
		}
	})
}

func listIngressWiringApplyDesires(t *testing.T, ctx context.Context, client kubeappliercosmosstorage.KubeApplierDBClient,
	subscriptionID, resourceGroupName, clusterName string) map[string]bool {
	t.Helper()
	crud, err := client.ApplyDesiresForCluster(subscriptionID, resourceGroupName, clusterName)
	require.NoError(t, err, "failed to get ApplyDesire CRUD")
	iterator, err := crud.List(ctx, nil)
	require.NoError(t, err, "failed to list ApplyDesires")
	desires := map[string]bool{}
	for _, desire := range iterator.Items(ctx) {
		desires[strings.ToLower(desire.ResourceID.Name)] = true
	}
	return desires
}

// ingressWiringClusterResources mirrors the Cluster Service resource set for a
// cluster: the HostedCluster, the legacy ACM-path ingress wiring and the
// kube-apiserver serving certificate objects owned by the dedicated
// certificate controllers.
func ingressWiringClusterResources() *arohcpv1alpha1.ClusterResources {
	cr, err := arohcpv1alpha1.NewClusterResources().
		Resources(map[string]string{
			"hosted":       `{"apiVersion":"hypershift.openshift.io/v1beta1","kind":"HostedCluster","metadata":{"name":"cluster-1","namespace":"hc-cluster-1"}}`,
			"managed":      `{"apiVersion":"cluster.open-cluster-management.io/v1","kind":"ManagedCluster","metadata":{"name":"cluster-1"}}`,
			"config":       `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"default-ingress","namespace":"open-cluster-management-policies"}}`,
			"sync":         `{"apiVersion":"secret-sync.x-k8s.io/v1alpha1","kind":"SecretSync","metadata":{"name":"default-ingress-tls-cert-cluster-1","namespace":"open-cluster-management-policies"}}`,
			"provider":     `{"apiVersion":"secrets-store.csi.x-k8s.io/v1","kind":"SecretProviderClass","metadata":{"name":"default-ingress-tls-cert-cluster-1","namespace":"open-cluster-management-policies"}}`,
			"kas":          `{"apiVersion":"secret-sync.x-k8s.io/v1alpha1","kind":"SecretSync","metadata":{"name":"kube-apiserver-tls-cert","namespace":"hc-cluster-1"}}`,
			"kas-provider": `{"apiVersion":"secrets-store.csi.x-k8s.io/v1","kind":"SecretProviderClass","metadata":{"name":"kube-apiserver-tls-cert","namespace":"hc-cluster-1"}}`,
			"signing":      `{"apiVersion":"secrets-store.csi.x-k8s.io/v1","kind":"SecretProviderClass","metadata":{"name":"signing-key-cluster-1","namespace":"hc-cluster-1"}}`,
		}).
		Build()
	if err != nil {
		panic(err)
	}
	return cr
}

type ingressWiringStorageFactory struct {
	resources   corecosmosstorage.ResourcesDBClient
	billing     billingcosmosstorage.BillingDBClient
	fleet       fleetcosmosstorage.FleetDBClient
	kubeApplier kubeappliercosmosstorage.KubeApplierDBClients
}

func (f *ingressWiringStorageFactory) ResourcesStorageClient(string) corecosmosstorage.ResourcesDBClient {
	return f.resources
}

func (f *ingressWiringStorageFactory) BillingStorageClient(string) billingcosmosstorage.BillingDBClient {
	return f.billing
}

func (f *ingressWiringStorageFactory) FleetStorageClient(string) fleetcosmosstorage.FleetDBClient {
	return f.fleet
}

func (f *ingressWiringStorageFactory) KubeApplierStorageClients(string) kubeappliercosmosstorage.KubeApplierDBClients {
	return f.kubeApplier
}

func serviceProviderClusterResourceID(clusterResourceID *azcorearm.ResourceID) *azcorearm.ResourceID {
	return metadataapi.Must(azcorearm.ParseResourceID(
		strings.ToLower(clusterResourceID.String()) +
			"/serviceProviderClusters/" + coreapi.ServiceProviderClusterResourceName))
}
