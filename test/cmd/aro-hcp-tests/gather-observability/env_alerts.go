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
	"strings"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/azure-kusto-go/azkustodata"
	"github.com/Azure/azure-kusto-go/azkustodata/kql"
	azkquery "github.com/Azure/azure-kusto-go/azkustodata/query"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/alertsmanagement/armalertsmanagement"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/snapshot"
)

// workspaceEnv is the pseudo-workspace type used in persistent-environment
// (int/stg/prod) mode. Those runs have no per-run management cluster: their
// alerts fire from standing shared infrastructure and are attributed through
// the namespaces of the run's own HCP clusters.
const workspaceEnv = "env"

// envQueryTimeout bounds the environment Kusto query.
const envQueryTimeout = 5 * time.Minute

// kustoRow is one query result row keyed by column name.
type kustoRow map[string]string

// envKustoTarget is the environment Kusto cluster resolved from the rendered
// config (the same kusto.* keys gather-snapshot reads).
type envKustoTarget struct {
	endpoint     string
	serviceDB    string
	monitoringDB string
}

func loadEnvKustoTarget(renderedConfig string) (envKustoTarget, error) {
	raw, err := os.ReadFile(renderedConfig)
	if err != nil {
		return envKustoTarget{}, fmt.Errorf("failed to read rendered config: %w", err)
	}
	cfg, err := snapshot.ParseConfig(raw)
	if err != nil {
		return envKustoTarget{}, fmt.Errorf("failed to parse rendered config: %w", err)
	}
	if cfg.KustoName == "" || cfg.Region == "" {
		return envKustoTarget{}, fmt.Errorf("rendered config %s has no kusto.kustoName/kusto.location", renderedConfig)
	}
	endpoint, err := kusto.KustoEndpoint(cfg.KustoName, cfg.Region)
	if err != nil {
		return envKustoTarget{}, err
	}
	return envKustoTarget{endpoint: endpoint.String(), serviceDB: cfg.ServiceDatabase, monitoringDB: cfg.MonitoringEventsDatabase}, nil
}

// queryKusto runs one read-only query and returns its primary result as
// string-valued rows.
func queryKusto(ctx context.Context, cred azcore.TokenCredential, endpoint, db, query string) ([]kustoRow, error) {
	client, err := azkustodata.New(azkustodata.NewConnectionStringBuilder(endpoint).WithTokenCredential(cred))
	if err != nil {
		return nil, fmt.Errorf("failed to create Kusto client for %s: %w", endpoint, err)
	}
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(ctx, envQueryTimeout)
	defer cancel()
	dataset, err := client.IterativeQuery(ctx, db, kql.New("").AddUnsafe(query))
	if err != nil {
		return nil, fmt.Errorf("failed to execute query on %s: %w", endpoint, err)
	}
	return readKustoRows(dataset)
}

// readKustoRows reads the primary result as string-valued rows and drains
// every remaining table result: the SDK reports query-level failures
// (DataSetCompletion errors, cancellation) only there, after the primary
// rows. Rows read before a failure are returned together with the error.
func readKustoRows(dataset azkquery.IterativeDataset) ([]kustoRow, error) {
	var rows []kustoRow
	var errs []error
	for result := range dataset.Tables() {
		if err := result.Err(); err != nil {
			errs = append(errs, err)
			continue
		}
		table := result.Table()
		for rowResult := range table.Rows() {
			if err := rowResult.Err(); err != nil {
				errs = append(errs, fmt.Errorf("failed to read row: %w", err))
				continue
			}
			if !table.IsPrimaryResult() || rowResult.Row() == nil {
				continue
			}
			values := rowResult.Row().Values()
			row := make(kustoRow, len(values))
			for i, col := range rowResult.Row().Columns() {
				if i < len(values) {
					row[col.Name()] = values[i].String()
				}
			}
			rows = append(rows, row)
		}
	}
	return rows, errors.Join(errs...)
}

// kqlStringList renders a KQL list of single-quoted string literals.
func kqlStringList(values []string) string {
	escape := strings.NewReplacer(`\`, `\\`, `'`, `\'`)
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = "'" + escape.Replace(v) + "'"
	}
	return "(" + strings.Join(quoted, ",") + ")"
}

