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
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func renderFixture() *Snapshot {
	start := time.Date(2026, 9, 1, 22, 0, 0, 0, time.UTC)
	return &Snapshot{
		Version: SchemaVersion, Currency: "USD", CostBasis: "AmortizedCost",
		CollectedAt: start.Add(48 * time.Hour), QueryStart: "2026-09-01", QueryEnd: "2026-09-03",
		Job: Job{
			Name: "e2e-cost", URL: "https://prow.example/view/job/123", BuildID: "123",
			PR: "42", Commit: "abc123", Result: "failure", StartedAt: start,
			FinishedAt: start.Add(time.Hour), InfraStartedAt: start, InfraEndedAt: start.Add(2 * time.Hour),
		},
		Groups: []Group{
			{Name: "service-rg", SubscriptionID: "sub-one", Category: "Infra", Owner: "Service Cluster", Kind: "primary", BillingStatus: "complete", Resources: []Resource{
				{ID: "/subscriptions/sub-one/resourceGroups/service-rg/providers/Compute/vms/vm", Name: "vm", Type: "Compute/vms", Source: "both", Charges: []Charge{
					{Day: "2026-09-01", MeterName: "compute", MeterID: "meter-1", MeterCategory: "VM", CostUSD: 1.75},
					{Day: "2026-09-02", MeterName: "compute", CostUSD: .5},
					{Day: "2026-09-01", MeterName: "credit", CostUSD: -.25},
				}},
				{Name: "credit", Type: "Compute/vms", Source: "billing", Charges: []Charge{{Day: "2026-09-01", CostUSD: -.5}}},
				{Name: "zero", Type: "Compute/vms", Source: "billing", Charges: []Charge{{Day: "2026-09-01", CostUSD: 0}}},
				{Name: "artifact-only", Source: "artifact"},
			}},
			{Name: "management-rg", Category: "Infra", Owner: "Management Cluster 1", BillingStatus: "complete", Resources: []Resource{
				{Name: "disk", Type: "Compute/disks", Source: "billing", Charges: []Charge{{Day: "2026-09-01", CostUSD: .125}}},
			}},
			{Name: "regional-rg", Category: "Infra", Owner: "Regional", BillingStatus: "complete"},
			{Name: "test-rg", Category: "Tests", Owner: "creates an HCP", Kind: "customer", Attempts: 2, Outcomes: []string{"failed", "passed"}, BillingStatus: "complete", Resources: []Resource{
				{Name: "network", Type: "Network/vnets", Source: "billing", Charges: []Charge{{Day: "2026-09-01", CostUSD: .375}}},
			}},
		},
	}
}

func rendered(t *testing.T, snapshot *Snapshot) string {
	t.Helper()
	var output bytes.Buffer
	if err := Render(&output, snapshot); err != nil {
		t.Fatalf("render fixture: %v", err)
	}
	return output.String()
}

func renderedInventory(t *testing.T, page string) []reportGroup {
	t.Helper()
	match := regexp.MustCompile(`(?s)<script id="inventory-data" type="application/json">(.*?)</script>`).FindStringSubmatch(page)
	if len(match) != 2 {
		t.Fatal("missing inventory JSON script")
	}
	var groups []reportGroup
	if err := json.Unmarshal([]byte(match[1]), &groups); err != nil {
		t.Fatalf("inventory must be valid JSON, not executable code: %v", err)
	}
	return groups
}

func renderedOption(t *testing.T, page string) (map[string]json.RawMessage, []*costNode) {
	t.Helper()
	match := regexp.MustCompile(`(?s)<script id="chart-option" type="application/json">(.*?)</script>`).FindStringSubmatch(page)
	if len(match) != 2 {
		t.Fatal("missing chart JSON script")
	}
	var option map[string]json.RawMessage
	if err := json.Unmarshal([]byte(match[1]), &option); err != nil {
		t.Fatalf("chart option must be valid JSON, not executable code: %v", err)
	}
	var series []struct {
		Type          string      `json:"type"`
		NodeClick     string      `json:"nodeClick"`
		LeafDepth     int         `json:"leafDepth"`
		Data          []*costNode `json:"data"`
		DrillDownIcon *string     `json:"drillDownIcon"`
	}
	if err := json.Unmarshal(option["series"], &series); err != nil {
		t.Fatal(err)
	}
	if len(series) != 1 || series[0].Type != "treemap" || series[0].NodeClick != "zoomToNode" || series[0].LeafDepth != 2 {
		t.Fatalf("expected native ECharts treemap drilldown, got %+v", series)
	}
	if series[0].DrillDownIcon == nil || *series[0].DrillDownIcon != "" {
		t.Fatal("treemap drilldown labels must not have an arrow prefix")
	}
	return option, series[0].Data
}

func TestRenderResourceTypeView(t *testing.T) {
	s := renderFixture()
	page := rendered(t, s)
	match := regexp.MustCompile(`(?s)<script id="chart-types" type="application/json">(.*?)</script>`).FindStringSubmatch(page)
	if len(match) != 2 {
		t.Fatal("missing resource type view")
	}
	var nodes []*costNode
	if err := json.Unmarshal([]byte(match[1]), &nodes); err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{}
	count := 0
	for _, g := range s.Groups {
		for _, r := range g.Resources {
			var net float64
			for _, c := range r.Charges {
				net += c.CostUSD
			}
			if net > 0 {
				want[r.Type] += net
				count++
			}
		}
	}
	if len(nodes) != len(want) {
		t.Fatalf("got %d type nodes, want %d", len(nodes), len(want))
	}
	for _, node := range nodes {
		if math.Abs(node.Value-want[node.Name]) > 1e-9 {
			t.Errorf("incorrect total for %s", node.Name)
		}
		for _, leaf := range node.Children {
			count--
			if leaf.Resource == "" || len(leaf.Children) != 0 || leaf.Value <= 0 {
				t.Errorf("invalid type resource leaf: %+v", leaf)
			}
		}
	}
	if count != 0 {
		t.Error("type view lost or duplicated resources")
	}
}

func TestRenderHierarchyAndTotals(t *testing.T) {
	page := rendered(t, renderFixture())
	_, nodes := renderedOption(t, page)
	var paths []string
	var visit func([]*costNode, string) float64
	visit = func(nodes []*costNode, path string) float64 {
		var sum float64
		for _, node := range nodes {
			if node.Value <= 0 {
				t.Fatalf("non-positive node has treemap area: %+v", node)
			}
			current := path + "/" + node.Name
			if len(node.Children) > 0 {
				if childSum := visit(node.Children, current); childSum != node.Value {
					t.Errorf("%s value %v differs from child sum %v", current, node.Value, childSum)
				}
			} else {
				paths = append(paths, current)
				if node.Resource == "" {
					t.Errorf("leaf %s missing resource detail link", current)
				}
				if node.Name == "vm" && (node.ItemStyle == nil || node.ItemStyle.BorderWidth != 4) {
					t.Error("post-job VM lacks warning outline")
				}
			}
			sum += node.Value
		}
		return sum
	}
	if sum := visit(nodes, ""); sum != 2.5 {
		t.Errorf("positive area = %v, want 2.5", sum)
	}
	want := []string{"/Infra/Service/service-rg/Compute/vms/vm", "/Infra/Management 1/management-rg/Compute/disks/disk", "/Tests/creates an HCP/test-rg/Network/vnets/network"}
	if fmt.Sprint(paths) != fmt.Sprint(want) {
		t.Errorf("hierarchy paths = %v, want %v", paths, want)
	}
	for _, text := range []string{"$2.5000", "$-0.5000", "$2.0000", "$1.0000", "$0.5000", "cost / infra-hour"} {
		if !strings.Contains(page, text) {
			t.Errorf("missing summary or detail %q", text)
		}
	}
	groups := renderedInventory(t, page)
	if groups[3].Attempts != 2 || strings.Join(groups[3].Outcomes, ", ") != "failed, passed" {
		t.Error("inventory lost attempts or outcomes")
	}
	if strings.Contains(page, "Incomplete billing:") {
		t.Error("complete fixture marked incomplete")
	}
}

