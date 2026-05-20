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
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/leaderelection/resourcelock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/app"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

func TestBackendExposesMetrics(t *testing.T) {
	defer integrationutils.VerifyNoNewGoLeaks(t)

	integrationutils.WithAndWithoutCosmos(t, func(t *testing.T, withMock bool) {
		ctx, cancel := context.WithCancel(t.Context())
		ctx = utils.ContextWithLogger(ctx, integrationutils.DefaultLogger(t))
		defer cancel()

		var (
			storageIntegrationTestInfo integrationutils.StorageIntegrationTestInfo
			err                        error
		)
		if withMock {
			storageIntegrationTestInfo, err = integrationutils.NewMockCosmosFromTestingEnv(ctx, t)
		} else {
			storageIntegrationTestInfo, err = integrationutils.NewCosmosFromTestingEnv(ctx, t)
		}
		require.NoError(t, err)

		clusterServiceMock := integrationutils.NewClusterServiceMock(t, storageIntegrationTestInfo.GetArtifactDir())
		internalClusterID := api.Must(api.NewInternalID("/api/clusters_mgmt/v1/clusters/test-cluster"))
		clusterServiceMock.GetOrCreateMockData(t.Name() + "_clusters")[internalClusterID.String()] = []any{
			api.Must(arohcpv1alpha1.NewCluster().ID("test-cluster").HREF(internalClusterID.String()).Build()),
		}
		clusterServiceMock.MockClusterServiceClient.EXPECT().ListProvisionShards().Return(ocm.NewSimpleProvisionShardListIterator(nil, nil)).AnyTimes()
		cleanupCtx := utils.ContextWithLogger(context.Background(), integrationutils.DefaultLogger(t))
		defer storageIntegrationTestInfo.Cleanup(cleanupCtx)
		defer clusterServiceMock.Cleanup(cleanupCtx)

		oldDefaultServeMux := http.DefaultServeMux
		http.DefaultServeMux = http.NewServeMux()
		defer func() { http.DefaultServeMux = oldDefaultServeMux }()

		registry := prometheus.NewRegistry()

		resourcesDBClient := storageIntegrationTestInfo.ResourcesDBClient()
		billingDBClient := storageIntegrationTestInfo.BillingDBClient()
		clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"))
		now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

		cluster := newMetricsTestCluster(clusterResourceID, arm.ProvisioningStateProvisioning, &now)
		_, err = resourcesDBClient.HCPClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName).Create(ctx, cluster, nil)
		require.NoError(t, err)

		operation := newMetricsTestOperation(t, clusterResourceID.SubscriptionID, "op-1", clusterResourceID, api.OperationRequestCreate, arm.ProvisioningStateSucceeded, now, now)
		_, err = resourcesDBClient.Operations(clusterResourceID.SubscriptionID).Create(ctx, operation, nil)
		require.NoError(t, err)

		metricsListener := newMetricsTestListener(t)
		metricsAddress := metricsListener.Addr().String()
		backendOptions := &app.BackendOptions{
			AppShortDescriptionName:            "backend",
			AppVersion:                         "test",
			AzureLocation:                      "fake-location",
			LeaderElectionLock:                 newFakeLeaderElectionLock("metrics-test"),
			ResourcesDBClient:                  resourcesDBClient,
			BillingDBClient:                    billingDBClient,
			FleetDBClient:                      databasetesting.NewMockFleetDBClient(),
			ClustersServiceClient:              clusterServiceMock.MockClusterServiceClient,
			MetricsRegisterer:                  registry,
			MetricsGatherer:                    registry,
			MetricsServerListenAddress:         metricsAddress,
			MetricsServerListener:              metricsListener,
			HealthzServerListenAddress:         "",
			TracerProviderShutdownFunc:         func(context.Context) error { return nil },
			MaestroSourceEnvironmentIdentifier: "test",
			ExitOnPanic:                        false,
		}

		backendErrCh := make(chan error, 1)
		go func() {
			backendErrCh <- backendOptions.RunBackend(ctx)
		}()
		defer func() {
			cancel()
			require.NoError(t, <-backendErrCh)
		}()

		metricsURL := fmt.Sprintf("http://%s/metrics", metricsAddress)
		clusterMetricLine := fmt.Sprintf(
			`backend_cluster_provision_state{phase="provisioning",resource_id="%s",subscription_id="%s"} 1`,
			strings.ToLower(clusterResourceID.String()),
			strings.ToLower(clusterResourceID.SubscriptionID),
		)
		operationMetricLine := fmt.Sprintf(
			`backend_resource_operation_phase_info{operation_type="create",phase="succeeded",resource_id="%s",resource_type="%s",subscription_id="%s"} 1`,
			strings.ToLower(operation.GetResourceID().String()),
			strings.ToLower(clusterResourceID.ResourceType.String()),
			strings.ToLower(operation.GetResourceID().SubscriptionID),
		)

		require.Eventually(t, func() bool {
			body, err := fetchMetricsBody(ctx, metricsURL)
			if err != nil {
				return false
			}
			return strings.Contains(body, clusterMetricLine) &&
				strings.Contains(body, operationMetricLine)
		}, 10*time.Second, 100*time.Millisecond)
	})
}

