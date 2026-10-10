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
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type fakeRepository struct {
	snapshots []Snapshot
	items     []Item
	details   []Detail
	filter    ItemFilter
}

func (f *fakeRepository) LatestSnapshots(context.Context, string) ([]Snapshot, error) {
	return f.snapshots, nil
}

func (f *fakeRepository) Items(_ context.Context, _ string, _ []Snapshot, filter ItemFilter) ([]Item, error) {
	f.filter = filter
	var result []Item
	for _, item := range f.items {
		if filter.Namespace != "" && item.Namespace != filter.Namespace {
			continue
		}
		if filter.Name != "" && item.Name != filter.Name {
			continue
		}
		result = append(result, item)
	}
	return result, nil
}

func (f *fakeRepository) Details(context.Context, string, []Item) ([]Detail, error) {
	return f.details, nil
}

func TestGetTableWideAllNamespacesAndShowKind(t *testing.T) {
	snapshot := podSnapshot()
	repository := &fakeRepository{
		snapshots: []Snapshot{snapshot},
		items: []Item{{
			Snapshot: snapshot, Namespace: "test-ns", Name: "test-pod", Index: 0,
			Display: []any{"test-pod", "1/1", "Running", "0", "2m", "10.0.0.1", "node-1"},
		}},
	}
	var output bytes.Buffer
	_, err := NewGetter(repository).Get(context.Background(), Request{
		Cluster: "mgmt-1", Resource: "po", AllNamespaces: true, Output: OutputWide, ShowKind: true,
	}, &output)
	if err != nil {
		t.Fatalf("Get returned an error: %v", err)
	}
	for _, expected := range []string{"NAMESPACE", "IP", "NODE", "test-ns", "pod/test-pod", "10.0.0.1", "node-1"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("output did not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestGetNamespacedResourceRequiresExplicitScope(t *testing.T) {
	repository := &fakeRepository{snapshots: []Snapshot{podSnapshot()}}
	_, err := NewGetter(repository).Get(context.Background(), Request{Cluster: "mgmt-1", Resource: "pods"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "namespace is required") {
		t.Fatalf("expected namespace-required error, got %v", err)
	}
}

func TestGetNamedNamespacedResourceRequiresNamespaceEvenWithAllNamespaces(t *testing.T) {
	repository := &fakeRepository{snapshots: []Snapshot{podSnapshot()}}
	_, err := NewGetter(repository).Get(context.Background(), Request{
		Cluster: "mgmt-1", Resource: "pods", Name: "test-pod", AllNamespaces: true,
	}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "namespace is required") {
		t.Fatalf("expected namespace-required error, got %v", err)
	}
}

func TestGetDefaultTableExcludesWideColumns(t *testing.T) {
	snapshot := podSnapshot()
	repository := &fakeRepository{
		snapshots: []Snapshot{snapshot},
		items: []Item{{
			Snapshot: snapshot, Namespace: "test-ns", Name: "test-pod",
			Display: []any{"test-pod", "1/1", "Running", "0", "2m", "10.0.0.1", "node-1"},
		}},
	}
	var output bytes.Buffer
	_, err := NewGetter(repository).Get(context.Background(), Request{
		Cluster: "mgmt-1", Resource: "pods", Namespace: "test-ns",
	}, &output)
	if err != nil {
		t.Fatalf("Get returned an error: %v", err)
	}
	if strings.Contains(output.String(), "10.0.0.1") || strings.Contains(output.String(), "node-1") {
		t.Errorf("default output included wide columns:\n%s", output.String())
	}
}

func TestGetJSONListUsesDetailsAndRemovesManagedFields(t *testing.T) {
	snapshot := podSnapshot()
	item := Item{Snapshot: snapshot, Namespace: "test-ns", Name: "test-pod", Display: []any{"test-pod"}}
	repository := &fakeRepository{
		snapshots: []Snapshot{snapshot},
		items:     []Item{item},
		details: []Detail{{
			APIVersion: "v1", ObjectKind: "Pod", Namespace: "test-ns", Name: "test-pod", Timestamp: time.Now(),
			Object: map[string]any{
				"apiVersion": "v1", "kind": "Pod",
				"metadata": map[string]any{"name": "test-pod", "namespace": "test-ns", "managedFields": []any{map[string]any{"manager": "test"}}},
			},
		}},
	}
	var output bytes.Buffer
	_, err := NewGetter(repository).Get(context.Background(), Request{
		Cluster: "mgmt-1", Resource: "pods", Namespace: "test-ns", Output: OutputJSON,
	}, &output)
	if err != nil {
		t.Fatalf("Get returned an error: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("output was not valid JSON: %v", err)
	}
	if result["kind"] != "PodList" {
		t.Errorf("kind = %v, want PodList", result["kind"])
	}
	if strings.Contains(output.String(), "managedFields") {
		t.Errorf("managedFields was not removed:\n%s", output.String())
	}
}

func TestGetDetailFailsWhenFullSnapshotIsMissing(t *testing.T) {
	snapshot := podSnapshot()
	repository := &fakeRepository{
		snapshots: []Snapshot{snapshot},
		items:     []Item{{Snapshot: snapshot, Namespace: "test-ns", Name: "test-pod"}},
	}
	_, err := NewGetter(repository).Get(context.Background(), Request{
		Cluster: "mgmt-1", Resource: "pods", Namespace: "test-ns", Output: OutputYAML,
	}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "no full resource snapshot") {
		t.Fatalf("expected missing-detail error, got %v", err)
	}
}

func TestAllWithNamespaceExcludesClusterScopedResources(t *testing.T) {
	pod := podSnapshot()
	namespace := Snapshot{ID: "namespaces", Resource: "namespaces", ObjectKind: "Namespace", Scope: "Cluster"}
	selected, err := selectSnapshots([]Snapshot{namespace, pod}, "all", "test-ns", false)
	if err != nil {
		t.Fatalf("selectSnapshots returned an error: %v", err)
	}
	if len(selected) != 1 || selected[0].Resource != "pods" {
		t.Fatalf("selected = %#v, want only pods", selected)
	}
}

func TestWriteSourceMetadata(t *testing.T) {
	snapshot := podSnapshot()
	snapshot.Time = time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	result := &Result{Snapshots: []Snapshot{snapshot}, Items: []Item{{Snapshot: snapshot}}}
	var output bytes.Buffer
	if err := WriteSourceMetadata(&output, result); err != nil {
		t.Fatalf("WriteSourceMetadata returned an error: %v", err)
	}
	if got, want := output.String(), "KUSTO_TIMESTAMP: pods 2026-09-17T10:00:00Z\n"; got != want {
		t.Errorf("metadata = %q, want %q", got, want)
	}
}

func TestWriteSourceMetadataWritesEverySnapshot(t *testing.T) {
	newer := podSnapshot()
	newer.Time = time.Date(2026, 9, 17, 10, 5, 0, 0, time.UTC)
	older := podSnapshot()
	older.Resource = "secrets"
	older.Time = time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	var output bytes.Buffer
	if err := WriteSourceMetadata(&output, &Result{Snapshots: []Snapshot{newer, older}}); err != nil {
		t.Fatalf("WriteSourceMetadata returned an error: %v", err)
	}
	want := "KUSTO_TIMESTAMP: pods 2026-09-17T10:05:00Z\n" +
		"KUSTO_TIMESTAMP: secrets 2026-09-17T10:00:00Z\n"
	if got := output.String(); got != want {
		t.Errorf("metadata = %q, want %q", got, want)
	}
}

func TestWriteSourceMetadataUsesOldestDetailTimestamp(t *testing.T) {
	snapshot := podSnapshot()
	snapshot.Time = time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	result := &Result{
		Snapshots: []Snapshot{snapshot},
		Details: []Detail{
			{APIVersion: "v1", ObjectKind: "Pod", Timestamp: time.Date(2026, 9, 17, 10, 4, 0, 0, time.UTC)},
			{APIVersion: "v1", ObjectKind: "Pod", Timestamp: time.Date(2026, 9, 17, 10, 3, 0, 0, time.UTC)},
		},
	}
	var output bytes.Buffer
	if err := WriteSourceMetadata(&output, result); err != nil {
		t.Fatalf("WriteSourceMetadata returned an error: %v", err)
	}
	if got, want := output.String(), "KUSTO_TIMESTAMP: pods 2026-09-17T10:03:00Z\n"; got != want {
		t.Errorf("metadata = %q, want %q", got, want)
	}
}

func podSnapshot() Snapshot {
	return Snapshot{
		ID: "pods", APIVersion: "v1", Resource: "pods", ObjectKind: "Pod", Scope: "Namespaced",
		PrinterColumns: []metav1.TableColumnDefinition{
			{Name: "Name", Type: "string", Format: "name", Priority: 0},
			{Name: "Ready", Type: "string", Priority: 0},
			{Name: "Status", Type: "string", Priority: 0},
			{Name: "Restarts", Type: "string", Priority: 0},
			{Name: "Age", Type: "string", Priority: 0},
			{Name: "IP", Type: "string", Priority: 1},
			{Name: "Node", Type: "string", Priority: 1},
		},
	}
}
