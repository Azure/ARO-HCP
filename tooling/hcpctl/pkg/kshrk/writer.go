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
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
)

// archiveRoot is the top-level directory name inside the ZIP container,
// matching k8shark's documented layout.
const archiveRoot = "k8shark-capture"

// epochModTime is a fixed, valid ZIP entry timestamp so archives are
// deterministic byte-for-byte given identical input, mirroring k8shark's own
// writer.
var epochModTime = time.Date(1980, 1, 1, 12, 0, 0, 0, time.UTC)

// validEventTypes are the only event_type values a watch record may carry.
var validEventTypes = map[string]bool{"ADDED": true, "MODIFIED": true, "DELETED": true}

// ArchiveWriter builds a .kshrk archive by accepting poll and watch records
// and writing metadata.json, index.json.zst, watch-index.json.zst, and
// records/<pathDir>/<seq>.json.zst on Finish. Not safe for concurrent use.
type ArchiveWriter struct {
	f  *os.File
	zw *zip.Writer

	// pathSeq assigns the shared, per-apiPath sequence number every record
	// (poll or watch) under that path is numbered from, matching k8shark's
	// own writer — index.json and watch-index.json reference disjoint seqs
	// out of the same per-path sequence, not independent counters.
	pathSeq map[string]int

	index       index
	watchIndex  watchIndex
	recordCount int

	closed bool
}

// NewArchiveWriter creates a new .kshrk archive at outputPath.
func NewArchiveWriter(outputPath string) (*ArchiveWriter, error) {
	f, err := os.Create(outputPath)
	if err != nil {
		return nil, fmt.Errorf("creating archive %q: %w", outputPath, err)
	}
	return &ArchiveWriter{
		f:          f,
		zw:         zip.NewWriter(f),
		pathSeq:    make(map[string]int),
		index:      index{Entries: make(map[string]*IndexEntry)},
		watchIndex: watchIndex{Entries: make(map[string]*WatchIndexEntry)},
	}, nil
}

// WritePollRecord writes a full poll response (a List body) under apiPath and
// registers it in index.json. itemCount is the number of top-level items in
// body, recorded as index.json's optional Counts field.
func (a *ArchiveWriter) WritePollRecord(apiPath string, capturedAt time.Time, body any, itemCount int) error {
	seq, err := a.writeRecord(apiPath, "", capturedAt, body)
	if err != nil {
		return err
	}
	entry, ok := a.index.Entries[apiPath]
	if !ok {
		entry = &IndexEntry{APIPath: apiPath}
		a.index.Entries[apiPath] = entry
	}
	entry.Seqs = append(entry.Seqs, seq)
	entry.Times = append(entry.Times, capturedAt)
	entry.Counts = append(entry.Counts, itemCount)
	return nil
}

// WriteWatchRecord writes a single-object watch event (eventType: ADDED,
// MODIFIED, or DELETED) under apiPath and registers it in watch-index.json.
func (a *ArchiveWriter) WriteWatchRecord(apiPath, eventType string, capturedAt time.Time, body any) error {
	if !validEventTypes[eventType] {
		return fmt.Errorf("invalid watch event type %q for %s: must be ADDED, MODIFIED, or DELETED", eventType, apiPath)
	}
	seq, err := a.writeRecord(apiPath, eventType, capturedAt, body)
	if err != nil {
		return err
	}
	entry, ok := a.watchIndex.Entries[apiPath]
	if !ok {
		entry = &WatchIndexEntry{APIPath: apiPath}
		a.watchIndex.Entries[apiPath] = entry
	}
	entry.Seqs = append(entry.Seqs, seq)
	entry.Times = append(entry.Times, capturedAt)
	entry.EventTypes = append(entry.EventTypes, eventType)
	return nil
}

// RecordCount returns the number of records written so far.
func (a *ArchiveWriter) RecordCount() int {
	return a.recordCount
}

