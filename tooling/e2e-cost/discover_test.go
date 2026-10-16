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
	"os"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

const discoveryTestSub = "974ebd46-8ad3-41e3-afef-7ef25fd5c371"
const discoveryTestPrefix = "pr-logs/pull/Azure_ARO-HCP/6976/" + discoveryJob + "/2100537004893671424/"
const discoveryTestURL = "https://prow.ci.openshift.org/view/gs/" + discoveryBucket + "/" + discoveryTestPrefix

type discoveryTransport func(*http.Request) (*http.Response, error)

func (f discoveryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Each page has at most three objects, so every complete fixture exercises pagination.
func discoveryClient(t *testing.T, objects map[string][]byte) *http.Client {
	t.Helper()
	var names []string
	for name := range objects {
		names = append(names, discoveryTestPrefix+name)
	}
	sort.Strings(names)
	return &http.Client{Transport: discoveryTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "storage.googleapis.com" {
			t.Fatalf("unexpected network host: %s", r.URL.Host)
		}
		status := http.StatusOK
		var body []byte
		if r.URL.Path == "/storage/v1/b/"+discoveryBucket+"/o" {
			if r.URL.Query().Get("prefix") != discoveryTestPrefix {
				t.Fatalf("wrong listing prefix: %s", r.URL.RawQuery)
			}
			offset := 0
			if token := r.URL.Query().Get("pageToken"); token != "" {
				if _, err := fmt.Sscanf(token, "page-%d", &offset); err != nil {
					t.Fatal(err)
				}
			}
			listing := map[string]any{}
			var items []map[string]string
			end := min(offset+3, len(names))
			for _, name := range names[offset:end] {
				items = append(items, map[string]string{"name": name})
			}
			listing["items"] = items
			if end < len(names) {
				listing["nextPageToken"] = fmt.Sprintf("page-%d", end)
			}
			body, _ = json.Marshal(listing)
		} else {
			name := strings.TrimPrefix(r.URL.Path, "/"+discoveryBucket+"/"+discoveryTestPrefix)
			var ok bool
			body, ok = objects[name]
			if !ok {
				status = http.StatusNotFound
				body = []byte(strings.Repeat("private error body", 1000))
			}
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
	})}
}

func discoveryFixture() map[string][]byte {
	return map[string][]byte{
		"started.json":                       []byte(`{"timestamp":1789642022}`),
		"finished.json":                      []byte(`{"timestamp":1789649185,"result":"SUCCESS"}`),
		"prowjob.json":                       []byte(`{"spec":{"refs":{"org":"Azure","repo":"ARO-HCP","pulls":[{"number":6976,"sha":"3f37770fba22a55b4336e3a85cbf275bf37c8279"}]}}}`),
		"artifacts/ci-operator-metrics.json": []byte(`{"pods":[{"pod_name":"e2e-parallel-aro-hcp-provision-environment","start_time":"2026-09-17T10:59:44Z"},{"pod_name":"e2e-parallel-aro-hcp-deprovision-tracked-resource-groups","completion_time":"2026-09-17T12:34:16Z"},{"pod_name":"e2e-parallel-aro-hcp-deprovision-environment","completion_time":"2026-09-17T12:44:41Z"}]}`),
		"artifacts/e2e-parallel/aro-hcp-provision-environment/artifacts/config.yaml":      []byte("regionRG: run\nsvc:\n  rg: run-svc\n  subscription: {key: XXXXXXXXXXXXXXXXXXXX}\nmgmt:\n  rg: run-mgmt-1\n  subscription: {key: REDACTED}\nglobal: {rg: global}\nkusto: {rg: shared-kusto}\n"),
		"artifacts/e2e-parallel/aro-hcp-gather-visualization/artifacts/timing/steps.yaml": []byte("- details:\n    arm:\n      operations:\n      - resource: {resourceGroup: run-mgmt-2, name: aks, resourceType: Microsoft.ContainerService/managedClusters}\n      - resource: {resourceGroup: global, name: shared, resourceType: Microsoft.Storage/storageAccounts}\n"),
		testArtifactPrefix + "identities-pool-state.yaml":                                 []byte("- resourceGroup: aro-hcp-msi-container-dev-shard0-02-01\n- resourceGroup: arbitrary-shared-pool\n"),
		testArtifactPrefix + "test-timing/one.yaml":                                       []byte("identifier: [Customer, creates clusters]\nsubscriptionID: " + discoveryTestSub + "\n"),
	}
}

func discoveryResults(t *testing.T, objects map[string][]byte, results ...map[string]any) {
	t.Helper()
	body, err := json.Marshal(results)
	if err != nil {
		t.Fatal(err)
	}
	objects[testArtifactPrefix+"extension_test_result_e2e_20260917.json"] = body
}

