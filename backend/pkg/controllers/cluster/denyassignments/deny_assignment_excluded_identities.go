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

func excludedIdentityWaitElapsed(status *coreapi.DenyAssignmentExcludedIdentityStatus, now time.Time) bool {
	if status == nil || status.DeconfigureTimestamp == nil {
		return false
	}
	return !now.Before(status.DeconfigureTimestamp.Time.Add(denyAssignmentExcludedIdentityDeconfigureDelay))
}

// selectExcludedPrincipalsForPUT returns the principal IDs that should be on
// Azure ExcludePrincipals for this type, and the cooldown keys that will not
// be included (wait elapsed or LRU-evicted for the 25-principal cap).
//
// Live desired principals always go on the list, as do observed rows that
// have no DeconfigureTimestamp (still on Azure; Intent has not started a
// cooldown). If that set already exceeds limit, this is an error. Remaining
// slots are filled with cooldown principals still inside the 24h wait,
// newest DeconfigureTimestamp first. Older waiters are dropped.
func selectExcludedPrincipalsForPUT(
	status *coreapi.DenyAssignmentStatus,
	desiredIdentities map[coreapi.DenyAssignmentExcludedIdentityKey]*coreapi.DenyAssignmentExcludedObservedIdentity,
	now time.Time,
	limit int,
) ([]string, []coreapi.DenyAssignmentExcludedIdentityKey, error) {
	if status == nil && len(desiredIdentities) == 0 {
		return nil, nil, nil
	}

	mustInclude := make([]string, 0, len(desiredIdentities))
	mustIncludeSeen := map[string]struct{}{}
	waiting := make([]waitingExcludedIdentity, 0)
	dropped := make([]coreapi.DenyAssignmentExcludedIdentityKey, 0)

	addMustInclude := func(principalID string) {
		if _, seen := mustIncludeSeen[principalID]; seen {
			return
		}
		mustIncludeSeen[principalID] = struct{}{}
		mustInclude = append(mustInclude, principalID)
	}

	for key := range desiredIdentities {
		addMustInclude(key.PrincipalID)
	}

	if status != nil {
		for key, identityStatus := range status.ExcludedIdentities {
			if identityStatus == nil {
				return nil, nil, utils.TrackError(fmt.Errorf("ExcludedIdentities has a nil status for resource ID %s principal ID %s", key.ResourceID, key.PrincipalID))
			}
			if _, desired := desiredIdentities[key]; desired {
				continue
			}
			if identityStatus.DeconfigureTimestamp == nil {
				addMustInclude(key.PrincipalID)
				continue
			}
			if excludedIdentityWaitElapsed(identityStatus, now) {
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

// syncObservedExcludedIdentities updates ExcludedIdentities after a successful
// Azure PUT. Included desired principals get ObservedIdentity from the live
// desired snapshot. Wait-elapsed or LRU-dropped keys are deleted.
func syncObservedExcludedIdentities(
	status *coreapi.DenyAssignmentStatus,
	desiredIdentities map[coreapi.DenyAssignmentExcludedIdentityKey]*coreapi.DenyAssignmentExcludedObservedIdentity,
	droppedKeys map[coreapi.DenyAssignmentExcludedIdentityKey]struct{},
) {
	if status == nil {
		return
	}

	next := make(map[coreapi.DenyAssignmentExcludedIdentityKey]*coreapi.DenyAssignmentExcludedIdentityStatus, len(status.ExcludedIdentities)+len(desiredIdentities))
	for key, identityStatus := range status.ExcludedIdentities {
		if identityStatus == nil {
			continue
		}
		if _, dropped := droppedKeys[key]; dropped {
			continue
		}
		next[key] = identityStatus
	}

	for key, observed := range desiredIdentities {
		if _, dropped := droppedKeys[key]; dropped {
			continue
		}
		existing := next[key]
		if existing == nil {
			next[key] = &coreapi.DenyAssignmentExcludedIdentityStatus{
				ObservedIdentity: observed.DeepCopy(),
			}
			continue
		}
		existing.DeconfigureTimestamp = nil
		existing.ObservedIdentity = observed.DeepCopy()
	}

	if len(next) == 0 {
		status.ExcludedIdentities = nil
		return
	}
	status.ExcludedIdentities = next
}
