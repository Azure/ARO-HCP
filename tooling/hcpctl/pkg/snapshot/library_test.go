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
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func loadTestLibrary(t *testing.T) *QueryLibrary {
	t.Helper()
	lib, err := LoadQueryLibrary()
	if err != nil {
		t.Fatalf("LoadQueryLibrary: %v", err)
	}
	return lib
}

// allSpecs returns every querySpec the gatherer knows, keyed by name.
func allSpecs() map[string]querySpec {
	specs := map[string]querySpec{}
	for _, q := range append(append([]querySpec{}, allQueries...), contextQueries...) {
		specs[q.key()] = q
	}
	return specs
}

// TestQueryLibraryDescribesEveryQuery ensures every query directory has a
// query.yaml and that its database agrees with the gatherer's spec.
func TestQueryLibraryDescribesEveryQuery(t *testing.T) {
	lib := loadTestLibrary(t)
	specs := allSpecs()
	templates, err := fs.Glob(queriesFS, queriesRoot+"/*/*/query.kql")
	if err != nil {
		t.Fatal(err)
	}
	if len(templates) == 0 {
		t.Fatal("no query templates found")
	}
	for _, tp := range templates {
		name := strings.TrimPrefix(path.Dir(tp), queriesRoot+"/")
		q := lib.Query(name)
		if q == nil {
			t.Errorf("%s has no query.yaml", name)
			continue
		}
		if spec, ok := specs[name]; ok && spec.database != q.Database {
			t.Errorf("%s: query.yaml database %q, gatherer uses %q", name, q.Database, spec.database)
		}
	}
	if len(lib.QueryNames()) != len(templates) {
		t.Errorf("library has %d queries, found %d templates", len(lib.QueryNames()), len(templates))
	}
}

// TestQueryLibraryParamsMatchTemplates ensures each query.yaml lists exactly
// the template variables its template (and the partials it uses) references.
func TestQueryLibraryParamsMatchTemplates(t *testing.T) {
	lib := loadTestLibrary(t)
	for _, name := range lib.QueryNames() {
		q := lib.Query(name)
		fields, err := templateFields(q.templatePath)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var declared []string
		for _, p := range q.Params {
			declared = append(declared, p.Name)
		}
		sort.Strings(declared)
		if !reflect.DeepEqual(fields, declared) {
			t.Errorf("%s: query.yaml params %v, template uses %v", name, declared, fields)
		}
	}
}

// TestQueryLibraryParamCatalog ensures every catalog parameter is used or
// provided by some query and has a way to obtain it.
func TestQueryLibraryParamCatalog(t *testing.T) {
	lib := loadTestLibrary(t)
	for _, name := range lib.ParamNames() {
		if len(lib.consumers(name)) == 0 && len(lib.providers(name)) == 0 {
			t.Errorf("parameter %s is neither used nor provided by any query", name)
		}
		if lib.Param(name).Guide != "" {
			if _, err := queriesFS.ReadFile(queriesRoot + "/" + lib.Param(name).Guide); err != nil {
				t.Errorf("parameter %s: guide %s: %v", name, lib.Param(name).Guide, err)
			}
		}
	}
}

