// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package recoverycontroller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/backup"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
)

var testKey = controllerutils.HCPClusterKey{
	SubscriptionID:    "test-sub",
	ResourceGroupName: "test-rg",
	HCPClusterName:    "test-cluster",
}

type alwaysSyncCooldownChecker struct{}

func (a *alwaysSyncCooldownChecker) CanSync(_ context.Context, _ any) bool { return true }

func TestFindActiveRecovery(t *testing.T) {
	tests := []struct {
		name            string
		requests        []coreapi.RecoveryRequest
		statuses        []coreapi.RecoveryStatus
		wantNil         bool
		wantReqID       string
		wantStatusID    string
		wantStatusState coreapi.RecoveryState
		wantStatusLen   int
		wantErrContains string
	}{
		{
			name:          "no recovery requests",
			requests:      nil,
			statuses:      nil,
			wantNil:       true,
			wantStatusLen: 0,
		},
		{
			name:            "single request, no existing status",
			requests:        []coreapi.RecoveryRequest{{RecoveryId: "r1", BackupId: "b1"}},
			statuses:        nil,
			wantReqID:       "r1",
			wantStatusID:    "r1",
			wantStatusState: coreapi.RecoveryStatePending,
			wantStatusLen:   1,
		},
		{
			name:          "single request, matching Pending status",
			requests:      []coreapi.RecoveryRequest{{RecoveryId: "r1"}},
			statuses:      []coreapi.RecoveryStatus{{RecoveryId: "r1", State: coreapi.RecoveryStatePending}},
			wantReqID:     "r1",
			wantStatusID:  "r1",
			wantStatusLen: 1,
		},
		{
			name:          "single request, non-terminal RecoveryCRCreated status",
			requests:      []coreapi.RecoveryRequest{{RecoveryId: "r1"}},
			statuses:      []coreapi.RecoveryStatus{{RecoveryId: "r1", State: coreapi.RecoveryStateRecoveryCRCreated}},
			wantReqID:     "r1",
			wantStatusID:  "r1",
			wantStatusLen: 1,
		},
		{
			name:          "single request, non-terminal Monitoring status",
			requests:      []coreapi.RecoveryRequest{{RecoveryId: "r1"}},
			statuses:      []coreapi.RecoveryStatus{{RecoveryId: "r1", State: coreapi.RecoveryStateMonitoring}},
			wantReqID:     "r1",
			wantStatusID:  "r1",
			wantStatusLen: 1,
		},
		{
			name:          "single request, terminal Completed status",
			requests:      []coreapi.RecoveryRequest{{RecoveryId: "r1"}},
			statuses:      []coreapi.RecoveryStatus{{RecoveryId: "r1", State: coreapi.RecoveryStateCompleted}},
			wantNil:       true,
			wantStatusLen: 1,
		},
		{
			name:          "single request, terminal Failed status",
			requests:      []coreapi.RecoveryRequest{{RecoveryId: "r1"}},
			statuses:      []coreapi.RecoveryStatus{{RecoveryId: "r1", State: coreapi.RecoveryStateFailed}},
			wantNil:       true,
			wantStatusLen: 1,
		},
		{
			name:            "two requests, neither has status entry",
			requests:        []coreapi.RecoveryRequest{{RecoveryId: "r1"}, {RecoveryId: "r2"}},
			statuses:        nil,
			wantErrContains: "found 2 active recoveries",
		},
		{
			name:     "two requests, both with non-terminal status",
			requests: []coreapi.RecoveryRequest{{RecoveryId: "r1"}, {RecoveryId: "r2"}},
			statuses: []coreapi.RecoveryStatus{
				{RecoveryId: "r1", State: coreapi.RecoveryStatePending},
				{RecoveryId: "r2", State: coreapi.RecoveryStatePending},
			},
			wantErrContains: "found 2 active recoveries",
		},
		{
			name:     "two requests, first terminal second active",
			requests: []coreapi.RecoveryRequest{{RecoveryId: "r1"}, {RecoveryId: "r2"}},
			statuses: []coreapi.RecoveryStatus{
				{RecoveryId: "r1", State: coreapi.RecoveryStateCompleted},
				{RecoveryId: "r2", State: coreapi.RecoveryStatePending},
			},
			wantReqID:     "r2",
			wantStatusID:  "r2",
			wantStatusLen: 2,
		},
		{
			name:     "two requests, first active second terminal",
			requests: []coreapi.RecoveryRequest{{RecoveryId: "r1"}, {RecoveryId: "r2"}},
			statuses: []coreapi.RecoveryStatus{
				{RecoveryId: "r1", State: coreapi.RecoveryStatePending},
				{RecoveryId: "r2", State: coreapi.RecoveryStateFailed},
			},
			wantReqID:     "r1",
			wantStatusID:  "r1",
			wantStatusLen: 2,
		},
		{
			name:     "two requests, both terminal",
			requests: []coreapi.RecoveryRequest{{RecoveryId: "r1"}, {RecoveryId: "r2"}},
			statuses: []coreapi.RecoveryStatus{
				{RecoveryId: "r1", State: coreapi.RecoveryStateCompleted},
				{RecoveryId: "r2", State: coreapi.RecoveryStateFailed},
			},
			wantNil:       true,
			wantStatusLen: 2,
		},
		{
			name:     "status entry for unrelated ID is ignored",
			requests: []coreapi.RecoveryRequest{{RecoveryId: "r1"}},
			statuses: []coreapi.RecoveryStatus{
				{RecoveryId: "other", State: coreapi.RecoveryStateCompleted},
			},
			wantReqID:       "r1",
			wantStatusID:    "r1",
			wantStatusState: coreapi.RecoveryStatePending,
			wantStatusLen:   2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spc := &coreapi.ServiceProviderCluster{
				Spec: coreapi.ServiceProviderClusterSpec{
					RecoveryRequests: tt.requests,
				},
				Status: coreapi.ServiceProviderClusterStatus{
					Recoveries: tt.statuses,
				},
			}

			gotReq, gotStatus, err := findActiveRecovery(spc)

			if tt.wantErrContains != "" {
				require.ErrorContains(t, err, tt.wantErrContains)
				return
			}
			require.NoError(t, err)

			assert.Len(t, spc.Status.Recoveries, tt.wantStatusLen)

			if tt.wantNil {
				assert.Nil(t, gotReq)
				assert.Nil(t, gotStatus)
				return
			}

			require.NotNil(t, gotReq, "expected non-nil RecoveryRequest")
			require.NotNil(t, gotStatus, "expected non-nil RecoveryStatus")
			assert.Equal(t, tt.wantReqID, gotReq.RecoveryId)
			assert.Equal(t, tt.wantStatusID, gotStatus.RecoveryId)
			if tt.wantStatusState != "" {
				assert.Equal(t, tt.wantStatusState, gotStatus.State)
			}
		})
	}
}

