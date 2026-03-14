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

package frontend

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/util/wait"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/test-integration/utils/databasemutationhelpers"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

// complianceScenario holds the metadata and fixture paths for a single
// version compliance test scenario, loaded from artifacts/VersionCompliance/.
type complianceScenario struct {
	name string
	dir  string

	Description       string `json:"description"`
	CreateVersion     string `json:"createVersion"`
	ResourceType      string `json:"resourceType"`
	ResourceID        string `json:"resourceID"`
	ClusterResourceID string `json:"clusterResourceID,omitempty"`
}

func TestVersionCompliance(t *testing.T) {
	defer integrationutils.VerifyNoNewGoLeaks(t)
	integrationutils.WithAndWithoutCosmos(t, testVersionCompliance)
}

func testVersionCompliance(t *testing.T, withMock bool) {
	allVersions := integrationutils.AllAPIVersions()
	scenarios := discoverScenarios(t, artifacts, "artifacts/VersionCompliance")

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			ctx = utils.ContextWithLogger(ctx, integrationutils.DefaultLogger(t))
			logger := utils.LoggerFromContext(ctx)

			// Spin up fresh mock cosmos + frontend
			testInfo, err := integrationutils.NewIntegrationTestInfoFromEnv(ctx, t, withMock)
			require.NoError(t, err)
			cleanupCtx := context.Background()
			cleanupCtx = utils.ContextWithLogger(cleanupCtx, integrationutils.DefaultLogger(t))
			defer testInfo.Cleanup(cleanupCtx)

			frontendStarted := atomic.Bool{}
			frontendErrCh := make(chan error, 1)
			defer func() {
				if frontendStarted.Load() {
					require.NoError(t, <-frontendErrCh)
				}
			}()
			adminAPIStarted := atomic.Bool{}
			adminAPIErrCh := make(chan error, 1)
			defer func() {
				if adminAPIStarted.Load() {
					require.NoError(t, <-adminAPIErrCh)
				}
			}()
			// cancel() must be deferred after the error channel reads above
			// so it runs first (LIFO), stopping the servers before we wait
			// for them to finish.
			defer cancel()
			go func() {
				frontendStarted.Store(true)
				frontendErrCh <- testInfo.Frontend.Run(ctx)
			}()
			go func() {
				adminAPIStarted.Store(true)
				adminAPIErrCh <- testInfo.AdminAPI.Run(ctx)
			}()

			// Wait for servers to be ready
			err = wait.PollUntilContextCancel(ctx, 100*time.Millisecond, true, func(ctx context.Context) (bool, error) {
				for _, url := range []string{testInfo.FrontendURL, testInfo.AdminURL} {
					resp, err := http.Get(url)
					if err != nil {
						return false, nil
					}
					if closeErr := resp.Body.Close(); closeErr != nil {
						logger.Error(closeErr, "failed to close response body")
					}
				}
				return true, nil
			})
			require.NoError(t, err)

			// Register subscription
			subscriptionID := api.Must(azcorearm.ParseResourceID(scenario.ResourceID)).SubscriptionID
			subscriptionResourceID := api.Must(arm.ToSubscriptionResourceID(subscriptionID))
			subscriptionJSON := api.Must(artifacts.ReadFile("artifacts/VersionCompliance/subscription.json"))
			subscriptionAccessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, scenario.CreateVersion)
			require.NoError(t, subscriptionAccessor.CreateOrUpdate(ctx, subscriptionResourceID.String(), subscriptionJSON))

			// For nodepool scenarios, create the parent cluster first
			if scenario.ResourceType == "nodePool" {
				require.NotEmpty(t, scenario.ClusterResourceID, "nodePool scenario must specify clusterResourceID")
				clusterJSON := api.Must(artifacts.ReadFile(scenario.dir + "/cluster.json"))
				clusterAccessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, scenario.CreateVersion)
				require.NoError(t, clusterAccessor.CreateOrUpdate(ctx, scenario.ClusterResourceID, clusterJSON))

				// Complete the cluster creation operation
				clusterResourceID := api.Must(azcorearm.ParseResourceID(scenario.ClusterResourceID))
				require.NoError(t, integrationutils.MarkOperationsCompleteForName(ctx, testInfo.CosmosClient(), subscriptionID, clusterResourceID.Name))
			}

			// Create the resource under test using the scenario's createVersion
			requestJSON := api.Must(artifacts.ReadFile(scenario.dir + "/request.json"))
			createAccessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, scenario.CreateVersion)
			require.NoError(t, createAccessor.CreateOrUpdate(ctx, scenario.ResourceID, requestJSON))

			// Complete the creation operation
			resourceID := api.Must(azcorearm.ParseResourceID(scenario.ResourceID))
			require.NoError(t, integrationutils.MarkOperationsCompleteForName(ctx, testInfo.CosmosClient(), subscriptionID, resourceID.Name))

			// GET via each version, compare to full expected response
			for _, v := range allVersions {
				t.Run("GET/"+v, func(t *testing.T) {
					getter := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, v)
					actual, err := getter.Get(ctx, scenario.ResourceID)
					require.NoError(t, err)

					expected := loadExpectedResponse(t, artifacts, scenario.dir, "get", v)
					diff, equals := databasemutationhelpers.ResourceInstanceEquals(t, expected, actual)
					if !equals {
						t.Logf("expected (from %s/expected/get/%s.json):\n%s", scenario.name, v, prettyJSON(t, expected))
						t.Logf("actual:\n%s", prettyJSON(t, actual))
						t.Errorf("GET %s response mismatch on api-version=%s (to fix: update %s/expected/get/%s.json):\n%s",
							scenario.ResourceID, v, scenario.name, v, diff)
					}
				})
			}

			// List via each version, compare to full expected list response
			listResourceID := scenario.ResourceID
			for _, v := range allVersions {
				t.Run("List/"+v, func(t *testing.T) {
					lister := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, v)
					actuals, err := lister.List(ctx, listResourceID)
					require.NoError(t, err)

					expected := loadExpectedResponse(t, artifacts, scenario.dir, "list", v)
					require.Len(t, actuals, 1, "expected exactly 1 resource in list response")

					diff, equals := databasemutationhelpers.ResourceInstanceEquals(t, expected, actuals[0])
					if !equals {
						t.Logf("expected (from %s/expected/list/%s.json):\n%s", scenario.name, v, prettyJSON(t, expected))
						t.Logf("actual:\n%s", prettyJSON(t, actuals[0]))
						t.Errorf("List response mismatch on api-version=%s (to fix: update %s/expected/list/%s.json):\n%s",
							v, scenario.name, v, diff)
					}
				})
			}
		})
	}
}

