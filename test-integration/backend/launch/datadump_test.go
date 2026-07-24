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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/datadumpcontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/test-integration/utils/databasemutationhelpers"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

func TestDataDumpControllerWithClusterAndCredentialRequest(t *testing.T) {
	defer integrationutils.VerifyNoNewGoLeaks(t)

	integrationutils.WithAndWithoutCosmos(t, func(t *testing.T, withMock bool) {
		t.Skip("slow test")
		frontendStarted := atomic.Bool{}
		frontendErrCh := make(chan error, 1)
		defer func() {
			if frontendStarted.Load() {
				require.NoError(t, <-frontendErrCh)
			}
		}()
		backendStarted := atomic.Bool{}
		backendErrCh := make(chan error, 2)
		defer func() {
			if backendStarted.Load() {
				require.NoError(t, <-backendErrCh)
				require.NoError(t, <-backendErrCh)
			}
		}()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		logCapture := &logCapture{}
		logger := funcr.New(func(prefix, args string) {
			line := prefix + args
			t.Log(line)
			logCapture.add(line)
		}, funcr.Options{Verbosity: 4})
		ctx = utils.ContextWithLogger(ctx, logger)

		testInfo, err := integrationutils.NewIntegrationTestInfoFromEnv(ctx, t, withMock)
		require.NoError(t, err)
		cleanupCtx := context.Background()
		cleanupCtx = utils.ContextWithLogger(cleanupCtx, integrationutils.DefaultLogger(t))
		defer testInfo.Cleanup(cleanupCtx)
		go func() {
			frontendStarted.Store(true)
			frontendErrCh <- testInfo.Frontend.Run(ctx)
		}()

		resourcesDBClient := testInfo.ResourcesDBClient()
		backendInformers := informers.NewBackendInformersWithRelistDuration(ctx, resourcesDBClient.ResourcesGlobalListers(), resourcesDBClient, testInfo.BillingDBClient().BillingGlobalListers(), nil)

		_, activeOperationLister := backendInformers.ActiveOperations()
		dataDumpController := datadumpcontrollers.NewClusterRecursiveDataDumpController(
			resourcesDBClient, nil, nil, activeOperationLister, backendInformers, nil)

		go func() {
			backendStarted.Store(true)
			backendInformers.RunWithContext(ctx)
			backendErrCh <- nil
		}()
		go func() {
			dataDumpController.Run(ctx, 20)
			backendErrCh <- nil
		}()

		clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/32350638-2403-4bc9-a36e-4922c8c99b52/resourceGroups/resourceGroupName/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/basic"))
		frontendClientAccessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, "2024-06-10-preview")

		subscriptionResourceID := api.Must(arm.ToSubscriptionResourceID(clusterResourceID.SubscriptionID))
		subscriptionJSONBytes := api.Must(artifacts.ReadFile("artifacts/subscription-32350638-2403-4bc9-a36e-4922c8c99b52.json"))
		require.NoError(t, frontendClientAccessor.CreateOrUpdate(ctx, subscriptionResourceID.String(), subscriptionJSONBytes))
		clusterJSONBytes := api.Must(artifacts.ReadFile("artifacts/cluster-basic.json"))
		require.NoError(t, frontendClientAccessor.CreateOrUpdate(ctx, clusterResourceID.String(), clusterJSONBytes))

		credentialName := "test-cred-0001"
		credResourceID := api.Must(api.ToSystemAdminCredentialRequestResourceID(
			clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName, clusterResourceID.Name, credentialName))
		credRequest := &api.SystemAdminCredentialRequest{
			CosmosMetadata: api.CosmosMetadata{
				ResourceID:   credResourceID,
				PartitionKey: strings.ToLower(clusterResourceID.SubscriptionID),
			},
			Spec: api.SystemAdminCredentialRequestSpec{
				Username:    "system:admin:test-cred-0001",
				OperationID: "test-op-cred",
			},
		}
		credCRUD := resourcesDBClient.SystemAdminCredentialRequests(
			clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName, clusterResourceID.Name)
		_, err = credCRUD.Create(ctx, credRequest, nil)
		require.NoError(t, err)

		controllerCRUD := resourcesDBClient.HCPClusters(
			clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName).Controllers(clusterResourceID.Name)
		require.Eventually(t, func() bool {
			controller, err := controllerCRUD.Get(ctx, "DataDump")
			return err == nil && controller != nil
		}, 30*time.Second, 200*time.Millisecond, "datadump controller should have written a controller status document")

		credResourceIDLower := strings.ToLower(credResourceID.String())
		clusterResourceIDLower := strings.ToLower(clusterResourceID.String())

		isDumpLine := func(line string) bool {
			lineLower := strings.ToLower(line)
			return strings.Contains(lineLower, "dumping resourceid") && strings.Contains(lineLower, "snapshottype")
		}
		isClusterDump := func(line string) bool {
			lineLower := strings.ToLower(line)
			return isDumpLine(line) && strings.Contains(lineLower, clusterResourceIDLower) && !strings.Contains(lineLower, "systemadmincredentialrequests")
		}
		isCredentialDump := func(line string) bool {
			lineLower := strings.ToLower(line)
			return isDumpLine(line) && strings.Contains(lineLower, credResourceIDLower)
		}

		firstDumpLines := logCapture.lines()
		foundClusterDump := false
		foundCredentialDump := false
		for _, line := range firstDumpLines {
			if isClusterDump(line) {
				foundClusterDump = true
			}
			if isCredentialDump(line) {
				foundCredentialDump = true
			}
		}
		require.True(t, foundClusterDump, "first dump should contain the cluster resource")
		require.True(t, foundCredentialDump, "first dump should contain the credential request resource")

		firstDumpCount := len(firstDumpLines)
		require.Eventually(t, func() bool {
			allLines := logCapture.lines()
			newLines := allLines[firstDumpCount:]
			for _, line := range newLines {
				if isCredentialDump(line) {
					return true
				}
			}
			return false
		}, 90*time.Second, 1*time.Second, "resync should produce a second dump containing the credential request")

		resyncLines := logCapture.lines()[firstDumpCount:]
		foundClusterDump = false
		foundCredentialDump = false
		for _, line := range resyncLines {
			if isClusterDump(line) {
				foundClusterDump = true
			}
			if isCredentialDump(line) {
				foundCredentialDump = true
			}
		}
		require.True(t, foundClusterDump, "resync dump should contain the cluster resource")
		require.True(t, foundCredentialDump, "resync dump should contain the credential request resource")

		t.Log("=== ALL CAPTURED LOG LINES ===")
		for i, line := range logCapture.lines() {
			t.Logf("[%d] %s", i, line)
		}
		t.Fatal("dumping all logs for review")
	})
}

type logCapture struct {
	mu       sync.Mutex
	captured []string
}

func (c *logCapture) add(line string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.captured = append(c.captured, line)
}

func (c *logCapture) lines() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.captured))
	copy(out, c.captured)
	return out
}
