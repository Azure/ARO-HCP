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

// ExternalAuth internal condition written by the
// ExternalAuthOIDCClientsDegradedController onto
// ServiceProviderExternalAuth.Status.Conditions.
// The ExternalAuthUserFacingConditionsAggregator reads this (and any
// future internal conditions) and produces a single user-facing Degraded
// condition on ExternalAuth.Status.UserFacingConditions.
const (
	// ExternalAuthOIDCClientsDegradedCondition is the condition type set on
	// ServiceProviderExternalAuth representing whether any OIDC client is
	// degraded. True means at least one client has an issue.
	ExternalAuthOIDCClientsDegradedCondition = "OIDCClientsDegraded"
)

// Reasons used with the OIDCClientsDegraded condition on
// ServiceProviderExternalAuth.
const (
	// ExternalAuthOIDCClientsDegradedReasonDegradation indicates at least
	// one OIDC client has an issue.
	ExternalAuthOIDCClientsDegradedReasonDegradation = "OIDCClientDegradation"

	// ExternalAuthOIDCClientsDegradedReasonAsExpected indicates all OIDC
	// clients are operational.
	ExternalAuthOIDCClientsDegradedReasonAsExpected = "AsExpected"
)

// Reasons used with the user-facing Degraded condition on
// ExternalAuth.Status.UserFacingConditions.
const (
	// ExternalAuthUserFacingDegradedReason is the reason when the
	// user-facing Degraded condition is True.
	ExternalAuthUserFacingDegradedReason = "ExternalAuthProvider"

	// ExternalAuthUserFacingDegradedReasonAsExpected is the reason when the
	// user-facing Degraded condition is False (all is well).
	ExternalAuthUserFacingDegradedReasonAsExpected = "AsExpected"
)

// User-facing message constants for OIDC client degradation messages.
// These are the messages surfaced to users in the OIDCClientsDegraded
// condition per component, mapped from Hypershift-reported reasons.
const (
	// ExternalAuthMessageAwaitingSecret is the user-facing message when a
	// confidential client's secret has not yet been created.
	ExternalAuthMessageAwaitingSecret = "Waiting for the client secret to be created in the openshift-config namespace by user"

	// ExternalAuthMessageIssuerURLInvalid is the user-facing message when
	// the issuer URL is invalid.
	ExternalAuthMessageIssuerURLInvalid = "Invalid issuer URL provided by user"

	// ExternalAuthMessageGenericNotWorking is the catch-all user-facing
	// message for any other degradation reason. Internal details are not
	// exposed.
	ExternalAuthMessageGenericNotWorking = "External authentication provider is not working"

	// ExternalAuthMessageAllOperational is the message when all OIDC
	// clients are operational.
	ExternalAuthMessageAllOperational = "All OIDC clients are operational"
)

// HostedCluster OIDCClientStatus condition reasons set by the Hypershift
// operator at runtime. These are matched against when reading the
// HostedCluster ReadDesire cache.
const (
	// HostedClusterOIDCConfigAvailable is the Hypershift Available condition
	// reason when the OIDC client configuration is fully operational.
	HostedClusterOIDCConfigAvailable = "OIDCConfigAvailable"

	// HostedClusterOIDCClientSecretGet is the Hypershift Degraded condition
	// reason when the operator cannot find/read the client secret the user
	// must create in the openshift-config namespace.
	HostedClusterOIDCClientSecretGet = "OIDCClientSecretGet"

	// HostedClusterOIDCIssuerURLInvalid is the Hypershift Degraded condition
	// reason when the issuer URL configured for the external auth provider
	// is invalid.
	HostedClusterOIDCIssuerURLInvalid = "OIDCIssuerURLInvalid"
)
