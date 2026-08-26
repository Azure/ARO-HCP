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

package mergegate

import "testing"

const validSpec = `{"type":"presubmit","refs":{"org":"Azure","repo":"ARO-HCP"}}`

func TestValidateTokenRequiresSecureURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		token   string
		wantErr bool
	}{
		{name: "https with token", url: "https://dash.example.com", token: "t"},
		{name: "http localhost with token", url: "http://localhost:8080", token: "t"},
		{name: "http 127.0.0.1 with token", url: "http://127.0.0.1:8080", token: "t"},
		{name: "plain http with token is rejected", url: "http://dash.example.com", token: "t", wantErr: true},
		{name: "plain http without token is allowed", url: "http://dash.example.com", token: ""},
		{name: "http non-loopback host with token rejected", url: "http://10.0.0.5:8080", token: "t", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := &RawOptions{URL: tc.url, Token: tc.token, JobSpec: validSpec}
			_, err := opts.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected validation error for url=%q token=%q", tc.url, tc.token)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
