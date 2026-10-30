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
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// CostSchemaVersion is the composite cost page schema written by this package.
const CostSchemaVersion = 1

// Chart settings. Monthly history is the longest window the charts show, daily
// detail is limited to the recent months that are useful at day granularity,
// and the largest groups keep their own series while the rest become Other.
const (
	costChartMonths      = 12
	costDailyMonths      = 3
	costTopSeries        = 8
	costTrailingMonths   = 3
	costOtherSeries      = "Other"
	costWarningRatio     = 0.9
	costGranularityMonth = "monthly"
	costGranularityDay   = "daily"
	costGroupingSub      = "subscription"
	costGroupingService  = "service"
	costGroupingUsage    = "usage"
	costGroupingRegion   = "region"
)

// CostGrouping is one stack-by choice for the composite cost chart.
type CostGrouping struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// CostGroupings lists the supported stack-by options in display order.
var CostGroupings = []CostGrouping{
	{Value: costGroupingSub, Label: "Subscription"},
	{Value: costGroupingService, Label: "Azure service"},
	{Value: costGroupingUsage, Label: "Usage type"},
	{Value: costGroupingRegion, Label: "Region"},
}

// costDimensionGrouping maps a queried dimension back to its stack-by choice.
func costDimensionGrouping(dimension string) string {
	switch dimension {
	case costDimensionUsage:
		return costGroupingUsage
	case costDimensionRegion:
		return costGroupingRegion
	default:
		return costGroupingService
	}
}

// CostSubscription identifies one charted Azure subscription.
type CostSubscription struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// CostOptions configures composite cost collection.
type CostOptions struct {
	// Months is how many calendar months, including the current one, are
	// charted and queried.
	Months int
	// DailyMonths is how many calendar months, including the current one, have
	// daily detail.
	DailyMonths int
	// MonthlyBudgetUSD is the monthly spend target; zero charts no budget line
	// and judges the projection against the trailing average instead.
	MonthlyBudgetUSD float64
	// Top is how many groups keep their own series; the rest become Other.
	Top int
	// Parallel bounds how many subscriptions are queried at once.
	Parallel int
}

// CostChart is one stacked bar chart: a granularity and a stack-by grouping.
type CostChart struct {
	Granularity string          `json:"granularity"` // monthly or daily
	Grouping    string          `json:"grouping"`
	Periods     []string        `json:"periods"` // 2006-01 for months, 2006-01-02 for days
	Current     int             `json:"current"` // index of the current month or day
	Series      []CostSeries    `json:"series"`  // largest first, Other last
	ForecastUSD []float64       `json:"forecastUSD"`
	References  []CostReference `json:"references,omitempty"`
}

// CostSeries is one stacked series: a group's cost for each charted period.
type CostSeries struct {
	Name   string    `json:"name"`
	Values []float64 `json:"values"`
	Total  float64   `json:"total"`
	Other  bool      `json:"other,omitempty"`
}

// CostReference is a horizontal reference line across the chart. The budget is
// the only one: the trailing average is a summary figure, not a chart overlay.
type CostReference struct {
	Kind     string  `json:"kind"` // budget
	ValueUSD float64 `json:"valueUSD"`
	Label    string  `json:"label"`
}

// CostsOverview is the current-month summary the page leads with. It is always
// monthly, whatever granularity is charted.
type CostsOverview struct {
	MonthToDateUSD        float64 `json:"monthToDateUSD"`
	ForecastRemainingUSD  float64 `json:"forecastRemainingUSD"`
	ProjectedUSD          float64 `json:"projectedUSD"`
	PreviousMonthUSD      float64 `json:"previousMonthUSD"`
	TrailingAverageUSD    float64 `json:"trailingAverageUSD"`
	TrailingAverageMonths int     `json:"trailingAverageMonths"`
	Status                string  `json:"status"` // good, warning, bad, or unknown
	ProjectionRatio       float64 `json:"projectionRatio"`
	// ForecastSubscriptions is how many subscriptions returned a forecast. Fewer
	// than all of them makes the projection a floor.
	ForecastSubscriptions int `json:"forecastSubscriptions"`
}

