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
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const (
	collectionMaxWorkspaces = 4
	collectionMaxMetrics    = 5 // Per workspace; at most 12 across all workspaces.
	collectionMaxQueries    = 78
	collectionMaxRequests   = 110 // Four discovery/platform plans, 72 aggregate and six inventory queries.
	collectionMaxBody       = 8 << 20
)

var (
	collectionResourceID      = regexp.MustCompile(`(?i)^/subscriptions/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})/resourceGroups/([a-z0-9_.()-]+)/providers/Microsoft\.Monitor/accounts/([a-z0-9_-]+)$`)
	collectionMetricName      = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)
	collectionPrometheusHost  = regexp.MustCompile(`^[a-z0-9-]+\.[a-z0-9-]+\.prometheus\.monitor\.azure\.com$`)
	collectionPlatformMetrics = []string{"ActiveTimeSeries", "ActiveTimeSeriesLimit", "EventsPerMinuteIngested", "EventsPerMinuteIngestedLimit", "EventsDropped", "TimeSeriesSamplesDropped"}
)

// CollectOptions describes an explicitly bounded, Azure public-cloud collection.
// Metrics are matched independently against each workspace's discovered catalog.
// Selection optionally adds exact names keyed by workspace name or ARM ID.
type CollectOptions struct {
	Workspaces         []string
	Start, End         time.Time
	Metrics            []string
	InventoryMetrics   []string            // Exact names; at most three workspace/metric pairs, two 12h snapshots each.
	InventorySelection map[string][]string // Optional workspace-scoped inventory names.
	Selection          map[string][]string
	Context            json.RawMessage
	RunName, ProwURL   string
	Output             string
	Credential         azcore.TokenCredential
	// Transport is optional and primarily useful for offline tests. Redirects are
	// always disabled, including when a custom transport is supplied.
	Transport http.RoundTripper
}

// CollectSummary counts attempted requests, not inferred ingestion or scan cost.
type CollectSummary struct {
	Requests, Failed, PromQL, Platform, Selected int
}

type collectionRun struct {
	Job           string `json:"job"`
	Build         string `json:"build"`
	Prow          string `json:"prow"`
	Start         int64  `json:"start"`
	End           int64  `json:"end"`
	DurationMS    int64  `json:"durationMs"`
	PlatformStart int64  `json:"platformStart"`
	PlatformEnd   int64  `json:"platformEnd"`
}

type collectionWorkspace struct {
	Name        string             `json:"name"`
	ID          string             `json:"id"`
	Endpoint    string             `json:"endpoint"`
	Names       []string           `json:"names"`
	Discovery   string             `json:"discovery"`
	Platform    map[string]string  `json:"platform"`
	Metrics     []collectionMetric `json:"metrics"`
	Inventories []seriesInventory  `json:"inventories,omitempty"`
}

type collectionMetric struct {
	Name                 string `json:"name"`
	Range                string `json:"range"`
	Instant              string `json:"instant"`
	RangeQuery           string `json:"rangeQuery"`
	InstantQuery         string `json:"instantQuery"`
	NewSeries            string `json:"newSeries,omitempty"`
	NewSeriesQuery       string `json:"newSeriesQuery,omitempty"`
	Samples              string `json:"samples,omitempty"`
	SamplesQuery         string `json:"samplesQuery,omitempty"`
	BaselineSeries       string `json:"baselineSeries,omitempty"`
	BaselineSeriesQuery  string `json:"baselineSeriesQuery,omitempty"`
	BaselineSamples      string `json:"baselineSamples,omitempty"`
	BaselineSamplesQuery string `json:"baselineSamplesQuery,omitempty"`
}

type collectionContext struct {
	SchemaVersion int `json:"schemaVersion"`
	Run           struct {
		Start time.Time `json:"start"`
		End   time.Time `json:"end"`
		Prow  string    `json:"prow"`
	} `json:"run"`
	Baseline *struct {
		Start   time.Time `json:"start"`
		End     time.Time `json:"end"`
		Reason  string    `json:"reason"`
		Sources []string  `json:"sources"`
	} `json:"baseline"`
	Clusters []struct {
		ID         string   `json:"id"`
		Name       string   `json:"name"`
		ResourceID string   `json:"resourceId"`
		Namespaces []string `json:"namespaces"`
		SourceURLs []string `json:"sourceURLs"`
	} `json:"clusters"`
	Limitations []string `json:"limitations"`
}

