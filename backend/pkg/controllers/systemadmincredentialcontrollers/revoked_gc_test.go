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

package systemadmincredentialcontrollers

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testSubscriptionID    = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName = "test-rg"
	testClusterName       = "test-cluster"
)

type alwaysSyncCooldownChecker struct{}

func (c *alwaysSyncCooldownChecker) CanSync(_ context.Context, _ any) bool { return true }

var _ controllerutil.CooldownChecker = (*alwaysSyncCooldownChecker)(nil)

func newTestClusterKey() controllerutils.HCPClusterKey {
	return controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}
}

func newTestCluster() *api.HCPOpenShiftCluster {
	clusterResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))

	return &api.HCPOpenShiftCluster{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   clusterResourceID,
			PartitionKey: strings.ToLower(clusterResourceID.SubscriptionID),
		},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   clusterResourceID,
				Name: testClusterName,
				Type: clusterResourceID.ResourceType.String(),
			},
		},
	}
}

func newTestCredentialForGC(credName string, phase api.SystemAdminCredentialPhase, revokedAt *metav1.Time) *api.SystemAdminCredential {
	credResourceID := api.Must(api.ToSystemAdminCredentialResourceID(testSubscriptionID, testResourceGroupName, testClusterName, credName))
	cred := &api.SystemAdminCredential{}
	cred.SetResourceID(credResourceID)
	cred.SetPartitionKey(strings.ToLower(testSubscriptionID))
	cred.Spec = api.SystemAdminCredentialSpec{
		Username:            "system-admin",
		OperationID:         "test-op",
		ExpirationTimestamp: metav1.NewTime(time.Date(2025, 6, 16, 10, 0, 0, 0, time.UTC)),
		PublicKeyPEM:        "test-public-key",
		PrivateKeyPEM:       "test-private-key",
	}
	cred.Status = api.SystemAdminCredentialStatus{
		Phase:     phase,
		RevokedAt: revokedAt,
	}
	return cred
}

func TestRevokedGCSyncer_SyncOnce(t *testing.T) {
	// Base time is well past the 48h retention
	baseTime := time.Date(2025, 6, 20, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name          string
		clockTime     time.Time
		credentials   []*api.SystemAdminCredential
		expectDeleted []string // credential names that should be deleted
		expectKept    []string // credential names that should remain
		wantErr       bool
	}{
		{
			name:      "deletes revoked credential past 48h retention",
			clockTime: baseTime,
			credentials: func() []*api.SystemAdminCredential {
				revokedAt := metav1.NewTime(baseTime.Add(-49 * time.Hour)) // 49h ago
				return []*api.SystemAdminCredential{
					newTestCredentialForGC("oldrevoked111111", api.SystemAdminCredentialPhaseRevoked, &revokedAt),
				}
			}(),
			expectDeleted: []string{"oldrevoked111111"},
			expectKept:    nil,
		},
		{
			name:      "keeps revoked credential within 48h retention",
			clockTime: baseTime,
			credentials: func() []*api.SystemAdminCredential {
				revokedAt := metav1.NewTime(baseTime.Add(-24 * time.Hour)) // 24h ago
				return []*api.SystemAdminCredential{
					newTestCredentialForGC("recentrevoke1111", api.SystemAdminCredentialPhaseRevoked, &revokedAt),
				}
			}(),
			expectDeleted: nil,
			expectKept:    []string{"recentrevoke1111"},
		},
		{
			name:      "skips non-revoked credentials",
			clockTime: baseTime,
			credentials: []*api.SystemAdminCredential{
				newTestCredentialForGC("requested1111111", api.SystemAdminCredentialPhaseRequested, nil),
				newTestCredentialForGC("issued1111111111", api.SystemAdminCredentialPhaseIssued, nil),
			},
			expectDeleted: nil,
			expectKept:    []string{"requested1111111", "issued1111111111"},
		},
		{
			name:      "skips revoked credential with nil RevokedAt",
			clockTime: baseTime,
			credentials: []*api.SystemAdminCredential{
				newTestCredentialForGC("nilrevokedat1111", api.SystemAdminCredentialPhaseRevoked, nil),
			},
			expectDeleted: nil,
			expectKept:    []string{"nilrevokedat1111"},
		},
		{
			name:      "handles mixed credentials correctly",
			clockTime: baseTime,
			credentials: func() []*api.SystemAdminCredential {
				oldRevokedAt := metav1.NewTime(baseTime.Add(-50 * time.Hour))
				recentRevokedAt := metav1.NewTime(baseTime.Add(-12 * time.Hour))
				return []*api.SystemAdminCredential{
					newTestCredentialForGC("expired111111111", api.SystemAdminCredentialPhaseRevoked, &oldRevokedAt),
					newTestCredentialForGC("recent1111111111", api.SystemAdminCredentialPhaseRevoked, &recentRevokedAt),
					newTestCredentialForGC("active1111111111", api.SystemAdminCredentialPhaseIssued, nil),
				}
			}(),
			expectDeleted: []string{"expired111111111"},
			expectKept:    []string{"recent1111111111", "active1111111111"},
		},
		{
			name:          "handles no credentials",
			clockTime:     baseTime,
			credentials:   nil,
			expectDeleted: nil,
			expectKept:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			resources := []any{newTestCluster()}
			for _, cred := range tt.credentials {
				resources = append(resources, cred)
			}

			mockDB, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			syncer := &revokedGCSyncer{
				cooldownChecker:   &alwaysSyncCooldownChecker{},
				clock:             clocktesting.NewFakePassiveClock(tt.clockTime),
				resourcesDBClient: mockDB,
			}

			key := newTestClusterKey()
			err = syncer.SyncOnce(ctx, key)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			// Verify expected deletions and retentions
			credIter, err := mockDB.SystemAdminCredentials(testSubscriptionID, testResourceGroupName, testClusterName).List(ctx, nil)
			require.NoError(t, err)

			remaining := map[string]bool{}
			for _, cred := range credIter.Items(ctx) {
				remaining[cred.GetResourceID().Name] = true
			}
			require.NoError(t, credIter.GetError())

			for _, deletedName := range tt.expectDeleted {
				assert.False(t, remaining[deletedName], "credential %q should have been deleted", deletedName)
			}

			for _, keptName := range tt.expectKept {
				assert.True(t, remaining[keptName], "credential %q should have been kept", keptName)
			}
		})
	}
}
