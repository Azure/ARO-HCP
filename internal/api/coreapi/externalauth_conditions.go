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

package coreapi

// ExternalAuth internal conditions written by the
// ExternalAuthOIDCClientsDegradedController onto
// ServiceProviderExternalAuth.Status.Conditions.
// The ExternalAuthUserFacingDegradedAggregator reads this (and any
// future allowlisted internal conditions) and produces a single user-facing
// Degraded condition on ExternalAuth.Status.UserFacingConditions.
//
// IMPORTANT: Messages written to conditions of this type are surfaced
// verbatim to end users through the ARM API. Never include internal
// implementation details, Hypershift-internal reasons, or cluster
// infrastructure paths in the Message field.
const (
	// ExternalAuthOIDCClientsDegradedCondition is the condition type set on
	// ServiceProviderExternalAuth representing whether any OIDC client is
	// degraded. True means at least one client has an issue.
	//
	// Messages on this condition are user-facing: they are promoted by the
	// ExternalAuthUserFacingDegradedAggregator into
	// ExternalAuth.Status.UserFacingConditions and returned through the ARM
	// API. All messages must be safe for external consumption.
	ExternalAuthOIDCClientsDegradedCondition = "OIDCClientsDegraded"

	// ExternalAuthOIDCIssuerCACertificateInvalidCondition is the condition
	// type set on ServiceProviderExternalAuth when any certificate in the issuer
	// CA bundle has a validity problem — either expired (NotAfter in the past)
	// or not yet valid (NotBefore in the future). A single condition type covers
	// both failure modes; the Message lists the specific certificates and their
	// issue (expired vs not-yet-valid).
	ExternalAuthOIDCIssuerCACertificateInvalidCondition = "OIDCIssuerCACertificateInvalid"
)

// Reasons used with the OIDCClientsDegraded condition on
// ServiceProviderExternalAuth.
const (
	ExternalAuthAvailableCondition = "Available"
)

// ExternalAuth availability condition Reason values used with the
// Available condition.
const (
	// ExternalAuthReasonOIDCConfigAvailable indicates the OIDC client
	// configuration is fully operational.
	ExternalAuthReasonOIDCConfigAvailable = "OIDCConfigAvailable"

	// ExternalAuthReasonAwaitingSecret indicates the hosted cluster is
	// waiting for the user to create the client secret in the
	// openshift-config namespace. Applies when any confidential client
	// is waiting for its secret.
	ExternalAuthReasonAwaitingSecret = "AwaitingSecret"

	// ExternalAuthReasonHostedClusterNotReady indicates the hosted cluster
	// status has not yet been observed or does not report authentication
	// configuration status.
	ExternalAuthReasonHostedClusterNotReady = "HostedClusterNotReady"
)

// HostedCluster OIDCClientStatus condition reasons set by the Hypershift
// operator at runtime. These are matched against when reading the
// HostedCluster ReadDesire cache.
const (
	HostedClusterOIDCConfigAvailable = "OIDCConfigAvailable"

	// ExternalAuthMessageIssuerURLInvalid is the user-facing message when
	// the issuer URL is invalid.
	ExternalAuthMessageIssuerURLInvalid = "Invalid issuer URL provided by user."

	// ExternalAuthMessageGenericNotWorking is the catch-all user-facing
	// message for any other degradation reason. Internal details are not
	// exposed.
	ExternalAuthMessageGenericNotWorking = "External authentication provider is not working."

	// ExternalAuthMessageOIDCClientsAllOperational is the message when all OIDC
	// clients are operational.
	ExternalAuthMessageOIDCClientsAllOperational = "All OIDC clients are operational"

	// ExternalAuthMessageObservabilityUnsupported is the user-facing message
	// when the cluster version does not support External Auth OIDC
	// observability. The supported OCP version are release-5.0, 4.23, 4.22, 4.21, 4.20.41+
	ExternalAuthMessageObservabilityUnsupported = "External Auth observability is not supported on this cluster version. The minimum supported OCP version is 4.20.41."
)

// Reasons used with the ExternalAuthOIDCConfigurationDegradedCondition condition on
// ServiceProviderExternalAuth.
const (
	// ExternalAuthOIDCCACertificateDegradationReason indicates at least
	// one OIDC client has an issue with CA Certificate.
	ExternalAuthOIDCCACertificateDegradationReason = "OIDCCACertificateInvalid"

	// ExternalAuthOIDCCACertificateDegradedReasonAsExpected indicates all OIDC
	// clients have a valid CA Certificate.
	ExternalAuthOIDCCACertificateDegradedReasonAsExpected = "AsExpected"
)

// User-facing message constants for OIDC configuration degraded messages.
const (
	// ExternalAuthMessageOIDCCACertificateValid is the message when all OIDC
	// client have a valid CA Certificate.
	ExternalAuthMessageOIDCCACertificateValid = "All OIDC clients have a valid CA Certificate"
)
