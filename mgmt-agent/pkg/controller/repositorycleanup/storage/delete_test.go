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

package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
)

type fakeCredential struct{}

func (fakeCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type fakeTransport func(*http.Request) (*http.Response, error)

func (f fakeTransport) Do(req *http.Request) (*http.Response, error) { return f(req) }

type exchange struct {
	method, marker, maxResults, name, version, snapshot, deleteSnapshots string
	status                                                               int
	code, body                                                           string
}

func listing(blobs, marker string) string {
	return `<EnumerationResults><Blobs>` + blobs + `</Blobs><NextMarker>` + marker + `</NextMarker></EnumerationResults>`
}

func listedBlob(name, extra string) string {
	return `<Blob><Name>` + name + `</Name><Properties/><Snapshot/>` + extra + `</Blob>`
}

func scriptedDeleter(t *testing.T, script []exchange) (func(context.Context, Target) error, *int) {
	t.Helper()
	calls := 0
	transport := fakeTransport(func(req *http.Request) (*http.Response, error) {
		if calls >= len(script) {
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
		}
		want := script[calls]
		calls++
		if req.URL.Scheme != "https" || req.URL.Host != "account123.blob.core.windows.net" || req.Method != want.method {
			t.Fatalf("unexpected endpoint/method: %s %s", req.Method, req.URL)
		}
		query := req.URL.Query()
		// The SDK writes lower-case header keys directly, before net/http's
		// wire canonicalization. Normalize the fake transport's view for Get.
		headers := make(http.Header)
		for key, values := range req.Header {
			headers[http.CanonicalHeaderKey(key)] = values
		}
		if req.Method == http.MethodGet {
			if req.URL.Path != "/backups" || query.Get("comp") != "list" || query.Get("restype") != "container" || query.Get("prefix") != testTarget().Prefix || query.Get("include") != "snapshots,versions" || query.Get("marker") != want.marker || query.Get("maxresults") != want.maxResults {
				t.Fatalf("unexpected listing query: %s, want %+v", req.URL, want)
			}
		} else {
			if req.URL.Path != "/backups/"+want.name || query.Get("versionid") != want.version || query.Get("snapshot") != want.snapshot || headers.Get("x-ms-delete-snapshots") != want.deleteSnapshots {
				t.Fatalf("unexpected deletion: %s headers=%v, want %+v", req.URL, req.Header, want)
			}
			if headers.Get("x-ms-delete-type") != "" {
				t.Fatal("must not permanently purge soft-deleted data")
			}
		}
		status := want.status
		if status == 0 {
			status = http.StatusOK
			if req.Method == http.MethodDelete {
				status = http.StatusAccepted
			}
		}
		headers = http.Header{"Content-Type": []string{"application/xml"}}
		body := want.body
		if want.code != "" {
			headers.Set("x-ms-error-code", want.code)
			body = `<Error><Code>` + want.code + `</Code></Error>`
		}
		return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})
	return newDeleter(fakeCredential{}, &azblob.ClientOptions{ClientOptions: azcore.ClientOptions{
		Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1},
	}}), &calls
}

func TestDeletePaginationVersionsAndSnapshots(t *testing.T) {
	prefix := testTarget().Prefix
	script := []exchange{
		{method: "GET", maxResults: "128", body: listing(listedBlob(prefix+"a", ""), "page2")},
		{method: "DELETE", name: prefix + "a", deleteSnapshots: "include"},
		{method: "GET", marker: "page2", maxResults: "128", body: listing(listedBlob(prefix+"b", `<VersionId>old</VersionId><IsCurrentVersion>false</IsCurrentVersion>`)+listedBlob(prefix+"b", `<VersionId>current</VersionId><IsCurrentVersion>true</IsCurrentVersion>`)+listedBlob(prefix+"a", `<Snapshot>snapshot1</Snapshot>`), "")},
		{method: "DELETE", name: prefix + "b", version: "old"},
		{method: "DELETE", name: prefix + "b", deleteSnapshots: "include"},
		{method: "DELETE", name: prefix + "b", version: "current"},
		{method: "DELETE", name: prefix + "a", snapshot: "snapshot1", status: 404, code: "BlobNotFound"},
		{method: "GET", maxResults: "1", body: listing("", "verify2")},
		{method: "GET", maxResults: "1", marker: "verify2", body: listing("", "")},
	}
	delete, calls := scriptedDeleter(t, script)
	if err := delete(t.Context(), testTarget()); err != nil {
		t.Fatal(err)
	}
	if *calls != len(script) {
		t.Fatalf("got %d calls, want %d", *calls, len(script))
	}
}