// TestQueryLibraryProvidesMatchGatherer ensures each query.yaml provides
// section stores exactly what the gatherer's storeResult stores, so dynamic
// investigations discover identifiers the same way snapshots do.
func TestQueryLibraryProvidesMatchGatherer(t *testing.T) {
	lib := loadTestLibrary(t)
	for name, spec := range allSpecs() {
		q := lib.Query(name)
		if q == nil {
			t.Errorf("%s has no query.yaml", name)
			continue
		}
		if spec.storeResult == nil {
			if len(q.Provides) > 0 {
				t.Errorf("%s: query.yaml provides %v but the gatherer stores nothing", name, sortedKeys(q.Provides))
			}
			continue
		}
		if len(q.Provides) == 0 {
			t.Errorf("%s: gatherer stores results but query.yaml provides nothing", name)
			continue
		}

		// Build one result row whose columns are those the provides reference,
		// in first-appearance order, each with a distinct value.
		var columns []string
		seen := map[string]bool{}
		addColumn := func(c string) {
			if !seen[c] {
				seen[c] = true
				columns = append(columns, c)
			}
		}
		for _, param := range sortedKeys(q.Provides) {
			p := q.Provides[param]
			if p.Column != "" {
				addColumn(p.Column)
			} else {
				for _, f := range collectTemplateFields(p.tmpl) {
					addColumn(f)
				}
			}
		}
		row := make([]string, len(columns))
		for i, c := range columns {
			row[i] = "value-of-" + c
		}

		var before, after queryData
		if err := spec.storeResult(&after, []resultRow{{columns: columns, values: row}}); err != nil {
			t.Fatalf("%s: storeResult: %v", name, err)
		}
		stored := map[string]string{}
		bv, av := reflect.ValueOf(before), reflect.ValueOf(after)
		for i := 0; i < bv.NumField(); i++ {
			if !reflect.DeepEqual(bv.Field(i).Interface(), av.Field(i).Interface()) {
				stored[bv.Type().Field(i).Name] = fmt.Sprint(av.Field(i).Interface())
			}
		}
		provided := map[string]string{}
		for _, param := range sortedKeys(q.Provides) {
			values, err := q.Provides[param].values(columns, [][]string{row})
			if err != nil {
				t.Fatalf("%s: provides %s: %v", name, param, err)
			}
			if len(values) != 1 {
				t.Fatalf("%s: provides %s: got %v", name, param, values)
			}
			provided[param] = values[0]
		}
		if !reflect.DeepEqual(stored, provided) {
			t.Errorf("%s: gatherer stores %v, query.yaml provides %v", name, stored, provided)
		}
	}
}

// testQueryData is a fully populated queryData used to compare gatherer and
// library rendering.
func testQueryData() queryData {
	start := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	return queryData{
		ClusterURI:                  "https://example.kusto.windows.net",
		ServiceDatabase:             "ServiceLogs",
		HCPDatabase:                 "HostedControlPlaneLogs",
		MonitoringEventsDatabase:    "MonitoringEvents",
		ResourceGroup:               "rg",
		ServiceClusterName:          "svc-1",
		ManagementClusterName:       "mgmt-1",
		FullStartTime:               start,
		FullEndTime:                 start.Add(4 * time.Hour),
		PhaseStartTime:              start.Add(time.Hour),
		PhaseEndTime:                start.Add(2 * time.Hour),
		CorrelationID:               "corr",
		ClientRequestID:             "client",
		SubscriptionID:              "sub",
		ResourceID:                  "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c1/nodePools/np1",
		ResourceType:                "Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools",
		ClusterResourceID:           "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c1",
		ClusterResourceName:         "c1",
		ServiceProviderResourceType: "microsoft.redhatopenshift/hcpopenshiftclusters/nodepools/serviceprovidernodepools",
		AsyncOperationId:            "/providers/op",
		AsyncOperationPath:          "/providers/op/path",
		InternalID:                  "/api/internal",
		ClusterID:                   "cid",
		HostedClusterNamespace:      "ocm-x-cid",
		HostedControlPlaneNamespace: "ocm-x-cid-c1",
	}
}

func testSessionConfig(d queryData, exec QueryExecutor, resultsDir string) LibrarySessionConfig {
	return LibrarySessionConfig{
		Config: map[string]string{
			"ClusterURI":               d.ClusterURI,
			"ServiceDatabase":          d.ServiceDatabase,
			"HCPDatabase":              d.HCPDatabase,
			"MonitoringEventsDatabase": d.MonitoringEventsDatabase,
		},
		Execute:    exec,
		ResultsDir: resultsDir,
	}
}

