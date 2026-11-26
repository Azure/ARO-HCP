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

package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/require"
)

func TestReleaseKubeCommit(t *testing.T) {
	version := semver.MustParse("4.20.48")
	commit := strings.Repeat("a", 40)
	for _, tt := range []struct {
		name, version, component, sha string
		status                        int
	}{
		{"stable hyperkube", "4.20.48", "hyperkube", commit, http.StatusOK},
		{"wrong release", "4.20.47", "hyperkube", commit, http.StatusOK},
		{"wrong component", "4.20.48", "cli", commit, http.StatusOK},
		{"missing commit", "4.20.48", "hyperkube", "", http.StatusOK},
		{"missing release", "", "", "", http.StatusNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/releasetag/4.20.48/json" {
					http.NotFound(w, r)
					return
				}
				w.WriteHeader(tt.status)
				fmt.Fprintf(w, `{"metadata":{"version":%q},"references":{"spec":{"tags":[{"name":%q,"annotations":{"io.openshift.build.commit.id":%q}}]}}}`, tt.version, tt.component, tt.sha)
			}))
			defer server.Close()
			got, err := releaseKubeCommit(t.Context(), server.URL, version)
			if tt.name == "stable hyperkube" {
				require.NoError(t, err)
				require.Equal(t, commit, got)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestPrivateReleaseKubeCommit(t *testing.T) {
	target := semver.MustParse("4.20.48-assembly.example")
	image := "example.org/ocp/release@sha256:" + strings.Repeat("c", 64)
	commit := strings.Repeat("b", 40)
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	for _, tt := range []struct{ embedded, wantError string }{
		{"4.20.0-0.internal", ""},
		{"4.21.0-0.internal", "minor line"},
	} {
		t.Run(tt.embedded, func(t *testing.T) {
			oc := fmt.Sprintf(`#!/bin/sh
test "$*" = 'adm release info %s --filter-by-os=linux/amd64 -o json' || exit 1
echo '{"metadata":{"version":"%s"},"references":{"spec":{"tags":[{"name":"hyperkube","annotations":{"io.openshift.build.commit.id":"%s"}}]}}}'
`, image, tt.embedded, commit)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "oc"), []byte(oc), 0700))
			got, err := privateReleaseKubeCommit(t.Context(), target, image)
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				return
			}
			require.NoError(t, err)
			require.Equal(t, commit, got)
		})
	}
}
