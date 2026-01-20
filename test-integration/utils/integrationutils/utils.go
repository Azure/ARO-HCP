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

	// register the APIs.
	_ "github.com/Azure/ARO-HCP/internal/api/v20240610preview"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Azure/ARO-HCP/frontend/pkg/frontend"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/audit"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func SkipIfNotSimulationTesting(t *testing.T) {
	if os.Getenv("FRONTEND_SIMULATION_TESTING") != "true" {
		//t.Skip("Skipping test")
	}
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

func NewFrontendFromTestingEnv(ctx context.Context, t *testing.T) (*frontend.Frontend, *FrontendIntegrationTestInfo, error) {
	cosmosTestEnv, err := NewCosmosFromTestingEnv(ctx, t)
	if err != nil {
		return nil, nil, err
	}

	logger := utils.DefaultLogger()

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}

	metricsListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}

	noOpAuditClient, err := audit.NewOtelAuditClient(audit.CreateConn(false))
	if err != nil {
		return nil, nil, err
	}

	metricsRegistry := prometheus.NewRegistry()

	clusterServiceMockInfo := NewClusterServiceMock(t, cosmosTestEnv.ArtifactsDir)

	aroHCPFrontend := frontend.NewFrontend(logger, listener, metricsListener, metricsRegistry, cosmosTestEnv.DBClient, clusterServiceMockInfo.MockClusterServiceClient, noOpAuditClient, "fake-location")
	testInfo := &FrontendIntegrationTestInfo{
		CosmosIntegrationTestInfo: cosmosTestEnv,
		ClusterServiceMock:        clusterServiceMockInfo,
		ArtifactsDir:              cosmosTestEnv.ArtifactsDir,
		FrontendURL:               fmt.Sprintf("http://%s", listener.Addr().String()),
	}
	return aroHCPFrontend, testInfo, nil
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
