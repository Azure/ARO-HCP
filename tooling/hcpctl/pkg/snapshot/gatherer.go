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
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// GatherInput provides the parameters needed to gather a diagnostic snapshot.
type GatherInput struct {
	// ClusterURI is the full Kusto cluster endpoint URL.
	ClusterURI string
	// ServiceDatabase is the Kusto database containing service logs.
	ServiceDatabase string
	// HCPDatabase is the Kusto database containing hosted control plane logs.
	HCPDatabase string
	// MonitoringEventsDatabase is the Kusto database containing monitoring events (alerts).
	MonitoringEventsDatabase string
	// ResourceGroup is the Azure resource group to scope queries to.
	ResourceGroup string
	// TimeWindow is the full time range to query.
	TimeWindow TimeWindow
	// QueryTimeout is the timeout for individual Kusto queries.
	QueryTimeout time.Duration
	// Concurrency is the maximum number of concurrent Kusto queries.
	// A value of 0 defaults to 4 * runtime.NumCPU().
	Concurrency int
	// TestStartTime is when the first non-setup test step began. Zero when
	// no non-setup steps are present in the timing metadata.
	TestStartTime time.Time
	// CleanupStartTime is the time at which test cleanup (resource deletion)
	// began. A zero value means no cleanup time is available.
	CleanupStartTime time.Time

	// ServiceClusterName and ManagementClusterName are AKS cluster names
	// used to filter queries for PR jobs. When both are non-empty, queries
	// include a "| where cluster in (...)" filter to scope results to only
	// the relevant clusters.
	ServiceClusterName    string
	ManagementClusterName string
}

// validate checks that all required fields are set.
func (g GatherInput) validate() error {
	var missing []string
	if g.ClusterURI == "" {
		missing = append(missing, "ClusterURI")
	}
	if g.ServiceDatabase == "" {
		missing = append(missing, "ServiceDatabase")
	}
	if g.HCPDatabase == "" {
		missing = append(missing, "HCPDatabase")
	}
	if g.MonitoringEventsDatabase == "" {
		missing = append(missing, "MonitoringEventsDatabase")
	}
	if g.ResourceGroup == "" {
		missing = append(missing, "ResourceGroup")
	}
	if g.TimeWindow.Start.IsZero() {
		missing = append(missing, "TimeWindow.Start")
	}
	if g.TimeWindow.End.IsZero() {
		missing = append(missing, "TimeWindow.End")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required fields: %s", strings.Join(missing, ", "))
	}
	return nil
}

// concurrency returns the effective concurrency limit.
func (g GatherInput) concurrency() int {
	if g.Concurrency > 0 {
		return g.Concurrency
	}
	return 4 * runtime.NumCPU()
}

// databaseFor returns the Kusto database name for the given database key.
func (g GatherInput) databaseFor(database string) string {
	switch database {
	case "hcp":
		return g.HCPDatabase
	case "monitoringEvents":
		return g.MonitoringEventsDatabase
	default:
		return g.ServiceDatabase
	}
}

// phaseSpec describes a single phase (test or cleanup) with its time boundaries.
type phaseSpec struct {
	name  string
	start time.Time
	end   time.Time
}

