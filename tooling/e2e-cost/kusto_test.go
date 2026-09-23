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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const kustoTestSub = "0ef1ad54-9296-44cd-9600-5dc8e9a74034"

type kustoCredential struct {
	t     *testing.T
	err   error
	calls int
}

func (c *kustoCredential) GetToken(_ context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	c.t.Helper()
	c.calls++
	if !reflect.DeepEqual(options.Scopes, []string{"https://kusto.kusto.windows.net/.default"}) {
		c.t.Fatalf("incorrect Kusto token scope: %v", options.Scopes)
	}
	return azcore.AccessToken{Token: "SECRET-TOKEN"}, c.err
}

func kustoFixture(t *testing.T) *Snapshot {
	t.Helper()
	objects := discoveryFixture()
	objects["artifacts/e2e-parallel/aro-hcp-provision-environment/artifacts/config.yaml"] = []byte(`regionRG: run
svc:
  rg: run-svc
  subscription: {key: XXXXXXXXXXXXXXXXXXXX}
  aks: {name: ci01-j3671424-svc, nodeResourceGroup: service-nodes}
mgmt:
  rg: run-mgmt-1
  subscription: {key: REDACTED}
  aks: {name: ci01-j3671424-mgmt1}
kusto: {rg: shared-kusto, kustoName: historical-audit, location: eastus, serviceLogsDatabase: service-logs}
`)
	objects["artifacts/e2e-parallel/aro-hcp-gather-visualization/artifacts/timing/steps.yaml"] = []byte(`- details:
    arm:
      operations:
      - resource: {resourceGroup: run-svc, name: ci01-j3671424-svc, resourceType: Microsoft.ContainerService/managedClusters, subscriptionID: XXXXX}
      - resource: {resourceGroup: run-mgmt-1, name: ci01-j3671424-mgmt1, resourceType: Microsoft.ContainerService/managedClusters}
      - resource: {resourceGroup: run-mgmt-2, name: ci01-j3671424-mgmt2, resourceType: Microsoft.ContainerService/managedClusters}
      - resource: {resourceGroup: run-mgmt-2, name: ci01-j3671424-mgmt2, resourceType: Microsoft.ContainerService/managedClusters}
      - resource: {resourceGroup: run-mgmt-2, name: not-an-aks, resourceType: Microsoft.Storage/storageAccounts}
`)
	discoveryResults(t, objects, map[string]any{"name": "Customer creates clusters", "result": "skipped", "output": ""})
	return Discover(context.Background(), discoveryClient(t, objects), discoveryTestURL, discoveryNow())
}

func kustoID(sub, rg, name string) string {
	return "/subscriptions/" + sub + "/resourceGroups/" + rg + "/providers/Microsoft.ContainerService/managedClusters/" + name
}

// Real v1 shape: the primary result is Table_0, identified by Table_3's TOC.
// QueryStatus contains informational completion and statistics rows, not severity 0.
func kustoResult(ids ...string) string {
	rows := make([][]string, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, []string{id})
	}
	encoded, _ := json.Marshal(rows)
	return fmt.Sprintf(`{"Tables":[
{"TableName":"Table_0","Columns":[{"ColumnName":"resourceId","DataType":"String","ColumnType":"string"}],"Rows":%s},
{"TableName":"Table_1","Columns":[{"ColumnName":"Value","ColumnType":"string"}],"Rows":[]},
{"TableName":"Table_2","Columns":[{"ColumnName":"Timestamp","ColumnType":"datetime"},{"ColumnName":"Severity","ColumnType":"int"},{"ColumnName":"SeverityName","ColumnType":"string"},{"ColumnName":"StatusCode","ColumnType":"int"},{"ColumnName":"StatusDescription","ColumnType":"string"},{"ColumnName":"Count","ColumnType":"int"}],"Rows":[["2026-09-17T12:45:00Z",4,"Info",0,"Query completed",1],["2026-09-17T12:45:00Z",6,"Stats",0,"Statistics",1]]},
{"TableName":"Table_3","Columns":[{"ColumnName":"Ordinal","ColumnType":"long"},{"ColumnName":"Kind","ColumnType":"string"},{"ColumnName":"Name","ColumnType":"string"},{"ColumnName":"Id","ColumnType":"string"}],"Rows":[[0,"QueryResult","PrimaryResult","id"],[1,"QueryProperties","@ExtendedProperties","id"],[2,"QueryStatus","QueryStatus","id"]]}
]}`, encoded)
}

