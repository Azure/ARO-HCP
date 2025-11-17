// Copyright 2025 Microsoft Corporation
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

package controller

import (
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sessiongatev1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
)

// Condition types for Session resources
const (
	// ConditionTypeReady indicates the overall operational state of the session
	ConditionTypeReady = "Ready"
	// ConditionTypeProgressing indicates active reconciliation
	ConditionTypeProgressing = "Progressing"
	// ConditionTypeDegraded indicates permanent configuration errors
	ConditionTypeDegraded = "Degraded"
	// ConditionTypeAvailable indicates endpoint accessibility
	ConditionTypeAvailable = "Available"
	// ConditionTypeCredentials indicates the status of credential provisioning
	ConditionTypeCredentials = "Credentials"
)

// Reasons for Ready condition
const (
	ReasonSessionReady         = "SessionReady"
	ReasonCredentialsMissing   = "CredentialsMissing"
	ReasonAuthorizationMissing = "AuthorizationMissing"
	ReasonEndpointNotPublished = "EndpointNotPublished"
	ReasonConfigurationInvalid = "ConfigurationInvalid"
	ReasonExpired              = "Expired"
)

// Reasons for Progressing condition
const (
	ReasonInitializing             = "Initializing"
	ReasonMintingCredentials       = "MintingCredentials"
	ReasonConfiguringAuthorization = "ConfiguringAuthorization"
	ReasonPublishingEndpoint       = "PublishingEndpoint"
	ReasonReconcileComplete        = "ReconcileComplete"
	ReasonPermanentError           = "PermanentError"
)

// Reasons for Degraded condition
const (
	ReasonInvalidConfiguration = "InvalidConfiguration"
	ReasonPermissionDenied     = "PermissionDenied"
	ReasonTimeout              = "Timeout"
)

// Reasons for Available condition
const (
	ReasonEndpointPublished       = "EndpointPublished"
	ReasonAwaitingCredentials     = "AwaitingCredentials"
	ReasonAwaitingAuthorization   = "AwaitingAuthorization"
	ReasonAwaitingRegistration    = "AwaitingRegistration"
	ReasonCredentialMintingFailed = "CredentialMintingFailed"
	ReasonAuthorizationFailed     = "AuthorizationFailed"
	ReasonRegistrationFailed      = "RegistrationFailed"
)

// Reasons for Credentials condition
const (
	// ReasonPrivateKeyCreated indicates the private key was generated and stored
	ReasonPrivateKeyCreated = "PrivateKeyCreated"
	// ReasonCertificatePending indicates the CSR was submitted and is awaiting approval
	ReasonCertificatePending = "CertificatePending"
	// ReasonCredentialsReady indicates both private key and certificate are ready
	ReasonCredentialsReady = "CredentialsReady"
	// ReasonCredentialsFailed indicates credential provisioning failed
	ReasonCredentialsFailed = "CredentialsFailed"
)

// setCondition sets a condition on a session using the standard helper.
// Returns true if the condition was changed.
func setCondition(session *sessiongatev1alpha1.Session, conditionType string, status metav1.ConditionStatus, reason, message string) bool {
	return meta.SetStatusCondition(&session.Status.Conditions, metav1.Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: session.Generation,
	})
}

// initializeConditions sets initial Unknown status for all conditions on first reconciliation.
// This function is defensive and initializes each condition type individually if not present,
// allowing it to handle partial state gracefully.
func initializeConditions(session *sessiongatev1alpha1.Session) {
	// Initialize each condition type if not present
	if meta.FindStatusCondition(session.Status.Conditions, ConditionTypeReady) == nil {
		setCondition(session, ConditionTypeReady, metav1.ConditionUnknown, ReasonInitializing, "Starting initial reconciliation")
	}
	if meta.FindStatusCondition(session.Status.Conditions, ConditionTypeProgressing) == nil {
		setCondition(session, ConditionTypeProgressing, metav1.ConditionTrue, ReasonInitializing, "Starting initial reconciliation")
	}
	if meta.FindStatusCondition(session.Status.Conditions, ConditionTypeDegraded) == nil {
		setCondition(session, ConditionTypeDegraded, metav1.ConditionFalse, "NoErrors", "No configuration errors detected")
	}
	if meta.FindStatusCondition(session.Status.Conditions, ConditionTypeAvailable) == nil {
		setCondition(session, ConditionTypeAvailable, metav1.ConditionUnknown, ReasonAwaitingCredentials, "Waiting for credentials to be minted")
	}
}

