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

// ProvisioningCondition extends the Kubernetes Condition type with a
// CorrelationRequestID to track which API request triggered the state
// transition.
type ProvisioningCondition struct {
	Condition
	// CorrelationRequestID is the optional ARM correlation request ID
	// associated with the state transition, if provided by the caller.
	CorrelationRequestID string `json:"correlationRequestId,omitempty"`
}

// SetProvisioningCondition updates the provisioning conditions to reflect a
// state transition. The new state is set to ConditionTrue, and all other
// states are set to ConditionFalse. LastTransitionTime records when the
// resource entered each phase and is preserved when the condition is set
// to False, so duration calculations like provisioned_time - accepted_time
// use the correct entry timestamps. The correlationRequestID, if provided
// by the caller, is stored for correlation with the originating API request.
func SetProvisioningCondition(conditions *[]ProvisioningCondition, state arm.ProvisioningState, correlationRequestID string) {
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
			(*conditions)[i].CorrelationRequestID = correlationRequestID
			found = true
			break
		}
	}
	if !found {
		*conditions = append(*conditions, ProvisioningCondition{
			Condition: Condition{
				Type:               stateStr,
				Status:             ConditionTrue,
				LastTransitionTime: now,
				Reason:             stateStr,
			},
			CorrelationRequestID: correlationRequestID,
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