func TestRenderValidation(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*Snapshot)
		want string
	}{
		{"version", func(s *Snapshot) { s.Version++ }, "version"},
		{"currency", func(s *Snapshot) { s.Currency = "EUR" }, "currency"},
		{"missing currency", func(s *Snapshot) { s.Currency = "" }, "currency"},
		{"basis", func(s *Snapshot) { s.CostBasis = "ActualCost" }, "cost basis"},
		{"category", func(s *Snapshot) { s.Groups[0].Category = "Other" }, "category"},
		{"status", func(s *Snapshot) { s.Groups[0].BillingStatus = "bogus" }, "billing status"},
		{"NaN", func(s *Snapshot) { s.Groups[0].Resources[0].Charges[0].CostUSD = math.NaN() }, "non-finite"},
		{"infinity", func(s *Snapshot) { s.Groups[0].Resources[0].Charges[0].CostUSD = math.Inf(1) }, "non-finite"},
		{"overflow", func(s *Snapshot) {
			s.Groups[0].Resources[0].Charges[0].CostUSD = math.MaxFloat64
			s.Groups[0].Resources[0].Charges[1].CostUSD = math.MaxFloat64
		}, "overflow"},
		{"day", func(s *Snapshot) { s.Groups[0].Resources[0].Charges[0].Day = "2026-02-30" }, "invalid usage day"},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := renderFixture()
			test.edit(snapshot)
			var output bytes.Buffer
			err := Render(&output, snapshot)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q error, got %v", test.want, err)
			}
			if output.Len() != 0 {
				t.Error("invalid snapshot wrote a partial report")
			}
		})
	}
	if err := Render(io.Discard, nil); err == nil {
		t.Error("nil snapshot must return an error")
	}
}

func TestRenderInjectionAndOfflineAssets(t *testing.T) {
	snapshot := renderFixture()
	payload := `</script><script>alert("injected")</script><img src=x onerror=alert(1)> &quot; __f__` + "\u2028\u2029"
	snapshot.Job.Name, snapshot.Job.PR, snapshot.Job.Commit = payload, payload, payload
	snapshot.Job.URL = "javascript:alert(1)"
	snapshot.Groups[0].Owner = payload
	snapshot.Groups[0].Attribution = payload
	snapshot.Groups[0].Resources[0].Name = payload
	snapshot.Groups[0].Resources[0].Charges[0].MeterName = payload
	snapshot.Diagnostics = []Diagnostic{{Severity: "warning", Code: payload, Scope: payload, Message: payload}}
	page := rendered(t, snapshot)
	option, nodes := renderedOption(t, page)
	groups := renderedInventory(t, page)
	if groups[0].Owner != payload || groups[0].Attribution != payload || groups[0].Resources[0].Name != payload || groups[0].Resources[0].Charges[0].MeterName != payload {
		t.Error("inventory data did not round-trip injection strings intact")
	}
	if nodes[0].Children[0].Name != payload || nodes[0].Children[0].Children[0].Children[0].Children[0].Name != payload {
		t.Error("chart data did not round-trip injection strings intact")
	}
	if strings.Contains(page, `</script><script>alert`) || strings.Contains(page, `<img src=x`) || strings.Contains(page, `href="javascript:`) {
		t.Error("unescaped HTML or executable URL in report")
	}
	if !strings.Contains(page, `\u003c/script\u003e`) || !strings.Contains(page, `&lt;/script&gt;`) {
		t.Error("expected script JSON escaping and HTML metadata escaping")
	}
	var tooltip map[string]any
	if err := json.Unmarshal(option["tooltip"], &tooltip); err != nil || tooltip["renderMode"] != "richText" {
		t.Error("tooltip must render text, never untrusted HTML")
	}
	if count := strings.Count(strings.ToLower(page), "</script>"); count != 5 {
		t.Errorf("expected exactly 5 script closing tags, got %d", count)
	}
	// The runtime contains inert HTML examples in JS strings; inspect markup,
	// not strings inside inline scripts, for external asset references.
	markup := regexp.MustCompile(`(?s)(<script\b[^>]*>).*?</script>`).ReplaceAllString(page, "$1</script>")
	for _, pattern := range []string{`(?i)<script\b[^>]*\bsrc\s*=`, `(?i)<link\b[^>]*\bhref\s*=`, `(?i)<(?:img|iframe|source)\b[^>]*\bsrc\s*=`, `(?i)@import\s`, `(?i)url\(\s*["']?https?://`} {
		if regexp.MustCompile(pattern).MatchString(markup) {
			t.Errorf("external asset matched %s", pattern)
		}
	}
	if !strings.Contains(page, echartsRuntime) || !strings.Contains(page, "Apache ECharts") || !strings.Contains(page, "The Apache Software Foundation") {
		t.Error("missing embedded runtime/license/notice")
	}
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(echartsRuntime))); got != "bf4a223524e40b77c304bec67e1222cf551f14880cf42c69dc046558e11c07b1" {
		t.Errorf("vendored ECharts 5.6.0 hash mismatch: %s", got)
	}
}

func TestRenderMissingZeroNegativeAndPartial(t *testing.T) {
	snapshot := renderFixture()
	snapshot.Groups[0].BillingStatus = "partial"
	snapshot.Groups[1].BillingStatus = "unavailable"
	snapshot.Groups[1].Resources = nil
	snapshot.Groups[2].BillingStatus = ""
	snapshot.Diagnostics = []Diagnostic{{Severity: "error", Code: "billing-failed", Scope: "management-rg", Message: "Query unavailable"}}
	page := rendered(t, snapshot)
	for _, text := range []string{"Incomplete billing: known subtotal only", "Known subtotal", "billing-failed", "partial", "unavailable", "Unknown", "No billing records (not an explicit zero)", "$-0.5000", "regional-rg", "0 (empty)", "artifact-only"} {
		if !strings.Contains(page, text) {
			t.Errorf("missing partial/inventory content %q", text)
		}
	}
	_, nodes := renderedOption(t, page)
	encoded, err := json.Marshal(nodes)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"artifact-only", "zero", "credit", "management-rg", "regional-rg"} {
		if strings.Contains(string(encoded), name) {
			t.Errorf("resource without positive net cost %q included in treemap", name)
		}
	}
}

func TestRenderResourceSubtotals(t *testing.T) {
	for _, status := range []string{"complete", "partial", "pending", "unavailable", ""} {
		t.Run("status="+status, func(t *testing.T) {
			snapshot := renderFixture()
			snapshot.Groups = snapshot.Groups[:1]
			snapshot.Groups[0].BillingStatus = status
			snapshot.Groups[0].Resources[2].Charges = []Charge{
				{Day: "2026-09-01", CostUSD: 1},
				{Day: "2026-09-02", CostUSD: -1},
			}
			group := renderedInventory(t, rendered(t, snapshot))[0]
			if group.BillingStatus != status || group.Net != 1.5 || !group.HasCharges {
				t.Fatalf("lost group billing status or subtotal: %+v", group)
			}
			for index, amount := range []float64{2, -.5, 0} {
				resource := group.Resources[index]
				if resource.Net != amount || !resource.HasCharges || resource.Anchor != fmt.Sprintf("resource-0-%d", index) {
					t.Errorf("lost resource subtotal/anchor: %+v", resource)
				}
			}
			if group.Resources[3].HasCharges {
				t.Error("missing billing records mistaken for explicit zero")
			}
		})
	}
}

