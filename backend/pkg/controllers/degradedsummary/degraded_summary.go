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

package degradedsummary

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
)

// DegradedControllerCondition represents a single degraded controller and its condition
// for JSON serialization in the summary message.
type DegradedControllerCondition struct {
	ControllerName string        `json:"controllerName"`
	Condition      api.Condition `json:"condition"`
}

// computeDegradedCondition examines all controllers and produces a metav1.Condition
// summarizing the degraded state. It applies inertia so that a controller's degraded
// condition must have persisted for at least the inertia duration before it is included.
//
// When no controllers are degraded (after inertia filtering):
//   - Status: False, Reason: "AsExpected", Message: "AsExpected"
//
// When one or more controllers are degraded:
//   - Status: True
//   - Reason: comma-delimited, alphabetized list of degraded controller names
//   - Message: JSON array of DegradedControllerCondition structs
func computeDegradedCondition(controllers []*api.Controller, inertia *controllerutils.InertiaConfig, now time.Time) metav1.Condition {
	var degradedControllers []DegradedControllerCondition

	for _, controller := range controllers {
		controllerName := controller.GetResourceID().Name
		degradedCondition := controllerutils.GetCondition(controller.Status.Conditions, "Degraded")
		if degradedCondition == nil || degradedCondition.Status != api.ConditionTrue {
			continue
		}

		inertiaDuration := inertia.Inertia(controllerName)
		if now.Sub(degradedCondition.LastTransitionTime) < inertiaDuration {
			continue
		}

		degradedControllers = append(degradedControllers, DegradedControllerCondition{
			ControllerName: controllerName,
			Condition:      *degradedCondition,
		})
	}

	if len(degradedControllers) == 0 {
		return metav1.Condition{
			Type:    "Degraded",
			Status:  metav1.ConditionFalse,
			Reason:  "AsExpected",
			Message: "AsExpected",
		}
	}

	sort.Slice(degradedControllers, func(i, j int) bool {
		return degradedControllers[i].ControllerName < degradedControllers[j].ControllerName
	})

	names := make([]string, 0, len(degradedControllers))
	for _, dc := range degradedControllers {
		names = append(names, dc.ControllerName)
	}

	messageBytes, _ := json.Marshal(degradedControllers)

	return metav1.Condition{
		Type:    "Degraded",
		Status:  metav1.ConditionTrue,
		Reason:  strings.Join(names, ","),
		Message: string(messageBytes),
	}
}
