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
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/azure-kusto-go/azkustodata"
	"github.com/Azure/azure-kusto-go/azkustodata/kql"
	azkquery "github.com/Azure/azure-kusto-go/azkustodata/query"
)

// GatherInput provides the parameters needed to gather a diagnostic snapshot.
type GatherInput struct {
	// ClusterURI is the full Kusto cluster endpoint URL.
	ClusterURI string
	// ServiceDatabase is the Kusto database containing service logs.
	ServiceDatabase string
	// HCPDatabase is the Kusto database containing hosted control plane logs.
	HCPDatabase string
	// ResourceGroup is the Azure resource group to scope queries to.
	ResourceGroup string
	// TimeWindow is the time range to query.
	TimeWindow TimeWindow
	// QueryTimeout is the timeout for individual Kusto queries.
	QueryTimeout time.Duration
}

// Gatherer produces structured diagnostic data directories by running
// a dependency chain of Kusto queries against Azure Data Explorer.
type Gatherer struct {
	client *azkustodata.Client
}

// NewGatherer creates a new Gatherer with the given Kusto SDK client.
func NewGatherer(client *azkustodata.Client) *Gatherer {
	return &Gatherer{client: client}
}

// resourceKey returns the deduplication key for a resource.
func resourceKey(data queryData) string {
	if data.ResourceType == "" {
		return ""
	}
	return sanitizePath(data.ResourceType) + "/" + sanitizePath(data.ResourceName)
}

// resourceDir returns the output directory for a resource within the snapshot.
// When resource type is unknown, correlationID is used to produce a unique path.
func resourceDir(outputDir string, data queryData, correlationID string) string {
	if data.ResourceType == "" {
		return filepath.Join(outputDir, "resources", "unknown", sanitizePath(correlationID))
	}
	return filepath.Join(outputDir, "resources", sanitizePath(data.ResourceType), sanitizePath(data.ResourceName))
}

// trackedRequest holds per-request discovery data and the original frontend request.
type trackedRequest struct {
	req           frontendRequest
	data          queryData
	cachedResults map[string][]resultRow // keyed by querySpec.key()
}

// recordVerification checks if a query requires results and records the appropriate case.
func recordVerification(report *VerificationReport, suite string, q querySpec, data queryData, rowCount int, skipped bool) {
	if q.requiredWhen == nil {
		return
	}

	// Render KQL for downstream consumers (e.g. HTML overview).
	var renderedKQL string
	if rendered, err := renderQuery(q.templatePath, data); err == nil {
		renderedKQL = rendered
	}

	c := VerificationCase{
		Suite:        suite,
		Query:        q.key(),
		Category:     string(q.category),
		ResourceType: data.ResourceType,
		RenderedKQL:  renderedKQL,
	}
	if skipped {
		c.Status = VerificationSkipped
		c.Message = "prerequisites not met: " + q.prerequisites
		report.Record(c)
		return
	}
	if q.requiredWhen(data) {
		if rowCount > 0 {
			c.Status = VerificationPass
		} else {
			c.Status = VerificationFail
			c.Message = fmt.Sprintf("Expected results but got 0 rows. Prerequisites: %s", q.prerequisites)
		}
		report.Record(c)
	}
}

// requestDiscoveryRowCount infers whether a request discovery query produced results
// by checking if the field it would have populated is non-empty in the data.
func requestDiscoveryRowCount(q querySpec, data queryData) int {
	switch q.queryName {
	case "resourceId":
		if data.ResourceID != "" {
			return 1
		}
	case "asyncOperationId":
		if data.AsyncOperationId != "" {
			return 1
		}
	case "asyncOperationPath":
		if data.AsyncOperationPath != "" {
			return 1
		}
	}
	return 0
}

