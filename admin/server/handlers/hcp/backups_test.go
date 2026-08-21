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

package hcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"
	velerov1api "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/backup"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestPatchBackupScheduleHandler(t *testing.T) {
	tests := []struct {
		name               string
		body               io.Reader
		skipResourceID     bool
		seedHCP            bool
		seedSPC            bool
		existingState      coreapi.BackupScheduleState
		expectedStatusCode int
		expectedError      string
		expectedState      coreapi.BackupScheduleState
	}{
		{
			name:               "invalid JSON body",
			body:               strings.NewReader(`{not json`),
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "invalid JSON body",
		},
		{
			name:               "invalid state value",
			body:               strings.NewReader(`{"state":"InvalidState"}`),
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      `invalid state "InvalidState"`,
		},
		{
			name:               "empty state",
			body:               strings.NewReader(`{"state":""}`),
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      `invalid state ""`,
		},
		{
			name:           "missing resource ID in context",
			body:           strings.NewReader(`{"state":"Enabled"}`),
			skipResourceID: true,
			expectedError:  "failed to resolve HCP context",
		},
		{
			name:               "HCP cluster not found",
			body:               strings.NewReader(`{"state":"Enabled"}`),
			expectedStatusCode: http.StatusNotFound,
			expectedError:      "not found",
		},
		{
			name:               "ServiceProviderCluster not found",
			body:               strings.NewReader(`{"state":"Enabled"}`),
			seedHCP:            true,
			expectedStatusCode: http.StatusNotFound,
			expectedError:      "ServiceProviderCluster not found",
		},
		{
			name:               "set Enabled on SPC with no prior state",
			body:               strings.NewReader(`{"state":"Enabled"}`),
			seedHCP:            true,
			seedSPC:            true,
			expectedStatusCode: http.StatusOK,
			expectedState:      coreapi.BackupScheduleStateEnabled,
		},
		{
			name:               "set Disabled on SPC with no prior state",
			body:               strings.NewReader(`{"state":"Disabled"}`),
			seedHCP:            true,
			seedSPC:            true,
			expectedStatusCode: http.StatusOK,
			expectedState:      coreapi.BackupScheduleStateDisabled,
		},
		{
			name:               "change Enabled to Disabled",
			body:               strings.NewReader(`{"state":"Disabled"}`),
			seedHCP:            true,
			seedSPC:            true,
			existingState:      coreapi.BackupScheduleStateEnabled,
			expectedStatusCode: http.StatusOK,
			expectedState:      coreapi.BackupScheduleStateDisabled,
		},
		{
			name:               "change Disabled to Enabled",
			body:               strings.NewReader(`{"state":"Enabled"}`),
			seedHCP:            true,
			seedSPC:            true,
			existingState:      coreapi.BackupScheduleStateDisabled,
			expectedStatusCode: http.StatusOK,
			expectedState:      coreapi.BackupScheduleStateEnabled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()

			resourceID, err := azcorearm.ParseResourceID(coreapitesting.TestClusterResourceID)
			require.NoError(t, err)

			if tt.seedHCP {
				hcp := &coreapi.HCPOpenShiftCluster{
					CosmosMetadata: coreapi.CosmosMetadata{
						ResourceID:   resourceID,
						PartitionKey: strings.ToLower(resourceID.SubscriptionID),
					},
					TrackedResource: coreapi.TrackedResource{
						Resource: coreapi.Resource{ID: resourceID},
					},
				}
				_, err = mockResourcesDBClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Create(ctx, hcp, nil)
				require.NoError(t, err)
			}

			if tt.seedSPC {
				spc, err := corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockResourcesDBClient, resourceID)
				require.NoError(t, err)
				if tt.existingState != "" {
					spc.Spec.BackupScheduleState = tt.existingState
					_, err = mockResourcesDBClient.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Replace(ctx, spc, nil)
					require.NoError(t, err)
				}
			}

			handler := NewHCPPatchBackupScheduleHandler(mockResourcesDBClient)

			if !tt.skipResourceID {
				ctx = utils.ContextWithResourceID(ctx, resourceID)
			}

			req := httptest.NewRequest(http.MethodPatch, "/backupschedules", tt.body)
			req = req.WithContext(ctx)
			recorder := httptest.NewRecorder()

			err = handler.ServeHTTP(recorder, req)

			if tt.expectedStatusCode >= 400 {
				if err == nil {
					t.Fatalf("expected error but got none")
				}
				var cloudErr *coreapi.CloudError
				if !errors.As(err, &cloudErr) {
					t.Fatalf("expected CloudError but got %T: %v", err, err)
				}
				if cloudErr.StatusCode != tt.expectedStatusCode {
					t.Errorf("expected status %d, got %d", tt.expectedStatusCode, cloudErr.StatusCode)
				}
				if tt.expectedError != "" && !strings.Contains(err.Error(), tt.expectedError) {
					t.Errorf("expected error containing %q, got %q", tt.expectedError, err.Error())
				}
				return
			}

			if tt.expectedError != "" {
				if err == nil {
					t.Fatalf("expected error but got none")
				}
				if !strings.Contains(err.Error(), tt.expectedError) {
					t.Errorf("expected error containing %q, got %q", tt.expectedError, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("expected no error but got %v", err)
			}

			var response BackupSchedulePatchResponse
			require.NoError(t, json.NewDecoder(recorder.Body).Decode(&response))
			if response.State != tt.expectedState {
				t.Errorf("expected response state %q, got %q", tt.expectedState, response.State)
			}

			spc, err := mockResourcesDBClient.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			if spc.Spec.BackupScheduleState != tt.expectedState {
				t.Errorf("expected DB state %q, got %q", tt.expectedState, spc.Spec.BackupScheduleState)
			}
		})
	}
}