func TestDiscoverKustoMetadata(t *testing.T) {
	s := kustoFixture(t)
	if s.infraLookup == nil || s.infraLookup.endpoint != "https://historical-audit.eastus.kusto.windows.net" || s.infraLookup.database != "service-logs" || s.infraLookup.invalid {
		t.Fatalf("incorrect lookup config: %+v", s.infraLookup)
	}
	want := []infraBinding{
		{rg: "run-svc", cluster: "ci01-j3671424-svc", owner: "Service Cluster", nodeRG: "service-nodes", regionalRG: "run"},
		{rg: "run-mgmt-1", cluster: "ci01-j3671424-mgmt1", owner: "Management Cluster 1", nodeRG: "run-mgmt-1-aks1"},
		{rg: "run-mgmt-2", cluster: "ci01-j3671424-mgmt2", owner: "Management Cluster 2", nodeRG: "run-mgmt-2-aks1"},
	}
	if !reflect.DeepEqual(s.infraLookup.bindings, want) {
		t.Fatalf("incorrect exact bindings: %+v", s.infraLookup.bindings)
	}
	g := findDiscoveryGroup(t, s, "run-svc")
	if len(g.Resources) != 1 || g.Resources[0].ID != "" || g.Resources[0].Name != "ci01-j3671424-svc" {
		t.Fatalf("redacted subscription lost artifact inventory: %+v", g.Resources)
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"infraLookup", "historical-audit", "service-logs", "regionalRG", "bindings"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("transient metadata leaked: %s", secret)
		}
	}
	var restored Snapshot
	if json.Unmarshal(data, &restored) != nil || restored.infraLookup != nil {
		t.Fatal("offline snapshot retained live lookup metadata")
	}
}

func TestResolveInfraSubscriptionsScoped(t *testing.T) {
	s := kustoFixture(t)
	s.Groups = append(s.Groups, Group{Name: "other-infra", Category: "Infra", Owner: "Regional", Kind: "primary", BillingStatus: "unavailable"}, Group{Name: "test", Category: "Tests", SubscriptionID: "REDACTED"})
	s.Diagnose("error", "ownership-conflict", "service-nodes", "retain conflict")
	s.Diagnose("error", "unrelated-error", "run-svc", "retain discovery error")
	credential := &kustoCredential{t: t}
	calls := 0
	subs := []string{kustoTestSub, discoveryTestSub, "11111111-1111-1111-1111-111111111111"}
	client := &http.Client{Transport: discoveryTransport(func(r *http.Request) (*http.Response, error) {
		binding := s.infraLookup.bindings[calls]
		if r.Method != http.MethodPost || r.URL.String() != s.infraLookup.endpoint+"/v1/rest/query" || r.Header.Get("Authorization") != "Bearer SECRET-TOKEN" || r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("incorrect Kusto request: %s %s", r.Method, r.URL)
		}
		var query map[string]string
		if json.NewDecoder(r.Body).Decode(&query) != nil || query["db"] != "service-logs" {
			t.Fatal("invalid query envelope")
		}
		want := fmt.Sprintf(`kubeAudit | where timestamp between (datetime(%s) .. datetime(%s)) | where resourceId endswith "/resourcegroups/%s/providers/microsoft.containerservice/managedclusters/%s" | summarize by resourceId`, s.Job.InfraStartedAt.Format(time.RFC3339), s.Job.InfraEndedAt.Format(time.RFC3339), binding.rg, binding.cluster)
		if query["csl"] != want || strings.Contains(query["csl"], s.QueryEnd) {
			t.Fatalf("query not scoped to full infra interval and exact RG/AKS: %s", query["csl"])
		}
		id := kustoID(subs[calls], binding.rg, binding.cluster)
		calls++
		return billingResponse(200, kustoResult(id, strings.ToUpper(id))), nil
	})}
	ResolveInfraSubscriptions(context.Background(), client, credential, s)
	if calls != 3 || credential.calls != 3 {
		t.Fatalf("duplicate or missing lookups: requests=%d tokens=%d", calls, credential.calls)
	}
	for name, sub := range map[string]string{"run": subs[0], "run-svc": subs[0], "service-nodes": subs[0], "run-mgmt-1": subs[1], "run-mgmt-1-aks1": subs[1], "run-mgmt-2": subs[2], "run-mgmt-2-aks1": subs[2]} {
		g := findDiscoveryGroup(t, s, name)
		status := "pending"
		if name == "service-nodes" {
			status = "unavailable"
		}
		if g.SubscriptionID != sub || g.BillingStatus != status || !strings.HasPrefix(g.Attribution, "Subscription resolved from historical AKS audit logs") {
			t.Fatalf("incorrect resolution: %+v", g)
		}
		found := false
		for _, d := range s.Diagnostics {
			if d.Scope == name && d.Code == "missing-infra-subscription" {
				t.Fatalf("resolved group retains missing subscription error: %+v", d)
			}
			found = found || d.Scope == name && d.Code == "infra-subscription-resolved" && d.Severity == "info"
		}
		if !found {
			t.Fatalf("missing per-group provenance: %s", name)
		}
	}
	if findDiscoveryGroup(t, s, "other-infra").SubscriptionID != "" || findDiscoveryGroup(t, s, "test").SubscriptionID != "REDACTED" || !hasDiscoveryDiagnostic(s, "unrelated-error") || !hasDiscoveryDiagnostic(s, "ownership-conflict") {
		t.Fatal("unrelated group or diagnostic modified")
	}
	for _, binding := range s.infraLookup.bindings {
		g := findDiscoveryGroup(t, s, binding.rg)
		for _, r := range g.Resources {
			if r.Type == "Microsoft.ContainerService/managedClusters" && r.ID != kustoID(g.SubscriptionID, binding.rg, binding.cluster) {
				t.Fatalf("exact AKS inventory ID not repaired: %+v", r)
			}
			if r.Type == "Microsoft.Storage/storageAccounts" && r.ID != "" {
				t.Fatal("unrelated inventory ID reconstructed without full scope evidence")
			}
		}
	}
}

