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

// ExternalAuth user-facing condition Type values (metav1.Condition.Type)
// for HCPOpenShiftClusterExternalAuth.Status.UserFacingConditions.
const (
	// ExternalAuthAvailableCondition tracks whether the external auth OIDC
	// configuration is fully operational on the hosted cluster.
	ExternalAuthAvailableCondition = "Available"
)

// ExternalAuth user-facing condition Reason values (metav1.Condition.Reason)
// for ExternalAuthAvailableCondition.
const (
	// ExternalAuthReasonOIDCConfigAvailable indicates the OIDC client
	// configuration is fully operational.
	ExternalAuthReasonOIDCConfigAvailable = "OIDCConfigAvailable"

	// ExternalAuthReasonAwaitingSecret indicates the hosted cluster is
	// waiting for the user to create the client secret in the
	// openshift-config namespace.
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
	HCReasonOIDCConfigAvailable = "OIDCConfigAvailable"

	// HCReasonOIDCClientSecretGet is the Hypershift Degraded condition reason
	// when the operator cannot find/read the client secret the user must
	// create in the openshift-config namespace.
	HCReasonOIDCClientSecretGet = "OIDCClientSecretGet"
)
