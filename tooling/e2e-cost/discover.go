// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cost

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const discoveryJob = "pull-ci-Azure-ARO-HCP-main-e2e-parallel"
const discoveryBucket = "test-platform-results-public"
const testArtifactPrefix = "artifacts/e2e-parallel/aro-hcp-test-local/artifacts/"
const maxArtifactBytes = 32 << 20

var discoveryPath = regexp.MustCompile(`^pr-logs/pull/Azure_ARO-HCP/([0-9]+)/` + discoveryJob + `/([0-9]+)/?$`)
var discoveryUUID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var discoverySHA = regexp.MustCompile(`(?i)^[0-9a-f]{40}$`)
var discoveryRG = regexp.MustCompile(`^[a-zA-Z0-9_.()-]{1,90}$`)
var discoveryKV = regexp.MustCompile(`"([a-zA-Z][a-zA-Z0-9]*)"\s*=\s*("(?:[^"\\]|\\.)*"|\[[^\n]*?\])`)
var discoveryRedacted = regexp.MustCompile(`(?i)^x+$`)

type discoveredArtifact struct {
	name string
	data any
}

type discoveredTest struct {
	sub      string
	badSub   bool
	attempts int
	outcomes []string
	clusters map[string]bool
	managed  map[string]bool
	mapped   map[string]bool
	rejected map[string]bool
	accepted map[string]bool
	emptyVMs map[string]string
	timing   []any
}

type discovery struct {
	snapshot *Snapshot
	groups   map[string]*Group
	tests    map[string]*discoveredTest
	excluded map[string]bool
}