func discoveryNow() time.Time {
	return time.Date(2026, 9, 19, 1, 0, 0, 0, time.FixedZone("offset", 2*3600))
}

func findDiscoveryGroup(t *testing.T, s *Snapshot, name string) Group {
	t.Helper()
	for _, g := range s.Groups {
		if strings.EqualFold(g.Name, name) {
			return g
		}
	}
	t.Fatalf("missing group %s; groups=%+v diagnostics=%+v", name, s.Groups, s.Diagnostics)
	return Group{}
}

func hasDiscoveryDiagnostic(s *Snapshot, code string) bool {
	for _, d := range s.Diagnostics {
		if d.Code == code && d.Severity == "error" {
			return true
		}
	}
	return false
}

func TestDiscoverOwnershipInventoryAndGzip(t *testing.T) {
	objects := discoveryFixture()
	var compressed bytes.Buffer
	w := gzip.NewWriter(&compressed)
	_, _ = w.Write([]byte("identifier: [Customer, creates clusters]\nsubscriptionID: " + discoveryTestSub + `
deployments:
  customer:
    infra:
    - resource: {resourceGroup: Customer-RG, name: vnet, resourceType: Microsoft.Network/virtualNetworks}
    - resource: {resourceGroup: arbitrary-shared-pool, name: service, resourceType: Microsoft.ManagedIdentity/userAssignedIdentities}
    - resource: {resourceGroup: unowned, name: unknown, resourceType: Microsoft.Storage/storageAccounts}
`))
	_ = w.Close()
	objects[testArtifactPrefix+"test-timing/one.yaml"] = compressed.Bytes()
	discoveryResults(t, objects,
		map[string]any{"name": "Customer creates clusters", "result": "failed", "output": `"msg"="creating resource group" "resourceGroup"="Customer-RG"
"msg"="Starting HCP cluster creation" "resourceGroup"="Customer-RG" "clusterName"="one"
"msg"="Found private KAS internal LB" "managedRG"="exact-managed-one"
"msg"="waiting for managed resource group cleanup" "remaining"=["exact-managed-two","exact-managed-three"]
"msg"="not cleanup" "remaining"=["unowned"]
"msg"="collecting deployments" "resourceGroup"="directory-only"
"msg"="creating resource group" "resourceGroup"="arbitrary-shared-pool"
"msg"="creating resource group" "resourceGroup"="global"
"msg"="derived DNS zone" "resourceGroup"="dns-managed" "resourceID"="/subscriptions/XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX/resourceGroups/dns-managed/providers/Microsoft.Network/dnsZones/test.example.com"
`},
		map[string]any{"name": "Customer creates clusters", "result": "passed", "output": `"msg"="creating resource group" "resourceGroup"="customer-rg"`})
	objects[testArtifactPrefix+"resourcegroups/Customer-RG/deployments.yaml"] = []byte(`- properties:
    parameters:
      managedResourceGroupName: {value: fourth-managed}
    outputResources:
    - id: /subscriptions/XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX/resourceGroups/Customer-RG/providers/Microsoft.Network/virtualNetworks/vnet
    - id: /subscriptions/XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX/resourceGroups/arbitrary-shared-pool/providers/Microsoft.ManagedIdentity/userAssignedIdentities/service
`)
	objects[testArtifactPrefix+"resourcegroups/directory-only/deployments.yaml"] = []byte(`- id: /subscriptions/` + discoveryTestSub + `/resourceGroups/directory-only/providers/Microsoft.Compute/virtualMachines/not-owned`)
	s := Discover(context.Background(), discoveryClient(t, objects), discoveryTestURL, discoveryNow())
	if s.QueryStart != "2026-09-17" || s.QueryEnd != "2026-09-18" {
		t.Fatalf("wrong frozen days: %s %s", s.QueryStart, s.QueryEnd)
	}
	if s.Job.Commit != "3f37770fba22a55b4336e3a85cbf275bf37c8279" || s.Job.Result != "SUCCESS" {
		t.Fatalf("wrong job metadata: %+v", s.Job)
	}
	if s.Job.InfraStartedAt.Format(time.RFC3339) != "2026-09-17T10:59:44Z" || s.Job.InfraEndedAt.Format(time.RFC3339) != "2026-09-17T12:44:41Z" {
		t.Fatalf("wrong infra interval: %+v", s.Job)
	}
	g := findDiscoveryGroup(t, s, "customer-rg")
	if g.Owner != "Customer creates clusters" || g.SubscriptionID != discoveryTestSub || g.Attempts != 2 || strings.Join(g.Outcomes, ",") != "failed,passed" {
		t.Fatalf("wrong test owner/attempts: %+v", g)
	}
	if len(g.Resources) != 1 || g.Resources[0].ID != "/subscriptions/"+discoveryTestSub+"/resourceGroups/Customer-RG/providers/Microsoft.Network/virtualNetworks/vnet" || g.Resources[0].Source != "artifact" || len(g.Resources[0].Charges) != 0 {
		t.Fatalf("wrong inventory: %+v", g.Resources)
	}
	for _, name := range []string{"exact-managed-one", "exact-managed-two", "exact-managed-three", "fourth-managed", "dns-managed"} {
		if g := findDiscoveryGroup(t, s, name); g.Kind != "hcp-managed" || g.SubscriptionID != discoveryTestSub {
			t.Fatalf("wrong managed owner: %+v", g)
		}
	}
	for _, g := range s.Groups {
		if g.Name == "global" || g.Name == "directory-only" || g.Name == "unowned" || g.Name == "arbitrary-shared-pool" {
			t.Fatalf("shared/referenced group acquired ownership: %+v", g)
		}
	}
	for _, name := range []string{"run", "run-svc", "run-svc-aks1", "run-mgmt-1", "run-mgmt-1-aks1", "run-mgmt-2", "run-mgmt-2-aks1"} {
		if g := findDiscoveryGroup(t, s, name); g.SubscriptionID != "" || g.BillingStatus != "unavailable" {
			t.Fatalf("redacted infra inferred subscription: %+v", g)
		}
	}
	if findDiscoveryGroup(t, s, "run").Owner != "Regional" || findDiscoveryGroup(t, s, "run-mgmt-2").Owner != "Management Cluster 2" {
		t.Fatal("incorrect infra top owner")
	}
	if !hasDiscoveryDiagnostic(s, "missing-infra-subscription") {
		t.Fatal("missing infra subscription error")
	}
	if hasDiscoveryDiagnostic(s, "missing-managed-mapping") {
		t.Fatalf("unexpected missing mapping: %+v", s.Diagnostics)
	}
}