func TestRecoverySyncer_SyncOnce(t *testing.T) {
	const (
		testClusterIDStr = "/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111"
		testStampID      = "mc1"
	)

	testMgmtClusterResourceID := func() *azcorearm.ResourceID {
		return metadataapi.Must(fleetapi.ToManagementClusterResourceID(testStampID))
	}

	newTestCluster := func() *coreapi.HCPOpenShiftCluster {
		resourceID := metadataapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testKey.SubscriptionID +
				"/resourceGroups/" + testKey.ResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testKey.HCPClusterName,
		))
		csID := metadataapi.Must(metadataapi.NewInternalID(testClusterIDStr))
		return &coreapi.HCPOpenShiftCluster{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   resourceID,
				PartitionKey: strings.ToLower(resourceID.SubscriptionID),
			},
			ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
				ClusterServiceID: &csID,
			},
		}
	}

	seedSPCWithRequests := func(
		t *testing.T,
		ctx context.Context,
		mockDB *corecosmosstoragetesting.MockResourcesDBClient,
		mcResourceID *azcorearm.ResourceID,
		requests []coreapi.RecoveryRequest,
		statuses []coreapi.RecoveryStatus,
	) {
		t.Helper()
		spc, err := corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockDB, testKey.GetResourceID())
		require.NoError(t, err)
		spc.Status.ManagementClusterResourceID = mcResourceID
		spc.Spec.RecoveryRequests = requests
		spc.Status.Recoveries = statuses
		_, err = mockDB.ServiceProviderClusters(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName).Replace(ctx, spc, nil)
		require.NoError(t, err)
	}

	tests := []struct {
		name            string
		seedDB          func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient)
		registerKA      bool
		wantErrContains string
	}{
		{
			name: "cluster not found is no-op",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
			},
		},
		{
			name: "SPC with no recovery requests is no-op",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(), nil)
				require.NoError(t, err)
				// SPC created implicitly by GetOrCreateServiceProviderCluster with no requests
			},
		},
		{
			name: "SPC with no management cluster resource ID is no-op",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(), nil)
				require.NoError(t, err)
				seedSPCWithRequests(t, ctx, mockDB, nil, // nil mcResourceID
					[]coreapi.RecoveryRequest{{RecoveryId: "r1"}},
					nil,
				)
			},
		},
		{
			name: "KA client not registered for MC is no-op",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(), nil)
				require.NoError(t, err)
				seedSPCWithRequests(t, ctx, mockDB, testMgmtClusterResourceID(),
					[]coreapi.RecoveryRequest{{RecoveryId: "r1"}},
					nil,
				)
			},
			registerKA: false, // MC not registered → For() returns nil
		},
		{
			name: "multiple active recoveries returns error",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(), nil)
				require.NoError(t, err)
				seedSPCWithRequests(t, ctx, mockDB, testMgmtClusterResourceID(),
					[]coreapi.RecoveryRequest{{RecoveryId: "r1"}, {RecoveryId: "r2"}},
					[]coreapi.RecoveryStatus{
						{RecoveryId: "r1", State: coreapi.RecoveryStatePending},
						{RecoveryId: "r2", State: coreapi.RecoveryStatePending},
					},
				)
			},
			registerKA:      true,
			wantErrContains: "found 2 active recoveries",
		},
		{
			name: "active recovery with BackupState not Paused runs process without error",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(), nil)
				require.NoError(t, err)
				seedSPCWithRequests(t, ctx, mockDB, testMgmtClusterResourceID(),
					[]coreapi.RecoveryRequest{{RecoveryId: "r1"}},
					nil,
				)
			},
			registerKA: true,
		},
		{
			name: "active recovery with BackupState already Paused runs process without error",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(), nil)
				require.NoError(t, err)
				spc, err := corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockDB, testKey.GetResourceID())
				require.NoError(t, err)
				spc.Status.ManagementClusterResourceID = testMgmtClusterResourceID()
				spc.Spec.RecoveryRequests = []coreapi.RecoveryRequest{{RecoveryId: "r1"}}
				spc.Spec.BackupScheduleState = coreapi.BackupScheduleStateDisabled
				_, err = mockDB.ServiceProviderClusters(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName).Replace(ctx, spc, nil)
				require.NoError(t, err)
			},
			registerKA: true,
		},
		{
			name: "all recoveries terminal is no-op",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(), nil)
				require.NoError(t, err)
				seedSPCWithRequests(t, ctx, mockDB, testMgmtClusterResourceID(),
					[]coreapi.RecoveryRequest{{RecoveryId: "r1"}},
					[]coreapi.RecoveryStatus{{RecoveryId: "r1", State: coreapi.RecoveryStateCompleted}},
				)
			},
			registerKA: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
			mockKA := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
			mockKAClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
			if tt.registerKA {
				mockKAClients.Register(testMgmtClusterResourceID(), mockKA)
			}

			tt.seedDB(t, ctx, mockDB)

			syncer := &recoverySyncer{
				cooldownChecker:      &alwaysSyncCooldownChecker{},
				cosmosClient:         mockDB,
				kubeApplierDBClients: mockKAClients,
			}

			err := syncer.SyncOnce(ctx, testKey)

			if tt.wantErrContains != "" {
				require.ErrorContains(t, err, tt.wantErrContains)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// stubRdCrud overrides List and Get to inject configurable errors.
type stubRdCrud struct {
	cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire]
	listErr error
	iterErr error
	getErr  error
}

