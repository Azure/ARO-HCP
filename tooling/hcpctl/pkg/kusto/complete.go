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

package kusto

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-kusto-go/azkustodata"
	"github.com/Azure/azure-kusto-go/azkustodata/query"
	queryv2 "github.com/Azure/azure-kusto-go/azkustodata/query/v2"
)

// MaxCompleteResponseBytes bounds the decoded payload retained for a complete read.
// Exceeding this limit is an error, never a truncated or sampled dataset.
const MaxCompleteResponseBytes int64 = 64 << 20

// CompleteQuery materializes a bounded, complete query using the SDK's request,
// authentication and typed decoding paths. A deadline is required. Publication
// checks always use strong consistency and disable the server result cache.
// All failures return a nil dataset.
func CompleteQuery(ctx context.Context, client *azkustodata.Client, database string, statement azkustodata.Statement, options ...azkustodata.QueryOption) (query.Dataset, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return nil, fmt.Errorf("complete Kusto query requires a deadline")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("complete Kusto query: %w", err)
	}
	if client == nil {
		return nil, fmt.Errorf("complete Kusto query requires a client")
	}
	// Do not mutate a caller-owned options slice. Append required options last
	// so callers cannot accidentally weaken a verification read.
	required := make([]azkustodata.QueryOption, 0, len(options)+7)
	required = append(required, options...)
	required = append(required,
		azkustodata.NoTruncation(),
		azkustodata.ServerTimeout(time.Until(deadline)),
		azkustodata.CustomQueryOption(azkustodata.QueryConsistencyValue, "strongconsistency"),
		azkustodata.CustomQueryOption(azkustodata.QueryResultsCacheMaxAgeValue, "00:00:00"),
		azkustodata.V2NewlinesBetweenFrames(),
		azkustodata.V2FragmentPrimaryTables(),
		azkustodata.ResultsErrorReportingPlacement(azkustodata.ResultsErrorReportingPlacementEndOfTable),
	)
	dataset, err := completeDataset(ctx, client, database, statement, required)
	if err != nil {
		return nil, fmt.Errorf("materialize complete Kusto query: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("complete Kusto query: %w", err)
	}
	var primaryCount int
	for _, table := range dataset.Tables() {
		if table.IsPrimaryResult() {
			primaryCount++
		}
	}
	if primaryCount != 1 {
		return nil, fmt.Errorf("complete Kusto query expected one primary table, got %d", primaryCount)
	}
	return dataset, nil
}

