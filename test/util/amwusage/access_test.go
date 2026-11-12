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

package amwusage

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type accessTestTransport func(*http.Request) (*http.Response, error)

func (f accessTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGrafanaAccess(t *testing.T) {
	var gotAuth string
	transport := accessTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://example.region.grafana.azure.com/api/datasources" {
			t.Fatalf("unexpected discovery URL %s", r.URL)
		}
		gotAuth = r.Header.Get("Authorization")
		body := `[
			{"uid":"am","type":"grafana-azure-monitor-datasource"},
			{"uid":"prom-b","type":"prometheus","url":"https://ws-abc.westus3.prometheus.monitor.azure.com"},
			{"uid":"prom-a","type":"prometheus","url":"https://ws-abc.westus3.prometheus.monitor.azure.com"},
			{"uid":"other","type":"prometheus","url":"https://prometheus.example.com"},
			{"uid":"kusto","type":"grafana-azure-data-explorer-datasource"}]`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})
	access, err := NewGrafanaAccess(context.Background(), "https://example.region.grafana.azure.com/", scanTestCredential{}, transport)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth == "" {
		t.Fatal("discovery request was not authenticated")
	}
	for _, tc := range []struct{ logical, want string }{
		{"https://management.azure.com/subscriptions/s/resourceGroups/rg/providers/Microsoft.Monitor/accounts/ws?api-version=2023-04-03",
			"https://example.region.grafana.azure.com/api/datasources/uid/am/resources/azuremonitor/subscriptions/s/resourceGroups/rg/providers/Microsoft.Monitor/accounts/ws?api-version=2023-04-03"},
		{"https://ws-abc.westus3.prometheus.monitor.azure.com/api/v1/query?query=up",
			"https://example.region.grafana.azure.com/api/datasources/uid/prom-a/resources/api/v1/query?query=up"},
	} {
		u, _ := url.Parse(tc.logical)
		target, scope, err := access.Resolve(u)
		if err != nil {
			t.Fatal(err)
		}
		if target.String() != tc.want || scope != grafanaScope {
			t.Fatalf("Resolve(%s) = %s %s, want %s", tc.logical, target, scope, tc.want)
		}
	}
	for _, logical := range []string{"https://other.westus3.prometheus.monitor.azure.com/api/v1/query", "https://example.com/x"} {
		u, _ := url.Parse(logical)
		if _, _, err := access.Resolve(u); err == nil {
			t.Fatalf("Resolve(%s) succeeded", logical)
		}
	}
}

func TestGrafanaAccessRejects(t *testing.T) {
	for _, endpoint := range []string{"http://example.region.grafana.azure.com", "https://example.com", "https://example.region.grafana.azure.com/path", "https://u@example.region.grafana.azure.com"} {
		if _, err := NewGrafanaAccess(context.Background(), endpoint, scanTestCredential{}, nil); err == nil {
			t.Fatalf("accepted %s", endpoint)
		}
	}
	for _, datasources := range [][]grafanaDatasource{
		nil,
		{{UID: "a", Type: grafanaAzureMonitorType}, {UID: "b", Type: grafanaAzureMonitorType}},
	} {
		if _, err := newGrafanaAccess(url.URL{Scheme: "https", Host: "example.region.grafana.azure.com"}, datasources); err == nil {
			t.Fatalf("accepted datasources %v", datasources)
		}
	}
}

func TestDirectAccess(t *testing.T) {
	for logical, scope := range map[string]string{
		"https://management.azure.com/subscriptions/s":                     armScope,
		"https://ws-abc.westus3.prometheus.monitor.azure.com/api/v1/query": prometheusScope,
	} {
		u, _ := url.Parse(logical)
		target, got, err := DirectAccess{}.Resolve(u)
		if err != nil || target.String() != logical || got != scope {
			t.Fatalf("Resolve(%s) = %v %s %v", logical, target, got, err)
		}
	}
}