// TestLibrarySessionRendersLikeGatherer ensures that, given the same values,
// the library renders every query exactly as the gatherer does.
func TestLibrarySessionRendersLikeGatherer(t *testing.T) {
	lib := loadTestLibrary(t)
	d := testQueryData()
	v := reflect.ValueOf(d)
	for _, name := range lib.QueryNames() {
		q := lib.Query(name)
		s := NewLibrarySession(lib, testSessionConfig(d, nil, ""))
		args := map[string]string{}
		for _, p := range s.agentParams(q) {
			switch f := v.FieldByName(p).Interface().(type) {
			case time.Time:
				args[p] = f.Format(time.RFC3339)
			default:
				args[p] = fmt.Sprint(f)
			}
		}
		got, _, err := s.Render(name, args)
		if err != nil {
			t.Fatalf("%s: Render: %v", name, err)
		}
		want, err := renderQuery(q.templatePath, d)
		if err != nil {
			t.Fatalf("%s: renderQuery: %v", name, err)
		}
		if got != want {
			t.Errorf("%s: library rendering differs from the gatherer's:\n--- library\n%s\n--- gatherer\n%s", name, got, want)
		}
		if strings.Contains(got, "<no value>") {
			t.Errorf("%s: rendering contains <no value>", name)
		}
	}
}

type fakeExecutor struct {
	queries []string
	columns []string
	rows    [][]string
}

func (f *fakeExecutor) exec(_ context.Context, kql string) ([]string, [][]string, error) {
	f.queries = append(f.queries, kql)
	return f.columns, f.rows, nil
}

func TestLibrarySessionDerivesProvidesAndInvalidates(t *testing.T) {
	lib := loadTestLibrary(t)
	d := testQueryData()
	fake := &fakeExecutor{}
	dir := t.TempDir()
	s := NewLibrarySession(lib, testSessionConfig(d, fake.exec, dir))
	ctx := context.Background()

	// Without a time window or resource, the error explains what is missing.
	_, err := s.Run(ctx, "clustersService/cid", nil)
	if err == nil || !strings.Contains(err.Error(), "FullStartTime") || !strings.Contains(err.Error(), "ClusterResourceID") {
		t.Fatalf("expected missing FullStartTime and ClusterResourceID, got %v", err)
	}

	// A node pool ResourceID yields the owning cluster's ID, and the single
	// cid returned is remembered.
	fake.columns, fake.rows = []string{"cid"}, [][]string{{"cid-1"}}
	res, err := s.Run(ctx, "clustersService/cid", map[string]string{
		"FullStartTime": "2025-01-02T03:04:05Z",
		"FullEndTime":   "2025-01-02 07:04:05",
		"ResourceID":    d.ResourceID,
	})
	if err != nil {
		t.Fatalf("Run cid: %v", err)
	}
	if !strings.Contains(res.Rendered, "=~ '"+d.ClusterResourceID+"'") {
		t.Errorf("cid query does not filter on the derived cluster resource ID:\n%s", res.Rendered)
	}
	if !strings.Contains(res.Rendered, "datetime(2025-01-02T07:04:05Z)") {
		t.Errorf("cid query does not use the normalized end time:\n%s", res.Rendered)
	}
	if res.Provided["ClusterID"] != "cid-1" {
		t.Errorf("expected ClusterID cid-1 to be provided, got %v", res.Provided)
	}
	out, err := os.ReadFile(res.File)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(path.Base(res.File), "001-clustersService-cid") {
		t.Errorf("unexpected result file name %s", res.File)
	}
	want := composeOutput(lib.Query("clustersService/cid").readme(), res.Rendered, []resultRow{{columns: []string{"cid"}, values: []string{"cid-1"}}})
	if string(out) != want {
		t.Errorf("result file does not match snapshot output format:\n%s", out)
	}

	// Later queries reuse the remembered arguments and provided values.
	fake.columns, fake.rows = []string{"x"}, nil
	res, err = s.Run(ctx, "acm/klusterletLogs", nil)
	if err != nil {
		t.Fatalf("Run klusterletLogs: %v", err)
	}
	if !strings.Contains(res.Rendered, "cid-1") {
		t.Errorf("klusterletLogs does not use the provided ClusterID:\n%s", res.Rendered)
	}
	if !strings.Contains(res.Rendered, "datetime(2025-01-02T03:04:05Z)") {
		t.Errorf("PhaseStartTime did not default to FullStartTime:\n%s", res.Rendered)
	}

	// Switching to another cluster invalidates the provided ClusterID.
	other := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c2"
	_, err = s.Run(ctx, "acm/klusterletLogs", map[string]string{"ResourceID": other})
	if err == nil || !strings.Contains(err.Error(), "ClusterID") || !strings.Contains(err.Error(), "clustersService/cid") ||
		!strings.Contains(err.Error(), "ClusterResourceID changed") {
		t.Fatalf("expected stale ClusterID error naming its provider, got %v", err)
	}

	// Maestro queries need only one of their parameter groups.
	if _, _, err := s.Render("maestro/serverLogs", nil); err != nil {
		t.Errorf("maestro/serverLogs should render from ResourceID alone: %v", err)
	}

	// Several distinct values are reported, not remembered.
	fake.columns, fake.rows = []string{"cid"}, [][]string{{"a"}, {"b"}}
	res, err = s.Run(ctx, "clustersService/cid", nil)
	if err != nil {
		t.Fatalf("Run cid: %v", err)
	}
	if len(res.Ambiguous["ClusterID"]) != 2 || len(res.Provided) != 0 {
		t.Errorf("expected ambiguous ClusterID, got provided %v ambiguous %v", res.Provided, res.Ambiguous)
	}
	if !strings.Contains(res.Markdown(10), "several values for ClusterID") {
		t.Errorf("run summary does not report ambiguity:\n%s", res.Markdown(10))
	}
}

