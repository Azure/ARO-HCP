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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCostQueryWindows(t *testing.T) {
	from := time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	windows := costQueryWindows(from, to)
	if len(windows) != 1 || !windows[0].from.Equal(from) || !windows[0].to.Equal(to) {
		t.Fatalf("a twelve-month span must be one window: %+v", windows)
	}
	long := costQueryWindows(time.Date(2024, 11, 1, 0, 0, 0, 0, time.UTC), to)
	if len(long) != 2 {
		t.Fatalf("a two-year span must be split into two windows, got %+v", long)
	}
	if !long[0].to.Before(long[1].from) || long[0].to.Sub(long[0].from) >= 366*24*time.Hour {
		t.Fatalf("windows must be contiguous and within a year: %+v", long)
	}
	if !long[1].to.Equal(to) {
		t.Fatalf("the last window must end at the requested end: %+v", long[1])
	}
}

func TestCostQueryResultDetails(t *testing.T) {
	const response = `{"properties":{"columns":[
		{"name":"Cost"},{"name":"CostUSD"},{"name":"UsageDate"},
		{"name":"ServiceName"},{"name":"MeterSubCategory"},{"name":"Currency"}],
		"rows":[
		[99,12.5,20261027,"Virtual Machines","Virtual Machines Dsv5 Series","USD"],
		[0,7.5,20261027,"","  ","USD"],
		[0,3,20261028,"Storage","General Block Blob","USD"]]}}`
	var result costQueryResult
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		t.Fatal(err)
	}
	details, err := result.details([]string{costDimensionService, costDimensionUsage})
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 3 || details[0].CostUSD != 12.5 || details[0].Date.Format(time.DateOnly) != "2026-10-27" {
		t.Fatalf("unexpected details: %+v", details)
	}
	rows := costRowsFromDetails(details, []string{costDimensionService, costDimensionUsage}, costGranularityMonth)
	services, usages := rows[costGroupingService], rows[costGroupingUsage]
	if len(services) != 3 || len(usages) != 3 {
		t.Fatalf("every detail must produce one row per grouping: %+v", rows)
	}
	if services[1].Name != costUnassignedValue || usages[1].Name != costUnassignedValue {
		t.Fatalf("blank dimension values must be labeled %q: %+v", costUnassignedValue, services[1])
	}
	if regions := rows[costGroupingRegion]; regions != nil {
		t.Fatalf("a query without the region dimension must not produce region rows: %+v", regions)
	}
}

func TestCostQueryResultRegionAndMonthly(t *testing.T) {
	const response = `{"properties":{"columns":[
		{"name":"Cost"},{"name":"CostUSD"},{"name":"BillingMonth"},{"name":"ResourceLocation"},{"name":"Currency"}],
		"rows":[[0,5,"2026-09-01T00:00:00","West US 3","USD"],[0,2,"2026-09-01T00:00:00","EASTUS","USD"]]}}`
	var result costQueryResult
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		t.Fatal(err)
	}
	details, err := result.details([]string{costDimensionRegion})
	if err != nil {
		t.Fatal(err)
	}
	rows := costRowsFromDetails(details, []string{costDimensionRegion}, costGranularityMonth)
	regions := rows[costGroupingRegion]
	if len(regions) != 2 || regions[0].Date.Format("2006-01") != "2026-09" {
		t.Fatalf("unexpected monthly region rows: %+v", regions)
	}
	if regions[0].Name != "west us 3" || regions[1].Name != "eastus" {
		t.Fatalf("regions must be lowercased: %+v", regions)
	}
}