func TestGetBackupScheduleHandler(t *testing.T) {
	managementClusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + coreapitesting.TestSubscriptionID + "/resourceGroups/mgmt-rg/providers/Microsoft.ContainerService/managedClusters/mgmt-cluster",
	))

	makeReadDesire := func(name string) *kubeapplierapi.ReadDesire {
		resourceIDStr := kubeapplierapi.ToClusterScopedReadDesireResourceIDString(
			coreapitesting.TestSubscriptionID, coreapitesting.TestResourceGroupName, coreapitesting.TestClusterName, name,
		)
		resourceID := metadataapi.Must(azcorearm.ParseResourceID(resourceIDStr))
		return &kubeapplierapi.ReadDesire{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   resourceID,
				PartitionKey: strings.ToLower(managementClusterResourceID.String()),
			},
			Spec: kubeapplierapi.ReadDesireSpec{
				ManagementCluster: managementClusterResourceID,
			},
			Tags: map[string]string{backup.DesireTagKeySchedule: ""},
		}
	}

	seedClusterWithMgmtPlacement := func(
		ctx context.Context,
		t *testing.T,
		mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient,
		resourceID *azcorearm.ResourceID,
		backupState coreapi.BackupScheduleState,
	) {
		t.Helper()
		hcp := &coreapi.HCPOpenShiftCluster{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   resourceID,
				PartitionKey: strings.ToLower(resourceID.SubscriptionID),
			},
			TrackedResource: coreapi.TrackedResource{
				Resource: coreapi.Resource{ID: resourceID},
			},
		}
		_, err := mockResourcesDBClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Create(ctx, hcp, nil)
		require.NoError(t, err)

		spc, err := corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockResourcesDBClient, resourceID)
		require.NoError(t, err)
		spc.Status.ManagementClusterResourceID = managementClusterResourceID
		if backupState != "" {
			spc.Spec.BackupScheduleState = backupState
		}
		_, err = mockResourcesDBClient.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Replace(ctx, spc, nil)
		require.NoError(t, err)
	}

	tests := []struct {
		name               string
		skipResourceID     bool
		seedCluster        bool
		backupState        coreapi.BackupScheduleState
		setMgmtPlacement   bool
		registerKubeClient bool
		readDesires        []*kubeapplierapi.ReadDesire
		expectedStatusCode int
		expectedError      string
		expectedState      coreapi.BackupScheduleState
		expectedSchedules  []BackupScheduleDetail
	}{
		{
			name:           "missing resource ID in context",
			skipResourceID: true,
			expectedError:  "failed to resolve HCP context",
		},
		{
			name:               "HCP cluster not found",
			expectedStatusCode: http.StatusNotFound,
			expectedError:      "not found",
		},
		{
			name:               "management cluster placement not resolved",
			seedCluster:        true,
			expectedStatusCode: http.StatusPreconditionFailed,
			expectedError:      "management cluster placement not resolved",
		},
		{
			name:               "kube-applier client not available",
			seedCluster:        true,
			setMgmtPlacement:   true,
			expectedStatusCode: http.StatusPreconditionFailed,
			expectedError:      "kube-applier client not available",
		},
		{
			name:               "empty state defaults to Enabled",
			seedCluster:        true,
			setMgmtPlacement:   true,
			registerKubeClient: true,
			expectedStatusCode: http.StatusOK,
			expectedState:      coreapi.BackupScheduleStateEnabled,
			expectedSchedules:  []BackupScheduleDetail{},
		},
		{
			name:               "explicit Disabled state preserved",
			seedCluster:        true,
			setMgmtPlacement:   true,
			registerKubeClient: true,
			backupState:        coreapi.BackupScheduleStateDisabled,
			expectedStatusCode: http.StatusOK,
			expectedState:      coreapi.BackupScheduleStateDisabled,
			expectedSchedules:  []BackupScheduleDetail{},
		},
		{
			name:               "returns schedule details from ReadDesires",
			seedCluster:        true,
			setMgmtPlacement:   true,
			registerKubeClient: true,
			readDesires: func() []*kubeapplierapi.ReadDesire {
				lastBackup := metav1.NewTime(time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC))
				scheduleJSON, _ := json.Marshal(velerov1api.Schedule{
					Spec: velerov1api.ScheduleSpec{
						Paused: true,
					},
					Status: velerov1api.ScheduleStatus{
						Phase:      velerov1api.SchedulePhaseEnabled,
						LastBackup: &lastBackup,
					},
				})
				rd := makeReadDesire(backup.BackupScheduleDesireNamePrefix + "hourly")
				rd.Status.KubeContent = &runtime.RawExtension{Raw: scheduleJSON}
				return []*kubeapplierapi.ReadDesire{rd}
			}(),
			expectedStatusCode: http.StatusOK,
			expectedState:      coreapi.BackupScheduleStateEnabled,
			expectedSchedules: []BackupScheduleDetail{
				{
					Name:                 "hourly",
					LastBackupTime:       "2026-08-01T12:00:00Z",
					BackupExecutionState: BackupExecutionStatePaused,
				},
			},
		},
		{
			name:               "ReadDesire without KubeContent returns name only",
			seedCluster:        true,
			setMgmtPlacement:   true,
			registerKubeClient: true,
			readDesires: []*kubeapplierapi.ReadDesire{
				makeReadDesire(backup.BackupScheduleDesireNamePrefix + "daily"),
			},
			expectedStatusCode: http.StatusOK,
			expectedState:      coreapi.BackupScheduleStateEnabled,
			expectedSchedules: []BackupScheduleDetail{
				{Name: "daily"},
			},
		},
		{
			name:               "ReadDesire without schedule tag is skipped",
			seedCluster:        true,
			setMgmtPlacement:   true,
			registerKubeClient: true,
			readDesires: func() []*kubeapplierapi.ReadDesire {
				rd := makeReadDesire(backup.BackupScheduleDesireNamePrefix + "untagged")
				rd.Tags = map[string]string{}
				return []*kubeapplierapi.ReadDesire{rd}
			}(),
			expectedStatusCode: http.StatusOK,
			expectedState:      coreapi.BackupScheduleStateEnabled,
			expectedSchedules:  []BackupScheduleDetail{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
			mockKubeApplierClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()

			resourceID, err := azcorearm.ParseResourceID(coreapitesting.TestClusterResourceID)
			require.NoError(t, err)

			if tt.seedCluster {
				if tt.setMgmtPlacement {
					seedClusterWithMgmtPlacement(ctx, t, mockResourcesDBClient, resourceID, tt.backupState)
				} else {
					hcp := &coreapi.HCPOpenShiftCluster{
						CosmosMetadata: coreapi.CosmosMetadata{
							ResourceID:   resourceID,
							PartitionKey: strings.ToLower(resourceID.SubscriptionID),
						},
						TrackedResource: coreapi.TrackedResource{
							Resource: coreapi.Resource{ID: resourceID},
						},
					}
					_, err = mockResourcesDBClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Create(ctx, hcp, nil)
					require.NoError(t, err)

					_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockResourcesDBClient, resourceID)
					require.NoError(t, err)
				}
			}

			if tt.registerKubeClient {
				mockKubeApplierClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
				mockKubeApplierClients.Register(managementClusterResourceID, mockKubeApplierClient)

				if len(tt.readDesires) > 0 {
					readDesireCRUD, err := mockKubeApplierClient.ReadDesiresForCluster(
						resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name,
					)
					require.NoError(t, err)
					for _, rd := range tt.readDesires {
						_, err = readDesireCRUD.Create(ctx, rd, nil)
						require.NoError(t, err)
					}
				}
			}

			handler := NewHCPGetBackupScheduleHandler(mockResourcesDBClient, mockKubeApplierClients)

			if !tt.skipResourceID {
				ctx = utils.ContextWithResourceID(ctx, resourceID)
			}

			req := httptest.NewRequest(http.MethodGet, "/backupschedules", nil)
			req = req.WithContext(ctx)
			recorder := httptest.NewRecorder()

			err = handler.ServeHTTP(recorder, req)

			if tt.expectedStatusCode >= 400 {
				if err == nil {
					t.Fatalf("expected error but got none")
				}
				var cloudErr *coreapi.CloudError
				if !errors.As(err, &cloudErr) {
					t.Fatalf("expected CloudError but got %T: %v", err, err)
				}
				if cloudErr.StatusCode != tt.expectedStatusCode {
					t.Errorf("expected status %d, got %d", tt.expectedStatusCode, cloudErr.StatusCode)
				}
				if tt.expectedError != "" && !strings.Contains(err.Error(), tt.expectedError) {
					t.Errorf("expected error containing %q, got %q", tt.expectedError, err.Error())
				}
				return
			}

			if tt.expectedError != "" {
				if err == nil {
					t.Fatalf("expected error but got none")
				}
				if !strings.Contains(err.Error(), tt.expectedError) {
					t.Errorf("expected error containing %q, got %q", tt.expectedError, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("expected no error but got %v", err)
			}

			var response BackupScheduleResponse
			require.NoError(t, json.NewDecoder(recorder.Body).Decode(&response))
			if response.State != tt.expectedState {
				t.Errorf("expected state %q, got %q", tt.expectedState, response.State)
			}
			require.Len(t, response.Schedules, len(tt.expectedSchedules))
			for i, expected := range tt.expectedSchedules {
				actual := response.Schedules[i]
				if actual.Name != expected.Name {
					t.Errorf("schedule[%d] name: expected %q, got %q", i, expected.Name, actual.Name)
				}
				if actual.LastBackupTime != expected.LastBackupTime {
					t.Errorf("schedule[%d] lastBackupTime: expected %q, got %q", i, expected.LastBackupTime, actual.LastBackupTime)
				}
				if actual.BackupExecutionState != expected.BackupExecutionState {
					t.Errorf("schedule[%d] state: expected %q, got %q", i, expected.BackupExecutionState, actual.BackupExecutionState)
				}
			}
		})
	}
}