func (s *stubRdCrud) List(_ context.Context, _ *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[kubeapplierapi.ReadDesire], error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return &erroringRdIterator{err: s.iterErr}, nil
}

func (s *stubRdCrud) Get(_ context.Context, _ string) (*kubeapplierapi.ReadDesire, error) {
	return nil, s.getErr
}

// stubAdCrud overrides Get to inject a configurable error.
type stubAdCrud struct {
	cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire]
	getErr error
}

func (s *stubAdCrud) Get(_ context.Context, _ string) (*kubeapplierapi.ApplyDesire, error) {
	return nil, s.getErr
}

// erroringRdIterator yields no items but returns an error from GetError.
type erroringRdIterator struct {
	err error
}

func (e *erroringRdIterator) Items(_ context.Context) cosmosstorageutils.DBClientIteratorItem[kubeapplierapi.ReadDesire] {
	return func(yield func(string, *kubeapplierapi.ReadDesire) bool) {}
}

func (e *erroringRdIterator) GetContinuationToken() string { return "" }
func (e *erroringRdIterator) GetError() error              { return e.err }

// makeScheduleRd builds a ReadDesire whose name has the given prefix and whose
// KubeContent contains a velerov1.Schedule with Paused set accordingly.
func makeScheduleRd(t *testing.T, name string, paused bool) *kubeapplierapi.ReadDesire {
	t.Helper()
	mcResourceID := metadataapi.Must(fleetapi.ToManagementClusterResourceID("mc1"))
	resourceIDStr := kubeapplierapi.ToClusterScopedReadDesireResourceIDString(
		testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName, name,
	)
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(resourceIDStr))
	raw, err := json.Marshal(velerov1.Schedule{
		Spec: velerov1.ScheduleSpec{Paused: paused},
	})
	require.NoError(t, err)
	return &kubeapplierapi.ReadDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(mcResourceID.String()),
		},
		Spec: kubeapplierapi.ReadDesireSpec{
			ManagementCluster: mcResourceID,
		},
		Status: kubeapplierapi.ReadDesireStatus{
			KubeContent: &k8sruntime.RawExtension{Raw: raw},
		},
	}
}

