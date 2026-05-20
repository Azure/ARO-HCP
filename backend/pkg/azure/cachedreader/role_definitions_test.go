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

package cachedreader

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	utilsclock "k8s.io/utils/clock"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
)

const (
	testRoleDefinitionRID      = "/subscriptions/11111111-1111-1111-1111-111111111111/providers/Microsoft.Authorization/roleDefinitions/b24988ac-6180-42a0-ab88-20f7382dd24c"
	testOtherRoleDefinitionRID = "/subscriptions/11111111-1111-1111-1111-111111111111/providers/Microsoft.Authorization/roleDefinitions/acdd72a7-3385-48ef-bd42-f606fba81ae7"
)

func roleDefByIDResponse(id string) armauthorization.RoleDefinitionsClientGetByIDResponse {
	return armauthorization.RoleDefinitionsClientGetByIDResponse{
		RoleDefinition: armauthorization.RoleDefinition{
			ID: ptr.To(id),
			Properties: &armauthorization.RoleDefinitionProperties{
				RoleName: ptr.To("Reader"),
			},
		},
	}
}

func TestRoleDefinitionsCachedReader_GetCachedByID(t *testing.T) {
	ctx := context.Background()
	innerErr := errors.New("azure unavailable")
	cachedResponse := roleDefByIDResponse(testRoleDefinitionRID)
	initialCached := roleDefByIDResponse(testRoleDefinitionRID)
	refreshedCached := roleDefByIDResponse(testRoleDefinitionRID)
	refreshedCached.Properties.RoleName = ptr.To("Contributor")
	firstCached := roleDefByIDResponse(testRoleDefinitionRID)
	secondCached := roleDefByIDResponse(testOtherRoleDefinitionRID)

	tests := []struct {
		name        string
		setupClient func(ctrl *gomock.Controller) azureclient.RoleDefinitionsClient
		clock       utilsclock.PassiveClock
		// calls: assert.Equal on wantResponse checks payload; gomock Times/InOrder enforces cache behavior.
		calls []struct {
			advanceClockBy   time.Duration
			roleDefinitionID string
			wantResponse     armauthorization.RoleDefinitionsClientGetByIDResponse
			wantError        bool
			wantErrContains  string
		}
	}{
		{
			name: "caches successful GetByID",
			setupClient: func(ctrl *gomock.Controller) azureclient.RoleDefinitionsClient {
				mockClient := azureclient.NewMockRoleDefinitionsClient(ctrl)
				// Times(1): second GetCachedByID must not call Azure again (cache hit).
				mockClient.EXPECT().GetByID(gomock.Any(), testRoleDefinitionRID, nil).Return(cachedResponse, nil).Times(1)
				return mockClient
			},
			clock: utilsclock.RealClock{},
			calls: []struct {
				advanceClockBy   time.Duration
				roleDefinitionID string
				wantResponse     armauthorization.RoleDefinitionsClientGetByIDResponse
				wantError        bool
				wantErrContains  string
			}{
				{
					roleDefinitionID: testRoleDefinitionRID,
					wantResponse:     cachedResponse,
				},
				{
					roleDefinitionID: testRoleDefinitionRID,
					wantResponse:     cachedResponse,
				},
			},
		},
		{
			name: "propagates azure api interaction error when it occurs",
			setupClient: func(ctrl *gomock.Controller) azureclient.RoleDefinitionsClient {
				mockClient := azureclient.NewMockRoleDefinitionsClient(ctrl)
				mockClient.EXPECT().GetByID(gomock.Any(), testRoleDefinitionRID, nil).Return(armauthorization.RoleDefinitionsClientGetByIDResponse{}, innerErr).Times(1)
				return mockClient
			},
			clock: utilsclock.RealClock{},
			calls: []struct {
				advanceClockBy   time.Duration
				roleDefinitionID string
				wantResponse     armauthorization.RoleDefinitionsClientGetByIDResponse
				wantError        bool
				wantErrContains  string
			}{
				{
					roleDefinitionID: testRoleDefinitionRID,
					wantError:        true,
					wantErrContains:  testRoleDefinitionRID,
				},
			},
		},
		{
			name: "error is not cached; next call retries",
			setupClient: func(ctrl *gomock.Controller) azureclient.RoleDefinitionsClient {
				mockClient := azureclient.NewMockRoleDefinitionsClient(ctrl)
				gomock.InOrder(
					mockClient.EXPECT().GetByID(gomock.Any(), testRoleDefinitionRID, nil).Return(armauthorization.RoleDefinitionsClientGetByIDResponse{}, errors.New("temporary")),
					mockClient.EXPECT().GetByID(gomock.Any(), testRoleDefinitionRID, nil).Return(cachedResponse, nil),
				)
				return mockClient
			},
			clock: utilsclock.RealClock{},
			calls: []struct {
				advanceClockBy   time.Duration
				roleDefinitionID string
				wantResponse     armauthorization.RoleDefinitionsClientGetByIDResponse
				wantError        bool
				wantErrContains  string
			}{
				{
					roleDefinitionID: testRoleDefinitionRID,
					wantError:        true,
				},
				{
					roleDefinitionID: testRoleDefinitionRID,
					wantResponse:     cachedResponse,
				},
			},
		},
		{
			name: "refreshes expired cache entry from inner client",
			setupClient: func(ctrl *gomock.Controller) azureclient.RoleDefinitionsClient {
				mockClient := azureclient.NewMockRoleDefinitionsClient(ctrl)
				gomock.InOrder(
					mockClient.EXPECT().GetByID(gomock.Any(), testRoleDefinitionRID, nil).Return(initialCached, nil),
					mockClient.EXPECT().GetByID(gomock.Any(), testRoleDefinitionRID, nil).Return(refreshedCached, nil),
				)
				return mockClient
			},
			clock: clocktesting.NewFakePassiveClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
			calls: []struct {
				advanceClockBy   time.Duration
				roleDefinitionID string
				wantResponse     armauthorization.RoleDefinitionsClientGetByIDResponse
				wantError        bool
				wantErrContains  string
			}{
				{
					roleDefinitionID: testRoleDefinitionRID,
					wantResponse:     initialCached,
				},
				{
					advanceClockBy:   roleDefinitionResourceIDCacheKeyTTL + time.Second,
					roleDefinitionID: testRoleDefinitionRID,
					wantResponse:     refreshedCached,
				},
			},
		},
		{
			name: "returns correct entry when multiple role definitions are cached",
			setupClient: func(ctrl *gomock.Controller) azureclient.RoleDefinitionsClient {
				mockClient := azureclient.NewMockRoleDefinitionsClient(ctrl)
				mockClient.EXPECT().GetByID(gomock.Any(), testRoleDefinitionRID, nil).Return(firstCached, nil).Times(1)
				mockClient.EXPECT().GetByID(gomock.Any(), testOtherRoleDefinitionRID, nil).Return(secondCached, nil).Times(1)
				return mockClient
			},
			clock: utilsclock.RealClock{},
			calls: []struct {
				advanceClockBy   time.Duration
				roleDefinitionID string
				wantResponse     armauthorization.RoleDefinitionsClientGetByIDResponse
				wantError        bool
				wantErrContains  string
			}{
				{
					roleDefinitionID: testRoleDefinitionRID,
					wantResponse:     firstCached,
				},
				{
					roleDefinitionID: testOtherRoleDefinitionRID,
					wantResponse:     secondCached,
				},
				{
					roleDefinitionID: testRoleDefinitionRID,
					wantResponse:     firstCached,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			r := &roleDefinitionsCachedReader{
				inner:                tt.setupClient(ctrl),
				clock:                tt.clock,
				roleDefinitionsCache: make(map[string]cachedGetByIDResponse),
			}

			for _, call := range tt.calls {
				if call.advanceClockBy > 0 {
					fakeClock, ok := tt.clock.(*clocktesting.FakePassiveClock)
					require.True(t, ok, "advanceClockBy requires a FakePassiveClock")
					fakeClock.SetTime(fakeClock.Now().Add(call.advanceClockBy))
				}

				got, err := r.GetCachedByID(ctx, call.roleDefinitionID, nil)
				if call.wantError {
					require.Error(t, err)
					if call.wantErrContains != "" {
						assert.ErrorContains(t, err, call.wantErrContains)
					}
					continue
				}
				require.NoError(t, err)
				assert.Equal(t, call.wantResponse, got)
			}
		})
	}
}
