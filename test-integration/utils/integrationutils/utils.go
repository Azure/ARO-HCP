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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/testr"
	"github.com/microsoft/go-otel-audit/audit/base"
	"github.com/microsoft/go-otel-audit/audit/msgs"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/goleak"

	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/set"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	adminApiServer "github.com/Azure/ARO-HCP/admin/server/server"
	operationcontrollers "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/frontend/pkg/frontend"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20240610preview"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20251223preview"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20260630preview"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20260901preview"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20261003preview"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
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
	// The Cosmos change-feed informer watchers log through this logger from
	// background goroutines. bc04160ce made ChangeFeedWatcher.Stop() block until
	// those goroutines finish, and the client-go Reflector calls Stop() when it
	// tears a watch down — which joins the watcher for the common case. However,
	// the Reflector starts a watcher inside ListWatcher.List() but only Stop()s
	// the watcher it obtains from Watch(); if the context is cancelled after
	// List() but before the Reflector enters its watch loop, that watcher is
	// never handed back to be Stop()d and unwinds asynchronously on ctx.Done().
	// Its deferred shutdown logging can then land after the test has completed,
	// which the testing framework reports as a data race ("Log in goroutine after
	// Test has completed"). Guard the backing *testing.T so any such late log
	// becomes a no-op instead of racing teardown. All logging works normally
	// during the test; only post-completion calls are dropped.
	safe := &afterTestSafeT{t: t}
	t.Cleanup(safe.markDone)
	return testr.NewWithInterface(safe, testr.Options{
		LogTimestamp: true,
		Verbosity:    4,
	})
}

// afterTestSafeT wraps *testing.T so Log/Helper become no-ops once the test has
// completed. markDone is registered via t.Cleanup, which the testing framework
// runs before its unsynchronized `t.done = true` write during teardown, so the
// RWMutex orders every in-flight Log call ahead of teardown and eliminates the
// data race between a late background log and test completion.
type afterTestSafeT struct {
	t    *testing.T
	mu   sync.RWMutex
	done bool
}

func (s *afterTestSafeT) markDone() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.done = true
}

func (s *afterTestSafeT) Helper() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.done {
		return
	}
	s.t.Helper()
}

func (s *afterTestSafeT) Log(args ...any) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.done {
		return
	}
	s.t.Log(args...)
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
	aroHCPFrontend := frontend.NewFrontend(logger, frontendListener, frontendMetricsListener, metricsRegistry, metricsRegistry, storageIntegrationTestInfo.ResourcesDBClient(), clusterServiceMockInfo.MockClusterServiceClient, fakeAuditClient, "fake-location", true)

	mockKubeApplierClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	testMCResourceID, err := azcorearm.ParseResourceID("/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default")
	if err != nil {
		return nil, err
	}

	hcReadDesireName := strings.ToLower(string(coreapi.MaestroBundleInternalNameReadonlyHypershiftHostedCluster))
	hcRDResourceIDStr := kubeapplierapi.ToClusterScopedReadDesireResourceIDString(
		"0465bc32-c654-41b8-8d87-9815d7abe8f6", "some-resource-group", "some-hcp-cluster", hcReadDesireName,
	)
	hcRDResourceID, err := azcorearm.ParseResourceID(hcRDResourceIDStr)
	if err != nil {
		return nil, err
	}
	mockKAClient, err := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClientWithResources(ctx, []any{
		&kubeapplierapi.ReadDesire{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   hcRDResourceID,
				PartitionKey: strings.ToLower(testMCResourceID.String()),
			},
			Spec: kubeapplierapi.ReadDesireSpec{
				ManagementCluster: testMCResourceID,
				TargetItem: kubeapplierapi.ResourceReference{
					Group:     "hypershift.openshift.io",
					Version:   "v1beta1",
					Resource:  "hostedclusters",
					Namespace: "ocm-testenv-fixed-value",
					Name:      "somecluster",
				},
			},
		},
	})
	if err != nil {
		return nil, err
	}
	mockKubeApplierClients.Register(testMCResourceID, mockKAClient)

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
		mockKubeApplierClients,
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

func MarkOperationsCompleteForName(ctx context.Context, resourcesDBClient corecosmosstorage.ResourcesDBClient, subscriptionID, resourceName string) error {
	operationsIterator := resourcesDBClient.Operations(subscriptionID).ListActiveOperations(nil)
	for _, operation := range operationsIterator.Items(ctx) {
		if operation.ExternalID.Name != resourceName {
			continue
		}
		err := operationcontrollers.UpdateOperationStatus(ctx, utilsclock.RealClock{}, resourcesDBClient, operation, coreapi.ProvisioningStateSucceeded, nil, nil)
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
	registry := coreapi.NewAPIRegistry()
	metadataapi.Must[any](nil, v20240610preview.RegisterVersion(registry))
	metadataapi.Must[any](nil, v20251223preview.RegisterVersion(registry))
	metadataapi.Must[any](nil, v20260630preview.RegisterVersion(registry))
	metadataapi.Must[any](nil, v20260901preview.RegisterVersion(registry))
	metadataapi.Must[any](nil, v20261003preview.RegisterVersion(registry))

	versions := registry.ListVersions().UnsortedList()
	sort.Strings(versions)
	return versions
}
