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
