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

package gatherobservability

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/Azure/ARO-HCP/test/util/timing"
)

const (
	maxEvidenceResponseBytes int64 = 16 << 20
	maxEvidenceBytes         int64 = 128 << 20
)

type evidenceRequest struct {
	Kind      string    `json:"kind"`
	Source    string    `json:"source"`
	Workspace string    `json:"workspace,omitempty"`
	Title     string    `json:"title,omitempty"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
}

type evidenceEntry struct {
	evidenceRequest
	Method            string     `json:"method,omitempty"`
	Endpoint          string     `json:"endpoint,omitempty"`
	Parameters        url.Values `json:"parameters,omitempty"`
	RequestedAt       time.Time  `json:"requestedAt"`
	CompletedAt       time.Time  `json:"completedAt,omitempty"`
	HTTPStatus        int        `json:"httpStatus,omitempty"`
	Status            string     `json:"status"`
	Failure           string     `json:"failure,omitempty"`
	Error             string     `json:"error,omitempty"`
	Path              string     `json:"path,omitempty"`
	UncompressedBytes int64      `json:"uncompressedBytes,omitempty"`
}

// The collector is used sequentially, just like the panel queries. Capture is
// driven by the consumer's reads: it never drains, replaces, or closes a body on
// the consumer's behalf, including on disk errors and oversized responses.
type evidenceCollector struct {
	dir       string
	window    timing.TimeWindow
	entries   []*evidenceEntry
	usedBytes int64
}

func newEvidenceCollector(outputDir string, window timing.TimeWindow) (*evidenceCollector, error) {
	dir := filepath.Join(outputDir, "evidence")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create evidence directory: %w", err)
	}
	return &evidenceCollector{dir: dir, window: window}, nil
}

func (c *evidenceCollector) remainingBytes() int64 {
	if c == nil {
		return 0
	}
	return max(int64(0), maxEvidenceBytes-c.usedBytes)
}

func (c *evidenceCollector) note(metadata evidenceRequest, status, detail string) {
	if c == nil {
		return
	}
	now := time.Now().UTC()
	c.entries = append(c.entries, &evidenceEntry{
		evidenceRequest: metadata, Status: status, Error: detail,
		RequestedAt: now, CompletedAt: now,
	})
}

func (c *evidenceCollector) writeManifest() error {
	if c == nil {
		return nil
	}
	manifest := struct {
		Version int `json:"version"`
		Window  struct {
			Start time.Time `json:"start"`
			End   time.Time `json:"end"`
		} `json:"window"`
		Entries []*evidenceEntry `json:"entries"`
	}{Version: 1, Entries: c.entries}
	manifest.Window.Start, manifest.Window.End = c.window.Start, c.window.End
	f, err := os.CreateTemp(c.dir, ".manifest-*")
	if err != nil {
		return fmt.Errorf("create evidence manifest: %w", err)
	}
	defer os.Remove(f.Name())
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	encodeErr := enc.Encode(manifest)
	closeErr := f.Close()
	if encodeErr != nil {
		return fmt.Errorf("write evidence manifest: %w", encodeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close evidence manifest: %w", closeErr)
	}
	return os.Rename(f.Name(), filepath.Join(c.dir, "manifest.json"))
}

func (c *evidenceCollector) client(base policy.Transporter, metadata evidenceRequest) policy.Transporter {
	if c == nil {
		return base
	}
	if base == nil {
		base = http.DefaultClient
	}
	return &evidenceTransport{base: base, collector: c, metadata: metadata}
}

type evidenceTransport struct {
	base      policy.Transporter
	collector *evidenceCollector
	metadata  evidenceRequest
}

func (t *evidenceTransport) Do(req *http.Request) (*http.Response, error) {
	entry := &evidenceEntry{
		evidenceRequest: t.metadata, Method: req.Method,
		RequestedAt: time.Now().UTC(), Status: "skipped",
		Failure: "unread-body", Error: "response body was not consumed",
	}
	if req.URL != nil {
		// Do not serialize User, RawQuery, Fragment, headers, or arbitrary query
		// parameters: URLs can carry credentials even without Authorization.
		entry.Endpoint = (&url.URL{Scheme: req.URL.Scheme, Host: req.URL.Host, Path: req.URL.Path, RawPath: req.URL.RawPath}).String()
		entry.Parameters = make(url.Values)
		for key, values := range req.URL.Query() {
			switch strings.ToLower(key) {
			case "query", "start", "end", "step", "time", "timeout", "api-version", "metricnames", "metricnamespace", "timespan", "interval", "aggregation", "$filter", "top", "orderby", "resulttype", "autoadjusttimegrain", "validatedimensions", "rollupby":
				entry.Parameters[key] = values
			}
		}
	}
	t.collector.entries = append(t.collector.entries, entry)
	resp, err := t.base.Do(req)
	if resp != nil {
		entry.HTTPStatus = resp.StatusCode
	}
	if err != nil {
		// Transport errors can embed the original credential-bearing URL.
		entry.finish("failed", "network", "HTTP request failed before a response could be captured")
		return resp, err
	}
	if resp == nil || resp.Body == nil {
		entry.finish("failed", "missing-body", "HTTP response has no body")
		return resp, err
	}
	resp.Body = &evidenceBody{ReadCloser: resp.Body, collector: t.collector, entry: entry}
	return resp, nil
}

func (e *evidenceEntry) finish(status, failure, detail string) {
	e.Status, e.Failure, e.Error = status, failure, detail
	e.CompletedAt = time.Now().UTC()
}

type evidenceBody struct {
	io.ReadCloser
	collector *evidenceCollector
	entry     *evidenceEntry
	buffer    []byte
	finished  bool
}

func (b *evidenceBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if b.finished {
		return n, err
	}
	remaining := min(maxEvidenceResponseBytes-int64(len(b.buffer)), b.collector.remainingBytes())
	if int64(n) > remaining {
		failure, detail := "response-limit", "response exceeds the 16 MiB capture limit"
		if b.collector.remainingBytes() < maxEvidenceResponseBytes-int64(len(b.buffer)) {
			failure, detail = "total-limit", "collection exhausted the 128 MiB capture budget"
		}
		b.stop("limited", failure, detail)
		return n, err
	}
	if n > 0 {
		if len(b.buffer)+n > cap(b.buffer) {
			// Explicit capacity growth prevents an arbitrary caller read size
			// from making append allocate beyond the per-response cap.
			capacity := min(int(maxEvidenceResponseBytes), max(4096, 2*cap(b.buffer), len(b.buffer)+n))
			grown := make([]byte, len(b.buffer), capacity)
			copy(grown, b.buffer)
			b.buffer = grown
		}
		b.buffer = append(b.buffer, p[:n]...)
		b.collector.usedBytes += int64(n)
		b.entry.UncompressedBytes += int64(n)
	}
	if err == io.EOF {
		b.save()
	} else if err != nil {
		b.stop("failed", "read", "response body could not be read completely")
	}
	return n, err
}

func (b *evidenceBody) Close() error {
	if !b.finished {
		b.stop("skipped", "incomplete-body", "consumer closed the response before EOF")
	}
	return b.ReadCloser.Close()
}

func (b *evidenceBody) stop(status, failure, detail string) {
	b.finished = true
	b.buffer = nil
	b.entry.finish(status, failure, detail)
}

func (b *evidenceBody) save() {
	if !json.Valid(b.buffer) {
		b.stop("failed", "malformed-json", "response body is not complete valid JSON")
		return
	}
	f, err := os.CreateTemp(b.collector.dir, ".response-*")
	if err != nil {
		b.stop("failed", "write", "could not create native response artifact")
		return
	}
	defer os.Remove(f.Name())
	z := gzip.NewWriter(f)
	_, writeErr := z.Write(b.buffer)
	gzipErr := z.Close()
	closeErr := f.Close()
	if writeErr != nil || gzipErr != nil || closeErr != nil {
		b.stop("failed", "write", "could not write complete compressed native response")
		return
	}
	name := strings.TrimPrefix(filepath.Base(f.Name()), ".") + ".json.gz"
	if err := os.Rename(f.Name(), filepath.Join(b.collector.dir, name)); err != nil {
		b.stop("failed", "write", "could not publish native response artifact")
		return
	}
	b.entry.Path = filepath.ToSlash(filepath.Join("evidence", name))
	if b.entry.HTTPStatus < 200 || b.entry.HTTPStatus >= 300 {
		b.stop("failed", "http", fmt.Sprintf("HTTP response status %d; native error body preserved", b.entry.HTTPStatus))
	} else {
		b.stop("complete", "", "")
	}
}
