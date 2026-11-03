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
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"

	kustov2 "github.com/Azure/azure-kusto-go/azkustodata/query/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/test/util/junit"
	"github.com/Azure/ARO-HCP/test/util/timing"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

const envTestConfig = "kusto:\n  kustoName: test-kusto\n  location: testregion\n  serviceLogsDatabase: ServiceLogs\n  monitoringEventsDatabase: MonitoringEvents\n"

// envTestEndpoint is the endpoint envTestConfig resolves to.
func envTestEndpoint(t *testing.T) string {
	t.Helper()
	endpoint, err := kusto.KustoEndpoint("test-kusto", "testregion")
	if err != nil {
		t.Fatal(err)
	}
	return endpoint.String()
}

func writeEnvTestConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestKQLStringList(t *testing.T) {
	t.Parallel()
	if got, want := kqlStringList([]string{"rg-1", `o'b\rien`}), `('rg-1','o\'b\\rien')`; got != want {
		t.Fatalf("kqlStringList = %s, want %s", got, want)
	}
}

func TestEnvAlertsQuery(t *testing.T) {
	t.Parallel()
	query := envAlertsQuery("MonitoringEvents", []string{"rg-1", "rg-2"},
		time.Date(2026, 10, 27, 11, 39, 36, 0, time.UTC), time.Date(2026, 10, 27, 13, 7, 21, 0, time.UTC))
	for _, want := range []string{
		// ARM resource group names are case-insensitive and may contain
		// characters beyond [a-z0-9-].
		`extract(@"/resourcegroups/([^/]+)", 1, tolower(tostring(resource_id))) in~ ('rg-1','rg-2')`,
		"database('MonitoringEvents').alertEvents",
		"datetime('2026-10-27T13:07:21Z') + 15m",
		// The suffix after the cid is optional: some prod namespaces have none.
		`extract(@"^ocm-[a-z0-9]+-([a-z0-9]{32})(?:-|$)", 1, ns) in (cids)`,
		"annotations = tostring(alertContext.annotations)",
		"summarize arg_max(lastChange, *) by alertId",
	} {
		if !strings.Contains(query, want) {
			t.Errorf("query missing %q:\n%s", want, query)
		}
	}
}

// kustoFrames builds a fragmented Kusto v2 response with one primary table
// holding the given alert names, ending with the given DataSetCompletion.
func kustoFrames(alertNames []string, completion string) string {
	rows := make([]string, len(alertNames))
	for i, name := range alertNames {
		rows[i] = `["` + name + `"]`
	}
	return `[{"FrameType":"DataSetHeader","IsProgressive":false,"Version":"v2.0","IsFragmented":true,"ErrorReportingPlacement":"EndOfTable"}
,{"FrameType":"DataTable","TableId":0,"TableKind":"QueryProperties","TableName":"@ExtendedProperties","Columns":[{"ColumnName":"TableId","ColumnType":"int"},{"ColumnName":"Key","ColumnType":"string"},{"ColumnName":"Value","ColumnType":"dynamic"}],"Rows":[]}
,{"FrameType":"TableHeader","TableId":1,"TableKind":"PrimaryResult","TableName":"PrimaryResult","Columns":[{"ColumnName":"alertName","ColumnType":"string"}]}
,{"FrameType":"TableFragment","TableFragmentType":"DataAppend","TableId":1,"Rows":[` + strings.Join(rows, ",") + `]}
,{"FrameType":"TableCompletion","TableId":1,"RowCount":` + strconv.Itoa(len(alertNames)) + `}
,` + completion + `
]`
}

func TestReadKustoRows(t *testing.T) {
	t.Parallel()
	complete := `{"FrameType":"DataSetCompletion","HasErrors":false,"Cancelled":false}`
	for _, tt := range []struct {
		name       string
		completion string
		cancelled  bool
		wantErr    string
	}{
		{
			name:       "complete",
			completion: complete,
		},
		{
			// When the query context ends, the SDK may close its channels
			// without reporting an error.
			name:       "cancelled query",
			completion: complete,
			cancelled:  true,
			wantErr:    context.Canceled.Error(),
		},
		{
			// The SDK reports this only after the primary rows, as a later
			// table result.
			name:       "failure after the primary table",
			completion: `{"FrameType":"DataSetCompletion","HasErrors":true,"Cancelled":false,"OneApiErrors":[{"error":{"code":"LimitsExceeded","message":"Request is invalid and cannot be executed.","@type":"Kusto.DataNode.Exceptions.QueryLimitsExceededException","@message":"query exceeded memory limit"}}]}`,
			wantErr:    "LimitsExceeded",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dataset, err := kustov2.NewIterativeDataset(t.Context(), io.NopCloser(strings.NewReader(kustoFrames([]string{"KubePodNotReady", "KubeNodeNotReady"}, tt.completion))), 10, 10, 10)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.cancelled {
				cancel()
			}
			rows, err := readKustoRows(ctx, dataset)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
			// Rows read before the failure are kept so they still gate.
			if len(rows) != 2 || rows[0]["alertName"] != "KubePodNotReady" || rows[1]["alertName"] != "KubeNodeNotReady" {
				t.Errorf("rows = %v", rows)
			}
		})
	}
}