func TestCostQueryResultForecast(t *testing.T) {
	const response = `{"properties":{"columns":[
		{"name":"Cost"},{"name":"UsageDate"},{"name":"CostStatus"},{"name":"Currency"}],
		"rows":[
		[100,20261028,"Actual","USD"],
		[0,20261029,"Forecast","USD"],
		[20,20261030,"Forecast","USD"],
		[30,20261030,"Forecast","USD"],
		[40,20261031,"Forecast","USD"]]}}`
	var result costQueryResult
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		t.Fatal(err)
	}
	forecast, err := result.forecast()
	if err != nil {
		t.Fatal(err)
	}
	if len(forecast) != 3 {
		t.Fatalf("only forecast days must be returned: %+v", forecast)
	}
	// The parser groups by day, so look the day up rather than relying on order;
	// queryForecast sorts before returning.
	byDay := map[string]float64{}
	for _, row := range forecast {
		byDay[row.Date.Format(time.DateOnly)] += row.CostUSD
	}
	if len(byDay) != 3 || byDay["2026-10-30"] != 50 || byDay["2026-10-29"] != 0 || byDay["2026-10-31"] != 40 {
		t.Fatalf("forecast rows must sum per day: %+v", forecast)
	}
	if _, ok := byDay["2026-10-28"]; ok {
		t.Fatalf("actual cost must not be reported as forecast: %+v", forecast)
	}
}

func TestBuildCostChartRanksAndCombines(t *testing.T) {
	periods := []time.Time{
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
	}
	var rows []costRow
	for _, date := range periods {
		rows = append(rows,
			costRow{Granularity: costGranularityMonth, Date: date, Name: "a", CostUSD: 100},
			costRow{Granularity: costGranularityMonth, Date: date, Name: "b", CostUSD: 50},
			costRow{Granularity: costGranularityMonth, Date: date, Name: "c", CostUSD: 10},
			costRow{Granularity: costGranularityMonth, Date: date, Name: "d", CostUSD: 5},
		)
	}
	chart := buildCostChart(costGranularityMonth, costGroupingSub, periods, rows, map[time.Time]float64{periods[3]: 30}, 3, 2)
	if len(chart.Series) != 3 || chart.Series[0].Name != "a" || chart.Series[1].Name != "b" {
		t.Fatalf("largest groups keep their series: %+v", chart.Series)
	}
	other := chart.Series[2]
	if other.Name != costOtherSeries || !other.Other || other.Total != 60 || other.Values[0] != 15 {
		t.Fatalf("remaining groups must be combined into Other: %+v", other)
	}
	if chart.Periods[0] != "2026-01" || chart.Current != 3 {
		t.Fatalf("periods and current must be reported: %+v", chart)
	}
	if len(chart.ForecastUSD) != 4 || chart.ForecastUSD[3] != 30 || chart.ForecastUSD[0] != 0 {
		t.Fatalf("the forecast must align with periods: %+v", chart.ForecastUSD)
	}
	if totals := chart.Totals(); totals[0] != 165 || totals[3] != 165 {
		t.Fatalf("totals must sum the stacked series: %+v", totals)
	}
}

func TestBuildCostChartTrimsEmptyLeadingPeriods(t *testing.T) {
	periods := []time.Time{
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	}
	rows := []costRow{
		{Granularity: costGranularityMonth, Date: periods[1], Name: "a", CostUSD: 10},
		{Granularity: costGranularityMonth, Date: periods[2], Name: "a", CostUSD: 20},
	}
	chart := buildCostChart(costGranularityMonth, costGroupingService, periods, rows, nil, 2, 8)
	if len(chart.Periods) != 2 || chart.Periods[0] != "2026-02" || chart.Current != 1 {
		t.Fatalf("leading periods without data must be trimmed: %+v", chart)
	}
	if len(chart.ForecastUSD) != 2 {
		t.Fatalf("the forecast must be trimmed with the periods: %+v", chart.ForecastUSD)
	}
}

func TestBuildCostChartKeepsGranularitiesApart(t *testing.T) {
	periods := []time.Time{
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}
	rows := []costRow{
		{Granularity: costGranularityMonth, Date: periods[0], Name: "a", CostUSD: 100},
		{Granularity: costGranularityMonth, Date: periods[1], Name: "a", CostUSD: 200},
		// Daily detail covers the same months and must not be added on top.
		{Granularity: costGranularityDay, Date: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC), Name: "a", CostUSD: 100},
		{Granularity: costGranularityDay, Date: time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC), Name: "a", CostUSD: 200},
	}
	chart := buildCostChart(costGranularityMonth, costGroupingSub, periods, rows, nil, 1, 8)
	if totals := chart.Totals(); totals[0] != 100 || totals[1] != 200 {
		t.Fatalf("daily rows must not be counted as monthly costs: %+v", totals)
	}
}