func newMetricsTestCluster(resourceID *azcorearm.ResourceID, provisioningState arm.ProvisioningState, createdAt *time.Time) *api.HCPOpenShiftCluster {
	return &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:         resourceID,
				SystemData: &arm.SystemData{CreatedAt: createdAt},
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: provisioningState,
			ClusterServiceID:  api.Ptr(api.Must(api.NewInternalID("/api/clusters_mgmt/v1/clusters/test-cluster"))),
		},
	}
}

func newMetricsTestOperation(t *testing.T, subscriptionID, name string, externalID *azcorearm.ResourceID, request api.OperationRequest, provisioningState arm.ProvisioningState, startTime, lastTransitionTime time.Time) *api.Operation {
	t.Helper()

	operationID := api.Must(azcorearm.ParseResourceID(fmt.Sprintf("/subscriptions/%s/providers/Microsoft.RedHatOpenShift/locations/fake-location/hcpOperationStatuses/%s", subscriptionID, name)))
	resourceID := api.Must(azcorearm.ParseResourceID(fmt.Sprintf("/subscriptions/%s/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/%s", subscriptionID, name)))
	return &api.Operation{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: resourceID,
		},
		OperationID:        operationID,
		ExternalID:         externalID,
		Request:            request,
		Status:             provisioningState,
		StartTime:          startTime,
		LastTransitionTime: lastTransitionTime,
	}
}

func newMetricsTestListener(t *testing.T) net.Listener {
	t.Helper()

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = listener.Close()
	})
	return listener
}

func fetchMetricsBody(ctx context.Context, metricsURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metricsURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}
	return string(body), nil
}

type fakeLeaderElectionLock struct {
	mu          sync.Mutex
	identity    string
	description string
	record      *resourcelock.LeaderElectionRecord
	rawRecord   []byte
}

func newFakeLeaderElectionLock(identity string) *fakeLeaderElectionLock {
	return &fakeLeaderElectionLock{
		identity:    identity,
		description: "test/metrics-lock",
	}
}

func (l *fakeLeaderElectionLock) Get(context.Context) (*resourcelock.LeaderElectionRecord, []byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.record == nil {
		return nil, nil, apierrors.NewNotFound(schema.GroupResource{Group: "coordination.k8s.io", Resource: "leases"}, l.description)
	}

	recordCopy := *l.record
	rawCopy := append([]byte(nil), l.rawRecord...)
	return &recordCopy, rawCopy, nil
}

func (l *fakeLeaderElectionLock) Create(_ context.Context, record resourcelock.LeaderElectionRecord) error {
	return l.store(record)
}

func (l *fakeLeaderElectionLock) Update(_ context.Context, record resourcelock.LeaderElectionRecord) error {
	return l.store(record)
}

func (l *fakeLeaderElectionLock) RecordEvent(string) {}

func (l *fakeLeaderElectionLock) Identity() string {
	return l.identity
}

func (l *fakeLeaderElectionLock) Describe() string {
	return l.description
}

func (l *fakeLeaderElectionLock) store(record resourcelock.LeaderElectionRecord) error {
	rawRecord, err := json.Marshal(record)
	if err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.record = &resourcelock.LeaderElectionRecord{
		HolderIdentity:       record.HolderIdentity,
		LeaseDurationSeconds: record.LeaseDurationSeconds,
		AcquireTime:          metav1.NewTime(record.AcquireTime.Time),
		RenewTime:            metav1.NewTime(record.RenewTime.Time),
		LeaderTransitions:    record.LeaderTransitions,
		Strategy:             record.Strategy,
		PreferredHolder:      record.PreferredHolder,
	}
	l.rawRecord = rawRecord
	return nil
}
