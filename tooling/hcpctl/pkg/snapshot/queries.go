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
	"strings"
	"text/template"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

//go:embed queries
var queriesFS embed.FS

// queryCategory determines where a query's output is written and how it is deduplicated.
type queryCategory string

const (
	// categoryContext queries are time-windowed and resource-group-scoped. They run once
	// per snapshot and are written to context/<component>/<queryName>/.
	categoryContext queryCategory = "context"

	// categoryRequestDiscovery queries depend on a specific correlation ID. They run
	// per-request and are written to resources/<type>/<name>/requests/<correlationID>/discovery/<component>/<queryName>/.
	categoryRequestDiscovery queryCategory = "requestDiscovery"

	// categoryResourceDiscovery queries depend on data discovered per-request but produce
	// results that are stable per-resource. They run once per unique resource and are
	// written to resources/<type>/<name>/discovery/<component>/<queryName>/.
	categoryResourceDiscovery queryCategory = "resourceDiscovery"

	// categoryState queries are time-windowed and resource-scoped. They run once per
	// unique resource and are written to resources/<type>/<name>/state/<component>/<queryName>/.
	categoryState queryCategory = "state"

	// categoryTrace queries are per-request and depend on request-specific data
	// (e.g. AsyncOperationId). They are written to
	// resources/<type>/<name>/requests/<correlationID>/<component>/<queryName>/.
	categoryTrace queryCategory = "trace"
)

