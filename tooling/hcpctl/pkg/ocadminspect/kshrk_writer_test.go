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

package ocadminspect

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kshrk"
)

func TestSplitAPIVersion(t *testing.T) {
	tests := map[string][2]string{
		"v1":                              {"", "v1"},
		"apps/v1":                         {"apps", "v1"},
		"hypershift.openshift.io/v1beta1": {"hypershift.openshift.io", "v1beta1"},
		"":                                {"", ""},
	}
	for apiVersion, want := range tests {
		group, version := splitAPIVersion(apiVersion)
		if group != want[0] || version != want[1] {
			t.Errorf("splitAPIVersion(%q) = (%q, %q), want (%q, %q)", apiVersion, group, version, want[0], want[1])
		}
	}
}

func TestAPIPathFor(t *testing.T) {
	tests := []struct {
		group, version, plural, namespace string
		want                              string
	}{
		{"", "v1", "nodes", "", "/api/v1/nodes"},
		{"", "v1", "pods", "default", "/api/v1/namespaces/default/pods"},
		{"hypershift.openshift.io", "v1beta1", "nodepools", "ns1", "/apis/hypershift.openshift.io/v1beta1/namespaces/ns1/nodepools"},
		{"apiextensions.k8s.io", "v1", "customresourcedefinitions", "", "/apis/apiextensions.k8s.io/v1/customresourcedefinitions"},
	}
	for _, tt := range tests {
		if got := apiPathFor(tt.group, tt.version, tt.plural, tt.namespace); got != tt.want {
			t.Errorf("apiPathFor(%q,%q,%q,%q) = %q, want %q", tt.group, tt.version, tt.plural, tt.namespace, got, tt.want)
		}
	}
}

func TestWatchEventType(t *testing.T) {
	tests := map[string]string{"Add": "ADDED", "Update": "MODIFIED", "Delete": "DELETED"}
	for event, want := range tests {
		got, err := watchEventType(event)
		if err != nil {
			t.Errorf("watchEventType(%q) unexpected error: %v", event, err)
		}
		if got != want {
			t.Errorf("watchEventType(%q) = %q, want %q", event, got, want)
		}
	}
	if _, err := watchEventType("Bogus"); err == nil {
		t.Error("watchEventType(\"Bogus\") expected an error, got nil")
	}
}

// readKshrkEntry reads and zstd-decompresses (unless raw) a single entry from
// a .kshrk archive, exactly as a third-party reader following
// docs/archive-format.md would.
func readKshrkEntry(t *testing.T, archivePath, name string, raw bool) []byte {
	t.Helper()
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatalf("opening archive: %v", err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("opening entry %s: %v", name, err)
		}
		defer rc.Close()
		data, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("reading entry %s: %v", name, err)
		}
		if raw {
			return data
		}
		dec, err := zstd.NewReader(nil)
		if err != nil {
			t.Fatalf("creating zstd decoder: %v", err)
		}
		defer dec.Close()
		decompressed, err := dec.DecodeAll(data, nil)
		if err != nil {
			t.Fatalf("decompressing entry %s: %v", name, err)
		}
		return decompressed
	}
	t.Fatalf("entry %s not found in archive", name)
	return nil
}

func kshrkPathDir(apiPath string) string {
	sum := sha256.Sum256([]byte(apiPath))
	return fmt.Sprintf("%x", sum[:8])
}