// Discover reads public, run-scoped artifacts only. It never consults Azure or
// the current checkout, and errors leave already confirmed ownership intact.
func Discover(ctx context.Context, client *http.Client, jobURL string, now time.Time) *Snapshot {
	s := &Snapshot{Version: SchemaVersion, CollectedAt: now.UTC(), Currency: "USD", CostBasis: "AmortizedCost", QueryEnd: now.UTC().Format(time.DateOnly), Job: Job{URL: jobURL}}
	d := &discovery{snapshot: s, groups: map[string]*Group{}, tests: map[string]*discoveredTest{}, excluded: map[string]bool{"global": true}}
	defer func() {
		// Shared subscription IDs may be masked; exclusions are names only.
		for name := range d.excluded {
			s.ExcludedGroups = append(s.ExcludedGroups, name)
		}
		sort.Strings(s.ExcludedGroups)
	}()
	u, err := url.Parse(jobURL)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		s.Diagnose("error", "invalid-job-url", "job", "Expected the public Prow e2e-parallel job URL")
		return s
	}
	prefix := ""
	switch {
	case u.Scheme == "https" && u.Host == "prow.ci.openshift.org":
		prefix = strings.TrimPrefix(u.Path, "/view/gs/"+discoveryBucket+"/")
	case u.Scheme == "https" && u.Host == "storage.googleapis.com":
		prefix = strings.TrimPrefix(u.Path, "/"+discoveryBucket+"/")
	case u.Scheme == "gs" && u.Host == discoveryBucket:
		prefix = strings.TrimPrefix(u.Path, "/")
	}
	match := discoveryPath.FindStringSubmatch(prefix)
	if match == nil {
		s.Diagnose("error", "invalid-job-url", "job", "Only public Azure/ARO-HCP pull-ci-Azure-ARO-HCP-main-e2e-parallel builds are supported")
		return s
	}
	prefix = strings.TrimSuffix(prefix, "/") + "/"
	s.Job.Name, s.Job.PR, s.Job.BuildID = discoveryJob, match[1], match[2]
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	names, listErr := listDiscoveryArtifacts(ctx, client, prefix)
	if listErr != nil {
		s.Diagnose("error", "artifact-list", "job", listErr.Error())
	}
	// Root timestamps remain useful even if listing fails partway through.
	for _, name := range []string{"started.json", "finished.json"} {
		body, err := getDiscoveryArtifact(ctx, client, discoveryObjectURL(prefix+name))
		var stamp struct {
			Timestamp int64  `json:"timestamp"`
			Result    string `json:"result"`
		}
		if err != nil || json.Unmarshal(body, &stamp) != nil || stamp.Timestamp <= 0 {
			s.Diagnose("error", "job-timestamp", name, "Missing or invalid job timestamp")
			continue
		}
		if name == "started.json" {
			s.Job.StartedAt = time.Unix(stamp.Timestamp, 0).UTC()
			s.QueryStart = s.Job.StartedAt.Format(time.DateOnly)
		} else {
			s.Job.FinishedAt = time.Unix(stamp.Timestamp, 0).UTC()
			s.Job.Result = stamp.Result
		}
	}
	if !s.Job.StartedAt.IsZero() && (s.Job.StartedAt.After(now) || (!s.Job.FinishedAt.IsZero() && s.Job.FinishedAt.Before(s.Job.StartedAt))) {
		s.Diagnose("error", "job-timestamp", "job", "Job timestamps are inconsistent with the collection time")
	}
	var docs []discoveredArtifact
	total := 0
	for _, fullName := range names {
		if ctx.Err() != nil {
			s.Diagnose("error", "artifact-read", "job", "Artifact discovery cancelled")
			break
		}
		name := strings.TrimPrefix(fullName, prefix)
		base := path.Base(name)
		original := strings.HasPrefix(name, testArtifactPrefix)
		selected := name == "prowjob.json" || strings.Contains(base, "clone-records") || name == "artifacts/ci-operator-metrics.json" ||
			(strings.Contains(name, "/aro-hcp-provision-environment/") && base == "config.yaml") || base == "steps.yaml" ||
			(original && (base == "identities-pool-state.yaml" || strings.HasPrefix(base, "extension_test_result_e2e_") ||
				strings.Contains(name, "/test-timing/") || strings.Contains(name, "/resourcegroups/") && (base == "deployments.yaml" || strings.HasPrefix(base, "deployment-operations-")) ||
				strings.Contains(strings.ToLower(base), "params") || base == "infrastructures.yaml" || base == "infrastructure.yaml"))
		if !selected {
			continue
		}
		body, err := getDiscoveryArtifact(ctx, client, discoveryObjectURL(fullName))
		if err != nil {
			s.Diagnose("error", "artifact-read", name, err.Error())
			continue
		}
		total += len(body)
		if total > 256<<20 {
			s.Diagnose("error", "artifact-limit", "job", "Selected artifacts exceed the 256 MiB discovery limit")
			break
		}
		var data any
		if strings.HasSuffix(base, ".json") {
			err = json.Unmarshal(body, &data)
		} else {
			err = yaml.Unmarshal(body, &data)
		}
		if err != nil || data == nil {
			s.Diagnose("error", "artifact-format", name, "Invalid JSON or YAML artifact")
			continue
		}
		docs = append(docs, discoveredArtifact{name, data})
	}
	config, steps, results, timings, pool := 0, 0, 0, 0, 0
	for _, doc := range docs {
		switch {
		case path.Base(doc.name) == "identities-pool-state.yaml":
			pool++
			walkDiscovery(doc.data, func(m map[string]any) {
				if rg := discoveryString(m, "resourceGroup"); rg != "" {
					d.excluded[strings.ToLower(rg)] = true
				}
			})
		case path.Base(doc.name) == "config.yaml":
			config++
			for _, section := range []string{"global", "kusto", "auditLogsEventHub", "serviceKeyVault"} {
				if rg := discoveryString(discoveryMap(doc.data, section), "rg"); rg != "" {
					d.excluded[strings.ToLower(rg)] = true
				}
			}
		}
	}
	for _, doc := range docs {
		switch {
		case doc.name == "prowjob.json":
			refs := discoveryMap(doc.data, "spec", "refs")
			if discoveryString(refs, "org") == "Azure" && discoveryString(refs, "repo") == "ARO-HCP" {
				walkDiscovery(refs["pulls"], func(m map[string]any) {
					if fmt.Sprint(m["number"]) == s.Job.PR && discoverySHA.MatchString(discoveryString(m, "sha")) {
						s.Job.Commit = discoveryString(m, "sha")
					}
				})
			}
		case strings.Contains(path.Base(doc.name), "clone-records"):
			walkDiscovery(doc.data, func(m map[string]any) {
				if discoveryString(m, "org") == "Azure" && discoveryString(m, "repo") == "ARO-HCP" {
					if sha := discoveryString(m, "checkout_sha"); discoverySHA.MatchString(sha) {
						s.Job.Commit = sha
					}
				}
			})
		case doc.name == "artifacts/ci-operator-metrics.json":
			walkDiscovery(discoveryMap(doc.data)["pods"], func(m map[string]any) {
				name := discoveryString(m, "pod_name")
				if strings.HasSuffix(name, "-aro-hcp-provision-environment") {
					t, _ := time.Parse(time.RFC3339, discoveryString(m, "start_time"))
					if !t.IsZero() && (s.Job.InfraStartedAt.IsZero() || t.Before(s.Job.InfraStartedAt)) {
						s.Job.InfraStartedAt = t.UTC()
					}
				}
				if strings.HasSuffix(name, "-aro-hcp-deprovision-environment") {
					t, _ := time.Parse(time.RFC3339, discoveryString(m, "completion_time"))
					if t.After(s.Job.InfraEndedAt) {
						s.Job.InfraEndedAt = t.UTC()
					}
				}
			})
		case path.Base(doc.name) == "config.yaml":
			d.infraConfig(doc.data)
		case strings.HasPrefix(doc.name, testArtifactPrefix+"test-timing/"):
			timings++
			m := discoveryMap(doc.data)
			parts, ok := m["identifier"].([]any)
			var identifiers []string
			for _, p := range parts {
				if v, ok := p.(string); ok {
					identifiers = append(identifiers, v)
				}
			}
			if !ok || len(identifiers) != len(parts) || len(parts) == 0 {
				s.Diagnose("error", "timing-identifier", doc.name, "Timing identifier must be a nonempty string array")
				continue
			}
			t := d.test(strings.Join(identifiers, " "))
			sub := discoveryString(m, "subscriptionID")
			if discoveryUUID.MatchString(sub) {
				if t.sub != "" && !strings.EqualFold(t.sub, sub) {
					t.badSub = true
				}
				t.sub = strings.ToLower(sub)
			}
			t.timing = append(t.timing, doc.data)
		}
	}
	// Steps prove additional management stamps; config count alone does not prove creation.
	for _, doc := range docs {
		if path.Base(doc.name) != "steps.yaml" {
			continue
		}
		steps++
		d.infraSteps(doc.data)
	}
	for name, t := range d.tests {
		if t.badSub {
			t.sub = ""
			s.Diagnose("error", "test-subscription-conflict", name, "Timing artifacts disagree on the test subscription")
		}
	}
	for _, doc := range docs {
		if !strings.HasPrefix(path.Base(doc.name), "extension_test_result_e2e_") {
			continue
		}
		results++
		entries, ok := doc.data.([]any)
		if !ok {
			s.Diagnose("error", "test-results", doc.name, "Expected a JSON result array")
			continue
		}
		for _, entry := range entries {
			m := discoveryMap(entry)
			name := discoveryString(m, "name")
			if name == "" {
				s.Diagnose("error", "test-results", doc.name, "Test result is missing its full name")
				continue
			}
			t := d.test(name)
			t.attempts++
			outcome := discoveryString(m, "result")
			if outcome == "" {
				outcome = "unknown"
				s.Diagnose("error", "test-results", name, "Test result is missing its outcome")
			}
			t.outcomes = append(t.outcomes, outcome)
			d.testOutput(name, discoveryString(m, "output"))
		}
	}
	var inventories []struct {
		owner string
		data  any
	}
	for _, doc := range docs {
		if !strings.HasPrefix(doc.name, testArtifactPrefix) {
			continue
		}
		base := path.Base(doc.name)
		if base != "deployments.yaml" && !strings.HasPrefix(base, "deployment-operations-") && !strings.Contains(strings.ToLower(base), "params") {
			continue
		}
		owner := ""
		if split := strings.Split(strings.TrimPrefix(doc.name, testArtifactPrefix), "/"); len(split) > 2 && split[0] == "resourcegroups" {
			for _, g := range d.groups {
				if g.Category == "Tests" && strings.EqualFold(g.Name, split[1]) {
					if owner != "" && owner != g.Owner {
						owner = ""
						break
					}
					owner = g.Owner
				}
			}
		} else if len(split) > 1 && strings.Contains(strings.ToLower(base), "params") {
			// Parameter files must belong to an exact test directory; directories
			// containing arbitrary diagnostic snapshots are not ownership evidence.
			owner = d.directoryOwner(split[0], false)
		}
		if owner == "" {
			continue
		}
		ambiguous := false
		for _, diagnostic := range s.Diagnostics {
			if diagnostic.Code == "ownership-conflict" && strings.Contains(strings.ToLower(doc.name), "/resourcegroups/"+strings.ToLower(diagnostic.Scope)+"/") {
				ambiguous = true
			}
		}
		if ambiguous {
			continue
		}
		d.managedMetadata(owner, doc.data)
		inventories = append(inventories, struct {
			owner string
			data  any
		}{owner, doc.data})
	}
	for name, t := range d.tests {
		for _, timing := range t.timing {
			d.managedMetadata(name, timing)
			inventories = append(inventories, struct {
				owner string
				data  any
			}{name, timing})
			walkDiscovery(timing, func(m map[string]any) {
				if step := discoveryString(m, "name"); strings.HasPrefix(step, "Deploy HCP cluster ") {
					cluster, _, _ := strings.Cut(strings.TrimPrefix(step, "Deploy HCP cluster "), " (v")
					t.clusters[cluster] = true
				}
			})
		}
	}
	// Inspect configuration is tied to both the exact sanitized test directory
	// and inspect-<cluster>. Never treat a diagnostic directory as an RG owner.
	for _, doc := range docs {
		if path.Base(doc.name) != "infrastructures.yaml" && path.Base(doc.name) != "infrastructure.yaml" {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(doc.name, testArtifactPrefix), "/")
		if len(parts) < 3 {
			continue
		}
		owner := d.directoryOwner(parts[0], false)
		if owner == "" || !strings.HasPrefix(parts[1], "inspect-") {
			continue
		}
		t := d.test(owner)
		cluster := ""
		for key := range t.clusters {
			if strings.EqualFold(path.Base(key), strings.TrimPrefix(parts[1], "inspect-")) {
				if cluster != "" {
					cluster = ""
					break
				}
				cluster = key
			}
		}
		if cluster == "" {
			continue
		}
		walkDiscovery(doc.data, func(m map[string]any) {
			if discoveryString(m, "kind") != "Infrastructure" || discoveryString(m, "apiVersion") != "config.openshift.io/v1" {
				return
			}
			platform := discoveryMap(m, "status", "platformStatus")
			if discoveryString(platform, "type") == "Azure" {
				d.clusterMapping(owner, cluster, discoveryString(discoveryMap(platform, "azure"), "resourceGroupName"))
			}
		})
	}
	known := map[string]bool{}
	for _, name := range names {
		known[strings.TrimPrefix(name, prefix)] = true
	}
	owners := make([]string, 0, len(d.tests))
	for name := range d.tests {
		owners = append(owners, name)
	}
	sort.Strings(owners)
	for _, name := range owners {
		t := d.test(name)
		if len(t.clusters) > 0 {
			logName := testArtifactPrefix + discoveryTestDirectory(name, false) + "/azure.log"
			if known[logName] && d.directoryOwner(discoveryTestDirectory(name, false), false) == name {
				body, err := getDiscoveryArtifact(ctx, client, discoveryObjectURL(prefix+logName))
				if err != nil {
					s.Diagnose("error", "artifact-read", logName, err.Error())
				} else {
					decoder := json.NewDecoder(bytes.NewReader(body))
					for {
						var entry map[string]any
						if err := decoder.Decode(&entry); err != nil {
							if err != io.EOF {
								s.Diagnose("error", "artifact-format", logName, "Invalid Azure SDK JSON log")
							}
							break
						}
						if discoveryString(entry, "event") == "ResponseError" {
							d.rejectedCreation(name, discoveryString(entry, "msg"))
						}
						if discoveryString(entry, "event") == "Response" {
							d.acceptedCreation(name, discoveryString(entry, "msg"))
						}
					}
				}
			}
		}
		for cluster, rg := range t.emptyVMs {
			if t.accepted[cluster] && len(t.clusters) == 1 {
				d.clusterMapping(name, cluster, rg)
			}
		}
		clusters := make([]string, 0, len(t.clusters))
		for cluster := range t.clusters {
			clusters = append(clusters, cluster)
		}
		sort.Strings(clusters)
		for _, cluster := range clusters {
			if t.mapped[cluster] || t.rejected[cluster] && !t.accepted[cluster] {
				continue
			}
			parts := strings.Split(cluster, "/")
			if len(parts) != 2 || !discoveryRG.MatchString(parts[0]) || !discoveryRG.MatchString(parts[1]) {
				continue
			}
			if d.directoryOwner(discoveryTestDirectory(name, true), true) != name {
				continue
			}
			for _, phase := range []string{"test_phase", "cleanup_phase"} {
				artifact := "artifacts/e2e-parallel/aro-hcp-gather-snapshot/artifacts/" + discoveryTestDirectory(name, true) + "/" + parts[0] + "/" + phase + "/resources/microsoft.redhatopenshift_hcpopenshiftclusters/" + parts[1] + "/state/backend/resourceState.md"
				if !known[artifact] {
					continue
				}
				body, err := getDiscoveryArtifact(ctx, client, discoveryObjectURL(prefix+artifact))
				if err != nil {
					s.Diagnose("error", "artifact-read", artifact, err.Error())
					continue
				}
				d.snapshotMapping(name, cluster, body, artifact)
				if t.mapped[cluster] {
					break
				}
			}
		}
		missing, clusterCounts := map[string]int{}, map[string]int{}
		for cluster := range t.clusters {
			if t.rejected[cluster] && !t.accepted[cluster] {
				continue
			}
			rg, _, _ := strings.Cut(cluster, "/")
			rg = strings.ToLower(rg)
			clusterCounts[rg]++
			if !t.mapped[cluster] {
				missing[rg]++
			}
		}
		// Older explicit logs supply only a test-level mapping, not a cluster ID.
		for rg, count := range missing {
			if len(t.clusters) == 1 && len(t.managed) > 0 {
				continue
			}
			s.Diagnostics = append(s.Diagnostics, Diagnostic{
				Severity: "error", Code: "missing-managed-mapping", Scope: name,
				SubscriptionID: t.sub, ResourceGroup: rg, ClusterCount: clusterCounts[rg], MissingClusterCount: count,
				Message: fmt.Sprintf("%s: %d of %d cluster creations lack an exact managed resource group mapping; confirmed rejected PUTs excluded", rg, count, clusterCounts[rg]),
			})
		}
		if t.attempts == 0 {
			s.Diagnose("error", "missing-test-result", name, "Timing artifact has no matching full test name in result JSON")
		}
		for _, g := range d.groups {
			if g.Category != "Tests" || g.Owner != name {
				continue
			}
			g.Attempts, g.Outcomes = t.attempts, t.outcomes
			if t.sub == "" || t.badSub {
				g.SubscriptionID = ""
				g.BillingStatus = "unavailable"
			}
		}
	}
	for _, item := range inventories {
		d.inventory(item.data, d.test(item.owner).sub, item.owner)
	}
	for _, required := range []struct {
		count int
		name  string
	}{{config, "provision config.yaml"}, {steps, "steps.yaml"}, {results, "extension_test_result_e2e_*.json"}, {timings, "original test-timing/*.yaml"}, {pool, "identities-pool-state.yaml"}} {
		if required.count == 0 {
			s.Diagnose("error", "missing-artifact", required.name, "Required discovery artifact is missing or unreadable")
		}
	}
	if s.Job.Commit == "" {
		s.Diagnose("error", "missing-commit", "job", "No tested SHA found in Prow or clone records")
	}
	if s.Job.InfraStartedAt.IsZero() || s.Job.InfraEndedAt.IsZero() || !s.Job.InfraEndedAt.After(s.Job.InfraStartedAt) {
		s.Diagnose("info", "infra-timing", "job", "Missing or invalid provision start / final deprovision completion timestamps; optional infra hourly rate omitted")
	}
	for _, g := range d.groups {
		sort.Slice(g.Resources, func(i, j int) bool {
			return strings.ToLower(g.Resources[i].ID+g.Resources[i].Type+g.Resources[i].Name) < strings.ToLower(g.Resources[j].ID+g.Resources[j].Type+g.Resources[j].Name)
		})
		s.Groups = append(s.Groups, *g)
	}
	sort.Slice(s.Groups, func(i, j int) bool {
		a, b := s.Groups[i], s.Groups[j]
		return a.Category+a.Owner+strings.ToLower(a.SubscriptionID+a.Name) < b.Category+b.Owner+strings.ToLower(b.SubscriptionID+b.Name)
	})
	sort.Slice(s.Diagnostics, func(i, j int) bool {
		a, b := s.Diagnostics[i], s.Diagnostics[j]
		return a.Code+a.Scope+a.Message < b.Code+b.Scope+b.Message
	})
	return s
}

