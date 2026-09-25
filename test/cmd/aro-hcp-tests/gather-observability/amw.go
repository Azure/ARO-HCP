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

package gatherobservability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

const (
	amwTimeout            = 60 * time.Second
	amwRequestTimeout     = 10 * time.Second
	amwMaxWorkspaces      = 4
	amwMaxDCRs            = 32
	amwMaxPages           = 10
	amwSeriesCap          = 1000
	amwMaxResponseBytes   = 8 << 20
	amwMaxTotalBytes      = 32 << 20
	amwWorkspaceNamespace = "Microsoft.Monitor/accounts"
	amwDCRNamespace       = "Microsoft.Insights/dataCollectionRules"
)

type amwReport struct {
	RequestedStart, RequestedEnd time.Time
	Start, End                   time.Time
	Resources                    []amwResource
	Errors                       []string
	Discovery                    []amwDiscovery
}

func (o Options) runAMW(ctx context.Context, deps gatherDependencies) (observabilityTab, error) {
	report := deps.collectAMW(ctx)
	logger := logr.FromContextOrDiscard(ctx)
	for _, message := range report.Errors {
		logger.Info("AMW collection incomplete", "warning", message)
	}
	content, writeErr := json.MarshalIndent(report, "", "  ")
	if writeErr == nil {
		writeErr = deps.writeFile(filepath.Join(o.OutputDir, "amw.json"), content, 0600)
	}
	html, renderErr := deps.renderAMW(report)
	if renderErr != nil {
		html = []byte(`<h1>AMW</h1><p>Partial evidence: <a href="amw.json">amw.json</a></p>`)
		for _, message := range report.Errors {
			html = incompleteHTML(html, errors.New(message))
		}
	}
	err := errors.Join(writeErr, renderErr)
	return observabilityTab{Title: "AMW", HTML: string(incompleteHTML(html, err))}, err
}

type amwResource struct {
	ID, Name, Kind string
	Workspaces     []string
	Metrics        []amwMetric
	Definitions    *armmonitor.MetricDefinitionsClientListResponse
}

type amwMetric struct {
	Name, Aggregation string
	Response          *armmonitor.MetricsClientListResponse
	Error             string
}

// Discovery retains the unmodified subscription-list pages, including destinations
// and data flows. A destination match does not prove a data flow is active.
type amwDiscovery struct {
	SubscriptionID string
	Responses      []armmonitor.DataCollectionRulesClientListBySubscriptionResponse
	Error          string
}

func (o Options) collectAMW(ctx context.Context) amwReport {
	if o.completedOptions == nil {
		return amwReport{Errors: []string{"AMW collection options unavailable"}}
	}
	var ids []string
	for _, id := range o.Workspaces {
		ids = append(ids, id.String())
	}
	report := collectAMW(ctx, o.cred, ids, o.TimeWindow.Start, o.TimeWindow.End, nil)
	for _, workspace := range slices.Sorted(maps.Keys(o.workspaceErrors)) {
		// Discovery errors may include credential diagnostics. Do not serialize them.
		report.Errors = append(report.Errors, fmt.Sprintf("workspace %s: workspace discovery failed", workspace))
	}
	slices.Sort(report.Errors)
	return report
}

