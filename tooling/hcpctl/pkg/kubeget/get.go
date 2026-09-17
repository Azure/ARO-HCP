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
	"io"
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/printers"

	"sigs.k8s.io/yaml"
)

type OutputFormat string

const (
	OutputDefault OutputFormat = ""
	OutputWide    OutputFormat = "wide"
	OutputJSON    OutputFormat = "json"
	OutputYAML    OutputFormat = "yaml"
	OutputName    OutputFormat = "name"
)

type Request struct {
	Cluster       string
	Resource      string
	Name          string
	Namespace     string
	AllNamespaces bool
	Output        OutputFormat
	ShowKind      bool
}

type Result struct {
	Snapshots []Snapshot
	Items     []Item
	Details   []Detail
}

type Getter struct {
	repository Repository
}

func NewGetter(repository Repository) *Getter {
	return &Getter{repository: repository}
}

func (g *Getter) Get(ctx context.Context, request Request, out io.Writer) (*Result, error) {
	snapshots, err := g.repository.LatestSnapshots(ctx, request.Cluster)
	if err != nil {
		return nil, err
	}
	selected, err := selectSnapshots(snapshots, request.Resource, request.Namespace, request.AllNamespaces)
	if err != nil {
		return nil, err
	}
	if requiresNamespace(selected) && request.Namespace == "" && !request.AllNamespaces {
		return nil, fmt.Errorf("a namespace is required for %s (use --namespace or --all-namespaces)", request.Resource)
	}
	if request.Name != "" && requiresNamespace(selected) && request.Namespace == "" {
		return nil, fmt.Errorf("a namespace is required when getting a named %s", request.Resource)
	}

	items, err := g.repository.Items(ctx, request.Cluster, selected, ItemFilter{
		Namespace:     request.Namespace,
		AllNamespaces: request.AllNamespaces,
		Name:          request.Name,
	})
	if err != nil {
		return nil, err
	}
	if request.Name != "" && len(items) == 0 {
		return nil, fmt.Errorf("%s %q not found", selected[0].Resource, request.Name)
	}

	result := &Result{Snapshots: selected, Items: items}
	switch request.Output {
	case OutputDefault, OutputWide:
		err = printTables(out, selected, items, request.Output == OutputWide, request.ShowKind || request.Resource == "all", request.AllNamespaces)
	case OutputName:
		err = printNames(out, items)
	case OutputJSON, OutputYAML:
		result.Details, err = g.repository.Details(ctx, request.Cluster, items)
		if err == nil {
			err = printDetails(out, selected, items, result.Details, request.Name != "", request.Output)
		}
	default:
		err = fmt.Errorf("unsupported output format %q", request.Output)
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

func selectSnapshots(snapshots []Snapshot, resource, namespace string, allNamespaces bool) ([]Snapshot, error) {
	if len(snapshots) == 0 {
		return nil, fmt.Errorf("no complete inventory snapshots found")
	}
	resource = strings.ToLower(resource)
	if resource == "all" {
		selected := append([]Snapshot(nil), snapshots...)
		if namespace != "" && !allNamespaces {
			selected = selected[:0]
			for _, snapshot := range snapshots {
				if snapshot.Namespaced() {
					selected = append(selected, snapshot)
				}
			}
		}
		sortSnapshots(selected)
		return selected, nil
	}

	aliases := map[string]string{
		"pod": "pods", "po": "pods",
		"secret":    "secrets",
		"namespace": "namespaces", "ns": "namespaces",
	}
	if canonical, ok := aliases[resource]; ok {
		resource = canonical
	}
	var selected []Snapshot
	for _, snapshot := range snapshots {
		if strings.EqualFold(snapshot.Resource, resource) || strings.EqualFold(snapshot.ObjectKind, resource) {
			selected = append(selected, snapshot)
		}
	}
	if len(selected) == 0 {
		available := make([]string, 0, len(snapshots))
		for _, snapshot := range snapshots {
			available = append(available, snapshot.Resource)
		}
		sort.Strings(available)
		return nil, fmt.Errorf("resource %q is not available in Kusto inventory (available: %s)", resource, strings.Join(available, ", "))
	}
	if len(selected) > 1 {
		return nil, fmt.Errorf("resource %q matches multiple API versions; version-qualified resources are not yet supported", resource)
	}
	return selected, nil
}

func requiresNamespace(snapshots []Snapshot) bool {
	for _, snapshot := range snapshots {
		if snapshot.Namespaced() {
			return true
		}
	}
	return false
}

func sortSnapshots(snapshots []Snapshot) {
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].Resource < snapshots[j].Resource
	})
}