func TestFetchSchedules(t *testing.T) {
	newRdCrud := func(t *testing.T, ctx context.Context, rds ...*kubeapplierapi.ReadDesire) cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire] {
		t.Helper()
		mockKA := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		rdCrud, err := mockKA.ReadDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
		require.NoError(t, err)
		for _, rd := range rds {
			_, err := rdCrud.Create(ctx, rd, nil)
			require.NoError(t, err)
		}
		return rdCrud
	}

	tests := []struct {
		name            string
		setupCrud       func(t *testing.T, ctx context.Context) cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire]
		wantLen         int
		wantErrContains string
	}{
		{
			name: "no items returns empty slice",
			setupCrud: func(t *testing.T, ctx context.Context) cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire] {
				return newRdCrud(t, ctx)
			},
			wantLen: 0,
		},
		{
			name: "single backupschedule item is returned",
			setupCrud: func(t *testing.T, ctx context.Context) cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire] {
				return newRdCrud(t, ctx, makeScheduleRd(t, backup.BackupScheduleDesireNamePrefix+"hourly", true))
			},
			wantLen: 1,
		},
		{
			name: "non-schedule item is filtered out",
			setupCrud: func(t *testing.T, ctx context.Context) cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire] {
				return newRdCrud(t, ctx, makeScheduleRd(t, "ondemandbackup-foo", true))
			},
			wantLen: 0,
		},
		{
			name: "mix of schedule and non-schedule items returns only schedule items",
			setupCrud: func(t *testing.T, ctx context.Context) cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire] {
				return newRdCrud(t, ctx,
					makeScheduleRd(t, backup.BackupScheduleDesireNamePrefix+"hourly", true),
					makeScheduleRd(t, backup.BackupScheduleDesireNamePrefix+"daily", true),
					makeScheduleRd(t, "ondemandbackup-foo", false),
				)
			},
			wantLen: 2,
		},
		{
			name: "list error is propagated",
			setupCrud: func(t *testing.T, ctx context.Context) cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire] {
				return &stubRdCrud{listErr: fmt.Errorf("cosmos unavailable")}
			},
			wantErrContains: "failed to list ReadDesires",
		},
		{
			name: "iterator GetError is propagated",
			setupCrud: func(t *testing.T, ctx context.Context) cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire] {
				return &stubRdCrud{iterErr: fmt.Errorf("iteration failed")}
			},
			wantErrContains: "failed to iterate ReadDesires",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			rdCrud := tt.setupCrud(t, ctx)
			state := recoverySyncState{rdCrud: rdCrud}

			got, err := fetchSchedules(ctx, state)

			if tt.wantErrContains != "" {
				require.ErrorContains(t, err, tt.wantErrContains)
				return
			}
			require.NoError(t, err)
			assert.Len(t, got, tt.wantLen)
		})
	}
}

