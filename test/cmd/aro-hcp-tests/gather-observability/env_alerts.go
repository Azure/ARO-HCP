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

	azkustodata "github.com/Azure/azure-kusto-go/azkustodata"
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
	// end are still attributed. It is a KQL timespan literal, not a
	// time.Duration, so it can be interpolated directly into the query.
	envAlertWindowPadding = "15m"
)

// envCluster is one HCP cluster created by the run, with the attribution
// identifiers resolved from the environment Kusto.
type envCluster struct {
	ResourceGroup string `json:"resourceGroup"`
	ClusterName   string `json:"clusterName"`
	CID           string `json:"cid,omitempty"`
	Namespace     string `json:"namespace,omitempty"`
}

// envClusterInventory is the durable per-run cluster inventory artifact
// (run-clusters.json). Test cleanup deletes the resource groups during the
// run, so this file is the only post-hoc record of the
// resource group -> cluster -> cid -> namespace attribution chain.
type envClusterInventory struct {
	KustoEndpoint string       `json:"kustoEndpoint"`
	TimeWindow    timeWindow   `json:"timeWindow"`
	Clusters      []envCluster `json:"clusters"`
}

// envKustoConfig holds the environment Kusto connection resolved from the
// rendered environment config.
type envKustoConfig struct {
	querier      kustoQuerier
	endpoint     string
	serviceDB    string
	monitoringDB string
}

// setupEnvKusto resolves the environment Kusto connection info from the
// rendered environment config (the same kusto.kustoName/kusto.location keys
// gather-snapshot reads) and opens a query client. The caller decides how to
// degrade when setup fails.
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
	kcsb := azkustodata.NewConnectionStringBuilder(endpoint.String()).WithTokenCredential(cred)
	client, err := azkustodata.New(kcsb)
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

// kustoRow is one query result row keyed by column name. Every value is the
// string representation of the underlying Kusto value; queries are expected
// to cast/project their columns to strings.
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
	table := primary.Table()
	if table == nil {
		return nil, nil
	}
	var rows []kustoRow
	for rowResult := range table.Rows() {
		if err := rowResult.Err(); err != nil {
			return nil, fmt.Errorf("failed to read row: %w", err)
		}
		row := rowResult.Row()
		if row == nil {
			continue
		}
		columns := row.Columns()
		values := row.Values()
		r := make(kustoRow, len(columns))
		for i, col := range columns {
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

// kqlEscapeString escapes a value for use inside a KQL single-quoted string.
func kqlEscapeString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `'`, `\'`)
}

// kqlStringList renders a KQL string list literal from values.
func kqlStringList(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = fmt.Sprintf("'%s'", kqlEscapeString(v))
	}
	return "(" + strings.Join(quoted, ",") + ")"
}

func kqlDatetime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05Z")
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
| distinct cluster_name, resource_group`,
		kqlDatetime(start), kqlDatetime(end), kqlStringList(rgs))
}

// envClusterCIDsQuery resolves the cid (the cluster's unique 32-char
// identifier) per cluster name. Filtering on resource group and name keeps a
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
| distinct cn, tostring(cid)`,
		kqlDatetime(start), kqlDatetime(end), kqlStringList(rgs), kqlStringList(names))
}

// envAlertsQuery fetches alertEvents fired on the run's cluster namespaces.
// The namespace is ocm-arohcp<env>-<cid>-<suffix> and the suffix is the
// cluster name only in int (it is random in stg/prod), so `has_any` on the
// cid is the exact join. Columns beyond the validated ones
// (firedDateTime/resolvedDateTime/alertContext) are read through
// column_ifexists so an alertEvents schema change degrades to empty values
// instead of failing the whole query.
func envAlertsQuery(cids []string, start, end time.Time) string {
	return fmt.Sprintf(`alertEvents
| where firedDateTime between (datetime('%s') .. datetime('%s') + %s)
| extend alertName = tostring(alertContext.labels.alertname), ns = tostring(alertContext.labels.namespace)
| where ns has_any %s and isnotempty(alertName)
| extend fired = format_datetime(firedDateTime, "yyyy-MM-ddTHH:mm:ssZ")
| extend resolved = iff(isnull(resolvedDateTime), '', format_datetime(todatetime(resolvedDateTime), "yyyy-MM-ddTHH:mm:ssZ"))
| project alertName,
    severity = tostring(column_ifexists('severity', '')),
    condition = coalesce(tostring(column_ifexists('monitorCondition', '')), tostring(column_ifexists('state', ''))),
    signalType = tostring(column_ifexists('signalType', '')),
    alertRule = tostring(column_ifexists('ruleName', '')),
    targetResourceId = tostring(column_ifexists('targetResourceId', '')),
    fired, resolved,
    labels = tostring(alertContext.labels),
    expression = tostring(alertContext.expression)`,
		kqlDatetime(start), kqlDatetime(end), envAlertWindowPadding, kqlStringList(cids))
}

