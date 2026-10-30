// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// Cost Management Query API limits. A single query groups by at most two
// dimensions and covers at most twelve months, so longer windows are split.
const (
	costQueryAPIVersion    = "2023-11-01"
	subscriptionAPIVersion = "2022-12-01"
	costQueryMaxGroupings  = 2
	costQueryMaxMonths     = 12
	// costQueryBodyLimit bounds a response; grouped results for several
	// dimensions over a year are far smaller than this.
	costQueryBodyLimit = 64 << 20
	// costQueryAttempts is how often a throttled Query API call is retried.
	// Cost Management's tenant-wide quota is small enough that a burst of
	// subscriptions exceeds it.
	costQueryAttempts = 8
	// costForecastInterval spaces Forecast API calls. The forecast endpoint is
	// throttled per tenant far below the Query API and answers 429 without a
	// retry-after header, so calls are serialized and paced instead of burst.
	costForecastInterval = 12 * time.Second
	// costQueryTransportAttempts is how often a query is retried after a
	// connection-level failure.
	costQueryTransportAttempts = 3
)

// costQueryClient reads grouped amortized costs from the Query API and the rest
// of the month from the Forecast API. It shares the billing collector's
// throttling handling, which understands Cost Management's own retry-after
// headers.
type costQueryClient struct {
	client     *http.Client
	credential azcore.TokenCredential

	// forecastMu serializes forecast calls, and nextForecast is the earliest
	// time the next one may start.
	forecastMu   sync.Mutex
	nextForecast time.Time
}

// costQueryRow is one result row: a period, its raw value for each requested
// grouping dimension in request order, and its USD cost.
type costQueryRow struct {
	Date    time.Time
	Values  []string
	CostUSD float64
}

// queryGroupedCosts returns amortized USD cost per period and dimension value
// for [from, to], grouping by dimensions. Every returned detail belongs to
// exactly one value of each dimension, so summing a single dimension yields the
// subscription total.
func (c *costQueryClient) queryGroupedCosts(ctx context.Context, subscriptionID string, dimensions []string, granularity string, from, to time.Time) ([]costQueryRow, error) {
	if len(dimensions) == 0 || len(dimensions) > costQueryMaxGroupings {
		return nil, fmt.Errorf("a cost query groups by 1 to %d dimensions, got %d", costQueryMaxGroupings, len(dimensions))
	}
	endpoint := fmt.Sprintf("https://management.azure.com/subscriptions/%s/providers/Microsoft.CostManagement/query?api-version=%s", url.PathEscape(subscriptionID), costQueryAPIVersion)
	groupings := make([]costGrouping, len(dimensions))
	for i, dimension := range dimensions {
		groupings[i] = costGrouping{Type: "Dimension", Name: dimension}
	}
	var rows []costQueryRow
	for _, window := range costQueryWindows(from, to) {
		body := costQueryDefinition{
			Type:      "ActualCost",
			Timeframe: "Custom",
			TimePeriod: &costTimePeriod{
				From: window.from.Format(time.RFC3339),
				To:   window.to.Format(time.RFC3339),
			},
			Dataset: costDataset{
				Granularity: granularity,
				Aggregation: map[string]costAggregation{
					"totalCost":    {Name: "Cost", Function: "Sum"},
					"totalCostUSD": {Name: "CostUSD", Function: "Sum"},
				},
				Grouping: groupings,
			},
		}
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode cost query: %w", err)
		}
		next := endpoint
		for next != "" {
			result, err := c.post(ctx, next, encoded)
			if err != nil {
				return nil, fmt.Errorf("query %s costs by %s for subscription %s: %w", granularity, strings.Join(dimensions, ", "), subscriptionID, err)
			}
			details, err := result.details(dimensions)
			if err != nil {
				return nil, fmt.Errorf("parse %s costs by %s for subscription %s: %w", granularity, strings.Join(dimensions, ", "), subscriptionID, err)
			}
			rows = append(rows, details...)
			next = result.Properties.NextLink
		}
	}
	return rows, nil
}

