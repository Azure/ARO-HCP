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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/azure-kusto-go/azkustodata"
	"github.com/Azure/azure-kusto-go/azkustodata/kql"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/alertsmanagement/armalertsmanagement"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/test/util/timing"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/snapshot"
)

// workspaceEnv is the pseudo-workspace type used in persistent-environment
// (int/stg/prod) mode. Non-dev runs have no per-run management cluster and no
// Azure Monitor resource-group scope to list alerts by: their alerts fire from
// standing shared infrastructure and must be attributed through the run's
// per-test HCP cluster namespaces.
const workspaceEnv = "env"

const (
	// envQueryTimeout bounds each KQL hop of the attribution chain.
	envQueryTimeout = 5 * time.Minute

	// envAlertWindowPadding extends the alert query window past the run window
	// so alerts that fired late in a test but were ingested after the run's
	// end are still attributed. It is a KQL timespan literal.
	envAlertWindowPadding = "15m"
)

// envKustoConfig holds the environment Kusto connection resolved from the
// rendered environment config.
type envKustoConfig struct {
	querier      kustoQuerier
	endpoint     string
	serviceDB    string
	monitoringDB string
}

// setupEnvKusto resolves the environment Kusto connection info from the
// rendered environment config (the kusto.* keys gather-snapshot also reads)
// and opens a query client. The caller decides how to degrade when setup
// fails.
func setupEnvKusto(renderedConfig string, cred azcore.TokenCredential) (envKustoConfig, error) {
	rawCfg, err := os.ReadFile(renderedConfig)
	if err != nil {
		return envKustoConfig{}, fmt.Errorf("failed to read rendered config: %w", err)
	}
	jobConfig, err := snapshot.ParseConfig(rawCfg)
	if err != nil {
		return envKustoConfig{}, fmt.Errorf("failed to parse rendered config: %w", err)
	}
	if jobConfig.KustoName == "" || jobConfig.Region == "" {
		return envKustoConfig{}, fmt.Errorf("rendered config %s has no kusto.kustoName/kusto.location", renderedConfig)
	}
	endpoint, err := kusto.KustoEndpoint(jobConfig.KustoName, jobConfig.Region)
	if err != nil {
		return envKustoConfig{}, fmt.Errorf("failed to build kusto endpoint: %w", err)
	}
	client, err := azkustodata.New(azkustodata.NewConnectionStringBuilder(endpoint.String()).WithTokenCredential(cred))
	if err != nil {
		return envKustoConfig{}, fmt.Errorf("failed to create Kusto client for %s: %w", endpoint, err)
	}
	return envKustoConfig{
		querier:      &azKustoQuerier{client: client},
		endpoint:     endpoint.String(),
		serviceDB:    jobConfig.ServiceDatabase,
		monitoringDB: jobConfig.MonitoringEventsDatabase,
	}, nil
}

// kustoRow is one query result row keyed by column name. Values are the
// string representation of the Kusto value; queries project string columns.
type kustoRow map[string]string

// kustoQuerier abstracts read-only KQL execution so orchestration tests can
// fake the environment Kusto clusters.
type kustoQuerier interface {
	Query(ctx context.Context, db, query string) ([]kustoRow, error)
	Close() error
}

// azKustoQuerier runs KQL queries against a single Kusto cluster.
type azKustoQuerier struct {
	client *azkustodata.Client
}

func (q *azKustoQuerier) Query(ctx context.Context, db, query string) ([]kustoRow, error) {
	queryCtx, cancel := context.WithTimeout(ctx, envQueryTimeout)
	defer cancel()

	dataset, err := q.client.IterativeQuery(queryCtx, db, kql.New("").AddUnsafe(query))
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}
	primary := <-dataset.Tables()
	if err := primary.Err(); err != nil {
		return nil, fmt.Errorf("failed to get primary result: %w", err)
	}
	if primary.Table() == nil {
		return nil, nil
	}
	var rows []kustoRow
	for rowResult := range primary.Table().Rows() {
		if err := rowResult.Err(); err != nil {
			return nil, fmt.Errorf("failed to read row: %w", err)
		}
		row := rowResult.Row()
		if row == nil {
			continue
		}
		values := row.Values()
		r := make(kustoRow, len(values))
		for i, col := range row.Columns() {
			if i < len(values) {
				r[col.Name()] = values[i].String()
			}
		}
		rows = append(rows, r)
	}
	return rows, nil
}