// Validate supplied provenance, not its truth: no source URLs are fetched and no
// quiet window or namespace ownership is inferred. Preserve the original JSON.
func (o CollectOptions) collectionContext() (*collectionContext, error) {
	if len(o.Context) == 0 {
		return nil, nil
	}
	var c collectionContext
	if err := json.Unmarshal(o.Context, &c); err != nil {
		return nil, fmt.Errorf("context: %w", err)
	}
	if c.SchemaVersion != 1 || !c.Run.Start.Equal(o.Start) || !c.Run.End.Equal(o.End) {
		return nil, errors.New("context must have schemaVersion 1 and run times matching start/end exactly")
	}
	sources := []string{c.Run.Prow}
	if o.ProwURL != "" && o.ProwURL != c.Run.Prow {
		return nil, errors.New("context run prow conflicts with prow-url")
	}
	if b := c.Baseline; b != nil {
		if b.Start.IsZero() || b.End.IsZero() || b.Start.Nanosecond() != 0 || b.End.Nanosecond() != 0 ||
			b.End.Sub(b.Start) < 5*time.Minute || b.End.Sub(b.Start) > 4*time.Hour ||
			b.End.After(o.Start) || b.Start.Before(o.Start.Add(-32*24*time.Hour)) || o.Start.Sub(b.End) > 24*time.Hour {
			return nil, errors.New("context baseline must be a prior nonoverlapping whole-second window of 5 minutes to 4 hours, starting within 32 days of run start and ending within 24 hours of run start")
		}
		if strings.TrimSpace(b.Reason) == "" || len(b.Sources) == 0 {
			return nil, errors.New("context baseline requires a reason and source URLs")
		}
		sources = append(sources, b.Sources...)
	}
	ids, resources, namespaces := map[string]bool{}, map[string]bool{}, map[string]bool{}
	namespaceName := regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	for _, cluster := range c.Clusters {
		resource := strings.ToLower(cluster.ResourceID)
		if strings.TrimSpace(cluster.ID) == "" || strings.TrimSpace(cluster.Name) == "" || ids[cluster.ID] || resources[resource] ||
			!strings.HasPrefix(resource, "/subscriptions/") || !strings.Contains(resource, "/providers/microsoft.redhatopenshift/hcpopenshiftclusters/") ||
			!strings.HasSuffix(resource, "/"+strings.ToLower(cluster.Name)) || len(cluster.Namespaces) == 0 || len(cluster.SourceURLs) == 0 {
			return nil, errors.New("context clusters require unique IDs and resource IDs, names, namespaces, and source URLs")
		}
		ids[cluster.ID], resources[resource] = true, true
		for _, namespace := range cluster.Namespaces {
			if len(namespace) > 63 || !namespaceName.MatchString(namespace) || namespaces[namespace] {
				return nil, fmt.Errorf("context namespace must be valid and uniquely owned: %q", namespace)
			}
			namespaces[namespace] = true
		}
		sources = append(sources, cluster.SourceURLs...)
	}
	for _, source := range sources {
		u, err := url.Parse(source)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return nil, errors.New("context provenance requires HTTPS URLs without credentials")
		}
	}
	return &c, nil
}

type collectionRequest struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Method string `json:"method"`
	URL    string `json:"url"`
}

type collectionEnvelope struct {
	Request       collectionRequest `json:"request"`
	OK            bool              `json:"ok"`
	Status        int               `json:"status"`
	StartedAt     time.Time         `json:"startedAt"`
	FinishedAt    time.Time         `json:"finishedAt"`
	ElapsedMS     int64             `json:"elapsedMs"`
	ResponseBytes int               `json:"responseBytes"`
	Headers       map[string]string `json:"headers"`
	Error         string            `json:"error,omitempty"`
	APICost       json.RawMessage   `json:"apiCost"`
	QueryStats    json.RawMessage   `json:"queryStats"`
}

type collectionRecord struct {
	collectionEnvelope
	Body string `json:"body"`
}

type collectionData struct {
	SchemaVersion int                          `json:"schemaVersion"`
	Subscription  string                       `json:"subscription"`
	Run           collectionRun                `json:"run"`
	Context       json.RawMessage              `json:"context,omitempty"`
	Workspaces    []*collectionWorkspace       `json:"workspaces"`
	Manifests     []collectionEnvelope         `json:"manifests"`
	Records       map[string]*collectionRecord `json:"records"`
	// These additions distinguish a checkpoint from a finished collection and
	// preserve selection errors that did not generate an HTTP request.
	Complete bool     `json:"complete"`
	Errors   []string `json:"errors,omitempty"`
}

