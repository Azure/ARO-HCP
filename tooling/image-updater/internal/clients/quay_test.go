// Copyright 2025 Microsoft Corporation
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

package clients

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-logr/logr"
)

func TestQuayClientGetAllTagsRejectsInvalidCandidateTimestamp(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tags":[{"name":"unrelated","manifest_digest":"sha256:unrelated","last_modified":"invalid"},{"name":"release-latest","manifest_digest":"sha256:latest","last_modified":"invalid"}],"page":1,"has_additional":false}`))
	}))
	defer server.Close()

	client := NewQuayClient(false)
	client.baseURL = server.URL
	ctx := logr.NewContext(context.Background(), logr.Discard())

	_, err := client.getAllTags(ctx, "test/repository", `^release-`)
	if err == nil || !strings.Contains(err.Error(), "release-latest") || !strings.Contains(err.Error(), "failed to parse timestamp") {
		t.Fatalf("getAllTags() error = %v, want matching candidate timestamp error", err)
	}
}

func TestQuayClientGetAllTagsRejectsEpochCandidateTimestamp(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tags":[{"name":"release-latest","manifest_digest":"sha256:latest","last_modified":"Thu, 01 Jan 1970 00:00:00 +0000"}],"page":1,"has_additional":false}`))
	}))
	defer server.Close()

	client := NewQuayClient(false)
	client.baseURL = server.URL
	ctx := logr.NewContext(context.Background(), logr.Discard())

	_, err := client.getAllTags(ctx, "test/repository", `^release-`)
	if err == nil || !strings.Contains(err.Error(), "release-latest") || !strings.Contains(err.Error(), "no creation timestamp") {
		t.Fatalf("getAllTags() error = %v, want missing candidate timestamp error", err)
	}
}

func TestQuayClientGetAllTagsViaRegistryAPIFollowsPagination(t *testing.T) {
	requests := 0
	client := NewQuayClient(false)
	client.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		body := `{"name":"test/repository","tags":["first"]}`
		header := make(http.Header)
		if req.URL.Query().Get("last") == "" {
			header.Set("Link", `</v2/test/repository/tags/list?n=1&last=first>; rel="next"`)
		} else {
			body = `{"name":"test/repository","tags":["second"]}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})}
	ctx := logr.NewContext(context.Background(), logr.Discard())

	tags, err := client.getAllTagsViaRegistryAPI(ctx, "test/repository")
	if err != nil {
		t.Fatalf("getAllTagsViaRegistryAPI() unexpected error = %v", err)
	}
	if requests != 2 {
		t.Fatalf("registry requests = %d, want 2 pages", requests)
	}
	if len(tags) != 2 || tags[0].Name != "first" || tags[1].Name != "second" {
		t.Fatalf("getAllTagsViaRegistryAPI() tags = %#v, want both pages", tags)
	}
}