// Gather runs the full diagnostic data gathering pipeline for a resource group
// and writes structured output to outputDir.
func (g *Gatherer) Gather(ctx context.Context, input GatherInput, outputDir string) (*Manifest, *VerificationReport, error) {
	logger := logr.FromContextOrDiscard(ctx)
	report := &VerificationReport{}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	seedData := queryData{
		ClusterURI:      input.ClusterURI,
		ServiceDatabase: input.ServiceDatabase,
		HCPDatabase:     input.HCPDatabase,
		StartTime:       input.TimeWindow.Start,
		EndTime:         input.TimeWindow.End,
		ResourceGroup:   input.ResourceGroup,
	}

	// Phase 1: Run initial context queries (frontendRequests) to discover all ARM requests.
	contextDir := filepath.Join(outputDir, "context")
	for _, q := range contextQueries {
		if _, err := g.executeQuery(ctx, q, &seedData, contextDir, input); err != nil {
			return nil, nil, fmt.Errorf("context query %s failed: %w", q.key(), err)
		}
	}

	// Discover all ARM requests from the frontend request data.
	requests, err := g.discoverRequests(ctx, input, seedData)
	if err != nil {
		logger.Error(err, "Failed to discover requests, continuing with empty request list")
		requests = nil
	}

	manifest := &Manifest{
		TimeWindow:      input.TimeWindow,
		ResourceGroup:   input.ResourceGroup,
		KustoCluster:    input.ClusterURI,
		KustoDatabase:   input.ServiceDatabase,
		DirectoryLayout: directoryLayout(),
	}

	// Phase 2: Per-request discovery — run requestDiscovery queries for each mutating request.
	var trackedRequests []trackedRequest
	for _, req := range requests {
		isMutating := req.Method == "PUT" || req.Method == "POST" || req.Method == "PATCH" || req.Method == "DELETE"
		if !isMutating {
			continue
		}

		logger.Info("Running request discovery", "correlationId", req.CorrelationID, "method", req.Method, "path", req.Path)

		reqData := seedData // copy seed values
		reqData.CorrelationID = req.CorrelationID

		cachedResults := make(map[string][]resultRow)

		// Run request discovery queries. We don't know the resource yet, so we
		// write to a temporary location and move later once we know the resource.
		for _, q := range queriesByCategory(categoryRequestDiscovery) {
			if q.ready != nil && !q.ready(reqData) {
				logger.Info("Skipping request discovery query (prerequisites not met)", "query", q.key(), "prerequisites", q.prerequisites)
				continue
			}

			results, err := g.executeQueryToDir(ctx, q, &reqData, "", input)
			if err != nil {
				logger.Error(err, "Request discovery query failed, continuing", "query", q.key())
				continue
			}
			cachedResults[q.key()] = results
			if q.storeResult != nil && len(results) > 0 {
				q.storeResult(&reqData, results)
			}
		}

		trackedRequests = append(trackedRequests, trackedRequest{req: req, data: reqData, cachedResults: cachedResults})
	}

	// Phase 3: Resource discovery + state — deduplicated per resource.
	// Build a map of unique resources and pick the richest queryData for each.
	type resourceState struct {
		data     queryData
		requests []trackedRequest
	}
	resources := make(map[string]*resourceState)
	// Track insertion order so output is deterministic.
	var resourceOrder []string

	for i := range trackedRequests {
		tr := &trackedRequests[i]
		key := resourceKey(tr.data)
		if key == "" {
			key = "unknown/" + tr.req.CorrelationID
		}
		if _, ok := resources[key]; !ok {
			resources[key] = &resourceState{data: tr.data}
			resourceOrder = append(resourceOrder, key)
		}
		resources[key].requests = append(resources[key].requests, *tr)
	}

	for _, key := range resourceOrder {
		rs := resources[key]
		// Use the first request's correlation ID as a fallback identifier
		// when resource type/name are unknown (discovery failed).
		var fallbackID string
		if len(rs.requests) > 0 {
			fallbackID = rs.requests[0].req.CorrelationID
		}
		resDir := resourceDir(outputDir, rs.data, fallbackID)

		logger.Info("Running resource discovery and state queries", "resource", key)

		// Run resource discovery queries.
		discoveryDir := filepath.Join(resDir, "discovery")
		var skipped []skippedQuery
		for _, q := range queriesByCategory(categoryResourceDiscovery) {
			if q.ready != nil && !q.ready(rs.data) {
				logger.Info("Skipping resource discovery query (prerequisites not met)", "query", q.key(), "prerequisites", q.prerequisites)
				skipped = append(skipped, skippedQuery{key: q.key(), prerequisites: q.prerequisites})
				recordVerification(report, key, q, rs.data, 0, true)
				continue
			}

			results, err := g.executeQuery(ctx, q, &rs.data, discoveryDir, input)
			if err != nil {
				logger.Error(err, "Resource discovery query failed, continuing", "query", q.key())
				continue
			}
			recordVerification(report, key, q, rs.data, len(results), false)
			if q.storeResult != nil && len(results) > 0 {
				q.storeResult(&rs.data, results)
			}
		}

		// Run state queries.
		stateDir := filepath.Join(resDir, "state")
		for _, q := range queriesByCategory(categoryState) {
			if q.ready != nil && !q.ready(rs.data) {
				logger.Info("Skipping state query (prerequisites not met)", "query", q.key(), "prerequisites", q.prerequisites)
				skipped = append(skipped, skippedQuery{key: q.key(), prerequisites: q.prerequisites})
				recordVerification(report, key, q, rs.data, 0, true)
				continue
			}

			results, err := g.executeQuery(ctx, q, &rs.data, stateDir, input)
			if err != nil {
				logger.Error(err, "State query failed, continuing", "query", q.key())
				continue
			}
			recordVerification(report, key, q, rs.data, len(results), false)
			if q.storeResult != nil && len(results) > 0 {
				q.storeResult(&rs.data, results)
			}
		}

		// Phase 4: Per-request trace queries + write request discovery output.
		for _, tr := range rs.requests {
			reqDir := filepath.Join(resDir, "requests", tr.req.CorrelationID)
			reqSuite := key + "/" + tr.req.CorrelationID

			// Write request discovery output now that we know the resource directory.
			reqDiscoveryDir := filepath.Join(reqDir, "discovery")
			traceData := tr.data
			// Merge resource-level discoveries into the per-request data so trace
			// queries have access to ClusterID, BundleIDs, HostedClusterNamespace, etc.
			mergeResourceData(&traceData, rs.data)

			for _, q := range queriesByCategory(categoryRequestDiscovery) {
				if q.ready != nil && !q.ready(traceData) {
					recordVerification(report, reqSuite, q, traceData, 0, true)
					continue
				}
				// Write cached results from Phase 2 without re-executing the query.
				cached := tr.cachedResults[q.key()]
				if err := writeQueryOutput(q, traceData, reqDiscoveryDir, cached); err != nil {
					logger.Error(err, "Failed to write request discovery output", "query", q.key())
				}
				// For verification, check if discovery already found results (stored in traceData).
				rowCount := requestDiscoveryRowCount(q, traceData)
				recordVerification(report, reqSuite, q, traceData, rowCount, false)
			}

			// Run trace queries.
			for _, q := range queriesByCategory(categoryTrace) {
				if q.ready != nil && !q.ready(traceData) {
					logger.Info("Skipping trace query (prerequisites not met)", "query", q.key(), "prerequisites", q.prerequisites)
					recordVerification(report, reqSuite, q, traceData, 0, true)
					continue
				}

				results, err := g.executeQuery(ctx, q, &traceData, reqDir, input)
				if err != nil {
					logger.Error(err, "Trace query failed, continuing", "query", q.key())
					continue
				}
				recordVerification(report, reqSuite, q, traceData, len(results), false)
				if q.storeResult != nil && len(results) > 0 {
					q.storeResult(&traceData, results)
				}
			}
		}

		// Run context queries that need resource-level data (e.g. hypershift events
		// need HostedControlPlaneNamespace). These are deduplicated per resource since context
		// queries with prerequisites will produce the same output for the same resource.
		for _, q := range queriesByCategory(categoryContext) {
			if q.ready != nil && !q.ready(rs.data) {
				logger.Info("Skipping context query (prerequisites not met)", "query", q.key(), "prerequisites", q.prerequisites)
				skipped = append(skipped, skippedQuery{key: q.key(), prerequisites: q.prerequisites})
				continue
			}
			if _, err := g.executeQuery(ctx, q, &rs.data, contextDir, input); err != nil {
				logger.Error(err, "Context query failed, continuing", "query", q.key())
			}
		}

		// Write per-resource SUMMARY.md.
		if err := writeResourceSummary(resDir, rs.data, rs.requests, skipped); err != nil {
			logger.Error(err, "Failed to write resource summary")
		}

		// Record in manifest — skip resources where we never discovered a type.
		if rs.data.ResourceType == "" {
			logger.Info("Skipping manifest entry for unknown resource", "key", key)
			continue
		}
		relDir, _ := filepath.Rel(outputDir, resDir)
		manifest.Resources = append(manifest.Resources, ResourceEntry{
			Type:                        rs.data.ResourceType,
			Name:                        rs.data.ResourceName,
			Dir:                         relDir,
			ResourceID:                  rs.data.ResourceID,
			ClusterResourceName:         rs.data.ClusterResourceName,
			InternalID:                  rs.data.InternalID,
			ClusterID:                   rs.data.ClusterID,
			HostedClusterNamespace:      rs.data.HostedClusterNamespace,
			HostedControlPlaneNamespace: rs.data.HostedControlPlaneNamespace,
			BundleIDs:                   rs.data.BundleIDs,
			ManifestWorkNames:           rs.data.ManifestWorkNames,
		})
	}

	if err := WriteManifest(outputDir, manifest); err != nil {
		return nil, nil, err
	}

	return manifest, report, nil
}