func TestAreAllSchedulesPaused(t *testing.T) {
	makeRd := func(t *testing.T, paused bool) *kubeapplierapi.ReadDesire {
		t.Helper()
		return makeScheduleRd(t, backup.BackupScheduleDesireNamePrefix+"test", paused)
	}
	makeRdInvalidContent := func(raw []byte) *kubeapplierapi.ReadDesire {
		return &kubeapplierapi.ReadDesire{
			Status: kubeapplierapi.ReadDesireStatus{
				KubeContent: &k8sruntime.RawExtension{Raw: raw},
			},
		}
	}

	tests := []struct {
		name  string
		input []*kubeapplierapi.ReadDesire
		want  bool
	}{
		{
			name:  "empty list",
			input: nil,
			want:  true,
		},
		{
			name:  "single paused schedule",
			input: []*kubeapplierapi.ReadDesire{makeRd(t, true)},
			want:  true,
		},
		{
			name:  "single unpaused schedule",
			input: []*kubeapplierapi.ReadDesire{makeRd(t, false)},
			want:  false,
		},
		{
			name:  "multiple schedules all paused",
			input: []*kubeapplierapi.ReadDesire{makeRd(t, true), makeRd(t, true), makeRd(t, true)},
			want:  true,
		},
		{
			name:  "multiple schedules one not paused",
			input: []*kubeapplierapi.ReadDesire{makeRd(t, true), makeRd(t, false), makeRd(t, true)},
			want:  false,
		},
		{
			name:  "nil KubeContent.Raw returns false",
			input: []*kubeapplierapi.ReadDesire{makeRdInvalidContent(nil)},
			want:  false,
		},
		{
			name:  "invalid JSON in KubeContent returns false",
			input: []*kubeapplierapi.ReadDesire{makeRdInvalidContent([]byte("not-json"))},
			want:  false,
		},
		{
			name:  "mix of valid paused and invalid KubeContent returns false",
			input: []*kubeapplierapi.ReadDesire{makeRd(t, true), makeRdInvalidContent(nil)},
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, areAllSchedulesPaused(tt.input))
		})
	}
}

func TestIsTerminal(t *testing.T) {
	tests := []struct {
		state coreapi.RecoveryState
		want  bool
	}{
		{coreapi.RecoveryStateCompleted, true},
		{coreapi.RecoveryStateFailed, true},
		{coreapi.RecoveryStatePending, false},
		{coreapi.RecoveryStateRecoveryCRCreated, false},
		{coreapi.RecoveryStateMonitoring, false},
		{coreapi.RecoveryStateRestoring, false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			assert.Equal(t, tt.want, isTerminal(tt.state))
		})
	}
}

