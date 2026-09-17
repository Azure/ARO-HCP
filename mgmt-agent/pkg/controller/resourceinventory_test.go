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

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/time/rate"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
)

type fakeResourceInventoryTableLister struct {
	pages [][]byte
	err   error
	calls []string
}

func TestCoreV1TableListerRequestsOnlyMetadataTable(t *testing.T) {
	columns := []metav1.TableColumnDefinition{{Name: "Name", Type: "string", Format: "name"}}
	response := marshalTable(t, columns, "rv-1", "", tableRow(t, "ns-a", "pod-a"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/api/v1/pods"; got != want {
			t.Errorf("request path = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("Accept"), tableMediaType; got != want {
			t.Errorf("Accept = %q, want %q", got, want)
		}
		query := r.URL.Query()
		if got, want := query.Get("includeObject"), string(metav1.IncludeMetadata); got != want {
			t.Errorf("includeObject = %q, want %q", got, want)
		}
		if got, want := query.Get("limit"), "500"; got != want {
			t.Errorf("limit = %q, want %q", got, want)
		}
		if got, want := query.Get("continue"), "next-token"; got != want {
			t.Errorf("continue = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", tableMediaType)
		_, _ = w.Write(response)
	}))
	defer server.Close()

	client, err := rest.RESTClientFor(&rest.Config{
		Host:    server.URL,
		APIPath: "/api",
		ContentConfig: rest.ContentConfig{
			GroupVersion:         &schema.GroupVersion{Version: "v1"},
			NegotiatedSerializer: scheme.Codecs.WithoutConversion(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	lister := &coreV1TableLister{client: client}
	if _, err := lister.ListTablePage(context.Background(), defaultResourceInventoryTargets[0].gvr, resourceInventoryLimit, "next-token"); err != nil {
		t.Fatalf("ListTablePage() returned error: %v", err)
	}
}

func (f *fakeResourceInventoryTableLister) ListTablePage(_ context.Context, _ schema.GroupVersionResource, _ int64, continueToken string) ([]byte, error) {
	f.calls = append(f.calls, continueToken)
	if f.err != nil {
		return nil, f.err
	}
	page := f.pages[len(f.calls)-1]
	return page, nil
}

func TestResourceInventoryCollectPaginatedTable(t *testing.T) {
	target := defaultResourceInventoryTargets[0]
	columns := []metav1.TableColumnDefinition{
		{Name: "Name", Type: "string", Format: "name"},
		{Name: "Ready", Type: "string"},
		{Name: "Node", Type: "string", Priority: 1},
	}
	lister := &fakeResourceInventoryTableLister{pages: [][]byte{
		marshalTable(t, columns, "rv-1", "next", tableRow(t, "ns-a", "pod-a", "1/1", "node-a")),
		marshalTable(t, columns, "rv-1", "", tableRow(t, "ns-b", "pod-b", "2/2", "node-b")),
	}}
	c := &ResourceInventory{lister: lister}
	summary := &resourceInventorySummary{
		SnapshotID:   "snapshot-1",
		SnapshotTime: time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC),
		Target:       target,
	}

	items, err := c.collect(context.Background(), summary)
	if err != nil {
		t.Fatalf("collect() returned error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("collect() returned %d items, want 2", len(items))
	}
	if got, want := lister.calls, []string{"", "next"}; !equalStrings(got, want) {
		t.Fatalf("continue tokens = %v, want %v", got, want)
	}
	if items[0].Namespace != "ns-a" || items[0].Name != "pod-a" || items[0].ItemIndex != 0 {
		t.Fatalf("first item identity = %#v", items[0])
	}
	if items[1].Namespace != "ns-b" || items[1].Name != "pod-b" || items[1].ItemIndex != 1 {
		t.Fatalf("second item identity = %#v", items[1])
	}
	if len(items[0].Cells) != 3 || items[0].Cells[2] != "node-a" {
		t.Fatalf("first item cells = %#v", items[0].Cells)
	}
	if summary.ListResourceVersion != "rv-1" || summary.PageCount != 2 {
		t.Fatalf("summary list metadata = %#v", summary)
	}
	wantBytes := int64(len(lister.pages[0]) + len(lister.pages[1]))
	if summary.UncompressedResponseBytes != wantBytes {
		t.Fatalf("response bytes = %d, want %d", summary.UncompressedResponseBytes, wantBytes)
	}
	if len(summary.PrinterColumns) != len(columns) {
		t.Fatalf("printer columns = %#v", summary.PrinterColumns)
	}
}

func TestResourceInventoryCollectRejectsNonTable(t *testing.T) {
	lister := &fakeResourceInventoryTableLister{pages: [][]byte{[]byte(`{"apiVersion":"v1","kind":"PodList","items":[]}`)}}
	c := &ResourceInventory{lister: lister}
	summary := &resourceInventorySummary{Target: defaultResourceInventoryTargets[0]}

	_, err := c.collect(context.Background(), summary)
	if err == nil {
		t.Fatal("collect() returned nil error for a non-Table response")
	}
}

func TestResourceInventoryCaptureEmitsCompletionLast(t *testing.T) {
	columns := []metav1.TableColumnDefinition{{Name: "Name", Type: "string", Format: "name"}}
	lister := &fakeResourceInventoryTableLister{pages: [][]byte{
		marshalTable(t, columns, "rv-2", "", tableRow(t, "ns-a", "pod-a"), tableRow(t, "ns-b", "pod-b")),
	}}
	now := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	var items []resourceInventoryItem
	var summaries []resourceInventorySummary
	c := &ResourceInventory{
		lister:  lister,
		limiter: rate.NewLimiter(rate.Inf, 1),
		now: func() time.Time {
			now = now.Add(time.Millisecond)
			return now
		},
		emitItem: func(item resourceInventoryItem) {
			if len(summaries) != 0 {
				t.Fatal("summary was emitted before all items")
			}
			items = append(items, item)
		},
		emitSummary: func(summary resourceInventorySummary) { summaries = append(summaries, summary) },
	}

	c.capture(context.Background(), defaultResourceInventoryTargets[0], now.Add(-time.Second), now, now.Add(30*time.Minute))

	if len(items) != 2 || len(summaries) != 1 {
		t.Fatalf("emitted %d items and %d summaries", len(items), len(summaries))
	}
	summary := summaries[0]
	if summary.Status != "Complete" || summary.ExpectedItemCount != 2 {
		t.Fatalf("completion summary = %#v", summary)
	}
	if summary.PageCount != 1 || summary.ListDuration <= 0 || summary.EmitDuration <= 0 {
		t.Fatalf("performance summary = %#v", summary)
	}
}

func TestResourceInventoryCaptureRecordsListFailure(t *testing.T) {
	lister := &fakeResourceInventoryTableLister{err: errors.New("API unavailable")}
	now := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	var summaries []resourceInventorySummary
	c := &ResourceInventory{
		lister:  lister,
		limiter: rate.NewLimiter(rate.Inf, 1),
		now:     func() time.Time { return now },
		emitItem: func(resourceInventoryItem) {
			t.Fatal("item emitted for failed List")
		},
		emitSummary: func(summary resourceInventorySummary) { summaries = append(summaries, summary) },
	}

	c.capture(context.Background(), defaultResourceInventoryTargets[1], now, now, now.Add(30*time.Minute))

	if len(summaries) != 1 || summaries[0].Status != "Failed" || summaries[0].ErrorMessage == "" {
		t.Fatalf("failure summary = %#v", summaries)
	}
}

func TestResourceInventoryInitialSchedulesAreSpread(t *testing.T) {
	start := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	c := &ResourceInventory{
		targets: defaultResourceInventoryTargets,
		jitter:  func(time.Duration) time.Duration { return 0 },
	}
	schedules := c.initialSchedules(start)

	if len(schedules) != 3 {
		t.Fatalf("initialSchedules() returned %d schedules", len(schedules))
	}
	wantSpacing := 30 * time.Minute / 3
	for i := range schedules {
		want := start.Add(time.Duration(i) * wantSpacing)
		if !schedules[i].next.Equal(want) {
			t.Errorf("schedule %d = %s, want %s", i, schedules[i].next, want)
		}
	}
}

func marshalTable(t *testing.T, columns []metav1.TableColumnDefinition, resourceVersion, continueToken string, rows ...metav1.TableRow) []byte {
	t.Helper()
	table := metav1.Table{
		TypeMeta:          metav1.TypeMeta{APIVersion: metav1.SchemeGroupVersion.String(), Kind: "Table"},
		ListMeta:          metav1.ListMeta{ResourceVersion: resourceVersion, Continue: continueToken},
		ColumnDefinitions: columns,
		Rows:              rows,
	}
	raw, err := json.Marshal(table)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func tableRow(t *testing.T, namespace, name string, extraCells ...interface{}) metav1.TableRow {
	t.Helper()
	metadata := metav1.PartialObjectMetadata{
		TypeMeta: metav1.TypeMeta{APIVersion: "meta.k8s.io/v1", Kind: "PartialObjectMetadata"},
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   namespace,
			Name:        name,
			Labels:      map[string]string{"must-not": "be retained"},
			Annotations: map[string]string{"sensitive": "discard me"},
		},
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	cells := append([]interface{}{name}, extraCells...)
	return metav1.TableRow{Cells: cells, Object: runtime.RawExtension{Raw: raw}}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
