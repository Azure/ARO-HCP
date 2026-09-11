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

// Package kshrk writes .kshrk archives: the portable capture format read by
// k8shark (https://github.com/phenixblue/k8shark), a Wireshark-style offline
// Kubernetes cluster replay tool. The format (ZIP container, Zstd-compressed
// JSON entries) is documented for third-party writers in k8shark's
// docs/archive-format.md; k8shark's own implementation lives entirely under
// internal/ and cannot be imported, so this package implements the documented
// schema independently. It only writes archives — reading one back is k8shark's
// job (via `kshrk open`/`kshrk ui`).
package kshrk

import "time"

// CurrentFormatVersion is the .kshrk archive schema version this package
// writes, matching k8shark's documented format_version 2 (index/watch-index
// wrapped under a top-level "entries" key).
const CurrentFormatVersion = 2

// CaptureMetadata is written as metadata.json inside the archive.
type CaptureMetadata struct {
	FormatVersion     int       `json:"format_version,omitempty"`
	CaptureID         string    `json:"capture_id"`
	CapturedAt        time.Time `json:"captured_at"`
	CapturedUntil     time.Time `json:"captured_until"`
	KubernetesVersion string    `json:"kubernetes_version"`
	ServerAddress     string    `json:"server_address"`
	RecordCount       int       `json:"record_count"`
}

// IndexEntry maps an API path to the ordered poll records captured for it.
// Seqs, Times, and Counts are parallel arrays ordered by capture time
// ascending; Counts is omitted when the caller never supplied an item count
// for that path (non-list-shaped responses).
type IndexEntry struct {
	APIPath string      `json:"api_path"`
	Seqs    []int       `json:"seqs"`
	Times   []time.Time `json:"times"`
	Counts  []int       `json:"counts,omitempty"`
}

// index is the on-disk shape of index.json.zst: format version 2 wraps the
// path->entry map under "entries" so sibling fields can be added later
// without another format-version bump.
type index struct {
	Entries map[string]*IndexEntry `json:"entries"`
}

// WatchIndexEntry maps an API path to the ordered watch events captured for
// it. Seqs, Times, and EventTypes are parallel arrays ordered by capture time
// ascending; each event_type is ADDED, MODIFIED, or DELETED.
type WatchIndexEntry struct {
	APIPath    string      `json:"api_path"`
	Seqs       []int       `json:"seqs"`
	Times      []time.Time `json:"times"`
	EventTypes []string    `json:"event_types"`
}

// watchIndex is the on-disk shape of watch-index.json.zst, wrapped under
// "entries" for the same reason as index.
type watchIndex struct {
	Entries map[string]*WatchIndexEntry `json:"entries"`
}

// Record holds one archived API response: a full List body for a poll record,
// or a single object body for a watch record (EventType set).
type Record struct {
	ID           string    `json:"id"`
	CapturedAt   time.Time `json:"captured_at"`
	APIPath      string    `json:"api_path"`
	EventType    string    `json:"event_type,omitempty"`
	HTTPMethod   string    `json:"http_method"`
	ResponseCode int       `json:"response_code"`
	ResponseBody any       `json:"response_body"`
}