// mergeResourceData copies resource-level discovered fields into a per-request
// queryData so that trace queries have access to data from resource discovery.
func mergeResourceData(dst *queryData, src queryData) {
	if dst.InternalID == "" {
		dst.InternalID = src.InternalID
	}
	if dst.ClusterID == "" {
		dst.ClusterID = src.ClusterID
	}
	if len(dst.BundleIDs) == 0 {
		dst.BundleIDs = src.BundleIDs
	}
	if len(dst.BundleNames) == 0 {
		dst.BundleNames = src.BundleNames
	}
	if len(dst.ManifestWorkNames) == 0 {
		dst.ManifestWorkNames = src.ManifestWorkNames
	}
	if dst.HostedClusterNamespace == "" {
		dst.HostedClusterNamespace = src.HostedClusterNamespace
	}
	if dst.HostedControlPlaneNamespace == "" {
		dst.HostedControlPlaneNamespace = src.HostedControlPlaneNamespace
	}
}

// frontendRequest represents a single ARM request discovered in frontend logs.
type frontendRequest struct {
	CorrelationID string
	Method        string
	Path          string
	ResourceType  string
	ResourceName  string
	Status        int
	Timestamp     time.Time
}

// discoverRequests queries frontend logs to find all ARM requests in the resource group
// during the time window.
func (g *Gatherer) discoverRequests(ctx context.Context, input GatherInput, data queryData) ([]frontendRequest, error) {
	rendered, err := renderQuery("queries/frontend/frontendRequests/query.kql", data)
	if err != nil {
		return nil, err
	}

	rows, err := g.executeKQL(ctx, rendered, input.ServiceDatabase, input.QueryTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to query frontend requests: %w", err)
	}

	var requests []frontendRequest
	for _, row := range rows {
		req := frontendRequest{}
		for i, col := range row.columns {
			val := row.values[i]
			switch col {
			case "correlation_id":
				req.CorrelationID = val
			case "method":
				req.Method = strings.ToUpper(val)
			case "path":
				req.Path = val
			case "status":
				_, _ = fmt.Sscanf(val, "%d", &req.Status)
			case "timestamp":
				req.Timestamp, _ = time.Parse(time.RFC3339, val)
			}
		}
		// Parse resource type and name from the path.
		if req.Path != "" {
			if parsed, err := parseResourceFromPath(req.Path); err == nil {
				req.ResourceType = parsed.resourceType
				req.ResourceName = parsed.resourceName
			}
		}
		requests = append(requests, req)
	}

	return requests, nil
}

