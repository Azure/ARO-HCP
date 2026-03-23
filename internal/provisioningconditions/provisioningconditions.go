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

package provisioningconditions

import (
	"time"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// Seed adds an initial condition entry with a specific timestamp. This is
// used by the backend to record the initial phase entry time from the
// operation's StartTime, since the frontend does not write conditions.
// If a condition with the given state already exists, it is not modified.
func Seed(conditions *[]api.Condition, state arm.ProvisioningState, timestamp time.Time) {
	stateStr := string(state)
	for _, c := range *conditions {
		if c.Type == stateStr {
			return
		}
	}
	*conditions = append(*conditions, api.Condition{
		Type:               stateStr,
		Status:             api.ConditionTrue,
		LastTransitionTime: timestamp,
		Reason:             stateStr,
	})
}

// Set updates the provisioning conditions to reflect a state transition.
// The new state is set to ConditionTrue, and all other states are set to
// ConditionFalse. LastTransitionTime records when the resource entered
// each phase and is preserved when the condition is set to False, so
// duration calculations like provisioned_time - accepted_time use the
// correct entry timestamps.
func Set(conditions *[]api.Condition, state arm.ProvisioningState, now time.Time) {
	stateStr := string(state)

	// Set the new state to True.
	found := false
	for i := range *conditions {
		if (*conditions)[i].Type == stateStr {
			if (*conditions)[i].Status != api.ConditionTrue {
				(*conditions)[i].LastTransitionTime = now
			}
			(*conditions)[i].Status = api.ConditionTrue
			(*conditions)[i].Reason = stateStr
			found = true
			break
		}
	}
	if !found {
		*conditions = append(*conditions, api.Condition{
			Type:               stateStr,
			Status:             api.ConditionTrue,
			LastTransitionTime: now,
			Reason:             stateStr,
		})
	}

	// Set all other existing states to False, preserving their
	// LastTransitionTime so it records "when the resource entered this phase."
	for i := range *conditions {
		if (*conditions)[i].Type != stateStr {
			(*conditions)[i].Status = api.ConditionFalse
		}
	}
}