// Validate rejects unsafe or unbounded plans before acquiring tokens or writing.
func (o CollectOptions) Validate() error {
	if len(o.Workspaces) == 0 || len(o.Workspaces) > collectionMaxWorkspaces {
		return fmt.Errorf("select 1-%d workspaces", collectionMaxWorkspaces)
	}
	if o.Start.IsZero() || o.End.IsZero() || o.End.Sub(o.Start) < 5*time.Minute || o.End.Sub(o.Start) > 12*time.Hour {
		return errors.New("explicit start/end window must be between 5 minutes and 12 hours")
	}
	if o.Start.Nanosecond() != 0 || o.End.Nanosecond() != 0 {
		return errors.New("schema-1 start/end must be whole seconds; fractional seconds are not rounded")
	}
	if o.Output == "" {
		return errors.New("output is required")
	}
	if o.ProwURL != "" {
		u, err := url.Parse(o.ProwURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return errors.New("prow-url must be an HTTPS URL without credentials (annotation only, not verified)")
		}
	}
	names, ids, keys := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, id := range o.Workspaces {
		parts := collectionResourceID.FindStringSubmatch(id)
		if parts == nil || parts[2] == "." || parts[2] == ".." {
			return fmt.Errorf("workspace must be a Microsoft.Monitor/accounts ARM resource ID: %q", id)
		}
		if ids[strings.ToLower(id)] || names[strings.ToLower(parts[3])] {
			return errors.New("workspace IDs and names must be unique")
		}
		ids[strings.ToLower(id)], names[strings.ToLower(parts[3])] = true, true
		keys[id], keys[parts[3]] = true, true
		if _, byID := o.Selection[id]; byID {
			if _, byName := o.Selection[parts[3]]; byName {
				return fmt.Errorf("selection specifies both name and ID for %s", parts[3])
			}
		}
	}
	for key := range o.Selection {
		if !keys[key] {
			return fmt.Errorf("selection key %q does not match a requested workspace", key)
		}
	}
	for key := range o.InventorySelection {
		if !keys[key] {
			return fmt.Errorf("inventory selection key %q does not match a requested workspace", key)
		}
	}
	if len(o.InventoryMetrics) > 3 {
		return errors.New("at most three inventory metric names may be requested")
	}
	selections := [][]string{o.Metrics, o.InventoryMetrics}
	for _, metrics := range o.Selection {
		selections = append(selections, metrics)
	}
	for _, metrics := range o.InventorySelection {
		selections = append(selections, metrics)
	}
	for _, metrics := range selections {
		seen := map[string]bool{}
		if len(metrics) > 12 {
			return errors.New("at most 12 distinct metric names may be requested")
		}
		for _, metric := range metrics {
			if !collectionMetricName.MatchString(metric) || seen[metric] {
				return fmt.Errorf("metric must be a unique exact Prometheus name: %q", metric)
			}
			seen[metric] = true
		}
	}
	_, err := o.collectionContext()
	return err
}

// Collect makes serial GETs without retries. It creates Output exclusively and
// replaces it with a schema-1 checkpoint after each response. Partial evidence
// remains renderable on query failures or cancellation; any failure returns an error.
func Collect(ctx context.Context, o CollectOptions) (CollectSummary, error) {
	return collect(ctx, o, nil)
}

