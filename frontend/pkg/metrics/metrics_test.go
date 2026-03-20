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

package metrics

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestSubscriptionCollector(t *testing.T) {
	logger := testr.New(t)

	// Create subscription with proper CosmosMetadata
	subID := "00000000-0000-0000-0000-000000000000"
	resourceID := api.Must(arm.ToSubscriptionResourceID(subID))
	testSub := &arm.Subscription{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID: resourceID,
		},
		ResourceID:       resourceID,
		State:            arm.SubscriptionStateRegistered,
		RegistrationDate: api.Ptr(time.Now().String()),
	}

	t.Run("no subscription", func(t *testing.T) {
		mockDBClient := databasetesting.NewMockDBClient()
		r := prometheus.NewPedanticRegistry()
		collector := NewSubscriptionCollector(r, mockDBClient, "test")

		// No subscriptions pre-populated
		collector.refresh(utils.ContextWithLogger(context.Background(), logger))

		assertMetrics(t, r, 5, `# HELP frontend_subscription_collector_failed_syncs_total Total number of failed syncs for the Subscription collector.
# TYPE frontend_subscription_collector_failed_syncs_total counter
frontend_subscription_collector_failed_syncs_total 0
# HELP frontend_subscription_collector_syncs_total Total number of syncs for the Subscription collector.
# TYPE frontend_subscription_collector_syncs_total counter
frontend_subscription_collector_syncs_total 1
# HELP frontend_subscription_collector_last_sync Last sync operation's result (1: success, 0: failed).
# TYPE frontend_subscription_collector_last_sync gauge
frontend_subscription_collector_last_sync 1
`)
	})

	t.Run("refresh with 1 subscription", func(t *testing.T) {
		mockDBClient := databasetesting.NewMockDBClient()
		r := prometheus.NewPedanticRegistry()
		collector := NewSubscriptionCollector(r, mockDBClient, "test")

		// Pre-populate with one subscription
		ctx := utils.ContextWithLogger(context.Background(), logger)
		_, err := mockDBClient.Subscriptions().Create(ctx, testSub, nil)
		assert.NoError(t, err)

		collector.refresh(ctx)

		assertMetrics(t, r, 7, `
# HELP frontend_lifecycle_last_update_timestamp_seconds Reports the timestamp when the subscription has been updated for the last time.
# TYPE frontend_lifecycle_last_update_timestamp_seconds gauge
frontend_lifecycle_last_update_timestamp_seconds{location="test",subscription_id="00000000-0000-0000-0000-000000000000"} 0
# HELP frontend_lifecycle_state Reports the current state of the subscription.
# TYPE frontend_lifecycle_state gauge
frontend_lifecycle_state{location="test",state="Registered",subscription_id="00000000-0000-0000-0000-000000000000"} 1
# HELP frontend_subscription_collector_failed_syncs_total Total number of failed syncs for the Subscription collector.
# TYPE frontend_subscription_collector_failed_syncs_total counter
frontend_subscription_collector_failed_syncs_total 0
# HELP frontend_subscription_collector_syncs_total Total number of syncs for the Subscription collector.
# TYPE frontend_subscription_collector_syncs_total counter
frontend_subscription_collector_syncs_total 1
# HELP frontend_subscription_collector_last_sync Last sync operation's result (1: success, 0: failed).
# TYPE frontend_subscription_collector_last_sync gauge
frontend_subscription_collector_last_sync 1
`)
	})
}

func assertMetrics(t *testing.T, r prometheus.Gatherer, metrics int, expectedOutput string) {
	t.Helper()

	n, err := testutil.GatherAndCount(r)
	assert.NoError(t, err)
	assert.Equal(t, metrics, n)

	// We can't check the timestamp-based metrics.
	err = testutil.GatherAndCompare(
		r,
		bytes.NewBufferString(expectedOutput),
		errCounterName,
		refreshCounterName,
		lastSyncResultName,
		subscriptionStateName,
		subscriptionLastUpdatedName,
	)
	assert.NoError(t, err)

	problems, err := testutil.GatherAndLint(r)
	assert.NoError(t, err)

	for _, p := range problems {
		t.Errorf("metric %q: %s", p.Metric, p.Text)
	}
}