func TestProcess(t *testing.T) {
	const (
		processTestRecoveryID = "r1"
		processTestBackupID   = "b1"
		processTestClusterID  = "cluster-abc"
	)

	processMcResourceID := metadataapi.Must(fleetapi.ToManagementClusterResourceID("mc1"))

	newMockCruds := func(t *testing.T) (
		cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire],
		cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire],
	) {
		t.Helper()
		mockKA := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		adCrud, err := mockKA.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
		require.NoError(t, err)
		rdCrud, err := mockKA.ReadDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
		require.NoError(t, err)
		return adCrud, rdCrud
	}

	makeState := func(
		spc *coreapi.ServiceProviderCluster,
		status *coreapi.RecoveryStatus,
		adCrud cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire],
		rdCrud cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire],
	) recoverySyncState {
		return recoverySyncState{
			key:          testKey,
			spc:          spc,
			clusterID:    processTestClusterID,
			mcResourceID: processMcResourceID,
			adCrud:       adCrud,
			rdCrud:       rdCrud,
			recoveryRequestToProcess: &coreapi.RecoveryRequest{
				RecoveryId: processTestRecoveryID,
				BackupId:   processTestBackupID,
			},
			recoveryRequestStatusToProcess: status,
		}
	}

	syncer := &recoverySyncer{}

	tests := []struct {
		name            string
		buildState      func(t *testing.T, ctx context.Context) recoverySyncState
		wantErrContains string
		verify          func(t *testing.T, ctx context.Context, state recoverySyncState)
	}{
		{
			name: "CompletedAt set returns nil immediately",
			buildState: func(t *testing.T, ctx context.Context) recoverySyncState {
				t.Helper()
				adCrud, rdCrud := newMockCruds(t)
				now := metav1.Now()
				return makeState(
					&coreapi.ServiceProviderCluster{},
					&coreapi.RecoveryStatus{RecoveryId: processTestRecoveryID, CompletedAt: &now},
					adCrud, rdCrud,
				)
			},
		},
		{
			name: "StartedAt nil is populated before BackupState is set",
			buildState: func(t *testing.T, ctx context.Context) recoverySyncState {
				t.Helper()
				adCrud, rdCrud := newMockCruds(t)
				mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
				spc, err := corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockDB, testKey.GetResourceID())
				require.NoError(t, err)
				spcCRUD := mockDB.ServiceProviderClusters(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				state := makeState(spc, &coreapi.RecoveryStatus{RecoveryId: processTestRecoveryID}, adCrud, rdCrud)
				state.spcCrud = spcCRUD
				return state
			},
			verify: func(t *testing.T, ctx context.Context, state recoverySyncState) {
				t.Helper()
				assert.NotNil(t, state.recoveryRequestStatusToProcess.StartedAt, "StartedAt should be set")
			},
		},
		{
			name: "BackupState not Paused sets it to Paused and returns nil",
			buildState: func(t *testing.T, ctx context.Context) recoverySyncState {
				t.Helper()
				adCrud, rdCrud := newMockCruds(t)
				mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
				spc, err := corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockDB, testKey.GetResourceID())
				require.NoError(t, err)
				spc.Spec.BackupScheduleState = coreapi.BackupScheduleStateEnabled
				spcCRUD := mockDB.ServiceProviderClusters(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				spc, err = spcCRUD.Replace(ctx, spc, nil)
				require.NoError(t, err)
				started := metav1.NewTime(time.Now())
				state := makeState(spc, &coreapi.RecoveryStatus{RecoveryId: processTestRecoveryID, StartedAt: &started}, adCrud, rdCrud)
				state.spcCrud = spcCRUD
				return state
			},
			verify: func(t *testing.T, ctx context.Context, state recoverySyncState) {
				t.Helper()
				assert.Equal(t, coreapi.BackupScheduleStateDisabled, state.spc.Spec.BackupScheduleState)
			},
		},
		{
			name: "fetchSchedules list error is propagated",
			buildState: func(t *testing.T, ctx context.Context) recoverySyncState {
				t.Helper()
				adCrud, _ := newMockCruds(t)
				started := metav1.NewTime(time.Now())
				return makeState(
					&coreapi.ServiceProviderCluster{Spec: coreapi.ServiceProviderClusterSpec{BackupScheduleState: coreapi.BackupScheduleStateDisabled}},
					&coreapi.RecoveryStatus{RecoveryId: processTestRecoveryID, StartedAt: &started},
					adCrud,
					&stubRdCrud{listErr: fmt.Errorf("cosmos unavailable")},
				)
			},
			wantErrContains: "failed to fetch schedules",
		},
		{
			name: "schedules not all paused returns waiting error",
			buildState: func(t *testing.T, ctx context.Context) recoverySyncState {
				t.Helper()
				adCrud, rdCrud := newMockCruds(t)
				_, err := rdCrud.Create(ctx, makeScheduleRd(t, backup.BackupScheduleDesireNamePrefix+"hourly", false), nil)
				require.NoError(t, err)
				started := metav1.NewTime(time.Now())
				return makeState(
					&coreapi.ServiceProviderCluster{Spec: coreapi.ServiceProviderClusterSpec{BackupScheduleState: coreapi.BackupScheduleStateDisabled}},
					&coreapi.RecoveryStatus{RecoveryId: processTestRecoveryID, StartedAt: &started},
					adCrud, rdCrud,
				)
			},
			wantErrContains: "waiting for all schedules to be paused",
		},
		{
			name: "all schedules paused, apply desire absent: creates it and returns nil",
			buildState: func(t *testing.T, ctx context.Context) recoverySyncState {
				t.Helper()
				adCrud, rdCrud := newMockCruds(t)
				started := metav1.NewTime(time.Now())
				return makeState(
					&coreapi.ServiceProviderCluster{Spec: coreapi.ServiceProviderClusterSpec{BackupScheduleState: coreapi.BackupScheduleStateDisabled}},
					&coreapi.RecoveryStatus{RecoveryId: processTestRecoveryID, StartedAt: &started},
					adCrud, rdCrud,
				)
			},
			verify: func(t *testing.T, ctx context.Context, state recoverySyncState) {
				t.Helper()
				got, err := state.adCrud.Get(ctx, backup.RecoveryDesireNamePrefix+processTestRecoveryID)
				require.NoError(t, err)
				require.NotNil(t, got)
			},
		},
		{
			name: "adCrud.Get non-not-found error is propagated",
			buildState: func(t *testing.T, ctx context.Context) recoverySyncState {
				t.Helper()
				_, rdCrud := newMockCruds(t)
				started := metav1.NewTime(time.Now())
				return makeState(
					&coreapi.ServiceProviderCluster{Spec: coreapi.ServiceProviderClusterSpec{BackupScheduleState: coreapi.BackupScheduleStateDisabled}},
					&coreapi.RecoveryStatus{RecoveryId: processTestRecoveryID, StartedAt: &started},
					&stubAdCrud{getErr: fmt.Errorf("cosmos timeout")},
					rdCrud,
				)
			},
			wantErrContains: "failed to fetch hcprecovery",
		},
		{
			name: "apply desire present, read desire absent: creates it and returns nil",
			buildState: func(t *testing.T, ctx context.Context) recoverySyncState {
				t.Helper()
				adCrud, rdCrud := newMockCruds(t)
				started := metav1.NewTime(time.Now())
				state := makeState(
					&coreapi.ServiceProviderCluster{Spec: coreapi.ServiceProviderClusterSpec{BackupScheduleState: coreapi.BackupScheduleStateDisabled}},
					&coreapi.RecoveryStatus{RecoveryId: processTestRecoveryID, StartedAt: &started},
					adCrud, rdCrud,
				)
				require.NoError(t, createRecoveryApplyDesire(ctx, state))
				return state
			},
			verify: func(t *testing.T, ctx context.Context, state recoverySyncState) {
				t.Helper()
				got, err := state.rdCrud.Get(ctx, backup.RecoveryDesireNamePrefix+processTestRecoveryID)
				require.NoError(t, err)
				require.NotNil(t, got)
			},
		},
		{
			name: "rdCrud.Get non-not-found error is propagated",
			buildState: func(t *testing.T, ctx context.Context) recoverySyncState {
				t.Helper()
				adCrud, _ := newMockCruds(t)
				started := metav1.NewTime(time.Now())
				stubRd := &stubRdCrud{getErr: fmt.Errorf("cosmos timeout")}
				state := makeState(
					&coreapi.ServiceProviderCluster{Spec: coreapi.ServiceProviderClusterSpec{BackupScheduleState: coreapi.BackupScheduleStateDisabled}},
					&coreapi.RecoveryStatus{RecoveryId: processTestRecoveryID, StartedAt: &started},
					adCrud, stubRd,
				)
				require.NoError(t, createRecoveryApplyDesire(ctx, state))
				return state
			},
			wantErrContains: "failed to fetch recovery read desire",
		},
		{
			name: "both desires present returns nil",
			buildState: func(t *testing.T, ctx context.Context) recoverySyncState {
				t.Helper()
				adCrud, rdCrud := newMockCruds(t)
				started := metav1.NewTime(time.Now())
				state := makeState(
					&coreapi.ServiceProviderCluster{Spec: coreapi.ServiceProviderClusterSpec{BackupScheduleState: coreapi.BackupScheduleStateDisabled}},
					&coreapi.RecoveryStatus{RecoveryId: processTestRecoveryID, StartedAt: &started},
					adCrud, rdCrud,
				)
				require.NoError(t, createRecoveryApplyDesire(ctx, state))
				require.NoError(t, createRecoveryReadDesire(ctx, state))
				return state
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			state := tt.buildState(t, ctx)

			err := syncer.process(ctx, state)

			if tt.wantErrContains != "" {
				require.ErrorContains(t, err, tt.wantErrContains)
			} else {
				require.NoError(t, err)
			}
			if tt.verify != nil {
				tt.verify(t, ctx, state)
			}
		})
	}
}

