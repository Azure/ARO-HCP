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

import (
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/errorutils"
)

const (
	WildcardDeploymentName    = "{" + PathSegmentDeploymentName + "}"
	WildcardLocation          = "{" + PathSegmentLocation + "}"
	WildcardNodePoolName      = "{" + PathSegmentNodePoolName + "}"
	WildcardExternalAuthName  = "{" + PathSegmentExternalAuthName + "}"
	WildcardOperationID       = "{" + PathSegmentOperationID + "}"
	WildcardResourceGroupName = "{" + PathSegmentResourceGroupName + "}"
	WildcardResourceName      = "{" + PathSegmentResourceName + "}"
	WildcardSubscriptionID    = "{" + PathSegmentSubscriptionID + "}"

	PatternSubscriptions     = "subscriptions/" + WildcardSubscriptionID
	PatternLocations         = "locations/" + WildcardLocation
	PatternProviders         = "providers/" + coreapi.ProviderNamespace
	PatternClusters          = coreapi.ClusterResourceTypeName + "/" + WildcardResourceName
	PatternNodePools         = coreapi.NodePoolResourceTypeName + "/" + WildcardNodePoolName
	PatternVersions          = coreapi.VersionResourceTypeName + "/" + WildcardResourceName
	PatternExternalAuth      = coreapi.ExternalAuthResourceTypeName + "/" + WildcardExternalAuthName
	PatternDeployments       = "deployments/" + WildcardDeploymentName
	PatternResourceGroups    = "resourcegroups/" + WildcardResourceGroupName
	PatternOperationResults  = coreapi.OperationResultResourceTypeName + "/" + WildcardOperationID
	PatternOperationStatuses = coreapi.OperationStatusResourceTypeName + "/" + WildcardOperationID

	ActionRequestAdminCredential = "requestadmincredential"
	ActionRevokeCredentials      = "revokecredentials"

	// User-visible display names for provider and resource types
	ProviderDisplay                          = "Azure Red Hat OpenShift"
	ClusterResourceTypeDisplaySingle         = "Hosted Control Plane (HCP) OpenShift Cluster"
	ClusterResourceTypeDisplayPlural         = "Hosted Control Plane (HCP) OpenShift Clusters"
	NodePoolResourceTypeDisplaySingle        = "Node Pool"
	NodePoolResourceTypeDisplayPlural        = "Node Pools"
	ExternalAuthResourceTypeDisplaySingle    = "External Authentication Configuration"
	ExternalAuthResourceTypeDisplayPlural    = "External Authentication Configurations"
	VersionResourceTypeDisplaySingle         = "OpenShift Container Platform Version"
	VersionResourceTypeDisplayPlural         = "OpenShift Container Platform Versions"
	OperationResultResourceTypeDisplaySingle = "Asynchronous Operation Result"
	OperationResultResourceTypeDisplayPlural = "Asynchronous Operation Results"
	OperationStatusResourceTypeDisplaySingle = "Asynchronous Operation Status"
	OperationStatusResourceTypeDisplayPlural = "Asynchronous Operation Statuses"
)