// setDegradedCondition marks the session as degraded due to permanent configuration errors.
// Use this for issues that require user intervention to fix (e.g., invalid spec fields).
// For transient errors that the controller can retry, use setTransientError instead.
func setDegradedCondition(session *sessiongatev1alpha1.Session, reason, message string) {
	setCondition(session, ConditionTypeDegraded, metav1.ConditionTrue, reason, message)
	setCondition(session, ConditionTypeProgressing, metav1.ConditionFalse, ReasonPermanentError, "Stopped due to invalid configuration")
	setCondition(session, ConditionTypeReady, metav1.ConditionFalse, ReasonConfigurationInvalid, message)
	setCondition(session, ConditionTypeAvailable, metav1.ConditionFalse, reason, message)
}

// setTransientError marks a transient error that can be retried.
// Unlike setDegradedCondition, this does NOT set Degraded=True since the error is retryable.
// The controller will retry via backoff when syncHandler returns an error.
// Use this for temporary failures like network issues, API unavailability, etc.
func setTransientError(session *sessiongatev1alpha1.Session, affectedCondition, reason, message string) {
	setCondition(session, affectedCondition, metav1.ConditionFalse, reason, message)
	setCondition(session, ConditionTypeReady, metav1.ConditionFalse, reason, message)
	// Don't touch Degraded - it's reserved for permanent configuration errors
	// Don't touch Progressing - it should remain True to indicate ongoing reconciliation
}

// clearDegradedCondition marks the session as not degraded after successful validation.
// This should be called after validation passes to ensure Degraded=False.
func clearDegradedCondition(session *sessiongatev1alpha1.Session) {
	setCondition(session, ConditionTypeDegraded, metav1.ConditionFalse, "NoErrors", "No configuration errors detected")
}

// setProgressingCondition updates the Progressing condition with current activity
func setProgressingCondition(session *sessiongatev1alpha1.Session, reason, message string) {
	setCondition(session, ConditionTypeProgressing, metav1.ConditionTrue, reason, message)
}

// setAvailableCondition marks endpoint availability status
func setAvailableCondition(session *sessiongatev1alpha1.Session, available bool, reason, message string) {
	status := metav1.ConditionFalse
	if available {
		status = metav1.ConditionTrue
	}
	setCondition(session, ConditionTypeAvailable, status, reason, message)
}

// setReadyCondition marks the session as fully operational
func setReadyCondition(session *sessiongatev1alpha1.Session, ready bool, reason, message string) {
	status := metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	setCondition(session, ConditionTypeReady, status, reason, message)

	if ready {
		// When ready, progressing is complete
		setCondition(session, ConditionTypeProgressing, metav1.ConditionFalse, ReasonReconcileComplete, "Reconciliation completed successfully")
	}
}

// setExpiredCondition marks the session as expired
func setExpiredCondition(session *sessiongatev1alpha1.Session) {
	setCondition(session, ConditionTypeReady, metav1.ConditionFalse, ReasonExpired, "Session has expired")
	setCondition(session, ConditionTypeProgressing, metav1.ConditionFalse, ReasonReconcileComplete, "Session expired")
	setCondition(session, ConditionTypeAvailable, metav1.ConditionFalse, ReasonExpired, "Session has expired")
}

// setCredentialsCondition updates the Credentials condition
func setCredentialsCondition(session *sessiongatev1alpha1.Session, ready bool, reason, message string) {
	status := metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	setCondition(session, ConditionTypeCredentials, status, reason, message)
}

// areCredentialsReady checks if the Credentials condition is True
func areCredentialsReady(session *sessiongatev1alpha1.Session) bool {
	cond := meta.FindStatusCondition(session.Status.Conditions, ConditionTypeCredentials)
	return cond != nil && cond.Status == metav1.ConditionTrue
}