// computePhases returns the phases to gather based on the input time boundaries.
// If TestStartTime is zero, StartTime is used as the test phase start.
// If CleanupStartTime is zero, only a test phase is returned spanning to EndTime.
func computePhases(input GatherInput) []phaseSpec {
	testStart := input.TestStartTime
	if testStart.IsZero() {
		testStart = input.TimeWindow.Start
	}

	if input.CleanupStartTime.IsZero() {
		return []phaseSpec{
			{name: "test_phase", start: testStart, end: input.TimeWindow.End},
		}
	}

	return []phaseSpec{
		{name: "test_phase", start: testStart, end: input.CleanupStartTime},
		{name: "cleanup_phase", start: input.CleanupStartTime, end: input.TimeWindow.End},
	}
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

// resourceDir returns the output directory for a resource within a base directory.
// When resource type is unknown, correlationID is used to produce a unique path.
func resourceDir(baseDir string, data queryData, correlationID string) string {
	if data.ResourceType == "" {
		return filepath.Join(baseDir, "resources", "unknown", sanitizePath(correlationID))
	}
	return filepath.Join(baseDir, "resources", sanitizePath(data.ResourceType), sanitizePath(data.ResourceName))
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

	// Render KQL for consumers that display the query text (e.g. HTML overview).
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

// resourceState holds per-resource discovery data across requests.
type resourceState struct {
	data     queryData
	mu       *sync.Mutex
	requests []trackedRequest
}

// Gather runs the full diagnostic data gathering pipeline for a resource group
// and writes structured output to outputDir. It runs discovery queries once,
// then per-phase (test and cleanup) queries with appropriate time boundaries.
func (g *Gatherer) Gather(ctx context.Context, input GatherInput, outputDir string) (*Manifest, *VerificationReport, error) {
	if err := input.validate(); err != nil {
		return nil, nil, fmt.Errorf("invalid GatherInput: %w", err)
	}

	logger := logr.FromContextOrDiscard(ctx)
	report := &VerificationReport{}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	// Build seed queryData for discovery (full time window).
	seedData := queryData{
		ClusterURI:               input.ClusterURI,
		ServiceDatabase:          input.ServiceDatabase,
		HCPDatabase:              input.HCPDatabase,
		MonitoringEventsDatabase: input.MonitoringEventsDatabase,
		ResourceGroup:            input.ResourceGroup,
		ServiceClusterName:       input.ServiceClusterName,
		ManagementClusterName:    input.ManagementClusterName,
		FullStartTime:            input.TimeWindow.Start,
		FullEndTime:              input.TimeWindow.End,
		// Discovery queries use the full window for phase fields too.
		PhaseStartTime: input.TimeWindow.Start,
		PhaseEndTime:   input.TimeWindow.End,
	}

	pool := &queryPool{gatherer: g, input: input}

	// =========================================================================
	// Phase-independent discovery
	// =========================================================================

	discoveryDir := filepath.Join(outputDir, "discovery")

	// Run context queries (frontendRequests) to discover all ARM requests.
	for _, q := range contextQueries {
		if _, err := g.executeQuery(ctx, q, &seedData, discoveryDir, input); err != nil {
			return nil, nil, fmt.Errorf("context query %s failed: %w", q.key(), err)
		}
	}

	// Discover all ARM requests from the frontend request data.
	requests, err := g.discoverRequests(ctx, input, seedData)
	if err != nil {
		logger.Error(err, "Failed to discover requests, continuing with empty request list")
		requests = nil
	}

	// Run per-request and per-resource discovery.
	trackedReqs, resources, resourceOrder := g.runDiscovery(ctx, pool, seedData, requests, discoveryDir, report, logger)

	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}

	// =========================================================================
	// Per-phase execution
	// =========================================================================

	phases := computePhases(input)
	manifest := &Manifest{
		TimeWindow: TimeWindow{
			Start:            input.TimeWindow.Start,
			End:              input.TimeWindow.End,
			SetupFinishTime:  input.TimeWindow.SetupFinishTime,
			TestStartTime:    input.TestStartTime,
			CleanupStartTime: input.CleanupStartTime,
		},
		ResourceGroup:   input.ResourceGroup,
		KustoCluster:    input.ClusterURI,
		KustoDatabase:   input.ServiceDatabase,
		DirectoryLayout: directoryLayout(),
	}

	for _, phase := range phases {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}

		phaseDir := filepath.Join(outputDir, phase.name)
		logger.Info("Gathering phase", "phase", phase.name, "start", phase.start.Format(time.RFC3339), "end", phase.end.Format(time.RFC3339))

		phaseManifest := g.gatherPhase(ctx, pool, input, phase, phaseDir, resources, resourceOrder, trackedReqs, report, logger)
		manifest.Phases = append(manifest.Phases, *phaseManifest)

		if err := writePhaseManifest(phaseDir, phaseManifest); err != nil {
			logger.Error(err, "Failed to write phase manifest", "phase", phase.name)
		}
	}

	// Record verification for request discovery.
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

	if err := WriteManifest(outputDir, manifest); err != nil {
		return nil, nil, err
	}

	return manifest, report, nil
}