func (q *azKustoQuerier) Close() error {
	return q.client.Close()
}

// kqlStringList renders a KQL string list literal, escaping each value for a
// single-quoted string.
func kqlStringList(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		v = strings.ReplaceAll(v, `\`, `\\`)
		quoted[i] = "'" + strings.ReplaceAll(v, `'`, `\'`) + "'"
	}
	return "(" + strings.Join(quoted, ",") + ")"
}

func kqlDatetime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// envClusterNamesQuery resolves the run's HCP cluster names from frontend
// request paths: every cluster operation goes through the frontend and the
// request path carries the (lowercase) ARM resource ID.
func envClusterNamesQuery(rgs []string, start, end time.Time) string {
	return fmt.Sprintf(`frontendLogs
| where timestamp between (datetime('%s') .. datetime('%s'))
| where resource_group in %s
| where request_path contains "hcpopenshiftclusters"
| extend cluster_name = extract(@"hcpopenshiftclusters/([a-z0-9-]+)", 1, tostring(request_path))
| where isnotempty(cluster_name)
| distinct cluster_name`,
		kqlDatetime(start), kqlDatetime(end), kqlStringList(rgs))
}

// envClusterCIDsQuery resolves each cluster's cid (its unique 32-char
// identifier). Filtering on resource group as well as name keeps a
// same-named cluster created by a concurrent run in the same window from
// leaking into this run's cid set.
func envClusterCIDsQuery(rgs, names []string, start, end time.Time) string {
	return fmt.Sprintf(`clustersServiceLogs
| where timestamp between (datetime('%s') .. datetime('%s'))
| where isnotempty(cid)
| extend rg = extract(@"/resourcegroups/([a-z0-9-]+)/", 1, tolower(tostring(resource_id)))
| where rg in %s
| extend cn = extract(@"/hcpopenshiftclusters/([a-z0-9-]+)", 1, tolower(tostring(resource_id)))
| where cn in %s
| distinct cid = tostring(cid)`,
		kqlDatetime(start), kqlDatetime(end), kqlStringList(rgs), kqlStringList(names))
}

// envAlertsQuery fetches the alerts fired on the run's cluster namespaces.
// The namespace is ocm-arohcp<env>-<cid>-<suffix> and the suffix is the
// cluster name only in int (it is random in stg/prod), so matching on the cid
// is the exact join. alertEvents records one row per state change, so rows
// are collapsed to each alert's latest state.
func envAlertsQuery(cids []string, start, end time.Time) string {
	return fmt.Sprintf(`alertEvents
| where firedDateTime between (datetime('%s') .. datetime('%s') + %s)
| extend alertName = tostring(alertContext.labels.alertname), ns = tostring(alertContext.labels.namespace)
| where ns has_any %s and isnotempty(alertName)
| extend lastChange = coalesce(resolvedDateTime, firedDateTime)
| summarize arg_max(lastChange, *) by alertId
| project alertName, severity, condition = monitorCondition, signalType, alertRule, targetResourceId,
    fired = tostring(firedDateTime), resolved = tostring(resolvedDateTime),
    labels = tostring(alertContext.labels), expression = tostring(alertContext.expression)`,
		kqlDatetime(start), kqlDatetime(end), envAlertWindowPadding, kqlStringList(cids))
}

// envSeverity maps an alertEvents severity ("Sev0".."Sev4") to the SDK
// severity. Unknown values map to Sev4 so they surface without escalating.
func envSeverity(s string) armalertsmanagement.Severity {
	if sev := armalertsmanagement.Severity(s); severityRank(sev) >= 0 {
		return sev
	}
	return armalertsmanagement.SeveritySev4
}

