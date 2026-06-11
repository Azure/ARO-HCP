// Copyright 2025 Microsoft Corporation
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

package integrationutils

import (
	"context"
	"fmt"
	"net"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/testr"
	"github.com/microsoft/go-otel-audit/audit/base"
	"github.com/microsoft/go-otel-audit/audit/msgs"
	"github.com/prometheus/client_golang/prometheus"
	velerov1api "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	"go.uber.org/goleak"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/set"

	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	adminApiServer "github.com/Azure/ARO-HCP/admin/server/server"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/operationcontrollers"
	"github.com/Azure/ARO-HCP/frontend/pkg/frontend"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/api/v20240610preview"
	"github.com/Azure/ARO-HCP/internal/api/v20251223preview"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/recovery"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func WithAndWithoutCosmos(t *testing.T, testFn func(t *testing.T, withMock bool)) {
	t.Run("WithMock", func(t *testing.T) {
		testFn(t, true)
	})

	if hasCosmos() {
		t.Run("WithCosmos", func(t *testing.T) {
			testFn(t, false)
		})
	}
}

func hasCosmos() bool {
	return os.Getenv("FRONTEND_SIMULATION_TESTING") == "true"
}

func VerifyNoNewGoLeaks(t *testing.T) {
	goleak.VerifyNone(t,
		// can't fix
		goleak.IgnoreTopFunction("github.com/golang/glog.(*fileSink).flushDaemon"),
		// stop the bleeding so we don't make it worse.  There is a shutdownWithDrain on workqueues
		goleak.IgnoreTopFunction("k8s.io/client-go/util/workqueue.(*delayingType[...]).waitingLoop"),
		// workqueue internal goroutine that may outlive ShutDown() briefly
		goleak.IgnoreTopFunction("k8s.io/client-go/util/workqueue.(*Typed[...]).updateUnfinishedWorkLoop"),
	)
}

func DefaultLogger(t *testing.T) logr.Logger {
	return testr.NewWithInterface(t, testr.Options{
		LogTimestamp: true,
		Verbosity:    4,
	})
}

var (
	artifactDir     string
	artifactDirInit sync.Once
)

func getArtifactDir() string {
	artifactDirInit.Do(func() {
		artifactDir = os.Getenv("ARTIFACT_DIR")
		if artifactDir == "" {
			// Default to temp directory if ARTIFACT_DIR not set
			var err error
			artifactDir, err = os.MkdirTemp("", "integration-testing")
			if err != nil {
				panic(err)
			}
		}
	})
	return artifactDir
}

