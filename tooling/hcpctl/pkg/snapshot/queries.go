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

package snapshot

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"strings"
	"text/template"
	"time"
)

//go:embed queries
var queriesFS embed.FS

// queryCategory determines where a query's output is written and how it is deduplicated.
type queryCategory string

const (
	// categoryContext queries are time-windowed and resource-group-scoped. They run once
	// per snapshot and are written to context/<component>/<queryName>.md.
	categoryContext queryCategory = "context"

	// categoryRequestDiscovery queries depend on a specific correlation ID. They run
	// per-request and are written to resources/<type>/<name>/requests/<METHOD>-<client_request_id>/discovery/<component>/<queryName>.md.
	categoryRequestDiscovery queryCategory = "requestDiscovery"

	// categoryResourceDiscovery queries depend on data discovered per-request but produce
	// results that are stable per-resource. They run once per unique resource and are
	// written to resources/<type>/<name>/discovery/<component>/<queryName>.md.
	categoryResourceDiscovery queryCategory = "resourceDiscovery"

	// categoryState queries are time-windowed and resource-scoped. They run once per
	// unique resource and are written to resources/<type>/<name>/state/<component>/<queryName>.md.
	categoryState queryCategory = "state"

	// categoryConditions queries extract status condition summaries. They run once per
	// unique resource and are written to resources/<type>/<name>/conditions/<component>/<queryName>.md.
	categoryConditions queryCategory = "conditions"

	// categoryLogs queries extract filtered or aggregated container/audit logs. They run once per
	// unique resource and are written to resources/<type>/<name>/logs/<component>/<queryName>.md.
	categoryLogs queryCategory = "logs"

	// categoryTraceState queries are per-request and produce state dumps specific to a request
	// (e.g. async operation state). They are written to
	// resources/<type>/<name>/requests/<METHOD>-<client_request_id>/state/<component>/<queryName>.md.
	categoryTraceState queryCategory = "traceState"

	// categoryTraceLogs queries are per-request and produce log output specific to a request
	// (e.g. async operation polling history). They are written to
	// resources/<type>/<name>/requests/<METHOD>-<client_request_id>/logs/<component>/<queryName>.md.
	categoryTraceLogs queryCategory = "traceLogs"

	// categoryEvents queries produce event logs scoped to a service component. They run once
	// per resource and are written to events/<component>/.
	categoryEvents queryCategory = "events"

	// categoryResourceEvents queries produce event logs specific to a single ARM resource
	// (e.g. control plane events for a cluster). They are written to
	// resources/<type>/<name>/events/<component>/<queryName>.md.
	categoryResourceEvents queryCategory = "resourceEvents"
)

// querySpec describes a single KQL query in the dependency chain.
type querySpec struct {
	// component is the top-level directory name (e.g. "frontend").
	component string
	// queryName is the output file name stem (e.g. "asyncOperationId" produces "asyncOperationId.md").
	queryName string
	// templatePath is the path within the embedded FS.
	templatePath string
	// database selects which database to query against: "service" or "hcp".
	database string
	// category determines where this query's output is written and how it is deduplicated.
	category queryCategory
	// ready returns true when all data required by this query is available.
	// A nil ready function means the query has no prerequisites.
	ready func(queryData) bool
	// prerequisites is a human-readable description of required data.
	prerequisites string
	// requiredWhen returns true if this query is expected to produce results given
	// the current data. A nil requiredWhen means the query is informational and
	// empty results are always acceptable (it will not appear in verification output).
	requiredWhen func(queryData) bool
	// storeResult is called with the full result rows from the query.
	// It stores discovered values for dependent queries. If the results
	// are ambiguous (e.g. multiple distinct values for a single-valued field),
	// storeResult should return an error.
	storeResult func(*queryData, []resultRow) error
}

// key returns the "component/queryName" identifier for this query.
func (q querySpec) key() string {
	return q.component + "/" + q.queryName
}