func TestBuildCostChartWithoutPeriods(t *testing.T) {
	chart := buildCostChart(costGranularityMonth, costGroupingSub, nil, nil, nil, 0, 8)
	if len(chart.Periods) != 0 || len(chart.Series) != 0 {
		t.Fatalf("no periods must produce an empty chart: %+v", chart)
	}
}

func TestBuildCostsOverview(t *testing.T) {
	chart := CostChart{
		Periods:     []string{"2026-07", "2026-08", "2026-09", "2026-10"},
		Current:     3,
		Series:      []CostSeries{{Name: "a", Values: []float64{1000, 800, 700, 500}}},
		ForecastUSD: []float64{0, 0, 0, 600},
	}
	overview := buildCostsOverview(chart, 1000)
	if overview.MonthToDateUSD != 500 || overview.ForecastRemainingUSD != 600 || overview.ProjectedUSD != 1100 {
		t.Fatalf("unexpected month figures: %+v", overview)
	}
	if overview.PreviousMonthUSD != 700 || overview.TrailingAverageMonths != 3 || math.Abs(overview.TrailingAverageUSD-2500.0/3) > 1e-9 {
		t.Fatalf("the trailing average must cover the previous complete months: %+v", overview)
	}
	gap := buildCostsOverview(CostChart{
		Periods: []string{"2026-07", "2026-08", "2026-09", "2026-10"}, Current: 3,
		Series: []CostSeries{{Name: "a", Values: []float64{1000, 0, 700, 500}}},
	}, 0)
	if gap.TrailingAverageMonths != 1 || gap.TrailingAverageUSD != 700 {
		t.Fatalf("the trailing average must stop at the first month without cost data: %+v", gap)
	}
	// A period with a forecast but no actual cost is still one period in the chart.
	if overview.Status != "bad" || math.Abs(overview.ProjectionRatio-0.1) > 1e-9 {
		t.Fatalf("a projection over budget must be bad: %+v", overview)
	}
	unknown := buildCostsOverview(CostChart{}, 0)
	if unknown.Status != "unknown" {
		t.Fatalf("an empty chart has no status: %+v", unknown)
	}
	warning := buildCostsOverview(CostChart{
		Periods: []string{"2026-10"}, Current: 0,
		Series:      []CostSeries{{Name: "a", Values: []float64{950}}},
		ForecastUSD: []float64{0},
	}, 1000)
	if warning.Status != "warning" || warning.TrailingAverageMonths != 0 {
		t.Fatalf("a projection within 10%% of the budget must warn: %+v", warning)
	}
}

func TestUniqueCostSubscriptionNames(t *testing.T) {
	names := uniqueCostSubscriptionNames([]CostSubscription{
		{ID: "1", Name: "same"},
		{ID: "2", Name: "same"},
		{ID: "3", Name: "unique"},
		{ID: "4"},
	})
	if names["1"] != "same (1)" || names["2"] != "same (2)" || names["3"] != "unique" || names["4"] != "4" {
		t.Fatalf("shared display names must be qualified with the ID: %+v", names)
	}
}

func TestCostMonthsAndDays(t *testing.T) {
	now := time.Date(2026, 10, 28, 15, 4, 5, 0, time.UTC)
	months := costMonths(now, 3)
	if len(months) != 3 || months[0].Format("2006-01") != "2026-08" || months[2].Format("2006-01") != "2026-10" {
		t.Fatalf("unexpected months: %+v", months)
	}
	days := costDays(now, 2)
	if days[0].Format(time.DateOnly) != "2026-09-01" || days[len(days)-1].Format(time.DateOnly) != "2026-10-31" {
		t.Fatalf("days must span the months and include the rest of the current month: %v through %v", days[0], days[len(days)-1])
	}
	if index := costDayIndex(days, now); days[index].Format(time.DateOnly) != "2026-10-28" {
		t.Fatalf("the current day must be found: %d", index)
	}
}

