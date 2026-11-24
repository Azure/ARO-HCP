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

package dependabot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// TestNativeVerifyOfficialRelease requires exact, complete repository tag evidence.
func TestNativeVerifyOfficialRelease(t *testing.T) {
	sha := strings.Repeat("a", 40)
	other := strings.Repeat("b", 40)
	tag := `{"name":"v2","commit":{"sha":"` + sha + `","url":"https://api.github.com/repos/actions/checkout/commits/` + sha + `"},"node_id":"tag-ref","zipball_url":"https://api.github.com/repos/actions/checkout/zipball/refs/tags/v2","tarball_url":"https://api.github.com/repos/actions/checkout/tarball/refs/tags/v2"}`
	firstPage := make([]string, 100)
	for index := range firstPage {
		firstPage[index] = fmt.Sprintf(`{"name":"older-%d","commit":{"sha":%q}}`, index, other)
	}
	fullPage := "[" + strings.Join(firstPage, ",") + "]"
	cases := []struct {
		name       string
		repository string
		version    string
		sha        string
		pages      []string
		wantError  string
	}{
		{name: "lightweight tag", version: "v2", pages: []string{`[{"name":"v2","commit":{"sha":"` + sha + `"}}]`}},
		{name: "annotated tag peeled commit", version: "v2", pages: []string{"[" + tag + "]"}},
		{name: "explicit second page", version: "v2", pages: []string{fullPage, "[" + tag + "]"}},
		{name: "raw SHA with official tag", version: sha, pages: []string{"[" + tag + "]"}},
		{name: "raw SHA without official tag", version: sha, pages: []string{`[{"name":"v1","commit":{"sha":"` + other + `"}}]`}, wantError: "do not prove"},
		{name: "raw SHA cannot prove itself", version: sha, pages: []string{"[]"}, wantError: "do not prove"},
		{name: "different raw SHA", version: other, wantError: "does not match"},
		{name: "stale tag", version: "v2", pages: []string{`[{"name":"v2","commit":{"sha":"` + other + `"}}]`}, wantError: "does not match"},
		{name: "uppercase raw SHA", version: strings.ToUpper(sha), wantError: "does not match"},
		{name: "wrong tag", version: "v3", pages: []string{"[" + tag + "]"}, wantError: "do not prove"},
		{name: "filtered empty", version: "v2", pages: []string{"[]"}, wantError: "do not prove"},
		{name: "missing later tag", version: "v2", pages: []string{fullPage, "[]"}, wantError: "do not prove"},
		{name: "filtered object", version: "v2", pages: []string{"{}"}, wantError: "malformed"},
		{name: "unwrapped single tag", version: "v2", pages: []string{tag}, wantError: "malformed"},
		{name: "null list", version: "v2", pages: []string{"null"}, wantError: "malformed"},
		{name: "missing commit", version: "v2", pages: []string{`[{"name":"v2"}]`}, wantError: "malformed"},
		{name: "null commit", version: "v2", pages: []string{`[{"name":"v2","commit":null}]`}, wantError: "malformed"},
		{name: "missing name", version: "v2", pages: []string{`[{"commit":{"sha":"` + sha + `"}}]`}, wantError: "malformed"},
		{name: "empty name", version: "v2", pages: []string{`[{"name":"","commit":{"sha":"` + sha + `"}}]`}, wantError: "malformed"},
		{name: "non-string name", version: "v2", pages: []string{`[{"name":2,"commit":{"sha":"` + sha + `"}}]`}, wantError: "malformed"},
		{name: "short SHA", version: "v2", pages: []string{`[{"name":"v2","commit":{"sha":"aaaa"}}]`}, wantError: "malformed"},
		{name: "uppercase SHA", version: "v2", pages: []string{`[{"name":"v2","commit":{"sha":"` + strings.ToUpper(sha) + `"}}]`}, wantError: "malformed"},
		{name: "non-string SHA", version: "v2", pages: []string{`[{"name":"v2","commit":{"sha":2}}]`}, wantError: "malformed"},
		{name: "duplicate tag", version: "v2", pages: []string{"[" + tag + "," + tag + "]"}, wantError: "duplicate"},
		{name: "duplicate across pages", version: "v2", pages: []string{fullPage, `[{"name":"older-0","commit":{"sha":"` + sha + `"}}]`}, wantError: "duplicate"},
		{name: "duplicate JSON key", version: "v2", pages: []string{`[{"name":"v2","name":"v2","commit":{"sha":"` + sha + `"}}]`}, wantError: "duplicate"},
		{name: "trailing JSON", version: "v2", pages: []string{"[" + tag + "] []"}, wantError: "trailing"},
		{name: "malformed after match", version: "v2", pages: []string{"[" + tag + `,{"name":"broken"}]`}, wantError: "malformed"},
		{name: "oversized page", version: "v2", pages: []string{strings.TrimSuffix(fullPage, "]") + "," + tag + "]"}, wantError: "malformed"},
		{name: "invalid repository", repository: "actions/checkout/extra", version: "v2", wantError: "identity"},
		{name: "query injection", repository: "actions/checkout?extra=yes", version: "v2", wantError: "identity"},
		{name: "invalid expected SHA", version: "v2", sha: "partial", wantError: "identity"},
		{name: "empty version", wantError: "identity"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			repository := test.repository
			if repository == "" {
				repository = "actions/checkout"
			}
			expected := test.sha
			if expected == "" {
				expected = sha
			}
			read := func(_ context.Context, endpoint string, pages, ci bool) (json.RawMessage, error) {
				if pages || ci {
					t.Fatal("requested automatic pagination or CI credentials")
				}
				for index, raw := range test.pages {
					if endpoint == "repos/actions/checkout/tags?per_page=100&page="+strconv.Itoa(index+1) {
						return json.RawMessage(raw), nil
					}
				}
				t.Fatalf("unexpected official read %q", endpoint)
				return nil, nil
			}
			err := NativeVerifyOfficialRelease(context.Background(), read, repository, test.version, expected)
			if test.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want %q", err, test.wantError)
			}
		})
	}
}

