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
	"strings"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
)

// reasonMissingDegraded is the synthesized reason used when an item has no
// Degraded condition at all. It shows up in the aggregated parent condition's
// reason via the standard union format (Source_Reason), which is grep-able from
// telemetry.
const reasonMissingDegraded = "MissingDegradedCondition"

// DegradedConditionType is the metav1.Condition.Type our controllers (and the
// kube-applier on its *Desires) write onto each per-item document and that we
// aggregate up onto the parent.
const DegradedConditionType = "Degraded"

// Source-name prefixes for kube-applier desire-sourced Degraded conditions.
// They are prepended to the desire's full lowercased resource ID (see
// CollectDegradedConditions) so each desire source stays attributable to its
// kind in the aggregated Degraded reason/message. Controllers use no prefix and
// are named by the trailing segment of their resource ID.
const (
	ApplyDesireSourcePrefix = "applydesire"
	ReadDesireSourcePrefix  = "readdesire"
)

// ConditionsOfKnown returns the Status.Conditions of any item type that this
// package aggregates (a controller or a kube-applier ApplyDesire/ReadDesire).
// It is the standard conditionsOf argument for CollectDegradedConditions, so
// callers pass ConditionsOfKnown rather than writing per-type lambdas.
//
// It lives here in statusutils (not the api packages) because it deliberately
// spans both coreapi and kubeapplierapi; neither of those imports statusutils,
// so there is no import cycle. An unrecognized type yields nil conditions.
func ConditionsOfKnown(item coreapi.CosmosMetadataAccessor) []metav1.Condition {
	switch v := item.(type) {
	case *coreapi.Controller:
		return v.Status.Conditions
	case *kubeapplierapi.ApplyDesire:
		return v.Status.Conditions
	case *kubeapplierapi.ReadDesire:
		return v.Status.Conditions
	default:
		return nil
	}
}

// CollectDegradedConditions flattens a slice of items (per-controller
// coreapi.Controller documents, or kube-applier ApplyDesire/ReadDesire
// documents) into the SourcedCondition form that UnionCondition consumes,
// applying identical degraded-detection logic to every item type.
//
// T is constrained to coreapi.CosmosMetadataAccessor so the collector can read
// each item's resource ID directly via item.GetResourceID(). conditionsOf
// extracts the item's Status.Conditions; pass ConditionsOfKnown for the item
// types this package aggregates.
//
// Only degraded items are emitted as sources — successful items are omitted so
// they do not clutter the aggregated Degraded message. This changes reporting
// only, not detection: Unknown still counts as bad and a missing condition is
// still synthesized as degraded.
//
// Three shapes are produced per item:
//   - Reports Degraded=True or Unknown: passed through untouched. The
//     condition's own LastTransitionTime drives inertia, and Unknown ends up
//     counted as bad by UnionCondition because it is not the default
//     ConditionFalse. Any prior missing-observation entry is forgotten.
//   - Reports Degraded=False (healthy/successful): NOT emitted as a source, so
//     it never appears in the aggregated message. Its prior missing-observation
//     entry is still forgotten so a later flap starts its inertia fresh.
//   - Has no Degraded condition at all: synthesized as Degraded=True with reason
//     MissingDegradedCondition. The synthesized LastTransitionTime is the
//     first-observed-bad time from the in-memory cache, so a brand-new item that
//     has not reported yet does not immediately flip the aggregate — the same
//     inertia window applies.
//
// The source name (SourcedCondition.ControllerName) is derived from sourcePrefix
// and the resource ID (see degradedSourceName): controllers (empty prefix) are
// named by the trailing resource-ID segment; desires (non-empty prefix) by the
// prefix plus the full lowercased resource ID, which is collision-safe.
//
// Items whose GetResourceID() returns nil are skipped — there is no key to track
// them and no name to attribute them to.
func CollectDegradedConditions[T coreapi.CosmosMetadataAccessor](
	items []T,
	conditionsOf func(coreapi.CosmosMetadataAccessor) []metav1.Condition,
	sourcePrefix string,
	firstObservedBad *FirstObservedBadCache,
) []SourcedCondition {
	out := make([]SourcedCondition, 0, len(items))
	for _, item := range items {
		resourceID := item.GetResourceID()
		if resourceID == nil {
			continue
		}
		ridString := resourceID.String()
		sourceName := degradedSourceName(sourcePrefix, resourceID)

		cond := apimeta.FindStatusCondition(conditionsOf(item), DegradedConditionType)
		if cond != nil {
			// Item has reported a Degraded condition (any status). Drop any prior
			// missing-observation entry so a future "condition disappeared" case
			// starts its inertia fresh.
			firstObservedBad.forget(ridString)
			if cond.Status == metav1.ConditionFalse {
				// Healthy/successful item: not emitted as a source, so it does not
				// appear in the aggregated Degraded message.
				continue
			}
			out = append(out, SourcedCondition{
				ControllerName: sourceName,
				Condition:      *cond,
			})
			continue
		}

		// Missing Degraded -> synthesize Degraded=True so UnionCondition counts it
		// as bad, using the first-observed-bad cache for LastTransitionTime. The
		// message is source-neutral; the source name is prefixed onto it by
		// UnionCondition's aggregated message.
		out = append(out, SourcedCondition{
			ControllerName: sourceName,
			Condition: metav1.Condition{
				Type:               DegradedConditionType,
				Status:             metav1.ConditionTrue,
				Reason:             reasonMissingDegraded,
				Message:            "has not reported a Degraded condition",
				LastTransitionTime: metav1.NewTime(firstObservedBad.observe(ridString)),
			},
		})
	}
	return out
}

// degradedSourceName builds the SourcedCondition.ControllerName for an item.
// With an empty prefix (controllers) it is the trailing name segment of the
// resource ID, matching the controller-name argument writers pass to
// controllerutils.WriteController. With a non-empty prefix (desires) it is the
// prefix immediately followed by the full lowercased resource ID (which begins
// with "/"), keeping the name collision-safe: two desires that share a trailing
// name but live at different resource paths still get distinct source names, and
// desire names never collide with the bare controller names.
func degradedSourceName(sourcePrefix string, resourceID *azcorearm.ResourceID) string {
	if sourcePrefix == "" {
		return resourceID.Name
	}
	return sourcePrefix + strings.ToLower(resourceID.String())
}
