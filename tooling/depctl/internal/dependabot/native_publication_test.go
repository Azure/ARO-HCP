// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0.

package dependabot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gh "github.com/google/go-github/v69/github"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/repository"
	automation "github.com/Azure/ARO-HCP/tooling/github"
)

// Native publication request fixtures represent an actual editable regular text file.
func nativePublicationTestRequest() nativePublicationRequest {
	return nativePublicationRequest{SourcePRs: []nativePublicationSource{{Number: 7, HeadSHA: strings.Repeat("a", 40)}}, ExpectedBaseSHA: strings.Repeat("b", 40), Patch: "diff --git a/go.mod b/go.mod\n--- a/go.mod\n+++ b/go.mod\n@@ -1 +1 @@\n-old\n+new\n", Title: "chore(deps): upgrade dependency", Body: "Validated @package consumer update."}
}

// TestNativePublicationDigestTransport exercises the public offline consumer including hostile filesystem objects.
func TestNativePublicationDigestTransport(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*testing.T, string, *[]byte, *map[string]any)
		absent  bool
		invalid bool
	}{
		{name: "valid digest"},
		{name: "nested safe envelope", mutate: func(t *testing.T, directory string, payload *[]byte, envelope *map[string]any) {
			item := (*envelope)["items"].([]any)[0].(map[string]any)
			(*envelope)["items"] = []any{map[string]any{"type": "native_replacement", "data": map[string]any{"request_sha256": item["request_sha256"]}}}
		}},
		{name: "no native request", absent: true, mutate: func(t *testing.T, directory string, payload *[]byte, envelope *map[string]any) {
			(*envelope)["items"] = []any{map[string]any{"type": "other"}}
		}},
		{name: "digest mismatch", invalid: true, mutate: func(t *testing.T, directory string, payload *[]byte, envelope *map[string]any) {
			*payload = append(*payload, ' ')
		}},
		{name: "duplicate request field", invalid: true, mutate: func(t *testing.T, directory string, payload *[]byte, envelope *map[string]any) {
			*payload = []byte(strings.Replace(string(*payload), `"body":`, `"body":"duplicate","body":`, 1))
			nativePublicationTestDigest(*payload, *envelope)
		}},
		{name: "unknown request field", invalid: true, mutate: func(t *testing.T, directory string, payload *[]byte, envelope *map[string]any) {
			*payload = append([]byte(`{"extra":true,`), (*payload)[1:]...)
			nativePublicationTestDigest(*payload, *envelope)
		}},
		{name: "agent errors", invalid: true, mutate: func(t *testing.T, directory string, payload *[]byte, envelope *map[string]any) {
			(*envelope)["errors"] = []any{"unsafe"}
		}},
		{name: "competing native action", invalid: true, mutate: func(t *testing.T, directory string, payload *[]byte, envelope *map[string]any) {
			(*envelope)["items"] = append((*envelope)["items"].([]any), map[string]any{"type": "native_action", "request": "{}"})
		}},
		{name: "ambiguous envelope", invalid: true, mutate: func(t *testing.T, directory string, payload *[]byte, envelope *map[string]any) {
			(*envelope)["items"].([]any)[0].(map[string]any)["request"] = "{}"
		}},
		{name: "artifact symlink", invalid: true, mutate: func(t *testing.T, directory string, payload *[]byte, envelope *map[string]any) {
			if err := os.Symlink(filepath.Join(directory, "payload"), filepath.Join(directory, "agent", nativeRequestFile)); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "artifact directory", invalid: true, mutate: func(t *testing.T, directory string, payload *[]byte, envelope *map[string]any) {
			if err := os.Mkdir(filepath.Join(directory, "agent", nativeRequestFile), 0700); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			if err := os.Mkdir(filepath.Join(directory, "agent"), 0700); err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(nativePublicationTestRequest())
			if err != nil {
				t.Fatal(err)
			}
			envelope := map[string]any{"items": []any{map[string]any{"type": "native_replacement", "request_sha256": ""}}}
			nativePublicationTestDigest(payload, envelope)
			if test.mutate != nil {
				test.mutate(t, directory, &payload, &envelope)
			}
			artifact := filepath.Join(directory, "agent", nativeRequestFile)
			if _, err := os.Lstat(artifact); os.IsNotExist(err) {
				if err := os.WriteFile(artifact, payload, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(directory, "payload"), payload, 0600); err != nil {
				t.Fatal(err)
			}
			output, err := json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			outputFile := filepath.Join(directory, "output.json")
			if err := os.WriteFile(outputFile, output, 0600); err != nil {
				t.Fatal(err)
			}
			request, err := ReadNativePublicationRequest(outputFile)
			if test.invalid {
				if err == nil {
					t.Fatal("unsafe request accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (request == nil) != test.absent {
				t.Fatalf("unexpected request presence: %v", request)
			}
			if request != nil && request.request.Patch != nativePublicationTestRequest().Patch {
				t.Fatal("digest transport altered patch")
			}
		})
	}
}

// Native fixture digests bind the exact byte stream, not a reserialized JSON document.
func nativePublicationTestDigest(payload []byte, envelope map[string]any) {
	sum := sha256.Sum256(payload)
	envelope["items"].([]any)[0].(map[string]any)["request_sha256"] = hex.EncodeToString(sum[:])
}

// Native test Git executes real plumbing in repositories created exclusively by the test.
func nativePublicationTestGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	repo := &repository.Repo{Path: root}
	output, err := repo.NativeGit(context.Background(), nil, "", args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(output))
}

// TestNativePublisherRejectsInvalidLocalInputs avoids network and executable dependencies entirely.
func TestNativePublisherRejectsInvalidLocalInputs(t *testing.T) {
	publisher := &NativePublisher{}
	request := nativePublicationTestRequest()
	request.Body = "/hold cancel"
	if _, err := publisher.Publish(context.Background(), NativePublishOptions{Root: t.TempDir(), Request: &NativePublicationRequest{request: request}}); err == nil {
		t.Fatal("unsafe local request accepted")
	}
	receipt, err := publisher.Publish(context.Background(), NativePublishOptions{})
	if err != nil || receipt["status"] != "no-request" {
		t.Fatalf("receipt=%v err=%v", receipt, err)
	}
	if _, err := NewNativePublisher("owner/repo"); err == nil {
		t.Fatal("missing client injection accepted")
	}
}

// TestNativePublicationGitConfigurationCannotExecute verifies inherited executable controls do not run during Git preparation.
func TestNativePublicationGitConfigurationCannotExecute(t *testing.T) {
	f := newNativeIntegrationFixture(t)
	git := nativePublicationGit{repo: &repository.Repo{Path: f.root}}
	marker := filepath.Join(t.TempDir(), "executed")
	script := filepath.Join(t.TempDir(), "malicious")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch '"+marker+"'\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	nativePublicationTestGit(t, git.repo.Path, "config", "core.fsmonitor", script)
	nativePublicationTestGit(t, git.repo.Path, "config", "core.hooksPath", filepath.Dir(script))
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.fsmonitor")
	t.Setenv("GIT_CONFIG_VALUE_0", script)
	t.Setenv("GIT_DIR", "/invalid/inherited/redirection")
	if _, err := git.run(context.Background(), nil, "read-tree", f.base); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("executable Git config ran: %v", err)
	}
}

// TestNativePublicationJSONUnicode preserves valid pairs and literal escapes while rejecting lone surrogates.
func TestNativePublicationJSONUnicode(t *testing.T) {
	for _, test := range []struct {
		name, payload string
		invalid       bool
	}{
		{name: "ordinary text", payload: `{"body":"safe"}`},
		{name: "unicode pair", payload: `{"body":"\ud83d\ude00"}`},
		{name: "literal escape", payload: `{"body":"\\ud800"}`},
		{name: "low surrogate", payload: `{"body":"\udc00"}`, invalid: true},
		{name: "high surrogate", payload: `{"body":"\ud800"}`, invalid: true},
		{name: "reversed pair", payload: `{"body":"\udc00\ud800"}`, invalid: true},
		{name: "high then ordinary", payload: `{"body":"\ud800\u0041"}`, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := nativePublicationJSONUnicode([]byte(test.payload))
			if (err != nil) != test.invalid {
				t.Fatalf("invalid=%v err=%v", test.invalid, err)
			}
		})
	}
}

