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

package databasetesting

import (
	"encoding/json"
	"net/http"
	"strconv"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

// mockChangeFeed is an in-memory log of mutations to the Resources
// container. Every successful StoreDocument call (Create or Replace)
// appends a snapshot of the stored document; reads return everything
// past the consumer's continuation position. Hard deletes are
// intentionally NOT recorded — that mirrors "latest version" change
// feed mode in production Cosmos DB. Soft deletes (which are modeled
// as Replace operations) ARE recorded via StoreDocument.
type mockChangeFeed struct {
	mu     sync.Mutex
	events []json.RawMessage
}

// record appends a copy of data to the log. The copy isolates the
// feed's history from any caller-side mutation of the underlying
// byte slice.
func (m *mockChangeFeed) record(data json.RawMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make(json.RawMessage, len(data))
	copy(cp, data)
	m.events = append(m.events, cp)
}

// read returns events starting at the position encoded in continuation
// (0 if blank/unparseable) and the next position token to hand back
// to the consumer. hasNew tells the caller whether to report 200 OK
// or 304 Not Modified upstream.
func (m *mockChangeFeed) read(continuation string) (docs []json.RawMessage, nextToken string, hasNew bool) {
	start := decodeMockChangeFeedPosition(continuation)

	m.mu.Lock()
	defer m.mu.Unlock()

	if start >= len(m.events) {
		return nil, strconv.Itoa(start), false
	}

	out := make([]json.RawMessage, len(m.events)-start)
	copy(out, m.events[start:])
	return out, strconv.Itoa(len(m.events)), true
}

// mockChangeFeedFeedRange is the single feed range advertised by the
// mock. Real Cosmos may report many ranges for one container; the
// in-memory mock has no partition-level parallelism to model so one
// range is sufficient.
var mockChangeFeedFeedRange = azcosmos.FeedRange{MinInclusive: "", MaxExclusive: "FF"}

// decodeMockChangeFeedPosition extracts the integer position the mock
// embeds in its continuation tokens. The consumer of a
// ChangeFeedResponse calls GetCompositeContinuationToken() which wraps
// the response's ETag inside a composite JSON envelope:
//
//	{"version":1,"resourceId":"...","continuation":[
//	    {"minInclusive":"","maxExclusive":"FF","continuationToken":"<our ETag>"}]}
//
// The next request hands that envelope back as
// options.Continuation. We unwrap it and parse our position out
// again. We also accept a bare integer to keep the function robust
// against direct uses of the mock that pre-date the wrapping.
func decodeMockChangeFeedPosition(s string) int {
	if s == "" {
		return 0
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	var composite struct {
		Continuation []struct {
			ContinuationToken string `json:"continuationToken"`
		} `json:"continuation"`
	}
	if err := json.Unmarshal([]byte(s), &composite); err != nil {
		return 0
	}
	if len(composite.Continuation) == 0 {
		return 0
	}
	n, _ := strconv.Atoi(composite.Continuation[0].ContinuationToken)
	return n
}

// buildMockChangeFeedResponse assembles an azcosmos.ChangeFeedResponse
// the production consumer can drive end to end — including a follow-up
// call to GetCompositeContinuationToken — by populating ResourceID,
// FeedRange, and the response ETag (which the SDK threads into the
// composite token).
func buildMockChangeFeedResponse(docs []json.RawMessage, nextToken string, hasNew bool) azcosmos.ChangeFeedResponse {
	status := http.StatusNotModified
	if hasNew {
		status = http.StatusOK
	}
	fr := mockChangeFeedFeedRange
	return azcosmos.ChangeFeedResponse{
		ResourceID: "mock-resources-container",
		Documents:  docs,
		FeedRange:  &fr,
		Response: azcosmos.Response{
			RawResponse: &http.Response{StatusCode: status},
			ETag:        azcore.ETag(nextToken),
		},
	}
}