func TestResolveInfraFailures(t *testing.T) {
	id := kustoID(kustoTestSub, "run-svc", "ci01-j3671424-svc")
	for _, tc := range []struct {
		name, body string
		status     int
		auth, net  bool
	}{
		{name: "no matches", body: kustoResult()},
		{name: "ambiguous", body: kustoResult(id, kustoID(discoveryTestSub, "run-svc", "ci01-j3671424-svc"))},
		{name: "wrong RG", body: kustoResult(strings.Replace(id, "/run-svc/", "/other/", 1))},
		{name: "wrong AKS", body: kustoResult(id + "-other")},
		{name: "wrong type", body: kustoResult(strings.Replace(id, "managedClusters", "other", 1))},
		{name: "invalid UUID", body: kustoResult(strings.Replace(id, kustoTestSub, "redacted", 1))},
		{name: "extension scope", body: kustoResult(id + "/providers/Microsoft.ContainerService/managedClusters/ci01-j3671424-svc")},
		{name: "malformed", body: `{"Tables":SECRET}`},
		{name: "partial status", body: strings.Replace(kustoResult(id), `4,"Info"`, `2,"Error"`, 1)},
		{name: "HTTP failure", status: 403, body: "SECRET"},
		{name: "authentication", auth: true},
		{name: "network", net: true},
		{name: "oversized valid JSON", body: kustoResult(id) + strings.Repeat(" ", 8<<20)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := kustoFixture(t)
			credential := &kustoCredential{t: t}
			if tc.auth {
				credential.err = errors.New("SECRET credential details")
			}
			client := &http.Client{Transport: discoveryTransport(func(*http.Request) (*http.Response, error) {
				if tc.auth {
					t.Fatal("request after failed authentication")
				}
				if tc.net {
					return nil, errors.New("SECRET network details")
				}
				status := tc.status
				if status == 0 {
					status = 200
				}
				return billingResponse(status, tc.body), nil
			})}
			ResolveInfraSubscriptions(context.Background(), client, credential, s)
			for _, g := range s.Groups {
				if g.SubscriptionID != "" || g.BillingStatus != "unavailable" {
					t.Fatalf("failed query resolved group: %+v", g)
				}
			}
			counts := map[string]int{}
			for _, d := range s.Diagnostics {
				if strings.Contains(d.Message, "SECRET") {
					t.Fatalf("unsafe diagnostic: %+v", d)
				}
				counts[d.Code]++
			}
			if counts["missing-infra-subscription"] != 7 || counts["infra-subscription-lookup"] != 7 {
				t.Fatalf("failures did not retain per-group discovery errors: %v", counts)
			}
		})
	}
}

