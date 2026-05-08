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

package controllerutils

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type mockClusterSyncer struct {
	syncOnceFunc func(ctx context.Context, key HCPClusterKey) error
	cooldown     CooldownChecker
}

func (m *mockClusterSyncer) SyncOnce(ctx context.Context, key HCPClusterKey) error {
	if m.syncOnceFunc != nil {
		return m.syncOnceFunc(ctx, key)
	}
	return nil
}

func (m *mockClusterSyncer) CooldownChecker() CooldownChecker {
	if m.cooldown != nil {
		return m.cooldown
	}
	return NewTimeBasedCooldownChecker(time.Minute)
}

func TestClusterWatchingControllerSyncHasLoggerContextValues(t *testing.T) {
	subscriptionID := "00000000-0000-0000-0000-000000000000"
	resourceGroup := "test-rg"
	clusterName := "test-cluster"

	var capturedCtx context.Context
	mockSyncer := &mockClusterSyncer{
		syncOnceFunc: func(ctx context.Context, key HCPClusterKey) error {
			capturedCtx = ctx
			return nil
		},
	}

	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	controller := NewClusterWatchingController(
		"test-controller",
		mockResourcesDBClient,
		nil, // nil informers for unit testing
		time.Minute,
		mockSyncer,
	)

	gwc := controller.(*genericWatchingController[HCPClusterKey])
	gwc.queue.Add(HCPClusterKey{
		SubscriptionID:    subscriptionID,
		ResourceGroupName: resourceGroup,
		HCPClusterName:    clusterName,
	})

	var logOutput strings.Builder
	logger := funcr.New(func(prefix, args string) {
		logOutput.WriteString(prefix)
		logOutput.WriteString(args)
	}, funcr.Options{})
	ctx := utils.ContextWithLogger(context.Background(), logger)

	gwc.processNextWorkItem(ctx)

	require.NotNil(t, capturedCtx, "syncer should have been called")

	// Log a message using the captured logger to verify its values
	capturedLogger := utils.LoggerFromContext(capturedCtx)
	capturedLogger.Info("test")

	output := logOutput.String()
	require.Contains(t, output, ` "subscription_id"="00000000-0000-0000-0000-000000000000" `, "logger should contain subscription_id")
	require.Contains(t, output, ` "resource_group"="test-rg" `, "logger should contain resource_group")
	require.Contains(t, output, ` "resource_name"="test-cluster" `, "logger should contain cluster name")
	require.Contains(t, output, `"hcp_cluster_name"="/subscriptions/00000000-0000-0000-0000-000000000000/resourcegroups/test-rg/providers/microsoft.redhatopenshift/hcpopenshiftclusters/test-cluster"`)

}