func TestWriteCostsRoundTrip(t *testing.T) {
	costs := &Costs{
		Version:     CostSchemaVersion,
		CollectedAt: time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC),
		Month:       "2026-10",
		Subscriptions: []CostSubscription{
			{ID: "1", Name: "<script>alert(1)</script>"},
		},
		Charts: map[string]map[string]CostChart{
			costGranularityMonth: {costGroupingSub: {Granularity: costGranularityMonth, Grouping: costGroupingSub, Periods: []string{"2026-10"}, Current: 0}},
		},
	}
	path := filepath.Join(t.TempDir(), "costs.json")
	if err := WriteCosts(path, costs); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte("<script>")) {
		t.Fatal("subscription names must round trip as escaped JSON")
	}
	var decoded Costs
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Subscriptions[0].Name != costs.Subscriptions[0].Name || decoded.Charts[costGranularityMonth][costGroupingSub].Periods[0] != "2026-10" {
		t.Fatalf("costs must round trip: %+v", decoded)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o007 != 0 {
		t.Fatalf("cost data must stay private, got %v", info.Mode())
	}
}

func TestCostsDiagnostics(t *testing.T) {
	costs := &Costs{}
	if costs.HasErrors() {
		t.Fatal("a page without diagnostics has no errors")
	}
	costs.Diagnose("warning", "cost-forecast", "monthly forecast", "no history")
	if costs.HasErrors() {
		t.Fatal("warnings are not errors")
	}
	costs.Diagnose("error", "cost-query", "monthly costs", "denied")
	if !costs.HasErrors() {
		t.Fatal("an error diagnostic must mark the page incomplete")
	}
}

func TestCostQueryPostRetriesTransportFailure(t *testing.T) {
	attempts := 0
	client := &http.Client{Transport: billingTransport(func(*http.Request) (*http.Response, error) {
		attempts++
		if attempts < 2 {
			return nil, errors.New("connection reset by peer")
		}
		return billingResponse(http.StatusOK, `{"properties":{"columns":[],"rows":[]}}`), nil
	})}
	query := &costQueryClient{client: client, credential: billingCredential{}}
	if _, err := query.post(context.Background(), "https://management.azure.com/query", []byte("{}")); err != nil || attempts != 2 {
		t.Fatalf("a reset connection must be retried: %d attempts, %v", attempts, err)
	}

	// A returned HTTP status is an answer, not a transport failure: retrying it
	// would multiply the tenant's throttling instead of honoring it.
	attempts = 0
	client = &http.Client{Transport: billingTransport(func(*http.Request) (*http.Response, error) {
		attempts++
		return billingResponse(http.StatusBadRequest, `{"error":{"message":"Invalid query definition"}}`), nil
	})}
	query = &costQueryClient{client: client, credential: billingCredential{}}
	if _, err := query.post(context.Background(), "https://management.azure.com/query", []byte("{}")); err == nil || attempts != 1 || !strings.Contains(err.Error(), "Invalid query definition") {
		t.Fatalf("an HTTP error must not be retried and must keep its message: %d attempts, %v", attempts, err)
	}
}

func TestCostReferencesAreBudgetOnly(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	monthly := monthlyCostReferences(400000)
	if len(monthly) != 1 || monthly[0].Kind != "budget" || monthly[0].ValueUSD != 400000 {
		t.Fatalf("the monthly chart must draw only the budget line: %+v", monthly)
	}
	daily := dailyCostReferences(400000, now)
	if len(daily) != 1 || daily[0].Kind != "budget" || math.Abs(daily[0].ValueUSD-400000.0/31) > 1e-9 {
		t.Fatalf("the daily chart must draw only the daily budget line: %+v", daily)
	}
	// The trailing average is a summary figure; it must not appear as a line.
	if refs := monthlyCostReferences(0); refs != nil {
		t.Fatalf("no budget must mean no monthly reference line: %+v", refs)
	}
	if refs := dailyCostReferences(0, now); refs != nil {
		t.Fatalf("no budget must mean no daily reference line: %+v", refs)
	}
}