func TestEnvAlertFromRow(t *testing.T) {
	t.Parallel()
	a := envAlertFromRow(kustoRow{
		"alertName": "KubePodNotReady",
		"severity":  "Sev3",
		"condition": "Resolved",
		"alertRule": "svc-rules/KubePodNotReady",
		// Kusto renders datetimes with fractional seconds.
		"fired":       "2026-10-27T11:40:00.1234567Z",
		"resolved":    "2026-10-27T11:45:00Z",
		"labels":      `{"pod":"hcco-abc","value":42,"empty":null}`,
		"annotations": `{"description":"Pod hcco-abc is not ready.","runbook_url":"https://runbooks.example/kubepodnotready"}`,
	})
	if a.Alert.Severity != "Sev3" || a.Alert.Condition != "Resolved" || a.Alert.AlertRule != "svc-rules/KubePodNotReady" {
		t.Errorf("unexpected alert fields: %+v", a.Alert)
	}
	if a.Alert.StartsAt == nil || !a.Alert.StartsAt.Equal(time.Date(2026, 10, 27, 11, 40, 0, 123456700, time.UTC)) {
		t.Errorf("startsAt = %v", a.Alert.StartsAt)
	}
	if a.Alert.EndsAt == nil {
		t.Error("endsAt not parsed")
	}
	if _, hasEmpty := a.Alert.Labels["empty"]; a.Alert.Labels["pod"] != "hcco-abc" || a.Alert.Labels["value"] != "42" || hasEmpty {
		t.Errorf("labels = %v", a.Alert.Labels)
	}
	if a.Alert.Description != "Pod hcco-abc is not ready." || a.Alert.Annotations["runbook_url"] != "https://runbooks.example/kubepodnotready" {
		t.Errorf("description = %q, annotations = %v", a.Alert.Description, a.Alert.Annotations)
	}
	if a.Metadata.MonitoringWorkspaceType != workspaceEnv {
		t.Errorf("workspace type = %q", a.Metadata.MonitoringWorkspaceType)
	}

	b := envAlertFromRow(kustoRow{"alertName": "WeirdAlert", "severity": "Sev9", "fired": "not-a-time", "labels": "not-json"})
	if b.Alert.Severity != "Sev4" || b.Alert.StartsAt != nil || b.Alert.EndsAt != nil || b.Alert.Labels != nil || b.Alert.Annotations != nil {
		t.Errorf("unparseable fields must fall back: %+v", b.Alert)
	}
}