// Costs is the composite subscription cost page: monthly and daily amortized
// spend across every charted subscription, stacked by grouping, with a
// projection for the rest of the current month and the budget it is judged
// against.
type Costs struct {
	Version       int                `json:"version"`
	CollectedAt   time.Time          `json:"collectedAt"`
	Currency      string             `json:"currency"`
	CostBasis     string             `json:"costBasis"`
	Month         string             `json:"month"` // YYYY-MM of the projected month
	BudgetUSD     float64            `json:"budgetUSD"`
	Incomplete    bool               `json:"incomplete"`
	Overview      CostsOverview      `json:"overview"`
	Subscriptions []CostSubscription `json:"subscriptions"`
	// Groupings are the stack-by choices the page offers.
	Groupings []CostGrouping `json:"groupings"`
	// Charts is keyed by granularity, then by grouping.
	Charts      map[string]map[string]CostChart `json:"charts"`
	Diagnostics []Diagnostic                    `json:"diagnostics"`
}

// costRow is one group's cost in one month or day. The granularity keeps
// monthly and daily rows apart, so a daily series is never re-read as months.
type costRow struct {
	Granularity string
	Date        time.Time
	Name        string
	CostUSD     float64
}

// CollectCosts queries Cost Management for every subscription and builds the
// composite cost page. A subscription that fails is reported as a diagnostic
// and left out of the totals, so the page is then a known subtotal.
func CollectCosts(ctx context.Context, client *http.Client, credential azcore.TokenCredential, subscriptions []CostSubscription, options CostOptions, now time.Time) *Costs {
	now = now.UTC()
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	if options.Months <= 0 {
		options.Months = costChartMonths
	}
	if options.DailyMonths <= 0 {
		options.DailyMonths = costDailyMonths
	}
	if options.Top <= 0 {
		options.Top = costTopSeries
	}
	if options.Parallel <= 0 {
		options.Parallel = 2
	}
	costs := &Costs{
		Version:       CostSchemaVersion,
		CollectedAt:   now,
		Currency:      "USD",
		CostBasis:     "AmortizedCost",
		Month:         now.Format("2006-01"),
		BudgetUSD:     options.MonthlyBudgetUSD,
		Subscriptions: subscriptions,
		Groupings:     CostGroupings,
		Charts:        map[string]map[string]CostChart{},
	}
	costs.Diagnose("info", "billing-provisional", "costs", "Cost Management data is provisional: recent usage may arrive late and charges may be adjusted; complete means every subscription's queries succeeded, not that billing is final")

	months := costMonths(now, options.Months)
	days := costDays(now, options.DailyMonths)
	query := &costQueryClient{client: client, credential: credential}

	results := make([]costSubscriptionData, len(subscriptions))
	work := make(chan int)
	var wait sync.WaitGroup
	for worker := 0; worker < min(options.Parallel, len(subscriptions)); worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := range work {
				results[index] = collectSubscriptionCosts(ctx, query, subscriptions[index], months, days, now)
			}
		}()
	}
	for index := range subscriptions {
		work <- index
	}
	close(work)
	wait.Wait()

	rows := map[string][]costRow{}
	for _, result := range results {
		costs.Diagnostics = append(costs.Diagnostics, result.diagnostics...)
		for grouping, groupRows := range result.rows {
			rows[grouping] = append(rows[grouping], groupRows...)
		}
	}
	if len(subscriptions) == 0 {
		costs.Diagnose("error", "no-subscriptions", "costs", "No subscription was charted; check the subscription selection and Cost Management access")
	}

	names := uniqueCostSubscriptionNames(subscriptions)
	rows[costGroupingSub] = renameSubscriptionRows(rows[costGroupingSub], names)

	currentMonth := len(months) - 1
	currentDay := costDayIndex(days, now)
	// The unbilled rest of the month is Azure's forecast, summed across the
	// subscriptions that returned one. buildCostChart rolls the same daily
	// values up to a month, so one map feeds both granularities.
	forecastByDay := map[time.Time]float64{}
	forecastSubscriptions := 0
	for _, result := range results {
		if !result.forecasted {
			continue
		}
		forecastSubscriptions++
		for day, value := range result.forecast {
			forecastByDay[day] += value
		}
	}
	if forecastSubscriptions < len(subscriptions) && costMonthStart(now).AddDate(0, 1, -1).After(costDayStart(now)) {
		costs.Diagnose("warning", "cost-forecast-partial", "costs", fmt.Sprintf("Azure returned a forecast for %d of %d subscriptions; the rest of the month is a floor", forecastSubscriptions, len(subscriptions)))
	}

	costs.Charts[costGranularityMonth] = map[string]CostChart{}
	costs.Charts[costGranularityDay] = map[string]CostChart{}
	for _, grouping := range CostGroupings {
		monthly := buildCostChart(costGranularityMonth, grouping.Value, months, rows[grouping.Value], forecastByDay, currentMonth, options.Top)
		monthly.References = monthlyCostReferences(options.MonthlyBudgetUSD)
		costs.Charts[costGranularityMonth][grouping.Value] = monthly

		day := buildCostChart(costGranularityDay, grouping.Value, days, rows[grouping.Value], forecastByDay, currentDay, options.Top)
		day.References = dailyCostReferences(options.MonthlyBudgetUSD, now)
		costs.Charts[costGranularityDay][grouping.Value] = day
	}
	costs.Overview = buildCostsOverview(costs.Charts[costGranularityMonth][costGroupingSub], options.MonthlyBudgetUSD)
	costs.Overview.ForecastSubscriptions = forecastSubscriptions
	costs.Incomplete = costs.HasErrors()
	return costs
}

