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

package api

import (
	"time"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// SeedProvisioningCondition adds an initial condition entry with a specific
// timestamp. This is used by the backend to record the initial phase entry
// time from the operation's StartTime, since the frontend does not write
// conditions.
func SeedProvisioningCondition(conditions *[]Condition, state arm.ProvisioningState, timestamp time.Time) {
	stateStr := string(state)
	for _, c := range *conditions {
		if c.Type == stateStr {
			return
		}
	}
	*conditions = append(*conditions, Condition{
		Type:               stateStr,
		Status:             ConditionTrue,
		LastTransitionTime: timestamp,
		Reason:             stateStr,
	})
}

// SetProvisioningCondition updates the provisioning conditions to reflect a
// state transition. The new state is set to ConditionTrue, and all other
// states are set to ConditionFalse. LastTransitionTime records when the
// resource entered each phase and is preserved when the condition is set
// to False, so duration calculations like provisioned_time - accepted_time
// use the correct entry timestamps.
func SetProvisioningCondition(conditions *[]Condition, state arm.ProvisioningState) {
	now := time.Now().UTC()
	stateStr := string(state)

	// Set the new state to True.
	found := false
	for i := range *conditions {
		if (*conditions)[i].Type == stateStr {
			if (*conditions)[i].Status != ConditionTrue {
				(*conditions)[i].LastTransitionTime = now
			}
			(*conditions)[i].Status = ConditionTrue
			(*conditions)[i].Reason = stateStr
			found = true
			break
		}
	}
	if !found {
		*conditions = append(*conditions, Condition{
			Type:               stateStr,
			Status:             ConditionTrue,
			LastTransitionTime: now,
			Reason:             stateStr,
		})
	}

	// Set all other existing states to False, preserving their
	// LastTransitionTime so it records "when the resource entered this phase."
	for i := range *conditions {
		if (*conditions)[i].Type != stateStr {
			(*conditions)[i].Status = ConditionFalse
		}
	}
}