func discoveryObjectURL(name string) string {
	return (&url.URL{Scheme: "https", Host: "storage.googleapis.com", Path: "/" + discoveryBucket + "/" + name}).String()
}

func getDiscoveryArtifact(ctx context.Context, client *http.Client, endpoint string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid artifact request")
	}
	var resp *http.Response
	for attempt := 0; attempt < 3; attempt++ {
		resp, err = client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("artifact request failed (network or context cancellation)")
		}
		if attempt == 2 || resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode != http.StatusServiceUnavailable {
			break
		}
		resp.Body.Close()
		timer := time.NewTimer(time.Duration(attempt+1) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("artifact request cancelled")
		case <-timer.C:
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("artifact request returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxArtifactBytes+1))
	if err != nil {
		return nil, fmt.Errorf("cannot read artifact body")
	}
	if len(body) > maxArtifactBytes {
		return nil, fmt.Errorf("artifact exceeds 32 MiB limit")
	}
	// Prow sometimes uploads gzip bytes without a Content-Encoding header.
	if len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b {
		gz, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("invalid gzip artifact")
		}
		defer gz.Close()
		body, err = io.ReadAll(io.LimitReader(gz, maxArtifactBytes+1))
		if err != nil {
			return nil, fmt.Errorf("invalid gzip artifact")
		}
		if len(body) > maxArtifactBytes {
			return nil, fmt.Errorf("expanded artifact exceeds 32 MiB limit")
		}
	}
	return body, nil
}