// costSubscriptionData is one subscription's contribution to the page.
type costSubscriptionData struct {
	rows map[string][]costRow
	// forecast is the subscription's daily cost for the unbilled rest of the
	// month; forecasted reports whether Azure returned one.
	forecast    map[time.Time]float64
	forecasted  bool
	diagnostics []Diagnostic
}

// collectSubscriptionCosts queries one subscription's monthly and daily costs by
// the queried dimensions. Two grouped queries per granularity cover all four
// stack-by choices, because a query may group by two dimensions at once.
func collectSubscriptionCosts(ctx context.Context, query *costQueryClient, subscription CostSubscription, months, days []time.Time, now time.Time) costSubscriptionData {
	data := costSubscriptionData{rows: map[string][]costRow{}}
	addDetails := func(details []costQueryRow, dimensions []string, granularity string) {
		for grouping, rows := range costRowsFromDetails(details, dimensions, granularity) {
			data.rows[grouping] = append(data.rows[grouping], rows...)
		}
	}

	requests := []struct {
		scope       string
		api         string
		granularity string
		from        time.Time
		dimensions  []string
	}{
		{scope: "monthly costs", api: "Monthly", granularity: costGranularityMonth, from: months[0], dimensions: []string{costDimensionService, costDimensionUsage}},
		{scope: "monthly regional costs", api: "Monthly", granularity: costGranularityMonth, from: months[0], dimensions: []string{costDimensionRegion}},
		{scope: "daily costs", api: "Daily", granularity: costGranularityDay, from: days[0], dimensions: []string{costDimensionService, costDimensionUsage}},
		{scope: "daily regional costs", api: "Daily", granularity: costGranularityDay, from: days[0], dimensions: []string{costDimensionRegion}},
	}
	for _, request := range requests {
		details, err := query.queryGroupedCosts(ctx, subscription.ID, request.dimensions, request.api, request.from, now)
		if err != nil {
			data.diagnostics = append(data.diagnostics, costSubscriptionDiagnostic("error", "cost-query", request.scope, subscription, err))
			continue
		}
		addDetails(details, request.dimensions, request.granularity)
		if len(request.dimensions) != 2 {
			continue
		}
		// Every detail belongs to exactly one service, so the same rows total
		// the subscription per period without a separate ungrouped query.
		totals := map[time.Time]float64{}
		for _, detail := range details {
			date := detail.Date.UTC()
			if request.granularity == costGranularityMonth {
				date = costMonthStart(date)
			} else {
				date = costDayStart(date)
			}
			totals[date] += detail.CostUSD
		}
		for date, total := range totals {
			data.rows[costGroupingSub] = append(data.rows[costGroupingSub], costRow{
				Granularity: request.granularity,
				Date:        date,
				Name:        subscription.ID,
				CostUSD:     total,
			})
		}
	}

	// The billed days are already covered; ask only for the rest of the month.
	// The Forecast API rejects a start date in the future, so the query starts
	// today and today's forecast row is dropped: today's actual cost is partial
	// and already part of the billed series.
	today, end := costDayStart(now), costMonthStart(now).AddDate(0, 1, -1)
	if !end.After(today) {
		return data
	}
	forecast, err := query.forecast(ctx, subscription.ID, today, end)
	if err != nil {
		data.diagnostics = append(data.diagnostics, costSubscriptionDiagnostic("warning", "cost-forecast", "cost forecast", subscription, err))
		return data
	}
	data.forecast = map[time.Time]float64{}
	for _, day := range forecast {
		key := costDayStart(day.Date)
		if !key.After(today) {
			continue
		}
		data.forecast[key] += day.CostUSD
	}
	if len(data.forecast) == 0 {
		// The Forecast API answers a subscription with nothing projected with an
		// empty result instead of zero-valued rows, so an empty answer is a zero
		// forecast; the diagnostic keeps that visible in costs.json.
		data.diagnostics = append(data.diagnostics, costSubscriptionDiagnostic("info", "cost-forecast-empty", "cost forecast", subscription, fmt.Errorf("no forecast rows for %s to %s; counted as zero", today.Format(time.DateOnly), end.Format(time.DateOnly))))
		data.forecasted = true
		return data
	}
	data.forecasted = true
	return data
}