// forecast returns the Forecast API's cost for each day of [from, to]. Unlike the
// Query API it reports the rest of a period that has not been billed yet, which
// is what the page needs for the unbilled days of the current month. Calls are
// serialized and spaced because a burst exceeds the tenant's forecast quota.
func (c *costQueryClient) forecast(ctx context.Context, subscriptionID string, from, to time.Time) ([]costQueryRow, error) {
	c.forecastMu.Lock()
	defer c.forecastMu.Unlock()
	if wait := time.Until(c.nextForecast); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, errors.New("billing request cancelled or timed out")
		case <-timer.C:
		}
	}
	rows, err := c.queryForecast(ctx, subscriptionID, from, to)
	c.nextForecast = time.Now().Add(costForecastInterval)
	return rows, err
}

// queryForecast issues one forecast request covering [from, to] and returns the
// forecast days it names, in date order.
func (c *costQueryClient) queryForecast(ctx context.Context, subscriptionID string, from, to time.Time) ([]costQueryRow, error) {
	endpoint := fmt.Sprintf("https://management.azure.com/subscriptions/%s/providers/Microsoft.CostManagement/forecast?api-version=%s", url.PathEscape(subscriptionID), costQueryAPIVersion)
	includeActual, includeFreshPartial := true, true
	body := costQueryDefinition{
		Type:      "ActualCost",
		Timeframe: "Custom",
		TimePeriod: &costTimePeriod{
			From: from.UTC().Format(time.RFC3339),
			To:   to.UTC().Format(time.RFC3339),
		},
		Dataset: costDataset{
			Granularity: "Daily",
			Aggregation: map[string]costAggregation{
				"totalCost":    {Name: "Cost", Function: "Sum"},
				"totalCostUSD": {Name: "CostUSD", Function: "Sum"},
			},
		},
		IncludeActualCost:       &includeActual,
		IncludeFreshPartialCost: &includeFreshPartial,
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode forecast query: %w", err)
	}
	var rows []costQueryRow
	next := endpoint
	for next != "" {
		result, err := c.post(ctx, next, encoded)
		if err != nil {
			return nil, fmt.Errorf("forecast costs for subscription %s: %w", subscriptionID, err)
		}
		forecast, err := result.forecast()
		if err != nil {
			return nil, fmt.Errorf("parse forecast for subscription %s: %w", subscriptionID, err)
		}
		rows = append(rows, forecast...)
		next = result.Properties.NextLink
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].Date.Before(rows[b].Date) })
	return rows, nil
}

// post issues one Query API request and decodes a 200 response. A transport
// failure is retried here: the reporting code fails fast, but a dropped cost
// query would remove a whole subscription's series from the page.
func (c *costQueryClient) post(ctx context.Context, target string, body []byte) (*costQueryResult, error) {
	var result *costQueryResult
	var err error
	for attempt := 0; attempt < costQueryTransportAttempts; attempt++ {
		if attempt > 0 {
			if err := billingWait(ctx, "", time.Second<<(attempt-1)); err != nil {
				return nil, err
			}
		}
		var transport bool
		if result, err, transport = c.postOnce(ctx, target, body); err == nil || !transport {
			return result, err
		}
	}
	return nil, err
}

// postOnce issues one request. transport reports a connection-level failure,
// which is worth retrying, as opposed to an HTTP status the API returned.
func (c *costQueryClient) postOnce(ctx context.Context, target string, body []byte) (result *costQueryResult, err error, transport bool) {
	resp, err := billingRequestAttempts(ctx, c.client, c.credential, http.MethodPost, target, string(body), true, costQueryAttempts)
	if err != nil {
		return nil, err, errors.Is(err, errBillingTransport)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// The API explains a rejected request (bad scope, unavailable forecast,
		// but never credentials) in a short OData error body.
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		detail := strings.Join(strings.Fields(string(message)), " ")
		if detail != "" {
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, detail), false
		}
		return nil, fmt.Errorf("HTTP %d; verify Cost Management Reader access on the subscription", resp.StatusCode), false
	}
	var decoded costQueryResult
	if err := json.NewDecoder(io.LimitReader(resp.Body, costQueryBodyLimit)).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode cost query response: %w", err), false
	}
	return &decoded, nil, false
}

