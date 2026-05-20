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
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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
	// Concurrency is the maximum number of concurrent Kusto queries.
	// A value of 0 defaults to 4 * runtime.NumCPU().
	Concurrency int
}

// concurrency returns the effective concurrency limit.
func (g GatherInput) concurrency() int {
	if g.Concurrency > 0 {
		return g.Concurrency
	}
	return 4 * runtime.NumCPU()
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

	pool := &queryPool{gatherer: g, input: input}

	// Phase 1: Run initial context queries (frontendRequests) to discover all ARM requests.
	for _, q := range contextQueries {
		if _, err := g.executeQuery(ctx, q, &seedData, outputDir, input); err != nil {
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

	// Phase 2 (Pool 1): Per-request discovery — run requestDiscovery queries
	// for each mutating request concurrently.
	type trackedRequestWithMu struct {
		trackedRequest
		mu *sync.Mutex
	}
	var trackedReqs []trackedRequestWithMu
	var pool1Items []workItem
	discoveryQueries := queriesByCategory(categoryRequestDiscovery)

	for _, req := range requests {
		isMutating := req.Method == "PUT" || req.Method == "POST" || req.Method == "PATCH" || req.Method == "DELETE"
		if !isMutating {
			continue
		}

		reqData := seedData // copy seed values
		reqData.CorrelationID = req.CorrelationID
		reqData.ClientRequestID = req.ClientRequestID
		reqData.ResponseStatusCode = req.Status

		tr := trackedRequestWithMu{
			trackedRequest: trackedRequest{
				req:           req,
				data:          reqData,
				cachedResults: make(map[string][]resultRow),
			},
			mu: &sync.Mutex{},
		}
		trackedReqs = append(trackedReqs, tr)
		idx := len(trackedReqs) - 1

		for _, q := range discoveryQueries {
			pool1Items = append(pool1Items, workItem{
				query: q,
				data:  &trackedReqs[idx].data,
				mu:    trackedReqs[idx].mu,
			})
		}
	}

	logger.Info("Running request discovery", "requests", len(trackedReqs), "queries", len(pool1Items))
	pool.runPool(ctx, pool1Items)

	// Populate cachedResults from pool1 items for later writing.
	itemIdx := 0
	for i := range trackedReqs {
		for range discoveryQueries {
			item := &pool1Items[itemIdx]
			trackedReqs[i].cachedResults[item.query.key()] = item.resultRows
			itemIdx++
		}
	}

	// Sync point: Deduplicate resources and build Pool 2 work items.
	type resourceState struct {
		data     queryData
		mu       *sync.Mutex
		requests []trackedRequest
	}
	resources := make(map[string]*resourceState)
	var resourceOrder []string

	for i := range trackedReqs {
		tr := &trackedReqs[i]
		key := resourceKey(tr.data)
		if key == "" {
			key = "unknown/" + tr.req.ClientRequestID
		}
		if _, ok := resources[key]; !ok {
			resData := tr.data
			resData.ResponseStatusCode = 0
			resources[key] = &resourceState{data: resData, mu: &sync.Mutex{}}
			resourceOrder = append(resourceOrder, key)
		}
		resources[key].requests = append(resources[key].requests, tr.trackedRequest)
	}

	// Write cached request discovery output and build Pool 2 work items.
	var pool2Items []workItem

	for _, key := range resourceOrder {
		rs := resources[key]
		var fallbackID string
		if len(rs.requests) > 0 {
			fallbackID = rs.requests[0].req.ClientRequestID
		}
		resDir := resourceDir(outputDir, rs.data, fallbackID)

		allClientErrors := len(rs.requests) > 0
		for _, tr := range rs.requests {
			if tr.req.Status < 400 || tr.req.Status >= 500 {
				allClientErrors = false
				break
			}
		}

		// Enqueue resource-level queries if not all client errors.
		if !allClientErrors {
			discoveryDir := filepath.Join(resDir, "discovery")
			for _, q := range queriesByCategory(categoryResourceDiscovery) {
				pool2Items = append(pool2Items, workItem{
					query:             q,
					data:              &rs.data,
					mu:                rs.mu,
					outputDir:         discoveryDir,
					verificationSuite: key,
				})
			}

			stateDir := filepath.Join(resDir, "state")
			for _, q := range queriesByCategory(categoryState) {
				pool2Items = append(pool2Items, workItem{
					query:             q,
					data:              &rs.data,
					mu:                rs.mu,
					outputDir:         stateDir,
					verificationSuite: key,
				})
			}

			conditionsDir := filepath.Join(resDir, "conditions")
			for _, q := range queriesByCategory(categoryConditions) {
				pool2Items = append(pool2Items, workItem{
					query:             q,
					data:              &rs.data,
					mu:                rs.mu,
					outputDir:         conditionsDir,
					verificationSuite: key,
				})
			}

			logsDir := filepath.Join(resDir, "logs")
			for _, q := range queriesByCategory(categoryLogs) {
				pool2Items = append(pool2Items, workItem{
					query:             q,
					data:              &rs.data,
					mu:                rs.mu,
					outputDir:         logsDir,
					verificationSuite: key,
				})
			}

			eventsDir := filepath.Join(outputDir, "events")
			for _, q := range queriesByCategory(categoryEvents) {
				pool2Items = append(pool2Items, workItem{
					query:     q,
					data:      &rs.data,
					mu:        rs.mu,
					outputDir: eventsDir,
				})
			}
		}

		// Enqueue per-request trace queries and write cached discovery output.
		for i := range rs.requests {
			tr := &rs.requests[i]
			reqDir := filepath.Join(resDir, "requests", tr.req.Method+"-"+tr.req.ClientRequestID)
			reqSuite := key + "/" + tr.req.Method + "-" + tr.req.ClientRequestID

			// Write request discovery output now that we know the resource directory.
			reqDiscoveryDir := filepath.Join(reqDir, "discovery")
			traceData := tr.data
			mergeResourceData(&traceData, rs.data)

			for _, q := range queriesByCategory(categoryRequestDiscovery) {
				cached := tr.cachedResults[q.key()]
				if err := writeQueryOutput(q, traceData, reqDiscoveryDir, cached); err != nil {
					logger.Error(err, "Failed to write request discovery output", "query", q.key())
				}
			}

			// Store the traceData for this request so trace queries can use it.
			// We need a stable pointer, so store it back.
			tr.data = traceData
			trMu := &sync.Mutex{}

			traceStateDir := filepath.Join(reqDir, "state")
			for _, q := range queriesByCategory(categoryTraceState) {
				pool2Items = append(pool2Items, workItem{
					query:             q,
					data:              &tr.data,
					mu:                trMu,
					outputDir:         traceStateDir,
					verificationSuite: reqSuite,
				})
			}

			traceLogsDir := filepath.Join(reqDir, "logs")
			for _, q := range queriesByCategory(categoryTraceLogs) {
				pool2Items = append(pool2Items, workItem{
					query:             q,
					data:              &tr.data,
					mu:                trMu,
					outputDir:         traceLogsDir,
					verificationSuite: reqSuite,
				})
			}
		}
	}

	logger.Info("Running resource and trace queries", "items", len(pool2Items))
	pool.runPool(ctx, pool2Items)

	// Record verification results from Pool 2 items.
	for i := range pool2Items {
		item := &pool2Items[i]
		if item.verificationSuite == "" {
			continue
		}
		if !item.executed {
			// Query was never ready — record as skipped.
			recordVerification(report, item.verificationSuite, item.query, *item.data, 0, true)
		} else if !item.failed {
			recordVerification(report, item.verificationSuite, item.query, *item.data, len(item.resultRows), false)
		}
	}

	// Record verification for request discovery (from Pool 1 cached results).
	for _, key := range resourceOrder {
		rs := resources[key]
		for _, tr := range rs.requests {
			reqSuite := key + "/" + tr.req.Method + "-" + tr.req.ClientRequestID
			for _, q := range queriesByCategory(categoryRequestDiscovery) {
				if q.ready != nil && !q.ready(tr.data) {
					recordVerification(report, reqSuite, q, tr.data, 0, true)
				} else {
					rowCount := requestDiscoveryRowCount(q, tr.data)
					recordVerification(report, reqSuite, q, tr.data, rowCount, false)
				}
			}
		}
	}

	// Build manifest.
	for _, key := range resourceOrder {
		rs := resources[key]
		var fallbackID string
		if len(rs.requests) > 0 {
			fallbackID = rs.requests[0].req.ClientRequestID
		}
		resDir := resourceDir(outputDir, rs.data, fallbackID)

		if rs.data.ResourceType == "" {
			logger.Info("Skipping manifest entry for unknown resource", "key", key)
			continue
		}
		relDir, _ := filepath.Rel(outputDir, resDir)
		var requestInfos []RequestInfo
		for _, tr := range rs.requests {
			reqRelDir := filepath.Join(relDir, "requests", tr.req.Method+"-"+tr.req.ClientRequestID)
			requestInfos = append(requestInfos, RequestInfo{
				ClientRequestID: tr.req.ClientRequestID,
				CorrelationID:   tr.req.CorrelationID,
				Method:          tr.req.Method,
				Path:            tr.req.Path,
				Status:          tr.req.Status,
				Timestamp:       tr.req.Timestamp,
				Dir:             reqRelDir,
			})
		}
		manifest.Resources = append(manifest.Resources, ResourceEntry{
			Type:                        rs.data.ResourceType,
			Name:                        rs.data.ResourceName,
			Dir:                         relDir,
			ResourceID:                  rs.data.ResourceID,
			ClusterResourceID:           rs.data.ClusterResourceID,
			ClusterResourceName:         rs.data.ClusterResourceName,
			InternalID:                  rs.data.InternalID,
			ClusterID:                   rs.data.ClusterID,
			HostedClusterNamespace:      rs.data.HostedClusterNamespace,
			HostedControlPlaneNamespace: rs.data.HostedControlPlaneNamespace,
			Requests:                    requestInfos,
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
	if dst.HostedClusterNamespace == "" {
		dst.HostedClusterNamespace = src.HostedClusterNamespace
	}
	if dst.HostedControlPlaneNamespace == "" {
		dst.HostedControlPlaneNamespace = src.HostedControlPlaneNamespace
	}
}

// frontendRequest represents a single ARM request discovered in frontend logs.
type frontendRequest struct {
	CorrelationID   string
	ClientRequestID string
	Method          string
	Path            string
	ResourceType    string
	ResourceName    string
	Status          int
	Timestamp       time.Time
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
			case "client_request_id":
				req.ClientRequestID = val
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

// readQueryReadme reads the embedded README.md for a query spec and returns its
// content as a string. Returns an empty string if no README exists.
func readQueryReadme(q querySpec) string {
	readmePath := filepath.Join("queries", q.component, q.queryName, "README.md")
	data, err := queriesFS.ReadFile(readmePath)
	if err != nil {
		return ""
	}
	return string(data)
}

// composeOutput builds a single markdown document combining the README content,
// the rendered KQL query, and the results table.
func composeOutput(readme string, rendered string, rows []resultRow) string {
	var buf bytes.Buffer
	if readme != "" {
		buf.WriteString(readme)
		if !strings.HasSuffix(readme, "\n") {
			buf.WriteString("\n")
		}
		buf.WriteString("\n")
	}
	buf.WriteString("## Query\n\n```kql\n")
	buf.WriteString(rendered)
	if !strings.HasSuffix(rendered, "\n") {
		buf.WriteString("\n")
	}
	buf.WriteString("```\n\n")
	buf.WriteString("## Results\n\n")
	if len(rows) > 0 {
		buf.WriteString(renderMarkdownTable(rows))
	} else {
		buf.WriteString("No results returned.\n")
	}
	return buf.String()
}

// executeQuery renders and executes a single query spec, writes a combined
// output file (<queryName>.md), and returns the result rows for downstream
// dependency resolution.
func (g *Gatherer) executeQuery(ctx context.Context, q querySpec, data *queryData, baseDir string, input GatherInput) ([]resultRow, error) {
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

	// Write combined output file: README + query + results.
	outDir := filepath.Join(baseDir, q.component)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	readme := readQueryReadme(q)
	md := composeOutput(readme, rendered, rows)
	mdFile := filepath.Join(outDir, q.queryName+".md")
	if err := os.WriteFile(mdFile, []byte(md), 0o644); err != nil {
		return nil, fmt.Errorf("failed to write output: %w", err)
	}

	if len(rows) > 0 {
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

// writeQueryOutput renders a query template and writes a combined output file
// using previously cached results, avoiding re-execution against Kusto.
// Used to write request discovery output after the resource directory is known.
func writeQueryOutput(q querySpec, data queryData, baseDir string, cachedRows []resultRow) error {
	rendered, err := renderQuery(q.templatePath, data)
	if err != nil {
		return fmt.Errorf("query %s: %w", q.key(), err)
	}

	outDir := filepath.Join(baseDir, q.component)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	readme := readQueryReadme(q)
	md := composeOutput(readme, rendered, cachedRows)
	mdFile := filepath.Join(outDir, q.queryName+".md")
	if err := os.WriteFile(mdFile, []byte(md), 0o644); err != nil {
		return fmt.Errorf("failed to write output: %w", err)
	}

	return nil
}

// executeKQL runs a KQL query string against the given database and returns
// the result rows with column names and string values.
func (g *Gatherer) executeKQL(ctx context.Context, kqlStr, database string, timeout time.Duration) ([]resultRow, error) {
	const maxAttempts = 3
	logger := logr.FromContextOrDiscard(ctx)
	var lastErr error
	for attempt := range maxAttempts {
		if attempt > 0 {
			backoff := time.Duration(1<<(attempt-1)) * time.Second
			logger.Info("Retrying query after transient error", "attempt", attempt+1, "backoff", backoff, "error", lastErr)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}
		rows, err := g.executeKQLOnce(ctx, kqlStr, database, timeout)
		if err == nil {
			return rows, nil
		}
		if !isRetryableError(err) {
			return nil, err
		}
		lastErr = err
	}
	return nil, lastErr
}

// isRetryableError returns true for transient network errors that warrant a retry.
func isRetryableError(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	// Some wrapped errors lose the net.Error type; check the message as a fallback.
	msg := err.Error()
	for _, substr := range []string{"connection reset", "TLS handshake", "broken pipe"} {
		if strings.Contains(msg, substr) {
			return true
		}
	}
	return false
}

func (g *Gatherer) executeKQLOnce(ctx context.Context, kqlStr, database string, timeout time.Duration) ([]resultRow, error) {
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

// escapeMarkdownCell escapes a value for use inside a GitHub-flavored markdown table cell.
func escapeMarkdownCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\r\n", "<br>")
	s = strings.ReplaceAll(s, "\n", "<br>")
	s = strings.ReplaceAll(s, "\r", "<br>")
	return s
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
				buf.WriteString(escapeMarkdownCell(row.values[i]))
			}
			buf.WriteString(" |")
		}
		buf.WriteString("\n")
	}

	return buf.String()
}

// sanitizePath replaces characters that are problematic in file paths.
func sanitizePath(s string) string {
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	return strings.ToLower(s)
}
