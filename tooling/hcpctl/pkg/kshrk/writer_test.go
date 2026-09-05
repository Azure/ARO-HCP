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

package kshrk

import (
	"archive/zip"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

// readZipEntry reads and, unless raw is true, zstd-decompresses a single
// entry from a plain (unencrypted) .kshrk archive — exercising the archive
// exactly as a third-party reader following docs/archive-format.md would,
// without importing anything from k8shark itself.
func readZipEntry(t *testing.T, archivePath, name string, raw bool) []byte {
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

func TestArchiveWriter_RoundTrip(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "capture.kshrk")
	aw, err := NewArchiveWriter(archivePath)
	if err != nil {
		t.Fatalf("NewArchiveWriter: %v", err)
	}

	t0 := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	t1 := t0.Add(30 * time.Second)

	pollBody := map[string]any{
		"apiVersion": "v1",
		"kind":       "NodeList",
		"items": []any{
			map[string]any{"metadata": map[string]any{"name": "node-1"}},
		},
	}
	if err := aw.WritePollRecord("/api/v1/nodes", t0, pollBody, 1); err != nil {
		t.Fatalf("WritePollRecord: %v", err)
	}

	watchBody := map[string]any{
		"apiVersion": "v1",
		"kind":       "Node",
		"metadata":   map[string]any{"name": "node-2"},
	}
	if err := aw.WriteWatchRecord("/api/v1/nodes", "ADDED", t1, watchBody); err != nil {
		t.Fatalf("WriteWatchRecord: %v", err)
	}

	if got, want := aw.RecordCount(), 2; got != want {
		t.Errorf("RecordCount() = %d, want %d", got, want)
	}

	meta := CaptureMetadata{
		CaptureID:         "test-capture-id",
		CapturedAt:        t0,
		CapturedUntil:     t1,
		KubernetesVersion: "v1.30.2",
		ServerAddress:     "kusto-reconstructed://mgmt-1",
	}
	if err := aw.Finish(meta); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	// metadata.json is stored uncompressed.
	var gotMeta CaptureMetadata
	if err := json.Unmarshal(readZipEntry(t, archivePath, "k8shark-capture/metadata.json", true), &gotMeta); err != nil {
		t.Fatalf("unmarshaling metadata.json: %v", err)
	}
	if gotMeta.FormatVersion != CurrentFormatVersion {
		t.Errorf("FormatVersion = %d, want %d", gotMeta.FormatVersion, CurrentFormatVersion)
	}
	if gotMeta.RecordCount != 2 {
		t.Errorf("RecordCount = %d, want 2", gotMeta.RecordCount)
	}
	if gotMeta.CaptureID != "test-capture-id" {
		t.Errorf("CaptureID = %q, want %q", gotMeta.CaptureID, "test-capture-id")
	}

	// index.json.zst wraps the map under "entries" (format version 2).
	var gotIndex index
	if err := json.Unmarshal(readZipEntry(t, archivePath, "k8shark-capture/index.json.zst", false), &gotIndex); err != nil {
		t.Fatalf("unmarshaling index.json.zst: %v", err)
	}
	pollEntry, ok := gotIndex.Entries["/api/v1/nodes"]
	if !ok {
		t.Fatalf("index.json.zst missing entry for /api/v1/nodes")
	}
	if len(pollEntry.Seqs) != 1 || pollEntry.Seqs[0] != 0 {
		t.Errorf("poll entry Seqs = %v, want [0]", pollEntry.Seqs)
	}
	if len(pollEntry.Counts) != 1 || pollEntry.Counts[0] != 1 {
		t.Errorf("poll entry Counts = %v, want [1]", pollEntry.Counts)
	}

	// watch-index.json.zst is present (we wrote a watch record) and wrapped
	// the same way, referencing the seq *after* the poll record's (shared
	// per-apiPath sequence space).
	var gotWatchIndex watchIndex
	if err := json.Unmarshal(readZipEntry(t, archivePath, "k8shark-capture/watch-index.json.zst", false), &gotWatchIndex); err != nil {
		t.Fatalf("unmarshaling watch-index.json.zst: %v", err)
	}
	watchEntry, ok := gotWatchIndex.Entries["/api/v1/nodes"]
	if !ok {
		t.Fatalf("watch-index.json.zst missing entry for /api/v1/nodes")
	}
	if len(watchEntry.Seqs) != 1 || watchEntry.Seqs[0] != 1 {
		t.Errorf("watch entry Seqs = %v, want [1]", watchEntry.Seqs)
	}
	if len(watchEntry.EventTypes) != 1 || watchEntry.EventTypes[0] != "ADDED" {
		t.Errorf("watch entry EventTypes = %v, want [ADDED]", watchEntry.EventTypes)
	}

	// The actual record files live under records/<pathDir>/<seq>.json.zst,
	// pathDir derived from SHA-256(apiPath) — same directory for both the
	// poll record (seq 0) and the watch record (seq 1), since they share an
	// api_path.
	dir := pathDir("/api/v1/nodes")
	if len(dir) != 16 {
		t.Fatalf("pathDir length = %d, want 16", len(dir))
	}

	var pollRec Record
	if err := json.Unmarshal(readZipEntry(t, archivePath, "k8shark-capture/records/"+dir+"/0.json.zst", false), &pollRec); err != nil {
		t.Fatalf("unmarshaling poll record: %v", err)
	}
	if pollRec.EventType != "" {
		t.Errorf("poll record EventType = %q, want empty", pollRec.EventType)
	}
	if pollRec.APIPath != "/api/v1/nodes" || pollRec.ResponseCode != 200 || pollRec.HTTPMethod != "GET" {
		t.Errorf("poll record envelope wrong: %+v", pollRec)
	}

	var watchRec Record
	if err := json.Unmarshal(readZipEntry(t, archivePath, "k8shark-capture/records/"+dir+"/1.json.zst", false), &watchRec); err != nil {
		t.Fatalf("unmarshaling watch record: %v", err)
	}
	if watchRec.EventType != "ADDED" {
		t.Errorf("watch record EventType = %q, want ADDED", watchRec.EventType)
	}
}

func TestArchiveWriter_NoWatchRecords_OmitsWatchIndex(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "capture.kshrk")
	aw, err := NewArchiveWriter(archivePath)
	if err != nil {
		t.Fatalf("NewArchiveWriter: %v", err)
	}
	if err := aw.WritePollRecord("/api/v1/nodes", time.Now(), map[string]any{"items": []any{}}, 0); err != nil {
		t.Fatalf("WritePollRecord: %v", err)
	}
	if err := aw.Finish(CaptureMetadata{CaptureID: "c1"}); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatalf("opening archive: %v", err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name == "k8shark-capture/watch-index.json.zst" {
			t.Fatalf("watch-index.json.zst should be omitted when no watch records were written")
		}
	}
}

func TestArchiveWriter_WriteWatchRecord_RejectsInvalidEventType(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "capture.kshrk")
	aw, err := NewArchiveWriter(archivePath)
	if err != nil {
		t.Fatalf("NewArchiveWriter: %v", err)
	}
	defer func() { _ = aw.Abort() }()

	err = aw.WriteWatchRecord("/api/v1/nodes", "UPDATED", time.Now(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for invalid event type, got nil")
	}
}