func listDiscoveryArtifacts(ctx context.Context, client *http.Client, prefix string) ([]string, error) {
	var names []string
	token := ""
	seen := map[string]bool{}
	objects := map[string]bool{}
	for page := 0; page < 1000; page++ {
		q := url.Values{"prefix": {prefix}, "maxResults": {"1000"}, "fields": {"items(name),nextPageToken"}, "matchGlob": {prefix + "**/{started.json,finished.json,prowjob.json,*clone-records*,ci-operator-metrics.json,config.yaml,steps.yaml,identities-pool-state.yaml,extension_test_result_e2e_*.json,timing-metadata-*,deployments.yaml,deployment-operations-*,*params*,infrastructure.yaml,infrastructures.yaml,azure.log,resourceState.md}"}}
		if token != "" {
			q.Set("pageToken", token)
		}
		body, err := getDiscoveryArtifact(ctx, client, "https://storage.googleapis.com/storage/v1/b/"+discoveryBucket+"/o?"+q.Encode())
		if err != nil {
			return names, err
		}
		var listing struct {
			Items []struct {
				Name string `json:"name"`
			} `json:"items"`
			Next string `json:"nextPageToken"`
		}
		if json.Unmarshal(body, &listing) != nil {
			return names, fmt.Errorf("invalid GCS object listing")
		}
		for _, item := range listing.Items {
			if strings.HasPrefix(item.Name, prefix) && !objects[item.Name] {
				names = append(names, item.Name)
				objects[item.Name] = true
			}
		}
		if listing.Next == "" {
			sort.Strings(names)
			return names, nil
		}
		if seen[listing.Next] {
			return names, fmt.Errorf("GCS listing repeated a page token")
		}
		seen[listing.Next], token = true, listing.Next
	}
	return names, fmt.Errorf("GCS listing exceeds 1000 pages")
}