// AvailableOperations defines the static response content for the resource provider's "operations" endpoint.
// There should be an entry in this list for each route defined in our provider namespace. For more details see:
// https://github.com/cloud-and-ai-microsoft/resource-provider-contract/blob/master/v1.0/proxy-api-reference.md#exposing-available-operations
var AvailableOperations = []coreapi.NamespaceOperation{
	{
		// Per the RPC, this is a required operation. It is not specific to ARO-HCP.
		Name: path.Join(coreapi.ProviderNamespace, "register", coreapi.NamespaceOperationAction),
		Display: coreapi.NamespaceOperationDisplay{
			Provider:    ProviderDisplay,
			Resource:    ProviderDisplay,
			Operation:   "Register the Azure Red Hat OpenShift (ARO) Resource Provider",
			Description: "Register the subscription for the Azure Red Hat OpenShift (ARO) resource provider and enable the creation of OpenShift clusters",
		},
	},
	{
		// Another required operation not mentioned in the RPC. It is not specific to ARO-HCP.
		Name: path.Join(coreapi.ProviderNamespace, "unregister", coreapi.NamespaceOperationAction),
		Display: coreapi.NamespaceOperationDisplay{
			Provider:    ProviderDisplay,
			Resource:    ProviderDisplay,
			Operation:   "Unregister the Azure Red Hat OpenShift (ARO) Resource Provider",
			Description: "Unregister the subscription from the Azure Red Hat OpenShift (ARO) resource provider",
		},
	},
	{
		Name: path.Join(coreapi.ClusterResourceType.String(), coreapi.NamespaceOperationRead),
		Display: coreapi.NamespaceOperationDisplay{
			Provider:    ProviderDisplay,
			Resource:    ClusterResourceTypeDisplayPlural,
			Operation:   "Read " + ClusterResourceTypeDisplaySingle,
			Description: "Read any " + ClusterResourceTypeDisplayPlural,
		},
	},
	{
		Name: path.Join(coreapi.ClusterResourceType.String(), coreapi.NamespaceOperationWrite),
		Display: coreapi.NamespaceOperationDisplay{
			Provider:    ProviderDisplay,
			Resource:    ClusterResourceTypeDisplayPlural,
			Operation:   "Create or Update " + ClusterResourceTypeDisplaySingle,
			Description: "Create or Update any " + ClusterResourceTypeDisplayPlural,
		},
	},
	{
		Name: path.Join(coreapi.ClusterResourceType.String(), coreapi.NamespaceOperationDelete),
		Display: coreapi.NamespaceOperationDisplay{
			Provider:    ProviderDisplay,
			Resource:    ClusterResourceTypeDisplayPlural,
			Operation:   "Delete " + ClusterResourceTypeDisplaySingle,
			Description: "Delete any " + ClusterResourceTypeDisplayPlural,
		},
	},
	{
		Name: path.Join(coreapi.ClusterResourceType.String(), ActionRequestAdminCredential, coreapi.NamespaceOperationAction),
		Display: coreapi.NamespaceOperationDisplay{
			Provider:    ProviderDisplay,
			Resource:    ClusterResourceTypeDisplayPlural,
			Operation:   "Request Administrator Credential",
			Description: "Request a kubeconfig file for a " + ClusterResourceTypeDisplaySingle + " that authenticates using a limited-time signed certificate with administrator permissions",
		},
	},
	{
		Name: path.Join(coreapi.ClusterResourceType.String(), ActionRevokeCredentials, coreapi.NamespaceOperationAction),
		Display: coreapi.NamespaceOperationDisplay{
			Provider:    ProviderDisplay,
			Resource:    ClusterResourceTypeDisplayPlural,
			Operation:   "Revoke All Credentials",
			Description: "Revoke all unexpired certificates issued for user access to a " + ClusterResourceTypeDisplaySingle,
		},
	},
	{
		Name: path.Join(coreapi.NodePoolResourceType.String(), coreapi.NamespaceOperationRead),
		Display: coreapi.NamespaceOperationDisplay{
			Provider:    ProviderDisplay,
			Resource:    NodePoolResourceTypeDisplayPlural,
			Operation:   "Read " + NodePoolResourceTypeDisplaySingle,
			Description: "Read any " + NodePoolResourceTypeDisplayPlural,
		},
	},
	{
		Name: path.Join(coreapi.NodePoolResourceType.String(), coreapi.NamespaceOperationWrite),
		Display: coreapi.NamespaceOperationDisplay{
			Provider:    ProviderDisplay,
			Resource:    NodePoolResourceTypeDisplayPlural,
			Operation:   "Create or Update " + NodePoolResourceTypeDisplaySingle,
			Description: "Create or Update any " + NodePoolResourceTypeDisplayPlural,
		},
	},
	{
		Name: path.Join(coreapi.NodePoolResourceType.String(), coreapi.NamespaceOperationDelete),
		Display: coreapi.NamespaceOperationDisplay{
			Provider:    ProviderDisplay,
			Resource:    NodePoolResourceTypeDisplayPlural,
			Operation:   "Delete " + NodePoolResourceTypeDisplaySingle,
			Description: "Delete any " + NodePoolResourceTypeDisplayPlural,
		},
	},
	{
		Name: path.Join(coreapi.ExternalAuthResourceType.String(), coreapi.NamespaceOperationRead),
		Display: coreapi.NamespaceOperationDisplay{
			Provider:    ProviderDisplay,
			Resource:    ExternalAuthResourceTypeDisplayPlural,
			Operation:   "Read " + ExternalAuthResourceTypeDisplaySingle,
			Description: "Read any " + ExternalAuthResourceTypeDisplayPlural,
		},
	},
	{
		Name: path.Join(coreapi.ExternalAuthResourceType.String(), coreapi.NamespaceOperationWrite),
		Display: coreapi.NamespaceOperationDisplay{
			Provider:    ProviderDisplay,
			Resource:    ExternalAuthResourceTypeDisplayPlural,
			Operation:   "Create or Update " + ExternalAuthResourceTypeDisplaySingle,
			Description: "Create or Update any " + ExternalAuthResourceTypeDisplayPlural,
		},
	},
	{
		Name: path.Join(coreapi.ExternalAuthResourceType.String(), coreapi.NamespaceOperationDelete),
		Display: coreapi.NamespaceOperationDisplay{
			Provider:    ProviderDisplay,
			Resource:    ExternalAuthResourceTypeDisplayPlural,
			Operation:   "Delete " + ExternalAuthResourceTypeDisplaySingle,
			Description: "Delete any " + ExternalAuthResourceTypeDisplayPlural,
		},
	},
	{
		Name: path.Join(coreapi.VersionResourceType.String(), coreapi.NamespaceOperationRead),
		Display: coreapi.NamespaceOperationDisplay{
			Provider:    ProviderDisplay,
			Resource:    VersionResourceTypeDisplayPlural,
			Operation:   "Read " + VersionResourceTypeDisplaySingle,
			Description: "Read any " + VersionResourceTypeDisplayPlural,
		},
	},
	{
		Name: path.Join(coreapi.ProviderNamespace, "locations", coreapi.OperationResultResourceTypeName, coreapi.NamespaceOperationRead),
		Display: coreapi.NamespaceOperationDisplay{
			Provider:    ProviderDisplay,
			Resource:    OperationResultResourceTypeDisplayPlural,
			Operation:   "Read " + OperationResultResourceTypeDisplaySingle,
			Description: "Read the result of a successful asynchronous operation",
		},
	},
	{
		Name: path.Join(coreapi.ProviderNamespace, "locations", coreapi.OperationStatusResourceTypeName, coreapi.NamespaceOperationRead),
		Display: coreapi.NamespaceOperationDisplay{
			Provider:    ProviderDisplay,
			Resource:    OperationStatusResourceTypeDisplayPlural,
			Operation:   "Read " + OperationStatusResourceTypeDisplaySingle,
			Description: "Read the status of an ongoing or failed asynchronous operation",
		},
	},
}