// querySpec describes a single KQL query in the dependency chain.
type querySpec struct {
	// component is the top-level directory name (e.g. "frontend").
	component string
	// queryName is the sub-directory name (e.g. "asyncOperationId").
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
	// It stores discovered values for downstream queries.
	storeResult func(*queryData, []resultRow)
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
	StartTime       time.Time
	EndTime         time.Time
	ResourceGroup   string

	// Per-request context — set when tracing a specific correlation ID.
	CorrelationID string

	// Discovered by queries.
	ResourceID                  string
	ResourceType                string
	ResourceName                string
	ClusterResourceName         string
	ServiceProviderResourceType string
	AsyncOperationId            string
	AsyncOperationPath          string
	InternalID                  string
	ClusterID                   string
	BundleIDs                   []string
	BundleNames                 []string
	ManifestWorkNames           []string
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

// allQueries defines the complete ordered query chain. Each query has a category
// that determines where its output is written and whether it is deduplicated
// across requests for the same resource. Queries are executed in order within
// each phase of the gathering pipeline.
var allQueries = []querySpec{
	// --- Request discovery: per correlation ID ---
	{
		component:    "frontend",
		queryName:    "resourceId",
		templatePath: "queries/frontend/resourceId/query.kql",
		database:     "service",
		category:     categoryRequestDiscovery,
		requiredWhen: func(d queryData) bool { return d.CorrelationID != "" },
		storeResult: func(d *queryData, rows []resultRow) {
			d.ResourceID = rows[0].values[0]
			parsed, err := azcorearm.ParseResourceID(rows[0].values[0])
			if err != nil {
				return
			}
			d.ResourceGroup = parsed.ResourceGroupName
			d.ResourceType = parsed.ResourceType.String()
			d.ResourceName = parsed.Name
			// Walk up the resource ID to find the HCP cluster name.
			for r := parsed; r != nil; r = r.Parent {
				if strings.EqualFold(r.ResourceType.Type, "hcpopenshiftclusters") {
					d.ClusterResourceName = r.Name
					break
				}
			}
			d.ServiceProviderResourceType = serviceProviderResourceType(d.ResourceType)
		},
	},
	{
		component:    "frontend",
		queryName:    "asyncOperationId",
		templatePath: "queries/frontend/asyncOperationId/query.kql",
		database:     "service",
		category:     categoryRequestDiscovery,
		requiredWhen: func(d queryData) bool { return d.CorrelationID != "" },
		storeResult: func(d *queryData, rows []resultRow) {
			d.AsyncOperationId = rows[0].values[0]
		},
	},
	{
		component:    "frontend",
		queryName:    "asyncOperationPath",
		templatePath: "queries/frontend/asyncOperationPath/query.kql",
		database:     "service",
		category:     categoryRequestDiscovery,
		requiredWhen: func(d queryData) bool { return d.CorrelationID != "" },
		storeResult: func(d *queryData, rows []resultRow) {
			d.AsyncOperationPath = rows[0].values[0]
		},
	},

	// --- Trace: per-request, depends on request-specific data ---
	{
		component:    "frontend",
		queryName:    "asyncOperationRequests",
		templatePath: "queries/frontend/asyncOperationRequests/query.kql",
		database:     "service",
		category:     categoryTrace,
		ready: func(d queryData) bool {
			return d.AsyncOperationPath != ""
		},
		prerequisites: "AsyncOperationPath",
		requiredWhen:  func(d queryData) bool { return d.AsyncOperationPath != "" },
	},
	{
		component:    "backend",
		queryName:    "asyncOperationState",
		templatePath: "queries/backend/asyncOperationState/query.kql",
		database:     "service",
		category:     categoryTrace,
		ready: func(d queryData) bool {
			return d.AsyncOperationId != ""
		},
		prerequisites: "AsyncOperationId",
		requiredWhen:  func(d queryData) bool { return d.AsyncOperationId != "" },
	},

	// --- Resource discovery: stable per-resource ---
	{
		component:    "backend",
		queryName:    "resourceInternalId",
		templatePath: "queries/backend/resourceInternalId/query.kql",
		database:     "service",
		category:     categoryResourceDiscovery,
		ready: func(d queryData) bool {
			return d.ResourceGroup != "" && d.ResourceType != ""
		},
		prerequisites: "ResourceGroup, ResourceType",
		requiredWhen:  isClusterOrNodePool,
		storeResult: func(d *queryData, rows []resultRow) {
			d.InternalID = rows[0].values[0]
		},
	},
	{
		component:    "clustersService",
		queryName:    "cid",
		templatePath: "queries/clustersService/cid/query.kql",
		database:     "service",
		category:     categoryResourceDiscovery,
		ready: func(d queryData) bool {
			return d.ResourceID != ""
		},
		prerequisites: "ResourceID",
		requiredWhen:  isClusterType,
		storeResult: func(d *queryData, rows []resultRow) {
			d.ClusterID = rows[0].values[0]
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
		requiredWhen:  func(d queryData) bool { return d.ClusterID != "" },
		storeResult: func(d *queryData, rows []resultRow) {
			// Columns: bundleId, bundleName, manifestWork
			for _, row := range rows {
				if len(row.values) >= 3 {
					if id := strings.TrimSpace(row.values[0]); id != "" {
						d.BundleIDs = append(d.BundleIDs, id)
					}
					if name := strings.TrimSpace(row.values[1]); name != "" {
						d.BundleNames = append(d.BundleNames, name)
					}
					if mw := strings.TrimSpace(row.values[2]); mw != "" {
						d.ManifestWorkNames = append(d.ManifestWorkNames, mw)
					}
				}
			}
		},
	},
	{
		component:    "hypershift",
		queryName:    "hostedClusterMetadata",
		templatePath: "queries/hypershift/hostedClusterMetadata/query.kql",
		database:     "service",
		category:     categoryResourceDiscovery,
		ready: func(d queryData) bool {
			return d.ResourceGroup != "" && d.ResourceName != "" && strings.EqualFold(d.ResourceType, "microsoft.redhatopenshift/hcpopenshiftclusters")
		},
		prerequisites: "ResourceGroup, ResourceName, ResourceType is cluster",
		requiredWhen:  isClusterType,
		storeResult: func(d *queryData, rows []resultRow) {
			d.HostedClusterNamespace = rows[0].values[0]
			d.HostedControlPlaneNamespace = rows[0].values[0] + "-" + rows[0].values[1]
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
		queryName:    "resourceControllerConditions",
		templatePath: "queries/backend/resourceControllerConditions/query.kql",
		database:     "service",
		category:     categoryState,
		ready: func(d queryData) bool {
			return d.ResourceGroup != "" && d.ResourceType != ""
		},
		prerequisites: "ResourceGroup, ResourceType",
		requiredWhen:  isClusterOrNodePool,
	},
	{
		component:    "backend",
		queryName:    "serviceProviderState",
		templatePath: "queries/backend/serviceProviderState/query.kql",
		database:     "service",
		category:     categoryState,
		ready: func(d queryData) bool {
			return d.ResourceGroup != "" && d.ServiceProviderResourceType != ""
		},
		prerequisites: "ResourceGroup, ServiceProviderResourceType",
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
			return d.ResourceGroup != "" && strings.EqualFold(d.ResourceType, "microsoft.redhatopenshift/hcpopenshiftclusters")
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
			return d.ResourceGroup != "" && d.ClusterResourceName != "" && strings.EqualFold(d.ResourceType, "microsoft.redhatopenshift/hcpopenshiftclusters/nodepools")
		},
		prerequisites: "ResourceGroup, ClusterResourceName, ResourceType is nodepool",
		requiredWhen:  isNodePoolType,
	},
	{
		component:    "clustersService",
		queryName:    "maestroInteractions",
		templatePath: "queries/clustersService/maestroInteractions/query.kql",
		database:     "service",
		category:     categoryState,
		ready: func(d queryData) bool {
			return d.ClusterID != ""
		},
		prerequisites: "ClusterID",
		requiredWhen:  func(d queryData) bool { return d.ClusterID != "" },
	},
	{
		component:    "hypershift",
		queryName:    "hostedClusterConditions",
		templatePath: "queries/hypershift/hostedClusterConditions/query.kql",
		database:     "service",
		category:     categoryState,
		ready: func(d queryData) bool {
			return d.ResourceGroup != "" && d.ResourceName != "" && strings.EqualFold(d.ResourceType, "microsoft.redhatopenshift/hcpopenshiftclusters")
		},
		prerequisites: "ResourceGroup, ResourceName, ResourceType is cluster",
		requiredWhen:  isClusterType,
	},
	{
		component:    "hypershift",
		queryName:    "nodePoolConditions",
		templatePath: "queries/hypershift/nodePoolConditions/query.kql",
		database:     "service",
		category:     categoryState,
		ready: func(d queryData) bool {
			return d.ResourceGroup != "" && d.ClusterResourceName != "" && strings.EqualFold(d.ResourceType, "microsoft.redhatopenshift/hcpopenshiftclusters/nodepools")
		},
		prerequisites: "ResourceGroup, ClusterResourceName, ResourceType is nodepool",
		requiredWhen:  isNodePoolType,
	},
	{
		component:    "maestro",
		queryName:    "serverLogs",
		templatePath: "queries/maestro/serverLogs/query.kql",
		database:     "service",
		category:     categoryState,
		ready: func(d queryData) bool {
			return len(d.BundleIDs) > 0
		},
		prerequisites: "BundleIDs",
		requiredWhen:  func(d queryData) bool { return len(d.BundleIDs) > 0 },
	},
	{
		component:    "maestro",
		queryName:    "agentLogs",
		templatePath: "queries/maestro/agentLogs/query.kql",
		database:     "service",
		category:     categoryState,
		ready: func(d queryData) bool {
			return len(d.BundleIDs) > 0
		},
		prerequisites: "BundleIDs",
		requiredWhen:  func(d queryData) bool { return len(d.BundleIDs) > 0 },
	},
	{
		component:    "maestro",
		queryName:    "mgmtAuditLogs",
		templatePath: "queries/maestro/mgmtAuditLogs/query.kql",
		database:     "service",
		category:     categoryState,
		ready: func(d queryData) bool {
			return len(d.ManifestWorkNames) > 0
		},
		prerequisites: "ManifestWorkNames",
	},

	// --- Context: time-windowed, resource-group-scoped ---
	{
		component:    "frontend",
		queryName:    "events",
		templatePath: "queries/frontend/events/query.kql",
		database:     "service",
		category:     categoryContext,
	},
	{
		component:    "backend",
		queryName:    "events",
		templatePath: "queries/backend/events/query.kql",
		database:     "service",
		category:     categoryContext,
	},
	{
		component:    "clustersService",
		queryName:    "events",
		templatePath: "queries/clustersService/events/query.kql",
		database:     "service",
		category:     categoryContext,
	},
	{
		component:    "maestro",
		queryName:    "events",
		templatePath: "queries/maestro/events/query.kql",
		database:     "service",
		category:     categoryContext,
	},
	{
		component:    "hypershift",
		queryName:    "events",
		templatePath: "queries/hypershift/events/query.kql",
		database:     "service",
		category:     categoryContext,
		ready: func(d queryData) bool {
			return d.HostedControlPlaneNamespace != ""
		},
		prerequisites: "HostedControlPlaneNamespace",
	},
	{
		component:    "hypershift",
		queryName:    "controlPlaneEvents",
		templatePath: "queries/hypershift/controlPlaneEvents/query.kql",
		database:     "service",
		category:     categoryContext,
		ready: func(d queryData) bool {
			return d.HostedControlPlaneNamespace != ""
		},
		prerequisites: "HostedControlPlaneNamespace",
	},
	{
		component:    "hypershift",
		queryName:    "pkiOperatorEvents",
		templatePath: "queries/hypershift/pkiOperatorEvents/query.kql",
		database:     "service",
		category:     categoryContext,
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
		category:     categoryContext,
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
		category:     categoryContext,
		ready: func(d queryData) bool {
			return d.HostedControlPlaneNamespace != "" && strings.EqualFold(d.ResourceType, "microsoft.redhatopenshift/hcpopenshiftclusters")
		},
		prerequisites: "HostedControlPlaneNamespace, ResourceType is cluster",
	},
}

// contextQueries are broader queries not tied to a specific correlation ID.
// They discover all frontend requests in the resource group during the time window.
var contextQueries = []querySpec{
	{
		component:    "context",
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
	"manifestWorkName": func(s string) string {
		if i := strings.LastIndex(s, "/"); i >= 0 {
			return s[i+1:]
		}
		return s
	},
	"manifestWorkNamespace": func(s string) string {
		if i := strings.LastIndex(s, "/"); i >= 0 {
			return s[:i]
		}
		return s
	},
}

// renderQuery reads and renders an embedded KQL template with the given data.
func renderQuery(templatePath string, data queryData) (string, error) {
	tmplBytes, err := queriesFS.ReadFile(templatePath)
	if err != nil {
		return "", fmt.Errorf("failed to read embedded query %s: %w", templatePath, err)
	}
	tmpl, err := template.New(templatePath).Funcs(templateFuncMap).Parse(string(tmplBytes))
	if err != nil {
		return "", fmt.Errorf("failed to parse query template %s: %w", templatePath, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render query template %s: %w", templatePath, err)
	}
	return buf.String(), nil
}
