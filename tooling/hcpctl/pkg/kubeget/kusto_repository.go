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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/Azure/azure-kusto-go/azkustodata/kql"
	azkquery "github.com/Azure/azure-kusto-go/azkustodata/query"
	"github.com/Azure/azure-kusto-go/azkustodata/types"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

type KustoRepository struct {
	exec kusto.KustoClient
}

func NewKustoRepository(exec kusto.KustoClient) *KustoRepository {
	return &KustoRepository{exec: exec}
}

func (r *KustoRepository) LatestSnapshots(ctx context.Context, cluster string) ([]Snapshot, error) {
	query := fmt.Sprintf(`let summaries = kubernetesResourceInventorySnapshots
| where cluster == '%s' and status == 'Complete'
| summarize arg_max(timestamp, *) by snapshotId;
let counts = kubernetesResourceInventory
| where cluster == '%s'
| summarize actualItemCount=count(), distinctItemCount=dcount(itemIndex), minItemIndex=min(itemIndex), maxItemIndex=max(itemIndex) by snapshotId;
summaries
| join kind=leftouter counts on snapshotId
| extend actualItemCount=coalesce(actualItemCount, 0), distinctItemCount=coalesce(distinctItemCount, 0)
| where actualItemCount == expectedItemCount and distinctItemCount == expectedItemCount
| where expectedItemCount == 0 or (minItemIndex == 0 and maxItemIndex == expectedItemCount - 1)
| summarize arg_max(snapshotTime, *) by apiGroup, apiVersion, resource
| project snapshotId, snapshotTime, apiGroup, apiVersion, resource, objectKind, scope, expectedItemCount, printerColumns
| order by resource asc`, escapeKQLString(cluster), escapeKQLString(cluster))

	rows, err := r.run(ctx, "kube-get-snapshots", query)
	if err != nil {
		return nil, err
	}
	snapshots := make([]Snapshot, 0, len(rows))
	for _, row := range rows {
		snapshotTime, err := parseTime(row, "snapshotTime")
		if err != nil {
			return nil, err
		}
		expected, err := parseInt64(row, "expectedItemCount")
		if err != nil {
			return nil, err
		}
		columns, err := decodeDynamicSlice[map[string]any](row["printerColumns"])
		if err != nil {
			return nil, fmt.Errorf("decode printerColumns: %w", err)
		}
		columnJSON, err := json.Marshal(columns)
		if err != nil {
			return nil, fmt.Errorf("marshal printerColumns: %w", err)
		}
		var printerColumns []struct {
			Name        string `json:"name"`
			Type        string `json:"type"`
			Format      string `json:"format"`
			Description string `json:"description"`
			Priority    int32  `json:"priority"`
		}
		if err := json.Unmarshal(columnJSON, &printerColumns); err != nil {
			return nil, fmt.Errorf("unmarshal printerColumns: %w", err)
		}
		snapshot := Snapshot{
			ID:                stringValue(row, "snapshotId"),
			Time:              snapshotTime,
			APIGroup:          stringValue(row, "apiGroup"),
			APIVersion:        stringValue(row, "apiVersion"),
			Resource:          stringValue(row, "resource"),
			ObjectKind:        stringValue(row, "objectKind"),
			Scope:             stringValue(row, "scope"),
			ExpectedItemCount: expected,
		}
		for _, column := range printerColumns {
			snapshot.PrinterColumns = append(snapshot.PrinterColumns, metav1Column(column.Name, column.Type, column.Format, column.Description, column.Priority))
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func (r *KustoRepository) Items(ctx context.Context, cluster string, snapshots []Snapshot, filter ItemFilter) ([]Item, error) {
	if len(snapshots) == 0 {
		return nil, nil
	}
	byID := make(map[string]Snapshot, len(snapshots))
	ids := make([]string, 0, len(snapshots))
	for _, snapshot := range snapshots {
		byID[snapshot.ID] = snapshot
		ids = append(ids, quoteKQLString(snapshot.ID))
	}
	var conditions strings.Builder
	if filter.Namespace != "" {
		fmt.Fprintf(&conditions, "\n| where namespace == '%s'", escapeKQLString(filter.Namespace))
	}
	if filter.Name != "" {
		fmt.Fprintf(&conditions, "\n| where name == '%s'", escapeKQLString(filter.Name))
	}
	query := fmt.Sprintf(`kubernetesResourceInventory
| where cluster == '%s' and snapshotId in (%s)%s
| project snapshotId, namespace, name, itemIndex, display
| order by snapshotId asc, itemIndex asc`, escapeKQLString(cluster), strings.Join(ids, ", "), conditions.String())

	rows, err := r.run(ctx, "kube-get-items", query)
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(rows))
	for _, row := range rows {
		snapshotID := stringValue(row, "snapshotId")
		snapshot, ok := byID[snapshotID]
		if !ok {
			return nil, fmt.Errorf("unknown inventory snapshot %q returned by Kusto", snapshotID)
		}
		index, err := parseInt64(row, "itemIndex")
		if err != nil {
			return nil, err
		}
		display, err := decodeDynamicSlice[any](row["display"])
		if err != nil {
			return nil, fmt.Errorf("decode display for %s/%s: %w", snapshot.Resource, stringValue(row, "name"), err)
		}
		items = append(items, Item{
			Snapshot:  snapshot,
			Namespace: stringValue(row, "namespace"),
			Name:      stringValue(row, "name"),
			Index:     index,
			Display:   display,
		})
	}
	return items, nil
}

func (r *KustoRepository) Details(ctx context.Context, cluster string, items []Item) ([]Detail, error) {
	if len(items) == 0 {
		return nil, nil
	}
	var requested strings.Builder
	requested.WriteString("let requested = datatable(apiVersion:string, objectKind:string, namespace:string, name:string) [\n")
	for i, item := range items {
		if i > 0 {
			requested.WriteString(",\n")
		}
		fmt.Fprintf(&requested, "  '%s', '%s', '%s', '%s'",
			escapeKQLString(fullAPIVersion(item.Snapshot)), escapeKQLString(item.Snapshot.ObjectKind),
			escapeKQLString(item.Namespace), escapeKQLString(item.Name))
	}
	requested.WriteString("\n];\n")
	query := fmt.Sprintf(`%skubernetesResourceSnapshots
| where cluster == '%s'
| join kind=inner requested on apiVersion, objectKind, namespace, name
| summarize arg_max(timestamp, *) by apiVersion, objectKind, namespace, name
| project timestamp, event, apiVersion, objectKind, namespace, name, object`, requested.String(), escapeKQLString(cluster))

	rows, err := r.run(ctx, "kube-get-details", query)
	if err != nil {
		return nil, err
	}
	details := make([]Detail, 0, len(rows))
	for _, row := range rows {
		timestamp, err := parseTime(row, "timestamp")
		if err != nil {
			return nil, err
		}
		object, ok := row["object"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("detail object for %s/%s has unexpected type %T", stringValue(row, "objectKind"), stringValue(row, "name"), row["object"])
		}
		details = append(details, Detail{
			Timestamp:  timestamp,
			Event:      stringValue(row, "event"),
			APIVersion: stringValue(row, "apiVersion"),
			ObjectKind: stringValue(row, "objectKind"),
			Namespace:  stringValue(row, "namespace"),
			Name:       stringValue(row, "name"),
			Object:     object,
		})
	}
	return details, nil
}

func (r *KustoRepository) run(ctx context.Context, name, queryText string) ([]map[string]any, error) {
	query := &repositoryQuery{name: name, query: kql.New("").AddUnsafe(queryText)}
	output := make(chan kusto.TaggedRow)
	done := make(chan struct{})
	var rows []map[string]any
	go func() {
		defer utilruntime.HandleCrash()
		defer close(done)
		for tagged := range output {
			rows = append(rows, rowToMap(tagged.Row))
		}
	}()

	_, queryErr := r.exec.ExecutePreconfiguredQuery(ctx, query, output)
	close(output)
	<-done
	if queryErr != nil {
		return nil, fmt.Errorf("execute %s query: %w", name, queryErr)
	}
	return rows, nil
}

type repositoryQuery struct {
	name  string
	query *kql.Builder
}

func (q *repositoryQuery) GetName() string               { return q.name }
func (q *repositoryQuery) GetQueryType() kusto.QueryType { return kusto.QueryTypeInternal }
func (q *repositoryQuery) GetDatabase() string           { return ServiceLogsDatabase }
func (q *repositoryQuery) GetQuery() *kql.Builder        { return q.query }
func (q *repositoryQuery) IsUnlimited() bool             { return true }

func rowToMap(row azkquery.Row) map[string]any {
	result := make(map[string]any, len(row.Columns()))
	values := row.Values()
	for i, column := range row.Columns() {
		if i >= len(values) {
			break
		}
		value := values[i]
		if value.GetType() == types.Dynamic {
			if raw, ok := value.GetValue().([]byte); ok {
				var decoded any
				decoder := json.NewDecoder(bytes.NewReader(raw))
				decoder.UseNumber()
				if decoder.Decode(&decoded) == nil {
					result[column.Name()] = decoded
					continue
				}
			}
		}
		result[column.Name()] = value.String()
	}
	return result
}

func escapeKQLString(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func quoteKQLString(value string) string {
	return "'" + escapeKQLString(value) + "'"
}

func stringValue(row map[string]any, key string) string {
	value, _ := row[key].(string)
	return value
}

func parseTime(row map[string]any, key string) (time.Time, error) {
	value := stringValue(row, key)
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse %s %q: %w", key, value, err)
	}
	return parsed, nil
}

func parseInt64(row map[string]any, key string) (int64, error) {
	value := stringValue(row, key)
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s %q: %w", key, value, err)
	}
	return parsed, nil
}

func decodeDynamicSlice[T any](value any) ([]T, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var decoded []T
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

func metav1Column(name, columnType, format, description string, priority int32) metav1.TableColumnDefinition {
	return metav1.TableColumnDefinition{Name: name, Type: columnType, Format: format, Description: description, Priority: priority}
}