func discoveryMap(v any, keys ...string) map[string]any {
	m, _ := v.(map[string]any)
	for _, key := range keys {
		m, _ = m[key].(map[string]any)
	}
	return m
}

func discoveryString(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func walkDiscovery(v any, visit func(map[string]any)) {
	switch x := v.(type) {
	case map[string]any:
		visit(x)
		keys := make([]string, 0, len(x))
		for key := range x {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			walkDiscovery(x[key], visit)
		}
	case []any:
		for _, child := range x {
			walkDiscovery(child, visit)
		}
	}
}

func (d *discovery) test(name string) *discoveredTest {
	if d.tests[name] == nil {
		d.tests[name] = &discoveredTest{clusters: map[string]bool{}, managed: map[string]bool{}, mapped: map[string]bool{}, rejected: map[string]bool{}, accepted: map[string]bool{}, emptyVMs: map[string]string{}}
	}
	return d.tests[name]
}

func (d *discovery) shared(rg string) bool {
	return d.excluded[strings.ToLower(rg)] || strings.HasPrefix(strings.ToLower(rg), "aro-hcp-msi-container-dev-shard")
}

func (d *discovery) group(sub, rg, category, owner, kind string) *Group {
	if !discoveryRG.MatchString(rg) || discoveryRedacted.MatchString(rg) || d.shared(rg) {
		return nil
	}
	if !discoveryUUID.MatchString(sub) {
		sub = ""
	}
	key := strings.ToLower(sub + "/" + rg)
	if old := d.groups[key]; old != nil {
		if old.Owner != owner || old.Category != category {
			if old.BillingStatus != "unavailable" {
				old.BillingStatus = "unavailable"
			}
			d.snapshot.Diagnose("error", "ownership-conflict", rg, fmt.Sprintf("Resource group is claimed by both %q and %q; billing disabled", old.Owner, owner))
		}
		return old
	}
	g := &Group{SubscriptionID: strings.ToLower(sub), Name: rg, Category: category, Owner: owner, Kind: kind, BillingStatus: "pending"}
	if sub == "" {
		g.BillingStatus = "unavailable"
		code := "missing-test-subscription"
		if category == "Infra" {
			code = "missing-infra-subscription"
		}
		d.snapshot.Diagnose("error", code, rg, "Artifact subscription is missing, redacted, or not a UUID; no account-name inference is performed")
	}
	d.groups[key] = g
	return g
}

func (d *discovery) infraConfig(data any) {
	m := discoveryMap(data)
	d.infraLookupConfig(data)
	if discoveryString(m, "regionRG") == "" || discoveryString(discoveryMap(data, "svc"), "rg") == "" || discoveryString(discoveryMap(data, "mgmt"), "rg") == "" {
		d.snapshot.Diagnose("error", "infra-config", "config.yaml", "Provision config is missing regional, service, or management resource group names")
	}
	svc := discoveryMap(data, "svc")
	sub := discoveryString(discoveryMap(svc, "subscription"), "key")
	d.group(sub, discoveryString(m, "regionRG"), "Infra", "Regional", "primary")
	for _, section := range []string{"svc", "mgmt"} {
		v := discoveryMap(data, section)
		rg := discoveryString(v, "rg")
		owner := "Service Cluster"
		if section == "mgmt" {
			owner = "Management Cluster " + discoveryStamp(rg)
		}
		sub := discoveryString(discoveryMap(v, "subscription"), "key")
		if g := d.group(sub, rg, "Infra", owner, "primary"); g != nil {
			nodes := discoveryString(discoveryMap(v, "aks"), "nodeResourceGroup")
			if nodes == "" {
				nodes = discoveryString(discoveryMap(v, "aks"), "nodeResourceGroupName")
			}
			if nodes == "" {
				nodes = rg + "-aks1"
			}
			d.group(sub, nodes, "Infra", owner, "aks-managed")
			regional := ""
			if section == "svc" {
				// This job's regional pipeline runs in the configured svc subscription.
				regional = discoveryString(m, "regionRG")
			}
			d.infraBinding(infraBinding{rg: rg, cluster: discoveryString(discoveryMap(v, "aks"), "name"), owner: owner, nodeRG: nodes, regionalRG: regional})
		}
	}
}

func discoveryStamp(rg string) string {
	i := strings.LastIndex(rg, "-mgmt-")
	if i >= 0 {
		if n, err := strconv.Atoi(rg[i+6:]); err == nil && n > 0 {
			return strconv.Itoa(n)
		}
	}
	return "1"
}

func (d *discovery) infraSteps(data any) {
	if _, ok := data.([]any); !ok {
		d.snapshot.Diagnose("error", "infra-steps", "steps.yaml", "Expected an array of pipeline steps")
		return
	}
	var mgmt *Group
	for _, g := range d.groups {
		if g.Category == "Infra" && g.Kind == "primary" && strings.HasPrefix(g.Owner, "Management Cluster ") {
			if mgmt == nil || g.Name < mgmt.Name {
				mgmt = g
			}
		}
	}
	if mgmt != nil {
		base := mgmt.Name
		if i := strings.LastIndex(base, "-mgmt-"); i >= 0 {
			base = base[:i+6]
			walkDiscovery(data, func(m map[string]any) {
				rg := discoveryString(m, "resourceGroup")
				if !strings.HasPrefix(rg, base) {
					return
				}
				if n, err := strconv.Atoi(strings.TrimPrefix(rg, base)); err == nil && n > 0 {
					owner := "Management Cluster " + strconv.Itoa(n)
					sub := discoveryString(m, "subscriptionID")
					if strings.EqualFold(rg, mgmt.Name) {
						return // Config already supplies this stamp's exact node RG.
					}
					// Additional stamps can live in a different subscription.
					d.group(sub, rg, "Infra", owner, "primary")
					d.group(sub, rg+"-aks1", "Infra", owner, "aks-managed")
				}
			})
		}
	}
	walkDiscovery(data, func(m map[string]any) {
		if !strings.EqualFold(discoveryString(m, "resourceType"), "Microsoft.ContainerService/managedClusters") {
			return
		}
		rg := discoveryString(m, "resourceGroup")
		for _, g := range d.groups {
			if g.Category != "Infra" || g.Kind != "primary" || g.Owner == "Regional" || !strings.EqualFold(g.Name, rg) {
				continue
			}
			nodes := ""
			for _, candidate := range d.groups {
				if candidate.Category == "Infra" && candidate.Kind == "aks-managed" && candidate.Owner == g.Owner {
					if nodes != "" && !strings.EqualFold(nodes, candidate.Name) {
						nodes = ""
						break
					}
					nodes = candidate.Name
				}
			}
			d.infraBinding(infraBinding{rg: g.Name, cluster: discoveryString(m, "name"), owner: g.Owner, nodeRG: nodes})
		}
	})
	d.inventory(data, "", "")
}

func (d *discovery) testOutput(owner, output string) {
	t := d.test(owner)
	lastCluster := ""
	for _, line := range strings.Split(output, "\n") {
		kv := map[string]string{}
		for _, pair := range discoveryKV.FindAllStringSubmatch(line, -1) {
			value := pair[2]
			if strings.HasPrefix(value, "\"") {
				value, _ = strconv.Unquote(value)
			}
			kv[pair[1]] = value
		}
		msg := kv["msg"]
		if msg == "creating resource group" {
			d.group(t.sub, kv["resourceGroup"], "Tests", owner, "customer")
		}
		if msg == "Starting HCP cluster creation" || strings.HasPrefix(msg, "Starting HCP cluster creation (v") {
			lastCluster = kv["resourceGroup"] + "/" + kv["clusterName"]
			t.clusters[lastCluster] = true
		}
		if msg == "starting oc adm inspect for HCP cluster" {
			cluster := kv["cluster"]
			if parts := strings.Split(cluster, "/"); len(parts) == 2 && discoveryRG.MatchString(parts[0]) && discoveryRG.MatchString(parts[1]) {
				t.clusters[cluster] = true
				t.accepted[cluster] = true
			}
		}
		// This framework message comes from node-pool boot-diagnostic collection.
		// Require an accepted cluster and a single-cluster test before using it.
		if msg == "no VMs found in resource group" && lastCluster != "" {
			t.emptyVMs[lastCluster] = kv["resourceGroup"]
		}
		if msg == "cluster deployment error" {
			d.rejectedCreation(owner, kv["error"])
		}
		managed := []string{kv["managedRG"], kv["managedResourceGroupName"]}
		if params := kv["clusterParams"]; params != "" {
			var data any
			if json.Unmarshal([]byte(params), &data) == nil {
				d.managedMetadata(owner, data)
			}
		}
		for _, key := range []string{"managedResourceGroups", "remaining"} {
			if key == "remaining" && (!strings.Contains(strings.ToLower(msg), "managed") || (!strings.Contains(msg, "delet") && !strings.Contains(msg, "cleanup") && !strings.Contains(msg, "waiting"))) {
				continue
			}
			var values []string
			if json.Unmarshal([]byte(kv[key]), &values) == nil {
				managed = append(managed, values...)
			}
		}
		if msg == "deleting managed resource group" {
			managed = append(managed, kv["resourceGroup"])
		}
		if msg == "derived DNS zone" {
			sub, rg, _, _, _ := discoveryResourceID(kv["resourceID"], t.sub)
			if sub == "" || strings.EqualFold(sub, t.sub) {
				managed = append(managed, rg)
			} else {
				d.snapshot.Diagnose("error", "managed-subscription-conflict", owner, "Managed DNS resource ID does not match this test's timing subscription")
			}
		}
		for _, rg := range managed {
			if g := d.group(t.sub, rg, "Tests", owner, "hcp-managed"); g != nil && g.Owner == owner {
				t.managed[strings.ToLower(rg)] = true
			}
		}
		if msg == "derived DNS zone" {
			d.inventory(map[string]any{"id": kv["resourceID"]}, t.sub, owner)
		}
	}
}

func (d *discovery) managedMetadata(owner string, data any) {
	t := d.test(owner)
	walkDiscovery(data, func(m map[string]any) {
		for _, key := range []string{"managedResourceGroupName", "managedResourceGroup"} {
			value := m[key]
			if param, ok := value.(map[string]any); ok {
				value = param["value"]
			}
			rg, _ := value.(string)
			if strings.HasPrefix(strings.ToLower(rg), "/subscriptions/") {
				var sub string
				sub, rg, _, _, _ = discoveryResourceID(rg, t.sub)
				if sub != "" && !strings.EqualFold(sub, t.sub) {
					d.snapshot.Diagnose("error", "managed-subscription-conflict", owner, "Managed resource group ID does not match this test's timing subscription")
					continue
				}
			}
			if g := d.group(t.sub, rg, "Tests", owner, "hcp-managed"); g != nil && g.Owner == owner {
				t.managed[strings.ToLower(rg)] = true
			}
		}
	})
}

// These are the two shipped artifact-directory encodings, not fuzzy matches.
func discoveryTestDirectory(name string, snapshot bool) string {
	name = strings.Map(func(r rune) rune {
		if snapshot {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
				return r
			}
			return '_'
		}
		if strings.ContainsRune(" /\\:*?\"<>|", r) {
			return '_'
		}
		return r
	}, name)
	if !snapshot && len(name) > 200 {
		name = name[:200]
	}
	return name
}