// collectAMW uses only read-only ARM APIs. transport is injectable for offline
// tests; nil uses an HTTP client that refuses redirects. All requests, including
// server-provided pagination links, are restricted to the public ARM endpoint.
func collectAMW(ctx context.Context, cred azcore.TokenCredential, workspaceIDs []string, start, end time.Time, transport policy.Transporter) amwReport {
	ctx, cancel := context.WithTimeout(ctx, amwTimeout)
	defer cancel()
	report := amwReport{RequestedStart: start.UTC(), RequestedEnd: end.UTC(), Start: start.UTC(), End: end.UTC(), Resources: []amwResource{}}
	// Gather windows include a future grace period. Do not wait for it or query
	// future usage; retain the requested bounds separately from observed bounds.
	if now := time.Now(); end.After(now) {
		end = now
	}
	if start.IsZero() || end.IsZero() || !start.Before(end) {
		report.Errors = append(report.Errors, "invalid AMW time window: require nonzero start < end at collection time")
		return report
	}
	report.Start = start.UTC().Truncate(time.Minute)
	report.End = end.UTC().Truncate(time.Minute)
	if report.End.Before(end) {
		report.End = report.End.Add(time.Minute)
	}
	// Rounding may include the current, incomplete minute, but never extends the
	// requested range by a whole minute or permits more than 24 hours of data.
	if report.End.Sub(report.Start) > 24*time.Hour {
		report.Errors = append(report.Errors, "AMW time window exceeds 24 hours after outward minute rounding")
		return report
	}
	selected := map[string]string{}
	for _, raw := range slices.Sorted(slices.Values(workspaceIDs)) {
		id, err := azcorearm.ParseResourceID(raw)
		if err != nil || id.SubscriptionID == "" || id.ResourceGroupName == "" || !strings.EqualFold(id.ResourceType.String(), amwWorkspaceNamespace) {
			report.Errors = append(report.Errors, "invalid AMW workspace ARM ID")
			continue
		}
		key := strings.ToLower(id.String())
		if _, exists := selected[key]; exists {
			continue
		}
		if len(selected) == amwMaxWorkspaces {
			report.Errors = append(report.Errors, "workspace cap (4) reached; collection incomplete")
			break
		}
		selected[key] = id.String()
		report.Resources = append(report.Resources, amwResource{ID: id.String(), Name: id.Name, Kind: "workspace"})
	}
	if len(selected) == 0 {
		report.Errors = append(report.Errors, "no AMW workspaces available")
		return report
	}
	if transport == nil {
		transport = &http.Client{Timeout: amwRequestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	boundedTransport := &amwARMTransport{transport: transport, remaining: amwMaxTotalBytes}
	opts := &azcorearm.ClientOptions{ClientOptions: policy.ClientOptions{
		Transport: boundedTransport, Retry: policy.RetryOptions{MaxRetries: -1},
	}}
	collectMetrics := func(resources []amwResource) {
		for i := range resources {
			resource := &resources[i]
			id, _ := azcorearm.ParseResourceID(resource.ID)
			namespace := amwWorkspaceNamespace
			names := []string{"ActiveTimeSeries", "ActiveTimeSeriesLimit", "EventsPerMinuteIngested", "EventsPerMinuteIngestedLimit", "EventsDropped", "TimeSeriesSamplesDropped"}
			aggregation := "Maximum"
			if resource.Kind == "dcr" {
				namespace, names, aggregation = amwDCRNamespace, []string{"MetricIngestionRequest_Count"}, "Total"
			}
			client, clientErr := armmonitor.NewMetricsClient(id.SubscriptionID, cred, opts)
			slices.Sort(names)
			for _, name := range names {
				metric := amwMetric{Name: name, Aggregation: aggregation}
				// StampColor appears in the published catalog but is not exposed by
				// all workspaces. Filtering on it makes Azure reject the entire query.
				var dimensions []string
				if strings.HasSuffix(name, "Dropped") {
					dimensions = append(dimensions, "Reason")
				}
				if resource.Kind == "dcr" {
					dimensions = []string{"InputStreamId", "ResponseCode"}
				}
				var filters []string
				for _, dimension := range dimensions {
					filters = append(filters, dimension+" eq '*'")
				}
				err := clientErr
				if err == nil {
					err = ctx.Err()
				}
				if err == nil {
					requestCtx, requestCancel := context.WithTimeout(ctx, amwRequestTimeout)
					query := &armmonitor.MetricsClientListOptions{
						Metricnamespace: &namespace, Metricnames: &name, Aggregation: &aggregation,
						Interval: to.Ptr("PT1M"), Timespan: to.Ptr(report.Start.Format(time.RFC3339) + "/" + report.End.Format(time.RFC3339)),
						Top:                 to.Ptr(int32(amwSeriesCap)),
						AutoAdjustTimegrain: to.Ptr(false), ValidateDimensions: to.Ptr(true),
					}
					if len(filters) > 0 {
						query.Filter = to.Ptr(strings.Join(filters, " and "))
					}
					response, requestErr := client.List(requestCtx, resource.ID, query)
					requestCancel()
					metric.Response, err = &response, requestErr
				}
				if err != nil {
					metric.Error = amwRequestError(err)
				} else {
					metric.Error = amwValidateMetric(metric, dimensions)
				}
				if metric.Error != "" {
					report.Errors = append(report.Errors, resource.ID+" "+name+": "+metric.Error)
				}
				resource.Metrics = append(resource.Metrics, metric)
			}
		}
	}
	// Core workspace usage has first claim on the time and byte budgets. Discovery
	// and definitions must not consume those budgets before any metrics are read.
	workspaceCount := len(report.Resources)
	collectMetrics(report.Resources)
	subscriptions := map[string]string{}
	for _, resource := range report.Resources {
		id, _ := azcorearm.ParseResourceID(resource.ID)
		subscriptions[strings.ToLower(id.SubscriptionID)] = id.SubscriptionID
	}
	seenDCRs := map[string]bool{}
	for _, subscription := range slices.Sorted(maps.Values(subscriptions)) {
		discovery := amwDiscovery{SubscriptionID: subscription}
		client, err := armmonitor.NewDataCollectionRulesClient(subscription, cred, opts)
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			pager := client.NewListBySubscriptionPager(nil)
			for page := 0; pager.More(); page++ {
				if err = ctx.Err(); err != nil {
					break
				}
				if page == amwMaxPages {
					discovery.Error = "DCR discovery page cap (10) reached; collection incomplete"
					break
				}
				requestCtx, requestCancel := context.WithTimeout(ctx, amwRequestTimeout)
				response, requestErr := pager.NextPage(requestCtx)
				requestCancel()
				if requestErr != nil {
					err = requestErr
					break
				}
				discovery.Responses = append(discovery.Responses, response)
				if response.Value == nil {
					discovery.Error = "DCR discovery response missing resource list; collection incomplete"
					break
				}
				for _, rule := range response.Value {
					if rule == nil || rule.Properties == nil || rule.Properties.Destinations == nil {
						continue
					}
					matches := map[string]bool{}
					for _, destination := range rule.Properties.Destinations.MonitoringAccounts {
						if destination != nil && destination.AccountResourceID != nil {
							if id, found := selected[strings.ToLower(*destination.AccountResourceID)]; found {
								matches[id] = true
							}
						}
					}
					if len(matches) == 0 {
						continue
					}
					id, parseErr := azcorearm.ParseResourceID(ptr.Deref(rule.ID, ""))
					if parseErr != nil || id.ResourceGroupName == "" || !strings.EqualFold(id.SubscriptionID, subscription) || !strings.EqualFold(id.ResourceType.String(), amwDCRNamespace) {
						report.Errors = append(report.Errors, "DCR discovery returned a matching rule with an invalid ARM ID")
						continue
					}
					key := strings.ToLower(id.String())
					if seenDCRs[key] {
						continue
					}
					if len(seenDCRs) == amwMaxDCRs {
						discovery.Error = "DCR cap (32) reached; collection incomplete"
						break
					}
					seenDCRs[key] = true
					report.Resources = append(report.Resources, amwResource{ID: id.String(), Name: id.Name, Kind: "dcr", Workspaces: slices.Sorted(maps.Keys(matches))})
				}
				if discovery.Error != "" {
					break
				}
			}
		}
		if err != nil {
			discovery.Error = "DCR discovery failed: " + amwRequestError(err)
		}
		if discovery.Error != "" {
			report.Errors = append(report.Errors, subscription+": "+discovery.Error)
		}
		report.Discovery = append(report.Discovery, discovery)
	}
	slices.SortFunc(report.Resources[workspaceCount:], func(a, b amwResource) int { return strings.Compare(a.ID, b.ID) })
	collectMetrics(report.Resources[workspaceCount:])
	// Definitions are supplementary evidence, collected only after all metrics.
	for i := range report.Resources {
		resource := &report.Resources[i]
		id, _ := azcorearm.ParseResourceID(resource.ID)
		namespace := amwWorkspaceNamespace
		if resource.Kind == "dcr" {
			namespace = amwDCRNamespace
		}
		definitions, err := armmonitor.NewMetricDefinitionsClient(id.SubscriptionID, cred, opts)
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			requestCtx, requestCancel := context.WithTimeout(ctx, amwRequestTimeout)
			// This SDK implements definitions as a single-page pager. The transport
			// flags unexpected nextLink fields rather than silently losing pages.
			response, requestErr := definitions.NewListPager(resource.ID, &armmonitor.MetricDefinitionsClientListOptions{Metricnamespace: &namespace}).NextPage(requestCtx)
			requestCancel()
			resource.Definitions, err = &response, requestErr
			if err == nil && len(response.Value) == 0 {
				report.Errors = append(report.Errors, resource.ID+": empty metric definitions")
			}
		}
		if err != nil {
			report.Errors = append(report.Errors, resource.ID+": metric definitions: "+amwRequestError(err))
		}
	}
	report.Errors = append(report.Errors, boundedTransport.warnings...)
	slices.SortFunc(report.Resources, func(a, b amwResource) int { return strings.Compare(a.ID, b.ID) })
	slices.Sort(report.Errors)
	report.Errors = slices.Compact(report.Errors)
	return report
}