// parseEnvAlertLabels decodes the JSON of the alertContext.labels column.
// Non-string values are stringified; unparseable input yields nil.
func parseEnvAlertLabels(s string) map[string]string {
	var raw map[string]any
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return nil
	}
	labels := make(map[string]string, len(raw))
	for k, v := range raw {
		switch v := v.(type) {
		case nil:
		case string:
			labels[k] = v
		default:
			labels[k] = fmt.Sprintf("%v", v)
		}
	}
	return labels
}

func parseEnvTime(s string) *time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}

// envAlertsFromRows maps alertEvents rows into the alert shape used by the
// shared classification, JUnit and rendering pipeline.
func envAlertsFromRows(rows []kustoRow) []alert {
	alerts := make([]alert, 0, len(rows))
	for _, row := range rows {
		alerts = append(alerts, alert{
			Alert: alertData{
				Name:       row["alertName"],
				Severity:   envSeverity(row["severity"]),
				Condition:  row["condition"],
				AlertRule:  row["alertRule"],
				SignalType: row["signalType"],
				StartsAt:   parseEnvTime(row["fired"]),
				EndsAt:     parseEnvTime(row["resolved"]),
				Labels:     parseEnvAlertLabels(row["labels"]),
				Expression: row["expression"],
			},
			Metadata: alertMetadata{
				MonitoringWorkspace:     row["targetResourceId"],
				MonitoringWorkspaceType: workspaceEnv,
			},
		})
	}
	sortAlerts(alerts)
	return alerts
}

// collectEnvResourceGroups returns the sorted, deduplicated union of resource
// groups across all tests in the timing input.
func collectEnvResourceGroups(testTimingInfo map[string]timing.TimingInfo) []string {
	var rgs []string
	for _, ti := range testTimingInfo {
		rgs = append(rgs, ti.ResourceGroupNames...)
	}
	slices.Sort(rgs)
	return slices.Compact(rgs)
}

// collectEnvAlerts runs the attribution chain against the environment Kusto
// cluster: resource groups -> cluster names -> cids -> fired alerts.
func (o Options) collectEnvAlerts(ctx context.Context, rgs []string) ([]alert, error) {
	logger := logr.FromContextOrDiscard(ctx)
	start, end := o.TimeWindow.Start, o.TimeWindow.End

	nameRows, err := o.envKusto.querier.Query(ctx, o.envKusto.serviceDB, envClusterNamesQuery(rgs, start, end))
	if err != nil {
		return nil, fmt.Errorf("failed to resolve cluster names: %w", err)
	}
	names := sets.New[string]()
	for _, row := range nameRows {
		if name := row["cluster_name"]; name != "" {
			names.Insert(name)
		}
	}
	if names.Len() == 0 {
		return nil, fmt.Errorf("no HCP clusters found for %d resource group(s) within the time window", len(rgs))
	}

	cidRows, err := o.envKusto.querier.Query(ctx, o.envKusto.serviceDB, envClusterCIDsQuery(rgs, sets.List(names), start, end))
	if err != nil {
		return nil, fmt.Errorf("failed to resolve cluster cids: %w", err)
	}
	cids := sets.New[string]()
	for _, row := range cidRows {
		if cid := row["cid"]; cid != "" {
			cids.Insert(cid)
		}
	}
	if cids.Len() == 0 {
		return nil, fmt.Errorf("no cids found for %d HCP cluster(s)", names.Len())
	}

	alertRows, err := o.envKusto.querier.Query(ctx, o.envKusto.monitoringDB, envAlertsQuery(sets.List(cids), start, end))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch environment alerts: %w", err)
	}
	alerts := envAlertsFromRows(alertRows)
	logger.Info("fetched environment alerts", "resourceGroups", len(rgs), "clusters", names.Len(), "cids", cids.Len(), "alerts", len(alerts))
	return alerts, nil
}