// queryData holds all context accumulated across the query chain.
type queryData struct {
	// Seed values — provided by the caller.
	ClusterURI      string
	ServiceDatabase string
	HCPDatabase     string
	ResourceGroup   string

	// ServiceClusterName and ManagementClusterName are AKS cluster names
	// used to filter queries for PR jobs. When both are non-empty, queries
	// include a "| where cluster in (...)" filter.
	ServiceClusterName    string
	ManagementClusterName string

	// FullStartTime and FullEndTime define the entire snapshot window. Use
	// these for broad timestamp pre-filters (Kusto partition pruning) and
	// for discovery queries that must see the complete time range.
	FullStartTime time.Time
	FullEndTime   time.Time

	// PhaseStartTime and PhaseEndTime define the current phase (test or
	// cleanup). Use these for queries that should be scoped to a single phase.
	PhaseStartTime time.Time
	PhaseEndTime   time.Time

	// PhaseName is the human-readable name of the current phase ("test" or "cleanup").
	PhaseName string

	// Per-request context — set when tracing a specific request.
	CorrelationID      string
	ClientRequestID    string
	ResponseStatusCode int

	// Discovered by queries.
	ResourceID                  string
	ResourceType                string
	ResourceName                string
	ClusterResourceID           string
	ClusterResourceName         string
	ServiceProviderResourceType string
	AsyncOperationId            string
	AsyncOperationPath          string
	InternalID                  string
	ClusterID                   string
	HostedClusterNamespace      string
	HostedControlPlaneNamespace string
}

// serviceProviderResourceType maps an ARM resource type to its corresponding
// service provider sub-resource type.
func serviceProviderResourceType(resourceType string) string {
	switch strings.ToLower(resourceType) {
	case "microsoft.redhatopenshift/hcpopenshiftclusters":
		return "microsoft.redhatopenshift/hcpopenshiftclusters/serviceproviderclusters"
	case "microsoft.redhatopenshift/hcpopenshiftclusters/nodepools":
		return "microsoft.redhatopenshift/hcpopenshiftclusters/nodepools/serviceprovidernodepools"
	default:
		return ""
	}
}

// isClusterType returns true if the resource type is the top-level HCP cluster.
func isClusterType(d queryData) bool {
	return strings.EqualFold(d.ResourceType, "microsoft.redhatopenshift/hcpopenshiftclusters")
}

// isNodePoolType returns true if the resource type is a nodepool.
func isNodePoolType(d queryData) bool {
	return strings.EqualFold(d.ResourceType, "microsoft.redhatopenshift/hcpopenshiftclusters/nodepools")
}

// isClusterOrNodePool returns true if the resource type is a cluster or nodepool.
func isClusterOrNodePool(d queryData) bool {
	return isClusterType(d) || isNodePoolType(d)
}

// hasAsyncOperations returns true if the resource type uses async operations.
// Action-style sub-resources like requestadmincredential, revokecredentials,
// and externalauths do not have async operations.
func hasAsyncOperations(d queryData) bool {
	rt := strings.ToLower(d.ResourceType)
	switch rt {
	case "microsoft.redhatopenshift/hcpopenshiftclusters/requestadmincredential",
		"microsoft.redhatopenshift/hcpopenshiftclusters/revokecredentials",
		"microsoft.redhatopenshift/hcpopenshiftclusters/externalauths":
		return false
	default:
		return true
	}
}

// isClientError returns true if the response status code is a 4xx client error.
func isClientError(d queryData) bool {
	return d.ResponseStatusCode >= 400 && d.ResponseStatusCode < 500
}