// costRowsFromDetails expands grouped query details into one row per covered
// stack-by grouping, normalizing dimension values so values differing only by
// case or blanks aggregate together.
func costRowsFromDetails(details []costQueryRow, dimensions []string, granularity string) map[string][]costRow {
	rows := map[string][]costRow{}
	for _, detail := range details {
		if len(detail.Values) != len(dimensions) {
			continue
		}
		for i, dimension := range dimensions {
			grouping := costDimensionGrouping(dimension)
			rows[grouping] = append(rows[grouping], costRow{
				Granularity: granularity,
				Date:        detail.Date,
				Name:        costNormalizeValue(dimension, detail.Values[i]),
				CostUSD:     detail.CostUSD,
			})
		}
	}
	return rows
}

// costSubscriptionDiagnostic names the subscription in a collection diagnostic.
func costSubscriptionDiagnostic(severity, code, scope string, subscription CostSubscription, err error) Diagnostic {
	name := subscription.Name
	if name == "" {
		name = subscription.ID
	}
	return Diagnostic{
		Severity:       severity,
		Code:           code,
		Scope:          scope,
		Message:        fmt.Sprintf("%s: %v", name, err),
		SubscriptionID: subscription.ID,
	}
}

// uniqueCostSubscriptionNames labels each subscription, qualifying display
// names shared by several subscriptions with their ID.
func uniqueCostSubscriptionNames(subscriptions []CostSubscription) map[string]string {
	idsByName := map[string]map[string]bool{}
	for _, subscription := range subscriptions {
		name := subscription.Name
		if name == "" {
			name = subscription.ID
		}
		if idsByName[name] == nil {
			idsByName[name] = map[string]bool{}
		}
		idsByName[name][subscription.ID] = true
	}
	names := make(map[string]string, len(subscriptions))
	for _, subscription := range subscriptions {
		name := subscription.Name
		if name == "" {
			name = subscription.ID
		}
		if len(idsByName[name]) > 1 {
			name = fmt.Sprintf("%s (%s)", name, subscription.ID)
		}
		names[subscription.ID] = name
	}
	return names
}