// TestNativeVerifyOfficialReleaseBoundsAndReadErrors checks finite evidence acquisition and error propagation.
func TestNativeVerifyOfficialReleaseBoundsAndReadErrors(t *testing.T) {
	sha := strings.Repeat("a", 40)
	readError := errors.New("proxy denied tag metadata")
	cases := []struct {
		name string
		read NativeReadFunc
		want string
	}{
		{name: "missing reader", want: "identity"},
		{name: "read error", read: func(context.Context, string, bool, bool) (json.RawMessage, error) {
			return nil, readError
		}, want: "reading official tags"},
		{name: "bounded pages", read: func(_ context.Context, endpoint string, pages, ci bool) (json.RawMessage, error) {
			page, err := strconv.Atoi(strings.TrimPrefix(endpoint, "repos/actions/checkout/tags?per_page=100&page="))
			if err != nil || page < 1 || page > 100 || pages || ci {
				t.Fatalf("unsupported read %q", endpoint)
			}
			tags := make([]string, 100)
			for index := range tags {
				tags[index] = fmt.Sprintf(`{"name":"page-%d-tag-%d","commit":{"sha":%q}}`, page, index, sha)
			}
			return json.RawMessage("[" + strings.Join(tags, ",") + "]"), nil
		}, want: "exceeds 100 pages"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := NativeVerifyOfficialRelease(context.Background(), test.read, "actions/checkout", "missing", sha)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if test.name == "read error" && !errors.Is(err, readError) {
				t.Fatalf("lost wrapped read error: %v", err)
			}
		})
	}
}

// TestNativeReadOfficialTagBoundary rejects endpoints and credentials outside explicit tag pages.
func TestNativeReadOfficialTagBoundary(t *testing.T) {
	cases := []struct {
		endpoint string
		pages    bool
		ci       bool
		allowed  bool
	}{
		{endpoint: "repos/actions/checkout/tags?per_page=100&page=1", allowed: true},
		{endpoint: "repos/actions/checkout/tags?per_page=100&page=100", allowed: true},
		{endpoint: "repos/actions/checkout/tags?per_page=100&page=0"},
		{endpoint: "repos/actions/checkout/tags?per_page=100&page=101"},
		{endpoint: "repos/actions/checkout/tags?per_page=100&page=01"},
		{endpoint: "repos/actions/checkout/tags?per_page=100&page=1&extra=true"},
		{endpoint: "repos/actions/checkout/commits/v2"},
		{endpoint: "repos/actions/checkout/git/tags/" + strings.Repeat("a", 40)},
		{endpoint: "repos/actions/checkout/tags?per_page=100&page=1", pages: true},
		{endpoint: "repos/actions/checkout/tags?per_page=100&page=1", ci: true},
	}
	for _, test := range cases {
		t.Run(fmt.Sprintf("%s/pages=%t/ci=%t", test.endpoint, test.pages, test.ci), func(t *testing.T) {
			read := func(context.Context, string, bool, bool) (json.RawMessage, error) {
				if !test.allowed {
					t.Fatal("unsupported request reached reader")
				}
				return json.RawMessage("[]"), nil
			}
			_, err := nativeRead(context.Background(), read, test.endpoint, test.pages, test.ci)
			if (err == nil) != test.allowed {
				t.Fatalf("error = %v, allowed = %t", err, test.allowed)
			}
		})
	}
}
