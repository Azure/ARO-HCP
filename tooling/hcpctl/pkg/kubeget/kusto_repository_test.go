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

package kubeget

import (
	"context"
	"strings"
	"testing"
	"time"

	kustoerrors "github.com/Azure/azure-kusto-go/azkustodata/errors"
	azkquery "github.com/Azure/azure-kusto-go/azkustodata/query"
	"github.com/Azure/azure-kusto-go/azkustodata/types"
	"github.com/Azure/azure-kusto-go/azkustodata/value"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

type fakeKustoClient struct {
	rowsByQuery map[string][]azkquery.Row
	queries     map[string]string
}

func (f *fakeKustoClient) ExecutePreconfiguredQuery(_ context.Context, query kusto.Query, output chan<- kusto.TaggedRow) (*kusto.QueryResult, error) {
	if f.queries == nil {
		f.queries = map[string]string{}
	}
	f.queries[query.GetName()] = query.GetQuery().String()
	for _, row := range f.rowsByQuery[query.GetName()] {
		output <- kusto.TaggedRow{Row: row, QueryName: query.GetName()}
	}
	return &kusto.QueryResult{}, nil
}

func (*fakeKustoClient) Close() error { return nil }

func TestKustoRepositoryParsesRowsAndEscapesQueries(t *testing.T) {
	snapshotTime := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	exec := &fakeKustoClient{rowsByQuery: map[string][]azkquery.Row{
		"kube-get-snapshots": {newTestRow(t,
			[]testColumn{
				{name: "snapshotId", columnType: types.String}, {name: "snapshotTime", columnType: types.DateTime},
				{name: "apiGroup", columnType: types.String}, {name: "apiVersion", columnType: types.String},
				{name: "resource", columnType: types.String}, {name: "objectKind", columnType: types.String},
				{name: "scope", columnType: types.String}, {name: "expectedItemCount", columnType: types.Long},
				{name: "printerColumns", columnType: types.Dynamic},
			},
			value.Values{
				value.NewString("snapshot-1"), value.NewDateTime(snapshotTime), value.NewString("apps"), value.NewString("v1"),
				value.NewString("deployments"), value.NewString("Deployment"), value.NewString("Namespaced"), value.NewLong(1),
				value.NewDynamic([]byte(`[{"name":"Name","type":"string","format":"name","priority":0}]`)),
			},
		)},
		"kube-get-items": {newTestRow(t,
			[]testColumn{
				{name: "snapshotId", columnType: types.String}, {name: "namespace", columnType: types.String},
				{name: "name", columnType: types.String}, {name: "itemIndex", columnType: types.Long},
				{name: "display", columnType: types.Dynamic},
			},
			value.Values{value.NewString("snapshot-1"), value.NewString("test-ns"), value.NewString("test-deployment"), value.NewLong(0), value.NewDynamic([]byte(`["test-deployment"]`))},
		)},
		"kube-get-details": {newTestRow(t,
			[]testColumn{
				{name: "timestamp", columnType: types.DateTime}, {name: "event", columnType: types.String},
				{name: "apiVersion", columnType: types.String}, {name: "objectKind", columnType: types.String},
				{name: "namespace", columnType: types.String}, {name: "name", columnType: types.String},
				{name: "object", columnType: types.Dynamic},
			},
			value.Values{
				value.NewDateTime(snapshotTime.Add(-time.Minute)), value.NewString("Update"), value.NewString("apps/v1"), value.NewString("Deployment"),
				value.NewString("test-ns"), value.NewString("test-deployment"), value.NewDynamic([]byte(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"test-deployment"}}`)),
			},
		)},
	}}
	repository := NewKustoRepository(exec)

	snapshots, err := repository.LatestSnapshots(context.Background(), "cluster'one")
	if err != nil {
		t.Fatalf("LatestSnapshots returned an error: %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].Resource != "deployments" || snapshots[0].PrinterColumns[0].Name != "Name" {
		t.Fatalf("unexpected snapshots: %#v", snapshots)
	}
	if !strings.Contains(exec.queries["kube-get-snapshots"], "cluster == 'cluster''one'") {
		t.Errorf("cluster was not escaped in query:\n%s", exec.queries["kube-get-snapshots"])
	}

	items, err := repository.Items(context.Background(), "cluster-one", snapshots, ItemFilter{Namespace: "test-ns", Name: "test-deployment"})
	if err != nil {
		t.Fatalf("Items returned an error: %v", err)
	}
	if len(items) != 1 || items[0].Display[0] != "test-deployment" {
		t.Fatalf("unexpected items: %#v", items)
	}

	details, err := repository.Details(context.Background(), "cluster-one", items)
	if err != nil {
		t.Fatalf("Details returned an error: %v", err)
	}
	if len(details) != 1 || details[0].APIVersion != "apps/v1" || details[0].Object["kind"] != "Deployment" {
		t.Fatalf("unexpected details: %#v", details)
	}
	if !strings.Contains(exec.queries["kube-get-details"], "'apps/v1', 'Deployment'") {
		t.Errorf("detail query did not use the full API version:\n%s", exec.queries["kube-get-details"])
	}
}

type testColumn struct {
	name       string
	columnType types.Column
}

func newTestRow(t *testing.T, definitions []testColumn, values value.Values) azkquery.Row {
	t.Helper()
	columns := make([]azkquery.Column, len(definitions))
	for i, definition := range definitions {
		columns[i] = azkquery.NewColumn(i, definition.name, definition.columnType)
	}
	dataset := azkquery.NewBaseDataset(context.Background(), kustoerrors.OpUnknown, "PrimaryResult")
	table := azkquery.NewBaseTable(dataset, 0, "test", "PrimaryResult", "PrimaryResult", columns)
	return azkquery.NewRow(table, 0, values)
}