// TestNativePublicationMarkerCanonicalRoundtrip retains exact Python-compatible source ordering across retries.
func TestNativePublicationMarkerCanonicalRoundtrip(t *testing.T) {
	group := strings.Repeat("a", 24)
	marker, err := nativeEncodePublicationMarker(group, nativePublicationTestRequest().SourcePRs)
	if err != nil {
		t.Fatal(err)
	}
	want := `<!-- native-dependabot-maintenance:{"group":"` + group + `","sources":[{"head_sha":"` + strings.Repeat("a", 40) + `","number":7}]} -->`
	if marker != want {
		t.Fatalf("ownership marker no longer matches stored PR metadata: got %s, want %s", marker, want)
	}
	metadata, err := nativePublicationMetadata(want)
	if err != nil {
		t.Fatal(err)
	}
	roundtrip, err := nativeEncodePublicationMarker(metadata["group"], metadata["sources"])
	if err != nil {
		t.Fatal(err)
	}
	if roundtrip != want {
		t.Fatalf("stored ownership metadata changed when re-encoded: got %s, want %s", roundtrip, want)
	}
}

// TestNativePublicationFixedLabelGuards creates labels only after an authenticated not-found response.
func TestNativePublicationFixedLabelGuards(t *testing.T) {
	for _, test := range []struct {
		name                        string
		initialStatus, createStatus int
		invalidLabel, wantError     bool
	}{
		{name: "existing fixed label", initialStatus: http.StatusOK},
		{name: "forbidden read fails closed", initialStatus: http.StatusForbidden, wantError: true},
		{name: "server read failure fails closed", initialStatus: http.StatusInternalServerError, wantError: true},
		{name: "missing label created", initialStatus: http.StatusNotFound, createStatus: http.StatusCreated},
		{name: "concurrent label creator", initialStatus: http.StatusNotFound, createStatus: http.StatusUnprocessableEntity},
		{name: "ambiguous creation failure", initialStatus: http.StatusNotFound, createStatus: http.StatusInternalServerError, wantError: true},
		{name: "wrong label identity", initialStatus: http.StatusOK, invalidLabel: true, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			created := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/repos/owner/repo/labels/"+nativePublicationLabel:
					if !created && test.initialStatus != http.StatusOK {
						http.Error(w, "label unavailable", test.initialStatus)
						return
					}
					label := &gh.Label{ID: gh.Ptr(int64(42)), Name: gh.Ptr(nativePublicationLabel)}
					if test.invalidLabel {
						label.Name = gh.Ptr("foreign-label")
					}
					if err := json.NewEncoder(w).Encode(label); err != nil {
						t.Error(err)
					}
				case r.Method == http.MethodPost && r.URL.Path == "/repos/owner/repo/labels":
					if test.initialStatus != http.StatusNotFound || created {
						t.Errorf("unauthorized or repeated label creation")
						http.Error(w, "unexpected creation", http.StatusForbidden)
						return
					}
					var label gh.Label
					if err := json.NewDecoder(r.Body).Decode(&label); err != nil {
						t.Error(err)
					}
					if label.GetName() != nativePublicationLabel || label.GetColor() != "0366d6" || label.GetDescription() != "Validated native Dependabot replacement maintained by gh-aw-recompile" {
						t.Errorf("unexpected label definition: %+v", label)
					}
					created = true
					if test.createStatus != http.StatusCreated {
						http.Error(w, "creation response", test.createStatus)
						return
					}
					w.WriteHeader(http.StatusCreated)
					if err := json.NewEncoder(w).Encode(&gh.Label{ID: gh.Ptr(int64(42)), Name: gh.Ptr(nativePublicationLabel)}); err != nil {
						t.Error(err)
					}
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			api := gh.NewClient(server.Client())
			var err error
			api.BaseURL, err = url.Parse(server.URL + "/")
			if err != nil {
				t.Fatal(err)
			}
			publisher := &NativePublisher{owner: "owner", repo: "repo", api: api, writer: api}
			err = publisher.ensurePublicationLabel(context.Background())
			if (err != nil) != test.wantError {
				t.Fatalf("wantError=%v err=%v", test.wantError, err)
			}
			if test.createStatus == http.StatusInternalServerError && !errors.Is(err, automation.ErrIndeterminate) {
				t.Fatalf("creation uncertainty not preserved: %v", err)
			}
		})
	}
}
