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

package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGrafanaProvider_NextVersion(t *testing.T) {
	p := NewGrafanaProvider("sub", nil)

	tests := []struct {
		name      string
		current   string
		available []string
		want      string
	}{
		{"next available", "12", []string{"11", "12", "13"}, "13"},
		{"already latest", "13", []string{"11", "12", "13"}, ""},
		{"gap in versions", "10", []string{"10", "12", "13"}, ""},
		{"invalid current", "abc", []string{"11", "12"}, ""},
		{"empty available", "12", nil, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.NextVersion(tt.current, tt.available)
			if got != tt.want {
				t.Errorf("NextVersion(%q, %v) = %q, want %q", tt.current, tt.available, got, tt.want)
			}
		})
	}
}

func TestGrafanaProvider_AvailableVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := `{"items":[
			{"version":"13.1.0","stable":true},
			{"version":"12.4.2","stable":true},
			{"version":"13.0.0-beta1","stable":false},
			{"version":"11.3.1","stable":true},
			{"version":"12.0.0","stable":true}
		]}`
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	defer server.Close()

	p := &GrafanaProvider{
		subscriptionID: "test-sub",
		httpClient: &http.Client{
			Transport: &redirectTransport{target: server.URL},
		},
	}

	versions, err := p.AvailableVersions(context.Background(), []string{"westus3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have stable versions only, deduped by major: 11, 12, 13
	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d: %v", len(versions), versions)
	}
	if versions[0] != "11" || versions[1] != "12" || versions[2] != "13" {
		t.Errorf("expected [11 12 13], got %v", versions)
	}
}

type redirectTransport struct {
	target string
}

func (t *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = t.target[len("http://"):]
	return http.DefaultTransport.RoundTrip(req)
}

func TestExtractMajor(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"12.0.0", "12"},
		{"13.1.2", "13"},
		{"abc", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := extractMajor(tt.input)
			if got != tt.want {
				t.Errorf("extractMajor(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
