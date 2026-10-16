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
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"math"
	"strings"
	"time"

	_ "embed"

	"github.com/go-echarts/go-echarts/v2/charts"
	"github.com/go-echarts/go-echarts/v2/opts"
)

//go:embed report.html
var reportTemplate string

//go:embed assets/echarts-5.6.0.min.js
var echartsRuntime string

//go:embed assets/LICENSE
var echartsLicense string

//go:embed assets/NOTICE
var echartsNotice string

//go:embed assets/LICENSE-d3
var d3License string

// ECharts supports floating-point areas; opts.TreeMapNode only supports ints.
type costNode struct {
	Name      string         `json:"name"`
	Value     float64        `json:"value"`
	Children  []*costNode    `json:"children,omitempty"`
	Resource  string         `json:"resource,omitempty"`
	ItemStyle *costNodeStyle `json:"itemStyle,omitempty"`
}

type costNodeStyle struct {
	BorderColor string `json:"borderColor"`
	BorderWidth int    `json:"borderWidth"`
}

type reportResource struct {
	Resource
	Anchor     string  `json:"anchor"`
	Net        float64 `json:"net"`
	HasCharges bool    `json:"hasCharges"`
	PostJob    bool    `json:"postJob,omitempty"`
}

type reportGroup struct {
	Group
	Resources  []reportResource `json:"resources"`
	Net        float64          `json:"net"`
	HasCharges bool             `json:"hasCharges"`
}

type costReport struct {
	*Snapshot
	ResourceCount           int
	Inventory               []reportGroup
	Alerts                  []Diagnostic
	InfoDiagnostics         []Diagnostic
	Positive, Negative, Net float64
	HasCharges, Incomplete  bool
	PostJobResources        int
	PostJobNet              float64
	InfraRate, JobDuration  string
	Option, Runtime         template.JS
	TypeData                template.JS
	InventoryData           template.JS
	FilteredData            template.JS
	SubscriptionData        template.JS
	SubscriptionMode        bool
	License, Notice         string
}