func (d *discovery) directoryOwner(directory string, snapshot bool) string {
	owner := ""
	for name := range d.tests {
		if discoveryTestDirectory(name, snapshot) != directory {
			continue
		}
		if owner != "" {
			return ""
		}
		owner = name
	}
	return owner
}

func (d *discovery) clusterMapping(owner, cluster, rg string) {
	t := d.test(owner)
	parent := strings.Split(cluster, "/")[0]
	g := d.groups[strings.ToLower(t.sub+"/"+parent)]
	if g == nil || g.Owner != owner || g.Kind != "customer" {
		return
	}
	if managed := d.group(t.sub, rg, "Tests", owner, "hcp-managed"); managed != nil && managed.Owner == owner && managed.Kind == "hcp-managed" {
		t.managed[strings.ToLower(rg)], t.mapped[cluster] = true, true
		t.accepted[cluster] = true
	}
}

var discoveryClusterPUT = regexp.MustCompile(`(?im)(?:^|\n)\s*PUT https://[^\s]+(/subscriptions/[^/\s]+/resourcegroups/[^/\s]+/providers/microsoft\.redhatopenshift/hcpopenshiftclusters/[^/?\s]+)(?:\?[^\s]*)?\s*\n`)
var discoveryRejectedPUT = regexp.MustCompile(`(?m)^RESPONSE (400|403|409|422):`)
var discoveryAcceptedPUT = regexp.MustCompile(`(?m)^\s*RESPONSE Status: (200|201|202) `)

