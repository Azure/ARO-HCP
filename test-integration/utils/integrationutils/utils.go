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
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/Azure/ARO-HCP/internal/api/v20240610preview"

	"github.com/prometheus/client_golang/prometheus"

	// register the APIs.
	"k8s.io/apimachinery/pkg/util/wait"

	server "github.com/Azure/ARO-HCP/admin/server/cmd/server"
	"github.com/Azure/ARO-HCP/frontend/pkg/frontend"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/audit"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func WithAndWithoutCosmos(t *testing.T, testFn func(t *testing.T, withMock bool)) {
	t.Run("WithMock", func(t *testing.T) {
		testFn(t, true)
	})

	if HasCosmos() {
		t.Run("WithCosmos", func(t *testing.T) {
			testFn(t, false)
		})
	}
}

func SkipIfNotSimulationTesting(t *testing.T) {
	if os.Getenv("FRONTEND_SIMULATION_TESTING") != "true" {
		t.Skip("Skipping test")
	}
}

func HasCosmos() bool {
	return os.Getenv("FRONTEND_SIMULATION_TESTING") == "true"
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

// NewFrontendFromMockDB creates a new Frontend using a mock database client.
// This allows tests to run without requiring a Cosmos DB emulator.
func NewFrontendFromMockDB(ctx context.Context, t *testing.T) (*IntegrationTestInfo, error) {
	return NewIntegrationTestInfoFromEnv(ctx, t, true)
}

type TestingEnvRunner func(ctx context.Context, t *testing.T) error

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
	noOpAuditClient, err := audit.NewOtelAuditClient(audit.CreateConn(false))
	if err != nil {
		return nil, err
	}
	metricsRegistry := prometheus.NewRegistry()
	aroHCPFrontend := frontend.NewFrontend(logger, frontendListener, frontendMetricsListener, metricsRegistry, storageIntegrationTestInfo.CosmosClient(), clusterServiceMockInfo.MockClusterServiceClient, noOpAuditClient, "fake-location")

	// admin setup
	adminHandler := server.NewAdminHandler(
		logger,
		storageIntegrationTestInfo.CosmosClient(),
		clusterServiceMockInfo.MockClusterServiceClient,
		nil,
	)
	adminListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	frontendURL := fmt.Sprintf("http://%s", frontendListener.Addr().String())
	adminURL := fmt.Sprintf("http://%s", adminListener.Addr().String())
	testInfo := &IntegrationTestInfo{
		StorageIntegrationTestInfo: storageIntegrationTestInfo,
		ClusterServiceMock:         clusterServiceMockInfo,
		ArtifactsDir:               storageIntegrationTestInfo.GetArtifactDir(),
		FrontendURL:                frontendURL,
		AdminURL:                   adminURL,
		Start: func(ctx context.Context) error {
			go aroHCPFrontend.Run(ctx, ctx.Done())
			go runServer(ctx, adminListener, adminHandler)
			serverUrls := []string{frontendURL, adminURL}
			// frontend: wait for migration to complete to eliminate races with our test's second call migrateCosmos and to ensure the server is ready for testing
			err = wait.PollUntilContextCancel(ctx, 1*time.Second, true, func(ctx context.Context) (bool, error) {
				for _, url := range serverUrls {
					_, err := http.Get(url)
					if err != nil {
						t.Log(err)
						return false, nil
					}
				}
				return true, nil
			})
			return err
		},
	}
	return testInfo, nil
}

func runServer(ctx context.Context, listener net.Listener, handler http.Handler) {
	adminApiServer := httptest.NewUnstartedServer(handler)
	adminApiServer.Listener = listener
	adminApiServer.Start()

	<-ctx.Done()
	adminApiServer.Close()
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