func TestCreateRecoveryApplyDesire(t *testing.T) {
	const (
		recoveryID = "r1"
		backupID   = "b1"
		clusterID  = "cluster-abc"
	)

	mcResourceID := metadataapi.Must(fleetapi.ToManagementClusterResourceID("mc1"))

	newState := func(t *testing.T, ctx context.Context) (recoverySyncState, cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire]) {
		t.Helper()
		mockKA := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		adCrud, err := mockKA.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
		require.NoError(t, err)
		state := recoverySyncState{
			key:          testKey,
			clusterID:    clusterID,
			mcResourceID: mcResourceID,
			adCrud:       adCrud,
			recoveryRequestToProcess: &coreapi.RecoveryRequest{
				RecoveryId: recoveryID,
				BackupId:   backupID,
			},
		}
		return state, adCrud
	}

	t.Run("success creates apply desire with correct content", func(t *testing.T) {
		ctx := context.Background()
		state, adCrud := newState(t, ctx)

		err := createRecoveryApplyDesire(ctx, state)
		require.NoError(t, err)

		got, err := adCrud.Get(ctx, backup.RecoveryDesireNamePrefix+recoveryID)
		require.NoError(t, err)
		require.NotNil(t, got)
		require.NotNil(t, got.Spec.ServerSideApply)
		require.NotNil(t, got.Spec.ServerSideApply.KubeContent)

		// Verify the embedded HCPRecovery has the right cluster and backup IDs.
		var content map[string]any
		require.NoError(t, json.Unmarshal(got.Spec.ServerSideApply.KubeContent.Raw, &content))
		spec, ok := content["spec"].(map[string]any)
		require.True(t, ok, "spec field missing from HCPRecovery")
		assert.Equal(t, clusterID, spec["clusterId"])
		assert.Equal(t, backupID, spec["backupId"])
	})

	t.Run("Create conflict propagates error", func(t *testing.T) {
		ctx := context.Background()
		state, _ := newState(t, ctx)

		// First call seeds the document.
		require.NoError(t, createRecoveryApplyDesire(ctx, state))
		// Second call must fail with a conflict from the mock.
		err := createRecoveryApplyDesire(ctx, state)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create recovery apply desire")
	})
}

