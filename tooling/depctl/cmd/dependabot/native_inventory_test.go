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
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	gh "github.com/google/go-github/v69/github"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/dependabot"
	"github.com/Azure/ARO-HCP/tooling/depctl/pkg/depctl"
)

// TestNativeInventoryCredentialBoundary checks that invalid arguments fail before credential resolution and that CI credentials cannot authorize general reads.
func TestNativeInventoryCredentialBoundary(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("CI_TOKEN", "ci-only")
	t.Setenv("PATH", t.TempDir())
	for _, tc := range []struct {
		name    string
		args    []string
		invalid bool
	}{
		{name: "missing repository", invalid: true},
		{name: "invalid repository", args: []string{"--repository", "Azure/ARO-HCP/extra"}, invalid: true},
		{name: "unexpected argument", args: []string{"--repository", "Azure/ARO-HCP", "extra"}, invalid: true},
		{name: "missing read credential", args: []string{"--repository", "Azure/ARO-HCP"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output, err := executeNativeCommand(t, append([]string{"native", "inventory"}, tc.args...))
			if output != "" || err == nil {
				t.Fatalf("expected failure without inventory JSON: output=%q error=%v", output, err)
			}
			var exit *depctl.ExitError
			if tc.invalid {
				if !errors.As(err, &exit) || exit.Code != depctl.ExitInvalid {
					t.Fatalf("expected invalid input before credential resolution: %v", err)
				}
				return
			}
			if !strings.Contains(err.Error(), "requires a GitHub read credential") {
				t.Fatalf("expected missing read credential: %v", err)
			}
		})
	}
}

// TestNativeInventoryJSON verifies the stdout object, including empty inventory arrays.
func TestNativeInventoryJSON(t *testing.T) {
	const sha = "0123456789012345678901234567890123456789"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("inventory attempted a write: %s %s", r.Method, r.URL)
			http.Error(w, "read-only", http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/repos/Azure/ARO-HCP/git/ref/heads/main":
			_, _ = io.WriteString(w, `{"ref":"refs/heads/main","object":{"type":"commit","sha":"`+sha+`"}}`)
		case "/repos/Azure/ARO-HCP/issues":
			_, _ = io.WriteString(w, `[]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	api := gh.NewClient(server.Client())
	baseURL, err := url.Parse(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	api.BaseURL = baseURL
	publisher, err := dependabot.NewNativePublisher("Azure/ARO-HCP", dependabot.WithNativeInventoryClients(api, api))
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	options := nativeInventoryOptions{publisher: publisher, output: &output}
	if err := options.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		Repository   string            `json:"repository"`
		BaseSHA      string            `json:"base_sha"`
		Groups       []json.RawMessage `json:"groups"`
		Replacements []json.RawMessage `json:"replacements"`
	}
	if err := json.Unmarshal(output.Bytes(), &inventory); err != nil {
		t.Fatal(err)
	}
	if inventory.Repository != "Azure/ARO-HCP" || inventory.BaseSHA != sha || inventory.Groups == nil ||
		len(inventory.Groups) != 0 || inventory.Replacements == nil || len(inventory.Replacements) != 0 {
		t.Fatalf("unexpected empty inventory: %s", &output)
	}
}