var (
	errAMWResponseLimit = errors.New("ARM response exceeds 8 MiB byte cap; collection incomplete")
	errAMWTotalLimit    = errors.New("ARM total response byte cap (32 MiB) reached; collection incomplete")
)

// Requests are sequential, so the transport owns the shared byte budget without
// synchronization. Buffer at most one bounded body before handing it to the SDK.
type amwARMTransport struct {
	transport policy.Transporter
	remaining int64
	warnings  []string
}

func (t *amwARMTransport) Do(request *http.Request) (*http.Response, error) {
	if err := request.Context().Err(); err != nil {
		return nil, err
	}
	if request.Method != http.MethodGet || request.URL.Scheme != "https" || request.URL.Host != "management.azure.com" || request.URL.User != nil {
		return nil, errors.New("AMW request outside read-only ARM endpoint blocked")
	}
	if t.remaining <= 0 {
		return nil, errAMWTotalLimit
	}
	response, err := t.transport.Do(request)
	if err != nil {
		return nil, err
	}
	if response == nil || response.Body == nil {
		return nil, errors.New("ARM response body missing")
	}
	limit := min(int64(amwMaxResponseBytes), t.remaining)
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	_ = response.Body.Close()
	t.remaining -= int64(len(body))
	if int64(len(body)) > limit {
		if t.remaining < 0 {
			return nil, errAMWTotalLimit
		}
		return nil, errAMWResponseLimit
	}
	if err != nil {
		return nil, err
	}
	if t.remaining == 0 {
		t.warnings = append(t.warnings, errAMWTotalLimit.Error())
	}
	if strings.HasSuffix(strings.ToLower(request.URL.Path), "/metricdefinitions") {
		var page struct {
			NextLink string `json:"nextLink"`
		}
		if json.Unmarshal(body, &page) == nil && page.NextLink != "" {
			t.warnings = append(t.warnings, request.URL.Path+": metric definitions returned a continuation; further pages unknown")
		}
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	return response, nil
}

// SDK errors can contain response bodies, URLs, headers, or credential output.
// Keep only safe, useful categories rather than persisting their Error() strings.
func amwRequestError(err error) string {
	switch {
	case errors.Is(err, errAMWResponseLimit):
		return errAMWResponseLimit.Error()
	case errors.Is(err, errAMWTotalLimit):
		return errAMWTotalLimit.Error()
	case errors.Is(err, context.Canceled):
		return "request canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "request deadline exceeded"
	}
	var responseError *azcore.ResponseError
	if errors.As(err, &responseError) {
		// Preserve service classification without serializing arbitrary response
		// bodies, request headers, or credential diagnostics.
		code := responseError.ErrorCode
		switch code {
		case "BadRequest", "InvalidRequest", "InvalidFilter", "InvalidMetric", "InvalidSamplingType", "AuthorizationFailed", "ResourceNotFound", "TooManyRequests":
			return fmt.Sprintf("ARM request failed (HTTP %d, code %s)", responseError.StatusCode, code)
		}
		return fmt.Sprintf("ARM request failed (HTTP %d)", responseError.StatusCode)
	}
	return "ARM request failed (credential, transport, or response decoding error)"
}

func amwValidateMetric(metric amwMetric, dimensions []string) string {
	response := metric.Response
	if response == nil {
		return "missing metrics response"
	}
	var issues []string
	if ptr.Deref(response.Interval, "") != "PT1M" {
		issues = append(issues, "response interval is not PT1M")
	}
	var matching []*armmonitor.Metric
	for _, value := range response.Value {
		if value != nil && value.Name != nil && ptr.Deref(value.Name.Value, "") == metric.Name {
			matching = append(matching, value)
		}
	}
	if len(matching) != 1 {
		issues = append(issues, "requested metric must be present exactly once")
		return strings.Join(issues, "; ")
	}
	value := matching[0]
	if (ptr.Deref(value.ErrorCode, "") != "" && !strings.EqualFold(*value.ErrorCode, "Success")) || ptr.Deref(value.ErrorMessage, "") != "" {
		issues = append(issues, "Azure reported an individual metric error; see raw response")
	}
	if len(value.Timeseries) == 0 {
		issues = append(issues, "empty timeseries; data unavailable, not zero")
	}
	if len(value.Timeseries) >= amwSeriesCap {
		issues = append(issues, "timeseries cap (1000) reached; data may be incomplete")
	}
	for _, series := range value.Timeseries {
		if series == nil {
			issues = append(issues, "nil timeseries")
			continue
		}
		for _, dimension := range dimensions {
			count := 0
			for _, metadata := range series.Metadatavalues {
				if metadata != nil && metadata.Name != nil && strings.EqualFold(ptr.Deref(metadata.Name.Value, ""), dimension) && ptr.Deref(metadata.Value, "") != "" {
					count++
				}
			}
			if count != 1 {
				issues = append(issues, "missing or duplicate dimension "+dimension)
			}
		}
		usable := false
		for _, point := range series.Data {
			if point == nil || point.TimeStamp == nil {
				continue
			}
			v := point.Maximum
			if metric.Aggregation == "Total" {
				v = point.Total
			}
			if v != nil && !math.IsNaN(*v) && !math.IsInf(*v, 0) {
				usable = true
			}
		}
		if !usable {
			issues = append(issues, "timeseries has no samples for requested aggregation; data unavailable, not zero")
		}
	}
	slices.Sort(issues)
	return strings.Join(slices.Compact(issues), "; ")
}