func TestKustoV1StatusValidation(t *testing.T) {
	valid := kustoResult(kustoID(kustoTestSub, "rg", "aks"))
	for _, tc := range []struct{ name, body string }{
		{"severity 0", strings.Replace(valid, `4,"Info"`, `0,"Fatal"`, 1)},
		{"severity 1", strings.Replace(valid, `4,"Info"`, `1,"Error"`, 1)},
		{"severity 2", strings.Replace(valid, `4,"Info"`, `2,"Error"`, 1)},
		{"null severity", strings.Replace(valid, `4,"Info"`, `null,"Info"`, 1)},
		{"string severity", strings.Replace(valid, `4,"Info"`, `"4","Info"`, 1)},
		{"missing status", strings.ReplaceAll(valid, `"QueryStatus"`, `"Other"`)},
		{"missing severity", strings.Replace(valid, `"Severity"`, `"Other"`, 1)},
		{"bad ordinal", strings.Replace(valid, `[2,"QueryStatus"`, `[22,"QueryStatus"`, 1)},
		{"null ordinal", strings.Replace(valid, `[2,"QueryStatus"`, `[null,"QueryStatus"`, 1)},
		{"duplicate primary", strings.Replace(valid, `[1,"QueryProperties","@ExtendedProperties"`, `[1,"QueryResult","PrimaryResult"`, 1)},
		{"bad columns", strings.Replace(valid, `"resourceId"`, `"other"`, 1)},
		{"bad row width", strings.Replace(valid, `4,"Info",0,`, `4,"Info",`, 1)},
		{"top-level error", strings.Replace(valid, `{"Tables":`, `{"error":{"message":"SECRET"},"Tables":`, 1)},
		{"table error", strings.Replace(valid, `"TableName":"Table_0"`, `"error":{"message":"SECRET"},"TableName":"Table_0"`, 1)},
		{"exceptions", strings.Replace(valid, `{"Tables":`, `{"Exceptions":[{"message":"SECRET"}],"Tables":`, 1)},
		{"truncated", valid[:len(valid)-5]},
		{"trailing error", valid + `{"error":"SECRET"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ids, err := kustoResourceIDs([]byte(tc.body))
			if err == nil || len(ids) != 0 || strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("unsafe partial status accepted: ids=%v err=%v", ids, err)
			}
		})
	}
	if ids, err := kustoResourceIDs([]byte(valid)); err != nil || len(ids) != 1 {
		t.Fatalf("valid v1 response rejected: %v %v", ids, err)
	}
}

func TestResolveInfraNoNetwork(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Snapshot)
	}{
		{"missing metadata", func(s *Snapshot) { s.infraLookup = nil }},
		{"missing bindings", func(s *Snapshot) { s.infraLookup.bindings = nil }},
		{"invalid metadata", func(s *Snapshot) { s.infraLookup.invalid = true }},
		{"missing database", func(s *Snapshot) { s.infraLookup.database = "" }},
		{"invalid interval", func(s *Snapshot) { s.Job.FinishedAt = time.Time{} }},
		{"nothing missing", func(s *Snapshot) {
			for i := range s.Groups {
				s.Groups[i].SubscriptionID = kustoTestSub
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := kustoFixture(t)
			tc.edit(s)
			credential := &kustoCredential{t: t}
			client := &http.Client{Transport: discoveryTransport(func(*http.Request) (*http.Response, error) {
				t.Fatal("unexpected network request")
				return nil, nil
			})}
			ResolveInfraSubscriptions(context.Background(), client, credential, s)
			if credential.calls != 0 {
				t.Fatal("unnecessary token request")
			}
			if tc.name != "nothing missing" && !hasDiscoveryDiagnostic(s, "infra-subscription-lookup") {
				t.Fatal("missing actionable unavailable diagnostic")
			}
		})
	}
	for _, endpoint := range []string{
		"https://evil.example", "http://cluster.eastus.kusto.windows.net", "https://cluster.eastus.kusto.windows.net.evil.example",
		"https://cluster.eastus.kusto.windows.net:443", "https://cluster.eastus.kusto.windows.net:", "https://user@cluster.eastus.kusto.windows.net",
		"https://cluster.eastus.kusto.windows.net/", "https://cluster.eastus.kusto.windows.net?", "https://cluster.eastus.kusto.windows.net?q=x",
		"https://cluster.eastus.kusto.windows.net#", "https://cluster.eastus.kusto.windows.net/#fragment", "https://.kusto.windows.net",
		"https://bad_label.eastus.kusto.windows.net", "https://cluster.eastus.kusto.windows.net.",
	} {
		t.Run(endpoint, func(t *testing.T) {
			s := kustoFixture(t)
			s.infraLookup.endpoint = endpoint
			credential := &kustoCredential{t: t}
			client := &http.Client{Transport: discoveryTransport(func(*http.Request) (*http.Response, error) {
				t.Fatal("untrusted endpoint requested")
				return nil, nil
			})}
			ResolveInfraSubscriptions(context.Background(), client, credential, s)
			if credential.calls != 0 || !hasDiscoveryDiagnostic(s, "infra-subscription-lookup") {
				t.Fatal("endpoint validation not applied before authentication")
			}
		})
	}
}

func TestResolveInfraKnownAndLinkedSubscriptions(t *testing.T) {
	for _, contradictory := range []bool{false, true} {
		t.Run(fmt.Sprint(contradictory), func(t *testing.T) {
			s := kustoFixture(t)
			for i := range s.Groups {
				g := &s.Groups[i]
				g.SubscriptionID = kustoTestSub
				g.BillingStatus = "complete"
				if g.Name == "service-nodes" {
					g.SubscriptionID, g.BillingStatus = "", "unavailable"
				}
			}
			calls := 0
			client := &http.Client{Transport: discoveryTransport(func(r *http.Request) (*http.Response, error) {
				body, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(body), "ci01-j3671424-svc") || strings.Contains(string(body), "mgmt") {
					t.Fatal("queried an already known, unneeded binding")
				}
				calls++
				sub := kustoTestSub
				if contradictory {
					sub = discoveryTestSub
				}
				return billingResponse(200, kustoResult(kustoID(sub, "run-svc", "ci01-j3671424-svc"))), nil
			})}
			ResolveInfraSubscriptions(context.Background(), client, &kustoCredential{t: t}, s)
			if calls != 1 || findDiscoveryGroup(t, s, "run-svc").SubscriptionID != kustoTestSub || findDiscoveryGroup(t, s, "run-svc").BillingStatus != "complete" {
				t.Fatal("known subscriptions were modified or unnecessarily queried")
			}
			nodes := findDiscoveryGroup(t, s, "service-nodes")
			if contradictory {
				if nodes.SubscriptionID != "" || !hasDiscoveryDiagnostic(s, "infra-subscription-conflict") {
					t.Fatal("contradictory linked subscription accepted")
				}
			} else if nodes.SubscriptionID != kustoTestSub || nodes.BillingStatus != "pending" {
				t.Fatal("linked missing group not resolved")
			}
		})
	}
}

func TestResolveInfraRedirectAndQueryEscaping(t *testing.T) {
	s := kustoFixture(t)
	s.infraLookup.database = "db\"; .show secrets\n"
	s.Job.InfraStartedAt, s.Job.InfraEndedAt = time.Time{}, time.Time{}
	calls := 0
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { t.Fatal("caller redirect policy used"); return nil },
		Transport: discoveryTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.Host != "historical-audit.eastus.kusto.windows.net" {
				t.Fatal("redirect forwarded token")
			}
			var query map[string]string
			if json.NewDecoder(r.Body).Decode(&query) != nil || query["db"] != s.infraLookup.database || strings.Contains(query["csl"], ".show") || !strings.Contains(query["csl"], s.Job.StartedAt.Format(time.RFC3339)) || !strings.Contains(query["csl"], s.Job.FinishedAt.Format(time.RFC3339)) {
				t.Fatal("database interpolated into KQL or fallback interval incorrect")
			}
			resp := billingResponse(307, "SECRET")
			resp.Header.Set("Location", "https://evil.example/steal-token")
			return resp, nil
		}),
	}
	ResolveInfraSubscriptions(context.Background(), client, &kustoCredential{t: t}, s)
	if calls != 3 || !hasDiscoveryDiagnostic(s, "infra-subscription-lookup") {
		t.Fatal("redirect response did not fail closed")
	}
}

func TestDiscoveryKustoBindingConflicts(t *testing.T) {
	s := &Snapshot{}
	d := &discovery{snapshot: s}
	d.infraBinding(infraBinding{rg: "svc", owner: "Service Cluster", nodeRG: "nodes", regionalRG: "regional"})
	d.infraBinding(infraBinding{rg: "svc", owner: "Service Cluster", cluster: "exact-aks", nodeRG: "nodes"})
	if len(s.infraLookup.bindings) != 1 || s.infraLookup.bindings[0].cluster != "exact-aks" || s.infraLookup.bindings[0].regionalRG != "regional" {
		t.Fatal("steps did not fill missing exact config name while preserving regional association")
	}
	d.infraBinding(infraBinding{rg: "SVC", owner: "Service Cluster", cluster: "EXACT-AKS", nodeRG: "NODES"})
	if len(s.infraLookup.bindings) != 1 || s.HasErrors() {
		t.Fatal("case-insensitive duplicate treated as a conflict")
	}
	d.infraBinding(infraBinding{rg: "svc", owner: "Service Cluster", cluster: "other-aks", nodeRG: "nodes"})
	if len(s.infraLookup.bindings) != 2 || !s.infraLookup.bindings[0].conflict || !s.infraLookup.bindings[1].conflict || !hasDiscoveryDiagnostic(s, "infra-lookup-conflict") {
		t.Fatal("contradictory exact AKS names not diagnosed")
	}
	for _, value := range []string{"cluster.evil.example", "cluster:443", "cluster/path", "-invalid", "", "bad_label"} {
		d.snapshot = &Snapshot{}
		d.infraLookupConfig(map[string]any{"kusto": map[string]any{"kustoName": value, "location": "eastus", "serviceLogsDatabase": "logs"}})
		if !d.snapshot.infraLookup.invalid {
			t.Fatalf("invalid artifact hostname component accepted: %q", value)
		}
	}
}

func TestResolveInfraBindingIsolation(t *testing.T) {
	for _, mode := range []string{"missing name", "conflicting name", "ambiguous result"} {
		t.Run(mode, func(t *testing.T) {
			s := kustoFixture(t)
			switch mode {
			case "missing name":
				s.infraLookup.bindings[0].cluster = ""
			case "conflicting name":
				d := &discovery{snapshot: s}
				d.infraBinding(infraBinding{rg: "run-svc", cluster: "other-aks", owner: "Service Cluster", nodeRG: "service-nodes"})
			}
			calls := 0
			client := &http.Client{Transport: discoveryTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				var query map[string]string
				if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
					t.Fatal(err)
				}
				for _, b := range s.infraLookup.bindings {
					if b.cluster == "" || !strings.Contains(query["csl"], "/managedclusters/"+b.cluster+`"`) {
						continue
					}
					id := kustoID(kustoTestSub, b.rg, b.cluster)
					if b.rg == "run-svc" {
						if mode != "ambiguous result" {
							t.Fatal("missing or conflicting name was queried")
						}
						return billingResponse(200, kustoResult(id, kustoID(discoveryTestSub, b.rg, b.cluster))), nil
					}
					return billingResponse(200, kustoResult(id)), nil
				}
				t.Fatal("query inferred an unknown AKS name")
				return nil, nil
			})}
			ResolveInfraSubscriptions(context.Background(), client, &kustoCredential{t: t}, s)
			wantCalls := 2
			if mode == "ambiguous result" {
				wantCalls = 3
			}
			if calls != wantCalls {
				t.Fatalf("unexpected request count: %d", calls)
			}
			for _, g := range s.Groups {
				if strings.HasPrefix(g.Owner, "Management Cluster") {
					if g.SubscriptionID != kustoTestSub || g.BillingStatus != "pending" {
						t.Fatalf("unrelated binding blocked: %+v", g)
					}
				} else if g.SubscriptionID != "" || g.BillingStatus != "unavailable" {
					t.Fatalf("failed binding borrowed unrelated subscription: %+v", g)
				}
			}
		})
	}
}