type parsedResource struct {
	resourceType string
	resourceName string
}

func parseResourceFromPath(path string) (parsedResource, error) {
	// ARM resource IDs look like:
	// /subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/{name}
	// /subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/{name}/nodePools/{npName}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	providerIdx := -1
	for i, p := range parts {
		if strings.EqualFold(p, "providers") {
			providerIdx = i
			break
		}
	}
	if providerIdx < 0 || providerIdx+2 >= len(parts) {
		return parsedResource{}, fmt.Errorf("cannot parse resource from path: %s", path)
	}
	// Build resource type and find the last name segment.
	remaining := parts[providerIdx+1:] // e.g. ["Microsoft.RedHatOpenShift", "hcpOpenShiftClusters", "name", "nodePools", "npName"]
	if len(remaining) < 2 {
		return parsedResource{}, fmt.Errorf("cannot parse resource from path: %s", path)
	}

	// Resource type segments: provider/type[/subType]*
	// Resource name is the last segment.
	var typeParts []string
	var name string
	typeParts = append(typeParts, remaining[0]) // provider namespace
	for i := 1; i < len(remaining); i += 2 {
		typeParts = append(typeParts, remaining[i])
		if i+1 < len(remaining) {
			name = remaining[i+1]
		}
	}

	return parsedResource{
		resourceType: strings.Join(typeParts, "/"),
		resourceName: name,
	}, nil
}

