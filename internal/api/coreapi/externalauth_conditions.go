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

import "strings"

// ExternalAuth per-client availability condition Type suffix. The
// ExternalAuthAvailableController writes one condition per declared client
// onto ServiceProviderExternalAuth.Status.Conditions with Type =
// "<ComponentName>Available" (e.g. "ConsoleAvailable",
// "CliAvailable"). The aggregator promotes conditions ending in this suffix
// to ExternalAuth.Status.UserFacingConditions.
const (
	ExternalAuthAvailableConditionSuffix = "Available"
)

// PerClientAvailableConditionType returns the per-client condition type for a
// given component name. For example "console" -> "ConsoleAvailable",
// "cli" -> "CliAvailable".
func PerClientAvailableConditionType(componentName string) string {
	if componentName == "" {
		return ExternalAuthAvailableConditionSuffix
	}
	return strings.ToUpper(componentName[:1]) + componentName[1:] + ExternalAuthAvailableConditionSuffix
}

// ExternalAuth availability condition Reason values used with per-client
// Available conditions.
const (
	// ExternalAuthReasonOIDCConfigAvailable indicates the OIDC client
	// configuration is fully operational.
	ExternalAuthReasonOIDCConfigAvailable = "OIDCConfigAvailable"

	// ExternalAuthConfidentialReasonAwaitingSecret indicates the hosted
	// cluster is waiting for the user to create the client secret in the
	// openshift-config namespace. Only applies to confidential clients.
	ExternalAuthConfidentialReasonAwaitingSecret = "AwaitingSecret"

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

	// HostedClusterOIDCClientSecretGet is the Hypershift Degraded condition
	// reason when the operator cannot find/read the client secret the user
	// must create in the openshift-config namespace.
	HostedClusterOIDCClientSecretGet = "OIDCClientSecretGet"
)
