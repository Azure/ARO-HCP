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
	view := costReport{Snapshot: snapshot, License: echartsLicense + "\n\n" + d3License, Notice: echartsNotice}
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
	if !snapshot.Job.FinishedAt.IsZero() {
		finishDay = snapshot.Job.FinishedAt.UTC().Format(time.DateOnly)
	}
	for gi, group := range snapshot.Groups {
		if group.Category != "Infra" && group.Category != "Tests" {
			return fmt.Errorf("render cost report: group %q has invalid category %q", group.Name, group.Category)
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
				categoryNode := childCostNode(root, group.Category)
				ownerNode := childCostNode(categoryNode, chartOwner)
				if groupNode == nil {
					groupNode = &costNode{Name: groupLabel}
					ownerNode.Children = append(ownerNode.Children, groupNode)
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
				for _, node := range []*costNode{categoryNode, ownerNode, groupNode, typeNode} {
					node.Value += row.Net
				}
			} else {
				view.Negative += row.Net
			}
		}
		view.Inventory = append(view.Inventory, inventory)
	}
	for _, total := range []float64{view.Positive, view.Negative, view.Net, view.PostJobNet} {
		if math.IsNaN(total) || math.IsInf(total, 0) {
			return fmt.Errorf("render cost report: cost totals overflow")
		}
	}
	job := snapshot.Job
	if !job.StartedAt.IsZero() && !job.FinishedAt.IsZero() && job.FinishedAt.After(job.StartedAt) {
		view.JobDuration = job.FinishedAt.Sub(job.StartedAt).String()
	}
	if view.HasCharges && !job.InfraStartedAt.IsZero() && !job.InfraEndedAt.IsZero() && job.InfraEndedAt.After(job.InfraStartedAt) {
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