func TestDiscoverMissingSubscriptionAndManagedMapping(t *testing.T) {
	objects := discoveryFixture()
	objects[testArtifactPrefix+"test-timing/one.yaml"] = []byte("identifier: [Customer, creates clusters]\nsubscriptionID: XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX\n")
	// The gathered copy must not repair the original subscription.
	objects["artifacts/e2e-parallel/aro-hcp-gather-test-visualization/artifacts/test-timing/one.yaml"] = []byte("identifier: [Customer, creates clusters]\nsubscriptionID: " + discoveryTestSub + "\n")
	discoveryResults(t, objects, map[string]any{"name": "Customer creates clusters", "result": "passed", "output": `"msg"="creating resource group" "resourceGroup"="test-rg"
"msg"="Starting HCP cluster creation" "resourceGroup"="test-rg" "clusterName"="one"`})
	s := Discover(context.Background(), discoveryClient(t, objects), discoveryTestURL, discoveryNow())
	g := findDiscoveryGroup(t, s, "test-rg")
	if g.SubscriptionID != "" || g.BillingStatus != "unavailable" {
		t.Fatalf("subscription inferred: %+v", g)
	}
	if !hasDiscoveryDiagnostic(s, "missing-test-subscription") || !hasDiscoveryDiagnostic(s, "missing-managed-mapping") {
		t.Fatalf("missing errors: %+v", s.Diagnostics)
	}
	for _, g := range s.Groups {
		if strings.HasPrefix(g.Name, "test-rg-") {
			t.Fatalf("guessed managed RG: %+v", g)
		}
	}
}

func TestDiscoverOwnershipConflict(t *testing.T) {
	objects := discoveryFixture()
	objects[testArtifactPrefix+"test-timing/two.yaml"] = []byte("identifier: [Other, test]\nsubscriptionID: " + strings.ToUpper(discoveryTestSub) + "\n")
	discoveryResults(t, objects,
		map[string]any{"name": "Customer creates clusters", "result": "passed", "output": `"msg"="creating resource group" "resourceGroup"="same-RG"`},
		map[string]any{"name": "Other test", "result": "passed", "output": `"msg"="creating resource group" "resourceGroup"="SAME-rg"`})
	s := Discover(context.Background(), discoveryClient(t, objects), discoveryTestURL, discoveryNow())
	count := 0
	for _, g := range s.Groups {
		if strings.EqualFold(g.Name, "same-rg") {
			count++
			if g.BillingStatus != "unavailable" {
				t.Fatal("conflict still billable")
			}
		}
	}
	if count != 1 || !hasDiscoveryDiagnostic(s, "ownership-conflict") {
		t.Fatalf("conflict double-counted or not diagnosed: %+v", s)
	}
}