// runDiscovery executes request-level and resource-level discovery queries,
// writing output to the discovery directory. Returns the tracked requests,
// deduplicated resources, and resource ordering.
func (g *Gatherer) runDiscovery(
	ctx context.Context,
	pool *queryPool,
	seedData queryData,
	requests []frontendRequest,
	discoveryDir string,
	report *VerificationReport,
	logger logr.Logger,
) ([]trackedRequest, map[string]*resourceState, []string) {
	type trackedRequestWithMu struct {
		trackedRequest
		mu *sync.Mutex
	}
	var trackedReqs []trackedRequestWithMu
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
		reqData.SubscriptionID = req.SubscriptionID
		reqData.ResourceID = req.ResourceID
		reqData.ResourceType = req.ResourceType
		reqData.ResourceName = req.ResourceName
		reqData.ClusterResourceID = req.ClusterResourceID
		reqData.ClusterResourceName = req.ClusterResourceName
		reqData.ServiceProviderResourceType = req.ServiceProviderResourceType

		trackedReqs = append(trackedReqs, trackedRequestWithMu{
			trackedRequest: trackedRequest{
				req:           req,
				data:          reqData,
				cachedResults: make(map[string][]resultRow),
			},
			mu: &sync.Mutex{},
		})
	}

	// Pool 1: Run request discovery queries concurrently.
	var pool1Items []workItem
	for idx := range trackedReqs {
		for _, q := range discoveryQueries {
			pool1Items = append(pool1Items, workItem{
				query: q,
				data:  &trackedReqs[idx].data,
				mu:    trackedReqs[idx].mu,
			})
		}
	}

	logger.V(1).Info("Running request discovery", "requests", len(trackedReqs), "queries", len(pool1Items))
	pool.runPool(ctx, pool1Items)

	// Populate cachedResults from pool1 items.
	itemIdx := 0
	for i := range trackedReqs {
		for range discoveryQueries {
			item := &pool1Items[itemIdx]
			trackedReqs[i].cachedResults[item.query.key()] = item.resultRows
			itemIdx++
		}
	}

	// =========================================================================
	// Cosmos resource discovery: find all resource types under the cluster prefix
	// =========================================================================

	// Collect the first ClusterResourceID + SubscriptionID from tracked requests.
	var cosmosDiscoveryData queryData
	for i := range trackedReqs {
		if trackedReqs[i].data.ClusterResourceID != "" && trackedReqs[i].data.SubscriptionID != "" {
			cosmosDiscoveryData = seedData
			cosmosDiscoveryData.ClusterResourceID = trackedReqs[i].data.ClusterResourceID
			cosmosDiscoveryData.SubscriptionID = trackedReqs[i].data.SubscriptionID
			break
		}
	}

	// cosmosChildTypes maps lowercased parent resource ID → set of child resource types.
	cosmosChildTypes := make(map[string]map[string]bool)
	// cosmosResources maps lowercased resource ID → resource type for all discovered resources.
	cosmosResources := make(map[string]string)

	if cosmosDiscoveryData.ClusterResourceID != "" {
		rendered, err := renderQuery("queries/backend/cosmosResourceDiscovery/query.kql", cosmosDiscoveryData)
		if err == nil {
			rows, queryErr := g.executeKQL(ctx, rendered, cosmosDiscoveryData.ServiceDatabase, 2*time.Minute)
			if queryErr != nil {
				logger.Error(queryErr, "Cosmos resource discovery query failed, continuing without discovery")
			} else {
				for _, row := range rows {
					if len(row.values) < 2 {
						continue
					}
					resID := row.values[0]
					resType := row.values[1]
					cosmosResources[resID] = resType

					// Determine parent resource ID by parsing and checking for a parent.
					parsed, parseErr := azcorearm.ParseResourceID(resID)
					if parseErr != nil {
						continue
					}
					if parsed.Parent != nil && parsed.Parent.ResourceType.Type != "" {
						parentID := strings.ToLower(parsed.Parent.String())
						if cosmosChildTypes[parentID] == nil {
							cosmosChildTypes[parentID] = make(map[string]bool)
						}
						cosmosChildTypes[parentID][resType] = true
					}
				}
				logger.V(1).Info("Cosmos resource discovery complete",
					"totalDocTypes", len(cosmosResources),
					"parentsWithChildren", len(cosmosChildTypes))
			}
		} else {
			logger.Error(err, "Failed to render cosmos resource discovery query")
		}
	}

	// Deduplicate resources.
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

	// Add Cosmos-discovered resources not found via frontend requests, and
	// populate ChildResourceTypes for all resources.
	if cosmosDiscoveryData.ClusterResourceID != "" {
		// Identify top-level resources from Cosmos (direct children of the cluster
		// or the cluster itself) and add them if not already tracked.
		clusterResourceIDLower := strings.ToLower(cosmosDiscoveryData.ClusterResourceID)
		for resID, resType := range cosmosResources {
			parsed, err := azcorearm.ParseResourceID(resID)
			if err != nil {
				continue
			}
			// A "top-level" resource is either the cluster itself or a direct child
			// (its parent is the cluster resource ID).
			isClusterItself := strings.EqualFold(resID, clusterResourceIDLower)
			isDirectChild := parsed.Parent != nil && strings.EqualFold(parsed.Parent.String(), clusterResourceIDLower)
			if !isClusterItself && !isDirectChild {
				continue
			}
			// Skip child-only resource types (hcpopenshiftcontrollers, readdesires,
			// serviceprovider*, applydesires) — these aren't top-level resources.
			typeParts := strings.Split(strings.ToLower(parsed.ResourceType.Type), "/")
			lastSegment := typeParts[len(typeParts)-1]
			if strings.HasPrefix(lastSegment, "serviceprovider") ||
				lastSegment == "hcpopenshiftcontrollers" ||
				lastSegment == "readdesires" ||
				lastSegment == "applydesires" {
				continue
			}

			key := sanitizePath(resType) + "/" + sanitizePath(parsed.Name)
			if _, ok := resources[key]; !ok {
				resData := seedData
				resData.SubscriptionID = cosmosDiscoveryData.SubscriptionID
				resData.ResourceID = resID
				resData.ResourceType = resType
				resData.ResourceName = parsed.Name
				resData.ClusterResourceID = cosmosDiscoveryData.ClusterResourceID
				if parsed.Parent != nil && parsed.Parent.ResourceType.Type != "" {
					resData.ClusterResourceName = parsed.Parent.Name
				} else {
					resData.ClusterResourceName = parsed.Name
				}
				resData.ServiceProviderResourceType = serviceProviderResourceType(resType)
				resources[key] = &resourceState{data: resData, mu: &sync.Mutex{}}
				resourceOrder = append(resourceOrder, key)
				logger.V(1).Info("Cosmos-discovered resource", "key", key, "resourceID", resID)
			}
		}

		// Populate ChildResourceTypes on all tracked resources.
		for _, rs := range resources {
			resIDLower := strings.ToLower(rs.data.ResourceID)
			if children, ok := cosmosChildTypes[resIDLower]; ok {
				rs.data.ChildResourceTypes = children
			}
			// Also derive ServiceProviderResourceType dynamically if not already set.
			if rs.data.ServiceProviderResourceType == "" && rs.data.ChildResourceTypes != nil {
				rs.data.ServiceProviderResourceType = rs.data.childServiceProviderType()
			}
		}
	}

	// Pool 2: Run resource discovery queries + write cached request discovery output.
	var pool2Items []workItem

	for _, key := range resourceOrder {
		rs := resources[key]
		var fallbackID string
		if len(rs.requests) > 0 {
			fallbackID = rs.requests[0].req.ClientRequestID
		}
		resDir := resourceDir(discoveryDir, rs.data, fallbackID)

		allClientErrors := len(rs.requests) > 0
		for _, tr := range rs.requests {
			if tr.req.Status < 400 || tr.req.Status >= 500 {
				allClientErrors = false
				break
			}
		}

		if !allClientErrors {
			for _, q := range queriesByCategory(categoryResourceDiscovery) {
				pool2Items = append(pool2Items, workItem{
					query:             q,
					data:              &rs.data,
					mu:                rs.mu,
					outputDir:         resDir,
					verificationSuite: key,
				})
			}
		}

		// Write cached request discovery output.
		for i := range rs.requests {
			tr := &rs.requests[i]
			reqDir := filepath.Join(resDir, "requests", tr.req.Method+"-"+tr.req.ClientRequestID)
			traceData := tr.data
			mergeResourceData(&traceData, rs.data)

			for _, q := range queriesByCategory(categoryRequestDiscovery) {
				cached := tr.cachedResults[q.key()]
				if err := writeQueryOutput(q, traceData, reqDir, cached); err != nil {
					logger.Error(err, "Failed to write request discovery output", "query", q.key())
				}
			}
			tr.data = traceData
		}
	}

	logger.V(1).Info("Running resource discovery", "items", len(pool2Items))
	pool.runPool(ctx, pool2Items)

	// Record verification for resource discovery.
	for i := range pool2Items {
		item := &pool2Items[i]
		if item.verificationSuite == "" {
			continue
		}
		if !item.executed {
			recordVerification(report, item.verificationSuite, item.query, *item.data, 0, true)
		} else if !item.failed {
			recordVerification(report, item.verificationSuite, item.query, *item.data, len(item.resultRows), false)
		}
	}

	// Flatten trackedReqs for return.
	flat := make([]trackedRequest, len(trackedReqs))
	for i := range trackedReqs {
		flat[i] = trackedReqs[i].trackedRequest
	}

	return flat, resources, resourceOrder
}

