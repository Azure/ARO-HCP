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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const (
	armScope        = "https://management.azure.com/.default"
	prometheusScope = "https://prometheus.monitor.azure.com/.default"
	// Azure Managed Grafana's first-party application ID; the same in every
	// Azure Managed Grafana instance.
	grafanaScope = "ce34e7e5-485f-4d76-964f-b3d2b16d1e4f/.default"

	grafanaAzureMonitorType = "grafana-azure-monitor-datasource"
	grafanaPrometheusType   = "prometheus"
)

var grafanaHost = regexp.MustCompile(`^[a-z0-9-]+\.[a-z0-9]+\.grafana\.azure\.com$`)

// Access maps the logical Azure Resource Manager and workspace Prometheus URLs
// the tool reasons about (and records as evidence) to the URL actually
// requested and the token scope for that request. Evidence and databases only
// contain logical URLs, so they are independent of the access mode.
type Access interface {
	Resolve(logical *url.URL) (target *url.URL, scope string, err error)
}

// DirectAccess talks to Azure Resource Manager and the workspace Prometheus
// endpoints directly with the caller's identity.
type DirectAccess struct{}

func (DirectAccess) Resolve(logical *url.URL) (*url.URL, string, error) {
	target := *logical
	switch {
	case logical.Host == "management.azure.com":
		return &target, armScope, nil
	case collectionPrometheusHost.MatchString(logical.Host):
		return &target, prometheusScope, nil
	}
	return nil, "", errors.New("refusing authentication to untrusted endpoint")
}

// GrafanaAccess sends requests through the datasource resource routes of an
// Azure Managed Grafana instance. Grafana authenticates downstream with its own
// identity, so the caller needs only Grafana access, not workspace or ARM
// permissions. Datasources are discovered from the instance:
//   - ARM requests use the single Azure Monitor datasource;
//   - Prometheus requests use the Prometheus datasource whose URL is the
//     workspace's query endpoint.
type GrafanaAccess struct {
	base         url.URL
	azureMonitor string
	prometheus   map[string]string
}

type grafanaDatasource struct {
	UID  string `json:"uid"`
	Type string `json:"type"`
	URL  string `json:"url"`
}

// NewGrafanaAccess discovers datasources from the Grafana instance at endpoint.
func NewGrafanaAccess(ctx context.Context, endpoint string, credential azcore.TokenCredential, transport http.RoundTripper) (*GrafanaAccess, error) {
	base, err := url.Parse(endpoint)
	if err != nil || base.Scheme != "https" || base.User != nil || base.Port() != "" || base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") || !grafanaHost.MatchString(base.Host) {
		return nil, errors.New("grafana URL must be an https://<name>.<region>.grafana.azure.com Azure Managed Grafana endpoint")
	}
	if credential == nil {
		return nil, errors.New("credential is required")
	}
	base.Path = ""
	token, err := credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{grafanaScope}})
	if err != nil {
		return nil, errors.New("grafana token acquisition failed; check credential configuration and login")
	}
	requestCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, base.String()+"/api/datasources", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token.Token)
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Transport: transport, Timeout: time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("listing grafana datasources: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("listing grafana datasources: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("listing grafana datasources: HTTP %d", response.StatusCode)
	}
	var datasources []grafanaDatasource
	if err := json.Unmarshal(body, &datasources); err != nil {
		return nil, fmt.Errorf("listing grafana datasources: %w", err)
	}
	return newGrafanaAccess(*base, datasources)
}

func newGrafanaAccess(base url.URL, datasources []grafanaDatasource) (*GrafanaAccess, error) {
	// Deterministic choice when several datasources point at one workspace.
	sort.Slice(datasources, func(i, j int) bool { return datasources[i].UID < datasources[j].UID })
	a := &GrafanaAccess{base: base, prometheus: map[string]string{}}
	var azureMonitor []string
	for _, ds := range datasources {
		if ds.UID == "" {
			continue
		}
		switch ds.Type {
		case grafanaAzureMonitorType:
			azureMonitor = append(azureMonitor, ds.UID)
		case grafanaPrometheusType:
			u, err := url.Parse(ds.URL)
			if err != nil || u.Scheme != "https" || !collectionPrometheusHost.MatchString(u.Host) {
				continue
			}
			if _, ok := a.prometheus[u.Host]; !ok {
				a.prometheus[u.Host] = ds.UID
			}
		}
	}
	switch len(azureMonitor) {
	case 0:
		return nil, errors.New("grafana has no Azure Monitor datasource")
	case 1:
		a.azureMonitor = azureMonitor[0]
	default:
		return nil, fmt.Errorf("grafana has %d Azure Monitor datasources; cannot choose one for ARM requests", len(azureMonitor))
	}
	return a, nil
}

func (a *GrafanaAccess) Resolve(logical *url.URL) (*url.URL, string, error) {
	var uid, prefix string
	switch {
	case logical.Host == "management.azure.com":
		uid, prefix = a.azureMonitor, "/resources/azuremonitor"
	case collectionPrometheusHost.MatchString(logical.Host):
		var ok bool
		if uid, ok = a.prometheus[logical.Host]; !ok {
			return nil, "", fmt.Errorf("grafana has no Prometheus datasource for %s", logical.Host)
		}
		prefix = "/resources"
	default:
		return nil, "", errors.New("refusing authentication to untrusted endpoint")
	}
	target := a.base
	target.Path = "/api/datasources/uid/" + uid + prefix + logical.Path
	target.RawPath = ""
	if logical.RawPath != "" {
		target.RawPath = "/api/datasources/uid/" + url.PathEscape(uid) + prefix + logical.RawPath
	}
	target.RawQuery = logical.RawQuery
	return &target, grafanaScope, nil
}
