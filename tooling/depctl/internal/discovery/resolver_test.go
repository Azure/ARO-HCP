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

package discovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Installs a fake gh that prints the given release fixture.
func fakeGH(t *testing.T, output string) {
	t.Helper()
	directory := t.TempDir()
	script := filepath.Join(directory, "gh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat \"$FAKE_GH_OUTPUT\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(directory, "release.json")
	if err := os.WriteFile(fixture, []byte(output), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+":"+os.Getenv("PATH"))
	t.Setenv("FAKE_GH_OUTPUT", fixture)
}

func TestLatestReleaseRequiresStabilityFields(t *testing.T) {
	cases := []struct {
		name    string
		release string
		want    string
		wantErr bool
	}{
		{name: "stable", release: `{"draft":false,"prerelease":false,"tag_name":"v0.48.1"}`, want: "0.48.1"},
		{name: "stable without v", release: `{"draft":false,"prerelease":false,"tag_name":"0.48.1"}`, want: "0.48.1"},
		{name: "draft", release: `{"draft":true,"prerelease":false,"tag_name":"v0.48.1"}`, wantErr: true},
		{name: "prerelease", release: `{"draft":false,"prerelease":true,"tag_name":"v0.49.0-rc.1"}`, wantErr: true},
		{name: "missing draft", release: `{"prerelease":false,"tag_name":"v0.48.1"}`, wantErr: true},
		{name: "null draft", release: `{"draft":null,"prerelease":false,"tag_name":"v0.48.1"}`, wantErr: true},
		{name: "missing prerelease", release: `{"draft":false,"tag_name":"v0.48.1"}`, wantErr: true},
		{name: "null prerelease", release: `{"draft":false,"prerelease":null,"tag_name":"v0.48.1"}`, wantErr: true},
		{name: "non-boolean draft", release: `{"draft":"false","prerelease":false,"tag_name":"v0.48.1"}`, wantErr: true},
		{name: "missing tag", release: `{"draft":false,"prerelease":false}`, wantErr: true},
		{name: "empty tag", release: `{"draft":false,"prerelease":false,"tag_name":""}`, wantErr: true},
		{name: "malformed", release: `{`, wantErr: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fakeGH(t, testCase.release)
			tag, err := NewCLIResolver(t.TempDir()).LatestRelease(context.Background(), "Azure/bicep")
			if testCase.wantErr {
				if err == nil {
					t.Fatalf("LatestRelease() = %q, want an error", tag)
				}
				return
			}
			if err != nil {
				t.Fatalf("LatestRelease() error = %v", err)
			}
			if tag != testCase.want {
				t.Fatalf("LatestRelease() = %q, want %q", tag, testCase.want)
			}
		})
	}
}