// costQueryWindow is an inclusive [from, to] query period.
type costQueryWindow struct {
	from time.Time
	to   time.Time
}

// costQueryWindows splits [from, to] into consecutive month-aligned windows no
// longer than the Query API's twelve-month limit.
func costQueryWindows(from, to time.Time) []costQueryWindow {
	var windows []costQueryWindow
	for start := from; !start.After(to); {
		end := costMonthStart(start).AddDate(0, costQueryMaxMonths, 0).Add(-time.Second)
		if end.After(to) {
			end = to
		}
		windows = append(windows, costQueryWindow{from: start, to: end})
		start = end.Add(time.Second)
	}
	return windows
}

// costMonthStart returns midnight UTC on the first day of t's month.
func costMonthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// costNormalizeValue maps Azure's dimension values to stable labels, so rows
// differing only by case or blank values are aggregated together.
func costNormalizeValue(dimension, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return costUnassignedValue
	}
	if dimension == costDimensionRegion {
		return strings.ToLower(value)
	}
	return value
}

// The dimension names are the exact values the Cost Management Query API
// accepts, and they double as the grouping keys of the built charts.
const (
	costDimensionService = "ServiceName"
	costDimensionUsage   = "MeterSubCategory"
	costDimensionRegion  = "ResourceLocation"
	costUnassignedValue  = "Unassigned"
)

type costQueryDefinition struct {
	Type                    string          `json:"type"`
	Timeframe               string          `json:"timeframe"`
	TimePeriod              *costTimePeriod `json:"timePeriod,omitempty"`
	Dataset                 costDataset     `json:"dataset"`
	IncludeActualCost       *bool           `json:"includeActualCost,omitempty"`
	IncludeFreshPartialCost *bool           `json:"includeFreshPartialCost,omitempty"`
}

type costTimePeriod struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type costDataset struct {
	Granularity string                     `json:"granularity"`
	Aggregation map[string]costAggregation `json:"aggregation"`
	Grouping    []costGrouping             `json:"grouping,omitempty"`
}

type costAggregation struct {
	Name     string `json:"name"`
	Function string `json:"function"`
}

type costGrouping struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

type costQueryResult struct {
	Properties struct {
		NextLink string `json:"nextLink"`
		Columns  []struct {
			Name string `json:"name"`
		} `json:"columns"`
		Rows [][]json.RawMessage `json:"rows"`
	} `json:"properties"`
}

// columnIndex maps column names to their position in each row.
func (r costQueryResult) columnIndex() map[string]int {
	index := make(map[string]int, len(r.Properties.Columns))
	for i, column := range r.Properties.Columns {
		index[column.Name] = i
	}
	return index
}

// details parses one page of a grouped cost query.
func (r costQueryResult) details(dimensions []string) ([]costQueryRow, error) {
	index := r.columnIndex()
	period, err := r.periodParser()
	if err != nil {
		return nil, err
	}
	columns := make([]int, len(dimensions))
	for i, dimension := range dimensions {
		column, ok := index[dimension]
		if !ok {
			return nil, fmt.Errorf("result has no %s column", dimension)
		}
		columns[i] = column
	}
	rows := make([]costQueryRow, 0, len(r.Properties.Rows))
	for _, row := range r.Properties.Rows {
		date, err := period(row)
		if err != nil {
			return nil, err
		}
		values := make([]string, len(columns))
		for i, column := range columns {
			if values[i], err = costString(row, column); err != nil {
				return nil, err
			}
		}
		cost, err := costUSD(row, index)
		if err != nil {
			return nil, err
		}
		rows = append(rows, costQueryRow{Date: date, Values: values, CostUSD: cost})
	}
	return rows, nil
}

