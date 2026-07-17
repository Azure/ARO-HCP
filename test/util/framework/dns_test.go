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

package framework

import (
	"testing"
)

func TestHostnameFromURL(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		want      string
		expectErr bool
	}{
		{
			name:  "full HTTPS URL",
			input: "https://console-openshift-console.apps.example.com",
			want:  "console-openshift-console.apps.example.com",
		},
		{
			name:  "HTTPS URL with port",
			input: "https://console-openshift-console.apps.example.com:443",
			want:  "console-openshift-console.apps.example.com",
		},
		{
			name:  "HTTPS URL with path",
			input: "https://console-openshift-console.apps.example.com/path",
			want:  "console-openshift-console.apps.example.com",
		},
		{
			name:  "HTTP URL",
			input: "http://example.com:8080",
			want:  "example.com",
		},
		{
			name:  "bare hostname",
			input: "console-openshift-console.apps.example.com",
			want:  "console-openshift-console.apps.example.com",
		},
		{
			name:  "bare hostname with port",
			input: "console-openshift-console.apps.example.com:443",
			want:  "console-openshift-console.apps.example.com",
		},
		{
			name:      "empty string",
			input:     "",
			expectErr: true,
		},
		{
			name:      "scheme only",
			input:     "https://",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := HostnameFromURL(tt.input)
			if tt.expectErr {
				if err == nil {
					t.Errorf("expected error, got hostname %q", got)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