func collect(ctx context.Context, o CollectOptions, source []byte) (CollectSummary, error) {
	var summary CollectSummary
	if err := o.Validate(); err != nil {
		return summary, err
	}
	runContext, err := o.collectionContext()
	if err != nil {
		return summary, err
	}
	if o.Credential == nil {
		return summary, errors.New("credential is required")
	}
	file, err := os.OpenFile(o.Output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return summary, err
	}
	if err := file.Close(); err != nil {
		return summary, err
	}
	start, end := o.Start.UTC(), o.End.UTC()
	platformStart := start.Add(-10 * time.Minute).Truncate(time.Minute)
	if runContext != nil {
		if o.ProwURL == "" {
			o.ProwURL = runContext.Run.Prow
		}
		if b := runContext.Baseline; b != nil && b.Start.Before(platformStart) {
			platformStart = b.Start.UTC().Truncate(time.Minute)
		}
	}
	platformEnd := end.Add(10 * time.Minute).Truncate(time.Minute)
	if platformEnd.Before(end.Add(10 * time.Minute)) {
		platformEnd = platformEnd.Add(time.Minute)
	}
	data := collectionData{
		SchemaVersion: 1,
		Context:       o.Context,
		Run: collectionRun{Job: o.RunName, Prow: o.ProwURL, Start: start.Unix(), End: end.Unix(),
			DurationMS: end.Sub(start).Milliseconds(), PlatformStart: platformStart.Unix(), PlatformEnd: platformEnd.Unix()},
		Workspaces: []*collectionWorkspace{}, Manifests: []collectionEnvelope{}, Records: map[string]*collectionRecord{},
	}
	for _, id := range o.Workspaces {
		parts := collectionResourceID.FindStringSubmatch(id)
		data.Workspaces = append(data.Workspaces, &collectionWorkspace{Name: parts[3], ID: id, Names: []string{}, Platform: map[string]string{}, Metrics: []collectionMetric{}})
	}
	// The original schema has one subscription field. Leave it empty for a
	// multi-subscription plan; each workspace still carries its full ARM ID.
	data.Subscription = collectionResourceID.FindStringSubmatch(o.Workspaces[0])[1]
	for _, id := range o.Workspaces {
		if !strings.EqualFold(collectionResourceID.FindStringSubmatch(id)[1], data.Subscription) {
			data.Subscription = ""
			break
		}
	}
	var original map[string]json.RawMessage
	var originalRecords map[string]json.RawMessage
	var originalManifests []json.RawMessage
	var originalManifestCount int
	if source != nil {
		if err := json.Unmarshal(source, &data); err != nil {
			return summary, err
		}
		if err := json.Unmarshal(source, &original); err != nil {
			return summary, err
		}
		if err := json.Unmarshal(original["records"], &originalRecords); err != nil {
			return summary, err
		}
		if err := json.Unmarshal(original["manifests"], &originalManifests); err != nil {
			return summary, err
		}
		originalManifestCount = len(data.Manifests)
		data.Complete = false
	}
	checkpoint := func() error {
		content, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			return err
		}
		if original != nil {
			// Augment only these fields; retain unknown source provenance verbatim.
			var updated map[string]json.RawMessage
			if err := json.Unmarshal(content, &updated); err != nil {
				return err
			}
			var workspaces []map[string]json.RawMessage
			if err := json.Unmarshal(original["workspaces"], &workspaces); err != nil {
				return err
			}
			for i, workspace := range data.Workspaces {
				if len(workspace.Inventories) > 0 {
					workspaces[i]["inventories"], err = json.Marshal(workspace.Inventories)
					if err != nil {
						return err
					}
				}
			}
			original["workspaces"], err = json.Marshal(workspaces)
			if err != nil {
				return err
			}
			for id, record := range data.Records {
				if _, exists := originalRecords[id]; !exists {
					originalRecords[id], err = json.Marshal(record)
					if err != nil {
						return err
					}
				}
			}
			original["records"], err = json.Marshal(originalRecords)
			if err != nil {
				return err
			}
			manifests := append([]json.RawMessage{}, originalManifests...)
			for _, record := range data.Manifests[originalManifestCount:] {
				raw, err := json.Marshal(record)
				if err != nil {
					return err
				}
				manifests = append(manifests, raw)
			}
			original["manifests"], err = json.Marshal(manifests)
			if err != nil {
				return err
			}
			for _, field := range []string{"complete", "errors"} {
				if value, ok := updated[field]; ok {
					original[field] = value
				}
			}
			content, err = json.MarshalIndent(original, "", "  ")
			if err != nil {
				return err
			}
		}
		tmp, err := os.CreateTemp(filepath.Dir(o.Output), ".amw-usage-*")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		_, writeErr := tmp.Write(content)
		closeErr := tmp.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			return err
		}
		return os.Rename(tmp.Name(), o.Output)
	}
	if err := checkpoint(); err != nil {
		return summary, err
	}
	client := &http.Client{Transport: o.Transport, Timeout: 120 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	tokens := map[string]azcore.AccessToken{}
	request := func(id, kind, target string, validate func([]byte) error) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if summary.Requests >= collectionMaxRequests || ((kind == "promql" || kind == "inventory") && summary.PromQL >= collectionMaxQueries) {
			return false, errors.New("request cap exceeded")
		}
		// Also validate at the token boundary, not only during endpoint discovery.
		u, err := url.Parse(target)
		if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" ||
			(u.Host != "management.azure.com" && !collectionPrometheusHost.MatchString(u.Host)) {
			return false, errors.New("refusing authentication to untrusted endpoint")
		}
		if summary.Requests > 0 {
			timer := time.NewTimer(300 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			case <-timer.C:
			}
		}
		scope := "https://prometheus.monitor.azure.com/.default"
		if u.Host == "management.azure.com" {
			scope = "https://management.azure.com/.default"
		}
		record := &collectionRecord{collectionEnvelope: collectionEnvelope{
			Request: collectionRequest{ID: id, Kind: kind, Method: http.MethodGet, URL: target}, StartedAt: time.Now().UTC(), Headers: map[string]string{},
		}}
		requestCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
		defer cancel()
		token := tokens[scope]
		if token.ExpiresOn.Before(time.Now().Add(time.Minute)) {
			token, err = o.Credential.GetToken(requestCtx, policy.TokenRequestOptions{Scopes: []string{scope}})
			if err == nil {
				tokens[scope] = token
			}
		}
		if err != nil {
			// Credential errors may contain subprocess output. Never persist it.
			record.Error = "Azure token acquisition failed; check credential configuration and login"
		} else {
			req, reqErr := http.NewRequestWithContext(requestCtx, http.MethodGet, target, nil)
			if reqErr != nil {
				return false, reqErr
			}
			req.Header.Set("Authorization", "Bearer "+token.Token)
			req.Header.Set("Accept", "application/json")
			response, doErr := client.Do(req)
			if doErr != nil {
				record.Error = "HTTP request failed (transport, timeout, or cancellation)"
			} else {
				record.Status = response.StatusCode
				for name := range response.Header {
					lower := strings.ToLower(name)
					if lower == "date" || lower == "content-type" || lower == "server-timing" || lower == "x-ms-request-id" || lower == "x-ms-correlation-request-id" || lower == "x-request-id" || strings.HasPrefix(lower, "x-ms-ratelimit") {
						record.Headers[lower] = response.Header.Get(name)
					}
				}
				maxBody := collectionMaxBody
				if kind == "inventory" {
					maxBody = 32 << 20
				}
				body, readErr := io.ReadAll(io.LimitReader(response.Body, int64(maxBody)+1))
				response.Body.Close()
				record.Body, record.ResponseBytes = string(body), len(body)
				var parsed struct {
					Status string          `json:"status"`
					Error  json.RawMessage `json:"error"`
					Cost   json.RawMessage `json:"cost"`
					Data   json.RawMessage `json:"data"`
				}
				switch {
				case readErr != nil:
					record.Error = "response body read failed"
				case len(body) > maxBody:
					record.Error = fmt.Sprintf("response exceeds %d MiB limit; body truncated", maxBody>>20)
				case json.Unmarshal(body, &parsed) != nil || strings.TrimSpace(string(body)) == "null":
					record.Error = "non-JSON or invalid API response"
				case response.StatusCode < 200 || response.StatusCode >= 300:
					record.Error = fmt.Sprintf("HTTP %d", response.StatusCode)
				case parsed.Status == "error" || (len(parsed.Error) > 0 && string(parsed.Error) != "null"):
					record.Error = "API returned an error; see raw body"
				default:
					if kind == "promql" || kind == "inventory" {
						var result struct {
							ResultType string            `json:"resultType"`
							Result     []json.RawMessage `json:"result"`
						}
						if parsed.Status != "success" || json.Unmarshal(parsed.Data, &result) != nil || result.Result == nil || (result.ResultType != "matrix" && result.ResultType != "vector") {
							record.Error = "invalid Prometheus query response"
						}
					}
					if kind == "platform" {
						var metrics struct {
							Value []struct {
								ErrorCode string `json:"errorCode"`
							} `json:"value"`
						}
						if json.Unmarshal(body, &metrics) != nil || metrics.Value == nil {
							record.Error = "invalid platform metrics response"
						}
						for _, metric := range metrics.Value {
							if metric.ErrorCode != "" && metric.ErrorCode != "Success" {
								record.Error = "platform metric returned an error; see raw body"
							}
						}
					}
					if validate != nil {
						if err := validate(body); err != nil {
							record.Error = err.Error()
						}
					}
				}
				record.APICost = parsed.Cost
				var stats struct {
					Stats json.RawMessage `json:"stats"`
				}
				if json.Unmarshal(parsed.Data, &stats) == nil {
					record.QueryStats = stats.Stats
				}
			}
		}
		record.OK = record.Error == ""
		record.FinishedAt = time.Now().UTC()
		record.ElapsedMS = record.FinishedAt.Sub(record.StartedAt).Milliseconds()
		data.Records[id] = record
		data.Manifests = append(data.Manifests, record.collectionEnvelope)
		summary.Requests++
		if kind == "promql" || kind == "inventory" {
			summary.PromQL++
		}
		if kind == "platform" {
			summary.Platform++
		}
		if !record.OK {
			summary.Failed++
		}
		return record.OK, checkpoint()
	}
	iso := func(t time.Time) string { return t.Format(time.RFC3339Nano) }
	queryURL := func(base string, values url.Values) string { return base + "?" + values.Encode() }
	collect := func() error {
		if source != nil {
			return collectInventories(data.Workspaces, o.InventoryMetrics, o.InventorySelection, start, end, request)
		}
		for _, workspace := range data.Workspaces {
			_, err := request(workspace.Name+"-resource", "resource", "https://management.azure.com"+workspace.ID+"?api-version=2023-04-03", func(body []byte) error {
				var resource struct {
					Properties struct {
						Metrics struct {
							Endpoint string `json:"prometheusQueryEndpoint"`
						} `json:"metrics"`
					} `json:"properties"`
				}
				if json.Unmarshal(body, &resource) != nil {
					return errors.New("invalid workspace resource response")
				}
				u, err := url.Parse(resource.Properties.Metrics.Endpoint)
				if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || !collectionPrometheusHost.MatchString(u.Host) {
					return errors.New("workspace returned an untrusted Prometheus endpoint")
				}
				workspace.Endpoint = strings.TrimSuffix(u.String(), "/")
				return nil
			})
			if err != nil {
				return err
			}
			if workspace.Endpoint != "" {
				workspace.Discovery = workspace.Name + "-names"
				_, err = request(workspace.Discovery, "discovery", queryURL(workspace.Endpoint+"/api/v1/label/__name__/values", url.Values{"start": {iso(start)}, "end": {iso(end)}}), func(body []byte) error {
					var catalog struct {
						Status string   `json:"status"`
						Data   []string `json:"data"`
					}
					if json.Unmarshal(body, &catalog) != nil || catalog.Status != "success" || catalog.Data == nil {
						return errors.New("invalid metric-name catalog")
					}
					workspace.Names = catalog.Data
					return nil
				})
				if err != nil {
					return err
				}
			}
			for _, metric := range collectionPlatformMetrics {
				id := workspace.Name + "-" + metric
				workspace.Platform[metric] = id
				values := url.Values{"api-version": {"2023-10-01"}, "metricnames": {metric}, "timespan": {iso(platformStart) + "/" + iso(platformEnd)}, "interval": {"PT1M"}, "aggregation": {"Maximum"}, "metricnamespace": {"Microsoft.Monitor/accounts"}}
				if strings.HasSuffix(metric, "Dropped") {
					values.Set("$filter", "Reason eq '*'")
					values.Set("top", "100")
				}
				_, err := request(id, "platform", queryURL("https://management.azure.com"+workspace.ID+"/providers/microsoft.insights/metrics", values), nil)
				if err != nil {
					return err
				}
			}
		}
		// Complete discovery before planning: a global name may exist in only one
		// workspace. No explicit request may silently disappear from the plan.
		matched := map[string]bool{}
		selected := make([][]string, len(data.Workspaces))
		for i, workspace := range data.Workspaces {
			for _, metric := range o.Metrics {
				if slices.Contains(workspace.Names, metric) {
					selected[i] = append(selected[i], metric)
					matched[metric] = true
				}
			}
			for _, key := range []string{workspace.Name, workspace.ID} {
				for _, metric := range o.Selection[key] {
					if !slices.Contains(workspace.Names, metric) {
						return fmt.Errorf("selected metric %q not discovered in %s", metric, workspace.Name)
					}
					if !slices.Contains(selected[i], metric) {
						selected[i] = append(selected[i], metric)
					}
				}
			}
			if len(selected[i]) > collectionMaxMetrics {
				return fmt.Errorf("%s selection exceeds %d metrics", workspace.Name, collectionMaxMetrics)
			}
			summary.Selected += len(selected[i])
		}
		for _, metric := range o.Metrics {
			if !matched[metric] {
				return fmt.Errorf("requested metric %q not discovered in any workspace", metric)
			}
		}
		if summary.Selected > 12 {
			return errors.New("selection exceeds 12 workspace/metric pairs (at most 72 PromQL queries)")
		}
		if _, err := planInventories(data.Workspaces, o.InventoryMetrics, o.InventorySelection); err != nil {
			return err
		}
		for i, workspace := range data.Workspaces {
			for _, metric := range selected[i] {
				labels := "cluster,job,namespace,hostedcontrolplane,prometheus"
				entry := collectionMetric{Name: metric, Range: workspace.Name + "-" + metric + "-range", Instant: workspace.Name + "-" + metric + "-instant",
					Samples:      workspace.Name + "-" + metric + "-samples",
					RangeQuery:   "sum by(" + labels + ")(count_over_time(" + metric + "[5m]))/5",
					InstantQuery: fmt.Sprintf("count by(%s)(count_over_time(%s[%dms]))", labels, metric, data.Run.DurationMS),
					SamplesQuery: fmt.Sprintf("sum by(%s)(count_over_time(%s[%dms]))", labels, metric, data.Run.DurationMS)}
				baselineEnd := end
				if runContext != nil && runContext.Baseline != nil {
					b := runContext.Baseline
					baselineEnd = b.End.UTC()
					durationMS := b.End.Sub(b.Start).Milliseconds()
					entry.NewSeries = workspace.Name + "-" + metric + "-new-series"
					// Subtract full physical labelsets before aggregation. The offset
					// places the explicit baseline at its own end, including any gap.
					entry.NewSeriesQuery = fmt.Sprintf("count by(%s)(count_over_time(%s[%dms]) unless count_over_time(%s[%dms] offset %dms))", labels, metric, data.Run.DurationMS, metric, durationMS, end.Sub(b.End).Milliseconds())
					entry.BaselineSeries = workspace.Name + "-" + metric + "-baseline-series"
					entry.BaselineSeriesQuery = fmt.Sprintf("count by(%s)(count_over_time(%s[%dms]))", labels, metric, durationMS)
					entry.BaselineSamples = workspace.Name + "-" + metric + "-baseline-samples"
					entry.BaselineSamplesQuery = fmt.Sprintf("sum by(%s)(count_over_time(%s[%dms]))", labels, metric, durationMS)
				}
				workspace.Metrics = append(workspace.Metrics, entry)
				_, err := request(entry.Range, "promql", queryURL(workspace.Endpoint+"/api/v1/query_range", url.Values{"query": {entry.RangeQuery}, "start": {iso(start.Add(5 * time.Minute))}, "end": {iso(end)}, "step": {"300"}, "timeout": {"90s"}}), nil)
				if err != nil {
					return err
				}
				for _, instant := range []struct {
					id, query string
					at        time.Time
				}{
					{entry.Instant, entry.InstantQuery, end},
					{entry.NewSeries, entry.NewSeriesQuery, end},
					{entry.Samples, entry.SamplesQuery, end},
					{entry.BaselineSeries, entry.BaselineSeriesQuery, baselineEnd},
					{entry.BaselineSamples, entry.BaselineSamplesQuery, baselineEnd},
				} {
					if instant.id == "" {
						continue
					}
					_, err = request(instant.id, "promql", queryURL(workspace.Endpoint+"/api/v1/query", url.Values{"query": {instant.query}, "time": {iso(instant.at)}, "timeout": {"90s"}}), nil)
					if err != nil {
						return err
					}
				}
			}
		}
		return collectInventories(data.Workspaces, o.InventoryMetrics, o.InventorySelection, start, end, request)
	}
	err = collect()
	if err != nil {
		data.Errors = append(data.Errors, err.Error())
	}
	data.Complete = err == nil && summary.Failed == 0 && len(data.Errors) == 0
	for _, record := range data.Records {
		if !record.OK {
			data.Complete = false
		}
	}
	if summary.Failed > 0 {
		err = errors.Join(err, fmt.Errorf("%d of %d requests failed; partial evidence retained", summary.Failed, summary.Requests))
	}
	return summary, errors.Join(err, checkpoint())
}