// envAlertsQuery returns the alerts fired on the run's HCP clusters. It runs
// against the service-logs database:
//
//   - The run's cluster IDs (cids) come from Clusters Service logs whose ARM
//     resource ID is in one of the run's resource groups (compared
//     case-insensitively, as ARM does). Test resource groups are unique per
//     run, so no other filter is needed.
//   - Alerts are matched on the cid in their namespace,
//     ocm-arohcp<env>-<cid>[-<suffix>]. The suffix is the cluster name in int,
//     random in stg, and absent on some prod namespaces.
//   - alertEvents holds one row per state change, so each alert is collapsed
//     to its latest state.
//
// The alert window is padded so alerts that fired late in the run but were
// ingested after it are still attributed.
func envAlertsQuery(monitoringDB string, rgs []string, start, end time.Time) string {
	return fmt.Sprintf(`let cids = clustersServiceLogs
| where timestamp between (datetime('%[2]s') .. datetime('%[3]s'))
| where isnotempty(cid)
| where extract(@"/resourcegroups/([^/]+)", 1, tolower(tostring(resource_id))) in~ %[4]s
| distinct cid = tostring(cid);
database('%[1]s').alertEvents
| where firedDateTime between (datetime('%[2]s') .. datetime('%[3]s') + 15m)
| extend alertName = tostring(alertContext.labels.alertname), ns = tostring(alertContext.labels.namespace)
| where isnotempty(alertName)
| where extract(@"^ocm-[a-z0-9]+-([a-z0-9]{32})(?:-|$)", 1, ns) in (cids)
| extend lastChange = coalesce(resolvedDateTime, firedDateTime)
| summarize arg_max(lastChange, *) by alertId
| project alertName, severity, condition = monitorCondition, signalType, alertRule, targetResourceId,
    fired = tostring(firedDateTime), resolved = tostring(resolvedDateTime),
    labels = tostring(alertContext.labels), expression = tostring(alertContext.expression)`,
		monitoringDB, start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339), kqlStringList(rgs))
}

// envAlertFromRow maps an alertEvents row into the alert shape used by the
// shared classification, JUnit and rendering pipeline. Unknown severities map
// to Sev4 so they surface without escalating.
func envAlertFromRow(row kustoRow) alert {
	severity := armalertsmanagement.Severity(row["severity"])
	if severityRank(severity) < 0 {
		severity = armalertsmanagement.SeveritySev4
	}
	var labels map[string]string
	var rawLabels map[string]any
	if json.Unmarshal([]byte(row["labels"]), &rawLabels) == nil {
		labels = make(map[string]string, len(rawLabels))
		for k, v := range rawLabels {
			if v != nil {
				labels[k] = fmt.Sprint(v)
			}
		}
	}
	parseTime := func(s string) *time.Time {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return &t
		}
		return nil
	}
	return alert{
		Alert: alertData{
			Name:       row["alertName"],
			Severity:   severity,
			Condition:  row["condition"],
			AlertRule:  row["alertRule"],
			SignalType: row["signalType"],
			StartsAt:   parseTime(row["fired"]),
			EndsAt:     parseTime(row["resolved"]),
			Labels:     labels,
			Expression: row["expression"],
		},
		Metadata: alertMetadata{
			MonitoringWorkspace:     row["targetResourceId"],
			MonitoringWorkspaceType: workspaceEnv,
		},
	}
}

// collectEnvAlerts fetches the alerts fired on the run's HCP clusters from
// the environment Kusto cluster.
func (o Options) collectEnvAlerts(ctx context.Context, deps gatherDependencies) ([]alert, error) {
	if len(o.envResourceGroups) == 0 {
		return nil, fmt.Errorf("no test resource groups found in the timing input")
	}
	target, err := loadEnvKustoTarget(o.renderedConfig)
	if err != nil {
		return nil, fmt.Errorf("environment Kusto unavailable: %w", err)
	}
	logr.FromContextOrDiscard(ctx).Info("collecting environment alerts", "kusto", target.endpoint, "resourceGroups", len(o.envResourceGroups))
	// Rows read before a failure are kept so a partial read still gates on
	// the un-annotated alerts it did see.
	rows, err := deps.queryKusto(ctx, o.cred, target.endpoint, target.serviceDB,
		envAlertsQuery(target.monitoringDB, o.envResourceGroups, o.TimeWindow.Start, o.TimeWindow.End))
	if err != nil {
		err = fmt.Errorf("failed to fetch environment alerts: %w", err)
	}
	alerts := make([]alert, 0, len(rows))
	for _, row := range rows {
		alerts = append(alerts, envAlertFromRow(row))
	}
	sortAlerts(alerts)
	return alerts, err
}

// runEnv implements persistent-environment (int/stg/prod) mode and writes the
// same artifacts dev runs produce: alerts.json, junit_alerts.xml and
// observability-summary.html.
//
// Collection failures are recorded in alerts.json and the JUnit output but do
// not fail the step, because the CI credential may not reach the environment
// Kusto clusters until a Microsoft-side credential grant lands. Un-annotated
// fired alerts still fail the step, matching the dev gate.
func (o Options) runEnv(ctx context.Context, deps gatherDependencies) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("logger not found in context: %w", err)
	}

	alerts, collectionErr := o.collectEnvAlerts(ctx, deps)
	var collectionErrors []string
	if collectionErr != nil {
		logger.Error(collectionErr, "environment alert collection incomplete")
		collectionErrors = []string{fmt.Sprintf("%s: %v", workspaceEnv, collectionErr)}
	}

	alerts = classifyAlerts(filterAlertsBySeverity(alerts, o.SeverityThreshold), o.knownIssues)
	// There is no rule inventory here, so each fired alert name becomes a
	// rule: the JUnit has one case per fired alert rather than "did not fire"
	// cases for every rule.
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
	logger.Info("classified environment alerts", "known", output.Summary.Known, "unknown", output.Summary.Unknown)

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
	if err := deps.writeJUnit(filepath.Join(o.OutputDir, "junit_alerts.xml"), alertsToJUnit(logger, workspaces, o.TimeWindow)); err != nil {
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