func printTables(out io.Writer, snapshots []Snapshot, items []Item, wide, showKind, allNamespaces bool) error {
	itemsBySnapshot := make(map[string][]Item, len(snapshots))
	for _, item := range items {
		itemsBySnapshot[item.Snapshot.ID] = append(itemsBySnapshot[item.Snapshot.ID], item)
	}
	for snapshotIndex, snapshot := range snapshots {
		if snapshotIndex > 0 {
			if _, err := fmt.Fprintln(out); err != nil {
				return err
			}
		}
		columns := append([]metav1.TableColumnDefinition(nil), snapshot.PrinterColumns...)
		withNamespace := allNamespaces && snapshot.Namespaced()
		if withNamespace {
			columns = append([]metav1.TableColumnDefinition{{Name: "Namespace", Type: "string"}}, columns...)
		}
		table := &metav1.Table{ColumnDefinitions: columns}
		for _, item := range itemsBySnapshot[snapshot.ID] {
			if len(item.Display) != len(snapshot.PrinterColumns) {
				return fmt.Errorf("%s/%s has %d display cells for %d columns", snapshot.Resource, item.Name, len(item.Display), len(snapshot.PrinterColumns))
			}
			cells := append([]any(nil), item.Display...)
			if withNamespace {
				cells = append([]any{item.Namespace}, cells...)
			}
			table.Rows = append(table.Rows, metav1.TableRow{Cells: cells})
		}
		printer := printers.NewTablePrinter(printers.PrintOptions{
			Wide:     wide,
			WithKind: showKind,
			Kind:     schema.GroupKind{Group: snapshot.APIGroup, Kind: snapshot.ObjectKind},
		})
		if err := printer.PrintObj(table, out); err != nil {
			return fmt.Errorf("print %s table: %w", snapshot.Resource, err)
		}
	}
	return nil
}

func printNames(out io.Writer, items []Item) error {
	for _, item := range items {
		if _, err := fmt.Fprintf(out, "%s/%s\n", strings.ToLower(item.Snapshot.ObjectKind), item.Name); err != nil {
			return err
		}
	}
	return nil
}

func printDetails(out io.Writer, snapshots []Snapshot, items []Item, details []Detail, single bool, format OutputFormat) error {
	detailByKey := make(map[string]Detail, len(details))
	for _, detail := range details {
		detailByKey[identityKey(detail.APIVersion, detail.ObjectKind, detail.Namespace, detail.Name)] = detail
	}
	objects := make([]map[string]any, 0, len(items))
	for _, item := range items {
		key := identityKey(fullAPIVersion(item.Snapshot), item.Snapshot.ObjectKind, item.Namespace, item.Name)
		detail, ok := detailByKey[key]
		if !ok {
			return fmt.Errorf("no full resource snapshot found for %s %s/%s", item.Snapshot.ObjectKind, item.Namespace, item.Name)
		}
		objects = append(objects, withoutManagedFields(detail.Object))
	}

	var document any
	if single {
		document = objects[0]
	} else {
		apiVersion := "v1"
		kind := "List"
		if len(snapshots) == 1 {
			apiVersion = fullAPIVersion(snapshots[0])
			kind = snapshots[0].ObjectKind + "List"
		}
		document = map[string]any{
			"apiVersion": apiVersion,
			"kind":       kind,
			"metadata":   map[string]any{},
			"items":      objects,
		}
	}

	var data []byte
	var err error
	if format == OutputJSON {
		data, err = json.MarshalIndent(document, "", "    ")
	} else {
		data, err = yaml.Marshal(document)
	}
	if err != nil {
		return fmt.Errorf("marshal %s output: %w", format, err)
	}
	_, err = io.Copy(out, bytes.NewReader(append(data, '\n')))
	return err
}

func withoutManagedFields(object map[string]any) map[string]any {
	data, err := json.Marshal(object)
	if err != nil {
		return object
	}
	var copy map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&copy) != nil {
		return object
	}
	if metadata, ok := copy["metadata"].(map[string]any); ok {
		delete(metadata, "managedFields")
	}
	return copy
}

func identityKey(apiVersion, kind, namespace, name string) string {
	return strings.Join([]string{apiVersion, kind, namespace, name}, "\x00")
}

func fullAPIVersion(snapshot Snapshot) string {
	if snapshot.APIGroup == "" {
		return snapshot.APIVersion
	}
	return snapshot.APIGroup + "/" + snapshot.APIVersion
}

func WriteSourceMetadata(out io.Writer, result *Result) error {
	var timestamps []time.Time
	if len(result.Details) > 0 {
		timestamps = make([]time.Time, 0, len(result.Details))
		for _, detail := range result.Details {
			timestamps = append(timestamps, detail.Timestamp)
		}
	} else {
		timestamps = make([]time.Time, 0, len(result.Snapshots))
		for _, snapshot := range result.Snapshots {
			timestamps = append(timestamps, snapshot.Time)
		}
	}
	if len(timestamps) == 0 {
		return nil
	}
	timestamp := timestamps[0]
	for _, candidate := range timestamps[1:] {
		if candidate.Before(timestamp) {
			timestamp = candidate
		}
	}
	_, err := fmt.Fprintf(out, "KUSTO_TIMESTAMP: %s\n", timestamp.UTC().Format(time.RFC3339Nano))
	return err
}
