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

package evidence

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const runURL = "https://prow.ci.openshift.org/view/gs/test-platform-results-public/pr-logs/pull/Azure_ARO-HCP/7892/pull-ci-Azure-ARO-HCP-main-e2e-parallel/2117218708026822656"

func TestArtifactBasePreservesBucketAndPath(t *testing.T) {
	base, err := ArtifactBase(runURL + "/")
	if err != nil {
		t.Fatalf("ArtifactBase() error = %v", err)
	}
	want := "https://storage.googleapis.com/test-platform-results-public/pr-logs/pull/Azure_ARO-HCP/7892/pull-ci-Azure-ARO-HCP-main-e2e-parallel/2117218708026822656/"
	if base != want {
		t.Fatalf("ArtifactBase() = %q, want %q", base, want)
	}
	for _, invalid := range []string{
		"https://prow.ci.openshift.org/view/gs/../etc/passwd",
		"https://example.com/view/gs/bucket/path/1",
		"https://prow.ci.openshift.org/view/gs/BUCKET/pr-logs/pull/x/1/job/1",
		"http://prow.ci.openshift.org/view/gs/bucket/pr-logs/pull/x/1/job/1",
	} {
		if _, err := ArtifactBase(invalid); err == nil {
			t.Fatalf("ArtifactBase(%q) must fail", invalid)
		}
	}
}

// rewriteTransport sends storage.googleapis.com requests to a local server.
type rewriteTransport struct{ target *url.URL }

func (t rewriteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request.URL.Scheme = t.target.Scheme
	request.URL.Host = t.target.Host
	return http.DefaultTransport.RoundTrip(request)
}

func TestFetchRequiresBothArtifactsAndPublishesManifestLast(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasSuffix(request.URL.Path, "finished.json"):
			fmt.Fprint(writer, `{"result":"FAILURE"}`)
		case strings.HasSuffix(request.URL.Path, "build-log.txt"):
			fmt.Fprint(writer, "error: ManagedNode infra config cache not synchronized\n")
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	fetcher := NewHTTPFetcher()
	fetcher.Client = &http.Client{Transport: rewriteTransport{target: target}}

	directory := t.TempDir()
	manifest, err := fetcher.Fetch(context.Background(), runURL, directory)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if !manifest.Complete || manifest.Result != "FAILURE" {
		t.Fatalf("unexpected manifest %+v", manifest)
	}
	for _, name := range []string{"finished.json", "build-log.txt", "manifest.json"} {
		if _, err := os.Stat(filepath.Join(directory, name)); err != nil {
			t.Fatalf("expected %s: %v", name, err)
		}
	}
}

func TestFetchRefusesExistingOutputAndMissingArtifacts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "finished.json") {
			fmt.Fprint(writer, `{"result":"SUCCESS"}`)
			return
		}
		http.NotFound(writer, request)
	}))
	defer server.Close()
	target, _ := url.Parse(server.URL)
	fetcher := NewHTTPFetcher()
	fetcher.Client = &http.Client{Transport: rewriteTransport{target: target}}

	directory := t.TempDir()
	if _, err := fetcher.Fetch(context.Background(), runURL, directory); err == nil {
		t.Fatal("expected a missing build-log error")
	}
	if _, err := os.Stat(filepath.Join(directory, "manifest.json")); err == nil {
		t.Fatal("a failed fetch must not publish a manifest")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read directory: %v", err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".fetch-prow-") {
			t.Fatalf("failed fetch left %s behind", entry.Name())
		}
	}

	success := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "finished.json") {
			fmt.Fprint(writer, `{"result":"SUCCESS"}`)
			return
		}
		fmt.Fprint(writer, "ok\n")
	}))
	defer success.Close()
	successTarget, _ := url.Parse(success.URL)
	fetcher.Client = &http.Client{Transport: rewriteTransport{target: successTarget}}
	first := t.TempDir()
	if _, err := fetcher.Fetch(context.Background(), runURL, first); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if _, err := fetcher.Fetch(context.Background(), runURL, first); err == nil {
		t.Fatal("expected a refusal to overwrite existing artifacts")
	}
}

func TestFetchRejectsBlankResultAndOversizedBody(t *testing.T) {
	blank := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "finished.json") {
			fmt.Fprint(writer, `{"result":"   "}`)
			return
		}
		fmt.Fprint(writer, "ok\n")
	}))
	defer blank.Close()
	blankTarget, _ := url.Parse(blank.URL)
	fetcher := NewHTTPFetcher()
	fetcher.Client = &http.Client{Transport: rewriteTransport{target: blankTarget}}
	if _, err := fetcher.Fetch(context.Background(), runURL, t.TempDir()); err == nil {
		t.Fatal("a blank finished.json result must be refused")
	}

	large := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "finished.json") {
			fmt.Fprint(writer, `{"result":"FAILURE"}`)
			return
		}
		fmt.Fprint(writer, strings.Repeat("x", 4096))
	}))
	defer large.Close()
	largeTarget, _ := url.Parse(large.URL)
	fetcher = NewHTTPFetcher()
	fetcher.Client = &http.Client{Transport: rewriteTransport{target: largeTarget}}
	fetcher.MaxBytes = 1024
	if _, err := fetcher.Fetch(context.Background(), runURL, t.TempDir()); err == nil {
		t.Fatal("an oversized artifact must be refused")
	}
}

func TestFetchRejectsPartialBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "finished.json") {
			writer.Header().Set("Content-Length", "100")
			fmt.Fprint(writer, `{"result":"FAILURE"}`)
			return
		}
		fmt.Fprint(writer, "ok\n")
	}))
	defer server.Close()
	target, _ := url.Parse(server.URL)
	fetcher := NewHTTPFetcher()
	fetcher.Client = &http.Client{Transport: rewriteTransport{target: target}}
	if _, err := fetcher.Fetch(context.Background(), runURL, t.TempDir()); err == nil {
		t.Fatal("a body shorter than Content-Length must be refused")
	}
}
