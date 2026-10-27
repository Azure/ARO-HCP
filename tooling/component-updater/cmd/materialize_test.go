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

package cmd

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/component-updater/internal/config"
	"github.com/Azure/ARO-HCP/tooling/component-updater/internal/updater"
)

func TestMaterializeUpdates(t *testing.T) {
	failure := errors.New("materialize failed\nmake: *** [materialize] Error 2")
	for _, tt := range []struct {
		name             string
		enabled, updated bool
		runError         error
		wantCalls        int
		wantMessage      string
	}{
		{"skip materialize prints instructions", false, true, nil, 0, "Run: make"},
		{"no change", true, false, nil, 0, "Materialization skipped: no source configuration changes."},
		{"successful materialization stays quiet", true, true, nil, 1, "Materialization completed"},
		{"failed materialization keeps recovery command", true, true, failure, 1, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{Components: map[string]config.ComponentConfig{
				"istio": {Targets: []config.Target{{FilePath: filepath.Join("config", "config.yaml")}}},
			}}
			results := []updater.ComponentResult{{Name: "istio", Updated: tt.updated}}
			var output bytes.Buffer
			calls := 0
			err := materializeUpdates(context.Background(), cfg, "root", results, tt.enabled, func(_ context.Context, directory string) error {
				calls++
				if directory != filepath.Join("root", "config") {
					t.Fatalf("wrong materialization directory: %s", directory)
				}
				if strings.Contains(output.String(), "Materialization completed.") {
					t.Fatalf("completion must be reported after materialize returns: %s", &output)
				}
				return tt.runError
			}, &output)
			if !errors.Is(err, tt.runError) && (tt.runError == nil || err == nil || !strings.Contains(err.Error(), "materialize failed")) {
				t.Fatalf("unexpected materialization: calls=%d, err=%v, output=%s", calls, err, &output)
			}
			if calls != tt.wantCalls {
				t.Fatalf("unexpected materialization calls: got %d want %d", calls, tt.wantCalls)
			}
			if tt.wantMessage == "" && tt.runError == nil && output.Len() != 0 {
				t.Fatalf("unexpected materialization output: %s", &output)
			}
			if tt.wantMessage != "" && !strings.Contains(output.String(), tt.wantMessage) {
				t.Fatalf("unexpected materialization output: %s", &output)
			}
			if err != nil && (!strings.Contains(err.Error(), "source configuration was updated") || !strings.Contains(err.Error(), "rerun make")) {
				t.Fatalf("error must explain the persistent source edit and recovery command: %v", err)
			}
			if err != nil && strings.Contains(output.String(), "Materialization completed.") {
				t.Fatalf("failed materialization must not report completion: %s", &output)
			}
			if err != nil && !strings.Contains(err.Error(), "make: *** [materialize] Error 2") {
				t.Fatalf("failed materialization must include make output: %v", err)
			}
		})
	}
}
