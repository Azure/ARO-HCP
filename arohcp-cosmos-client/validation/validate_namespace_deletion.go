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
	"reflect"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	apivalidation "k8s.io/apimachinery/pkg/api/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/fleetapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/apihelpers/fleetapihelpers"
)

// ValidateNamespaceDeletionCreate validates a new NamespaceDeletion.
func ValidateNamespaceDeletionCreate(_ context.Context, request *fleetapi.NamespaceDeletion) field.ErrorList {
	var errs field.ErrorList
	errs = append(errs, validateNamespaceDeletionSpec(request)...)
	if request.Spec.RetryGeneration != 0 || len(request.Spec.RetryReason) != 0 {
		errs = append(errs, field.Forbidden(field.NewPath("spec", "retryGeneration"), "retry is available only for an existing stopped request"))
	}
	if request.Status.AttemptGeneration != 0 || len(request.Status.AttemptReason) != 0 || len(request.Status.PreviousAttempts) != 0 {
		errs = append(errs, field.Forbidden(field.NewPath("status", "attemptGeneration"), "attempt state is controller-owned"))
	}

	// Execution state is learned by the controller and cannot be supplied by a
	// caller creating a request.
	if len(request.Status.NamespaceUID) > 0 {
		errs = append(errs, field.Forbidden(
			field.NewPath("status", "namespaceUID"),
			"namespaceUID is set by the controller on first observation and cannot be supplied at creation",
		))
	}
	for _, value := range []struct{ name, data string }{
		{"hostedClusterUID", request.Status.HostedClusterUID},
		{"controlPlaneNamespaceUID", request.Status.ControlPlaneNamespaceUID},
		{"cleanupIntent", request.Status.CleanupIntent},
	} {
		if len(value.data) != 0 {
			errs = append(errs, field.Forbidden(field.NewPath("status", value.name), "set by the controller"))
		}
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
	if newRequest.Spec.RetryGeneration != oldRequest.Spec.RetryGeneration {
		if newRequest.Spec.RetryGeneration != oldRequest.Spec.RetryGeneration+1 ||
			newRequest.Spec.RetryGeneration > fleetapi.MaxNamespaceDeletionRetries ||
			oldRequest.Spec.RetryGeneration != oldRequest.Status.AttemptGeneration ||
			!apimeta.IsStatusConditionTrue(oldRequest.Status.Conditions, fleetapi.ConditionTypeNamespaceDeletionStopped) ||
			apimeta.IsStatusConditionTrue(oldRequest.Status.Conditions, fleetapi.ConditionTypeNamespaceDeletionComplete) ||
			oldRequest.Status.CompletedAt == nil || len(newRequest.Spec.RetryReason) == 0 ||
			newRequest.Status.AttemptGeneration != oldRequest.Status.AttemptGeneration {
			errs = append(errs, field.Invalid(specPath.Child("retryGeneration"), newRequest.Spec.RetryGeneration, "retry must advance one stopped, settled attempt with a reason"))
		}
	} else if newRequest.Spec.RetryReason != oldRequest.Spec.RetryReason {
		errs = append(errs, field.Invalid(specPath.Child("retryReason"), newRequest.Spec.RetryReason, "retry details cannot change without a new generation"))
	}
	if newRequest.Status.AttemptGeneration != oldRequest.Status.AttemptGeneration {
		if newRequest.Status.AttemptGeneration != oldRequest.Status.AttemptGeneration+1 ||
			newRequest.Status.AttemptGeneration != newRequest.Spec.RetryGeneration ||
			!apimeta.IsStatusConditionTrue(oldRequest.Status.Conditions, fleetapi.ConditionTypeNamespaceDeletionStopped) ||
			oldRequest.Status.CompletedAt == nil || newRequest.Status.CompletedAt != nil || len(newRequest.Status.Conditions) != 0 ||
			len(newRequest.Status.CleanupIntent) != 0 ||
			newRequest.Status.AttemptReason != newRequest.Spec.RetryReason ||
			!validNamespaceDeletionAttemptArchive(newRequest, oldRequest) {
			errs = append(errs, field.Invalid(field.NewPath("status", "attemptGeneration"), newRequest.Status.AttemptGeneration, "new attempt must archive and reset the settled attempt"))
		}
	} else if newRequest.Status.AttemptReason != oldRequest.Status.AttemptReason ||
		!reflect.DeepEqual(newRequest.Status.PreviousAttempts, oldRequest.Status.PreviousAttempts) {
		errs = append(errs, field.Forbidden(field.NewPath("status", "previousAttempts"), "attempt history cannot change without a new attempt"))
	}

	if len(oldRequest.Status.NamespaceUID) > 0 && newRequest.Status.NamespaceUID != oldRequest.Status.NamespaceUID {
		errs = append(errs, field.Invalid(
			field.NewPath("status", "namespaceUID"),
			newRequest.Status.NamespaceUID,
			"namespaceUID is immutable once the controller has pinned it",
		))
	}
	for _, value := range []struct{ name, old, new string }{
		{"hostedClusterUID", oldRequest.Status.HostedClusterUID, newRequest.Status.HostedClusterUID},
		{"controlPlaneNamespaceUID", oldRequest.Status.ControlPlaneNamespaceUID, newRequest.Status.ControlPlaneNamespaceUID},
	} {
		if len(value.old) != 0 && value.new != value.old {
			errs = append(errs, field.Invalid(field.NewPath("status", value.name), value.new, "UID is immutable once pinned"))
		}
	}
	if newRequest.Status.CleanupIntent != oldRequest.Status.CleanupIntent && newRequest.Status.AttemptGeneration == oldRequest.Status.AttemptGeneration {
		validNextIntent := map[string]string{
			"":                              "UnblockHostedCluster",
			"UnblockHostedCluster":          "FinalizeControlPlaneNamespace",
			"FinalizeNamespace":             "FinalizeControlPlaneNamespace",
			"FinalizeControlPlaneNamespace": "FinalizeHostedClusterNamespace",
		}
		if next, ok := validNextIntent[oldRequest.Status.CleanupIntent]; !ok || next != newRequest.Status.CleanupIntent {
			errs = append(errs, field.Invalid(field.NewPath("status", "cleanupIntent"), newRequest.Status.CleanupIntent, "cleanup intent may only advance through UnblockHostedCluster, FinalizeControlPlaneNamespace, and FinalizeHostedClusterNamespace"))
		}
	}

	return errs
}

func validNamespaceDeletionAttemptArchive(newRequest, oldRequest *fleetapi.NamespaceDeletion) bool {
	previous := oldRequest.Status.PreviousAttempts
	if len(newRequest.Status.PreviousAttempts) != len(previous)+1 ||
		(len(previous) > 0 && !reflect.DeepEqual(newRequest.Status.PreviousAttempts[:len(previous)], previous)) {
		return false
	}
	stopped := apimeta.FindStatusCondition(oldRequest.Status.Conditions, fleetapi.ConditionTypeNamespaceDeletionStopped)
	if stopped == nil || oldRequest.Status.CompletedAt == nil {
		return false
	}
	reason := oldRequest.Status.AttemptReason
	if oldRequest.Status.AttemptGeneration == 0 {
		reason = oldRequest.Spec.RequestReason
	}
	want := fleetapi.NamespaceDeletionAttempt{
		Generation:  oldRequest.Status.AttemptGeneration,
		Reason:      reason,
		StoppedAt:   *oldRequest.Status.CompletedAt,
		StopReason:  stopped.Reason,
		StopMessage: stopped.Message,
	}
	return reflect.DeepEqual(newRequest.Status.PreviousAttempts[len(previous)], want)
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
