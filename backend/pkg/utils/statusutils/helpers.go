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

package statusutils

import (
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// reasonMissingDegraded is the synthesized reason used when a controller
// has no Degraded condition at all. It shows up in the aggregated parent
// condition's reason via the standard union format (Controller_Reason),
// which is grep-able from telemetry.
const reasonMissingDegraded = "MissingDegradedCondition"

// DegradedConditionType is the metav1.Condition.Type our controllers write
// onto their per-controller Controller document and that we aggregate up
// onto the parent.
const DegradedConditionType = "Degraded"

// CollectDegradedConditions flattens an coreapi.Controller slice into the
// SourcedCondition form that UnionCondition consumes. Each entry's
// ControllerName is the trailing name segment of the controller document's
// resource ID — matching the controller-name argument that writers pass
// to controllerutils.WriteController, so it is a stable identifier of the
// producing subsystem.
//
// Two shapes are produced:
//   - Controller reports any Degraded condition (True, False, or Unknown):
//     passed through untouched. The condition's own LastTransitionTime
//     drives inertia, and Unknown ends up counted as bad by UnionCondition
//     because it is not the default ConditionFalse. The aggregator forgets
//     any prior missing-observation entry for this controller.
//   - Controller has no Degraded condition at all: synthesized as
//     Degraded=True with reason MissingDegradedCondition. The synthesized
//     LastTransitionTime is the first-observed-bad time from the in-memory
//     cache, so a brand-new controller that hasn't reported yet does not
//     immediately flip the aggregate — the same inertia window applies.
//
// Controllers with a nil ResourceID are skipped — we have no key to track
// them and no name to attribute them to.
func CollectDegradedConditions(controllers []*coreapi.Controller, firstObservedBad *FirstObservedBadCache) []SourcedCondition {
	out := make([]SourcedCondition, 0, len(controllers))
	for _, ctrl := range controllers {
		if ctrl == nil || ctrl.ResourceID == nil {
			continue
		}
		ridString := ctrl.ResourceID.String()
		controllerName := ctrl.ResourceID.Name

		cond := apimeta.FindStatusCondition(ctrl.Status.Conditions, DegradedConditionType)
		if cond != nil {
			// Controller has reported a Degraded condition (any status). Drop any
			// prior missing-observation entry so a future "condition disappeared"
			// case starts its inertia fresh.
			firstObservedBad.forget(ridString)
			out = append(out, SourcedCondition{
				ControllerName: controllerName,
				Condition:      *cond,
			})
			continue
		}

		// Missing Degraded -> synthesize Degraded=True so UnionCondition counts
		// it as bad, using the first-observed-bad cache for LastTransitionTime.
		out = append(out, SourcedCondition{
			ControllerName: controllerName,
			Condition: metav1.Condition{
				Type:               DegradedConditionType,
				Status:             metav1.ConditionTrue,
				Reason:             reasonMissingDegraded,
				Message:            "Controller has not reported a Degraded condition",
				LastTransitionTime: metav1.NewTime(firstObservedBad.observe(ridString)),
			},
		})
	}
	return out
}

// Source-name prefixes for kube-applier desire-sourced Degraded conditions.
// They namespace a desire's SourcedCondition.ControllerName as
// "<prefix>/<desireName>" so it stays attributable in the aggregated Degraded
// reason/message and can never collide with a controller name (a controller
// name is a bare resource-name segment, which contains no "/").
const (
	ApplyDesireSourcePrefix = "applydesire"
	ReadDesireSourcePrefix  = "readdesire"
)

// CollectDegradedDesireConditions flattens kube-applier *Desires (ApplyDesire
// or ReadDesire) into the SourcedCondition form UnionCondition consumes,
// including ONLY desires that are actually degraded.
//
// This differs from CollectDegradedConditions in two deliberate ways:
//   - A desire is included ONLY when it carries a Degraded condition whose
//     Status is ConditionTrue. Healthy desires (Degraded=False), desires whose
//     Degraded condition is Unknown, and desires that have never reported a
//     Degraded condition all contribute nothing — a successful or
//     not-yet-reported desire must not surface in the cluster's aggregated
//     Degraded message.
//   - Consequently there is no missing-as-degraded synthesis and no
//     first-observed-bad cache: every included condition brings its own
//     LastTransitionTime, so it participates in the inertia window naturally.
//
// The source name is "<sourcePrefix>/<name>", where name is the trailing
// segment of the desire's resource ID. conditionsOf extracts the desire's
// Status.Conditions; it is passed in so this package need not import
// kubeapplierapi and so one generic body serves both desire types.
//
// Desires with a nil ResourceID are skipped — there is no name to attribute
// them to.
func CollectDegradedDesireConditions[T coreapi.CosmosMetadataAccessor](
	sourcePrefix string,
	desires []T,
	conditionsOf func(T) []metav1.Condition,
) []SourcedCondition {
	out := make([]SourcedCondition, 0, len(desires))
	for _, desire := range desires {
		resourceID := desire.GetResourceID()
		if resourceID == nil {
			continue
		}
		cond := apimeta.FindStatusCondition(conditionsOf(desire), DegradedConditionType)
		if cond == nil || cond.Status != metav1.ConditionTrue {
			// Only actually-degraded desires are reported; skip healthy
			// (False/Unknown) and never-reported desires.
			continue
		}
		out = append(out, SourcedCondition{
			ControllerName: sourcePrefix + "/" + resourceID.Name,
			Condition:      *cond,
		})
	}
	return out
}