// These operations were copied from the ARO "Classic" service:
// https://github.com/Azure/ARO-RP/blob/master/pkg/api/operation.go
//
// ARO-HCP will respond with both services' operations until we
// have approval to use the Split Operations ARM feature.
//
// Note, these are technically non-conformant to RPC requirements
// because they lack the required Display.Description field. Unit
// tests have been temporarily(?) adjusted to compensate.
var AvailableClassicOperations = []coreapi.NamespaceOperation{
	{
		Name: path.Join(coreapi.ProviderNamespace, "locations", "operationresults", coreapi.NamespaceOperationRead),
		Display: coreapi.NamespaceOperationDisplay{
			Provider:  ProviderDisplay,
			Resource:  "locations/operationresults",
			Operation: "Read operation results",
		},
		Origin: coreapi.NamespaceOperationOriginUserSystem,
	},
	{
		Name: path.Join(coreapi.ProviderNamespace, "locations", "operationsstatus", coreapi.NamespaceOperationRead),
		Display: coreapi.NamespaceOperationDisplay{
			Provider:  ProviderDisplay,
			Resource:  "locations/operationsstatus",
			Operation: "Read operations status",
		},
		Origin: coreapi.NamespaceOperationOriginUserSystem,
	},
	{
		Name: path.Join(coreapi.ProviderNamespace, "operations", coreapi.NamespaceOperationRead),
		Display: coreapi.NamespaceOperationDisplay{
			Provider:  ProviderDisplay,
			Resource:  "operations",
			Operation: "Read operations",
		},
		Origin: coreapi.NamespaceOperationOriginUserSystem,
	},
	{
		Name: path.Join(coreapi.ProviderNamespace, "openShiftClusters", coreapi.NamespaceOperationRead),
		Display: coreapi.NamespaceOperationDisplay{
			Provider:  ProviderDisplay,
			Resource:  "openShiftClusters",
			Operation: "Read OpenShift cluster",
		},
		Origin: coreapi.NamespaceOperationOriginUserSystem,
	},
	{
		Name: path.Join(coreapi.ProviderNamespace, "openShiftClusters", coreapi.NamespaceOperationWrite),
		Display: coreapi.NamespaceOperationDisplay{
			Provider:  ProviderDisplay,
			Resource:  "openShiftClusters",
			Operation: "Write OpenShift cluster",
		},
		Origin: coreapi.NamespaceOperationOriginUserSystem,
	},
	{
		Name: path.Join(coreapi.ProviderNamespace, "openShiftClusters", coreapi.NamespaceOperationDelete),
		Display: coreapi.NamespaceOperationDisplay{
			Provider:  ProviderDisplay,
			Resource:  "openShiftClusters",
			Operation: "Delete OpenShift cluster",
		},
		Origin: coreapi.NamespaceOperationOriginUserSystem,
	},
	{
		Name: path.Join(coreapi.ProviderNamespace, "openShiftClusters", "listCredentials", coreapi.NamespaceOperationAction),
		Display: coreapi.NamespaceOperationDisplay{
			Provider:  ProviderDisplay,
			Resource:  "openShiftClusters",
			Operation: "List credentials of an OpenShift cluster",
		},
		Origin: coreapi.NamespaceOperationOriginUserSystem,
	},
	{
		Name: path.Join(coreapi.ProviderNamespace, "openShiftClusters", "listAdminCredentials", coreapi.NamespaceOperationAction),
		Display: coreapi.NamespaceOperationDisplay{
			Provider:  ProviderDisplay,
			Resource:  "openShiftClusters",
			Operation: "List Admin Kubeconfig of an OpenShift cluster",
		},
		Origin: coreapi.NamespaceOperationOriginUserSystem,
	},
	{
		Name: path.Join(coreapi.ProviderNamespace, "openShiftClusters", "detectors", coreapi.NamespaceOperationRead),
		Display: coreapi.NamespaceOperationDisplay{
			Provider:  ProviderDisplay,
			Resource:  "openShiftClusters",
			Operation: "Get OpenShift Cluster Detector",
		},
		Origin: coreapi.NamespaceOperationOriginUserSystem,
	},
	{
		Name: path.Join(coreapi.ProviderNamespace, "locations", "listPlatformWorkloadIdentityRoleSets", coreapi.NamespaceOperationRead),
		Display: coreapi.NamespaceOperationDisplay{
			Provider:  ProviderDisplay,
			Resource:  "listPlatformWorkloadIdentityRoleSets",
			Operation: "Lists all PlatformWorkloadIdentityRoleSets available in the specified location",
		},
		Origin: coreapi.NamespaceOperationOriginUserSystem,
	},
	{
		Name: path.Join(coreapi.ProviderNamespace, "locations", "openshiftVersions", coreapi.NamespaceOperationRead),
		Display: coreapi.NamespaceOperationDisplay{
			Provider:  ProviderDisplay,
			Resource:  "openshiftVersions",
			Operation: "Lists all OpenShift versions available to install in the specified location",
		},
		Origin: coreapi.NamespaceOperationOriginUserSystem,
	},
}