// writeRecord marshals rec, zstd-compresses it, and writes it to
// records/<pathDir>/<seq>.json.zst, returning the assigned seq.
func (a *ArchiveWriter) writeRecord(apiPath, eventType string, capturedAt time.Time, body any) (int, error) {
	rec := Record{
		ID:           uuid.NewString(),
		CapturedAt:   capturedAt,
		APIPath:      apiPath,
		EventType:    eventType,
		HTTPMethod:   "GET",
		ResponseCode: 200,
		ResponseBody: body,
	}
	recBytes, err := json.Marshal(rec)
	if err != nil {
		return 0, fmt.Errorf("marshaling record for %s: %w", apiPath, err)
	}
	compressed, err := zstdCompress(recBytes)
	if err != nil {
		return 0, fmt.Errorf("compressing record for %s: %w", apiPath, err)
	}

	seq := a.pathSeq[apiPath]
	a.pathSeq[apiPath] = seq + 1

	entryName := fmt.Sprintf("%s/records/%s/%d.json.zst", archiveRoot, pathDir(apiPath), seq)
	if err := a.writeZipEntry(entryName, compressed); err != nil {
		return 0, err
	}
	a.recordCount++
	return seq, nil
}

// pathDir returns the filesystem-safe directory name for an API path: the
// first 16 hex chars of SHA-256(apiPath), matching k8shark's documented
// derivation.
func pathDir(apiPath string) string {
	sum := sha256.Sum256([]byte(apiPath))
	return fmt.Sprintf("%x", sum[:8])
}

func (a *ArchiveWriter) writeZipEntry(name string, data []byte) error {
	hdr := &zip.FileHeader{
		Name:     name,
		Method:   zip.Store, // payloads are already zstd-compressed (or tiny, for metadata.json)
		Modified: epochModTime,
	}
	w, err := a.zw.CreateHeader(hdr)
	if err != nil {
		return fmt.Errorf("creating zip entry %s: %w", name, err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("writing zip entry %s: %w", name, err)
	}
	return nil
}

func zstdCompress(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	enc, err := zstd.NewWriter(&buf)
	if err != nil {
		return nil, fmt.Errorf("creating zstd encoder: %w", err)
	}
	if _, err := enc.Write(data); err != nil {
		_ = enc.Close()
		return nil, fmt.Errorf("zstd-compressing data: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("closing zstd encoder: %w", err)
	}
	return buf.Bytes(), nil
}

// Finish writes metadata.json (uncompressed), index.json.zst, and (when any
// watch records were written) watch-index.json.zst, then closes the archive.
// meta's FormatVersion and RecordCount are set automatically.
func (a *ArchiveWriter) Finish(meta CaptureMetadata) error {
	meta.FormatVersion = CurrentFormatVersion
	meta.RecordCount = a.recordCount

	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshaling metadata.json: %w", err)
	}
	if err := a.writeZipEntry(archiveRoot+"/metadata.json", metaBytes); err != nil {
		return err
	}

	if err := a.writeCompressedJSON(archiveRoot+"/index.json.zst", a.index); err != nil {
		return fmt.Errorf("writing index.json.zst: %w", err)
	}

	if len(a.watchIndex.Entries) > 0 {
		if err := a.writeCompressedJSON(archiveRoot+"/watch-index.json.zst", a.watchIndex); err != nil {
			return fmt.Errorf("writing watch-index.json.zst: %w", err)
		}
	}

	a.closed = true
	if err := a.zw.Close(); err != nil {
		return fmt.Errorf("closing zip writer: %w", err)
	}
	if err := a.f.Close(); err != nil {
		return fmt.Errorf("closing archive file: %w", err)
	}
	return nil
}

func (a *ArchiveWriter) writeCompressedJSON(name string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshaling: %w", err)
	}
	compressed, err := zstdCompress(data)
	if err != nil {
		return err
	}
	return a.writeZipEntry(name, compressed)
}

// Abort discards a partially-written archive: it closes the underlying file
// handles without writing metadata/index entries and removes the output file.
// Call this on an error path in place of Finish.
func (a *ArchiveWriter) Abort() error {
	if a.closed {
		return nil
	}
	a.closed = true
	name := a.f.Name()
	_ = a.zw.Close()
	_ = a.f.Close()
	return os.Remove(name)
}
