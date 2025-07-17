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

	errLocationFailedBuildingClusterServiceCluster  = "failed building cluster service cluster"
	errLocationFailedBuildingClusterServiceNodePool = "failed building cluster service node pool"
	errLocationFailedCallingClusterService          = "failed during delegation"
	errLocationFailedCancelingActiveOperations      = "failed during active operation cancellation"
	errLocationFailedCreatingClusterServiceID       = "failed during delegation ID creation"
	errLocationFailedCreatingResource               = "failed creating resource"
	errLocationFailedDeletingResource               = "failed deleting resource"
	errLocationFailedDuringTransaction              = "failed during transaction"
	errLocationFailedGettingDatabaseClient          = "failed getting database client"
	errLocationFailedGettingResourceID              = "failed getting resource ID"
	errLocationFailedGettingSystemData              = "failed getting system data"
	errLocationFailedGettingVersionedInterface      = "failed getting versioned interface"
	errLocationFailedIteratingActiveOperations      = "failed iterating over active operations"
	errLocationFailedIteratingResource              = "failed iterating over resource"
	errLocationFailedMarshallingCluster             = "failed marshalling cluster"
	errLocationFailedMarshallingNodePool            = "failed marshalling node pool"
	errLocationFailedMarshallingResponse            = "failed marshalling response"
	errLocationFailedParsingID                      = "failed parsing ID"
	errLocationFailedReadingAncestor                = "failed reading ancestor"
	errLocationFailedReadingCluster                 = "failed reading cluster"
	errLocationFailedReadingClusterAgain            = "failed reading cluster again"
	errLocationFailedReadingNodePool                = "failed reading node pool"
	errLocationFailedReadingResource                = "failed reading resource"
	errLocationFailedReadingResourceToMarshal       = "failed reading resource to marshal"
	errLocationFailedReadingTransactionResult       = "failed reading transaction result"
	errLocationFailedSettingNextLink                = "failed setting next link"
	errLocationFailedToAcquireLock                  = "failed to acquire lock"
	errLocationFailedUpdatingResource               = "failed updating resource"
	errLocationNotReady                             = "not ready"
	errLocationPanicked                             = "panicked"
	errLocationProvisioningFailedOrCancelled        = "provisioning was failed or cancelled"
	errLocationUnhandledState                       = "unhandled state"
	errLocationUnknownClusterServiceError           = "unknown cluster-service error type"
	errLocationUnknownType                          = "unknown type"
	errLocationFailedGettingBody                    = "failed getting body"
)