func (d *discovery) acceptedCreation(owner, message string) {
	match := discoveryClusterPUT.FindStringSubmatch(message)
	if match == nil || !discoveryAcceptedPUT.MatchString(message) {
		return
	}
	t := d.test(owner)
	sub, rg, cluster, typ, _ := discoveryResourceID(match[1], t.sub)
	if !strings.EqualFold(typ, "Microsoft.RedHatOpenShift/hcpOpenShiftClusters") || sub != "" && !strings.EqualFold(sub, t.sub) {
		return
	}
	for key := range t.clusters {
		if strings.EqualFold(key, rg+"/"+cluster) {
			t.accepted[key] = true
		}
	}
}

func (d *discovery) rejectedCreation(owner, message string) {
	match := discoveryClusterPUT.FindStringSubmatch(message)
	if match == nil || !discoveryRejectedPUT.MatchString(message) {
		return
	}
	t := d.test(owner)
	sub, rg, cluster, typ, _ := discoveryResourceID(match[1], t.sub)
	if !strings.EqualFold(typ, "Microsoft.RedHatOpenShift/hcpOpenShiftClusters") || sub != "" && !strings.EqualFold(sub, t.sub) {
		return
	}
	for key := range t.clusters {
		if strings.EqualFold(key, rg+"/"+cluster) {
			t.rejected[key] = true
		}
	}
}