// Render writes a self-contained, offline report. Collection diagnostics are
// report content, not render failures; invalid snapshot data is a render failure.
func Render(w io.Writer, snapshot *Snapshot) error {
	if snapshot == nil {
		return fmt.Errorf("render cost report: nil snapshot")
	}
	if snapshot.Version != SchemaVersion {
		return fmt.Errorf("render cost report: unsupported snapshot version %d (want %d)", snapshot.Version, SchemaVersion)
	}
	if snapshot.Currency != "USD" {
		return fmt.Errorf("render cost report: unsupported currency %q (want USD)", snapshot.Currency)
	}
	if snapshot.CostBasis != "" && snapshot.CostBasis != "AmortizedCost" && !strings.EqualFold(snapshot.CostBasis, "amortized") {
		return fmt.Errorf("render cost report: unsupported cost basis %q", snapshot.CostBasis)
	}
	if snapshot.Mode != "" && snapshot.Mode != "subscriptions" {
		return fmt.Errorf("render cost report: unsupported mode %q", snapshot.Mode)
	}
	view := costReport{Snapshot: snapshot, SubscriptionMode: snapshot.Mode == "subscriptions", License: echartsLicense + "\n\n" + d3License, Notice: echartsNotice}
	subscriptions := make(map[string]SubscriptionSummary)
	retained := make(map[string]float64)
	excluded := make(map[string]float64)
	for _, sub := range snapshot.Subscriptions {
		for _, amount := range []float64{sub.TotalUSD, sub.RetainedUSD, sub.ExcludedUSD} {
			if math.IsNaN(amount) || math.IsInf(amount, 0) {
				return fmt.Errorf("render cost report: subscription %q has non-finite cost", sub.ID)
			}
		}
		if !view.SubscriptionMode {
			continue
		}
		id := strings.ToLower(sub.ID)
		if _, exists := subscriptions[id]; exists || id == "" {
			return fmt.Errorf("render cost report: empty or duplicate subscription %q", sub.ID)
		}
		subscriptions[id] = sub
		switch sub.BillingStatus {
		case "complete":
			// A successful subscription query can explicitly return no charges.
			view.HasCharges = true
		case "", "pending", "partial", "unavailable":
			view.Incomplete = true
		default:
			return fmt.Errorf("render cost report: subscription %q has invalid billing status %q", sub.ID, sub.BillingStatus)
		}
	}
	for _, group := range snapshot.FilteredGroups {
		if math.IsNaN(group.CostUSD) || math.IsInf(group.CostUSD, 0) {
			return fmt.Errorf("render cost report: filtered group %q has non-finite cost", group.Name)
		}
		if view.SubscriptionMode {
			id := strings.ToLower(group.SubscriptionID)
			if _, exists := subscriptions[id]; !exists {
				return fmt.Errorf("render cost report: filtered group %q has unknown subscription %q", group.Name, group.SubscriptionID)
			}
			excluded[id] += group.CostUSD
			// Recorded exclusions establish a known retained zero even if nothing remains.
			view.HasCharges = true
		}
	}
	for _, d := range snapshot.Diagnostics {
		if d.Severity == "info" {
			view.InfoDiagnostics = append(view.InfoDiagnostics, d)
		} else {
			view.Alerts = append(view.Alerts, d)
		}
		if d.Severity == "error" || d.Severity == "warning" {
			view.Incomplete = true
		}
	}
	root := &costNode{}
	typesRoot := &costNode{}
	groupSubscriptions := make(map[string]map[string]bool)
	for _, group := range snapshot.Groups {
		name := strings.ToLower(group.Name)
		if groupSubscriptions[name] == nil {
			groupSubscriptions[name] = make(map[string]bool)
		}
		groupSubscriptions[name][strings.ToLower(group.SubscriptionID)] = true
	}
	finishDay := ""
	if !view.SubscriptionMode && !snapshot.Job.FinishedAt.IsZero() {
		finishDay = snapshot.Job.FinishedAt.UTC().Format(time.DateOnly)
	}
	for gi, group := range snapshot.Groups {
		if (view.SubscriptionMode && group.Category != "Residual") || (!view.SubscriptionMode && group.Category != "Infra" && group.Category != "Tests") {
			return fmt.Errorf("render cost report: group %q has invalid category %q", group.Name, group.Category)
		}
		if view.SubscriptionMode {
			if _, exists := subscriptions[strings.ToLower(group.SubscriptionID)]; !exists {
				return fmt.Errorf("render cost report: group %q has unknown subscription %q", group.Name, group.SubscriptionID)
			}
		}
		switch group.BillingStatus {
		case "complete":
		case "", "pending", "partial", "unavailable":
			view.Incomplete = true
		default:
			return fmt.Errorf("render cost report: group %q has invalid billing status %q", group.Name, group.BillingStatus)
		}
		inventory := reportGroup{Group: group}
		owner := group.Owner
		if owner == "" {
			owner = "Unknown owner"
		}
		chartOwner := owner
		if group.Category == "Infra" {
			if owner == "Service Cluster" {
				chartOwner = "Service"
			} else if strings.HasPrefix(owner, "Management Cluster ") {
				chartOwner = "Management " + strings.TrimPrefix(owner, "Management Cluster ")
			}
		}
		// Keep identical RG names in different subscriptions separate in the tree.
		groupLabel := group.Name
		if len(groupSubscriptions[strings.ToLower(group.Name)]) > 1 {
			subscription := group.SubscriptionID
			if subscription == "" {
				subscription = "Unknown subscription"
			}
			groupLabel += " [" + subscription + "]"
		}
		var groupNode *costNode
		for ri, resource := range group.Resources {
			row := reportResource{
				Resource: resource, Anchor: fmt.Sprintf("resource-%d-%d", gi, ri),
				HasCharges: len(resource.Charges) > 0,
			}
			for ci, charge := range resource.Charges {
				if math.IsNaN(charge.CostUSD) || math.IsInf(charge.CostUSD, 0) {
					return fmt.Errorf("render cost report: group %q resource %q charge %d has non-finite cost", group.Name, resource.Name, ci)
				}
				if _, err := time.Parse(time.DateOnly, charge.Day); err != nil {
					return fmt.Errorf("render cost report: group %q resource %q invalid usage day %q: %w", group.Name, resource.Name, charge.Day, err)
				}
				row.Net += charge.CostUSD
				if finishDay != "" && charge.Day > finishDay {
					row.PostJob = true
					view.PostJobNet += charge.CostUSD
				}
			}
			if row.Name == "" {
				row.Name = resource.ID
				if row.Name == "" {
					row.Name = "Unattributed charge"
				}
			}
			if row.Type == "" {
				row.Type = "Unknown type"
			}
			if row.PostJob {
				view.PostJobResources++
			}
			inventory.Resources = append(inventory.Resources, row)
			view.ResourceCount++
			view.HasCharges = view.HasCharges || row.HasCharges
			inventory.HasCharges = inventory.HasCharges || row.HasCharges
			inventory.Net += row.Net
			view.Net += row.Net
			if row.Net > 0 {
				view.Positive += row.Net
				var parents []*costNode
				if view.SubscriptionMode {
					parents = []*costNode{childCostNode(root, subscriptions[strings.ToLower(group.SubscriptionID)].ID)}
				} else {
					categoryNode := childCostNode(root, group.Category)
					parents = []*costNode{categoryNode, childCostNode(categoryNode, chartOwner)}
				}
				if groupNode == nil {
					groupNode = &costNode{Name: groupLabel}
					if view.SubscriptionMode {
						groupNode.Name = group.Name
					}
					parent := parents[len(parents)-1]
					parent.Children = append(parent.Children, groupNode)
				}
				typeNode := childCostNode(groupNode, row.Type)
				leaf := &costNode{Name: row.Name, Value: row.Net, Resource: row.Anchor}
				if row.PostJob {
					leaf.ItemStyle = &costNodeStyle{BorderColor: "#a85d00", BorderWidth: 4}
				}
				typeNode.Children = append(typeNode.Children, leaf)
				resourceType := childCostNode(typesRoot, row.Type)
				typeLeaf := *leaf
				typeLeaf.Name = row.Name + " (" + groupLabel + ")"
				resourceType.Children = append(resourceType.Children, &typeLeaf)
				resourceType.Value += row.Net
				for _, node := range append(parents, groupNode, typeNode) {
					node.Value += row.Net
				}
			} else {
				view.Negative += row.Net
			}
		}
		view.Inventory = append(view.Inventory, inventory)
		retained[strings.ToLower(group.SubscriptionID)] += inventory.Net
	}
	if view.SubscriptionMode {
		for _, sub := range snapshot.Subscriptions {
			id := strings.ToLower(sub.ID)
			for _, pair := range [][2]float64{
				{sub.TotalUSD, sub.ExcludedUSD + sub.RetainedUSD},
				{sub.RetainedUSD, retained[id]},
				{sub.ExcludedUSD, excluded[id]},
			} {
				// Allow floating-point accumulation noise, not missing accounting rows.
				tolerance := 1e-6 + 1e-9*math.Max(math.Abs(pair[0]), math.Abs(pair[1]))
				if math.IsNaN(pair[1]) || math.IsInf(pair[1], 0) || math.Abs(pair[0]-pair[1]) > tolerance {
					return fmt.Errorf("render cost report: subscription %q reconciliation mismatch (%g vs %g)", sub.ID, pair[0], pair[1])
				}
			}
		}
	}
	for _, total := range []float64{view.Positive, view.Negative, view.Net, view.PostJobNet} {
		if math.IsNaN(total) || math.IsInf(total, 0) {
			return fmt.Errorf("render cost report: cost totals overflow")
		}
	}
	job := snapshot.Job
	if !view.SubscriptionMode && !job.StartedAt.IsZero() && !job.FinishedAt.IsZero() && job.FinishedAt.After(job.StartedAt) {
		view.JobDuration = job.FinishedAt.Sub(job.StartedAt).String()
	}
	if !view.SubscriptionMode && view.HasCharges && !job.InfraStartedAt.IsZero() && !job.InfraEndedAt.IsZero() && job.InfraEndedAt.After(job.InfraStartedAt) {
		rate := view.Net / job.InfraEndedAt.Sub(job.InfraStartedAt).Hours()
		if !math.IsInf(rate, 0) && !math.IsNaN(rate) {
			view.InfraRate = fmt.Sprintf("$%.4f", rate)
		}
	}
	chart := charts.NewTreeMap()
	chart.SetGlobalOptions(charts.WithLegendOpts(opts.Legend{Show: opts.Bool(false)}))
	chart.AddSeries("Positive net resource costs (USD)", nil,
		charts.WithSeriesOpts(func(series *charts.SingleSeries) {
			series.LeafDepth = 2
			series.NodeClick = "zoomToNode"
			series.Label = &opts.Label{Show: opts.Bool(true), Formatter: "{b}"}
			series.UpperLabel = map[string]any{"show": true, "height": 24}
			series.Left, series.Right, series.Top, series.Bottom = "8", "8", "8", "45"
		}))
	// RenderSnippet rewrites function markers and HTML entities. Only feed it
	// trusted configuration; add snapshot strings afterward via encoding/json.
	var option map[string]any
	if err := json.Unmarshal([]byte(chart.RenderSnippet().Option), &option); err != nil {
		return fmt.Errorf("render cost report: decode chart options: %w", err)
	}
	series := option["series"].([]any)[0].(map[string]any)
	series["data"] = root.Children
	series["drillDownIcon"] = ""
	option["tooltip"] = map[string]any{"show": true, "renderMode": "richText", "confine": true}
	option["color"] = []string{"#287d8e", "#5967a8", "#709653", "#b88040", "#9e6389"}
	encoded, err := json.Marshal(option)
	if err != nil {
		return fmt.Errorf("render cost report: encode chart options: %w", err)
	}
	// json.Marshal escapes '<', '>', '&', and JS line separators, including any
	// user-supplied closing script tag. Only the pinned runtime is trusted code.
	view.Option = template.JS(encoded)
	typeData, err := json.Marshal(typesRoot.Children)
	if err != nil {
		return fmt.Errorf("render cost report: encode resource types: %w", err)
	}
	view.TypeData = template.JS(typeData)
	inventoryData, err := json.Marshal(view.Inventory)
	if err != nil {
		return fmt.Errorf("render cost report: encode inventory: %w", err)
	}
	view.InventoryData = template.JS(inventoryData)
	if view.SubscriptionMode {
		filteredData, err := json.Marshal(snapshot.FilteredGroups)
		if err != nil {
			return fmt.Errorf("render cost report: encode filtered groups: %w", err)
		}
		view.FilteredData = template.JS(filteredData)
		subscriptionData, err := json.Marshal(snapshot.Subscriptions)
		if err != nil {
			return fmt.Errorf("render cost report: encode subscriptions: %w", err)
		}
		view.SubscriptionData = template.JS(subscriptionData)
	}
	view.Runtime = template.JS(echartsRuntime)
	tmpl, err := template.New("report").Funcs(template.FuncMap{
		"usd": func(v float64) string { return fmt.Sprintf("$%.4f", v) },
		"utc": func(t time.Time) string {
			if t.IsZero() {
				return "Unavailable"
			}
			return t.UTC().Format("2006-01-02 15:04:05 UTC")
		},
		"known": func(s string) string {
			if s == "" {
				return "Unknown"
			}
			return s
		},
	}).Parse(reportTemplate)
	if err != nil {
		return fmt.Errorf("render cost report: parse template: %w", err)
	}
	var output bytes.Buffer
	if err := tmpl.Execute(&output, view); err != nil {
		return fmt.Errorf("render cost report: execute template: %w", err)
	}
	if _, err := output.WriteTo(w); err != nil {
		return fmt.Errorf("render cost report: write: %w", err)
	}
	return nil
}

func childCostNode(parent *costNode, name string) *costNode {
	for _, child := range parent.Children {
		if child.Name == name {
			return child
		}
	}
	child := &costNode{Name: name}
	parent.Children = append(parent.Children, child)
	return child
}
