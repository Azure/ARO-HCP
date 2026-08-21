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

package kubeapplierapi

// Condition types reported on every *Desire's .status.conditions.
const (
	// ConditionTypeSuccessful is true when the controller most-recently observed the
	// desired effect of the *Desire achieved against the kube-apiserver.
	//
	// For an ApplyDesire it is retained for backwards compatibility and mirrors
	// whichever operation-specific condition applies — ConditionTypeSuccessfullyApplied
	// for Type=ServerSideApply, ConditionTypeSuccessfullyDeleted for Type=Delete.
	// Readers should prefer the operation-specific condition (see
	// kubeapplierapihelpers.IsConditionTruePreferring) and fall back to this one
	// only when the operation-specific condition is absent (e.g. a document last
	// written by an older kube-applier). It remains the primary condition for a
	// ReadDesire, which has a single observe operation.
	ConditionTypeSuccessful = "Successful"

	// ConditionTypeSuccessfullyApplied is true when the controller most-recently
	// observed that an ApplyDesire with Type=ServerSideApply achieved its desired
	// effect (the server-side apply succeeded).
	ConditionTypeSuccessfullyApplied = "SuccessfullyApplied"

	// ConditionTypeSuccessfullyDeleted is true when the controller most-recently
	// observed that an ApplyDesire with Type=Delete achieved its desired effect
	// (the target is gone). While finalizers are running it stays False with reason
	// ConditionReasonWaitingForDeletion.
	ConditionTypeSuccessfullyDeleted = "SuccessfullyDeleted"

	// ConditionTypeDegraded reports controller-level health for the *Desire.
	// True means the controller failed in a way unrelated to the kube-apiserver
	// rejecting our request.
	ConditionTypeDegraded = "Degraded"
)

// Condition reasons.
const (
	// ConditionReasonKubeAPIError is set when the kube-apiserver returned an error for our request.
	ConditionReasonKubeAPIError = "KubeAPIError"

	// ConditionReasonPreCheckFailed is set when we could not even issue the kube-apiserver request
	// (e.g. malformed kubeContent, or missing required spec.targetItem fields). An unresolvable GVR
	// is not a pre-check failure: the controller passes the GVR straight to the dynamic client
	// without consulting a RESTMapper, so the kube-apiserver rejects it as a ConditionReasonKubeAPIError.
	ConditionReasonPreCheckFailed = "PreCheckFailed"

	// ConditionReasonWaitingForDeletion is set on an ApplyDesire with Type=Delete when the
	// target item still exists in the cluster, either because finalizers are running or
	// the delete call has just been issued.
	ConditionReasonWaitingForDeletion = "WaitingForDeletion"

	// ConditionReasonNoErrors is the success reason matching the existing controller
	// convention (see backend's controllerutils.ReportSyncError).
	ConditionReasonNoErrors = "NoErrors"

	// ConditionReasonFailed is the failure reason matching the existing controller
	// convention (see backend's controllerutils.ReportSyncError).
	ConditionReasonFailed = "Failed"
)