// MuxPattern forms a URL pattern suitable for passing to http.ServeMux.
// Literal path segments must be lowercase because MiddlewareLowercase
// converts the request URL to lowercase before multiplexing.
func MuxPattern(method string, segments ...string) string {
	return fmt.Sprintf("%s /%s", method, strings.ToLower(path.Join(segments...)))
}

func (f *Frontend) routes(r prometheus.Registerer) http.Handler {
	// Setup metrics middleware
	metricsMiddleware := NewMetricsMiddleware(r, f.collector)

	middlewareMux := NewMiddlewareMux(
		MiddlewarePanic,
		MiddlewareReferer,
		metricsMiddleware.Metrics(),
		MiddlewareCorrelationData,
		newMiddlewareAudit(f.auditClient).handleRequest,
		MiddlewareTracing,
		MiddlewareLowercase,
		MiddlewareLogging,
		// NOTE: register panic middleware twice.
		// Making sure we can capture panicked requests in our trace data.
		// But we also can recover if the tracing or logging middleware caused a panic.
		MiddlewarePanic,
		MiddlewareBody,
		MiddlewareSystemData,
	)

	middlewareMux.HandleFunc("/", f.NotFound)

	// Resource list endpoints
	postMuxMiddleware := NewMiddleware(
		MiddlewareResourceID,
		MiddlewareLoggingPostMux,
		newMiddlewareValidatedAPIVersion(f.apiRegistry).handleRequest,
		newMiddlewareValidateSubscriptionState(f.resourcesDBClient).handleRequest)
	middlewareMux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternProviders, coreapi.ClusterResourceTypeName),
		postMuxMiddleware.HandlerFunc(errorutils.ReportError(f.ArmResourceListClusters)))
	middlewareMux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternResourceGroups, PatternProviders, coreapi.ClusterResourceTypeName),
		postMuxMiddleware.HandlerFunc(errorutils.ReportError(f.ArmResourceListClusters)))
	middlewareMux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, coreapi.NodePoolResourceTypeName),
		postMuxMiddleware.HandlerFunc(errorutils.ReportError(f.ArmResourceListNodePools)))
	middlewareMux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, coreapi.ExternalAuthResourceTypeName),
		postMuxMiddleware.HandlerFunc(errorutils.ReportError(f.ArmResourceListExternalAuths)))
	middlewareMux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternProviders, PatternLocations, coreapi.VersionResourceTypeName),
		postMuxMiddleware.HandlerFunc(errorutils.ReportError(f.ArmResourceListVersion)))

	// Resource read endpoints
	// These endpoints must have a corresponding entry in AvailableOperations.
	postMuxMiddleware = NewMiddleware(
		MiddlewareResourceID,
		MiddlewareLoggingPostMux,
		newMiddlewareValidatedAPIVersion(f.apiRegistry).handleRequest,
		newMiddlewareValidateSubscriptionState(f.resourcesDBClient).handleRequest)
	middlewareMux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters),
		postMuxMiddleware.HandlerFunc(errorutils.ReportError(f.GetHCPCluster)))
	middlewareMux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, PatternNodePools),
		postMuxMiddleware.HandlerFunc(errorutils.ReportError(f.GetNodePool)))
	middlewareMux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternProviders, PatternLocations, PatternVersions),
		postMuxMiddleware.HandlerFunc(errorutils.ReportError(f.GetOpenshiftVersions)))
	middlewareMux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, PatternExternalAuth),
		postMuxMiddleware.HandlerFunc(errorutils.ReportError(f.GetExternalAuth)))

	// Resource create/update/delete endpoints
	// These endpoints must have a corresponding entry in AvailableOperations.
	postMuxMiddleware = NewMiddleware(
		MiddlewareResourceID,
		MiddlewareLoggingPostMux,
		newMiddlewareValidatedAPIVersion(f.apiRegistry).handleRequest,
		newMiddlewareValidateSubscriptionState(f.resourcesDBClient).handleRequest)
	middlewareMux.Handle(
		MuxPattern(http.MethodPut, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters),
		postMuxMiddleware.HandlerFunc(errorutils.ReportError(f.CreateOrUpdateHCPCluster)))
	middlewareMux.Handle(
		MuxPattern(http.MethodPatch, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters),
		postMuxMiddleware.HandlerFunc(errorutils.ReportError(f.CreateOrUpdateHCPCluster)))
	middlewareMux.Handle(
		MuxPattern(http.MethodDelete, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters),
		postMuxMiddleware.HandlerFunc(errorutils.ReportError(f.DeleteCluster)))
	middlewareMux.Handle(
		MuxPattern(http.MethodPost, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, ActionRequestAdminCredential),
		postMuxMiddleware.HandlerFunc(errorutils.ReportError(f.ArmResourceActionRequestAdminCredential)))
	middlewareMux.Handle(
		MuxPattern(http.MethodPost, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, ActionRevokeCredentials),
		postMuxMiddleware.HandlerFunc(errorutils.ReportError(f.ArmResourceActionRevokeCredentials)))
	middlewareMux.Handle(
		MuxPattern(http.MethodPut, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, PatternNodePools),
		postMuxMiddleware.HandlerFunc(errorutils.ReportError(f.CreateOrUpdateNodePool)))
	middlewareMux.Handle(
		MuxPattern(http.MethodPatch, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, PatternNodePools),
		postMuxMiddleware.HandlerFunc(errorutils.ReportError(f.CreateOrUpdateNodePool)))
	middlewareMux.Handle(
		MuxPattern(http.MethodDelete, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, PatternNodePools),
		postMuxMiddleware.HandlerFunc(errorutils.ReportError(f.DeleteNodePool)))
	middlewareMux.Handle(
		MuxPattern(http.MethodPut, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, PatternExternalAuth),
		postMuxMiddleware.HandlerFunc(errorutils.ReportError(f.CreateOrUpdateExternalAuth)))
	middlewareMux.Handle(
		MuxPattern(http.MethodPatch, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, PatternExternalAuth),
		postMuxMiddleware.HandlerFunc(errorutils.ReportError(f.CreateOrUpdateExternalAuth)))
	middlewareMux.Handle(
		MuxPattern(http.MethodDelete, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, PatternExternalAuth),
		postMuxMiddleware.HandlerFunc(errorutils.ReportError(f.DeleteExternalAuth)))

	// Asynchronous operation endpoints
	// These endpoints must have a corresponding entry in AvailableOperations.
	postMuxMiddleware = NewMiddleware(
		MiddlewareResourceID,
		MiddlewareLoggingPostMux,
		newMiddlewareValidatedAPIVersion(f.apiRegistry).handleRequest,
		newMiddlewareValidateSubscriptionState(f.resourcesDBClient).handleRequest)
	middlewareMux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternProviders, PatternLocations, PatternOperationResults),
		postMuxMiddleware.HandlerFunc(errorutils.ReportError(f.OperationResult)))
	middlewareMux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternProviders, PatternLocations, PatternOperationStatuses),
		postMuxMiddleware.HandlerFunc(errorutils.ReportError(f.OperationStatus)))

	// Exclude ARO-HCP API version validation for the following endpoints defined by ARM.

	// Available operations endpoint
	// This is a tenant-wide endpoint and is therefore not grouped with the other subscription-scoped list endpoints.
	// API version validation is intentionally omitted on this endpoint because both ARO Classic and ARO-HCP services
	// share the same provider namespace, and each service uses its own disjoint set of API versions. This endpoint
	// must respond with the same content across all API versions registered to this provider namespace.
	postMuxMiddleware = NewMiddleware(
		MiddlewareLoggingPostMux)
	middlewareMux.Handle(
		MuxPattern(http.MethodGet, PatternProviders, "operations"),
		postMuxMiddleware.HandlerFunc(errorutils.ReportError(f.ArmOperationsList)))

	// Subscription management endpoints
	postMuxMiddleware = NewMiddleware(
		MiddlewareResourceID,
		MiddlewareLoggingPostMux)
	middlewareMux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions),
		postMuxMiddleware.HandlerFunc(errorutils.ReportError(f.ArmSubscriptionGet)))
	postMuxMiddleware = NewMiddleware(
		MiddlewareResourceID,
		MiddlewareLoggingPostMux)
	middlewareMux.Handle(
		MuxPattern(http.MethodPut, PatternSubscriptions),
		postMuxMiddleware.HandlerFunc(errorutils.ReportError(f.ArmSubscriptionPut)))

	// Deployment preflight endpoint
	postMuxMiddleware = NewMiddleware(
		MiddlewareLoggingPostMux,
		newMiddlewareValidateSubscriptionState(f.resourcesDBClient).handleRequest)
	middlewareMux.Handle(
		MuxPattern(http.MethodPost, PatternSubscriptions, PatternResourceGroups, "providers", coreapi.ProviderNamespace, PatternDeployments, "preflight"),
		postMuxMiddleware.HandlerFunc(errorutils.ReportError(f.ArmDeploymentPreflight)))

	mux := http.NewServeMux()
	mux.HandleFunc("/", middlewareMux.ServeHTTP)

	// These endpoints do not use middleware. They are only called
	// from within the service cluster or via kubectl port forwarding.
	mux.HandleFunc(MuxPattern(http.MethodGet, "healthz"), f.Healthz)
	mux.HandleFunc(MuxPattern(http.MethodGet, "location"), f.Location)

	return mux
}

func (f *Frontend) metricsRoutes(gatherer prometheus.Gatherer) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{}))

	return mux
}