func TestRenderAttributionAndCaveats(t *testing.T) {
	snapshot := renderFixture()
	note := "Billing-backed inferred RG; ownership inferred from the customer RG."
	snapshot.Groups[0].Attribution = note
	page := rendered(t, snapshot)
	if group := renderedInventory(t, page)[0]; group.Attribution != note {
		t.Error("attribution note missing from inventory data")
	}
	if strings.Count(page, note) != 1 {
		t.Error("group attribution should be serialized only once, not repeated per resource")
	}
	footer := regexp.MustCompile(`(?s)<footer>(.*?)<details>`).FindString(page)
	if !strings.Contains(footer, "Shared infrastructure costs are excluded") {
		t.Error("shared infrastructure caveat must be visible outside collapsed license details")
	}
	for _, text := range []string{"rate is approximate", "provisioning start to asynchronous cleanup completion", "including later charges outside that interval"} {
		if !strings.Contains(page, text) {
			t.Errorf("missing infra-hour caveat %q", text)
		}
	}
}

func TestRenderInfoDiagnosticsCollapsedAtBottom(t *testing.T) {
	for _, alerts := range []bool{false, true} {
		snapshot := renderFixture()
		snapshot.Diagnostics = []Diagnostic{{Severity: "info", Code: "lookup-resolved", Message: "Informational lookup detail"}}
		if alerts {
			snapshot.Diagnostics = append(snapshot.Diagnostics,
				Diagnostic{Severity: "error", Code: "lookup-failed", Message: "Actionable lookup failure"},
				Diagnostic{Severity: "warning", Code: "coverage", Message: "Coverage warning"})
		}
		page := rendered(t, snapshot)
		top := strings.SplitN(page, `<section class="panel" aria-label="Cost summary">`, 2)[0]
		if strings.Contains(top, "Informational lookup detail") || strings.Contains(top, "Collection notes") {
			t.Error("informational diagnostics must not precede the cost summary")
		}
		if strings.Contains(top, `aria-label="Collection diagnostics"`) != alerts {
			t.Error("top diagnostics section should exist only for alerts")
		}
		if alerts && (!strings.Contains(top, "Actionable lookup failure") || !strings.Contains(top, "Coverage warning")) {
			t.Error("errors and warnings must remain visible at the top")
		}
		footer := strings.SplitN(page, "<footer>", 2)[1]
		if !strings.Contains(footer, `<details aria-label="Collection notes"><summary>Collection notes (1)</summary>`) || !strings.Contains(footer, "Informational lookup detail") {
			t.Error("info diagnostics must be preserved in collapsed footer details")
		}
	}
}

func TestRenderEmptyAndUnavailable(t *testing.T) {
	for _, diagnostics := range [][]Diagnostic{nil, {{Severity: "error", Code: "unsupported-billing", Message: "Subscription does not support billing"}}} {
		snapshot := &Snapshot{Version: SchemaVersion, Currency: "USD", Diagnostics: diagnostics}
		page := rendered(t, snapshot)
		for _, text := range []string{"No billing records available", "No resource groups discovered", "No resources discovered", "No positive net resource costs", "Unavailable"} {
			if !strings.Contains(page, text) {
				t.Errorf("empty report missing %q", text)
			}
		}
		if strings.Contains(page, "cost / infra-hour") || strings.Contains(page, `id="treemap"`) || strings.Contains(page, "$0.0000") {
			t.Error("empty report invented zero costs, infra-hour rate, or chart")
		}
	}
	for _, cost := range []float64{0, -1} {
		snapshot := renderFixture()
		snapshot.Groups = snapshot.Groups[:1]
		snapshot.Groups[0].Resources = snapshot.Groups[0].Resources[:1]
		snapshot.Groups[0].Resources[0].Charges = []Charge{{Day: "2026-09-01", CostUSD: cost}}
		page := rendered(t, snapshot)
		if strings.Contains(page, `id="treemap"`) || strings.Contains(page, "No billing records available") {
			t.Errorf("recorded cost %v should show totals but no chart", cost)
		}
	}
}

func TestRenderPostJobUTCAndMetadata(t *testing.T) {
	snapshot := renderFixture()
	// This local finish date is Sep 2, but its UTC day is Sep 1.
	snapshot.Job.FinishedAt = time.Date(2026, 9, 2, 1, 0, 0, 0, time.FixedZone("UTC+2", 2*60*60))
	page := rendered(t, snapshot)
	for _, text := range []string{"1 resource(s)", "$0.5000 net usage", "2026-09-01 23:00:00 UTC", "1h0m0s", "2026-09-01 through 2026-09-03", "2026-09-03 22:00:00 UTC", "PR 42", "abc123", `href="https://prow.example/view/job/123"`, "Amortized cost / USD"} {
		if !strings.Contains(page, text) {
			t.Errorf("missing UTC or job metadata %q", text)
		}
	}
	snapshot.Job.FinishedAt = time.Time{}
	snapshot.Job.InfraEndedAt = time.Time{}
	page = rendered(t, snapshot)
	if strings.Contains(page, `aria-label="Post-job usage"`) || strings.Contains(page, "cost / infra-hour") {
		t.Error("missing times should not fabricate post-job usage or infra-hour rate")
	}
}

func TestRenderSeparateSubscriptionsAndStableOutput(t *testing.T) {
	snapshot := renderFixture()
	other := snapshot.Groups[0]
	other.SubscriptionID = "sub-two"
	snapshot.Groups = append(snapshot.Groups, other)
	page := rendered(t, snapshot)
	_, nodes := renderedOption(t, page)
	service := nodes[0].Children[0]
	if len(service.Children) != 2 || service.Children[0].Value != 2 || service.Children[1].Value != 2 {
		t.Fatalf("same-name RGs in different subscriptions must stay separate: %+v", service)
	}
	if service.Children[0].Children[0].Children[0].Resource == service.Children[1].Children[0].Children[0].Resource {
		t.Error("resource detail anchors collide across subscriptions")
	}
	for i, subscription := range []string{"sub-one", "sub-two"} {
		if want := "service-rg [" + subscription + "]"; service.Children[i].Name != want {
			t.Errorf("ambiguous chart label/tooltip: got %q, want %q", service.Children[i].Name, want)
		}
	}
	if again := rendered(t, snapshot); again != page {
		t.Error("rendering the same snapshot is not deterministic")
	}
	if snapshot.Groups[0].Owner != "Service Cluster" || snapshot.Groups[0].Resources[3].Type != "" {
		t.Error("rendering modified the shared snapshot")
	}
}

type failedReportWriter struct{ err error }

func (w failedReportWriter) Write([]byte) (int, error) { return 0, w.err }

func TestRenderWriterError(t *testing.T) {
	want := errors.New("write failed")
	if err := Render(failedReportWriter{want}, renderFixture()); !errors.Is(err, want) {
		t.Errorf("writer failure not propagated: %v", err)
	}
}