// An installed CompleteTransport already retains and validates the bounded
// response before the SDK sees it. Let the SDK consume that body directly,
// including http.Client timeout wrappers, rather than copying and validating
// it again. Larger transport limits cannot weaken CompleteQuery's hard bound.
func completeDataset(ctx context.Context, client *azkustodata.Client, database string, statement azkustodata.Statement, options []azkustodata.QueryOption) (query.Dataset, error) {
	switch client.HttpClient().Transport.(type) {
	case CompleteTransport, *CompleteTransport:
		return client.Query(ctx, database, statement, options...)
	}
	body, err := client.RawV2(ctx, database, statement, options)
	if err != nil {
		return nil, fmt.Errorf("request complete Kusto query: %w", err)
	}
	data, err := readCompleteBody(ctx, body, "", MaxCompleteResponseBytes)
	if err != nil {
		return nil, err
	}
	if err := validateV2(data); err != nil {
		return nil, fmt.Errorf("validate complete Kusto query: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("decode complete Kusto query: %w", err)
	}
	iterative, err := queryv2.NewIterativeDataset(ctx, io.NopCloser(bytes.NewReader(data)), queryv2.DefaultIoCapacity, queryv2.DefaultRowCapacity, queryv2.DefaultTableCapacity)
	if err != nil {
		return nil, fmt.Errorf("decode complete Kusto query: %w", err)
	}
	return iterative.ToDataset()
}

// CompleteTransport validates query and management HTTP responses before the SDK
// can expose any rows. Install it in the SDK's HTTP client, outside a transport
// that binds requests and response bodies to the owner's lifetime. Other SDK
// traffic (authentication metadata and ingestion) is left unchanged. Management
// checks only whole-document JSON integrity. Query frame and server error
// decoding remain the SDK's responsibility.
//
// Base defaults to http.DefaultTransport. MaxResponseBytes defaults to, and
// cannot exceed, MaxCompleteResponseBytes. The transport never changes
// authentication or owns the SDK client's lifetime.
type CompleteTransport struct {
	Base             http.RoundTripper
	MaxResponseBytes int64
}

func (t CompleteTransport) CloseIdleConnections() {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	if closer, ok := base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func (t CompleteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	response, err := base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	v2 := strings.HasSuffix(request.URL.Path, "/v2/rest/query")
	v1 := strings.HasSuffix(request.URL.Path, "/v1/rest/mgmt")
	if (!v2 && !v1) || response.StatusCode < 200 || response.StatusCode >= 300 {
		return response, nil
	}
	limit := t.MaxResponseBytes
	if limit <= 0 || limit > MaxCompleteResponseBytes {
		limit = MaxCompleteResponseBytes
	}
	data, err := readCompleteBody(request.Context(), response.Body, response.Header.Get("Content-Encoding"), limit)
	if err != nil {
		return nil, err
	}
	if v2 {
		err = validateV2(data)
	} else {
		err = validateV1(data)
	}
	if err != nil {
		return nil, fmt.Errorf("incomplete Kusto response: %w", err)
	}
	if err := request.Context().Err(); err != nil {
		return nil, fmt.Errorf("decode complete Kusto response: %w", err)
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	response.ContentLength = int64(len(data))
	response.Header.Del("Content-Encoding")
	response.Header.Del("Content-Length")
	response.Uncompressed = true
	return response, nil
}

func readCompleteBody(ctx context.Context, body io.ReadCloser, encoding string, limit int64) ([]byte, error) {
	// Closing unblocks custom transports that do not observe request cancellation.
	stop := context.AfterFunc(ctx, func() { _ = body.Close() })
	defer stop()
	defer body.Close()
	var decoded io.Reader = body
	switch encoding = strings.ToLower(encoding); encoding {
	case "":
	case "gzip":
		var err error
		decoded, err = gzip.NewReader(body)
		if err != nil {
			return nil, fmt.Errorf("decode Kusto gzip response: %w", err)
		}
	case "deflate":
		reader := flate.NewReader(body)
		defer reader.Close()
		decoded = reader
	default:
		return nil, fmt.Errorf("unsupported Kusto content encoding %q", encoding)
	}
	data, err := io.ReadAll(io.LimitReader(decoded, limit+1))
	if ctx.Err() != nil {
		return nil, fmt.Errorf("read complete Kusto response: %w", ctx.Err())
	}
	if err != nil {
		return nil, fmt.Errorf("read complete Kusto response: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("kusto response exceeds %d bytes", limit)
	}
	return data, nil
}

// SDK v1.2.3 can return rows without a terminal frame, ignores cancellation
// and trailing bytes, and trusts HasErrors even when error details disagree.
// Frame and typed decoding otherwise remain with the SDK.
func validateV2(data []byte) error {
	var frames []struct {
		FrameType    string
		HasErrors    bool
		Cancelled    bool
		OneApiErrors []struct {
			Error struct {
				Code    string
				Message string
			}
		}
	}
	if err := json.Unmarshal(data, &frames); err != nil {
		return fmt.Errorf("decode v2 response: %w", err)
	}
	if len(frames) == 0 || frames[len(frames)-1].FrameType != "DataSetCompletion" {
		return fmt.Errorf("missing terminal DataSetCompletion")
	}
	for _, frame := range frames {
		if len(frame.OneApiErrors) != 0 {
			// Keep the server's reason (for example a query limit), not only the fact of failure.
			details := make([]string, len(frame.OneApiErrors))
			for index, detail := range frame.OneApiErrors {
				details[index] = detail.Error.Code + ": " + detail.Error.Message
			}
			return fmt.Errorf("kusto %s reports errors: %s", frame.FrameType, strings.Join(details, "; "))
		}
		if frame.HasErrors {
			return fmt.Errorf("kusto %s reports errors", frame.FrameType)
		}
		if frame.Cancelled {
			return fmt.Errorf("kusto %s was cancelled", frame.FrameType)
		}
	}
	return nil
}

func validateV1(data []byte) error {
	if !json.Valid(data) {
		return fmt.Errorf("invalid management response JSON")
	}
	return nil
}
