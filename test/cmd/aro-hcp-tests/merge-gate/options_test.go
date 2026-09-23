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

// validOpts returns a RawOptions with every required input populated.
func validOpts() *RawOptions {
	return &RawOptions{
		URL:          "https://dash.example.com",
		JobSpec:      validSpec,
		ClientID:     "id",
		ClientSecret: "secret",
		TokenURL:     "https://login.example.com/oauth2/token",
		Scopes:       []string{"api://dash/.default"},
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*RawOptions)
		wantErr bool
	}{
		{name: "valid"},
		{name: "http localhost endpoint allowed", mutate: func(o *RawOptions) { o.URL = "http://localhost:8080" }},
		{name: "http 127.0.0.1 endpoint allowed", mutate: func(o *RawOptions) { o.URL = "http://127.0.0.1:8080" }},
		{name: "plain http endpoint rejected", mutate: func(o *RawOptions) { o.URL = "http://dash.example.com" }, wantErr: true},
		{name: "http non-loopback endpoint rejected", mutate: func(o *RawOptions) { o.URL = "http://10.0.0.5:8080" }, wantErr: true},
		{name: "insecure token url rejected", mutate: func(o *RawOptions) { o.TokenURL = "http://login.example.com/token" }, wantErr: true},
		{name: "empty token url rejected", mutate: func(o *RawOptions) { o.TokenURL = "" }, wantErr: true},
		{name: "empty job spec rejected", mutate: func(o *RawOptions) { o.JobSpec = "" }, wantErr: true},
		{name: "malformed job spec rejected", mutate: func(o *RawOptions) { o.JobSpec = "{" }, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := validOpts()
			if tc.mutate != nil {
				tc.mutate(o)
			}
			_, err := o.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("expected validation error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