func TestRenderLazyInventory(t *testing.T) {
	snapshot := renderFixture()
	snapshot.Groups = snapshot.Groups[:1]
	snapshot.Groups[0].Attribution = "Unique group attribution stored once"
	resource := snapshot.Groups[0].Resources[0]
	snapshot.Groups[0].Resources = make([]Resource, 600)
	for i := range snapshot.Groups[0].Resources {
		snapshot.Groups[0].Resources[i] = resource
	}
	page := rendered(t, snapshot)
	markup := strings.SplitN(page, "<script>", 2)[0]
	for _, forbidden := range []string{`class="resource `, `data-resource=`, `<tbody`, `<template`} {
		if strings.Contains(markup, forbidden) {
			t.Errorf("initial HTML contains eagerly rendered inventory: %s", forbidden)
		}
	}
	if count := strings.Count(markup, "<"); count > 500 {
		t.Fatalf("initial HTML must remain bounded, got %d markup tags", count)
	}
	for _, text := range []string{"All resources (600 resources)", "Resource group inventory (1 groups)", "JavaScript is required"} {
		if !strings.Contains(markup, text) {
			t.Errorf("missing inventory count or noscript guidance %q", text)
		}
	}
	groups := renderedInventory(t, page)
	if len(groups) != 1 || len(groups[0].Resources) != 600 || groups[0].Resources[599].Anchor != "resource-0-599" {
		t.Fatal("lazy inventory lost off-page resources or stable anchors")
	}
	if strings.Count(page, snapshot.Groups[0].Attribution) != 1 {
		t.Error("group fields duplicated for each resource")
	}
}

func residualRenderFixture() *Snapshot {
	s := renderFixture()
	s.Mode = "subscriptions"
	s.Groups = s.Groups[:1]
	g := &s.Groups[0]
	g.SubscriptionID = "11111111-1111-1111-1111-111111111111"
	g.Owner, g.Category, g.Kind = g.SubscriptionID, "Residual", "retained"
	g.Attribution = "Shared infrastructure retained; no e2e naming filter matched."
	s.Subscriptions = []SubscriptionSummary{{ID: g.SubscriptionID, BillingStatus: "complete", TotalUSD: 4, RetainedUSD: 1.5, ExcludedUSD: 2.5}}
	s.FilteredGroups = []FilteredGroup{{SubscriptionID: g.SubscriptionID, Name: "e2e-test-rg", Reason: "matched ^e2e-.*$", CostUSD: 2.5}}
	return s
}

func TestRenderSubscriptions(t *testing.T) {
	s := residualRenderFixture()
	other := s.Groups[0]
	other.SubscriptionID = "22222222-2222-2222-2222-222222222222"
	other.Owner = other.SubscriptionID
	s.Groups = append(s.Groups, other)
	s.Subscriptions = append(s.Subscriptions, SubscriptionSummary{ID: other.SubscriptionID, BillingStatus: "complete", TotalUSD: 1.5, RetainedUSD: 1.5})
	s.ExcludedGroups = []string{"job-only-exclusion"}
	s.Diagnostics = []Diagnostic{{Severity: "info", Code: "filter-note", Message: "Residual collection detail"}}
	page := rendered(t, s)
	markup := strings.SplitN(page, "<script>", 2)[0]
	for _, text := range []string{
		"Subscription residual costs", "Net retained cost", "$4.0000", "$-1.0000", "$3.0000",
		"Residual means not matched by e2e naming filters; may include unrecognized e2e resources. Shared infrastructure is retained, not allocated to jobs.",
		"By subscription / resource group", "Subscription reconciliation", "Total USD", "Excluded USD", "Retained USD",
		"2026-09-01 through 2026-09-03", "2026-09-03 22:00:00 UTC", "Amortized cost / USD",
	} {
		if !strings.Contains(markup, text) {
			t.Errorf("missing subscription report content %q", text)
		}
	}
	for _, text := range []string{"Prow job", s.Job.URL, "Job started", "Job finished", "Job duration", "Infrastructure start", "cost / infra-hour", "Post-job usage", "By infrastructure / test", "job-only-exclusion", "Shared infrastructure costs are excluded", "Incomplete billing:"} {
		if strings.Contains(markup, text) {
			t.Errorf("subscription report contains job-only or misleading content %q", text)
		}
	}
	_, nodes := renderedOption(t, page)
	if len(nodes) != 2 {
		t.Fatalf("expected two subscription roots, got %+v", nodes)
	}
	for i, root := range nodes {
		if root.Name != s.Subscriptions[i].ID || root.Value != 2 || len(root.Children) != 1 {
			t.Fatalf("invalid subscription root: %+v", root)
		}
		rg := root.Children[0]
		if rg.Name != "service-rg" || rg.Children[0].Name != "Compute/vms" || rg.Children[0].Children[0].Name != "vm" || rg.Children[0].Children[0].ItemStyle != nil {
			t.Fatalf("expected subscription/RG/type/resource without post-job warning: %+v", rg)
		}
	}
	match := regexp.MustCompile(`(?s)<script id="chart-types" type="application/json">(.*?)</script>`).FindStringSubmatch(page)
	var types []*costNode
	if len(match) != 2 || json.Unmarshal([]byte(match[1]), &types) != nil || len(types) != 1 || types[0].Value != 4 || len(types[0].Children) != 2 {
		t.Fatal("resource type view must combine all subscriptions")
	}
	for _, g := range renderedInventory(t, page) {
		if g.Category != "Residual" || g.Kind != "retained" || g.Attribution == "" || g.Resources[0].PostJob {
			t.Errorf("lost residual attribution or invented post-job usage: %+v", g)
		}
	}
	footer := strings.SplitN(markup, "<footer>", 2)[1]
	if !strings.Contains(footer, `<details aria-label="Collection notes">`) || !strings.Contains(footer, "Residual collection detail") {
		t.Error("collection notes must stay collapsed at the bottom")
	}
	if again := rendered(t, s); again != page {
		t.Error("subscription rendering is not deterministic")
	}
}

func TestRenderSubscriptionNames(t *testing.T) {
	for _, name := range []string{"", "Shared subscription", `</script><script>window.injected=true</script><img src=x onerror=alert(1)> & "` + "\u2028\u2029"} {
		t.Run(name, func(t *testing.T) {
			s := residualRenderFixture()
			s.Subscriptions[0].DisplayName = name
			other := SubscriptionSummary{ID: "22222222-2222-2222-2222-222222222222", DisplayName: name, BillingStatus: "complete"}
			s.Subscriptions = append(s.Subscriptions, other)
			encoded, err := json.Marshal(s)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), `"displayName"`) != (name != "") {
				t.Fatal("missing display names must be omitted for old snapshots")
			}
			var persisted Snapshot
			if err := json.Unmarshal(encoded, &persisted); err != nil {
				t.Fatal(err)
			}
			page := rendered(t, &persisted)
			for _, sub := range s.Subscriptions {
				cell := "<td>" + sub.ID + "</td>"
				if name != "" {
					cell = "<td><strong>" + html.EscapeString(name) + "</strong><br>" + sub.ID + "</td>"
				}
				if !strings.Contains(page, cell) {
					t.Fatalf("missing bold name above distinct UUID (or UUID fallback): %s", cell)
				}
			}
			match := regexp.MustCompile(`(?s)<script id="subscription-data" type="application/json">(.*?)</script>`).FindStringSubmatch(page)
			var subs []SubscriptionSummary
			if len(match) != 2 || json.Unmarshal([]byte(match[1]), &subs) != nil || len(subs) != 2 || subs[0].DisplayName != name || subs[1].DisplayName != name || subs[0].ID == subs[1].ID {
				t.Fatal("subscription names must round trip without merging same-name subscriptions")
			}
			if strings.Contains(match[1], "\n") || strings.Contains(page, "</script><script>window.injected") || strings.Contains(page, "<img src=x") || strings.Count(page, "</script>") != 7 {
				t.Fatal("subscription JSON must be compact and HTML escaped")
			}
			for _, id := range []string{"inventory-data", "filtered-data"} {
				data := regexp.MustCompile(`(?s)<script id="` + id + `" type="application/json">(.*?)</script>`).FindStringSubmatch(page)
				if len(data) != 2 || strings.Contains(data[1], `"displayName"`) {
					t.Fatal("subscription metadata duplicated in group/resource data")
				}
			}
		})
	}
}

