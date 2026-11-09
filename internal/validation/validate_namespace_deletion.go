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
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
)

// ValidateNamespaceDeletionCreate validates a new NamespaceDeletion.
func ValidateNamespaceDeletionCreate(_ context.Context, request *fleetapi.NamespaceDeletion) field.ErrorList {
	var errs field.ErrorList
	errs = append(errs, validateNamespaceDeletionSpec(request)...)

	// Execution state is learned by the controller and cannot be supplied by a
	// caller creating a request.
	if len(request.Status.NamespaceUID) > 0 {
		errs = append(errs, field.Forbidden(
			field.NewPath("status", "namespaceUID"),
			"namespaceUID is set by the controller on first observation and cannot be supplied at creation",
		))
	}
	if request.Status.HostedClusterDeleteIssuedAt != nil {
		errs = append(errs, field.Forbidden(
			field.NewPath("status", "hostedClusterDeleteIssuedAt"),
			"hostedClusterDeleteIssuedAt is set by the controller after issuing a delete",
		))
	}
	return errs
}

// ValidateNamespaceDeletionUpdate validates an update to a NamespaceDeletion.
//
// The spec is the immutable operator request. Controller-observed execution
// state may be populated once but cannot be changed after it is pinned.
func ValidateNamespaceDeletionUpdate(_ context.Context, newRequest, oldRequest *fleetapi.NamespaceDeletion) field.ErrorList {
	var errs field.ErrorList
	errs = append(errs, validateNamespaceDeletionSpec(newRequest)...)

	if oldRequest == nil {
		return errs
	}

	specPath := field.NewPath("spec")
	if newRequest.Spec.ManagementClusterName != oldRequest.Spec.ManagementClusterName {
		errs = append(errs, field.Invalid(specPath.Child("managementClusterName"), newRequest.Spec.ManagementClusterName, "managementClusterName is immutable"))
	}
	if newRequest.Spec.HostedClusterNamespaceName != oldRequest.Spec.HostedClusterNamespaceName {
		errs = append(errs, field.Invalid(specPath.Child("hostedClusterNamespaceName"), newRequest.Spec.HostedClusterNamespaceName, "hostedClusterNamespaceName is immutable"))
	}
	if newRequest.Spec.HostedClusterName != oldRequest.Spec.HostedClusterName {
		errs = append(errs, field.Invalid(specPath.Child("hostedClusterName"), newRequest.Spec.HostedClusterName, "hostedClusterName is immutable"))
	}
	if newRequest.Spec.RequestReason != oldRequest.Spec.RequestReason {
		errs = append(errs, field.Invalid(specPath.Child("requestReason"), newRequest.Spec.RequestReason, "requestReason is immutable"))
	}

	if len(oldRequest.Status.NamespaceUID) > 0 && newRequest.Status.NamespaceUID != oldRequest.Status.NamespaceUID {
		errs = append(errs, field.Invalid(
			field.NewPath("status", "namespaceUID"),
			newRequest.Status.NamespaceUID,
			"namespaceUID is immutable once the controller has pinned it",
		))
	}
	if issuedAt := oldRequest.Status.HostedClusterDeleteIssuedAt; issuedAt != nil {
		newIssuedAt := newRequest.Status.HostedClusterDeleteIssuedAt
		if newIssuedAt == nil || !newIssuedAt.Time.Equal(issuedAt.Time) {
			errs = append(errs, field.Invalid(
				field.NewPath("status", "hostedClusterDeleteIssuedAt"),
				newIssuedAt,
				"hostedClusterDeleteIssuedAt is immutable once set",
			))
		}
	}

	return errs
}

func validateNamespaceDeletionSpec(request *fleetapi.NamespaceDeletion) field.ErrorList {
	var errs field.ErrorList
	specPath := field.NewPath("spec")
	managementClusterPath := specPath.Child("managementClusterName")
	if len(request.Spec.ManagementClusterName) == 0 {
		errs = append(errs, field.Required(managementClusterPath, "managementClusterName is required"))
	} else if request.Spec.ManagementClusterName != fleetapi.ManagementClusterResourceName {
		errs = append(errs, field.Invalid(managementClusterPath, request.Spec.ManagementClusterName, "only the Fleet management-cluster singleton is supported"))
	}

	// The namespace must be named exactly; a prefix is never accepted. Validating
	// it as a DNS label also enforces the 63-character bound explicitly rather
	// than relying on it, which matters because the namespace name is also the
	// ARM path segment and the Cosmos resource name.
	namespacePath := specPath.Child("hostedClusterNamespaceName")
	if len(request.Spec.HostedClusterNamespaceName) == 0 {
		errs = append(errs, field.Required(namespacePath, "hostedClusterNamespaceName is required"))
	} else {
		for _, msg := range apivalidation.ValidateNamespaceName(request.Spec.HostedClusterNamespaceName, false) {
			errs = append(errs, field.Invalid(namespacePath, request.Spec.HostedClusterNamespaceName, msg))
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
	if len(request.Spec.HostedClusterNamespaceName) > 0 && len(request.Spec.HostedClusterName) > 0 {
		if _, err := fleetapihelpers.RelateHostedClusterNamespace(request.Spec.HostedClusterNamespaceName, request.Spec.HostedClusterName, ""); err != nil {
			errs = append(errs, field.Invalid(namespacePath, request.Spec.HostedClusterNamespaceName, err.Error()))
		}
	}

	if len(request.Spec.RequestReason) == 0 {
		errs = append(errs, field.Required(specPath.Child("requestReason"), "requestReason is required"))
	}

	return errs
}