func TestDiscoverMalformedArtifactsRetainPartialGroups(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
		code string
	}{
		{"malformed-yaml", []byte("identifier: [unterminated"), "artifact-format"},
		{"corrupt-gzip", []byte{0x1f, 0x8b, 0x00, 0x01}, "artifact-read"},
		{"wrong-identifier", []byte("identifier: not-an-array\nsubscriptionID: " + discoveryTestSub), "timing-identifier"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objects := discoveryFixture()
			objects[testArtifactPrefix+"test-timing/broken.yaml"] = tc.body
			discoveryResults(t, objects, map[string]any{"name": "Customer creates clusters", "result": "passed", "output": `"msg"="creating resource group" "resourceGroup"="retained"`})
			s := Discover(context.Background(), discoveryClient(t, objects), discoveryTestURL, discoveryNow())
			findDiscoveryGroup(t, s, "retained")
			if !hasDiscoveryDiagnostic(s, tc.code) {
				t.Fatalf("missing %s: %+v", tc.code, s.Diagnostics)
			}
		})
	}
}

func TestDiscoverURLValidation(t *testing.T) {
	for _, input := range []string{"", "https://example.com/" + discoveryTestPrefix, strings.Replace(discoveryTestURL, "e2e-parallel", "e2e", 1), discoveryTestURL + "?foo=bar", discoveryTestURL + "../", strings.Replace(discoveryTestURL, discoveryBucket, "private-bucket", 1)} {
		t.Run(input, func(t *testing.T) {
			client := &http.Client{Transport: discoveryTransport(func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid URL performed network request")
				return nil, nil
			})}
			s := Discover(context.Background(), client, input, discoveryNow())
			if !hasDiscoveryDiagnostic(s, "invalid-job-url") {
				t.Fatalf("accepted %q", input)
			}
		})
	}
}

func TestDiscoveryNetworkFailuresAndPaginationLoop(t *testing.T) {
	client := &http.Client{Transport: discoveryTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"nextPageToken":"repeat","items":[{"name":"` + discoveryTestPrefix + `started.json"}]}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	names, err := listDiscoveryArtifacts(context.Background(), client, discoveryTestPrefix)
	if err == nil || !strings.Contains(err.Error(), "repeated") || len(names) != 1 {
		t.Fatalf("pagination loop: names=%v err=%v", names, err)
	}
	client.Transport = discoveryTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader(strings.Repeat("secret", 10000)))}, nil
	})
	_, err = getDiscoveryArtifact(context.Background(), client, "https://storage.googleapis.com/object")
	if err == nil || strings.Contains(err.Error(), "secret") || len(err.Error()) > 100 {
		t.Fatalf("unsafe HTTP diagnostic: %v", err)
	}
}

func TestDiscoveryResourceIDs(t *testing.T) {
	for _, tc := range []struct{ id, sub, want string }{
		{"/subscriptions/XXXX/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet", discoveryTestSub, "/subscriptions/" + discoveryTestSub + "/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet"},
		{"/subscriptions/XXXX/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet", "", ""},
		{"/subscriptions/not-a-uuid/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet", discoveryTestSub, ""},
	} {
		_, _, _, _, got := discoveryResourceID(tc.id, tc.sub)
		if got != tc.want {
			t.Errorf("repair %q: got %q want %q", tc.id, got, tc.want)
		}
	}
	_, _, name, typ, _ := discoveryResourceID("/subscriptions/"+discoveryTestSub+"/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/default/providers/Microsoft.Authorization/roleAssignments/role", "")
	if name != "role" || typ != "Microsoft.Authorization/roleAssignments" {
		t.Fatalf("wrong extension resource metadata: %s %s", name, typ)
	}
}

func TestDiscoverSixtySharedGroupsAndClusterParameters(t *testing.T) {
	objects := discoveryFixture()
	excluded := []string{"global", "shared-kusto"}
	var pool strings.Builder
	var deployments strings.Builder
	for i := 0; i < 60; i++ {
		rg := fmt.Sprintf("aro-hcp-msi-container-dev-shard0-02-%02d", i)
		excluded = append(excluded, rg)
		fmt.Fprintf(&pool, "- resourceGroup: %s\n", rg)
		fmt.Fprintf(&deployments, "- id: /subscriptions/XXXX/resourceGroups/%s/providers/Microsoft.ManagedIdentity/userAssignedIdentities/service\n", rg)
		objects[testArtifactPrefix+"resourcegroups/"+rg+"/deployments.yaml"] = []byte("[]")
	}
	objects[testArtifactPrefix+"identities-pool-state.yaml"] = []byte(pool.String())
	objects[testArtifactPrefix+"resourcegroups/test-rg/deployments.yaml"] = []byte(deployments.String())
	objects[testArtifactPrefix+"Customer_creates_clusters/cluster-params.json"] = []byte(`{"managedResourceGroupName":"nonconventional-managed-name"}`)
	discoveryResults(t, objects, map[string]any{"name": "Customer creates clusters", "result": "passed", "output": `"msg"="creating resource group" "resourceGroup"="test-rg"
"msg"="Starting HCP cluster creation" "resourceGroup"="test-rg" "clusterName"="one"`})
	s := Discover(context.Background(), discoveryClient(t, objects), discoveryTestURL, discoveryNow())
	sort.Strings(excluded)
	if !slices.Equal(s.ExcludedGroups, excluded) {
		t.Fatalf("incorrect explicit shared exclusions: got %v want %v", s.ExcludedGroups, excluded)
	}
	if len(s.Groups) != 9 {
		t.Fatalf("expected seven infra and two test groups, got %d: %+v", len(s.Groups), s.Groups)
	}
	if g := findDiscoveryGroup(t, s, "nonconventional-managed-name"); g.Kind != "hcp-managed" {
		t.Fatalf("missing cluster parameter mapping: %+v", g)
	}
	if hasDiscoveryDiagnostic(s, "missing-managed-mapping") {
		t.Fatalf("mapping not recognized: %+v", s.Diagnostics)
	}
}