// renameSubscriptionRows replaces subscription IDs with their display names.
func renameSubscriptionRows(rows []costRow, names map[string]string) []costRow {
	for i, row := range rows {
		if name, ok := names[row.Name]; ok {
			rows[i].Name = name
		}
	}
	return rows
}

// costMonths returns the first day of each of the count months ending with
// now's month, oldest first.
func costMonths(now time.Time, count int) []time.Time {
	current := costMonthStart(now)
	months := make([]time.Time, count)
	for i := range months {
		months[i] = current.AddDate(0, i-(count-1), 0)
	}
	return months
}

// costDays returns every day of the months ending with now's month,
// including the rest of the current month so its forecast can be drawn.
func costDays(now time.Time, months int) []time.Time {
	starts := costMonths(now, months)
	end := starts[len(starts)-1].AddDate(0, 1, 0)
	var days []time.Time
	for day := starts[0]; day.Before(end); day = day.AddDate(0, 0, 1) {
		days = append(days, day)
	}
	return days
}

// costDayStart returns midnight UTC on t's day.
func costDayStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// costDayIndex returns the index of now's day, defaulting to the last day.
func costDayIndex(days []time.Time, now time.Time) int {
	today := costDayStart(now)
	for i, day := range days {
		if day.Equal(today) {
			return i
		}
	}
	return len(days) - 1
}

// costPeriodKey formats a period for the chart's x axis.
func costPeriodKey(granularity string, period time.Time) string {
	if granularity == costGranularityDay {
		return period.Format(time.DateOnly)
	}
	return period.Format("2006-01")
}

// buildCostChart aligns rows and the forecast to periods, trims leading periods
// without any cost data (before collection began) but never past current, and
// ranks the series: the largest groups over the window keep their own series
// and the rest are combined into Other.
func buildCostChart(granularity, grouping string, periods []time.Time, rows []costRow, forecast map[time.Time]float64, current, top int) CostChart {
	if len(periods) == 0 {
		return CostChart{Granularity: granularity, Grouping: grouping}
	}
	index := make(map[time.Time]int, len(periods))
	for i, period := range periods {
		index[period] = i
	}
	byName := map[string]*CostSeries{}
	for _, row := range rows {
		if row.Granularity != granularity {
			continue
		}
		date := row.Date.UTC()
		if granularity == costGranularityMonth {
			date = costMonthStart(date)
		} else {
			date = costDayStart(date)
		}
		i, ok := index[date]
		if !ok {
			continue
		}
		series, ok := byName[row.Name]
		if !ok {
			series = &CostSeries{Name: row.Name, Values: make([]float64, len(periods))}
			byName[row.Name] = series
		}
		series.Values[i] += row.CostUSD
		series.Total += row.CostUSD
	}
	forecastValues := make([]float64, len(periods))
	for period, value := range forecast {
		key := period.UTC()
		if granularity == costGranularityMonth {
			key = costMonthStart(key)
		} else {
			key = costDayStart(key)
		}
		if i, ok := index[key]; ok {
			forecastValues[i] += value
		}
	}
	if current < 0 {
		current = 0
	}
	if current > len(periods)-1 {
		current = len(periods) - 1
	}

	first := current
	for i := 0; i < current; i++ {
		if forecastValues[i] != 0 {
			first = i
			break
		}
		found := false
		for _, series := range byName {
			if series.Values[i] != 0 {
				found = true
				break
			}
		}
		if found {
			first = i
			break
		}
	}

	all := make([]CostSeries, 0, len(byName))
	for _, series := range byName {
		series.Values = series.Values[first:]
		all = append(all, *series)
	}
	sort.Slice(all, func(a, b int) bool {
		if all[a].Total != all[b].Total {
			return all[a].Total > all[b].Total
		}
		return all[a].Name < all[b].Name
	})

	charted := periods[first:]
	series := all
	if len(all) > top {
		series = append([]CostSeries(nil), all[:top]...)
		other := CostSeries{Name: costOtherSeries, Values: make([]float64, len(charted)), Other: true}
		for _, rest := range all[top:] {
			for i, value := range rest.Values {
				other.Values[i] += value
			}
			other.Total += rest.Total
		}
		series = append(series, other)
	}
	chartPeriods := make([]string, len(charted))
	for i, period := range charted {
		chartPeriods[i] = costPeriodKey(granularity, period)
	}
	return CostChart{
		Granularity: granularity,
		Grouping:    grouping,
		Periods:     chartPeriods,
		Current:     current - first,
		Series:      series,
		ForecastUSD: forecastValues[first:],
	}
}