// resultRow is a simple container for a row of query results.
type resultRow struct {
	columns []string
	values  []string
}

// executeQuery renders and executes a single query spec, writes output files,
// and returns the result rows for downstream dependency resolution.
func (g *Gatherer) executeQuery(ctx context.Context, q querySpec, data *queryData, baseDir string, input GatherInput) ([]resultRow, error) {
	logger := logr.FromContextOrDiscard(ctx)

	rendered, err := renderQuery(q.templatePath, *data)
	if err != nil {
		return nil, fmt.Errorf("query %s: %w", q.key(), err)
	}

	outDir := filepath.Join(baseDir, q.component, q.queryName)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	// Write the rendered query for reproducibility.
	queryFile := filepath.Join(outDir, "query.kql")
	if err := os.WriteFile(queryFile, []byte(rendered), 0o644); err != nil {
		return nil, fmt.Errorf("failed to write query file: %w", err)
	}

	db := input.ServiceDatabase
	if q.database == "hcp" {
		db = input.HCPDatabase
	}

	rows, err := g.executeKQL(ctx, rendered, db, input.QueryTimeout)
	if err != nil {
		return nil, fmt.Errorf("query %s failed: %w", q.key(), err)
	}

	// Write markdown output.
	if len(rows) > 0 {
		md := renderMarkdownTable(rows)
		mdFile := filepath.Join(outDir, "output.md")
		if err := os.WriteFile(mdFile, []byte(md), 0o644); err != nil {
			return nil, fmt.Errorf("failed to write markdown: %w", err)
		}
		logger.Info("Wrote query output", "query", q.key(), "rows", len(rows), "file", mdFile)
	} else {
		logger.Info("Query returned no results", "query", q.key())
	}

	return rows, nil
}