func TestRenderSubscriptionsEmptyAndPartial(t *testing.T) {
	for _, status := range []string{"complete", "partial", "pending", "unavailable", ""} {
		t.Run(status, func(t *testing.T) {
			s := residualRenderFixture()
			s.Groups, s.FilteredGroups = nil, nil
			s.Subscriptions = []SubscriptionSummary{{ID: s.Subscriptions[0].ID, BillingStatus: status}}
			page := rendered(t, s)
			if strings.Contains(page, "Incomplete billing:") != (status != "complete") {
				t.Fatal("subscription status alone must determine incompleteness with no groups")
			}
			if strings.Contains(page, "No billing records available") != (status != "complete") || !strings.Contains(page, "No resource groups discovered") || strings.Contains(page, `id="treemap"`) {
				t.Fatal("empty subscription lost query status or invented chart")
			}
			if status == "complete" && !strings.Contains(page, "Net retained cost</span><strong>$0.0000") {
				t.Fatal("successful empty subscription query should show retained zero")
			}
			if status != "complete" && !strings.Contains(page, "Known subtotal") {
				t.Fatal("incomplete reconciliation must be labeled known subtotal")
			}
			if (status == "unavailable" || status == "pending" || status == "") && strings.Contains(page, "$0.0000") {
				t.Fatal("failed subscription invented explicit zeros")
			}
		})
	}
	t.Run("no subscriptions", func(t *testing.T) {
		s := &Snapshot{Version: SchemaVersion, Mode: "subscriptions", Currency: "USD"}
		page := rendered(t, s)
		if !strings.Contains(page, "No subscription summaries available") || strings.Contains(page, "$0.0000") {
			t.Fatal("missing subscription summaries invented zero")
		}
		s.Diagnostics = []Diagnostic{{Severity: "error", Code: "billing-failed", Message: "Failed before inventory"}}
		if !strings.Contains(rendered(t, s), "Incomplete billing:") {
			t.Fatal("diagnostic alone must mark subscription report incomplete")
		}
	})
	for _, amount := range []float64{0, -1, 2} {
		s := residualRenderFixture()
		s.FilteredGroups = nil
		s.Groups[0].Kind = "unassigned"
		s.Groups[0].Name = "Unknown resource group"
		s.Groups[0].Resources = []Resource{{Charges: []Charge{{Day: "2026-09-01", CostUSD: amount}}}}
		s.Subscriptions[0].TotalUSD, s.Subscriptions[0].RetainedUSD, s.Subscriptions[0].ExcludedUSD = amount, amount, 0
		page := rendered(t, s)
		g := renderedInventory(t, page)[0]
		if strings.Contains(page, "No billing records available") || !strings.Contains(page, fmt.Sprintf("$%.4f", amount)) || g.Resources[0].ID != "" || g.Resources[0].Name != "Unattributed charge" || g.Resources[0].Type != "Unknown type" {
			t.Fatalf("unassigned charge lost signed amount or fabricated ID: %+v", g)
		}
	}
	t.Run("all excluded", func(t *testing.T) {
		s := residualRenderFixture()
		s.Groups = nil
		s.Subscriptions[0].TotalUSD, s.Subscriptions[0].RetainedUSD = 2.5, 0
		page := rendered(t, s)
		if !strings.Contains(page, "Net retained cost</span><strong>$0.0000") || strings.Contains(page, "No billing records available") {
			t.Fatal("recorded exclusions should establish retained zero")
		}
	})
	t.Run("partial amounts", func(t *testing.T) {
		s := residualRenderFixture()
		s.Subscriptions[0].BillingStatus = "partial"
		s.Groups[0].BillingStatus = "partial"
		page := rendered(t, s)
		if !strings.Contains(page, "Net retained cost (Known subtotal)") || !strings.Contains(page, "$1.5000") {
			t.Fatal("partial signed totals must remain visible and qualified")
		}
	})
}

func TestRenderSubscriptionsValidation(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*Snapshot)
		want string
	}{
		{"mode", func(s *Snapshot) { s.Mode = "unknown" }, "mode"},
		{"job category", func(s *Snapshot) { s.Groups[0].Category = "Infra" }, "category"},
		{"residual in job", func(s *Snapshot) { s.Mode = "" }, "category"},
		{"status", func(s *Snapshot) { s.Subscriptions[0].BillingStatus = "failed" }, "billing status"},
		{"total NaN", func(s *Snapshot) { s.Subscriptions[0].TotalUSD = math.NaN() }, "non-finite"},
		{"retained infinity", func(s *Snapshot) { s.Subscriptions[0].RetainedUSD = math.Inf(1) }, "non-finite"},
		{"excluded infinity", func(s *Snapshot) { s.Subscriptions[0].ExcludedUSD = math.Inf(-1) }, "non-finite"},
		{"filtered NaN", func(s *Snapshot) { s.FilteredGroups[0].CostUSD = math.NaN() }, "non-finite"},
		{"filtered infinity", func(s *Snapshot) { s.FilteredGroups[0].CostUSD = math.Inf(1) }, "non-finite"},
		{"total mismatch", func(s *Snapshot) { s.Subscriptions[0].TotalUSD++ }, "reconciliation"},
		{"retained mismatch", func(s *Snapshot) { s.Groups[0].Resources[0].Charges[0].CostUSD++ }, "reconciliation"},
		{"excluded mismatch", func(s *Snapshot) { s.FilteredGroups[0].CostUSD++ }, "reconciliation"},
		{"partial mismatch", func(s *Snapshot) { s.Subscriptions[0].BillingStatus = "partial"; s.Subscriptions[0].TotalUSD++ }, "reconciliation"},
		{"duplicate subscription", func(s *Snapshot) { s.Subscriptions = append(s.Subscriptions, s.Subscriptions[0]) }, "duplicate subscription"},
		{"unknown retained subscription", func(s *Snapshot) { s.Groups[0].SubscriptionID = "unknown" }, "unknown subscription"},
		{"unknown filtered subscription", func(s *Snapshot) { s.FilteredGroups[0].SubscriptionID = "unknown" }, "unknown subscription"},
		{"overflow", func(s *Snapshot) {
			s.Subscriptions[0].ExcludedUSD, s.Subscriptions[0].RetainedUSD = math.MaxFloat64, math.MaxFloat64
		}, "reconciliation"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := residualRenderFixture()
			test.edit(s)
			var output bytes.Buffer
			err := Render(&output, s)
			if err == nil || !strings.Contains(err.Error(), test.want) || output.Len() != 0 {
				t.Fatalf("expected atomic %q failure, got %v (%d output bytes)", test.want, err, output.Len())
			}
		})
	}
	s := residualRenderFixture()
	s.Subscriptions[0].TotalUSD += 1e-8
	rendered(t, s)
}

