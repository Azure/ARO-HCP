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
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/test/util/alertdiagnostics"
)

const alertDiagnosticsTimeout = 10 * time.Minute

type alertDiagnosticsReport struct {
	SchemaVersion int                    `json:"schemaVersion"`
	Start         time.Time              `json:"start"`
	End           time.Time              `json:"end"`
	GeneratedAt   time.Time              `json:"generatedAt"`
	Alerts        []alertDiagnostic      `json:"alerts"`
	Queries       []alertDiagnosticQuery `json:"queries"`
}

type alertDiagnostic struct {
	Charts  []alertDiagnosticChart `json:"charts"`
	Error   string                 `json:"error,omitempty"`
	Warning string                 `json:"warning,omitempty"`
}

type alertDiagnosticChart struct {
	Expression string                       `json:"expression"`
	Path       string                       `json:"path"`
	Queries    []alertDiagnosticReference   `json:"queries"`
	Thresholds []alertdiagnostics.Threshold `json:"thresholds,omitempty"`
	Fallback   string                       `json:"fallback,omitempty"`
}

type alertDiagnosticReference struct {
	Result int    `json:"result"`
	Role   string `json:"role"`
}

type alertDiagnosticQuery struct {
	Expression string             `json:"expression"`
	Workspace  string             `json:"workspace"`
	Step       string             `json:"step"`
	Series     []PrometheusResult `json:"series"`
	Error      string             `json:"error,omitempty"`
	Warnings   []string           `json:"warnings,omitempty"`
}

// The report owns each query result once; alert cards retain independent firing
// intervals and reference shared data. Diagnostic errors never classify alerts.
func (o Options) collectAlertDiagnostics(ctx context.Context, alerts []alert, workspaces map[string]*workspaceData, deps gatherDependencies, now time.Time) *alertDiagnosticsReport {
	ctx, cancel := context.WithTimeout(ctx, alertDiagnosticsTimeout)
	defer cancel()
	end := o.TimeWindow.End
	if end.After(now) {
		end = now
	}
	report := &alertDiagnosticsReport{
		SchemaVersion: 1, Start: o.TimeWindow.Start.UTC(), End: end.UTC(), GeneratedAt: now.UTC(),
		Alerts: make([]alertDiagnostic, len(alerts)), Queries: []alertDiagnosticQuery{},
	}
	type queryKey struct{ workspace, expression, step string }
	seen := map[queryKey]int{}
	for i, a := range alerts {
		entry := &report.Alerts[i]
		if !end.After(report.Start) {
			entry.Error = "no elapsed time in the report window"
			continue
		}
		if strings.TrimSpace(a.Alert.Expression) == "" {
			entry.Error = "alert has no PromQL expression; automatic metric history is unavailable"
			continue
		}
		wsType := a.Metadata.MonitoringWorkspaceType
		ws := workspaces[wsType]
		if ws == nil || ws.PromEndpoint == "" || ws.PromError != nil {
			entry.Error = "Prometheus workspace endpoint unavailable"
			if ws != nil && ws.PromError != nil {
				entry.Error += ": " + ws.PromError.Error()
			}
			continue
		}
		step, warning := diagnosticStep(a, ws.RuleDefinitions)
		entry.Warning = warning
		plan, err := alertdiagnostics.Extract(a.Alert.Expression)
		if err != nil {
			// A backend may understand syntax newer than this binary's parser.
			plan.Conditions = []alertdiagnostics.Condition{{Expression: a.Alert.Expression, Path: "root", Unsupported: err.Error()}}
		}
		for _, condition := range plan.Conditions {
			chart := alertDiagnosticChart{Expression: condition.Expression, Path: condition.Path, Thresholds: condition.Thresholds}
			queries := condition.Queries
			if condition.Unsupported != "" {
				chart.Fallback = "Alert expression output; underlying signal could not be extracted: " + condition.Unsupported
				queries = []alertdiagnostics.Query{{Expression: condition.Expression, Role: "signal"}}
			}
			for _, query := range queries {
				key := queryKey{wsType, query.Expression, step}
				index, ok := seen[key]
				if !ok {
					index = len(report.Queries)
					seen[key] = index
					report.Queries = append(report.Queries, alertDiagnosticQuery{Expression: query.Expression, Workspace: wsType, Step: step})
				}
				chart.Queries = append(chart.Queries, alertDiagnosticReference{Result: index, Role: query.Role})
			}
			entry.Charts = append(entry.Charts, chart)
		}
	}
	client := &http.Client{Timeout: 30 * time.Second}
	logger := logr.FromContextOrDiscard(ctx)
	// Fixed workers bound both requests and goroutines. Each owns distinct entries.
	var workers sync.WaitGroup
	for worker := 0; worker < 2; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := worker; index < len(report.Queries); index += 2 {
				query := &report.Queries[index]
				if err := ctx.Err(); err != nil {
					query.Error = "diagnostics not collected: " + err.Error()
					continue
				}
				response, err := deps.queryRange(ctx, client, o.cred, workspaces[query.Workspace].PromEndpoint, query.Expression, report.Start, report.End, query.Step)
				if err == nil && (response == nil || response.Data.ResultType != "matrix") {
					err = fmt.Errorf("expected a Prometheus matrix response")
				}
				if err != nil {
					query.Error = err.Error()
					logger.Error(err, "alert diagnostic query failed", "workspace", query.Workspace, "query", query.Expression)
					continue
				}
				query.Series = response.Data.Result
				query.Warnings = append(response.Warnings, response.Infos...)
			}
		}()
	}
	workers.Wait()
	return report
}