// envSeverity maps an alertEvents severity string ("Sev0".."Sev4") to the SDK
// severity used across the shared pipeline. Unknown or missing values map to
// Sev4 (verbose) so they are still surfaced but never escalated.
func envSeverity(s string) armalertsmanagement.Severity {
	if sev := armalertsmanagement.Severity(s); severityRank(sev) >= 0 {
		return sev
	}
	return armalertsmanagement.SeveritySev4
}

// parseEnvAlertLabels decodes the JSON representation of the alertContext
// labels dynamic column. Non-string values are stringified; unparseable input
// yields nil.
func parseEnvAlertLabels(s string) map[string]string {
	if s == "" {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return nil
	}
	labels := make(map[string]string, len(raw))
	for k, v := range raw {
		if v == nil {
			continue
		}
		if str, ok := v.(string); ok {
			labels[k] = str
		} else {
			labels[k] = fmt.Sprintf("%v", v)
		}
	}
	return labels
}

// envAlertsFromRows maps alertEvents rows into the alert shape used by the
// shared classification, JUnit and rendering pipeline.
func envAlertsFromRows(rows []kustoRow) []alert {
	alerts := make([]alert, 0, len(rows))
	for _, row := range rows {
		var a alertData
		a.Name = row["alertName"]
		a.Severity = envSeverity(row["severity"])
		a.Condition = row["condition"]
		a.AlertRule = row["alertRule"]
		a.SignalType = row["signalType"]
		if t, err := time.Parse(time.RFC3339, row["fired"]); err == nil {
			a.StartsAt = &t
		}
		if row["resolved"] != "" {
			if t, err := time.Parse(time.RFC3339, row["resolved"]); err == nil {
				a.EndsAt = &t
			}
		}
		a.Labels = parseEnvAlertLabels(row["labels"])
		a.Expression = row["expression"]
		alerts = append(alerts, alert{
			Alert: a,
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

// collectEnvAlerts runs the three-hop attribution chain against the
// environment Kusto cluster: resource groups -> cluster names -> cids ->
// fired alerts. The cluster list is returned even when the alert hop fails so
// the inventory artifact can still be written.
func (o Options) collectEnvAlerts(ctx context.Context, rgs []string) ([]envCluster, []alert, error) {
	logger := logr.FromContextOrDiscard(ctx)

	type clusterKey struct{ rg, name string }
	clusters := map[clusterKey]envCluster{}
	byName := map[string][]clusterKey{}

	nameRows, err := o.envKusto.querier.Query(ctx, o.envKusto.serviceDB, envClusterNamesQuery(rgs, o.TimeWindow.Start, o.TimeWindow.End))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to resolve cluster names: %w", err)
	}
	for _, row := range nameRows {
		name, rg := row["cluster_name"], row["resource_group"]
		if name == "" || rg == "" {
			continue
		}
		key := clusterKey{rg: rg, name: name}
		if _, ok := clusters[key]; ok {
			continue
		}
		clusters[key] = envCluster{ResourceGroup: rg, ClusterName: name}
		byName[name] = append(byName[name], key)
	}
	logger.Info("resolved run clusters", "resourceGroups", len(rgs), "clusters", len(clusters))
	if len(clusters) == 0 {
		return nil, nil, fmt.Errorf("no HCP clusters found for the run's resource groups within the time window")
	}

	var names []string
	for name := range byName {
		names = append(names, name)
	}
	slices.Sort(names)

	cidRows, err := o.envKusto.querier.Query(ctx, o.envKusto.serviceDB, envClusterCIDsQuery(rgs, names, o.TimeWindow.Start, o.TimeWindow.End))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to resolve cluster cids: %w", err)
	}
	var cids []string
	seenCID := map[string]bool{}
	for _, row := range cidRows {
		cn, cid := row["cn"], row["cid"]
		if cn == "" || cid == "" {
			continue
		}
		for _, key := range byName[cn] {
			c := clusters[key]
			c.CID = cid
			clusters[key] = c
		}
		if !seenCID[cid] {
			seenCID[cid] = true
			cids = append(cids, cid)
		}
	}
	logger.Info("resolved cluster cids", "cids", len(cids))

	var alerts []alert
	if len(cids) > 0 {
		alertRows, err := o.envKusto.querier.Query(ctx, o.envKusto.monitoringDB, envAlertsQuery(cids, o.TimeWindow.Start, o.TimeWindow.End))
		if err != nil {
			return envClusterList(clusters), nil, fmt.Errorf("failed to fetch environment alerts: %w", err)
		}
		alerts = envAlertsFromRows(alertRows)
		// Best-effort namespace resolution for the inventory: the full
		// namespace is only visible on the alerts themselves.
		for _, a := range alerts {
			ns := a.Alert.Labels["namespace"]
			if ns == "" {
				continue
			}
			for key, c := range clusters {
				if c.CID != "" && c.Namespace == "" && strings.Contains(ns, c.CID) {
					c.Namespace = ns
					clusters[key] = c
				}
			}
		}
	}
	logger.Info("fetched environment alerts", "alerts", len(alerts))
	return envClusterList(clusters), alerts, nil
}

func envClusterList[K comparable](clusters map[K]envCluster) []envCluster {
	list := make([]envCluster, 0, len(clusters))
	for _, c := range clusters {
		list = append(list, c)
	}
	slices.SortFunc(list, func(a, b envCluster) int {
		if c := strings.Compare(a.ResourceGroup, b.ResourceGroup); c != 0 {
			return c
		}
		return strings.Compare(a.ClusterName, b.ClusterName)
	})
	return list
}

// runEnv implements persistent-environment (int/stg/prod) collection and
// writes the same artifacts dev runs produce (alerts.json, junit_alerts.xml,
// observability-summary.html) plus run-clusters.json.
//
// Collection failures degrade gracefully: the cluster inventory and any
// partial alert data are still written and the errors surface in alerts.json
// and the JUnit output, but the step does not fail on a missing source — the
// CI credential may not be able to reach the environment Kusto clusters
// until a Microsoft-side credential grant lands. The step still fails when
// collection succeeded and un-annotated alerts fired, matching the dev gate.
//
// Unlike dev mode the JUnit only contains cases for alerts that actually
// fired (plus a collection-completeness case): the environment Kusto chain
// has no rule inventory to assert "did not fire" cases for.
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

	var collectionErrs []error
	record := func(err error) {
		if err != nil {
			logger.Error(err, "environment alert collection incomplete")
			collectionErrs = append(collectionErrs, err)
		}
	}
	// Artifact write/render failures stay fatal, like dev mode; only
	// collection failures degrade gracefully.
	var fatalErrors []error
	fail := func(err error) {
		if err != nil {
			logger.Error(err, "observability artifact failure")
			fatalErrors = append(fatalErrors, err)
		}
	}

	rgs := collectEnvResourceGroups(o.testTimingInfo)
	if len(rgs) == 0 {
		record(fmt.Errorf("no test resource groups found in the timing input; cannot attribute environment alerts"))
	}

	var clusters []envCluster
	var alerts []alert
	switch {
	case o.envKustoError != nil:
		record(fmt.Errorf("environment Kusto unavailable: %w", o.envKustoError))
	case len(rgs) > 0:
		clusters, alerts, err = o.collectEnvAlerts(ctx, rgs)
		record(err)
	}

	writeJSON := func(name string, data any) error {
		content, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal %s: %w", name, err)
		}
		path := filepath.Join(o.OutputDir, name)
		if err := deps.writeFile(path, content, 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", path, err)
		}
		logger.Info("wrote JSON artifact", "path", path)
		return nil
	}

	// The inventory artifact is durable evidence of the attribution chain:
	// test cleanup deletes the resource groups during the run.
	inventory := envClusterInventory{
		KustoEndpoint: o.envKusto.endpoint,
		TimeWindow: timeWindow{
			Start: o.TimeWindow.Start.UTC().Format(time.RFC3339),
			End:   o.TimeWindow.End.UTC().Format(time.RFC3339),
		},
		Clusters: clusters,
	}
	if inventory.Clusters == nil {
		inventory.Clusters = []envCluster{}
	}
	fail(writeJSON("run-clusters.json", inventory))

	alerts = filterAlertsBySeverity(alerts, o.SeverityThreshold)
	alerts = classifyAlerts(alerts, o.knownIssues)

	rules := make([]string, 0, len(alerts))
	seenRule := map[string]bool{}
	for _, a := range alerts {
		if !seenRule[a.Alert.Name] {
			seenRule[a.Alert.Name] = true
			rules = append(rules, a.Alert.Name)
		}
	}
	slices.Sort(rules)

	collectionErr := errors.Join(collectionErrs...)
	envWorkspace := &workspaceData{
		Type:            workspaceEnv,
		AlertRules:      rules,
		FiredAlerts:     alerts,
		CollectionError: collectionErr,
	}
	workspaces := map[string]*workspaceData{workspaceEnv: envWorkspace}

	severityCounts := map[armalertsmanagement.Severity]int{}
	var knownCount int
	for _, a := range alerts {
		severityCounts[a.Alert.Severity]++
		if a.Metadata.KnownIssue {
			knownCount++
		}
	}
	unknownCount := len(alerts) - knownCount
	logger.Info("classified environment alerts", "known", knownCount, "unknown", unknownCount)

	filterKeys, filterOptions := collectFilterOptions(alerts)
	output := alertsOutput{
		Alerts: alerts,
		Summary: alertsSummary{
			Total:      len(alerts),
			Known:      knownCount,
			Unknown:    unknownCount,
			BySeverity: severityCounts,
		},
		TimeWindow: timeWindow{
			Start: o.TimeWindow.Start.UTC().Format(time.RFC3339),
			End:   o.TimeWindow.End.UTC().Format(time.RFC3339),
		},
		FilterKeys:    filterKeys,
		FilterOptions: filterOptions,
	}
	if collectionErr != nil {
		output.CollectionErrors = []string{fmt.Sprintf("%s: %v", workspaceEnv, collectionErr)}
	}
	fail(writeJSON("alerts.json", output))

	junitPath := filepath.Join(o.OutputDir, "junit_alerts.xml")
	suites := alertsToJUnit(logger, workspaces, o.TimeWindow)
	if err := deps.writeJUnit(junitPath, suites); err != nil {
		fail(fmt.Errorf("failed to write JUnit output: %w", err))
	} else {
		logger.Info("wrote alert JUnit artifact", "path", junitPath)
	}

	alertsHTML, err := deps.renderAlerts(output)
	if err != nil {
		fail(fmt.Errorf("failed to render alerts HTML: %w", err))
	}
	tabs := []observabilityTab{{Title: "Environment Alerts", HTML: string(incompleteHTML(alertsHTML, err))}}
	htmlPath := filepath.Join(o.OutputDir, "observability-summary.html")
	if err := deps.renderPage(htmlPath, tabs); err != nil {
		fail(fmt.Errorf("failed to render observability HTML: %w", err))
	} else {
		logger.Info("wrote observability HTML artifact", "path", htmlPath, "tabs", len(tabs))
	}

	// Gate on un-annotated alert firings, not on collection completeness:
	// collection errors already surfaced in the artifacts must not fail the
	// step while the environment Kusto credential grant is pending.
	var alertFailures uint
	for _, s := range suites.Suites {
		for _, tc := range s.TestCases {
			if tc.FailureOutput == nil || strings.HasSuffix(tc.Name, "alert collection is complete") {
				continue
			}
			alertFailures++
		}
	}
	if alertFailures > 0 {
		fatalErrors = append(fatalErrors, fmt.Errorf("JUnit results contain %d failing test case(s)", alertFailures))
	}
	return utils.TrackError(errors.Join(fatalErrors...))
}