func TestDeleteErrorsAndVerification(t *testing.T) {
	name := testTarget().Prefix + "blob"
	first := exchange{method: "GET", maxResults: "128", body: listing(listedBlob(name, ""), "")}
	remove := exchange{method: "DELETE", name: name, deleteSnapshots: "include"}
	empty := exchange{method: "GET", maxResults: "1", body: listing("", "")}
	for _, tc := range []struct {
		name    string
		script  []exchange
		wantErr bool
	}{
		{"empty verified", []exchange{{method: "GET", maxResults: "128", body: listing("", "")}, empty}, false},
		{"container absent", []exchange{{method: "GET", maxResults: "128", status: 404, code: "ContainerNotFound"}}, false},
		{"account absent", []exchange{{method: "GET", maxResults: "128", status: 404, code: "AccountNotFound"}}, true},
		{"generic 404", []exchange{{method: "GET", maxResults: "128", status: 404}}, true},
		{"list blob absent", []exchange{{method: "GET", maxResults: "128", status: 404, code: "BlobNotFound"}}, true},
		{"forbidden list", []exchange{{method: "GET", maxResults: "128", status: 403, code: "AuthorizationPermissionMismatch"}}, true},
		{"forbidden misleading code", []exchange{{method: "GET", maxResults: "128", status: 403, code: "ContainerNotFound"}}, true},
		{"forbidden delete", []exchange{first, {method: "DELETE", name: name, deleteSnapshots: "include", status: 403, code: "AuthorizationFailure"}}, true},
		{"forbidden misleading delete code", []exchange{first, {method: "DELETE", name: name, deleteSnapshots: "include", status: 403, code: "BlobNotFound"}}, true},
		{"immutable", []exchange{first, {method: "DELETE", name: name, deleteSnapshots: "include", status: 409, code: "BlobImmutableDueToPolicy"}}, true},
		{"missing blob verified", []exchange{first, {method: "DELETE", name: name, deleteSnapshots: "include", status: 404, code: "BlobNotFound"}, empty}, false},
		{"container vanished verified", []exchange{first, {method: "DELETE", name: name, deleteSnapshots: "include", status: 404, code: "ContainerNotFound"}, {method: "GET", maxResults: "1", status: 404, code: "ContainerNotFound"}}, false},
		{"generic delete 404", []exchange{first, {method: "DELETE", name: name, deleteSnapshots: "include", status: 404}}, true},
		{"verification forbidden", []exchange{first, remove, {method: "GET", maxResults: "1", status: 403, code: "AuthorizationFailure"}}, true},
		{"live remaining", []exchange{first, remove, {method: "GET", maxResults: "1", body: listing(listedBlob(name, ""), "")}}, true},
		{"version remaining", []exchange{first, remove, {method: "GET", maxResults: "1", body: listing(listedBlob(name, "<VersionId>v1</VersionId>"), "")}}, true},
		{"snapshot remaining", []exchange{first, remove, {method: "GET", maxResults: "1", body: listing(listedBlob(name, "<Snapshot>s1</Snapshot>"), "")}}, true},
		{"soft deleted ignored", []exchange{{method: "GET", maxResults: "128", body: listing(listedBlob(name, "<Deleted>true</Deleted>"), "")}, {method: "GET", maxResults: "1", body: listing(listedBlob(name, "<Deleted>true</Deleted>"), "")}}, false},
		{"exact boundary", []exchange{{method: "GET", maxResults: "128", body: listing(listedBlob(strings.TrimSuffix(testTarget().Prefix, "/")+"-sibling/blob", ""), "")}}, true},
		{"malformed response", []exchange{{method: "GET", maxResults: "128", body: "not XML"}}, true},
		{"missing segment", []exchange{{method: "GET", maxResults: "128", body: "<EnumerationResults/>"}}, true},
		{"missing name", []exchange{{method: "GET", maxResults: "128", body: listing("<Blob><Properties/></Blob>", "")}}, true},
		{"later page error", []exchange{{method: "GET", maxResults: "128", body: listing(listedBlob(name, ""), "next")}, remove, {method: "GET", maxResults: "128", marker: "next", status: 500, code: "InternalError"}}, true},
		{"verification later page nonempty", []exchange{first, remove, {method: "GET", maxResults: "1", body: listing("", "next")}, {method: "GET", maxResults: "1", marker: "next", body: listing(listedBlob(name, ""), "")}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			delete, calls := scriptedDeleter(t, tc.script)
			if err := delete(t.Context(), testTarget()); (err != nil) != tc.wantErr {
				t.Fatalf("delete() = %v, wantErr=%t", err, tc.wantErr)
			}
			if *calls != len(tc.script) {
				t.Fatalf("got %d requests, want %d", *calls, len(tc.script))
			}
		})
	}
}