// allQueries defines the complete ordered query chain. Each query has a category
// that determines where its output is written and whether it is deduplicated
// across requests for the same resource. Queries are executed in order within
// each phase of the gathering pipeline.
var allQueries = []querySpec{
	// --- Request discovery: per client request ID ---
	{
		component:    "frontend",
		queryName:    "clientRequestId",
		templatePath: "queries/frontend/clientRequestId/query.kql",
		database:     "service",
		category:     categoryRequestDiscovery,
		ready: func(d queryData) bool {
			return d.CorrelationID != "" && d.ClientRequestID == ""
		},
		prerequisites: "CorrelationID (and ClientRequestID not already set)",
		storeResult: func(d *queryData, rows []resultRow) error {
			if len(rows) > 1 {
				return fmt.Errorf("query returned %d distinct client request IDs for correlation_request_id, expected 1", len(rows))
			}
			d.ClientRequestID = rows[0].values[0]
			return nil
		},
	},
	{
		component:    "frontend",
		queryName:    "asyncOperationId",
		templatePath: "queries/frontend/asyncOperationId/query.kql",
		database:     "service",
		category:     categoryRequestDiscovery,
		ready: func(d queryData) bool {
			return d.ClientRequestID != ""
		},
		prerequisites: "ClientRequestID",
		requiredWhen: func(d queryData) bool {
			return (d.ClientRequestID != "" || d.CorrelationID != "") && hasAsyncOperations(d) && !isClientError(d)
		},
		storeResult: func(d *queryData, rows []resultRow) error {
			if len(rows) > 1 {
				return fmt.Errorf("query returned %d distinct async operation IDs for client_request_id, expected 1", len(rows))
			}
			d.AsyncOperationId = rows[0].values[0]
			return nil
		},
	},
	{
		component:    "frontend",
		queryName:    "asyncOperationPath",
		templatePath: "queries/frontend/asyncOperationPath/query.kql",
		database:     "service",
		category:     categoryRequestDiscovery,
		ready: func(d queryData) bool {
			return d.ClientRequestID != ""
		},
		prerequisites: "ClientRequestID",
		requiredWhen: func(d queryData) bool {
			return (d.ClientRequestID != "" || d.CorrelationID != "") && hasAsyncOperations(d) && !isClientError(d)
		},
		storeResult: func(d *queryData, rows []resultRow) error {
			if len(rows) > 1 {
				return fmt.Errorf("query returned %d distinct async operation paths for client_request_id, expected 1", len(rows))
			}
			d.AsyncOperationPath = rows[0].values[0]
			return nil
		},
	},

	// --- Trace: per-request, depends on request-specific data ---
	{
		component:    "frontend",
		queryName:    "requestLogs",
		templatePath: "queries/frontend/requestLogs/query.kql",
		database:     "service",
		category:     categoryTraceLogs,
		ready: func(d queryData) bool {
			return d.ClientRequestID != ""
		},
		prerequisites: "ClientRequestID",
		requiredWhen:  func(d queryData) bool { return d.ClientRequestID != "" },
	},
	{
		component:    "frontend",
		queryName:    "asyncOperationRequests",
		templatePath: "queries/frontend/asyncOperationRequests/query.kql",
		database:     "service",
		category:     categoryTraceLogs,
		ready: func(d queryData) bool {
			return d.AsyncOperationPath != ""
		},
		prerequisites: "AsyncOperationPath",
		requiredWhen:  func(d queryData) bool { return d.AsyncOperationPath != "" && !isClientError(d) },
	},

	// --- Resource discovery: stable per-resource ---
	{
		component:    "backend",
		queryName:    "resourceInternalId",
		templatePath: "queries/backend/resourceInternalId/query.kql",
		database:     "service",
		category:     categoryResourceDiscovery,
		ready: func(d queryData) bool {
			return d.ResourceGroup != "" && d.ResourceType != "" && d.ResourceID != "" && d.ClusterResourceName != ""
		},
		prerequisites: "ResourceGroup, ResourceType, ResourceID",
		requiredWhen:  isClusterOrNodePool,
		storeResult: func(d *queryData, rows []resultRow) error {
			if len(rows) > 1 {
				return fmt.Errorf("query returned %d distinct internal IDs, expected 1", len(rows))
			}
			d.InternalID = rows[0].values[0]
			return nil
		},
	},
	{
		component:    "clustersService",
		queryName:    "cid",
		templatePath: "queries/clustersService/cid/query.kql",
		database:     "service",
		category:     categoryResourceDiscovery,
		ready: func(d queryData) bool {
			return d.ClusterResourceID != ""
		},
		prerequisites: "ClusterResourceID",
		requiredWhen:  isClusterType,
		storeResult: func(d *queryData, rows []resultRow) error {
			if len(rows) > 1 {
				return fmt.Errorf("query returned %d distinct cluster IDs, expected 1", len(rows))
			}
			d.ClusterID = rows[0].values[0]
			return nil
		},
	},
	{
		component:    "clustersService",
		queryName:    "maestroBundleAssociations",
		templatePath: "queries/clustersService/maestroBundleAssociations/query.kql",
		database:     "service",
		category:     categoryResourceDiscovery,
		ready: func(d queryData) bool {
			return d.ClusterID != ""
		},
		prerequisites: "ClusterID",
		// Maestro readonly bundles are being phased out in favor of ReadDesires,
		// so empty results are acceptable. Query stays informational for older snapshots.
	},
	{
		component:    "backend",
		queryName:    "maestroBundleAssociations",
		templatePath: "queries/backend/maestroBundleAssociations/query.kql",
		database:     "service",
		category:     categoryResourceDiscovery,
		ready: func(d queryData) bool {
			return d.ResourceGroup != "" && d.ClusterResourceName != "" && d.ServiceProviderResourceType != "" && d.ResourceName != ""
		},
		prerequisites: "ResourceGroup, ClusterResourceName, ServiceProviderResourceType, ResourceName",
		// Maestro readonly bundles are being phased out in favor of ReadDesires,
		// so empty results are acceptable. Query stays informational for older snapshots.
	},
	{
		component:    "hypershift",
		queryName:    "hostedClusterMetadata",
		templatePath: "queries/hypershift/hostedClusterMetadata/query.kql",
		database:     "service",
		category:     categoryResourceDiscovery,
		ready: func(d queryData) bool {
			return d.ResourceGroup != "" && d.ClusterResourceName != ""
		},
		prerequisites: "ResourceGroup, ClusterResourceName",
		requiredWhen:  func(d queryData) bool { return d.ClusterResourceName != "" },
		storeResult: func(d *queryData, rows []resultRow) error {
			if len(rows) > 1 {
				return fmt.Errorf("query returned %d distinct hosted cluster metadata rows, expected 1", len(rows))
			}
			d.HostedClusterNamespace = rows[0].values[0]
			d.HostedControlPlaneNamespace = rows[0].values[0] + "-" + rows[0].values[1]
			return nil
		},
	},

	// --- State: time-windowed, resource-scoped ---
	{
		component:    "backend",
		queryName:    "resourceState",
		templatePath: "queries/backend/resourceState/query.kql",
		database:     "service",
		category:     categoryState,
		ready: func(d queryData) bool {
			return d.ResourceID != ""
		},
		prerequisites: "ResourceID",
		requiredWhen:  isClusterOrNodePool,
	},
	{
		component:    "backend",
		queryName:    "resourceControllerConditionTimeline",
		templatePath: "queries/backend/resourceControllerConditionTimeline/query.kql",
		database:     "service",
		category:     categoryConditions,
		ready: func(d queryData) bool {
			return d.ResourceGroup != "" && d.ResourceType != "" && d.ResourceName != "" && d.ClusterResourceName != ""
		},
		prerequisites: "ResourceGroup, ResourceType, ResourceName, ClusterResourceName",
		requiredWhen:  isClusterOrNodePool,
	},
	{
		component:    "backend",
		queryName:    "resourceControllerConditions",
		templatePath: "queries/backend/resourceControllerConditions/query.kql",
		database:     "service",
		category:     categoryConditions,
		ready: func(d queryData) bool {
			return d.ResourceGroup != "" && d.ResourceType != "" && d.ResourceName != "" && d.ClusterResourceName != ""
		},
		prerequisites: "ResourceGroup, ResourceType, ResourceName, ClusterResourceName",
		requiredWhen:  isClusterOrNodePool,
	},
	{
		component:    "backend",
		queryName:    "serviceProviderState",
		templatePath: "queries/backend/serviceProviderState/query.kql",
		database:     "service",
		category:     categoryState,
		ready: func(d queryData) bool {
			return d.ResourceGroup != "" && d.ServiceProviderResourceType != "" && d.ResourceName != "" && d.ClusterResourceName != ""
		},
		prerequisites: "ResourceGroup, ServiceProviderResourceType, ResourceName, ClusterResourceName",
		requiredWhen:  func(d queryData) bool { return d.ServiceProviderResourceType != "" },
	},
	{
		component:    "clustersService",
		queryName:    "phases",
		templatePath: "queries/clustersService/phases/query.kql",
		database:     "service",
		category:     categoryState,
		ready: func(d queryData) bool {
			if d.ResourceID == "" {
				return false
			}
			rt := strings.ToLower(d.ResourceType)
			return rt == "microsoft.redhatopenshift/hcpopenshiftclusters" ||
				rt == "microsoft.redhatopenshift/hcpopenshiftclusters/nodepools"
		},
		prerequisites: "ResourceID, ResourceType is cluster or nodepool",
		requiredWhen:  isClusterOrNodePool,
	},
	{
		component:    "clustersService",
		queryName:    "clusterState",
		templatePath: "queries/clustersService/clusterState/query.kql",
		database:     "service",
		category:     categoryState,
		ready: func(d queryData) bool {
			return d.ResourceGroup != "" && d.ClusterResourceName != "" && strings.EqualFold(d.ResourceType, "microsoft.redhatopenshift/hcpopenshiftclusters")
		},
		prerequisites: "ResourceGroup, ResourceType is cluster",
		requiredWhen:  isClusterType,
	},
	{
		component:    "clustersService",
		queryName:    "nodePoolState",
		templatePath: "queries/clustersService/nodePoolState/query.kql",
		database:     "service",
		category:     categoryState,
		ready: func(d queryData) bool {
			return d.ResourceGroup != "" && d.ClusterResourceName != "" && d.ResourceID != "" && strings.EqualFold(d.ResourceType, "microsoft.redhatopenshift/hcpopenshiftclusters/nodepools")
		},
		prerequisites: "ResourceGroup, ClusterResourceName, ResourceID, ResourceType is nodepool",
		requiredWhen:  isNodePoolType,
	},

	{
		component:    "hypershift",
		queryName:    "hostedClusterConditionTimeline",
		templatePath: "queries/hypershift/hostedClusterConditionTimeline/query.kql",
		database:     "service",
		category:     categoryConditions,
		ready: func(d queryData) bool {
			return d.ResourceGroup != "" && d.ClusterResourceName != "" && strings.EqualFold(d.ResourceType, "microsoft.redhatopenshift/hcpopenshiftclusters")
		},
		prerequisites: "ResourceGroup, ResourceName, ResourceType is cluster",
		requiredWhen:  isClusterType,
	},
	{
		component:    "hypershift",
		queryName:    "hostedClusterConditions",
		templatePath: "queries/hypershift/hostedClusterConditions/query.kql",
		database:     "service",
		category:     categoryConditions,
		ready: func(d queryData) bool {
			return d.ResourceGroup != "" && d.ClusterResourceName != "" && strings.EqualFold(d.ResourceType, "microsoft.redhatopenshift/hcpopenshiftclusters")
		},
		prerequisites: "ResourceGroup, ResourceName, ResourceType is cluster",
		requiredWhen:  isClusterType,
	},
	{
		component:    "hypershift",
		queryName:    "nodePoolConditionTimeline",
		templatePath: "queries/hypershift/nodePoolConditionTimeline/query.kql",
		database:     "service",
		category:     categoryConditions,
		ready: func(d queryData) bool {
			return d.ResourceGroup != "" && d.ClusterResourceName != "" && d.ResourceName != "" && strings.EqualFold(d.ResourceType, "microsoft.redhatopenshift/hcpopenshiftclusters/nodepools")
		},
		prerequisites: "ResourceGroup, ClusterResourceName, ResourceName, ResourceType is nodepool",
		requiredWhen:  isNodePoolType,
	},
	{
		component:    "hypershift",
		queryName:    "nodePoolConditions",
		templatePath: "queries/hypershift/nodePoolConditions/query.kql",
		database:     "service",
		category:     categoryConditions,
		ready: func(d queryData) bool {
			return d.ResourceGroup != "" && d.ClusterResourceName != "" && d.ResourceName != "" && strings.EqualFold(d.ResourceType, "microsoft.redhatopenshift/hcpopenshiftclusters/nodepools")
		},
		prerequisites: "ResourceGroup, ClusterResourceName, ResourceName, ResourceType is nodepool",
		requiredWhen:  isNodePoolType,
	},
	{
		component:    "clustersService",
		queryName:    "logs",
		templatePath: "queries/clustersService/logs/query.kql",
		database:     "service",
		category:     categoryLogs,
		ready: func(d queryData) bool {
			if d.ResourceID == "" {
				return false
			}
			rt := strings.ToLower(d.ResourceType)
			return rt == "microsoft.redhatopenshift/hcpopenshiftclusters" ||
				rt == "microsoft.redhatopenshift/hcpopenshiftclusters/nodepools"
		},
		prerequisites: "ResourceID, ResourceType is cluster or nodepool",
		requiredWhen:  isClusterOrNodePool,
	},
	{
		component:    "clustersService",
		queryName:    "inflightChecks",
		templatePath: "queries/clustersService/inflightChecks/query.kql",
		database:     "service",
		category:     categoryLogs,
		ready: func(d queryData) bool {
			return d.ResourceID != "" && isClusterType(d)
		},
		prerequisites: "ResourceID, ResourceType is cluster",
		requiredWhen:  isClusterType,
	},
	{
		component:    "clustersService",
		queryName:    "provisionSteps",
		templatePath: "queries/clustersService/provisionSteps/query.kql",
		database:     "service",
		category:     categoryLogs,
		ready: func(d queryData) bool {
			return d.ResourceID != "" && isClusterType(d)
		},
		prerequisites: "ResourceID, ResourceType is cluster",
		requiredWhen:  isClusterType,
	},
	{
		component:    "maestro",
		queryName:    "serverLogs",
		templatePath: "queries/maestro/serverLogs/query.kql",
		database:     "service",
		category:     categoryLogs,
		ready: func(d queryData) bool {
			return d.ClusterID != "" || (d.ResourceGroup != "" && d.ClusterResourceName != "" && d.ServiceProviderResourceType != "")
		},
		prerequisites: "ClusterID, or ResourceGroup + ClusterResourceName + ServiceProviderResourceType",
		requiredWhen:  isClusterOrNodePool,
	},
	{
		component:    "maestro",
		queryName:    "agentLogs",
		templatePath: "queries/maestro/agentLogs/query.kql",
		database:     "service",
		category:     categoryLogs,
		ready: func(d queryData) bool {
			return d.ClusterID != "" || (d.ResourceGroup != "" && d.ClusterResourceName != "" && d.ServiceProviderResourceType != "")
		},
		prerequisites: "ClusterID, or ResourceGroup + ClusterResourceName + ServiceProviderResourceType",
		requiredWhen:  isClusterOrNodePool,
	},
	{
		component:    "maestro",
		queryName:    "mgmtAuditLogs",
		templatePath: "queries/maestro/mgmtAuditLogs/query.kql",
		database:     "service",
		category:     categoryLogs,
		ready: func(d queryData) bool {
			return d.ClusterID != "" || (d.ResourceGroup != "" && d.ClusterResourceName != "" && d.ServiceProviderResourceType != "")
		},
		prerequisites: "ClusterID, or ResourceGroup + ClusterResourceName + ServiceProviderResourceType",
	},
	{
		component:    "maestro",
		queryName:    "transitions",
		templatePath: "queries/maestro/transitions/query.kql",
		database:     "service",
		category:     categoryState,
		ready: func(d queryData) bool {
			return d.ClusterID != "" || (d.ResourceGroup != "" && d.ClusterResourceName != "" && d.ServiceProviderResourceType != "")
		},
		prerequisites: "ClusterID, or ResourceGroup + ClusterResourceName + ServiceProviderResourceType",
		requiredWhen:  isClusterOrNodePool,
	},

	// --- mgmt-agent: pod lifecycle events from PodWatcher ---
	{
		component:    "mgmtAgent",
		queryName:    "podEvents",
		templatePath: "queries/mgmtAgent/podEvents/query.kql",
		database:     "service",
		category:     categoryResourceEvents,
		ready: func(d queryData) bool {
			return d.ClusterID != "" && strings.EqualFold(d.ResourceType, "microsoft.redhatopenshift/hcpopenshiftclusters")
		},
		prerequisites: "ClusterID, ResourceType is cluster",
	},
	{
		component:    "mgmtAgent",
		queryName:    "podEvictions",
		templatePath: "queries/mgmtAgent/podEvictions/query.kql",
		database:     "service",
		category:     categoryResourceEvents,
		ready: func(d queryData) bool {
			return d.ClusterID != "" && strings.EqualFold(d.ResourceType, "microsoft.redhatopenshift/hcpopenshiftclusters")
		},
		prerequisites: "ClusterID, ResourceType is cluster",
	},

	// --- Events: time-windowed, component-scoped ---
	{
		component:    "frontend",
		queryName:    "events",
		templatePath: "queries/frontend/events/query.kql",
		database:     "service",
		category:     categoryEvents,
	},
	{
		component:    "backend",
		queryName:    "events",
		templatePath: "queries/backend/events/query.kql",
		database:     "service",
		category:     categoryEvents,
	},
	{
		component:    "clustersService",
		queryName:    "events",
		templatePath: "queries/clustersService/events/query.kql",
		database:     "service",
		category:     categoryEvents,
	},
	{
		component:    "maestro",
		queryName:    "events",
		templatePath: "queries/maestro/events/query.kql",
		database:     "service",
		category:     categoryEvents,
	},
	{
		component:    "hypershift",
		queryName:    "events",
		templatePath: "queries/hypershift/events/query.kql",
		database:     "service",
		category:     categoryResourceEvents,
		ready: func(d queryData) bool {
			return d.HostedControlPlaneNamespace != "" && strings.EqualFold(d.ResourceType, "microsoft.redhatopenshift/hcpopenshiftclusters")
		},
		prerequisites: "HostedControlPlaneNamespace, ResourceType is cluster",
	},
	{
		component:    "hypershift",
		queryName:    "controlPlaneEvents",
		templatePath: "queries/hypershift/controlPlaneEvents/query.kql",
		database:     "service",
		category:     categoryResourceEvents,
		ready: func(d queryData) bool {
			return d.HostedControlPlaneNamespace != "" && strings.EqualFold(d.ResourceType, "microsoft.redhatopenshift/hcpopenshiftclusters")
		},
		prerequisites: "HostedControlPlaneNamespace, ResourceType is cluster",
	},
	{
		component:    "hypershift",
		queryName:    "pkiOperatorEvents",
		templatePath: "queries/hypershift/pkiOperatorEvents/query.kql",
		database:     "service",
		category:     categoryResourceEvents,
		ready: func(d queryData) bool {
			return d.HostedControlPlaneNamespace != "" && strings.EqualFold(d.ResourceType, "microsoft.redhatopenshift/hcpopenshiftclusters/requestadmincredential")
		},
		prerequisites: "HostedControlPlaneNamespace, ResourceType is requestAdminCredential",
	},
	{
		component:    "hypershift",
		queryName:    "hypershiftOperatorLogs",
		templatePath: "queries/hypershift/hypershiftOperatorLogs/query.kql",
		database:     "service",
		category:     categoryLogs,
		ready: func(d queryData) bool {
			return d.HostedClusterNamespace != "" && (strings.EqualFold(d.ResourceType, "microsoft.redhatopenshift/hcpopenshiftclusters") || strings.EqualFold(d.ResourceType, "microsoft.redhatopenshift/hcpopenshiftclusters/nodepools"))
		},
		prerequisites: "HostedClusterNamespace, ResourceType is cluster or nodepool",
	},
	{
		component:    "hypershift",
		queryName:    "controlPlaneOperatorLogs",
		templatePath: "queries/hypershift/controlPlaneOperatorLogs/query.kql",
		database:     "hcp",
		category:     categoryLogs,
		ready: func(d queryData) bool {
			return d.HostedControlPlaneNamespace != "" && strings.EqualFold(d.ResourceType, "microsoft.redhatopenshift/hcpopenshiftclusters")
		},
		prerequisites: "HostedControlPlaneNamespace, ResourceType is cluster",
	},
	{
		component:    "hypershift",
		queryName:    "clusterAPILogs",
		templatePath: "queries/hypershift/clusterAPILogs/query.kql",
		database:     "hcp",
		category:     categoryLogs,
		ready: func(d queryData) bool {
			return d.HostedControlPlaneNamespace != "" && strings.EqualFold(d.ResourceType, "microsoft.redhatopenshift/hcpopenshiftclusters")
		},
		prerequisites: "HostedControlPlaneNamespace, ResourceType is cluster",
	},
	{
		component:    "hypershift",
		queryName:    "clusterAPIProviderLogs",
		templatePath: "queries/hypershift/clusterAPIProviderLogs/query.kql",
		database:     "hcp",
		category:     categoryLogs,
		ready: func(d queryData) bool {
			return d.HostedControlPlaneNamespace != "" && strings.EqualFold(d.ResourceType, "microsoft.redhatopenshift/hcpopenshiftclusters")
		},
		prerequisites: "HostedControlPlaneNamespace, ResourceType is cluster",
	},
}