func TestRenderFilteredAuditLazyAndEscaped(t *testing.T) {
	s := residualRenderFixture()
	payload := `^e2e-[a-z]+$ </script><script>window.injected=true</script><img src=x onerror=alert(1)> & "` + "\u2028\u2029"
	s.Groups[0].Attribution = payload
	s.FilteredGroups = make([]FilteredGroup, 5001)
	for i := range s.FilteredGroups {
		s.FilteredGroups[i] = FilteredGroup{SubscriptionID: s.Subscriptions[0].ID, Name: fmt.Sprintf("filtered-%d", i), Reason: "matched ^e2e-[a-z]+$", CostUSD: .5}
	}
	s.FilteredGroups[5000].Name, s.FilteredGroups[5000].Reason = payload, payload
	s.Subscriptions[0].ExcludedUSD = 2500.5
	s.Subscriptions[0].TotalUSD = 2502
	page := rendered(t, s)
	markup := strings.SplitN(page, "<script>", 2)[0]
	if !strings.Contains(markup, `<details id="filtered-inventory"`) || !strings.Contains(markup, "Filtered resource groups (5001 groups)") || strings.Contains(markup, "filtered-4999") || strings.Count(markup, "<") > 500 {
		t.Fatal("audit must be collapsed and not prerender thousands of rows")
	}
	match := regexp.MustCompile(`(?s)<script id="filtered-data" type="application/json">(.*?)</script>`).FindStringSubmatch(page)
	var groups []FilteredGroup
	if len(match) != 2 || json.Unmarshal([]byte(match[1]), &groups) != nil || len(groups) != 5001 || groups[5000].Reason != payload || groups[5000].Name != payload || groups[5000].CostUSD != .5 {
		t.Fatal("filtered audit JSON did not preserve all rows and hostile strings")
	}
	if strings.Count(page, `"name":"filtered-4999"`) != 1 || strings.Count(page, `id="filtered-data"`) != 1 {
		t.Error("audit data must be serialized only once")
	}
	if strings.Contains(page, "</script><script>window.injected") || strings.Contains(page, "<img src=x") || strings.Count(page, "</script>") != 7 {
		t.Error("hostile audit data escaped JSON script")
	}
	if renderedInventory(t, page)[0].Attribution != payload {
		t.Error("hostile retention attribution did not round trip")
	}
	for _, code := range []string{`lazy("filtered-inventory"`, `filtered, 25, "filtered groups"`, `index[i].includes(query)`, `node("td", g.reason, row)`} {
		if !strings.Contains(page, code) {
			t.Errorf("missing lazy, bounded, literal-text audit behavior %q", code)
		}
	}
}

func TestRenderSubscriptionNameFallbackBrowser(t *testing.T) {
	if os.Getenv("E2E_COST_BROWSER") == "" {
		t.Skip("set E2E_COST_BROWSER to run desktop/mobile browser checks")
	}
	s := residualRenderFixture()
	s.Subscriptions[0].DisplayName = "Same subscription name"
	for i, name := range []string{"Same subscription name", ""} {
		id := fmt.Sprintf("%08d-aaaa-aaaa-aaaa-aaaaaaaaaaaa", i+2)
		s.Subscriptions = append(s.Subscriptions, SubscriptionSummary{ID: id, DisplayName: name, BillingStatus: "complete"})
		s.FilteredGroups = append(s.FilteredGroups, FilteredGroup{SubscriptionID: strings.ToUpper(id), Name: "excluded group", Reason: "test exclusion"})
	}
	check := `<script>
(async () => {
try {
  const wait = () => new Promise(resolve => setTimeout(resolve, 220));
  const assert = (condition, message) => { if (!condition) throw Error(message); };
  const subs = JSON.parse(document.getElementById("subscription-data").textContent);
  const checkCells = cells => {
    assert(cells.length === 3, "same-name subscriptions merged");
    cells.forEach((cell, i) => {
      assert(cell.textContent.toLowerCase().endsWith(subs[i].id), "subscription UUID lost");
      const name = cell.querySelector("strong");
      if (subs[i].displayName) assert(name && name.textContent === subs[i].displayName, "duplicate display name lost");
      else assert(!name && !cell.querySelector("br") && cell.textContent.toLowerCase() === subs[i].id, "missing name must show UUID only");
    });
  };
  checkCells([...document.querySelectorAll('[aria-label="Subscription reconciliation"] tbody tr')].map(row => row.cells[0]));
  document.getElementById("filtered-inventory").open = true;
  await wait();
  checkCells([...document.querySelectorAll("#filtered-browser tbody tr")].map(row => row.cells[0]));
  const search = async value => {
    const input = document.getElementById("filtered-search");
    input.value = value;
    input.dispatchEvent(new Event("input"));
    await wait();
  };
  await search("same subscription name");
  assert(document.getElementById("filtered-matches").textContent === "2 of 3 filtered groups | 1-2", "same-name search merged or mismatched IDs");
  await search(subs[1].id);
  assert(document.getElementById("filtered-matches").textContent === "1 of 3 filtered groups | 1-1", "UUID search must disambiguate names");
  await search(subs[2].id);
  assert(document.getElementById("filtered-matches").textContent === "1 of 3 filtered groups | 1-1", "nameless subscription not searchable");
  assert(!document.querySelector("#filtered-browser strong"), "fallback inherited another subscription's name");
  assert(document.documentElement.scrollWidth <= window.innerWidth, "mobile horizontal overflow");
  document.body.dataset.browserCheck = "passed";
} catch (error) { document.body.dataset.browserCheck = String(error); }
})();
</script>`
	runRenderBrowser(t, rendered(t, s), check)
}

