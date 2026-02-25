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
	"embed"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/test-integration/utils/databasemutationhelpers"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

//go:embed artifacts/*
var artifacts embed.FS

func TestControllerNotifications(t *testing.T) {
	defer integrationutils.VerifyNoNewGoLeaks(t)

	integrationutils.WithAndWithoutCosmos(t, func(t *testing.T, withMock bool) {

		frontendStarted := atomic.Bool{}
		frontendErrCh := make(chan error, 1)
		defer func() { // this has to happen after the cancel() is called.
			if frontendStarted.Load() {
				require.NoError(t, <-frontendErrCh)
			}
		}()
		backendStarted := atomic.Bool{}
		backendErrCh := make(chan error, 2)
		defer func() { // this has to happen after the cancel() is called.
			if backendStarted.Load() {
				require.NoError(t, <-backendErrCh)
				require.NoError(t, <-backendErrCh)
			}
		}()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		ctx = utils.ContextWithLogger(ctx, integrationutils.DefaultLogger(t))

		testInfo, err := integrationutils.NewIntegrationTestInfoFromEnv(ctx, t, withMock)
		require.NoError(t, err)
		cleanupCtx := context.Background()
		cleanupCtx = utils.ContextWithLogger(cleanupCtx, integrationutils.DefaultLogger(t))
		defer testInfo.Cleanup(cleanupCtx)
		go func() {
			frontendStarted.Store(true)
			frontendErrCh <- testInfo.Frontend.Run(ctx)
		}()

		cosmosClient := testInfo.CosmosClient()
		backendInformers := informers.NewBackendInformersWithRelistDuration(ctx, cosmosClient.GlobalListers(), ptr.To(100*time.Millisecond))

		_, activeOperationLister := backendInformers.ActiveOperations()
		clusterInformer, _ := backendInformers.Clusters()
		testSyncer := newTestController(activeOperationLister)
		testingController := controllerutils.NewClusterWatchingController(
			"TestingController", cosmosClient, clusterInformer, 1*time.Minute, testSyncer)

		go func() {
			backendStarted.Store(true)
			backendInformers.RunWithContext(ctx)
			backendErrCh <- nil
		}()
		go func() {
			testingController.Run(ctx, 20)
			backendErrCh <- nil
		}()

		clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/32350638-2403-4bc9-a36e-4922c8c99b52/resourceGroups/resourceGroupName/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/basic"))
		frontendClientAccessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, "2024-06-10-preview")

		subscriptionResourceID := api.Must(arm.ToSubscriptionResourceID(clusterResourceID.SubscriptionID))
		subscriptionJSONBytes := api.Must(artifacts.ReadFile("artifacts/subscription-32350638-2403-4bc9-a36e-4922c8c99b52.json"))
		require.NoError(t, frontendClientAccessor.CreateOrUpdate(ctx, subscriptionResourceID.String(), subscriptionJSONBytes))
		clusterJSONBytes := api.Must(artifacts.ReadFile("artifacts/cluster-basic.json"))
		err = frontendClientAccessor.CreateOrUpdate(ctx, clusterResourceID.String(), clusterJSONBytes)
		require.NoError(t, err)

		select {
		case <-time.After(10 * time.Second):
		case <-testSyncer.synced:
		case <-ctx.Done():
		}

		require.Equal(t, testSyncer.count.Load(), int32(1), "missing sync")

		testSyncer.observedKeys.Range(func(key, value interface{}) bool {
			t.Log(key)
			require.Equal(t, strings.ToLower(clusterResourceID.String()), strings.ToLower(key.(string)))
			return true
		})
	})
}

type testController struct {
	cooldownChecker controllerutils.CooldownChecker

	count        atomic.Int32
	observedKeys sync.Map
	synced       chan struct{}
}

func newTestController(activeOperationLister listers.ActiveOperationLister) *testController {
	c := &testController{
		cooldownChecker: controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		observedKeys:    sync.Map{},
		synced:          make(chan struct{}),
	}

	return c
}

func (c *testController) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("SyncOnce", "key", key)

	c.count.Add(1)
	c.observedKeys.Store(key.GetResourceID().String(), true)

	if c.count.Load() > 0 {
		close(c.synced)
	}

	return nil
}

func (c *testController) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}