// discoverScenarios walks the artifacts FS to find all scenario.json files
// and returns them organized by resource type and scenario name.
func discoverScenarios(t *testing.T, fsys fs.FS, basePath string) []complianceScenario {
	t.Helper()

	var scenarios []complianceScenario

	// Walk resource type directories (Cluster, NodePool, etc.)
	resourceTypeDirs := api.Must(fs.ReadDir(fsys, basePath))
	for _, rtEntry := range resourceTypeDirs {
		if !rtEntry.IsDir() {
			continue
		}
		resourceTypePath := basePath + "/" + rtEntry.Name()

		// Walk scenario directories within each resource type
		scenarioDirs := api.Must(fs.ReadDir(fsys, resourceTypePath))
		for _, scenarioEntry := range scenarioDirs {
			if !scenarioEntry.IsDir() {
				continue
			}
			scenarioPath := resourceTypePath + "/" + scenarioEntry.Name()

			scenarioBytes, err := fs.ReadFile(fsys, scenarioPath+"/scenario.json")
			if err != nil {
				continue // skip directories without scenario.json
			}

			var scenario complianceScenario
			require.NoError(t, json.Unmarshal(scenarioBytes, &scenario))
			scenario.name = rtEntry.Name() + "/" + scenarioEntry.Name()
			scenario.dir = scenarioPath
			scenarios = append(scenarios, scenario)
		}
	}

	return scenarios
}

// loadExpectedResponse reads the expected response fixture file for a given
// scenario directory, operation type (get/list), and API version.
func loadExpectedResponse(t *testing.T, fsys fs.FS, scenarioDir, operation, version string) map[string]any {
	t.Helper()

	path := fmt.Sprintf("%s/expected/%s/%s.json", scenarioDir, operation, version)
	content, err := fs.ReadFile(fsys, path)
	require.NoError(t, err, "missing expected response fixture: %s", path)

	var result map[string]any
	require.NoError(t, json.Unmarshal(content, &result))
	return result
}

func prettyJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "\t")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return strings.TrimSpace(string(b))
}