func (d *discovery) snapshotMapping(owner, cluster string, body []byte, artifact string) {
	t := d.test(owner)
	for _, line := range bytes.Split(body, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("| {")) || !bytes.HasSuffix(line, []byte("|")) {
			continue
		}
		var row map[string]any
		if json.Unmarshal(bytes.TrimSpace(line[1:len(line)-1]), &row) != nil {
			d.snapshot.Diagnose("error", "artifact-format", artifact, "Invalid JSON in backend resource-state table")
			continue
		}
		id := discoveryString(row, "resourceID")
		if id == "" {
			id = discoveryString(row, "id")
		}
		sub, rg, name, typ, _ := discoveryResourceID(id, t.sub)
		if !strings.EqualFold(typ, "Microsoft.RedHatOpenShift/hcpOpenShiftClusters") || !strings.EqualFold(rg+"/"+name, cluster) {
			continue
		}
		if sub != "" && !strings.EqualFold(sub, t.sub) {
			continue
		}
		properties := discoveryMap(row, "properties")
		managed := discoveryString(discoveryMap(properties, "customerProperties", "platform"), "managedResourceGroup")
		if managed == "" {
			managed = discoveryString(discoveryMap(properties, "platform"), "managedResourceGroup")
		}
		d.clusterMapping(owner, cluster, managed)
		if t.mapped[cluster] {
			d.inventory(map[string]any{"id": id}, t.sub, owner)
			return
		}
	}
}

// Resource IDs are repaired only with the subscription of the same test, never
// with an infra subscription or a subscription found in an unrelated document.
func discoveryResourceID(id, testSub string) (sub, rg, name, typ, repaired string) {
	parts := strings.Split(strings.Trim(id, "/"), "/")
	if len(parts) < 4 || !strings.EqualFold(parts[0], "subscriptions") || !strings.EqualFold(parts[2], "resourceGroups") {
		return
	}
	sub, rg = parts[1], parts[3]
	if discoveryRedacted.MatchString(sub) && discoveryUUID.MatchString(testSub) {
		sub = testSub
		parts[1] = testSub
	}
	if !discoveryUUID.MatchString(sub) {
		sub = ""
	}
	if len(parts) < 8 || !strings.EqualFold(parts[4], "providers") {
		return
	}
	// The final provider segment identifies extension resources such as RBAC.
	start := 4
	for i := 4; i+3 < len(parts); i++ {
		if strings.EqualFold(parts[i], "providers") {
			start = i
		}
	}
	if (len(parts)-start)%2 != 0 {
		return sub, rg, "", "", ""
	}
	types, names := []string{parts[start+1]}, []string{}
	for i := start + 2; i+1 < len(parts); i += 2 {
		types = append(types, parts[i])
		names = append(names, parts[i+1])
	}
	typ, name = strings.Join(types, "/"), strings.Join(names, "/")
	if sub != "" {
		repaired = "/" + strings.Join(parts, "/")
	}
	return
}

func (d *discovery) inventory(data any, testSub, owner string) {
	walkDiscovery(data, func(m map[string]any) {
		id := discoveryString(m, "id")
		if id == "" {
			id = discoveryString(m, "resourceId")
		}
		if id == "" {
			id = discoveryString(m, "resourceID")
		}
		sub, rg, name, typ, id := discoveryResourceID(id, testSub)
		if rg == "" {
			rg, name, typ = discoveryString(m, "resourceGroup"), discoveryString(m, "name"), discoveryString(m, "resourceType")
			sub = discoveryString(m, "subscriptionID")
			if sub == "" && owner != "" {
				sub = testSub
			}
		}
		if rg == "" || name == "" || typ == "" || d.shared(rg) {
			return
		}
		if !discoveryUUID.MatchString(sub) {
			sub = ""
		}
		g := d.groups[strings.ToLower(sub+"/"+rg)]
		if g == nil && owner == "" && sub == "" {
			for _, candidate := range d.groups {
				if candidate.Category == "Infra" && strings.EqualFold(candidate.Name, rg) {
					if g != nil {
						return
					}
					g = candidate
				}
			}
		}
		if g == nil || owner != "" && g.Owner != owner {
			return
		}
		if id == "" && discoveryUUID.MatchString(g.SubscriptionID) {
			types, names := strings.Split(typ, "/"), strings.Split(name, "/")
			if len(types) == len(names)+1 {
				id = "/subscriptions/" + g.SubscriptionID + "/resourceGroups/" + g.Name + "/providers/" + types[0]
				for i := range names {
					id += "/" + types[i+1] + "/" + names[i]
				}
			}
		}
		for _, r := range g.Resources {
			if id != "" && strings.EqualFold(r.ID, id) || id == "" && strings.EqualFold(r.Name, name) && strings.EqualFold(r.Type, typ) {
				return
			}
		}
		g.Resources = append(g.Resources, Resource{ID: id, Name: name, Type: typ, Source: "artifact"})
	})
}