// forecast parses one page of a forecast response, summing all rows per day and
// keeping only the days Azure reports as Forecast. Rows for elapsed days are
// actual cost the monthly chart already has.
func (r costQueryResult) forecast() ([]costQueryRow, error) {
	index := r.columnIndex()
	status, ok := index["CostStatus"]
	if !ok {
		return nil, errors.New("forecast result has no CostStatus column")
	}
	date, ok := index["UsageDate"]
	if !ok {
		return nil, errors.New("forecast result has no UsageDate column")
	}
	days := map[time.Time]float64{}
	for _, row := range r.Properties.Rows {
		value, err := costString(row, status)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(value, "Forecast") {
			continue
		}
		day, err := costUsageDate(row, date)
		if err != nil {
			return nil, err
		}
		cost, err := costUSD(row, index)
		if err != nil {
			return nil, err
		}
		days[day] += cost
	}
	rows := make([]costQueryRow, 0, len(days))
	for day, cost := range days {
		rows = append(rows, costQueryRow{Date: day, CostUSD: cost})
	}
	return rows, nil
}

// periodParser returns a parser for the result's period column: BillingMonth
// for monthly queries or UsageDate for daily ones.
func (r costQueryResult) periodParser() (func(row []json.RawMessage) (time.Time, error), error) {
	index := r.columnIndex()
	if i, ok := index["BillingMonth"]; ok {
		return func(row []json.RawMessage) (time.Time, error) {
			value, err := costString(row, i)
			if err != nil {
				return time.Time{}, err
			}
			return costBillingMonth(value)
		}, nil
	}
	if i, ok := index["UsageDate"]; ok {
		return func(row []json.RawMessage) (time.Time, error) {
			return costUsageDate(row, i)
		}, nil
	}
	return nil, errors.New("result has neither a BillingMonth nor a UsageDate column")
}

// costUSD returns the row's cost in USD, preferring the explicit CostUSD column
// and otherwise requiring the billing currency to be USD.
func costUSD(row []json.RawMessage, index map[string]int) (float64, error) {
	if i, ok := index["CostUSD"]; ok {
		return costNumber(row, i)
	}
	i, ok := index["Cost"]
	if !ok {
		return 0, errors.New("result has neither a CostUSD nor a Cost column")
	}
	currency, err := costCurrency(row, index)
	if err != nil {
		return 0, err
	}
	if currency != "USD" {
		return 0, fmt.Errorf("unsupported billing currency %q without a CostUSD column", currency)
	}
	return costNumber(row, i)
}

func costCurrency(row []json.RawMessage, index map[string]int) (string, error) {
	i, ok := index["Currency"]
	if !ok {
		return "", errors.New("result has no Currency column")
	}
	return costString(row, i)
}

func costNumber(row []json.RawMessage, i int) (float64, error) {
	if i >= len(row) {
		return 0, fmt.Errorf("row has %d columns, expected at least %d", len(row), i+1)
	}
	var value float64
	if err := json.Unmarshal(row[i], &value); err != nil {
		return 0, fmt.Errorf("parse number %s: %w", string(row[i]), err)
	}
	return value, nil
}

func costString(row []json.RawMessage, i int) (string, error) {
	if i >= len(row) {
		return "", fmt.Errorf("row has %d columns, expected at least %d", len(row), i+1)
	}
	if string(row[i]) == "null" {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(row[i], &value); err != nil {
		return "", fmt.Errorf("parse string %s: %w", string(row[i]), err)
	}
	return value, nil
}

// costUsageDate parses the Query API's UsageDate values, which are numbers of
// the form YYYYMMDD.
func costUsageDate(row []json.RawMessage, i int) (time.Time, error) {
	if i >= len(row) {
		return time.Time{}, fmt.Errorf("row has %d columns, expected at least %d", len(row), i+1)
	}
	var value json.Number
	if err := json.Unmarshal(row[i], &value); err != nil {
		return time.Time{}, fmt.Errorf("parse usage date %s: %w", string(row[i]), err)
	}
	day, err := time.Parse("20060102", value.String())
	if err != nil {
		return time.Time{}, fmt.Errorf("parse usage date %s: %w", value.String(), err)
	}
	return day, nil
}

// costBillingMonth accepts the Query API's BillingMonth values, which are local
// timestamps (e.g. "2026-09-01T00:00:00") naming the first day of the month.
func costBillingMonth(value string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02T15:04:05", time.RFC3339, time.DateOnly} {
		if t, err := time.Parse(layout, value); err == nil {
			return costMonthStart(t), nil
		}
	}
	return time.Time{}, fmt.Errorf("parse billing month %q", value)
}