// contextQueries are broader queries not tied to a specific correlation ID.
// They discover all frontend requests in the resource group during the time window.
// These are written directly to the top-level output directory.
var contextQueries = []querySpec{
	{
		component:    "frontend",
		queryName:    "frontendRequests",
		templatePath: "queries/frontend/frontendRequests/query.kql",
		database:     "service",
		category:     categoryContext,
	},
}

// queriesByCategory returns the subset of allQueries matching the given category.
func queriesByCategory(cat queryCategory) []querySpec {
	var result []querySpec
	for _, q := range allQueries {
		if q.category == cat {
			result = append(result, q)
		}
	}
	return result
}

// kqlDatetime formats a time.Time as a KQL datetime literal.
func kqlDatetime(t time.Time) string {
	return fmt.Sprintf("datetime(%s)", t.UTC().Format(time.RFC3339))
}

// templateFuncMap provides template functions available to KQL templates.
var templateFuncMap = template.FuncMap{
	"kqlDatetime": kqlDatetime,
	"toLower":     strings.ToLower,
}

// renderQuery reads and renders an embedded KQL template with the given data.
// Shared partial templates from queries/_partials/ are automatically available.
func renderQuery(templatePath string, data queryData) (string, error) {
	tmplBytes, err := queriesFS.ReadFile(templatePath)
	if err != nil {
		return "", fmt.Errorf("failed to read embedded query %s: %w", templatePath, err)
	}
	tmpl, err := template.New(templatePath).Funcs(templateFuncMap).Parse(string(tmplBytes))
	if err != nil {
		return "", fmt.Errorf("failed to parse query template %s: %w", templatePath, err)
	}
	// Parse shared partial templates so queries can use {{ template "bundleMap" . }} etc.
	partials, err := fs.Glob(queriesFS, "queries/partials/*.kql")
	if err != nil {
		return "", fmt.Errorf("failed to glob partial templates: %w", err)
	}
	for _, p := range partials {
		pBytes, err := queriesFS.ReadFile(p)
		if err != nil {
			return "", fmt.Errorf("failed to read partial %s: %w", p, err)
		}
		if _, err := tmpl.Parse(string(pBytes)); err != nil {
			return "", fmt.Errorf("failed to parse partial %s: %w", p, err)
		}
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render query template %s: %w", templatePath, err)
	}
	return buf.String(), nil
}
