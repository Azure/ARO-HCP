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
	"sync"
	"testing"

	_ "github.com/Azure/ARO-HCP/internal/api/v20240610preview"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/testr"
	"github.com/microsoft/go-otel-audit/audit/base"
	"github.com/microsoft/go-otel-audit/audit/msgs"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/goleak"

	adminApiServer "github.com/Azure/ARO-HCP/admin/server/server"
	"github.com/Azure/ARO-HCP/frontend/pkg/frontend"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
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
	aroHCPFrontend := frontend.NewFrontend(logger, frontendListener, frontendMetricsListener, metricsRegistry, storageIntegrationTestInfo.CosmosClient(), clusterServiceMockInfo.MockClusterServiceClient, fakeAuditClient, "fake-location")

	// admin api setup
	adminListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	adminMetricsListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	adminAPI := adminApiServer.NewAdminAPI(logger, "fake-location", adminListener, adminMetricsListener, storageIntegrationTestInfo.CosmosClient(), clusterServiceMockInfo.MockClusterServiceClient, nil, nil)

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
	}
	return testInfo, nil
}

func MarkOperationsCompleteForName(ctx context.Context, dbClient database.DBClient, subscriptionID, resourceName string) error {
	operationsIterator := dbClient.Operations(subscriptionID).ListActiveOperations(nil)
	for _, operation := range operationsIterator.Items(ctx) {
		if operation.ExternalID.Name != resourceName {
			continue
		}
		err := database.UpdateOperationStatus(ctx, dbClient, operation, arm.ProvisioningStateSucceeded, nil, nil)
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