// executeQueryToDir renders and executes a query but does not write output files.
// It is used when we need to run a query for its storeResult side effects but
// don't yet know the output directory (e.g. request discovery before resource is known).
func (g *Gatherer) executeQueryToDir(ctx context.Context, q querySpec, data *queryData, _ string, input GatherInput) ([]resultRow, error) {
	logger := logr.FromContextOrDiscard(ctx)

	rendered, err := renderQuery(q.templatePath, *data)
	if err != nil {
		return nil, fmt.Errorf("query %s: %w", q.key(), err)
	}

	db := input.ServiceDatabase
	if q.database == "hcp" {
		db = input.HCPDatabase
	}

	rows, err := g.executeKQL(ctx, rendered, db, input.QueryTimeout)
	if err != nil {
		return nil, fmt.Errorf("query %s failed: %w", q.key(), err)
	}

	if len(rows) > 0 {
		logger.Info("Query returned results (deferred write)", "query", q.key(), "rows", len(rows))
	} else {
		logger.Info("Query returned no results", "query", q.key())
	}

	return rows, nil
}

// writeQueryOutput renders a query template and writes the query.kql and output.md
// files using previously cached results, avoiding re-execution against Kusto.
// Used to write request discovery output after the resource directory is known.
func writeQueryOutput(q querySpec, data queryData, baseDir string, cachedRows []resultRow) error {
	rendered, err := renderQuery(q.templatePath, data)
	if err != nil {
		return fmt.Errorf("query %s: %w", q.key(), err)
	}

	outDir := filepath.Join(baseDir, q.component, q.queryName)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	queryFile := filepath.Join(outDir, "query.kql")
	if err := os.WriteFile(queryFile, []byte(rendered), 0o644); err != nil {
		return fmt.Errorf("failed to write query file: %w", err)
	}

	if len(cachedRows) > 0 {
		md := renderMarkdownTable(cachedRows)
		mdFile := filepath.Join(outDir, "output.md")
		if err := os.WriteFile(mdFile, []byte(md), 0o644); err != nil {
			return fmt.Errorf("failed to write markdown: %w", err)
		}
	}

	return nil
}

// executeKQL runs a KQL query string against the given database and returns
// the result rows with column names and string values.
func (g *Gatherer) executeKQL(ctx context.Context, kqlStr, database string, timeout time.Duration) ([]resultRow, error) {
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	builder := kql.New("")
	builder.AddUnsafe(kqlStr)

	dataset, err := g.client.IterativeQuery(queryCtx, database, builder)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}

	var rows []resultRow
	primaryResult := <-dataset.Tables()
	if err := primaryResult.Err(); err != nil {
		return nil, fmt.Errorf("failed to get primary result: %w", err)
	}
	if primaryResult.Table() == nil {
		return nil, nil
	}

	var columns []string
	columnsSet := false

	for rowResult := range primaryResult.Table().Rows() {
		row := rowResult.Row()
		if row == nil {
			continue
		}
		if !columnsSet && row.Columns() != nil {
			columns = extractColumnNames(row.Columns())
			columnsSet = true
		}
		values := extractRowValues(row)
		rows = append(rows, resultRow{columns: columns, values: values})
	}

	return rows, nil
}

// extractColumnNames gets column names from a Columns slice.
func extractColumnNames(cols azkquery.Columns) []string {
	names := make([]string, len(cols))
	for i, col := range cols {
		names[i] = col.Name()
	}
	return names
}

// extractRowValues converts a Row's values to string representations.
func extractRowValues(row azkquery.Row) []string {
	values := row.Values()
	strs := make([]string, len(values))
	for i, v := range values {
		strs[i] = v.String()
	}
	return strs
}