func TestLibrarySessionRejectsBadArguments(t *testing.T) {
	lib := loadTestLibrary(t)
	s := NewLibrarySession(lib, testSessionConfig(testQueryData(), (&fakeExecutor{}).exec, ""))
	for _, tc := range []struct {
		args map[string]string
		want string
	}{
		{map[string]string{"Bogus": "x"}, "unknown parameter"},
		{map[string]string{"ClusterURI": "x"}, "fixed by the CLI"},
		{map[string]string{"FullStartTime": "yesterday"}, "not a datetime"},
	} {
		if _, err := s.Run(context.Background(), "clustersService/cid", tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("args %v: expected error containing %q, got %v", tc.args, tc.want, err)
		}
	}
	if _, err := s.Run(context.Background(), "nope/nope", nil); err == nil || !strings.Contains(err.Error(), "list_queries") {
		t.Errorf("expected unknown query error, got %v", err)
	}
}

func TestLibrarySessionMarkdown(t *testing.T) {
	lib := loadTestLibrary(t)
	s := NewLibrarySession(lib, testSessionConfig(testQueryData(), (&fakeExecutor{}).exec, ""))

	list := s.ListMarkdown("")
	for _, name := range lib.QueryNames() {
		if !strings.Contains(list, "| "+name+" |") {
			t.Errorf("list does not include %s", name)
		}
	}
	if filtered := s.ListMarkdown("maestro"); strings.Contains(filtered, "frontend/requestLogs") || !strings.Contains(filtered, "maestro/serverLogs") {
		t.Errorf("filtered list is wrong:\n%s", filtered)
	}

	desc, err := s.DescribeQueryMarkdown("hypershift/hostedClusterMetadata")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Parameters", "ClusterResourceID", "## Provides", "HostedControlPlaneNamespace", "Not renderable yet", "## README"} {
		if !strings.Contains(desc, want) {
			t.Errorf("describe_query output missing %q:\n%s", want, desc)
		}
	}

	param, err := s.DescribeParamMarkdown("ClusterID")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Provided by: clustersService/cid", "Used by:", "acm/klusterletLogs", "guides/resource-identity.md"} {
		if !strings.Contains(param, want) {
			t.Errorf("describe_param output missing %q:\n%s", want, param)
		}
	}
	if _, err := s.DescribeParamMarkdown("Nope"); err == nil {
		t.Error("expected error for unknown parameter")
	}
}

func TestArmClusterResourceID(t *testing.T) {
	cluster := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c1"
	for in, want := range map[string]string{
		cluster:                                cluster,
		cluster + "/nodePools/np1":             cluster,
		cluster + "/externalAuths/ea":          cluster,
		"/subscriptions/sub/resourceGroups/rg": "",
		"not an id":                            "",
	} {
		if got := armClusterResourceID(in); !strings.EqualFold(got, want) {
			t.Errorf("armClusterResourceID(%q) = %q, want %q", in, got, want)
		}
	}
}