// Totals returns the actual cost of each charted period.
func (c CostChart) Totals() []float64 {
	totals := make([]float64, len(c.Periods))
	for _, series := range c.Series {
		for i, value := range series.Values {
			if i < len(totals) {
				totals[i] += value
			}
		}
	}
	return totals
}

// costTrailingAverage averages the actual totals of up to limit periods before
// current, stopping at the first period without cost data.
func costTrailingAverage(totals []float64, current, limit int) (float64, int) {
	var sum float64
	var count int
	for i := current - 1; i >= 0 && count < limit; i-- {
		if totals[i] <= 0 {
			break
		}
		sum += totals[i]
		count++
	}
	if count == 0 {
		return 0, 0
	}
	return sum / float64(count), count
}

// buildCostsOverview builds the current-month summary from the monthly chart.
func buildCostsOverview(monthly CostChart, budgetUSD float64) CostsOverview {
	overview := CostsOverview{Status: "unknown"}
	totals := monthly.Totals()
	if len(totals) == 0 || monthly.Current >= len(totals) {
		return overview
	}
	overview.MonthToDateUSD = totals[monthly.Current]
	if monthly.Current > 0 {
		overview.PreviousMonthUSD = totals[monthly.Current-1]
	}
	for _, value := range monthly.ForecastUSD {
		overview.ForecastRemainingUSD += value
	}
	overview.ProjectedUSD = overview.MonthToDateUSD + overview.ForecastRemainingUSD
	overview.TrailingAverageUSD, overview.TrailingAverageMonths = costTrailingAverage(totals, monthly.Current, costTrailingMonths)
	reference := budgetUSD
	if reference <= 0 {
		reference = overview.TrailingAverageUSD
	}
	switch {
	case reference <= 0:
		overview.Status = "unknown"
	case overview.ProjectedUSD > reference:
		overview.Status = "bad"
		overview.ProjectionRatio = overview.ProjectedUSD/reference - 1
	case overview.ProjectedUSD > reference*costWarningRatio:
		overview.Status = "warning"
		overview.ProjectionRatio = overview.ProjectedUSD/reference - 1
	default:
		overview.Status = "good"
		overview.ProjectionRatio = overview.ProjectedUSD/reference - 1
	}
	return overview
}

// monthlyCostReferences draws the monthly budget across the monthly chart. The
// trailing average stays a summary figure and is deliberately not a reference
// line, which keeps the chart to one unambiguous benchmark.
func monthlyCostReferences(budgetUSD float64) []CostReference {
	if budgetUSD <= 0 {
		return nil
	}
	return []CostReference{{Kind: "budget", ValueUSD: budgetUSD, Label: "Budget " + costUSDCompact(budgetUSD)}}
}

// dailyCostReferences spreads the monthly budget over the current month's days.
func dailyCostReferences(budgetUSD float64, now time.Time) []CostReference {
	if budgetUSD <= 0 {
		return nil
	}
	monthStart := costMonthStart(now)
	daysInMonth := monthStart.AddDate(0, 1, -1).Day()
	daily := budgetUSD / float64(daysInMonth)
	return []CostReference{{Kind: "budget", ValueUSD: daily, Label: "Daily budget " + costUSDCompact(daily)}}
}

// costUSDCompact formats a USD amount for a chart label, e.g. $400k.
func costUSDCompact(value float64) string {
	switch {
	case math.Abs(value) >= 1000000:
		return fmt.Sprintf("$%.1fM", value/1000000)
	case math.Abs(value) >= 1000:
		return fmt.Sprintf("$%.0fk", value/1000)
	default:
		return fmt.Sprintf("$%.0f", value)
	}
}

