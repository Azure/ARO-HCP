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

package nodehealth

import "testing"

func TestDefaultIsDisabled(t *testing.T) {
	cfg := Default()
	if cfg.Enabled {
		t.Error("default config should be disabled")
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name        string
		data        string
		wantEnabled bool
		wantErr     bool
	}{
		{name: "empty falls back to default", data: "", wantEnabled: false},
		{name: "enable", data: "enabled: true\n", wantEnabled: true},
		{name: "invalid yaml", data: "enabled: [oops", wantErr: true},
		{name: "unknown key is rejected", data: "enabeld: true\n", wantErr: true},
		{name: "unknown key alongside a known one is rejected", data: "enabled: true\nextra: 1\n", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tc.data))
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.Enabled != tc.wantEnabled {
				t.Errorf("Enabled = %v, want %v", cfg.Enabled, tc.wantEnabled)
			}
		})
	}
}