func TestDiscoverParameterDirectoryOwnership(t *testing.T) {
	longName := strings.Repeat("a", 200)
	for _, tc := range []struct {
		name, directory string
		owners          []string
		wantOwner       string
	}{
		{"punctuation", "Customer_creates__cluster__", []string{"Customer creates: cluster?*"}, "Customer creates: cluster?*"},
		{"punctuation collision", "Customer_creates_cluster", []string{"Customer creates:cluster", "Customer creates?cluster"}, ""},
		{"truncation", longName, []string{longName + " suffix"}, longName + " suffix"},
		{"truncation collision", longName, []string{longName + " first", longName + " second"}, ""},
		{"noncanonical directory", "Customer_creates:cluster", []string{"Customer creates:cluster"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objects := discoveryFixture()
			delete(objects, testArtifactPrefix+"test-timing/one.yaml")
			var results []map[string]any
			for i, owner := range tc.owners {
				// JSON is also valid YAML, with the exact identifier preserved.
				objects[fmt.Sprintf("%stest-timing/%d.yaml", testArtifactPrefix, i)], _ = json.Marshal(map[string]any{"identifier": []string{owner}, "subscriptionID": discoveryTestSub})
				results = append(results, map[string]any{"name": owner, "result": "passed", "output": ""})
			}
			discoveryResults(t, objects, results...)
			objects[testArtifactPrefix+tc.directory+"/cluster-params.json"] = []byte(`{"managedResourceGroupName":"exact-managed"}`)
			s := Discover(context.Background(), discoveryClient(t, objects), discoveryTestURL, discoveryNow())
			gotOwner := ""
			for _, g := range s.Groups {
				if g.Name == "exact-managed" {
					gotOwner = g.Owner
				}
			}
			if gotOwner != tc.wantOwner {
				t.Fatalf("parameter owner: got %q want %q", gotOwner, tc.wantOwner)
			}
		})
	}
}

func TestDiscoverExportsExclusionsOnPartialAndEarlyReturn(t *testing.T) {
	objects := discoveryFixture()
	objects[testArtifactPrefix+"identities-pool-state.yaml"] = []byte("- resourceGroup: MIXED-Shared\n- resourceGroup: mixed-shared\n")
	objects[testArtifactPrefix+"test-timing/one.yaml"] = []byte("identifier: [broken")
	s := Discover(context.Background(), discoveryClient(t, objects), discoveryTestURL, discoveryNow())
	if !slices.Equal(s.ExcludedGroups, []string{"global", "mixed-shared", "shared-kusto"}) || !hasDiscoveryDiagnostic(s, "artifact-format") {
		t.Fatalf("partial discovery lost sorted, normalized exclusions: %+v", s)
	}
	s = Discover(context.Background(), nil, "invalid", discoveryNow())
	if !slices.Equal(s.ExcludedGroups, []string{"global"}) {
		t.Fatalf("early return lost known exclusions: %v", s.ExcludedGroups)
	}
}

func TestDiscoverConflictingTimingSubscriptions(t *testing.T) {
	objects := discoveryFixture()
	objects[testArtifactPrefix+"test-timing/duplicate.yaml"] = []byte("identifier: [Customer, creates clusters]\nsubscriptionID: 11111111-1111-1111-1111-111111111111\n")
	discoveryResults(t, objects, map[string]any{"name": "Customer creates clusters", "result": "passed", "output": `"msg"="creating resource group" "resourceGroup"="test-rg"`})
	s := Discover(context.Background(), discoveryClient(t, objects), discoveryTestURL, discoveryNow())
	if g := findDiscoveryGroup(t, s, "test-rg"); g.SubscriptionID != "" || g.BillingStatus != "unavailable" {
		t.Fatalf("conflicting subscription chosen: %+v", g)
	}
	if !hasDiscoveryDiagnostic(s, "test-subscription-conflict") {
		t.Fatal("missing conflicting timing diagnostic")
	}
}