func TestDiscoveryKustoExactManagementNodeGroup(t *testing.T) {
	s := &Snapshot{}
	d := &discovery{snapshot: s, groups: map[string]*Group{}, excluded: map[string]bool{}}
	d.infraConfig(map[string]any{
		"regionRG": "regional",
		"svc":      map[string]any{"rg": "svc", "aks": map[string]any{"name": "service-aks"}},
		"mgmt":     map[string]any{"rg": "run-mgmt-1", "aks": map[string]any{"name": "management-aks", "nodeResourceGroup": "exact-management-nodes"}},
		"kusto":    map[string]any{"kustoName": "audit", "location": "eastus", "serviceLogsDatabase": "logs"},
	})
	d.infraSteps([]any{map[string]any{"resource": map[string]any{"resourceGroup": "run-mgmt-1", "name": "management-aks", "resourceType": "Microsoft.ContainerService/managedClusters"}}})
	if d.groups["/run-mgmt-1-aks1"] != nil || d.groups["/exact-management-nodes"] == nil {
		t.Fatal("steps replaced explicit management node RG with a naming convention")
	}
	for _, b := range s.infraLookup.bindings {
		if b.rg == "run-mgmt-1" && (b.nodeRG != "exact-management-nodes" || b.conflict) {
			t.Fatalf("incorrect management node binding: %+v", b)
		}
	}
}

