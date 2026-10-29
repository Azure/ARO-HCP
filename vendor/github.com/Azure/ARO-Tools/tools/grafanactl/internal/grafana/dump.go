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

package grafana

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	httptransport "github.com/go-openapi/runtime/client"
	goapi "github.com/grafana/grafana-openapi-client-go/client"
)

const (
	// httpDumpDirEnv, when set, records every Grafana HTTP round-trip to that
	// directory. Authorization values and PII fields are redacted; request and
	// response bodies are dumped in full so the recorded traffic can be verified.
	httpDumpDirEnv   = "GRAFANACTL_HTTP_DUMP_DIR"
	httpDumpFilePerm = 0o600
	httpDumpDirPerm  = 0o700
)

type dumpedCall struct {
	Seq            int             `json:"seq"`
	Client         string          `json:"client,omitempty"`
	Method         string          `json:"method"`
	Path           string          `json:"path"`
	Query          string          `json:"query,omitempty"`
	Authorization  string          `json:"authorization"`
	RequestBody    json.RawMessage `json:"requestBody,omitempty"`
	ResponseStatus int             `json:"responseStatus"`
	ResponseBytes  int             `json:"responseBytes"`
	ResponseBody   json.RawMessage `json:"responseBody,omitempty"`
}

type httpDumper struct {
	dir     string
	mu      sync.Mutex
	seq     int
	lastErr error
}

func attachHTTPDump(api *goapi.GrafanaHTTPAPI, httpClient *http.Client) error {
	dir := os.Getenv(httpDumpDirEnv)
	if dir == "" {
		return nil
	}
	if err := ensureDumpDir(dir); err != nil {
		return fmt.Errorf("%s: %w", httpDumpDirEnv, err)
	}
	dumper := &httpDumper{dir: dir}
	httpClient.Transport = wrapDumpTransport(httpClient.Transport, dumper)
	runtime, ok := api.Transport.(*httptransport.Runtime)
	if !ok {
		return fmt.Errorf("grafana OpenAPI transport is %T, cannot attach HTTP dump", api.Transport)
	}
	runtime.Transport = wrapDumpTransport(runtime.Transport, dumper)
	return nil
}

func wrapDumpTransport(base http.RoundTripper, dumper *httpDumper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &dumpRoundTripper{base: base, dumper: dumper}
}

type dumpRoundTripper struct {
	base   http.RoundTripper
	dumper *httpDumper
}

func (d *dumpRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	reqBody, err := snapshotBody(&req.Body)
	if err != nil {
		return nil, err
	}
	if len(reqBody) > 0 {
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(reqBody)), nil
		}
		req.ContentLength = int64(len(reqBody))
		req.Body, _ = req.GetBody()
	}

	resp, err := d.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	respBody, err := snapshotBody(&resp.Body)
	if err != nil {
		return nil, err
	}

	if err := d.dumper.record(req, reqBody, resp.StatusCode, respBody); err != nil {
		fmt.Fprintf(os.Stderr, "grafanactl: %s: %v\n", httpDumpDirEnv, err)
	}
	return resp, nil
}

func ensureDumpDir(dir string) error {
	if err := os.MkdirAll(dir, httpDumpDirPerm); err != nil {
		return err
	}
	// MkdirAll does not tighten an existing directory's mode.
	return os.Chmod(dir, httpDumpDirPerm)
}

func snapshotBody(body *io.ReadCloser) ([]byte, error) {
	if body == nil || *body == nil {
		return nil, nil
	}
	orig := *body
	raw, err := io.ReadAll(orig)
	if err != nil {
		_ = orig.Close()
		return nil, err
	}
	if err := orig.Close(); err != nil {
		return nil, err
	}
	*body = io.NopCloser(bytes.NewReader(raw))
	return raw, nil
}

func (d *httpDumper) record(req *http.Request, reqBody []byte, status int, respBody []byte) error {
	d.mu.Lock()
	d.seq++
	seq := d.seq
	d.mu.Unlock()

	call := dumpedCall{
		Seq:            seq,
		Method:         req.Method,
		Path:           req.URL.Path,
		Query:          req.URL.RawQuery,
		Authorization:  redactAuthorization(req.Header.Get("Authorization")),
		ResponseStatus: status,
		ResponseBytes:  len(respBody),
		ResponseBody:   dumpJSONBody(respBody),
	}
	if len(bytes.TrimSpace(reqBody)) > 0 {
		call.RequestBody = redactDumpPII(dumpJSONBody(reqBody))
	}
	call.ResponseBody = redactDumpPII(call.ResponseBody)

	payload, err := json.MarshalIndent(call, "", "  ")
	if err != nil {
		return d.setErr(fmt.Errorf("marshal dump: %w", err))
	}
	name := fmt.Sprintf("%03d-%s-%s.json", seq, req.Method, sanitizeDumpName(req.URL.Path))
	if err := os.WriteFile(filepath.Join(d.dir, name), append(payload, '\n'), httpDumpFilePerm); err != nil {
		return d.setErr(fmt.Errorf("write dump %s: %w", name, err))
	}
	return nil
}

func (d *httpDumper) setErr(err error) error {
	d.mu.Lock()
	d.lastErr = err
	d.mu.Unlock()
	return err
}

func (d *httpDumper) err() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastErr
}

func redactAuthorization(value string) string {
	if value == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(value), "bearer ") {
		return "Bearer <redacted>"
	}
	return "<redacted>"
}

func sanitizeDumpName(path string) string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return "root"
	}
	return strings.ReplaceAll(trimmed, "/", "_")
}

func dumpJSONBody(raw []byte) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}
	if !json.Valid(trimmed) {
		return nonJSONBody(raw)
	}
	return append([]byte(nil), raw...)
}

func nonJSONBody(raw []byte) json.RawMessage {
	out, err := json.Marshal(struct {
		NonJSON bool `json:"_nonJSON"`
		Bytes   int  `json:"bytes"`
	}{NonJSON: true, Bytes: len(raw)})
	if err != nil {
		return json.RawMessage(`{"_nonJSON":true}`)
	}
	return out
}

var dumpPIIKeys = map[string]struct{}{
	"useremail":     {},
	"userlogin":     {},
	"useravatarurl": {},
	"useruid":       {},
	"teamemail":     {},
	"email":         {},
}

func redactDumpPII(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return raw
	}
	redacted, err := json.Marshal(redactDumpValue(value))
	if err != nil {
		return raw
	}
	return redacted
}

func redactDumpValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if _, pii := dumpPIIKeys[strings.ToLower(key)]; pii {
				if s, ok := item.(string); ok && s != "" {
					out[key] = "<redacted>"
					continue
				}
			}
			out[key] = redactDumpValue(item)
		}
		return out
	case []any:
		for i, item := range typed {
			typed[i] = redactDumpValue(item)
		}
		return typed
	default:
		return value
	}
}