func TestRenderSubscriptionsBrowser(t *testing.T) {
	if os.Getenv("E2E_COST_BROWSER") == "" {
		t.Skip("set E2E_COST_BROWSER to run desktop/mobile browser checks")
	}
	s := residualRenderFixture()
	s.Groups[0].Attribution = `Retained </script><img src=x onerror="window.injected=true">`
	s.Subscriptions[0].DisplayName = `Shared subscription </script><img src=x onerror="window.injected=true">`
	s.FilteredGroups = make([]FilteredGroup, 5001)
	for i := range s.FilteredGroups {
		s.FilteredGroups[i] = FilteredGroup{SubscriptionID: s.Subscriptions[0].ID, Name: fmt.Sprintf("filtered-%d", i), Reason: "matched ^e2e-[a-z]+$", CostUSD: .5}
	}
	s.FilteredGroups[5000].Reason = `unique reason .* [ </script><img src=x onerror="window.injected=true">`
	s.FilteredGroups[5000].CostUSD = -.25
	s.Subscriptions[0].ExcludedUSD, s.Subscriptions[0].TotalUSD = 2499.75, 2501.25
	page := rendered(t, s)
	check := `<script>
(async () => {
try {
  const wait = () => new Promise(resolve => setTimeout(resolve, 220));
  const assert = (condition, message) => { if (!condition) throw Error(message); };
  const rows = () => document.querySelectorAll("#filtered-browser tbody tr");
  const buttons = () => document.querySelectorAll("#filtered-browser .pager button");
  const search = async value => {
    const input = document.getElementById("filtered-search");
    input.value = value;
    input.dispatchEvent(new Event("input"));
    await wait();
  };
  assert(!document.querySelector("#filtered-browser table, #group-browser table, #resource-browser table, details.resource"), "inventory mounted eagerly");
  assert(document.querySelectorAll("*").length < 350, "initial DOM unbounded");
  const summaryCell = document.querySelector('[aria-label="Subscription reconciliation"] tbody td');
  const displayName = JSON.parse(document.getElementById("subscription-data").textContent)[0].displayName;
  assert(summaryCell.querySelector("strong").textContent === displayName, "summary name missing or not bold");
  assert(summaryCell.querySelector("strong").nextElementSibling.tagName === "BR" && summaryCell.textContent.endsWith("11111111-1111-1111-1111-111111111111"), "summary name must appear above UUID");
  assert(!document.querySelector('[aria-label="Subscription reconciliation"] img'), "summary name injection");
  const chart = echarts.getInstanceByDom(document.getElementById("treemap"));
  const series = chart.getModel().getSeriesByIndex(0);
  const subscription = series.getData().tree.root.children[0];
  assert(subscription.name === "11111111-1111-1111-1111-111111111111", "fake job root");
  assert(subscription.children[0].name === "service-rg", "missing RG hierarchy");
  chart.dispatchAction({type: "treemapRootToNode", seriesIndex: 0, targetNode: subscription.children[0]});
  assert(series.getViewRoot().name === "service-rg", "subscription drilldown failed");
  chart.trigger("click", {data: {resource: "resource-0-0"}});
  assert(document.querySelectorAll("details.resource").length === 1, "selected resource missing");
  assert(!document.getElementById("selected-resource").textContent.includes("Post-job usage"), "fabricated post-job usage");
  assert(!document.querySelector("#selected-resource img, #selected-resource script"), "retention reason injection");
  const grouping = document.getElementById("chart-grouping");
  grouping.value = "type";
  grouping.dispatchEvent(new Event("change"));
  assert(chart.getModel().getSeriesByIndex(0).get("leafDepth") === 1, "type grouping failed");
  const shell = document.getElementById("filtered-inventory");
  assert(!shell.open, "audit not collapsed");
  shell.open = true;
  await wait();
  assert(rows().length === 25 && buttons()[0].disabled, "first audit page not bounded");
  const subscriptionCell = rows()[0].cells[0];
  assert(subscriptionCell.querySelector("strong").textContent === displayName, "audit name missing or not bold");
  assert(subscriptionCell.querySelector("strong").nextElementSibling.tagName === "BR" && subscriptionCell.querySelector("span").textContent === "11111111-1111-1111-1111-111111111111", "audit name must appear above UUID");
  assert(document.getElementById("filtered-matches").textContent === "5001 of 5001 filtered groups | 1-25", "audit count wrong");
  buttons()[1].click();
  assert(rows()[0].textContent.includes("filtered-25"), "audit next failed");
  buttons()[0].click();
  assert(rows()[0].textContent.includes("filtered-0"), "audit prev failed");
  for (let i = 0; i < 200; i++) buttons()[1].click();
  assert(rows().length === 1 && buttons()[1].disabled, "audit last page failed");
  assert(rows()[0].textContent.includes("filtered-5000") && rows()[0].textContent.includes("$-0.2500"), "last audit row lost");
  assert(!window.injected && !document.querySelector("#filtered-browser img, #filtered-browser script"), "audit injection executed");
  await search(".* [");
  assert(document.getElementById("filtered-matches").textContent === "1 of 5001 filtered groups | 1-1", "reason search interpreted regex");
  await search("^e2e-[a-z]+$");
  assert(document.getElementById("filtered-matches").textContent === "5000 of 5001 filtered groups | 1-25", "regex reason text not searchable");
  await search("$-0.2500");
  assert(rows()[0].textContent.includes("filtered-5000"), "cost search failed");
  await search("11111111-1111-1111-1111-111111111111");
  assert(rows().length === 25 && buttons()[0].disabled, "subscription search failed");
  await search("SHARED SUBSCRIPTION");
  assert(document.getElementById("filtered-matches").textContent === "5001 of 5001 filtered groups | 1-25", "subscription display name not searchable");
  await search("no-such-exclusion");
  assert(buttons()[0].disabled && buttons()[1].disabled && rows()[0].textContent.includes("No matching"), "empty search failed");
  await search("filtered-4999");
  assert(rows().length === 1 && rows()[0].textContent.includes("filtered-4999"), "offpage name search failed");
  await search("");
  shell.open = false;
  await wait();
  shell.open = true;
  await wait();
  assert(document.querySelectorAll("#filtered-browser table").length === 1 && rows().length === 25, "audit duplicated on reopen");
  assert(!document.querySelector("#group-browser table, #resource-browser table"), "audit mounted unrelated inventory");
  assert(document.querySelectorAll("*").length < 700, "audit browsing grew DOM");
  assert(document.documentElement.scrollWidth <= window.innerWidth, "mobile horizontal overflow");
  document.body.dataset.browserCheck = "passed";
} catch (error) { document.body.dataset.browserCheck = String(error); }
})();
</script>`
	runRenderBrowser(t, page, check)
}