func TestResolveInfraMixedSubscriptionEvidence(t *testing.T) {
	for _, knownFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(knownFirst), func(t *testing.T) {
			s := kustoFixture(t)
			binding := s.infraLookup.bindings[2]
			first := Charge{Day: "2026-09-17", MeterID: "meter", CostUSD: 1}
			second := Charge{Day: "2026-09-18", MeterID: "meter", CostUSD: 2}
			var duplicates []Group
			for i := range s.Groups {
				g := &s.Groups[i]
				if g.Owner != binding.owner {
					g.SubscriptionID = kustoTestSub
					continue
				}
				id := "/subscriptions/" + kustoTestSub + "/resourceGroups/" + g.Name + "/providers/Microsoft.Compute/disks/disk"
				g.Resources = []Resource{
					{ID: id, Name: "disk", Type: "Microsoft.Compute/disks", Source: "artifact", Charges: []Charge{first}},
					{Name: "id-less", Type: "Microsoft.Compute/disks", Source: "artifact"},
					{Name: "only-masked", Type: "Microsoft.Compute/disks", Source: "artifact"},
				}
				known := *g
				known.SubscriptionID, known.Name, known.BillingStatus = strings.ToUpper(kustoTestSub), strings.ToUpper(g.Name), "pending"
				known.Resources = []Resource{
					{ID: strings.ToUpper(id), Name: "DISK", Type: "Microsoft.Compute/disks", Source: "billing", Charges: []Charge{first, second}},
					{Name: "disk", Type: "Microsoft.Compute/disks", Source: "artifact", Charges: []Charge{first}},
					{Name: "id-less", Type: "Microsoft.Compute/disks", Source: "artifact"},
					{Name: "only-known", Type: "Microsoft.Compute/disks", Source: "artifact"},
				}
				duplicates = append(duplicates, known)
			}
			if knownFirst {
				s.Groups = append(duplicates, s.Groups...)
			} else {
				s.Groups = append(s.Groups, duplicates...)
			}
			calls := 0
			client := &http.Client{Transport: discoveryTransport(func(*http.Request) (*http.Response, error) {
				calls++
				return billingResponse(200, kustoResult(kustoID(kustoTestSub, binding.rg, binding.cluster))), nil
			})}
			ResolveInfraSubscriptions(context.Background(), client, &kustoCredential{t: t}, s)
			if calls != 1 || len(s.Groups) != 7 {
				t.Fatalf("mixed evidence not reconciled: calls=%d groups=%d", calls, len(s.Groups))
			}
			seen := map[string]bool{}
			for _, g := range s.Groups {
				key := strings.ToLower(g.SubscriptionID + "/" + g.Name)
				if seen[key] {
					t.Fatalf("duplicate billing claim remains: %s", key)
				}
				seen[key] = true
				if g.Owner != binding.owner {
					continue
				}
				if g.BillingStatus != "pending" || g.Attribution == "" || len(g.Resources) != 4 {
					t.Fatalf("merged group lost status, provenance or inventory: %+v", g)
				}
				for _, r := range g.Resources {
					if strings.EqualFold(r.Name, "disk") && (r.ID == "" || r.Source != "both" || !reflect.DeepEqual(r.Charges, []Charge{first, second})) {
						t.Fatalf("canonical resource merge lost or duplicated charges: %+v", r)
					}
				}
			}
			if hasDiscoveryDiagnostic(s, "ownership-conflict") {
				t.Fatal("consistent mixed evidence treated as conflicting ownership")
			}
			before, _ := json.Marshal(s)
			ResolveInfraSubscriptions(context.Background(), client, &kustoCredential{t: t}, s)
			after, _ := json.Marshal(s)
			if calls != 1 || string(before) != string(after) {
				t.Fatal("repeat resolution changed inventory or duplicated charges")
			}
		})
	}
}