func TestLoadEnvKustoTarget(t *testing.T) {
	t.Parallel()
	got, err := loadEnvKustoTarget(writeEnvTestConfig(t, envTestConfig))
	if err != nil {
		t.Fatal(err)
	}
	if want := (envKustoTarget{endpoint: envTestEndpoint(t), serviceDB: "ServiceLogs", monitoringDB: "MonitoringEvents"}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestValidatePersistentEnv(t *testing.T) {
	t.Parallel()
	if _, err := (&RawOptions{PersistentEnv: true, OutputDir: "out", RenderedConfig: "cfg"}).Validate(); err != nil {
		t.Errorf("persistent-env must not require --subscription-id: %v", err)
	}
	if _, err := (&RawOptions{OutputDir: "out", RenderedConfig: "cfg"}).Validate(); err == nil {
		t.Error("dev mode must still require --subscription-id")
	}
	if _, err := (&RawOptions{PersistentEnv: true, AMWOnly: true, OutputDir: "out", RenderedConfig: "cfg"}).Validate(); err == nil {
		t.Error("--amw-only and --persistent-env must be mutually exclusive")
	}
}

func TestCompletePersistentEnvSkipsDevSetup(t *testing.T) {
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "AzureCLICredential")
	dir := t.TempDir()
	// Only kusto.* keys: no regionRG, workspaces or Cosmos config.
	raw := &RawOptions{
		PersistentEnv:     true,
		TimingInputDir:    dir,
		OutputDir:         dir,
		RenderedConfig:    writeEnvTestConfig(t, envTestConfig),
		StartTimeFallback: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
	}
	validated, err := raw.Validate()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validated.Complete(logr.NewContext(t.Context(), logr.Discard())); err != nil {
		t.Fatalf("persistent-env Complete must not need dev-only config: %v", err)
	}
}

var firedAlertRow = kustoRow{
	"alertName": "KubePodNotReady",
	"severity":  "Sev3",
	"condition": "Fired",
	"fired":     "2026-10-27T11:40:00Z",
	"labels":    `{"alertname":"KubePodNotReady","namespace":"ocm-arohcpint-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-cluster-a"}`,
}

func TestRunEnv(t *testing.T) {
	t.Parallel()
	knownPodNotReady, err := parseKnownIssues([]byte("knownIssues:\n- name: KubePodNotReady\n  reason: tracked noise\n"))
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name   string
		mutate func(*Options)
		rows   []kustoRow
		err    error
		// wantErr is whether the step fails.
		wantErr bool
		// wantCollectionError is a substring of the recorded collection
		// error; empty means collection must be complete.
		wantCollectionError string
		wantUnknown         int
		wantJUnitFailed     uint
		wantJUnitSkipped    uint
	}{
		{
			name:            "un-annotated alert fails the step",
			rows:            []kustoRow{firedAlertRow},
			wantErr:         true,
			wantUnknown:     1,
			wantJUnitFailed: 1,
		},
		{
			name:             "known-issue alert passes",
			mutate:           func(o *Options) { o.knownIssues = knownPodNotReady },
			rows:             []kustoRow{firedAlertRow},
			wantJUnitSkipped: 1,
		},
		{
			name: "no alerts passes",
		},
		{
			name:        "alerts read before a stream failure still gate",
			rows:        []kustoRow{firedAlertRow},
			err:         errors.New("stream reset"),
			wantErr:     true,
			wantUnknown: 1,
			// One case for the alert, one for incomplete collection.
			wantJUnitFailed:     2,
			wantCollectionError: "stream reset",
		},
		{
			name:                "query failure degrades",
			err:                 errors.New("connection refused"),
			wantCollectionError: "connection refused",
			wantJUnitFailed:     1,
		},
		{
			name: "config without kusto degrades",
			mutate: func(o *Options) {
				o.renderedConfig = writeEnvTestConfig(t, "regionRG: hcp-underlay\n")
			},
			wantCollectionError: "environment Kusto unavailable",
			wantJUnitFailed:     1,
		},
		{
			name:                "missing timing input degrades",
			mutate:              func(o *Options) { o.envResourceGroups = nil },
			wantCollectionError: "no test resource groups",
			wantJUnitFailed:     1,
		},
		{
			name:            "broken known-issues file fails the step",
			mutate:          func(o *Options) { o.knownIssuesError = errors.New("bad yaml") },
			wantErr:         true,
			wantJUnitFailed: 1,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			o := Options{completedOptions: &completedOptions{
				PersistentEnv:     true,
				OutputDir:         t.TempDir(),
				SeverityThreshold: -1,
				TimeWindow: timing.TimeWindow{
					Start: time.Date(2026, 10, 27, 11, 39, 36, 0, time.UTC),
					End:   time.Date(2026, 10, 27, 13, 7, 21, 0, time.UTC),
				},
				renderedConfig:    writeEnvTestConfig(t, envTestConfig),
				envResourceGroups: []string{"rg-1"},
			}}
			if tt.mutate != nil {
				tt.mutate(&o)
			}
			deps := o.dependencies()
			deps.queryKusto = func(_ context.Context, _ azcore.TokenCredential, endpoint, db, query string) ([]kustoRow, error) {
				if endpoint != envTestEndpoint(t) || db != "ServiceLogs" || !strings.Contains(query, "('rg-1')") {
					t.Errorf("unexpected query: endpoint=%s db=%s query=%s", endpoint, db, query)
				}
				return tt.rows, tt.err
			}

			err := o.runEnv(logr.NewContext(t.Context(), logr.Discard()), deps)
			if (err != nil) != tt.wantErr {
				t.Fatalf("runEnv error = %v, wantErr %v", err, tt.wantErr)
			}

			var output alertsOutput
			readJSONArtifact(t, filepath.Join(o.OutputDir, "alerts.json"), &output)
			if output.Summary.Unknown != tt.wantUnknown {
				t.Errorf("unknown alerts = %d, want %d", output.Summary.Unknown, tt.wantUnknown)
			}
			switch {
			case tt.wantCollectionError == "" && len(output.CollectionErrors) != 0:
				t.Errorf("collection errors = %v, want none", output.CollectionErrors)
			case tt.wantCollectionError != "" && (len(output.CollectionErrors) != 1 || !strings.Contains(output.CollectionErrors[0], tt.wantCollectionError)):
				t.Errorf("collection errors = %v, want %q", output.CollectionErrors, tt.wantCollectionError)
			}

			suites := readJUnitArtifact(t, filepath.Join(o.OutputDir, "junit_alerts.xml"))
			if got := suites.Suites[0]; got.NumFailed != tt.wantJUnitFailed || got.NumSkipped != tt.wantJUnitSkipped {
				t.Errorf("junit failed=%d skipped=%d, want %d/%d", got.NumFailed, got.NumSkipped, tt.wantJUnitFailed, tt.wantJUnitSkipped)
			}
			if _, err := os.Stat(filepath.Join(o.OutputDir, "observability-summary.html")); err != nil {
				t.Errorf("observability-summary.html not written: %v", err)
			}
		})
	}
}

func readJSONArtifact(t *testing.T, path string, dest any) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path, err)
	}
	if err := json.Unmarshal(content, dest); err != nil {
		t.Fatalf("failed to unmarshal %s: %v", path, err)
	}
}

func readJUnitArtifact(t *testing.T, path string) *junit.TestSuites {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path, err)
	}
	var suites junit.TestSuites
	if err := xml.Unmarshal(content, &suites); err != nil {
		t.Fatalf("failed to parse %s: %v", path, err)
	}
	return &suites
}