func TestDeleteRetryAfterPartialFailure(t *testing.T) {
	name := testTarget().Prefix + "versioned"
	other := testTarget().Prefix + "other"
	firstPage := exchange{method: "GET", maxResults: "128", body: listing(listedBlob(name, "<VersionId>current</VersionId><IsCurrentVersion>true</IsCurrentVersion>")+listedBlob(other, ""), "")}
	script := []exchange{
		firstPage,
		{method: "DELETE", name: name, deleteSnapshots: "include", status: 403, code: "AuthorizationFailure"},
		firstPage,
		{method: "DELETE", name: name, deleteSnapshots: "include", status: 404, code: "BlobNotFound"},
		{method: "DELETE", name: name, version: "current", status: 404, code: "BlobNotFound"},
		{method: "DELETE", name: other, deleteSnapshots: "include"},
		{method: "GET", maxResults: "1", body: listing("", "")},
	}
	delete, calls := scriptedDeleter(t, script)
	if err := delete(t.Context(), testTarget()); err == nil {
		t.Fatal("partial failure must not report success")
	}
	if err := delete(t.Context(), testTarget()); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	if *calls != len(script) {
		t.Fatalf("got %d requests, want %d", *calls, len(script))
	}
}

func TestDeleteBudgets(t *testing.T) {
	t.Run("deletes", func(t *testing.T) {
		var script []exchange
		for page := 0; page < 3; page++ {
			var blobs strings.Builder
			for i := 0; i < pageSize; i++ {
				blobs.WriteString(listedBlob(fmt.Sprintf("%s%d-%d", testTarget().Prefix, page, i), ""))
			}
			marker := ""
			if page > 0 {
				marker = fmt.Sprint(page)
			}
			script = append(script, exchange{method: "GET", marker: marker, maxResults: "128", body: listing(blobs.String(), fmt.Sprint(page+1))})
			if page < 2 {
				for i := 0; i < pageSize; i++ {
					script = append(script, exchange{method: "DELETE", name: fmt.Sprintf("%s%d-%d", testTarget().Prefix, page, i), deleteSnapshots: "include"})
				}
			}
		}
		delete, calls := scriptedDeleter(t, script)
		if err := delete(t.Context(), testTarget()); err == nil || !strings.Contains(err.Error(), "deletion budget") {
			t.Fatalf("expected bounded partial cleanup, got %v", err)
		}
		if *calls != maxDeletes+3 {
			t.Fatalf("unexpected number of requests: %d", *calls)
		}
	})
	t.Run("empty pages", func(t *testing.T) {
		var script []exchange
		for i := 0; i < maxPages; i++ {
			marker := ""
			if i > 0 {
				marker = fmt.Sprint(i)
			}
			script = append(script, exchange{method: "GET", marker: marker, maxResults: "128", body: listing("", fmt.Sprint(i+1))})
		}
		delete, calls := scriptedDeleter(t, script)
		if err := delete(t.Context(), testTarget()); err == nil || !strings.Contains(err.Error(), "page budget") {
			t.Fatalf("expected page budget error, got %v", err)
		}
		if *calls != maxPages {
			t.Fatalf("unexpected number of requests: %d", *calls)
		}
	})
}

func TestDeleteRejectsBeforeTransport(t *testing.T) {
	delete, calls := scriptedDeleter(t, nil)
	target := testTarget()
	target.AccountURL = "https://attacker.example"
	if err := delete(t.Context(), target); err == nil {
		t.Fatal("invalid target accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := delete(ctx, testTarget()); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if *calls != 0 {
		t.Fatal("unexpected transport call")
	}
	if err := NewDeleter(nil)(t.Context(), testTarget()); err == nil {
		t.Fatal("nil credential accepted")
	}
}