// Opt in with E2E_COST_BROWSER=/path/to/chrome for a real offline browser check.
func TestRenderBrowser(t *testing.T) {
	browser := os.Getenv("E2E_COST_BROWSER")
	if browser == "" {
		t.Skip("set E2E_COST_BROWSER to run desktop/mobile browser checks")
	}
	snapshot := renderFixture()
	snapshot.Groups[0].Resources[0].Name = `</script><script>window.injected = true</script>`
	snapshot.Groups[0].Resources[0].Charges[0].MeterName = `<img src=x onerror="window.injected = true">`
	snapshot.Job.Name = `<img src=x onerror="window.injected = true">`
	snapshot.Job.Name += strings.Repeat("UnbrokenJobName", 30)
	snapshot.Groups[0].Resources[0].Name += strings.Repeat("UnbrokenResourceName", 30)
	snapshot.Groups[0].Attribution = "Billing-backed inferred RG"
	for gi := range 30 {
		group := Group{Name: fmt.Sprintf("stress-group-%d", gi), SubscriptionID: "stress-sub", Category: "Tests", Owner: "stress owner", Kind: "customer", Attempts: 3, Outcomes: []string{"failed", "passed"}, BillingStatus: []string{"complete", "partial", "pending", "unavailable", ""}[gi%5]}
		for ri := range 20 {
			resource := Resource{Name: fmt.Sprintf("stress-resource-%d-%d", gi, ri), Type: "Stress/type", Source: "billing"}
			if ri%4 != 3 {
				resource.Charges = []Charge{{Day: "2026-09-01", CostUSD: []float64{0, -1, 1}[ri%4]}}
			}
			group.Resources = append(group.Resources, resource)
		}
		snapshot.Groups = append(snapshot.Groups, group)
	}
	last := &snapshot.Groups[33]
	last.Attribution = "unique offpage attribution"
	for i := range 123 {
		last.Resources[19].Charges = append(last.Resources[19].Charges, Charge{Day: "2026-09-02", MeterName: fmt.Sprintf("offpage-meter-%d", i), MeterID: fmt.Sprintf("meter-id-%d", i), MeterCategory: "Stress category", CostUSD: .25})
	}
	page := rendered(t, snapshot)
	check := `<script>
(async () => {
try {
  const wait = () => new Promise(resolve => setTimeout(resolve, 220));
  const assert = (condition, message) => { if (!condition) throw Error(message); };
  const rows = id => document.querySelectorAll("#" + id + " tbody tr");
  const next = id => document.querySelectorAll("#" + id + " .pager button")[1];
  const prev = id => document.querySelector("#" + id + " .pager button");
  const searchFor = async (id, value) => {
    const input = document.getElementById(id);
    input.value = value;
    input.dispatchEvent(new Event("input"));
    await wait();
  };
  assert(!document.querySelector("details.resource"), "initial resource details mounted");
  assert(!document.querySelector("tbody"), "initial inventory mounted");
  assert(document.querySelectorAll("*").length < 300, "initial DOM not bounded");
  if (window.injected || document.querySelector("h1 img")) throw Error("injected markup executed");
  const chart = echarts.getInstanceByDom(document.getElementById("treemap"));
  if (!chart || !document.querySelector("#treemap canvas")) throw Error("chart not initialized");
  const series = chart.getModel().getSeriesByIndex(0);
  const root = series.getData().tree.root;
  if (!series.get("breadcrumb.show")) throw Error("native breadcrumbs disabled");
  const target = root.children[0].children[0];
  chart.dispatchAction({type: "treemapRootToNode", seriesIndex: 0, targetNode: target});
  if (series.getViewRoot().name !== "Service") throw Error("native drilldown failed");
  chart.dispatchAction({type: "treemapRootToNode", seriesIndex: 0, targetNode: root});
  chart.trigger("click", {data: {resource: "resource-33-19"}});
  assert(document.getElementById("resource-33-19").open, "offpage chart details did not open");
  assert(!document.getElementById("resource-inventory").open && !document.querySelector("#resource-browser table"), "chart mounted inventory");
  assert(rows("charges").length === 50, "meter pagination not bounded");
  assert(document.querySelector("#charges .pager p").textContent === "123 of 123 meter rows | 1-50", "meter count missing");
  next("charges").click();
  assert(rows("charges").length === 50 && rows("charges")[0].textContent.includes("offpage-meter-50"), "meter next failed");
  next("charges").click();
  assert(rows("charges").length === 23 && next("charges").disabled, "meter last page failed");
  assert(document.getElementById("charges").textContent.includes("meter-id-122"), "last meter ID missing");
  prev("charges").click();
  assert(rows("charges").length === 50, "meter prev failed");
  assert(document.getElementById("selected-resource").textContent.includes("Stress category"), "meter category missing");
  const grouping = document.getElementById("chart-grouping");
  grouping.value = "type";
  grouping.dispatchEvent(new Event("change"));
  assert(chart.getModel().getSeriesByIndex(0).get("leafDepth") === 1, "type view switch failed");
  chart.trigger("click", {data: {resource: "resource-0-0"}});
  assert(document.getElementById("resource-0-0").open && !document.getElementById("resource-33-19"), "selected details not replaced");
  assert(document.querySelector("#resource-0-0 .badge") && document.querySelectorAll("details.resource").length === 1, "post-job badge or single panel failed");
  assert(!window.injected && !document.querySelector("#selected-resource script, #selected-resource img"), "detail injection executed");
  const vm = document.getElementById("selected-resource").textContent;
  for (const text of ["/subscriptions/sub-one/resourceGroups/service-rg/providers/Compute/vms/vm", "Service Cluster", "primary", "Billing-backed inferred RG", "Compute/vms / both", "$-0.2500", "meter-1", "2026-09-02"]) {
    assert(vm.includes(text), "missing VM detail " + text);
  }
  document.getElementById("resource-inventory").open = true;
  document.getElementById("group-inventory").open = true;
  await wait();
  assert(rows("resource-browser").length === 50 && rows("group-browser").length === 25, "inventory page limits failed");
  assert(prev("resource-browser").disabled && prev("group-browser").disabled, "first page Prev enabled");
  next("resource-browser").click();
  assert(rows("resource-browser").length === 50 && rows("resource-browser")[0].dataset.resource === "resource-6-4", "resource next failed");
  prev("resource-browser").click();
  assert(rows("resource-browser")[0].dataset.resource === "resource-0-0", "resource prev failed");
  for (let i = 0; i < 12; i++) next("resource-browser").click();
  assert(rows("resource-browser").length === 6 && next("resource-browser").disabled, "resource last page failed");
  next("group-browser").click();
  assert(rows("group-browser").length === 9 && next("group-browser").disabled, "group next failed");
  prev("group-browser").click();
  assert(rows("group-browser").length === 25, "group prev failed");
  await searchFor("group-search", "unique offpage attribution");
  assert(rows("group-browser").length === 1 && rows("group-browser")[0].textContent.includes("stress-group-29"), "offpage group search failed");
  await searchFor("group-search", "regional-rg");
  assert(rows("group-browser")[0].textContent.includes("0 (empty)") && rows("group-browser")[0].textContent.includes("No billing records"), "empty group lost");
  await searchFor("search", "offpage-meter-122");
  assert(document.getElementById("matches").textContent === "1 of 606 resources | 1-1", "offpage meter search failed");
  document.querySelector("#resource-browser a").click();
  assert(document.getElementById("resource-33-19").open && rows("charges").length === 50, "offpage search selection failed");
  await searchFor("search", "unique offpage attribution");
  assert(document.getElementById("matches").textContent === "20 of 606 resources | 1-20", "offpage attribution search failed");
  await searchFor("search", "no-such-resource");
  assert(document.getElementById("matches").textContent === "0 of 606 resources" && next("resource-browser").disabled && prev("resource-browser").disabled, "empty search pagination failed");
  await searchFor("search", "artifact-only");
  assert(document.getElementById("matches").textContent === "1 of 606 resources | 1-1", "search failed");
  document.querySelector("#resource-browser a").click();
  assert(document.getElementById("selected-resource").textContent.includes("No billing records (not an explicit zero)"), "missing records shown as zero");
  await searchFor("search", "Billing-backed inferred RG");
  assert(document.getElementById("matches").textContent === "4 of 606 resources | 1-4", "attribution search failed");
  await searchFor("search", "");
  assert(rows("resource-browser").length === 50 && prev("resource-browser").disabled, "search did not reset page");
  // Every billing status retains signed costs and distinguishes absent from explicit zero.
  for (let g = 4; g < 9; g++) {
    for (let r = 0; r < 4; r++) {
      const id = "resource-" + g + "-" + r;
      await searchFor("search", "stress-resource-" + (g - 4) + "-" + r);
      const row = document.querySelector('[data-resource="' + id + '"]');
      row.querySelector("a").click();
      const detail = document.getElementById(id);
      for (const text of [row.textContent, detail.textContent]) {
        assert(text.includes(["$0.0000", "$-1.0000", "$1.0000", "No billing records"][r]), "lost signed or missing amount");
        assert(text.includes("Known subtotal") === (g !== 4 && r !== 3), "incorrect partial subtotal label");
        assert(text.includes("Explicit net zero") === (g === 4 && r === 0), "incorrect explicit zero label");
      }
      assert(detail.textContent.includes("failed, passed") && detail.textContent.includes("Attempts3"), "attempts/outcomes lost");
      assert(document.querySelectorAll("details.resource").length === 1, "details DOM grew");
    }
  }
  location.hash = "resource-3-0";
  await wait();
  assert(document.getElementById("resource-3-0").open, "hash navigation failed");
  location.hash = "resource-0-0";
  await wait();
  await searchFor("search", "");
  document.querySelector('[data-resource="resource-0-0"] a').click();
  if (!document.getElementById("resource-0-0").open) throw Error("details did not open");
  for (const element of [document.querySelector("h1"), document.querySelector("#resource-0-0 summary")]) {
    if (element.scrollWidth > element.clientWidth) throw Error("unbroken name overflows");
  }
  if (document.documentElement.scrollWidth > window.innerWidth) throw Error("horizontal page overflow");
  assert(document.querySelectorAll("*").length < 1200, "browsing grew DOM beyond page limits");
  document.getElementById("resource-inventory").open = false;
  await wait();
  document.getElementById("resource-inventory").open = true;
  await wait();
  assert(document.querySelectorAll("#resource-browser table").length === 1 && rows("resource-browser").length === 50, "reopening inventory duplicated DOM");
  document.body.dataset.browserCheck = "passed";
} catch (error) { document.body.dataset.browserCheck = String(error); }
})();
</script>`
	runRenderBrowser(t, page, check)
}

func runRenderBrowser(t *testing.T, page, check string) {
	t.Helper()
	browser := os.Getenv("E2E_COST_BROWSER")
	path := filepath.Join(t.TempDir(), "report.html")
	end := strings.LastIndex(page, "</body>")
	if err := os.WriteFile(path, []byte(page[:end]+check+page[end:]), 0600); err != nil {
		t.Fatal(err)
	}
	for _, size := range []string{"1280,900", "390,844"} {
		t.Run(size, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, browser, "--headless", "--no-sandbox", "--disable-gpu", "--disable-background-networking", "--host-resolver-rules=MAP * ~NOTFOUND", "--user-data-dir="+t.TempDir(), "--window-size="+size, "--virtual-time-budget=20000", "--dump-dom", "file://"+path)
			output, err := command.Output()
			if err != nil {
				t.Fatalf("headless browser: %v", err)
			}
			if !bytes.Contains(output, []byte(`data-browser-check="passed"`)) {
				state := regexp.MustCompile(`data-browser-check="[^"]*"`).Find(output)
				t.Fatalf("browser check failed: %s", state)
			}
		})
	}
}
