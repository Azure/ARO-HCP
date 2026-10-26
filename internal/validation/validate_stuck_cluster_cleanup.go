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

package validation

import (
	"context"
	"fmt"

	apivalidation "k8s.io/apimachinery/pkg/api/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

// ValidateStuckClusterCleanupCreate validates a new StuckClusterCleanup.
func ValidateStuckClusterCleanupCreate(_ context.Context, cleanup *fleetapi.StuckClusterCleanup) field.ErrorList {
	var errs field.ErrorList
	errs = append(errs, validateStuckClusterCleanupSpec(cleanup)...)

	// The UID is learned by observing the namespace, so it cannot be supplied by
	// the caller. Rejecting it at creation keeps an operator from asserting an
	// identity the controller is supposed to establish.
	if len(cleanup.Spec.NamespaceUID) > 0 {
		errs = append(errs, field.Forbidden(
			field.NewPath("spec", "namespaceUID"),
			"namespaceUID is set by the controller on first observation and cannot be supplied at creation",
		))
	}
	return errs
}

// ValidateStuckClusterCleanupUpdate validates an update to a StuckClusterCleanup.
//
// The spec is an operator request: once written it is a record of what was
// asked for, so every field is immutable. The single exception is NamespaceUID,
// which starts empty and is pinned once by the controller — pinning it is the
// whole point of carrying it, and un-pinning or re-pointing it would silently
// retarget the request at a different namespace.
func ValidateStuckClusterCleanupUpdate(_ context.Context, newCleanup, oldCleanup *fleetapi.StuckClusterCleanup) field.ErrorList {
	var errs field.ErrorList
	errs = append(errs, validateStuckClusterCleanupSpec(newCleanup)...)

	if oldCleanup == nil {
		return errs
	}

	specPath := field.NewPath("spec")
	if newCleanup.Spec.Mode != oldCleanup.Spec.Mode {
		errs = append(errs, field.Invalid(specPath.Child("mode"), newCleanup.Spec.Mode, "mode is immutable"))
	}
	if newCleanup.Spec.NamespaceName != oldCleanup.Spec.NamespaceName {
		errs = append(errs, field.Invalid(specPath.Child("namespaceName"), newCleanup.Spec.NamespaceName, "namespaceName is immutable"))
	}
	if newCleanup.Spec.ClusterServiceID != oldCleanup.Spec.ClusterServiceID {
		errs = append(errs, field.Invalid(specPath.Child("clusterServiceID"), newCleanup.Spec.ClusterServiceID, "clusterServiceID is immutable"))
	}
	if newCleanup.Spec.RequestReason != oldCleanup.Spec.RequestReason {
		errs = append(errs, field.Invalid(specPath.Child("requestReason"), newCleanup.Spec.RequestReason, "requestReason is immutable"))
	}

	oldClusterResourceID, newClusterResourceID := "", ""
	if oldCleanup.Spec.ClusterResourceID != nil {
		oldClusterResourceID = oldCleanup.Spec.ClusterResourceID.String()
	}
	if newCleanup.Spec.ClusterResourceID != nil {
		newClusterResourceID = newCleanup.Spec.ClusterResourceID.String()
	}
	if newClusterResourceID != oldClusterResourceID {
		errs = append(errs, field.Invalid(specPath.Child("clusterResourceID"), newClusterResourceID, "clusterResourceID is immutable"))
	}

	if len(oldCleanup.Spec.NamespaceUID) > 0 && newCleanup.Spec.NamespaceUID != oldCleanup.Spec.NamespaceUID {
		errs = append(errs, field.Invalid(
			specPath.Child("namespaceUID"),
			newCleanup.Spec.NamespaceUID,
			"namespaceUID is immutable once the controller has pinned it",
		))
	}

	return errs
}

func validateStuckClusterCleanupSpec(cleanup *fleetapi.StuckClusterCleanup) field.ErrorList {
	var errs field.ErrorList
	specPath := field.NewPath("spec")

	switch cleanup.Spec.Mode {
	case fleetapi.StuckClusterCleanupModeInspect, fleetapi.StuckClusterCleanupModeExecute:
	case "":
		errs = append(errs, field.Required(specPath.Child("mode"), "mode is required"))
	default:
		errs = append(errs, field.NotSupported(
			specPath.Child("mode"),
			cleanup.Spec.Mode,
			[]fleetapi.StuckClusterCleanupMode{fleetapi.StuckClusterCleanupModeInspect, fleetapi.StuckClusterCleanupModeExecute},
		))
	}

	// The namespace must be named exactly. Validating it as a DNS label is what
	// makes "a prefix is never accepted as ownership proof" enforceable here
	// rather than merely documented: a prefix fragment that happens to be a
	// valid label still has to match a real namespace, and the preflight's
	// point reads are what establish that.
	namespacePath := specPath.Child("namespaceName")
	if len(cleanup.Spec.NamespaceName) == 0 {
		errs = append(errs, field.Required(namespacePath, "namespaceName is required"))
	} else {
		for _, msg := range apivalidation.ValidateNamespaceName(cleanup.Spec.NamespaceName, false) {
			errs = append(errs, field.Invalid(namespacePath, cleanup.Spec.NamespaceName, msg))
		}
	}

	if len(cleanup.Spec.RequestReason) == 0 {
		errs = append(errs, field.Required(specPath.Child("requestReason"), "requestReason is required"))
	}

	if cleanup.Spec.ClusterResourceID != nil {
		if !metadataapi.ResourceTypeEqual(cleanup.Spec.ClusterResourceID.ResourceType, coreapi.ClusterResourceType) {
			errs = append(errs, field.Invalid(
				specPath.Child("clusterResourceID"),
				cleanup.Spec.ClusterResourceID.String(),
				fmt.Sprintf("must be an HCP cluster resource ID, got resource type %q", cleanup.Spec.ClusterResourceID.ResourceType.String()),
			))
		}
	}

	return errs
}