func NewIntegrationTestInfoFromEnv(ctx context.Context, t *testing.T, withMock bool) (*IntegrationTestInfo, error) {
	logger := utils.DefaultLogger()

	// cosmos setup
	var storageIntegrationTestInfo StorageIntegrationTestInfo
	var err error
	if withMock {
		storageIntegrationTestInfo, err = NewMockCosmosFromTestingEnv(ctx, t)
	} else {
		storageIntegrationTestInfo, err = NewCosmosFromTestingEnv(ctx, t)
	}
	if err != nil {
		return nil, err
	}

	// cluster service setup
	clusterServiceMockInfo := NewClusterServiceMock(t, storageIntegrationTestInfo.GetArtifactDir())

	// kubernetes client sets setup
	sessionNamespace := "aro-hcp-breakglass-sessions"
	kubernetesClientSets := NewKubernetesClientSets(sessionNamespace)

	// frontend setup
	frontendListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	frontendMetricsListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	fakeAuditClient := &FakeOTELClient{}
	metricsRegistry := prometheus.NewRegistry()
	aroHCPFrontend := frontend.NewFrontend(logger, frontendListener, frontendMetricsListener, metricsRegistry, metricsRegistry, storageIntegrationTestInfo.ResourcesDBClient(), storageIntegrationTestInfo.LocksDBClient(), clusterServiceMockInfo.MockClusterServiceClient, fakeAuditClient, "fake-location", "", false, false, true)

	fakeClock := clocktesting.NewFakePassiveClock(time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC))

	// create a single fake management cluster client so state persists across
	// HTTP calls within the same test case (e.g. POST then GET).
	fakeMgmtClient, err := recovery.NewFakeClient(
		&velerov1api.Backup{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test-backup-1",
				Namespace: "velero",
				Labels:    map[string]string{"api.openshift.com/id": "fixed-value"},
			},
			Status: velerov1api.BackupStatus{
				Phase: velerov1api.BackupPhaseCompleted,
			},
		},
		&hypershiftv1beta1.HostedCluster{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test-hosted-cluster",
				Namespace: "test-namespace",
				Labels:    map[string]string{"api.openshift.com/id": "fixed-value"},
			},
		},
	)
	if err != nil {
		return nil, err
	}
	fakeMgmtClientFactory := func(ctx context.Context, aksResourceID string, credential azcore.TokenCredential) (ctrlclient.Client, error) {
		return fakeMgmtClient, nil
	}

	// Pre-populate the fleet DB with a management cluster so backup handlers
	// can resolve the AKS resource ID without calling cluster-service.
	fakeStampID := "1"
	fakeMgmtClusterResourceID := api.Must(fleet.ToManagementClusterResourceID(fakeStampID))
	fakeAKSResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/fake-rg/providers/Microsoft.ContainerService/managedClusters/fake-aks-cluster"))
	fleetDBClient := storageIntegrationTestInfo.FleetDBClient()
	fakeProvisionShardID := api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/provision_shards/00000000-0000-0000-0000-000000000000"))
	_, err = fleetDBClient.Stamps().ManagementClusters(fakeStampID).Create(ctx, &fleet.ManagementCluster{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: fakeMgmtClusterResourceID,
		},
		ResourceID: fakeMgmtClusterResourceID,
		Spec: fleet.ManagementClusterSpec{
			SchedulingPolicy: fleet.ManagementClusterSchedulingPolicySchedulable,
		},
		Status: fleet.ManagementClusterStatus{
			AKSResourceID:                                        fakeAKSResourceID,
			PublicDNSZoneResourceID:                              api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/fake-dns-rg/providers/Microsoft.Network/dnszones/fake.example.com")),
			HostedClustersSecretsKeyVaultURL:                     "https://fake-kv-secrets.vault.azure.net",
			HostedClustersManagedIdentitiesKeyVaultURL:           "https://fake-kv-mi.vault.azure.net",
			HostedClustersSecretsKeyVaultManagedIdentityClientID: "00000000-0000-0000-0000-000000000001",
			MaestroConsumerName:                                  "fake-consumer",
			MaestroRESTAPIURL:                                    "http://maestro.maestro.svc.cluster.local:8000",
			MaestroGRPCTarget:                                    "maestro-grpc.maestro.svc.cluster.local:8090",
			ClusterServiceProvisionShardID:                       &fakeProvisionShardID,
			KubeApplierCosmosContainerName:                       "fake-kube-applier-container",
		},
	}, nil)
	if err != nil && !database.IsConflictError(err) {
		return nil, fmt.Errorf("failed to pre-populate management cluster: %w", err)
	}

	// Pre-populate a ServiceProviderCluster with the management cluster reference
	// so backup handlers can resolve the management cluster from Cosmos DB.
	fakeClusterResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/0465bc32-c654-41b8-8d87-9815d7abe8f6/resourceGroups/some-resource-group/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/some-hcp-cluster",
	))
	spcCRUD := storageIntegrationTestInfo.ResourcesDBClient().ServiceProviderClusters(
		fakeClusterResourceID.SubscriptionID,
		fakeClusterResourceID.ResourceGroupName,
		fakeClusterResourceID.Name,
	)
	_, err = spcCRUD.Create(ctx, &api.ServiceProviderCluster{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: api.Must(azcorearm.ParseResourceID(
				fakeClusterResourceID.String() + "/serviceProviderClusters/default",
			)),
		},
		Status: api.ServiceProviderClusterStatus{
			ManagementClusterResourceID: fakeMgmtClusterResourceID,
		},
	}, nil)
	if err != nil && !database.IsConflictError(err) {
		return nil, fmt.Errorf("failed to pre-populate service provider cluster: %w", err)
	}

	// admin api setup
	adminListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	adminMetricsListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	adminAPI := adminApiServer.NewAdminAPI(
		logger,
		"fake-location",
		adminListener,
		adminMetricsListener,
		storageIntegrationTestInfo.ResourcesDBClient(),
		storageIntegrationTestInfo.BillingDBClient(),
		storageIntegrationTestInfo.FleetDBClient(),
		clusterServiceMockInfo.MockClusterServiceClient,
		nil,
		nil,
		fakeAuditClient,
		kubernetesClientSets.SessiongateClientset.SessiongateV1alpha1().Sessions(sessionNamespace),
		kubernetesClientSets.SessionInformerFactory.Sessiongate().V1alpha1().Sessions().Lister().Sessions(sessionNamespace),
		10*time.Minute,
		24*time.Hour,
		set.New("aro-sre-pso", "aro-sre-csa"),
		metricsRegistry,
		nil,
		fakeMgmtClientFactory,
		fakeClock,
	)

	frontendURL := fmt.Sprintf("http://%s", frontendListener.Addr().String())
	adminURL := fmt.Sprintf("http://%s", adminListener.Addr().String())
	testInfo := &IntegrationTestInfo{
		StorageIntegrationTestInfo: storageIntegrationTestInfo,
		ClusterServiceMock:         clusterServiceMockInfo,
		ArtifactsDir:               storageIntegrationTestInfo.GetArtifactDir(),
		FrontendURL:                frontendURL,
		Frontend:                   aroHCPFrontend,
		AdminURL:                   adminURL,
		AdminAPI:                   adminAPI,
		adminAPIListener:           adminListener,
		KubernetesClientSets:       kubernetesClientSets,
	}
	return testInfo, nil
}

func MarkOperationsCompleteForName(ctx context.Context, resourcesDBClient database.ResourcesDBClient, subscriptionID, resourceName string) error {
	operationsIterator := resourcesDBClient.Operations(subscriptionID).ListActiveOperations(nil)
	for _, operation := range operationsIterator.Items(ctx) {
		if operation.ExternalID.Name != resourceName {
			continue
		}
		err := operationcontrollers.UpdateOperationStatus(ctx, utilsclock.RealClock{}, resourcesDBClient, operation, arm.ProvisioningStateSucceeded, nil, nil)
		if err != nil {
			return err
		}
	}
	if operationsIterator.GetError() != nil {
		return operationsIterator.GetError()
	}
	return nil
}

type FakeOTELClient struct{}

func (t *FakeOTELClient) Send(ctx context.Context, msg msgs.Msg, options ...base.SendOption) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("Sending message", "msg", msg)
	return nil
}

// AllAPIVersions returns a sorted list of all registered API versions.
// IMPORTANT: When adding a new API version to frontend/pkg/frontend/frontend.go,
// also add a RegisterVersion call here.
func AllAPIVersions() []string {
	registry := api.NewAPIRegistry()
	api.Must[any](nil, v20240610preview.RegisterVersion(registry))
	api.Must[any](nil, v20251223preview.RegisterVersion(registry))

	versions := registry.ListVersions().UnsortedList()
	sort.Strings(versions)
	return versions
}