// runEnv implements persistent-environment (int/stg/prod) collection and
// writes the same artifacts dev runs produce: alerts.json, junit_alerts.xml
// and observability-summary.html.
//
// Collection failures degrade gracefully: they surface in alerts.json and the
// JUnit output but do not fail the step, because the CI credential may not
// reach the environment Kusto clusters until a Microsoft-side credential
// grant lands. Un-annotated fired alerts still fail the step, matching the
// dev gate.
func (o Options) runEnv(ctx context.Context, deps gatherDependencies) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("logger not found in context: %w", err)
	}
	if o.envKusto.querier != nil {
		defer func() {
			if err := o.envKusto.querier.Close(); err != nil {
				logger.Error(err, "failed to close environment Kusto client")
			}
		}()
	}

	var alerts []alert
	var collectionErr error
	rgs := collectEnvResourceGroups(o.testTimingInfo)
	switch {
	case o.envKustoError != nil:
		collectionErr = fmt.Errorf("environment Kusto unavailable: %w", o.envKustoError)
	case len(rgs) == 0:
		collectionErr = fmt.Errorf("no test resource groups found in the timing input")
	default:
		logger.Info("collecting environment alerts", "kusto", o.envKusto.endpoint, "resourceGroups", len(rgs))
		alerts, collectionErr = o.collectEnvAlerts(ctx, rgs)
	}
	var collectionErrors []string
	if collectionErr != nil {
		logger.Error(collectionErr, "environment alert collection incomplete")
		collectionErrors = []string{fmt.Sprintf("%s: %v", workspaceEnv, collectionErr)}
	}

	alerts = classifyAlerts(filterAlertsBySeverity(alerts, o.SeverityThreshold), o.knownIssues)
	// The environment Kusto chain has no rule inventory, so every fired alert
	// name becomes a rule: the JUnit gets one case per fired alert instead of
	// "did not fire" cases for every rule.
	rules := sets.New[string]()
	for _, a := range alerts {
		rules.Insert(a.Alert.Name)
	}
	workspaces := map[string]*workspaceData{workspaceEnv: {
		Type:            workspaceEnv,
		AlertRules:      sets.List(rules),
		FiredAlerts:     alerts,
		CollectionError: errors.Join(collectionErr, o.knownIssuesError),
	}}
	output := buildAlertsOutput(alerts, o.TimeWindow, collectionErrors)

	// Artifact and known-issues failures stay fatal, like dev mode: without
	// the known-issues file every firing would count as un-annotated.
	var fatalErrors []error
	fail := func(err error) {
		if err != nil {
			logger.Error(err, "observability collection incomplete")
			fatalErrors = append(fatalErrors, err)
		}
	}
	fail(o.knownIssuesError)
	fail(writeJSONArtifact(logger, deps, o.OutputDir, "alerts.json", output))

	junitPath := filepath.Join(o.OutputDir, "junit_alerts.xml")
	if err := deps.writeJUnit(junitPath, alertsToJUnit(logger, workspaces, o.TimeWindow)); err != nil {
		fail(fmt.Errorf("failed to write JUnit output: %w", err))
	}

	alertsHTML, err := deps.renderAlerts(output)
	if err != nil {
		fail(fmt.Errorf("failed to render alerts HTML: %w", err))
	}
	tabs := []observabilityTab{{Title: "Environment Alerts", HTML: string(incompleteHTML(alertsHTML, err))}}
	if err := deps.renderPage(filepath.Join(o.OutputDir, "observability-summary.html"), tabs); err != nil {
		fail(fmt.Errorf("failed to render observability HTML: %w", err))
	}

	if output.Summary.Unknown > 0 {
		fail(fmt.Errorf("%d un-annotated alert firing(s) on the run's clusters", output.Summary.Unknown))
	}
	return utils.TrackError(errors.Join(fatalErrors...))
}
