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

package denyassignments

import (
	"fmt"
	"slices"
	"time"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type waitingExcludedIdentity struct {
	key       coreapi.DenyAssignmentExcludedIdentityKey
	timestamp time.Time
}

// selectExcludedPrincipalsForPUT returns the principal IDs that should be on
// Azure ExcludePrincipals for this type, and the PendingDeconfigure keys that
// will not be included (wait elapsed or LRU-evicted for the 25-principal cap).
//
// Currently desired principals (PendingConfigure and Configured) always go
// on the list. If that set already exceeds limit, this is an error. Remaining
// slots are filled with PendingDeconfigure principals still inside the 24h
// wait, newest DeconfigureTimestamp first. Older waiters are dropped.
func selectExcludedPrincipalsForPUT(
	status *coreapi.DenyAssignmentStatus,
	now time.Time,
	limit int,
) ([]string, []coreapi.DenyAssignmentExcludedIdentityKey, error) {
	if status == nil {
		return nil, nil, nil
	}

	mustInclude := make([]string, 0, len(status.ExcludedIdentities))
	mustIncludeSeen := map[string]struct{}{}
	waiting := make([]waitingExcludedIdentity, 0, len(status.ExcludedIdentities))
	dropped := make([]coreapi.DenyAssignmentExcludedIdentityKey, 0)

	for key, identityStatus := range status.ExcludedIdentities {
		if identityStatus == nil {
			return nil, nil, utils.TrackError(fmt.Errorf("ExcludedIdentities has a nil status for resource ID %s principal ID %s", key.ResourceID, key.PrincipalID))
		}
		switch identityStatus.Phase {
		case coreapi.DenyAssignmentExcludedIdentityPhasePendingConfigure,
			coreapi.DenyAssignmentExcludedIdentityPhaseConfigured:
			if _, seen := mustIncludeSeen[key.PrincipalID]; seen {
				continue
			}
			mustIncludeSeen[key.PrincipalID] = struct{}{}
			mustInclude = append(mustInclude, key.PrincipalID)
		case coreapi.DenyAssignmentExcludedIdentityPhasePendingDeconfigure:
			if identityStatus.DeconfigureTimestamp == nil || !now.Before(identityStatus.DeconfigureTimestamp.Time.Add(denyAssignmentExcludedIdentityDeconfigureDelay)) {
				dropped = append(dropped, key)
				continue
			}
			waiting = append(waiting, waitingExcludedIdentity{key: key, timestamp: identityStatus.DeconfigureTimestamp.Time})
		}
	}

	if len(mustInclude) > limit {
		return nil, nil, utils.TrackError(fmt.Errorf("desired excluded principals %d exceed Azure ExcludePrincipals limit %d", len(mustInclude), limit))
	}

	slices.SortFunc(waiting, func(a, b waitingExcludedIdentity) int {
		return b.timestamp.Compare(a.timestamp)
	})

	included := append([]string{}, mustInclude...)
	includedSeen := make(map[string]struct{}, limit)
	for principalID := range mustIncludeSeen {
		includedSeen[principalID] = struct{}{}
	}

	slots := limit - len(mustInclude)
	waitingKept := 0
	for _, waiter := range waiting {
		if _, seen := includedSeen[waiter.key.PrincipalID]; seen {
			dropped = append(dropped, waiter.key)
			continue
		}
		if waitingKept >= slots {
			dropped = append(dropped, waiter.key)
			continue
		}
		includedSeen[waiter.key.PrincipalID] = struct{}{}
		included = append(included, waiter.key.PrincipalID)
		waitingKept++
	}

	if len(included) == 0 {
		included = nil
	}
	if len(dropped) == 0 {
		dropped = nil
	}
	return included, dropped, nil
}
