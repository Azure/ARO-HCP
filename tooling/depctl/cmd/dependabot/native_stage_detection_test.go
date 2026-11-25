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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNativeStageDetectionCommand checks the offline success result and fail-closed input handling.
func TestNativeStageDetectionCommand(t *testing.T) {
	for _, tc := range []struct {
		name       string
		repository string
		payload    string
		omitPath   string
		wantErr    string
	}{
		{name: "no request", repository: "Azure/ARO-HCP", payload: `{"items":[]}`},
		{name: "invalid repository", repository: "Azure/ARO-HCP/extra", omitPath: "--output-file", wantErr: "repository must be OWNER/REPO"},
		{name: "missing output file flag", repository: "Azure/ARO-HCP", omitPath: "--output-file", wantErr: "--output-file and --detection-dir are required"},
		{name: "missing detection directory flag", repository: "Azure/ARO-HCP", omitPath: "--detection-dir", wantErr: "--output-file and --detection-dir are required"},
		{name: "malformed agent output", repository: "Azure/ARO-HCP", payload: `{`, wantErr: "stage native detection"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			original := filepath.Join(root, "agent_output.json")
			if tc.payload != "" {
				if err := os.WriteFile(original, []byte(tc.payload), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			detectionDir := filepath.Join(root, "detection")
			args := []string{"native", "stage-detection", "--repository", tc.repository}
			if tc.omitPath != "--output-file" {
				args = append(args, "--output-file", original)
			}
			if tc.omitPath != "--detection-dir" {
				args = append(args, "--detection-dir", detectionDir)
			}
			out, err := executeNativeCommand(t, args)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) || out != "" {
					t.Fatalf("stdout=%q error=%v; want %q without stdout", out, err, tc.wantErr)
				}
				return
			}
			if err != nil || out != "{\"status\":\"no-request\"}\n" {
				t.Fatalf("stdout=%q error=%v; want no-request JSON", out, err)
			}
			after, err := os.ReadFile(original)
			if err != nil || string(after) != tc.payload {
				t.Fatalf("original output changed: %q, %v", after, err)
			}
			if _, err := os.Stat(detectionDir); !os.IsNotExist(err) {
				t.Fatalf("detection directory must not be accessed for no-request: %v", err)
			}
		})
	}
}