// TestKshrkWriter_EmptyBaselinePlaceholder_ForWatchOnlyCollection covers the
// case oc-adm-inspect hits against a freshly-booted (or otherwise
// baseline-empty) mgmt cluster: a collection with real watch history but zero
// poll-baseline rows. Finish should register an empty poll placeholder so
// k8shark's cross-namespace "-A" aggregation (which enumerates candidates
// from index.json only) can discover the collection at all.
func TestKshrkWriter_EmptyBaselinePlaceholder_ForWatchOnlyCollection(t *testing.T) {
	ctx := context.Background()
	archivePath := filepath.Join(t.TempDir(), "capture.kshrk")
	windowStart := time.Date(2026, 9, 5, 6, 20, 0, 0, time.UTC)
	windowEnd := windowStart.Add(time.Hour)

	w, err := NewKshrkWriter(archivePath, "ci01-mgmt-1", windowStart, windowEnd)
	if err != nil {
		t.Fatalf("NewKshrkWriter: %v", err)
	}

	// No Pod in the poll baseline at all (e.g. no row within the lookback —
	// the cluster only booted around windowStart).
	if err := w.WriteResources(ctx, "", nil); err != nil {
		t.Fatalf("WriteResources: %v", err)
	}

	history := []ResourceEvent{
		{
			Timestamp: windowStart.Add(time.Minute),
			Event:     "Add",
			Resource: Resource{
				APIVersion: "v1",
				Kind:       "Pod",
				Namespace:  "arobit",
				Name:       "arobit-forwarder-abcde",
				Object:     map[string]any{"metadata": map[string]any{"name": "arobit-forwarder-abcde", "namespace": "arobit"}},
			},
		},
	}
	if err := w.WriteResourceHistory(ctx, history); err != nil {
		t.Fatalf("WriteResourceHistory: %v", err)
	}
	if err := w.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	podPath := "/api/v1/namespaces/arobit/pods"

	var index struct {
		Entries map[string]struct {
			Seqs   []int `json:"seqs"`
			Counts []int `json:"counts"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(readKshrkEntry(t, archivePath, "k8shark-capture/index.json.zst", false), &index); err != nil {
		t.Fatalf("unmarshaling index.json.zst: %v", err)
	}
	entry, ok := index.Entries[podPath]
	if !ok {
		t.Fatalf("index.json.zst missing placeholder entry for %s — \"-A\" aggregation would never discover this collection", podPath)
	}
	if len(entry.Counts) != 1 || entry.Counts[0] != 0 {
		t.Errorf("placeholder entry Counts = %v, want [0]", entry.Counts)
	}

	// The placeholder record itself is an empty, well-formed PodList.
	dir := kshrkPathDir(podPath)
	var rec kshrk.Record
	if err := json.Unmarshal(readKshrkEntry(t, archivePath, "k8shark-capture/records/"+dir+"/"+fmt.Sprint(entry.Seqs[0])+".json.zst", false), &rec); err != nil {
		t.Fatalf("unmarshaling placeholder record: %v", err)
	}
	var list struct {
		Kind  string           `json:"kind"`
		Items []map[string]any `json:"items"`
	}
	bodyBytes, err := json.Marshal(rec.ResponseBody)
	if err != nil {
		t.Fatalf("marshaling response_body: %v", err)
	}
	if err := json.Unmarshal(bodyBytes, &list); err != nil {
		t.Fatalf("unmarshaling PodList: %v", err)
	}
	if list.Kind != "PodList" {
		t.Errorf("placeholder Kind = %q, want PodList", list.Kind)
	}
	if len(list.Items) != 0 {
		t.Errorf("placeholder Items = %v, want empty", list.Items)
	}

	// Sanity: the real watch record is still there too.
	var watchIndex struct {
		Entries map[string]struct {
			EventTypes []string `json:"event_types"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(readKshrkEntry(t, archivePath, "k8shark-capture/watch-index.json.zst", false), &watchIndex); err != nil {
		t.Fatalf("unmarshaling watch-index.json.zst: %v", err)
	}
	if _, ok := watchIndex.Entries[podPath]; !ok {
		t.Fatalf("watch-index.json.zst missing entry for %s", podPath)
	}
}

func TestKshrkWriter_WholeClusterExport(t *testing.T) {
	ctx := context.Background()
	archivePath := filepath.Join(t.TempDir(), "capture.kshrk")
	windowStart := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	windowEnd := windowStart.Add(time.Hour)

	w, err := NewKshrkWriter(archivePath, "mgmt-1", windowStart, windowEnd)
	if err != nil {
		t.Fatalf("NewKshrkWriter: %v", err)
	}

	node := Resource{
		APIVersion: "v1",
		Kind:       "Node",
		Namespace:  "",
		Name:       "node-1",
		Object: map[string]any{
			"metadata": map[string]any{"name": "node-1"},
			"status": map[string]any{
				"nodeInfo": map[string]any{"kubeletVersion": "v1.30.2"},
			},
		},
	}
	nodePoolCRD := crdResource("hypershift.openshift.io", "NodePool", "nodepools", "nodepool", []string{"np"}, true)
	nodePool := Resource{
		APIVersion: "hypershift.openshift.io/v1beta1",
		Kind:       "NodePool",
		Namespace:  "ocm-hcp-abc123",
		Name:       "workers",
		Object:     map[string]any{"metadata": map[string]any{"name": "workers"}},
	}

	if err := w.WriteResources(ctx, "", []Resource{node, nodePoolCRD, nodePool}); err != nil {
		t.Fatalf("WriteResources: %v", err)
	}

	history := []ResourceEvent{
		{
			Timestamp: windowStart.Add(5 * time.Minute),
			Event:     "Update",
			Resource: Resource{
				APIVersion: "hypershift.openshift.io/v1beta1",
				Kind:       "NodePool",
				Namespace:  "ocm-hcp-abc123",
				Name:       "workers",
				Object:     map[string]any{"metadata": map[string]any{"name": "workers"}, "spec": map[string]any{"replicas": 3}},
			},
		},
	}
	if err := w.WriteResourceHistory(ctx, history); err != nil {
		t.Fatalf("WriteResourceHistory: %v", err)
	}

	events := []map[string]any{
		{
			"eventNamespace":   "ocm-hcp-abc123",
			"reason":           "ScalingReplicaSet",
			"message":          "Scaled up replica set",
			"objectKind":       "NodePool",
			"objectApiVersion": "hypershift.openshift.io/v1beta1",
			"objectName":       "workers",
			"kubeEventType":    "Normal",
			"sourceComponent":  "hypershift-operator",
			"firstSeen":        "2026-09-05T10:05:00Z",
			"lastSeen":         "2026-09-05T10:05:00Z",
			"count":            "1",
		},
		{
			// No eventNamespace: a cluster-scoped involved object (e.g. Node).
			"eventNamespace":   "",
			"reason":           "NodeReady",
			"objectKind":       "Node",
			"objectApiVersion": "v1",
			"objectName":       "node-1",
			"kubeEventType":    "Normal",
			"count":            "1",
		},
	}
	if err := w.WriteEvents(ctx, "", events); err != nil {
		t.Fatalf("WriteEvents: %v", err)
	}

	if err := w.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	// metadata.json: kubernetes_version resolved from the captured Node.
	var meta struct {
		KubernetesVersion string `json:"kubernetes_version"`
		ServerAddress     string `json:"server_address"`
	}
	if err := json.Unmarshal(readKshrkEntry(t, archivePath, "k8shark-capture/metadata.json", true), &meta); err != nil {
		t.Fatalf("unmarshaling metadata.json: %v", err)
	}
	if meta.KubernetesVersion != "v1.30.2" {
		t.Errorf("metadata.json kubernetes_version = %q, want %q", meta.KubernetesVersion, "v1.30.2")
	}
	if meta.ServerAddress != "kusto-reconstructed://mgmt-1" {
		t.Errorf("metadata.json server_address = %q, want %q", meta.ServerAddress, "kusto-reconstructed://mgmt-1")
	}

	// index.json.zst: the cluster-scoped Node list and the namespaced NodePool
	// list both got poll records, at distinct api_paths.
	var index struct {
		Entries map[string]struct {
			Seqs []int `json:"seqs"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(readKshrkEntry(t, archivePath, "k8shark-capture/index.json.zst", false), &index); err != nil {
		t.Fatalf("unmarshaling index.json.zst: %v", err)
	}
	for _, path := range []string{
		"/api/v1/nodes",
		"/apis/hypershift.openshift.io/v1beta1/namespaces/ocm-hcp-abc123/nodepools",
		"/apis/apiextensions.k8s.io/v1/customresourcedefinitions",
		"/api/v1/namespaces/ocm-hcp-abc123/events",
		"/api/v1/namespaces/default/events",     // the cluster-scoped-object event falls back to "default"
		"/apis/hypershift.openshift.io/v1beta1", // discovery record
		"/api/v1",                               // discovery record
	} {
		if _, ok := index.Entries[path]; !ok {
			t.Errorf("index.json.zst missing expected api_path %q", path)
		}
	}

	// watch-index.json.zst: the one history event, MODIFIED, under the
	// NodePool's namespaced path.
	var watchIndex struct {
		Entries map[string]struct {
			EventTypes []string `json:"event_types"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(readKshrkEntry(t, archivePath, "k8shark-capture/watch-index.json.zst", false), &watchIndex); err != nil {
		t.Fatalf("unmarshaling watch-index.json.zst: %v", err)
	}
	nodePoolPath := "/apis/hypershift.openshift.io/v1beta1/namespaces/ocm-hcp-abc123/nodepools"
	entry, ok := watchIndex.Entries[nodePoolPath]
	if !ok {
		t.Fatalf("watch-index.json.zst missing entry for %s", nodePoolPath)
	}
	if len(entry.EventTypes) != 1 || entry.EventTypes[0] != "MODIFIED" {
		t.Errorf("watch-index EventTypes = %v, want [MODIFIED]", entry.EventTypes)
	}

	// Discovery: the NodePool CRD's exact name/shortNames won, not the
	// heuristic (which would also happen to guess "nodepools" here, but the
	// singular/shortNames only come from the captured CRD).
	dir := kshrkPathDir("/apis/hypershift.openshift.io/v1beta1")
	var discoveryList struct {
		Resources []struct {
			Name         string   `json:"name"`
			SingularName string   `json:"singularName"`
			ShortNames   []string `json:"shortNames"`
			Namespaced   bool     `json:"namespaced"`
		} `json:"resources"`
	}
	var rec kshrk.Record
	if err := json.Unmarshal(readKshrkEntry(t, archivePath, "k8shark-capture/records/"+dir+"/0.json.zst", false), &rec); err != nil {
		t.Fatalf("unmarshaling discovery record: %v", err)
	}
	bodyBytes, err := json.Marshal(rec.ResponseBody)
	if err != nil {
		t.Fatalf("marshaling response_body: %v", err)
	}
	if err := json.Unmarshal(bodyBytes, &discoveryList); err != nil {
		t.Fatalf("unmarshaling APIResourceList: %v", err)
	}
	if len(discoveryList.Resources) != 1 {
		t.Fatalf("discovery list has %d resources, want 1", len(discoveryList.Resources))
	}
	got := discoveryList.Resources[0]
	if got.Name != "nodepools" || got.SingularName != "nodepool" || !got.Namespaced {
		t.Errorf("discovery entry = %+v, want name=nodepools singular=nodepool namespaced=true", got)
	}
	if len(got.ShortNames) != 1 || got.ShortNames[0] != "np" {
		t.Errorf("discovery entry ShortNames = %v, want [np]", got.ShortNames)
	}
}

// TestKshrkWriter_Discovery_BuiltinShortNames covers "kubectl get ns" (short
// name) working, not just "kubectl get namespaces" (exact plural). Built-in
// Kinds have no CustomResourceDefinition, so CRDNameResolver can never supply
// ShortNames for them — and because we capture our own /api/v1 discovery
// record, k8shark serves it verbatim instead of falling back to its own
// built-in short-name table, so this tool must supply ShortNames itself.
func TestKshrkWriter_Discovery_BuiltinShortNames(t *testing.T) {
	ctx := context.Background()
	archivePath := filepath.Join(t.TempDir(), "capture.kshrk")
	windowStart := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	windowEnd := windowStart.Add(time.Hour)

	w, err := NewKshrkWriter(archivePath, "mgmt-1", windowStart, windowEnd)
	if err != nil {
		t.Fatalf("NewKshrkWriter: %v", err)
	}

	namespace := Resource{
		APIVersion: "v1",
		Kind:       "Namespace",
		Namespace:  "",
		Name:       "arobit",
		Object:     map[string]any{"metadata": map[string]any{"name": "arobit"}},
	}
	if err := w.WriteResources(ctx, "", []Resource{namespace}); err != nil {
		t.Fatalf("WriteResources: %v", err)
	}
	if err := w.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	dir := kshrkPathDir("/api/v1")
	var rec kshrk.Record
	if err := json.Unmarshal(readKshrkEntry(t, archivePath, "k8shark-capture/records/"+dir+"/0.json.zst", false), &rec); err != nil {
		t.Fatalf("unmarshaling discovery record: %v", err)
	}
	bodyBytes, err := json.Marshal(rec.ResponseBody)
	if err != nil {
		t.Fatalf("marshaling response_body: %v", err)
	}
	var discoveryList struct {
		Resources []struct {
			Name       string   `json:"name"`
			ShortNames []string `json:"shortNames"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(bodyBytes, &discoveryList); err != nil {
		t.Fatalf("unmarshaling APIResourceList: %v", err)
	}
	if len(discoveryList.Resources) != 1 || discoveryList.Resources[0].Name != "namespaces" {
		t.Fatalf("discovery resources = %+v, want exactly one entry named namespaces", discoveryList.Resources)
	}
	shortNames := discoveryList.Resources[0].ShortNames
	if len(shortNames) != 1 || shortNames[0] != "ns" {
		t.Errorf("namespaces ShortNames = %v, want [ns] (this is what makes \"kubectl get ns\" resolve)", shortNames)
	}
}