func TestDiscoverValidInfraSubscriptionAndExactAKSGroup(t *testing.T) {
	objects := discoveryFixture()
	objects["artifacts/e2e-parallel/aro-hcp-provision-environment/artifacts/config.yaml"] = []byte("regionRG: run\nsvc:\n  rg: run-svc\n  subscription: {key: " + discoveryTestSub + "}\n  aks: {nodeResourceGroup: exact-aks-nodes}\nmgmt:\n  rg: run-mgmt-1\n  subscription: {key: friendly-name-not-an-ID}\n")
	discoveryResults(t, objects, map[string]any{"name": "Customer creates clusters", "result": "skipped", "output": ""})
	s := Discover(context.Background(), discoveryClient(t, objects), "gs://"+discoveryBucket+"/"+discoveryTestPrefix, discoveryNow())
	if g := findDiscoveryGroup(t, s, "exact-aks-nodes"); g.SubscriptionID != discoveryTestSub || g.Kind != "aks-managed" {
		t.Fatalf("exact AKS group not honored: %+v", g)
	}
	for _, g := range s.Groups {
		if g.Name == "run-svc-aks1" {
			t.Fatal("historical fallback retained despite exact AKS group")
		}
	}
	if g := findDiscoveryGroup(t, s, "run-mgmt-1"); g.SubscriptionID != "" || g.BillingStatus != "unavailable" {
		t.Fatalf("display-name lookup inferred: %+v", g)
	}
}

func TestDiscoverMissingRootsAndMalformedJSON(t *testing.T) {
	objects := discoveryFixture()
	delete(objects, "started.json")
	objects["finished.json"] = []byte(`{"timestamp":"invalid"}`)
	objects[testArtifactPrefix+"extension_test_result_e2e_bad.json"] = []byte(`[{invalid`)
	s := Discover(context.Background(), discoveryClient(t, objects), discoveryTestURL, discoveryNow())
	findDiscoveryGroup(t, s, "run")
	if s.QueryStart != "" || !s.Job.StartedAt.IsZero() || !s.Job.FinishedAt.IsZero() {
		t.Fatalf("invented timestamp: %+v", s.Job)
	}
	if !hasDiscoveryDiagnostic(s, "job-timestamp") || !hasDiscoveryDiagnostic(s, "artifact-format") || !hasDiscoveryDiagnostic(s, "missing-artifact") {
		t.Fatalf("missing artifact errors: %+v", s.Diagnostics)
	}
}

