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

package frontend

const (
	ProgramName = "ARO HCP Frontend"

	// APIVersionKey is the request parameter name for the API version.
	APIVersionKey = "api-version"

	// Wildcard path segment names for request multiplexing.
	// Must be lowercase as we lowercase the request URL pattern
	// when registering handlers.
	PathSegmentDeploymentName    = "deploymentname"
	PathSegmentLocation          = "location"
	PathSegmentNodePoolName      = "nodepoolname"
	PathSegmentOperationID       = "operationid"
	PathSegmentResourceGroupName = "resourcegroupname"
	PathSegmentResourceName      = "resourcename"
	PathSegmentSubscriptionID    = "subscriptionid"

	healthGaugeName     = "frontend_health"
	requestCounterName  = "frontend_http_requests_total"
	requestDurationName = "frontend_http_requests_duration_seconds"

	noMatchRouteLabel   = "<no match>"
	unknownVersionLabel = "<unknown>"
)