// renderMarkdownTable produces a GitHub-flavored markdown table from result rows.
func renderMarkdownTable(rows []resultRow) string {
	if len(rows) == 0 {
		return ""
	}
	var buf bytes.Buffer
	cols := rows[0].columns

	// Header
	buf.WriteString("|")
	for _, col := range cols {
		buf.WriteString(" ")
		buf.WriteString(col)
		buf.WriteString(" |")
	}
	buf.WriteString("\n|")
	for range cols {
		buf.WriteString(" --- |")
	}
	buf.WriteString("\n")

	// Rows
	for _, row := range rows {
		buf.WriteString("|")
		for i := range cols {
			buf.WriteString(" ")
			if i < len(row.values) {
				buf.WriteString(strings.ReplaceAll(row.values[i], "|", "\\|"))
			}
			buf.WriteString(" |")
		}
		buf.WriteString("\n")
	}

	return buf.String()
}

// skippedQuery records a query that was not executed.
type skippedQuery struct {
	key           string
	prerequisites string
}

// writeResourceSummary generates a SUMMARY.md for a resource directory, including
// discovered facts, the list of requests, and any skipped queries.
func writeResourceSummary(dir string, data queryData, requests []trackedRequest, skipped []skippedQuery) error {
	var buf bytes.Buffer
	buf.WriteString("# Resource Summary\n\n")

	buf.WriteString("## Discovered Facts\n\n")
	buf.WriteString("| Fact | Value |\n")
	buf.WriteString("|------|-------|\n")

	buf.WriteString(fmt.Sprintf("| Start Time | `%s` |\n", data.StartTime.Format(time.RFC3339)))
	buf.WriteString(fmt.Sprintf("| End Time | `%s` |\n", data.EndTime.Format(time.RFC3339)))

	type fact struct {
		label string
		value string
	}
	facts := []fact{
		{"Resource ID", data.ResourceID},
		{"Resource Type", data.ResourceType},
		{"Resource Group", data.ResourceGroup},
		{"Resource Name", data.ResourceName},
		{"Cluster Resource Name", data.ClusterResourceName},
		{"Service Provider Resource Type", data.ServiceProviderResourceType},
		{"Internal ID", data.InternalID},
		{"Cluster ID", data.ClusterID},
		{"Hosted Cluster Namespace", data.HostedClusterNamespace},
		{"Hosted Control Plane Namespace", data.HostedControlPlaneNamespace},
		{"Bundle IDs", strings.Join(data.BundleIDs, ", ")},
		{"Bundle Names", strings.Join(data.BundleNames, ", ")},
		{"Manifest Work Names", strings.Join(data.ManifestWorkNames, ", ")},
	}
	for _, f := range facts {
		if f.value != "" {
			buf.WriteString(fmt.Sprintf("| %s | `%s` |\n", f.label, f.value))
		}
	}

	buf.WriteString("\n## Requests\n\n")
	if len(requests) == 0 {
		buf.WriteString("No mutating requests were traced.\n")
	} else {
		buf.WriteString("| Correlation ID | Method | Path | Status | Timestamp |\n")
		buf.WriteString("|----------------|--------|------|--------|----------|\n")
		for _, tr := range requests {
			buf.WriteString(fmt.Sprintf("| `%s` | %s | `%s` | %d | %s |\n",
				tr.req.CorrelationID, tr.req.Method, tr.req.Path, tr.req.Status, tr.req.Timestamp.Format(time.RFC3339)))
		}
	}

	buf.WriteString("\n## Skipped Queries\n\n")
	if len(skipped) == 0 {
		buf.WriteString("All queries were executed.\n")
	} else {
		buf.WriteString("| Query | Missing Prerequisites |\n")
		buf.WriteString("|-------|-----------------------|\n")
		for _, s := range skipped {
			buf.WriteString(fmt.Sprintf("| %s | %s |\n", s.key, s.prerequisites))
		}
	}

	return os.WriteFile(filepath.Join(dir, "SUMMARY.md"), buf.Bytes(), 0o644)
}

// sanitizePath replaces characters that are problematic in file paths.
func sanitizePath(s string) string {
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	return strings.ToLower(s)
}