func TestDiscoverLive(t *testing.T) {
	if os.Getenv("E2E_COST_LIVE_DISCOVERY") != "1" {
		t.Skip("set E2E_COST_LIVE_DISCOVERY=1 for public artifact smoke test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	s := Discover(ctx, nil, discoveryTestURL, time.Now())
	counts := map[string]int{}
	managedOwners := map[string]bool{}
	for _, g := range s.Groups {
		counts[g.Kind]++
		if g.Kind == "hcp-managed" {
			managedOwners[g.Owner] = true
		}
	}
	t.Logf("groups=%d kinds=%v managed-test-owners=%d", len(s.Groups), counts, len(managedOwners))
	for _, g := range s.Groups {
		if g.Kind == "customer" && !managedOwners[g.Owner] {
			t.Logf("customer without managed group: %s (%s)", g.Name, g.Owner)
		}
	}
	for _, diagnostic := range s.Diagnostics {
		t.Logf("%s %s: %s", diagnostic.Code, diagnostic.Scope, diagnostic.Message)
	}
	if counts["hcp-managed"] != 58 || counts["customer"] != 58 || counts["primary"]+counts["aks-managed"] != 7 {
		t.Errorf("incomplete live managed discovery: %v", counts)
	}
	missing := 0
	for _, diagnostic := range s.Diagnostics {
		if diagnostic.Code == "missing-managed-mapping" {
			missing++
			if diagnostic.Scope != "SRE can pause schedules to stop backup execution for an HCP cluster" {
				t.Errorf("unexpected missing mapping: %+v", diagnostic)
			}
		} else if diagnostic.Code != "missing-infra-subscription" {
			t.Errorf("unexpected diagnostic: %+v", diagnostic)
		}
	}
	if missing != 1 {
		t.Errorf("expected one documented artifact coverage gap, got %d", missing)
	}
}

func TestDiscoverExactClusterArtifactsAndRejectedCreations(t *testing.T) {
	objects := discoveryFixture()
	owner := "Customer creates clusters: two, not three"
	objects[testArtifactPrefix+"test-timing/one.yaml"] = []byte("identifier: [Customer, 'creates clusters: two, not three']\nsubscriptionID: " + discoveryTestSub + "\nsteps:\n- name: Deploy HCP cluster customer/one (v20251223preview)\n")
	output := `"msg"="creating resource group" "resourceGroup"="customer"
"msg"="Starting HCP cluster creation (v20251223preview)" "resourceGroup"="customer" "clusterName"="one"
"msg"="Starting HCP cluster creation" "resourceGroup"="customer" "clusterName"="two"
"msg"="Starting HCP cluster creation" "resourceGroup"="customer" "clusterName"="three"`
	discoveryResults(t, objects, map[string]any{"name": owner, "result": "passed", "output": output})
	dir := testArtifactPrefix + "Customer_creates_clusters__two,_not_three/"
	objects[dir+"inspect-one/cluster-scoped-resources/config.openshift.io/infrastructures.yaml"] = []byte(`kind: InfrastructureList
items:
- apiVersion: config.openshift.io/v1
  kind: Infrastructure
  status:
    platformStatus:
      type: Azure
      azure: {resourceGroupName: exact-first}
`)
	artifact := "artifacts/e2e-parallel/aro-hcp-gather-snapshot/artifacts/Customer_creates_clusters__two__not_three/customer/test_phase/resources/microsoft.redhatopenshift_hcpopenshiftclusters/two/state/backend/resourceState.md"
	objects[artifact] = []byte(`# Query with a misleading shared resourceGroup is not evidence
| content |
| --- |
| {"resourceID":"/subscriptions/XXXX/resourceGroups/customer/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/two","properties":{"customerProperties":{"platform":{"managedResourceGroup":"exact-second"}}}} |
`)
	errorMessage := "PUT https://rp.example/subscriptions/XXXX/resourceGroups/customer/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/three\n---\nRESPONSE 400: Bad Request\n"
	objects[dir+"azure.log"], _ = json.Marshal(map[string]string{"event": "ResponseError", "msg": errorMessage})
	s := Discover(context.Background(), discoveryClient(t, objects), discoveryTestURL, discoveryNow())
	for _, name := range []string{"exact-first", "exact-second"} {
		g := findDiscoveryGroup(t, s, name)
		if g.Owner != owner || g.SubscriptionID != discoveryTestSub {
			t.Fatalf("incorrect mapping: %+v", g)
		}
	}
	if hasDiscoveryDiagnostic(s, "missing-managed-mapping") {
		t.Fatalf("rejected creation counted: %+v", s.Diagnostics)
	}
	if s.CostBasis != "AmortizedCost" {
		t.Fatal("changed billing basis")
	}
	// Direct SDK callers may omit the framework's creation message altogether.
	discoveryResults(t, objects, map[string]any{"name": owner, "result": "passed", "output": strings.Replace(output, `"msg"="Starting HCP cluster creation" "resourceGroup"="customer" "clusterName"="two"`, `"msg"="starting oc adm inspect for HCP cluster" "cluster"="customer/two"`, 1)})
	s = Discover(context.Background(), discoveryClient(t, objects), discoveryTestURL, discoveryNow())
	findDiscoveryGroup(t, s, "exact-second")
	if hasDiscoveryDiagnostic(s, "missing-managed-mapping") {
		t.Fatal("direct SDK cluster not associated through explicit inspect log")
	}
	// A rejected node-pool request cannot excuse an unmapped cluster.
	objects[dir+"azure.log"], _ = json.Marshal(map[string]string{"event": "ResponseError", "msg": strings.ReplaceAll(errorMessage, "/three\n", "/three/nodePools/np\n")})
	s = Discover(context.Background(), discoveryClient(t, objects), discoveryTestURL, discoveryNow())
	if !hasDiscoveryDiagnostic(s, "missing-managed-mapping") {
		t.Fatal("nodepool failure masked unmapped cluster")
	}
}

func TestDiscoverSnapshotGuardsAndMalformedRows(t *testing.T) {
	for _, tc := range []struct {
		name, row  string
		wantFormat bool
	}{
		{"wrong-cluster", `{"resourceID":"/subscriptions/XXXX/resourceGroups/customer/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/other","properties":{"platform":{"managedResourceGroup":"not-owned"}}}`, false},
		{"wrong-subscription", `{"resourceID":"/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/customer/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/one","properties":{"platform":{"managedResourceGroup":"not-owned"}}}`, false},
		{"malformed", `{invalid}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objects := discoveryFixture()
			discoveryResults(t, objects, map[string]any{"name": "Customer creates clusters", "result": "passed", "output": `"msg"="creating resource group" "resourceGroup"="customer"
"msg"="Starting HCP cluster creation" "resourceGroup"="customer" "clusterName"="one"`})
			artifact := "artifacts/e2e-parallel/aro-hcp-gather-snapshot/artifacts/Customer_creates_clusters/customer/test_phase/resources/microsoft.redhatopenshift_hcpopenshiftclusters/one/state/backend/resourceState.md"
			objects[artifact] = []byte("| " + tc.row + " |\n")
			s := Discover(context.Background(), discoveryClient(t, objects), discoveryTestURL, discoveryNow())
			for _, g := range s.Groups {
				if g.Kind == "hcp-managed" {
					t.Fatalf("invalid row claimed group: %+v", g)
				}
			}
			if !hasDiscoveryDiagnostic(s, "missing-managed-mapping") || hasDiscoveryDiagnostic(s, "artifact-format") != tc.wantFormat {
				t.Fatalf("incorrect errors: %+v", s.Diagnostics)
			}
		})
	}
}

func TestDiscoveryDirectoryCollisionsAndVMContext(t *testing.T) {
	s := &Snapshot{}
	d := &discovery{snapshot: s, groups: map[string]*Group{}, tests: map[string]*discoveredTest{}, excluded: map[string]bool{}}
	d.test("test:a")
	d.test("test?a")
	if d.directoryOwner("test_a", false) != "" {
		t.Fatal("ambiguous sanitized directory accepted")
	}
	if len(discoveryTestDirectory(strings.Repeat("a", 250), false)) != 200 {
		t.Fatal("framework truncation differs")
	}
	owner := "one cluster"
	d.test(owner).sub = discoveryTestSub
	d.testOutput(owner, `"msg"="creating resource group" "resourceGroup"="customer"
"msg"="no VMs found in resource group" "resourceGroup"="unrelated"
"msg"="Starting HCP cluster creation" "resourceGroup"="customer" "clusterName"="one"
"msg"="no VMs found in resource group" "resourceGroup"="exact-empty-managed"`)
	if len(d.test(owner).managed) != 0 || d.test(owner).emptyVMs["customer/one"] != "exact-empty-managed" {
		t.Fatal("VM message accepted without cluster status/context")
	}
	message := "PUT https://rp.example/subscriptions/XXXX/resourceGroups/customer/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/one\nRESPONSE Status: 201 Created\n"
	d.acceptedCreation(owner, message)
	d.rejectedCreation(owner, strings.Replace(message, "RESPONSE Status: 201 Created", "RESPONSE 409: Conflict", 1))
	if !d.test(owner).accepted["customer/one"] || !d.test(owner).rejected["customer/one"] {
		t.Fatal("acceptance/rejection tracking failed")
	}
}

func TestDiscoverMappingDiagnosticsPerCustomer(t *testing.T) {
	objects := discoveryFixture()
	discoveryResults(t, objects, map[string]any{"name": "Customer creates clusters", "result": "passed", "output": `"msg"="creating resource group" "resourceGroup"="first"
"msg"="creating resource group" "resourceGroup"="second"
"msg"="Starting HCP cluster creation" "resourceGroup"="first" "clusterName"="one"
"msg"="Starting HCP cluster creation" "resourceGroup"="second" "clusterName"="two"
"msg"="Starting HCP cluster creation" "resourceGroup"="second" "clusterName"="three"`})
	s := Discover(context.Background(), discoveryClient(t, objects), discoveryTestURL, discoveryNow())
	found := map[string]Diagnostic{}
	for _, d := range s.Diagnostics {
		if d.Code == "missing-managed-mapping" {
			found[d.ResourceGroup] = d
		}
	}
	if len(found) != 2 {
		t.Fatalf("expected per-customer gaps: %+v", found)
	}
	for rg, count := range map[string]int{"first": 1, "second": 2} {
		d := found[rg]
		if d.Scope != "Customer creates clusters" || d.SubscriptionID != discoveryTestSub || d.ClusterCount != count || d.MissingClusterCount != count || !strings.Contains(d.Message, rg) {
			t.Fatalf("incorrect structured mapping diagnostic: %+v", d)
		}
	}
}

func TestDiscoverInfraTimingOptional(t *testing.T) {
	for _, metrics := range []string{"", `{"pods":[]}`, `{"pods":[{"pod_name":"e2e-parallel-aro-hcp-provision-environment","start_time":"2026-09-17T12:00:00Z"},{"pod_name":"e2e-parallel-aro-hcp-deprovision-environment","completion_time":"2026-09-17T11:00:00Z"}]}`} {
		objects := discoveryFixture()
		if metrics == "" {
			delete(objects, "artifacts/ci-operator-metrics.json")
		} else {
			objects["artifacts/ci-operator-metrics.json"] = []byte(metrics)
		}
		discoveryResults(t, objects, map[string]any{"name": "Customer creates clusters", "result": "skipped", "output": ""})
		s := Discover(context.Background(), discoveryClient(t, objects), discoveryTestURL, discoveryNow())
		found := false
		for _, d := range s.Diagnostics {
			if d.Code == "infra-timing" {
				found = true
				if d.Severity != "info" || !strings.Contains(d.Message, "hourly rate omitted") {
					t.Fatalf("optional timing blocks collection: %+v", d)
				}
			}
		}
		if !found {
			t.Fatal("missing optional rate diagnostic")
		}
	}
}
