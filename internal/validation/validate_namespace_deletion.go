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

	apivalidation "k8s.io/apimachinery/pkg/api/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
)

// ValidateNamespaceDeletionCreate validates a new NamespaceDeletion.
func ValidateNamespaceDeletionCreate(_ context.Context, request *fleetapi.NamespaceDeletion) field.ErrorList {
	var errs field.ErrorList
	errs = append(errs, validateNamespaceDeletionSpec(request)...)

	// The UID is learned by observing the namespace, so it cannot be supplied by
	// the caller. Rejecting it at creation keeps an operator from asserting an
	// identity the controller is supposed to establish.
	if len(request.Spec.NamespaceUID) > 0 {
		errs = append(errs, field.Forbidden(
			field.NewPath("spec", "namespaceUID"),
			"namespaceUID is set by the controller on first observation and cannot be supplied at creation",
		))
	}
	return errs
}

// ValidateNamespaceDeletionUpdate validates an update to a NamespaceDeletion.
//
// The spec is an operator request: once written it is a record of what was
// asked for, so every field is immutable. The single exception is NamespaceUID,
// which starts empty and is pinned once by the controller — pinning it is the
// whole point of carrying it, and un-pinning or re-pointing it would silently
// retarget the request at a different namespace.
//
// DeleteHostedCluster is immutable for a stronger reason than the rest: it is
// an authorization to originate a destructive operation. Allowing it to be
// added to an already-accepted request would mean an attempt could acquire that
// authorization after the fact, which is exactly what recording it per request
// is meant to prevent.
func ValidateNamespaceDeletionUpdate(_ context.Context, newRequest, oldRequest *fleetapi.NamespaceDeletion) field.ErrorList {
	var errs field.ErrorList
	errs = append(errs, validateNamespaceDeletionSpec(newRequest)...)

	if oldRequest == nil {
		return errs
	}

	specPath := field.NewPath("spec")
	if newRequest.Spec.NamespaceName != oldRequest.Spec.NamespaceName {
		errs = append(errs, field.Invalid(specPath.Child("namespaceName"), newRequest.Spec.NamespaceName, "namespaceName is immutable"))
	}
	if newRequest.Spec.HostedClusterName != oldRequest.Spec.HostedClusterName {
		errs = append(errs, field.Invalid(specPath.Child("hostedClusterName"), newRequest.Spec.HostedClusterName, "hostedClusterName is immutable"))
	}
	if newRequest.Spec.DeleteHostedCluster != oldRequest.Spec.DeleteHostedCluster {
		errs = append(errs, field.Invalid(
			specPath.Child("deleteHostedCluster"),
			newRequest.Spec.DeleteHostedCluster,
			"deleteHostedCluster is immutable: an authorization to delete a HostedCluster cannot be added to or removed from an existing request",
		))
	}
	if newRequest.Spec.RequestReason != oldRequest.Spec.RequestReason {
		errs = append(errs, field.Invalid(specPath.Child("requestReason"), newRequest.Spec.RequestReason, "requestReason is immutable"))
	}

	if len(oldRequest.Spec.NamespaceUID) > 0 && newRequest.Spec.NamespaceUID != oldRequest.Spec.NamespaceUID {
		errs = append(errs, field.Invalid(
			specPath.Child("namespaceUID"),
			newRequest.Spec.NamespaceUID,
			"namespaceUID is immutable once the controller has pinned it",
		))
	}

	return errs
}

func validateNamespaceDeletionSpec(request *fleetapi.NamespaceDeletion) field.ErrorList {
	var errs field.ErrorList
	specPath := field.NewPath("spec")

	// The namespace must be named exactly; a prefix is never accepted. Validating
	// it as a DNS label also enforces the 63-character bound explicitly rather
	// than relying on it, which matters because the namespace name is also the
	// ARM path segment and the Cosmos resource name.
	namespacePath := specPath.Child("namespaceName")
	if len(request.Spec.NamespaceName) == 0 {
		errs = append(errs, field.Required(namespacePath, "namespaceName is required"))
	} else {
		for _, msg := range apivalidation.ValidateNamespaceName(request.Spec.NamespaceName, false) {
			errs = append(errs, field.Invalid(namespacePath, request.Spec.NamespaceName, msg))
		}
	}

	// HostedClusterName is required: it derives the control-plane namespace name
	// and confirms the observed HostedCluster is the one the operator means. A
	// mistyped name is the main way a request lands on the wrong namespace.
	hostedClusterPath := specPath.Child("hostedClusterName")
	if len(request.Spec.HostedClusterName) == 0 {
		errs = append(errs, field.Required(hostedClusterPath, "hostedClusterName is required"))
	} else {
		for _, msg := range apivalidation.NameIsDNSLabel(request.Spec.HostedClusterName, false) {
			errs = append(errs, field.Invalid(hostedClusterPath, request.Spec.HostedClusterName, msg))
		}
	}

	if len(request.Spec.RequestReason) == 0 {
		errs = append(errs, field.Required(specPath.Child("requestReason"), "requestReason is required"))
	}

	return errs
}