func TestResolveInfraDuplicateOwnershipConflict(t *testing.T) {
	for _, field := range []string{"owner", "category", "kind"} {
		t.Run(field, func(t *testing.T) {
			s := kustoFixture(t)
			binding := s.infraLookup.bindings[2]
			for i := range s.Groups {
				if s.Groups[i].Owner != binding.owner {
					s.Groups[i].SubscriptionID = kustoTestSub
				}
			}
			other := findDiscoveryGroup(t, s, binding.rg)
			other.SubscriptionID, other.BillingStatus = kustoTestSub, "pending"
			switch field {
			case "owner":
				other.Owner = "Other Owner"
			case "category":
				other.Category = "Tests"
			case "kind":
				other.Kind = "customer"
			}
			s.Groups = append(s.Groups, other)
			client := &http.Client{Transport: discoveryTransport(func(*http.Request) (*http.Response, error) {
				return billingResponse(200, kustoResult(kustoID(kustoTestSub, binding.rg, binding.cluster))), nil
			})}
			ResolveInfraSubscriptions(context.Background(), client, &kustoCredential{t: t}, s)
			if len(s.Groups) != 8 || !hasDiscoveryDiagnostic(s, "ownership-conflict") {
				t.Fatal("genuinely different ownership was silently merged")
			}
			for _, g := range s.Groups {
				if g.Name == binding.rg && g.BillingStatus != "unavailable" {
					t.Fatalf("conflicting group remains billable: %+v", g)
				}
			}
		})
	}
}

func TestReconcileInfraPreservesDistinctResourceScopes(t *testing.T) {
	parent := "/subscriptions/" + kustoTestSub + "/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/"
	resources := []Resource{
		{ID: parent + "first/extensions/agent", Name: "agent", Type: "Microsoft.Compute/virtualMachines/extensions"},
		{ID: parent + "second/extensions/agent", Name: "agent", Type: "Microsoft.Compute/virtualMachines/extensions"},
		{Name: "agent", Type: "Microsoft.Compute/virtualMachines/extensions", Charges: []Charge{{CostUSD: 3}}},
	}
	g := Group{SubscriptionID: kustoTestSub, Name: "rg", Category: "Infra", Owner: "Management Cluster 2", Kind: "primary"}
	s := &Snapshot{Groups: []Group{g, g}}
	s.Groups[0].Resources, s.Groups[1].Resources = resources[:1], resources[1:]
	reconcileResolvedInfraGroups(s, map[string]bool{kustoTestSub + "/rg": true})
	if len(s.Groups) != 1 || !reflect.DeepEqual(s.Groups[0].Resources, resources) {
		t.Fatalf("ambiguous ID-less inventory was assigned to an arbitrary resource: %+v", s.Groups)
	}
}