func TestCreateRecoveryReadDesire(t *testing.T) {
	const recoveryID = "r1"

	mcResourceID := metadataapi.Must(fleetapi.ToManagementClusterResourceID("mc1"))

	newState := func(t *testing.T, ctx context.Context) (recoverySyncState, cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire]) {
		t.Helper()
		mockKA := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		rdCrud, err := mockKA.ReadDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
		require.NoError(t, err)
		state := recoverySyncState{
			key:          testKey,
			mcResourceID: mcResourceID,
			rdCrud:       rdCrud,
			recoveryRequestToProcess: &coreapi.RecoveryRequest{
				RecoveryId: recoveryID,
			},
		}
		return state, rdCrud
	}

	t.Run("success creates read desire targeting hcprecoveries resource", func(t *testing.T) {
		ctx := context.Background()
		state, rdCrud := newState(t, ctx)

		err := createRecoveryReadDesire(ctx, state)
		require.NoError(t, err)

		got, err := rdCrud.Get(ctx, backup.RecoveryDesireNamePrefix+recoveryID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "hcprecoveries", got.Spec.TargetItem.Resource)
		assert.Equal(t, recoveryID, got.Spec.TargetItem.Name)
		assert.Equal(t, "hcp-recovery", got.Spec.TargetItem.Namespace)
	})

	t.Run("Create conflict propagates error", func(t *testing.T) {
		ctx := context.Background()
		state, _ := newState(t, ctx)

		// First call seeds the document.
		require.NoError(t, createRecoveryReadDesire(ctx, state))
		// Second call must fail with a conflict from the mock.
		err := createRecoveryReadDesire(ctx, state)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create recovery read desire")
	})
}