// WriteCosts writes the composite cost page data, replacing the file only when
// it is fully written.
func WriteCosts(path string, costs *Costs) error {
	return WriteFile(path, func(w io.Writer) error {
		encoder := json.NewEncoder(w)
		return encoder.Encode(costs)
	})
}

// ResolveCostSubscriptions returns the explicit subscriptions, or every enabled
// subscription the credential can read in tenantID (when set).
func ResolveCostSubscriptions(ctx context.Context, client *http.Client, credential azcore.TokenCredential, ids []string, tenantID string) ([]CostSubscription, []Diagnostic) {
	var subscriptions []CostSubscription
	var diagnostics []Diagnostic
	if len(ids) > 0 {
		for _, id := range ids {
			subscription, err := getCostSubscription(ctx, client, credential, id)
			if err != nil {
				diagnostics = append(diagnostics, Diagnostic{Severity: "warning", Code: "subscription-metadata", Scope: "subscriptions", Message: fmt.Sprintf("%s: %v", id, err), SubscriptionID: id})
				subscriptions = append(subscriptions, CostSubscription{ID: id})
				continue
			}
			subscriptions = append(subscriptions, subscription)
		}
		return subscriptions, diagnostics
	}
	next := fmt.Sprintf("https://management.azure.com/subscriptions?api-version=%s", subscriptionAPIVersion)
	for next != "" {
		resp, err := billingRequest(ctx, client, credential, http.MethodGet, next, "", true)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Severity: "error", Code: "subscription-discovery", Scope: "subscriptions", Message: err.Error()})
			return subscriptions, diagnostics
		}
		var page struct {
			Value []struct {
				SubscriptionID string `json:"subscriptionId"`
				DisplayName    string `json:"displayName"`
				State          string `json:"state"`
				TenantID       string `json:"tenantId"`
			} `json:"value"`
			NextLink string `json:"nextLink"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, costQueryBodyLimit)).Decode(&page)
		resp.Body.Close()
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Severity: "error", Code: "subscription-discovery", Scope: "subscriptions", Message: fmt.Sprintf("decode subscription list: %v", err)})
			return subscriptions, diagnostics
		}
		for _, entry := range page.Value {
			switch strings.ToLower(entry.State) {
			case "disabled", "deleted", "warned":
				continue
			}
			if tenantID != "" && !strings.EqualFold(entry.TenantID, tenantID) {
				continue
			}
			subscriptions = append(subscriptions, CostSubscription{ID: entry.SubscriptionID, Name: entry.DisplayName})
		}
		next = page.NextLink
	}
	sort.Slice(subscriptions, func(a, b int) bool { return subscriptions[a].Name < subscriptions[b].Name })
	return subscriptions, diagnostics
}

// getCostSubscription reads one subscription's display name.
func getCostSubscription(ctx context.Context, client *http.Client, credential azcore.TokenCredential, id string) (CostSubscription, error) {
	target := fmt.Sprintf("https://management.azure.com/subscriptions/%s?api-version=%s", url.PathEscape(id), subscriptionAPIVersion)
	resp, err := billingRequest(ctx, client, credential, http.MethodGet, target, "", true)
	if err != nil {
		return CostSubscription{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return CostSubscription{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var body struct {
		SubscriptionID string `json:"subscriptionId"`
		DisplayName    string `json:"displayName"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return CostSubscription{}, err
	}
	if body.SubscriptionID == "" {
		body.SubscriptionID = id
	}
	return CostSubscription{ID: body.SubscriptionID, Name: body.DisplayName}, nil
}

// Diagnose records a collection diagnostic on the composite cost page.
func (c *Costs) Diagnose(severity, code, scope, message string) {
	c.Diagnostics = append(c.Diagnostics, Diagnostic{Severity: severity, Code: code, Scope: scope, Message: message})
}

// HasErrors reports whether any diagnostic is an error.
func (c *Costs) HasErrors() bool {
	for _, diagnostic := range c.Diagnostics {
		if diagnostic.Severity == "error" {
			return true
		}
	}
	return false
}
