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

package operationutils

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
)

func TestCredentialDeadline(t *testing.T) {
	for _, request := range []coreapi.OperationRequest{coreapi.OperationRequestSystemAdminCredentialRequest, coreapi.OperationRequestSystemAdminCredentialRevocation} {
		for _, tc := range []struct {
			name     string
			deadline *time.Time
			expire   bool
		}{
			{name: "legacy nil"},
			{name: "before", deadline: timePointer(time.Unix(101, 0))},
			{name: "boundary", deadline: timePointer(time.Unix(100, 0)), expire: true},
			{name: "after", deadline: timePointer(time.Unix(99, 0)), expire: true},
		} {
			t.Run(string(request)+"/"+tc.name, func(t *testing.T) {
				ctx := context.Background()
				clock := clocktesting.NewFakeClock(time.Unix(100, 0))
				id := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"))
				op := cosmosstorageutils.NewOperation(request, id, metadataapi.InternalID{}, "eastus", "", "", "", nil)
				op.CompletionDeadline = tc.deadline
				db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{op})
				require.NoError(t, err)
				op, err = db.Operations(id.SubscriptionID).Get(ctx, op.OperationID.Name)
				require.NoError(t, err)
				expired, err := ExpireCredentialOperation(ctx, clock, db, op)
				require.NoError(t, err, "expiry must not depend on a cluster document")
				require.Equal(t, tc.expire, expired)
				live, err := db.Operations(id.SubscriptionID).Get(ctx, op.OperationID.Name)
				require.NoError(t, err)
				if tc.expire {
					require.Equal(t, coreapi.ProvisioningStateFailed, live.Status)
					require.Equal(t, "DeadlineExceeded", live.Error.Code)
					expired, err = ExpireCredentialOperation(ctx, clock, db, live)
					require.NoError(t, err)
					require.False(t, expired, "terminal monitoring is immutable")
				} else {
					require.Equal(t, coreapi.ProvisioningStateAccepted, live.Status)
				}
			})
		}
	}
}

func timePointer(t time.Time) *time.Time { return &t }

func TestCredentialCompletionAcknowledgement(t *testing.T) {
	for _, revocation := range []bool{false, true} {
		t.Run(fmt.Sprint(revocation), func(t *testing.T) {
			ctx := t.Context()
			id := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"))
			request, child := coreapi.OperationRequestSystemAdminCredentialRequest, "systemAdminCredentialRequests"
			if revocation {
				request, child = coreapi.OperationRequestSystemAdminCredentialRevocation, "systemAdminCredentialRevocations"
			}
			rid := metadataapi.Must(azcorearm.ParseResourceID(id.String() + "/" + child + "/intent"))
			op := cosmosstorageutils.NewOperation(request, id, metadataapi.InternalID{}, "eastus", "", "", "", nil)
			op.Status = coreapi.ProvisioningStateFailed
			db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{op})
			require.NoError(t, err)
			require.NoError(t, CompleteCredentialIntent(ctx, db, rid, op.OperationID.Name, revocation))
			require.NoError(t, CompleteCredentialIntent(ctx, db, rid, op.OperationID.Name, revocation), "retry after interrupted deletion is idempotent")
			require.ErrorIs(t, LinkCredentialIntent(ctx, db, rid, op.OperationID.Name, revocation), ErrCredentialIntentComplete, "replayed creation must not resume side effects")
			live, err := db.Operations(id.SubscriptionID).Get(ctx, op.OperationID.Name)
			require.NoError(t, err)
			require.Equal(t, coreapi.ProvisioningStateFailed, live.Status)
		})
	}
}