// gatherPhase runs all non-discovery queries for a single phase, writing output
// to phaseDir. It partitions requests by timestamp into this phase and runs
// state, conditions, logs, events, and trace queries.
func (g *Gatherer) gatherPhase(
	ctx context.Context,
	pool *queryPool,
	input GatherInput,
	phase phaseSpec,
	phaseDir string,
	resources map[string]*resourceState,
	resourceOrder []string,
	allTrackedReqs []trackedRequest,
	report *VerificationReport,
	logger logr.Logger,
) *PhaseManifest {

	phaseManifest := &PhaseManifest{
		Name:  phase.name,
		Dir:   phase.name,
		Start: phase.start,
		End:   phase.end,
	}

	var poolItems []workItem

	// For each resource, enqueue phase-scoped queries.
	for _, key := range resourceOrder {
		rs := resources[key]

		allClientErrors := len(rs.requests) > 0
		for _, tr := range rs.requests {
			if tr.req.Status < 400 || tr.req.Status >= 500 {
				allClientErrors = false
				break
			}
		}

		var fallbackID string
		if len(rs.requests) > 0 {
			fallbackID = rs.requests[0].req.ClientRequestID
		}
		resDir := resourceDir(phaseDir, rs.data, fallbackID)

		// Build phase-scoped queryData for this resource.
		phaseData := rs.data
		phaseData.FullStartTime = input.TimeWindow.Start
		phaseData.FullEndTime = input.TimeWindow.End
		phaseData.PhaseStartTime = phase.start
		phaseData.PhaseEndTime = phase.end
		phaseData.PhaseName = phase.name

		phaseDataPtr := &phaseData
		phaseMu := &sync.Mutex{}

		if !allClientErrors {
			// State queries
			stateDir := filepath.Join(resDir, "state")
			for _, q := range queriesByCategory(categoryState) {
				poolItems = append(poolItems, workItem{
					query:             q,
					data:              phaseDataPtr,
					mu:                phaseMu,
					outputDir:         stateDir,
					verificationSuite: phase.name + "/" + key,
				})
			}

			// Conditions queries
			conditionsDir := filepath.Join(resDir, "conditions")
			for _, q := range queriesByCategory(categoryConditions) {
				poolItems = append(poolItems, workItem{
					query:             q,
					data:              phaseDataPtr,
					mu:                phaseMu,
					outputDir:         conditionsDir,
					verificationSuite: phase.name + "/" + key,
				})
			}

			// Logs queries
			logsDir := filepath.Join(resDir, "logs")
			for _, q := range queriesByCategory(categoryLogs) {
				poolItems = append(poolItems, workItem{
					query:             q,
					data:              phaseDataPtr,
					mu:                phaseMu,
					outputDir:         logsDir,
					verificationSuite: phase.name + "/" + key,
				})
			}

			// Resource events queries
			eventsDir := filepath.Join(resDir, "events")
			for _, q := range queriesByCategory(categoryResourceEvents) {
				poolItems = append(poolItems, workItem{
					query:             q,
					data:              phaseDataPtr,
					mu:                phaseMu,
					outputDir:         eventsDir,
					verificationSuite: phase.name + "/" + key,
				})
			}
		}

		// Per-request trace queries — only for requests in this phase.
		for i := range rs.requests {
			tr := &rs.requests[i]
			if !g.requestInPhase(tr.req, phase) {
				continue
			}

			reqDir := filepath.Join(resDir, "requests", tr.req.Method+"-"+tr.req.ClientRequestID)
			reqSuite := phase.name + "/" + key + "/" + tr.req.Method + "-" + tr.req.ClientRequestID

			// Trace queries use full time window so we get the complete request lifecycle.
			traceData := tr.data
			mergeResourceData(&traceData, rs.data)
			traceData.FullStartTime = input.TimeWindow.Start
			traceData.FullEndTime = input.TimeWindow.End
			traceData.PhaseStartTime = input.TimeWindow.Start
			traceData.PhaseEndTime = input.TimeWindow.End
			traceData.PhaseName = phase.name

			traceDataPtr := &traceData
			traceMu := &sync.Mutex{}

			traceStateDir := filepath.Join(reqDir, "state")
			for _, q := range queriesByCategory(categoryTraceState) {
				poolItems = append(poolItems, workItem{
					query:             q,
					data:              traceDataPtr,
					mu:                traceMu,
					outputDir:         traceStateDir,
					verificationSuite: reqSuite,
				})
			}

			traceLogsDir := filepath.Join(reqDir, "logs")
			for _, q := range queriesByCategory(categoryTraceLogs) {
				poolItems = append(poolItems, workItem{
					query:             q,
					data:              traceDataPtr,
					mu:                traceMu,
					outputDir:         traceLogsDir,
					verificationSuite: reqSuite,
				})
			}
		}

		// Build resource entry for phase manifest.
		if rs.data.ResourceType != "" {
			relDir, _ := filepath.Rel(phaseDir, resDir)
			var requestInfos []RequestInfo
			for _, tr := range rs.requests {
				if !g.requestInPhase(tr.req, phase) {
					continue
				}
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
			phaseManifest.Resources = append(phaseManifest.Resources, ResourceEntry{
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
	}

	// Service-level event queries for this phase.
	serviceEventsDir := filepath.Join(phaseDir, "events")
	serviceEventData := queryData{
		ClusterURI:            input.ClusterURI,
		ServiceDatabase:       input.ServiceDatabase,
		HCPDatabase:           input.HCPDatabase,
		ResourceGroup:         input.ResourceGroup,
		ServiceClusterName:    input.ServiceClusterName,
		ManagementClusterName: input.ManagementClusterName,
		FullStartTime:         input.TimeWindow.Start,
		FullEndTime:           input.TimeWindow.End,
		PhaseStartTime:        phase.start,
		PhaseEndTime:          phase.end,
		PhaseName:             phase.name,
	}
	serviceEventMu := &sync.Mutex{}
	for _, q := range queriesByCategory(categoryEvents) {
		poolItems = append(poolItems, workItem{
			query:     q,
			data:      &serviceEventData,
			mu:        serviceEventMu,
			outputDir: serviceEventsDir,
		})
	}

	logger.V(1).Info("Running phase queries", "phase", phase.name, "items", len(poolItems))
	pool.runPool(ctx, poolItems)

	// Record verification results from phase pool items.
	for i := range poolItems {
		item := &poolItems[i]
		if item.verificationSuite == "" {
			continue
		}
		if !item.executed {
			recordVerification(report, item.verificationSuite, item.query, *item.data, 0, true)
		} else if !item.failed {
			recordVerification(report, item.verificationSuite, item.query, *item.data, len(item.resultRows), false)
		}
	}

	return phaseManifest
}

// requestInPhase returns true if the request's timestamp falls within the phase window.
func (g *Gatherer) requestInPhase(req frontendRequest, phase phaseSpec) bool {
	if req.Timestamp.IsZero() {
		return phase.name == "test_phase" // default to test phase if timestamp unknown
	}
	return !req.Timestamp.Before(phase.start) && req.Timestamp.Before(phase.end)
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
	CorrelationID               string
	ClientRequestID             string
	Method                      string
	Path                        string
	SubscriptionID              string
	ResourceID                  string
	ResourceType                string
	ResourceName                string
	ClusterResourceID           string
	ClusterResourceName         string
	ServiceProviderResourceType string
	Status                      int
	Timestamp                   time.Time
}

// discoverRequests queries frontend logs to find all ARM requests in the resource group
// during the time window.
func (g *Gatherer) discoverRequests(ctx context.Context, input GatherInput, data queryData) ([]frontendRequest, error) {
	logger := logr.FromContextOrDiscard(ctx)
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
			case "cluster_resource_id":
				if val != "" {
					req.ClusterResourceID = val
					if clusterParsed, err := azcorearm.ParseResourceID(val); err == nil {
						req.ClusterResourceName = clusterParsed.Name
					}
				}
			case "status":
				_, _ = fmt.Sscanf(val, "%d", &req.Status)
			case "timestamp":
				req.Timestamp, _ = time.Parse(time.RFC3339, val)
			}
		}
		// Parse resource type and name from the path. The path column from
		// frontendRequests is the full ARM resource ID.
		if req.Path != "" {
			if res, err := azcorearm.ParseResourceID(req.Path); err == nil {
				req.ResourceID = req.Path
				req.SubscriptionID = res.SubscriptionID
				req.ResourceType = res.ResourceType.String()
				req.ResourceName = res.Name
				req.ServiceProviderResourceType = serviceProviderResourceType(req.ResourceType)
				// If cluster_resource_id wasn't returned by the query, fall
				// back to deriving it from the path.
				if req.ClusterResourceName == "" {
					if res.Parent != nil && res.Parent.ResourceType.Type != "" {
						req.ClusterResourceID = res.Parent.String()
						req.ClusterResourceName = res.Parent.Name
					} else {
						req.ClusterResourceID = req.Path
						req.ClusterResourceName = res.Name
					}
				}
				logger.V(2).Info("Parsed resource from request path",
					"clientRequestID", req.ClientRequestID,
					"resourceType", req.ResourceType,
					"resourceName", req.ResourceName,
					"clusterResourceName", req.ClusterResourceName,
					"path", req.Path,
				)
			} else {
				logger.V(2).Info("Failed to parse resource from request path",
					"clientRequestID", req.ClientRequestID,
					"path", req.Path,
					"err", err,
				)
			}
		}
		requests = append(requests, req)
	}

	return requests, nil
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
// output file (<queryName>.md), and returns the result rows for
// dependency resolution.
func (g *Gatherer) executeQuery(ctx context.Context, q querySpec, data *queryData, baseDir string, input GatherInput) ([]resultRow, error) {
	logger := logr.FromContextOrDiscard(ctx)

	rendered, err := renderQuery(q.templatePath, *data)
	if err != nil {
		return nil, fmt.Errorf("query %s: %w", q.key(), err)
	}

	db := input.databaseFor(q.database)

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
		logger.V(1).Info("Wrote query output", "query", q.key(), "rows", len(rows), "file", mdFile)
	} else {
		logger.V(1).Info("Query returned no results", "query", q.key())
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

	db := input.databaseFor(q.database)

	rows, err := g.executeKQL(ctx, rendered, db, input.QueryTimeout)
	if err != nil {
		return nil, fmt.Errorf("query %s failed: %w", q.key(), err)
	}

	if len(rows) > 0 {
		logger.V(1).Info("Query returned results (deferred write)", "query", q.key(), "rows", len(rows))
	} else {
		logger.V(1).Info("Query returned no results", "query", q.key())
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
	for _, substr := range []string{"connection reset", "TLS handshake", "broken pipe", "QueryThrottledException"} {
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
