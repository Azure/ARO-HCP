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

package citables

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Azure/azure-kusto-go/azkustodata"
	"github.com/Azure/azure-kusto-go/azkustodata/query"
)

func TestValidateAcceptsTheRequiredSchema(t *testing.T) {
	if err := Validate(declared(Required)); err != nil {
		t.Fatal(err)
	}
}

func TestValidateReportsMissingAndMistypedColumns(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func([]Column) []Column
		want   string
	}{
		{"mistyped", func(columns []Column) []Column {
			for i := range columns {
				if columns[i].TableName == TestResults && columns[i].ColumnName == "finishedAt" {
					columns[i].ColumnType = "string"
				}
			}
			return columns
		}, "ciTestResults.finishedAt:datetime"},
		{"missing", func(columns []Column) []Column {
			out := columns[:0]
			for _, column := range columns {
				if column.TableName == JobOutcomes && column.ColumnName == "runtimeRegion" {
					continue
				}
				out = append(out, column)
			}
			return out
		}, "ciJobOutcomes.runtimeRegion:string"},
		{"missing-table", func(columns []Column) []Column {
			out := columns[:0]
			for _, column := range columns {
				if column.TableName == ProcessedJobs {
					continue
				}
				out = append(out, column)
			}
			return out
		}, "ciProcessedJobs.buildId:string"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.mutate(declared(Required)))
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "deploy") {
				t.Fatalf("missing actionable schema error for %s: %v", tc.want, err)
			}
		})
	}
}

func TestEnsureSurfacesQueryFailures(t *testing.T) {
	cause := errors.New("kusto unavailable")
	err := Ensure(context.Background(), failingClient{err: cause}, "ServiceLogs")
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "inspect CI schema") {
		t.Fatalf("schema failure is not actionable: %v", err)
	}
}

// declared expands a required schema into the rows the schema query returns.
func declared(required map[string]map[string]string) []Column {
	columns := make([]Column, 0, len(required))
	for table, names := range required {
		for name, kind := range names {
			columns = append(columns, Column{TableName: table, ColumnName: name, ColumnType: kind})
		}
	}
	return columns
}

type failingClient struct{ err error }

func (client failingClient) Query(context.Context, string, azkustodata.Statement, ...azkustodata.QueryOption) (query.Dataset, error) {
	return nil, client.err
}
