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

package gatherobservability

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Azure/ARO-HCP/tooling/rightsize-requests/pkg/targets"
)

func TestRenderRightSizingHTML(t *testing.T) {
	report := buildRightSizingReport(rightSizingFixture(), 0.1)
	attack := "</script><img src=x onerror=alert(1)>&\u2028\u2029"
	report.Warnings = append(report.Warnings, attack)
	report.CPUWindow = attack
	row := &report.Recommendations[0]
	row.Cluster, row.Namespace, row.Kind, row.Workload, row.Container = attack, attack, attack, attack, attack
	row.SuggestedQuantity, row.Warnings = attack, []string{attack}
	html, err := renderRightSizingHTML(report)
	if err != nil {
		t.Fatal(err)
	}
	text := string(html)
	for _, want := range []string{"<!DOCTYPE html>", `name="viewport"`, `id="cpu-rows"`, `id="memory-rows"`, `\u003c/script\u003e`, `\u0026`, `\u2028`, `\u2029`, "Kind namespace/name", "Samples", "Unknown", "Round up (ceil)", "<summary>Details</summary>"} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered report missing %q", want)
		}
	}
	for _, forbidden := range []string{attack, "innerHTML", "#ZgotmplZ", "https://", "http://", "<script src=", "fetch(", "nearest", "safety floor"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("unsafe, non-self-contained or outdated report: %q", forbidden)
		}
	}
	const marker = `<script id="right-sizing-data" type="application/json">`
	if strings.Count(text, marker) != 1 {
		t.Fatal("report must embed JSON exactly once")
	}
	_, embedded, _ := strings.Cut(text, marker)
	embedded, _, _ = strings.Cut(embedded, "</script>")
	var decoded rightSizingReport
	if err := json.Unmarshal([]byte(embedded), &decoded); err != nil {
		t.Fatalf("decode embedded JSON: %v", err)
	}
	if !reflect.DeepEqual(report, decoded) {
		t.Fatal("embedded report changed values, including HTML-like strings")
	}
}

func TestRenderRightSizingHTMLEditableCatalog(t *testing.T) {
	report := buildRightSizingReport(rightSizingFixture(), 0.1)
	report.Recommendations = nil
	var expected []bool
	add := func(row rightSizingRecommendation, editable bool) {
		report.Recommendations = append(report.Recommendations, row)
		expected = append(expected, editable)
	}
	for _, target := range targets.ServiceTargets() {
		kind, workload := target.Kind, target.Workload
		if kind == "" {
			kind = "Deployment"
		}
		if workload == "" {
			workload = target.Service
		}
		add(rightSizingRecommendation{Cluster: "int-eastus-" + target.ClusterRole + "-1", Namespace: strings.ReplaceAll(target.Namespace, "*", "int-one"), Kind: kind, Workload: workload, Container: target.Container, InitContainer: target.InitContainer}, true)
	}
	for _, target := range targets.MinimalTargets() {
		for _, namespace := range []string{"ocm-arohcpint-one", "ocm-arohcpstg-two", "ocm-arohcpprod-three"} {
			add(rightSizingRecommendation{Namespace: namespace, Kind: target.Kind, Workload: target.Workload, Container: target.Container}, true)
		}
	}
	for _, target := range targets.AdditionalMinimalTargets() {
		add(rightSizingRecommendation{Cluster: "int-eastus-mgmt-1", Namespace: "ocm-arohcpint-one", Kind: target.Kind, Workload: target.Workload, Container: target.Container}, true)
	}
	for _, mapped := range append([]rightSizingRecommendation(nil), report.Recommendations...) {
		row := mapped
		row.InitContainer = !row.InitContainer
		add(row, false)
		row = mapped
		row.Container += "-sidecar"
		add(row, false)
		row = mapped
		row.Kind = "Pod"
		add(row, false)
	}
	add(rightSizingRecommendation{Namespace: "aks-istio-system", Kind: "Deployment", Workload: "istiod", Container: "discovery"}, false)
	add(rightSizingRecommendation{Namespace: "ocm-arohcpint-one", Kind: "Deployment", Workload: "etcd", Container: "etcd"}, false)
	add(rightSizingRecommendation{Namespace: "ocm-arohcpint-one", Kind: "StatefulSet", Workload: "etcd-unknown", Container: "etcd"}, false)
	before, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	html, err := renderRightSizingHTML(report)
	if err != nil {
		t.Fatal(err)
	}
	_, embedded, _ := strings.Cut(string(html), `<script id="right-sizing-data" type="application/json">`)
	embedded, _, _ = strings.Cut(embedded, "</script>")
	var decoded struct {
		Recommendations []struct {
			rightSizingRecommendation
			Editable *bool `json:"editable"`
		} `json:"recommendations"`
	}
	if err := json.Unmarshal([]byte(embedded), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Recommendations) != len(report.Recommendations) {
		t.Fatal("HTML must retain all mapped and unmapped source rows")
	}
	for i, row := range decoded.Recommendations {
		original := report.Recommendations[i]
		// HTML may add target-wide warnings; raw report immutability is checked below.
		original.Warnings = row.Warnings
		if !reflect.DeepEqual(row.rightSizingRecommendation, original) {
			t.Errorf("row %d changed source evidence", i)
		}
		if row.Editable == nil || *row.Editable != expected[i] || *row.Editable != targets.EditableInCluster(original.Namespace, original.Kind, original.Workload, original.Container, original.Cluster, original.InitContainer) {
			t.Errorf("row %d (%s/%s/%s) editability %v does not match catalog expectation %t", i, row.Namespace, row.Workload, row.Container, row.Editable, expected[i])
		}
	}
	after, err := json.Marshal(report)
	if err != nil || !bytes.Equal(before, after) || bytes.Contains(after, []byte(`"editable"`)) {
		t.Fatalf("HTML rendering changed persisted source report: %v", err)
	}
}

func TestRenderRightSizingAdditionalCapabilities(t *testing.T) {
	for _, target := range targets.AdditionalMinimalTargets() {
		for _, evidence := range []string{"none", "Pod", "wrong kind", "init", "missing peak", "unknown role", "other workload", "other namespace", "other resource"} {
			t.Run(target.Workload+"/"+target.Container+"/"+evidence, func(t *testing.T) {
				report := buildRightSizingReport(rightSizingFixture(), .1)
				row := report.Recommendations[0]
				row.Cluster, row.Namespace = "int-eastus-mgmt-1", "ocm-arohcpint-one"
				row.Kind, row.Workload, row.Container = target.Kind, target.Workload, target.Container
				row.Eligible, row.Actionable, row.Resource = true, true, "cpu"
				row.Warnings = make([]string, 1, 8)
				row.Warnings[0] = "source warning"
				report.Recommendations = []rightSizingRecommendation{row}
				other := row
				other.Cluster, other.Namespace = "stg-westus3-mgmt-2", "ocm-arohcpstg-two"
				switch evidence {
				case "Pod":
					other.Kind, other.Workload = "Pod", "unknown"
				case "wrong kind":
					other.Kind = "DaemonSet"
				case "init":
					other.InitContainer = true
				case "missing peak":
					other.Peak = nil
				case "unknown role":
					other.Cluster = "unknown"
				case "other workload":
					other.Workload = "different-workload"
				case "other namespace":
					other.Namespace = "ocm-other-two"
				case "other resource":
					other.Kind, other.Resource = "Pod", "memory"
				}
				if evidence != "none" {
					report.Recommendations = append(report.Recommendations, other)
				}
				before, err := json.Marshal(report)
				if err != nil {
					t.Fatal(err)
				}
				html, err := renderRightSizingHTML(report)
				if err != nil {
					t.Fatal(err)
				}
				_, embedded, _ := strings.Cut(string(html), `<script id="right-sizing-data" type="application/json">`)
				embedded, _, _ = strings.Cut(embedded, "</script>")
				var displayed struct {
					Recommendations []struct {
						rightSizingRecommendation
						Editable bool `json:"editable"`
					} `json:"recommendations"`
				}
				if err := json.Unmarshal([]byte(embedded), &displayed); err != nil {
					t.Fatal(err)
				}
				got := displayed.Recommendations[0]
				warning := strings.Join(got.Warnings, " ")
				if !got.Editable || got.Eligible || got.Actionable || !strings.Contains(warning, "Requires configured baseline in hypershift.additionalMinimalResourceRequests; use --additional-hcp-config. Not automatically editable until configured.") {
					t.Fatalf("additional capability incorrectly advertised: %+v", got)
				}
				blocked := evidence != "none" && evidence != "other workload" && evidence != "other namespace" && evidence != "other resource"
				if strings.Contains(warning, "Shared target blocked") != blocked {
					t.Fatalf("incorrect cross-namespace blocker: %s", warning)
				}
				want := row
				want.Eligible, want.Actionable, want.Warnings = false, false, got.Warnings
				if !reflect.DeepEqual(got.rightSizingRecommendation, want) {
					t.Fatal("display changed measurements or suggestions")
				}
				after, err := json.Marshal(report)
				if err != nil || !bytes.Equal(before, after) || row.Warnings[:cap(row.Warnings)][1] != "" {
					t.Fatalf("renderer mutated raw report or warning storage: %v", err)
				}
				if evidence == "none" && (target.Container == "control-plane-operator" || target.Container == "etcd-metrics") {
					for _, size := range [][2]int{{1440, 1000}, {390, 844}} {
						t.Run(fmt.Sprint(size), func(t *testing.T) {
							call := rightSizingBrowser(t, html, size[0], size[1])
							result := call("Runtime.evaluate", map[string]any{"expression": `(() => {
  if ($('cpu-rows').querySelector('.warning')) return false;
  $('changes').checked = false; $('changes').dispatchEvent(new Event('input'));
  return $('editable').checked && [...$('cpu-rows').querySelectorAll('.warning')].some(node => node.title.includes('Requires configured baseline')) && document.documentElement.scrollWidth <= innerWidth;
})()`, "returnByValue": true})
							var evaluation struct{ Result struct{ Value bool } }
							if err := json.Unmarshal(result, &evaluation); err != nil || !evaluation.Result.Value {
								t.Fatalf("capability must be hidden by default, inspectable with changes unchecked: %s (%v)", result, err)
							}
						})
					}
				}
			})
		}
	}
}

func TestAdditionalCapabilitiesExcludedFromSavings(t *testing.T) {
	for _, target := range targets.AdditionalMinimalTargets() {
		t.Run(target.Workload+"/"+target.Container, func(t *testing.T) {
			r, u := savingsFixture()
			u.Clusters = []string{"mgmt-1"}
			row := &r.Recommendations[0]
			row.Cluster, row.Namespace, row.Kind, row.Workload, row.Container = "mgmt-1", "ocm-arohcpint-one", target.Kind, target.Workload, target.Container
			w := &u.Snapshots[0].Workloads[0]
			w.Cluster, w.Namespace, w.Kind, w.Name, w.Containers[0].Name = row.Cluster, row.Namespace, row.Kind, row.Workload, row.Container
			got := buildRightSizingSavings(r, u)
			if got == nil || len(got.Resources) != 2 {
				t.Fatalf("missing savings baseline: %+v", got)
			}
			for _, resource := range got.Resources {
				if resource.Before != resource.After || resource.Reductions != 0 || resource.Increases != 0 || resource.ChangedContainers != 0 || resource.ExcludedContainers != 1 {
					t.Fatalf("unconfigured additional capability changed savings: %+v", resource)
				}
			}
		})
	}
}

func TestRenderRightSizingHTMLSharedTargets(t *testing.T) {
	base := buildRightSizingReport(rightSizingFixture(), .1).Recommendations[0]
	base.Cluster, base.Namespace, base.Kind, base.Workload, base.Container = "cluster-a", "aro-hcp", "Deployment", "aro-hcp-backend", "aro-hcp-backend"
	base.Resource, base.Eligible, base.Actionable = "cpu", true, true
	base.Warnings = make([]string, 1, 8) // Appending display warnings must not alias source storage.
	base.Warnings[0] = "original warning"
	for _, tc := range []struct {
		name  string
		edit  func(*rightSizingRecommendation)
		block bool
	}{
		{"unresolved Pod", func(r *rightSizingRecommendation) { r.Kind, r.Workload, r.Eligible = "Pod", "unknown", false }, true},
		{"ineligible", func(r *rightSizingRecommendation) { r.Eligible = false }, true},
		{"missing suggestion", func(r *rightSizingRecommendation) { r.Suggested = nil }, true},
		{"missing peak", func(r *rightSizingRecommendation) { r.Peak = nil }, true},
		{"missing request minimum", func(r *rightSizingRecommendation) { r.RequestMin = nil }, true},
		{"missing request maximum", func(r *rightSizingRecommendation) { r.RequestMax = nil }, true},
		{"missing replicas", func(r *rightSizingRecommendation) { r.Replicas, r.MeasuredReplicas = 0, 0 }, true},
		{"replica mismatch", func(r *rightSizingRecommendation) { r.MeasuredReplicas = r.Replicas - 1 }, true},
		{"other service workload", func(r *rightSizingRecommendation) { r.Kind, r.Workload = "StatefulSet", "other-backend" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := buildRightSizingReport(rightSizingFixture(), .1)
			other := base
			other.Cluster = "cluster-b"
			higher := *base.Suggested * 2
			other.Suggested = &higher
			tc.edit(&other)
			memory := base
			memory.Resource = "memory"
			frontend := base
			frontend.Container, frontend.Workload = "aro-hcp-frontend", "aro-hcp-frontend"
			report.Recommendations = []rightSizingRecommendation{base, other, memory, frontend}
			before, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			html, err := renderRightSizingHTML(report)
			if err != nil {
				t.Fatal(err)
			}
			_, embedded, _ := strings.Cut(string(html), `<script id="right-sizing-data" type="application/json">`)
			embedded, _, _ = strings.Cut(embedded, "</script>")
			var displayed rightSizingReport
			if err := json.Unmarshal([]byte(embedded), &displayed); err != nil {
				t.Fatal(err)
			}
			for i, row := range displayed.Recommendations {
				want := report.Recommendations[i]
				if i < 2 {
					if tc.block {
						want.Eligible, want.Actionable = false, false
						if !strings.Contains(strings.Join(row.Warnings, " "), "Shared target blocked") {
							t.Errorf("row %d missing shared-target block warning: %v", i, row.Warnings)
						}
					} else if i == 0 && !strings.Contains(strings.Join(row.Warnings, " "), "Shared target has a higher suggestion") {
						t.Error("different service kind/workload must contribute to the shared maximum")
					}
					want.Warnings = row.Warnings
				}
				if !reflect.DeepEqual(row, want) {
					t.Errorf("row %d changed measurements or has incorrect display guards: got %+v, want %+v", i, row, want)
				}
			}
			after, err := json.Marshal(report)
			if err != nil || !bytes.Equal(before, after) || base.Warnings[:cap(base.Warnings)][1] != "" {
				t.Fatalf("HTML rendering mutated raw report or warning storage: %v", err)
			}
			if tc.name == "unresolved Pod" {
				for _, size := range [][2]int{{1440, 1000}, {390, 844}} {
					t.Run(fmt.Sprint(size), func(t *testing.T) {
						call := rightSizingBrowser(t, html, size[0], size[1])
						result := call("Runtime.evaluate", map[string]any{"expression": `(() => {
  const blocked = () => grouped.find(row => row.resource === 'cpu' && row.workload === 'aro-hcp-backend');
  if (!blocked().editable || blocked().eligible || blocked().actionable || $('cpu-rows').textContent.includes('/aro-hcp-backend')) return false;
  $('cluster').value = 'cluster-a'; $('cluster').dispatchEvent(new Event('input'));
  if (blocked().eligible || blocked().actionable || $('cpu-rows').textContent.includes('/aro-hcp-backend')) return false;
  $('changes').checked = false; $('changes').dispatchEvent(new Event('input'));
  return $('cpu-rows').textContent.includes('/aro-hcp-backend') &&
    [...$('cpu-rows').querySelectorAll('.warning')].some(node => node.title.includes('Shared target blocked')) &&
    document.documentElement.scrollWidth <= innerWidth;
})()`, "returnByValue": true})
						var evaluation struct {
							Result struct{ Value bool }
						}
						if err := json.Unmarshal(result, &evaluation); err != nil || !evaluation.Result.Value {
							t.Fatalf("hidden Pod must block service even after cluster filtering: %s (%v)", result, err)
						}
					})
				}
			}
		})
	}
}

func TestRenderRightSizingHTMLSharedHCPTargets(t *testing.T) {
	for _, target := range targets.MinimalTargets() {
		for _, owner := range []string{"unresolved Pod", "wrong kind", "resolved different workload"} {
			t.Run(target.Workload+"/"+owner, func(t *testing.T) {
				report := buildRightSizingReport(rightSizingFixture(), .1)
				base := report.Recommendations[0]
				report.Recommendations = nil
				for _, mapped := range targets.MinimalTargets() {
					row := base
					row.Cluster, row.Namespace = "cluster-a", "ocm-arohcpint-one"
					row.Kind, row.Workload, row.Container = mapped.Kind, mapped.Workload, mapped.Container
					row.Eligible, row.Actionable, row.Warnings = true, true, nil
					report.Recommendations = append(report.Recommendations, row)
				}
				other := base
				other.Cluster, other.Namespace = "cluster-b", "ocm-arohcpstg-two"
				other.Kind, other.Workload, other.Container = target.Kind, target.Workload, target.Container
				other.Eligible, other.Actionable = false, false
				switch owner {
				case "unresolved Pod":
					other.Kind, other.Workload = "Pod", "unknown"
				case "wrong kind":
					other.Kind = "DaemonSet"
				case "resolved different workload":
					other.Workload = "different-workload"
				}
				report.Recommendations = append(report.Recommendations, other)
				before, err := json.Marshal(report)
				if err != nil {
					t.Fatal(err)
				}
				html, err := renderRightSizingHTML(report)
				if err != nil {
					t.Fatal(err)
				}
				_, embedded, _ := strings.Cut(string(html), `<script id="right-sizing-data" type="application/json">`)
				embedded, _, _ = strings.Cut(embedded, "</script>")
				var displayed rightSizingReport
				if err := json.Unmarshal([]byte(embedded), &displayed); err != nil {
					t.Fatal(err)
				}
				for i, row := range displayed.Recommendations[:len(targets.MinimalTargets())] {
					want := report.Recommendations[i]
					if row.Container == target.Container && owner != "resolved different workload" {
						want.Eligible, want.Actionable = false, false
						want.Warnings = row.Warnings
						if !strings.Contains(strings.Join(row.Warnings, " "), "Shared target blocked") {
							t.Error("cross-namespace unresolved/wrong-kind evidence must warn on the mapped HCP target")
						}
					}
					if !reflect.DeepEqual(row, want) {
						t.Errorf("incorrect HCP target scope for %s: got %+v, want %+v", row.Workload, row, want)
					}
				}
				after, err := json.Marshal(report)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatalf("HTML rendering mutated raw HCP report: %v", err)
				}
			})
		}
	}
}

func TestRightSizingResolvedTargetScopes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		identities []rightSizingRecommendation
		edit       func(*rightSizingRecommendation)
		blocked    []bool
	}{
		{
			name: "role-specific maximum",
			identities: []rightSizingRecommendation{
				{Cluster: "int-eastus-svc-1", Namespace: "prometheus", Kind: "StatefulSet", Workload: "prom-agent-prometheus", Container: "prometheus"},
				{Cluster: "int-eastus-mgmt-1", Namespace: "prometheus", Kind: "StatefulSet", Workload: "prom-agent-prometheus-shard-1", Container: "prometheus"},
			},
			edit: func(r *rightSizingRecommendation) {}, blocked: []bool{false, false},
		},
		{
			name: "wrong kind blocks only known role",
			identities: []rightSizingRecommendation{
				{Cluster: "int-eastus-svc-1", Namespace: "prometheus", Kind: "StatefulSet", Workload: "prom-agent-prometheus", Container: "prometheus"},
				{Cluster: "int-eastus-mgmt-1", Namespace: "prometheus", Kind: "StatefulSet", Workload: "prom-agent-prometheus", Container: "prometheus"},
			},
			edit: func(r *rightSizingRecommendation) { r.Kind = "Deployment" }, blocked: []bool{true, false},
		},
		{
			name: "unknown role blocks both paths",
			identities: []rightSizingRecommendation{
				{Cluster: "int-eastus-svc-1", Namespace: "prometheus", Kind: "StatefulSet", Workload: "prom-agent-prometheus", Container: "prometheus"},
				{Cluster: "int-eastus-mgmt-1", Namespace: "prometheus", Kind: "StatefulSet", Workload: "prom-agent-prometheus", Container: "prometheus"},
			},
			edit: func(r *rightSizingRecommendation) { r.Cluster = "unknown" }, blocked: []bool{true, true},
		},
		{
			name: "refresher maximum does not cross workloads",
			identities: []rightSizingRecommendation{
				{Cluster: "svc", Namespace: "aks-istio-ingress", Kind: "Deployment", Workload: "frontend-certificate-refresher", Container: "init-container-msg-container-init"},
				{Cluster: "svc", Namespace: "aks-istio-ingress", Kind: "Deployment", Workload: "admin-api-certificate-refresher", Container: "init-container-msg-container-init"},
				{Cluster: "svc", Namespace: "aks-istio-ingress", Kind: "Deployment", Workload: "sessiongate-certificate-refresher", Container: "init-container-msg-container-init"},
			},
			edit: func(r *rightSizingRecommendation) {}, blocked: []bool{false, false, false},
		},
		{
			name: "wrong kind blocks only identified refresher",
			identities: []rightSizingRecommendation{
				{Cluster: "svc", Namespace: "aks-istio-ingress", Kind: "Deployment", Workload: "frontend-certificate-refresher", Container: "init-container-msg-container-init"},
				{Cluster: "svc", Namespace: "aks-istio-ingress", Kind: "Deployment", Workload: "admin-api-certificate-refresher", Container: "init-container-msg-container-init"},
			},
			edit: func(r *rightSizingRecommendation) { r.Kind = "StatefulSet" }, blocked: []bool{true, false},
		},
		{
			name: "bare pod blocks all refresher candidates",
			identities: []rightSizingRecommendation{
				{Cluster: "svc", Namespace: "aks-istio-ingress", Kind: "Deployment", Workload: "frontend-certificate-refresher", Container: "init-container-msg-container-init"},
				{Cluster: "svc", Namespace: "aks-istio-ingress", Kind: "Deployment", Workload: "admin-api-certificate-refresher", Container: "init-container-msg-container-init"},
				{Cluster: "svc", Namespace: "aks-istio-ingress", Kind: "Deployment", Workload: "sessiongate-certificate-refresher", Container: "init-container-msg-container-init"},
			},
			edit: func(r *rightSizingRecommendation) { r.Kind, r.Workload = "Pod", "frontend-certificate-refresher-pod" }, blocked: []bool{true, true, true},
		},
		{
			name: "guest KSM uses service path across HCP namespaces",
			identities: []rightSizingRecommendation{
				{Cluster: "int-eastus-mgmt-1", Namespace: "ocm-arohcpint-one", Kind: "Deployment", Workload: "kube-state-metrics-hcp", Container: "kube-state-metrics"},
				{Cluster: "int-eastus-mgmt-1", Namespace: "ocm-arohcpint-two", Kind: "Deployment", Workload: "kube-state-metrics-hcp", Container: "kube-state-metrics"},
				{Cluster: "int-eastus-mgmt-1", Namespace: "prometheus", Kind: "Deployment", Workload: "prometheus-kube-state-metrics", Container: "kube-state-metrics"},
			},
			edit: func(r *rightSizingRecommendation) { r.Kind = "StatefulSet" }, blocked: []bool{true, true, false},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, u := savingsFixture()
			base := r.Recommendations[0]
			r.Recommendations = nil
			for _, id := range tc.identities {
				row := base
				row.Cluster, row.Namespace, row.Kind, row.Workload, row.Container = id.Cluster, id.Namespace, id.Kind, id.Workload, id.Container
				row.Actionable = true
				r.Recommendations = append(r.Recommendations, row)
			}
			other := r.Recommendations[0]
			higher := .75
			other.Suggested = &higher
			tc.edit(&other)
			r.Recommendations = append(r.Recommendations, other)
			html, err := renderRightSizingHTML(r)
			if err != nil {
				t.Fatal(err)
			}
			_, embedded, _ := strings.Cut(string(html), `<script id="right-sizing-data" type="application/json">`)
			embedded, _, _ = strings.Cut(embedded, "</script>")
			var displayed rightSizingReport
			if err := json.Unmarshal([]byte(embedded), &displayed); err != nil {
				t.Fatal(err)
			}
			for i, blocked := range tc.blocked {
				row := displayed.Recommendations[i]
				if row.Eligible == blocked || row.Actionable == blocked {
					t.Errorf("row %d: blocked=%t, got %+v", i, blocked, row)
				}
				w := &u.Snapshots[0].Workloads[0]
				w.Cluster, w.Namespace, w.Kind, w.Name, w.Containers[0].Name = row.Cluster, row.Namespace, row.Kind, row.Workload, row.Container
				u.Clusters = []string{row.Cluster}
				got := buildRightSizingSavings(r, u).Resources[0]
				wantAfter := .5
				if i == 0 {
					wantAfter = .75
				}
				if blocked {
					wantAfter = 1
				}
				if got.After != wantAfter || (got.ExcludedContainers == 1) != blocked {
					t.Errorf("row %d savings: got %+v, want after %g, blocked=%t", i, got, wantAfter, blocked)
				}
				warned := strings.Contains(strings.Join(row.Warnings, " "), "higher suggestion")
				if warned != (i == 0 && !blocked) {
					t.Errorf("row %d: incorrect maximum warning: %v", i, row.Warnings)
				}
			}
		})
	}
}

func TestRightSizingSupportedInitTarget(t *testing.T) {
	for _, eligible := range []bool{false, true} {
		r, u := savingsFixture()
		row := &r.Recommendations[0]
		row.Namespace, row.Workload, row.Container = "clusters-service", "clusters-service", "clusters-service-init"
		row.InitContainer, row.Eligible, row.Actionable = true, eligible, eligible
		html, err := renderRightSizingHTML(r)
		if err != nil {
			t.Fatal(err)
		}
		_, embedded, _ := strings.Cut(string(html), `<script id="right-sizing-data" type="application/json">`)
		embedded, _, _ = strings.Cut(embedded, "</script>")
		var displayed struct {
			Recommendations []struct {
				rightSizingRecommendation
				Editable bool `json:"editable"`
			} `json:"recommendations"`
		}
		if err := json.Unmarshal([]byte(embedded), &displayed); err != nil {
			t.Fatal(err)
		}
		got := displayed.Recommendations[0]
		if !got.Editable || got.Eligible != eligible || got.Actionable != eligible {
			t.Fatalf("supported init mapping must preserve measurement eligibility: %+v", got)
		}
		w := &u.Snapshots[0].Workloads[0]
		w.Namespace, w.Name, w.Containers[0].Name = row.Namespace, row.Workload, row.Container
		savings := buildRightSizingSavings(r, u).Resources[0]
		if savings.After != 1 || savings.ExcludedContainers != 1 || savings.ChangedContainers != 0 {
			t.Fatalf("init evidence cannot authorize ordinary-container savings: %+v", savings)
		}
	}
}

func TestRenderRightSizingHTMLValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*rightSizingReport)
		want string
	}{
		{"old rounding policy", func(r *rightSizingReport) { r.Version = 1 }, "regenerate from replica-peaks.json using render-right-sizing"},
		{"old headroom policy", func(r *rightSizingReport) { r.Version = 2 }, "regenerate from replica-peaks.json using render-right-sizing"},
		{"version", func(r *rightSizingReport) { r.Version = 4 }, "version"},
		{"headroom", func(r *rightSizingReport) { r.Headroom = 1.2 }, "headroom=1"},
		{"start", func(r *rightSizingReport) { r.Start = time.Time{} }, "timestamps"},
		{"end", func(r *rightSizingReport) { r.End = time.Time{} }, "timestamps"},
		{"reversed", func(r *rightSizingReport) { r.End = r.Start.Add(-time.Second) }, "start <= end"},
		{"nonfinite", func(r *rightSizingReport) { r.Headroom = math.NaN() }, "headroom=1"},
		{"infinite peak", func(r *rightSizingReport) { v := math.Inf(1); r.Recommendations[0].Peak = &v }, "encode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := buildRightSizingReport(rightSizingFixture(), 0.1)
			tc.edit(&report)
			if _, err := renderRightSizingHTML(report); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, err)
			}
		})
	}
	report := buildRightSizingReport(rightSizingFixture(), 0.1)
	report.Recommendations = nil
	if _, err := renderRightSizingHTML(report); err != nil {
		t.Fatalf("empty report must render: %v", err)
	}
}

func TestRenderRightSizingCommandReplay(t *testing.T) {
	peaks := rightSizingFixture()
	peaks.GeneratedAt = peaks.End
	data, err := json.Marshal(peaks)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	input := filepath.Join(dir, "replica-peaks.json")
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, threshold := range []string{"", "0.75"} {
		t.Run("threshold="+threshold, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "rendered")
			cmd := newRenderRightSizingCommand()
			args := []string{"--input", input, "--output", output}
			wantThreshold := 0.1
			if threshold != "" {
				args = append(args, "--change-threshold", threshold)
				wantThreshold = 0.75
			}
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("offline replay: %v", err)
			}
			data, err := os.ReadFile(filepath.Join(output, "right-sizing.json"))
			if err != nil {
				t.Fatal(err)
			}
			var got rightSizingReport
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			want := buildRightSizingReport(peaks, wantThreshold)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("replayed JSON differs from builder: got %+v, want %+v", got, want)
			}
			if bytes.Contains(data, []byte(`"editable"`)) {
				t.Fatal("display metadata must not change persisted JSON")
			}
			html, err := os.ReadFile(filepath.Join(output, "right-sizing.html"))
			if err != nil {
				t.Fatal(err)
			}
			wantHTML, err := renderRightSizingHTML(want)
			if err != nil || !bytes.Equal(html, wantHTML) {
				t.Fatalf("replayed HTML differs from renderer: %v", err)
			}
			files, err := os.ReadDir(output)
			if err != nil || len(files) != 2 {
				t.Fatalf("expected only JSON and standalone HTML, got %v: %v", files, err)
			}
			unchanged, err := os.ReadFile(input)
			if err != nil || !bytes.Equal(unchanged, dataForPeaks(t, peaks)) {
				t.Fatalf("replay modified input: %v", err)
			}
		})
	}
}

func dataForPeaks(t *testing.T, peaks replicaPeakReport) []byte {
	t.Helper()
	data, err := json.Marshal(peaks)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Retain real peak/ownership evidence and the savings fixture's single-pod
// baseline, aligning identities, requests and windows as in a collected run.
func rightSizingReplayFixture() (replicaPeakReport, utilizationReport) {
	peaks := rightSizingFixture()
	peaks.GeneratedAt = peaks.End
	r, utilization := savingsFixture()
	identity := r.Recommendations[0]
	for i := range peaks.Containers {
		c := &peaks.Containers[i]
		c.Cluster, c.Namespace, c.Container = identity.Cluster, identity.Namespace, identity.Container
	}
	for _, metadata := range peaks.Metadata {
		metadata.Labels["cluster"], metadata.Labels["namespace"] = identity.Cluster, identity.Namespace
		if metadata.Labels["metric"] == "kube_replicaset_owner" {
			metadata.Labels["owner_name"] = identity.Workload
		}
	}
	utilization.SchemaVersion = utilizationSchemaVersion
	utilization.GeneratedAt, utilization.Start, utilization.End = peaks.GeneratedAt, peaks.Start, peaks.End
	utilization.Snapshots[0].Time = peaks.Start
	cpu, memory := .1, float64(100*1024*1024)
	utilization.Snapshots[0].Workloads[0].Containers[0].Requests = utilizationResources{CPU: &cpu, Memory: &memory}
	return peaks, utilization
}

func TestRenderRightSizingCommandReplaySavings(t *testing.T) {
	for _, baseline := range []string{"omitted", "concurrent", "no snapshots", "outside window"} {
		t.Run(baseline, func(t *testing.T) {
			peaks, utilization := rightSizingReplayFixture()
			switch baseline {
			case "no snapshots":
				utilization.Snapshots = nil
			case "outside window":
				utilization.Snapshots[0].Time = peaks.Start.Add(-time.Minute)
				utilization.Start = utilization.Snapshots[0].Time
			}
			dir := t.TempDir()
			input, utilizationInput, output := filepath.Join(dir, "replica-peaks.json"), filepath.Join(dir, "utilization.json"), filepath.Join(dir, "output")
			peakData := dataForPeaks(t, peaks)
			utilizationData, err := json.Marshal(utilization)
			if err != nil {
				t.Fatal(err)
			}
			for path, data := range map[string][]byte{input: peakData, utilizationInput: utilizationData} {
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			// Execute through the public parent command, not just the leaf constructor.
			cmd, err := NewCommand()
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"render-right-sizing", "--input", input, "--output", output}
			want := buildRightSizingReport(peaks, .1)
			if baseline != "omitted" {
				args = append(args, "--utilization-input", utilizationInput)
				want.Savings = buildRightSizingSavings(want, utilization)
			}
			if baseline == "concurrent" {
				if want.Savings == nil || len(want.Savings.Resources) != 2 {
					t.Fatalf("fixture must yield concurrent savings: %+v", want.Savings)
				}
				cpu := want.Savings.Resources[0]
				if cpu.Cluster != "svc" || cpu.Resource != "cpu" || cpu.Before != .1 || math.Abs(cpu.After-.02) > 1e-12 || cpu.ChangedContainers != 1 || cpu.ExcludedContainers != 0 {
					t.Fatalf("fixture must authorize a real CPU decrease: %+v", cpu)
				}
			} else if want.Savings != nil {
				t.Fatalf("unavailable baseline must not invent savings: %+v", want.Savings)
			}
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("offline savings replay: %v", err)
			}
			data, err := os.ReadFile(filepath.Join(output, "right-sizing.json"))
			if err != nil {
				t.Fatal(err)
			}
			var got rightSizingReport
			if err := json.Unmarshal(data, &got); err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("replay lost recommendations or savings: got %+v, want %+v, error %v", got, want, err)
			}
			if bytes.Contains(data, []byte(`"savings"`)) != (baseline == "concurrent") {
				t.Fatalf("savings must serialize only with a concurrent baseline: %s", data)
			}
			html, err := os.ReadFile(filepath.Join(output, "right-sizing.html"))
			if err != nil {
				t.Fatal(err)
			}
			_, embedded, _ := strings.Cut(string(html), `<script id="right-sizing-data" type="application/json">`)
			embedded, _, _ = strings.Cut(embedded, "</script>")
			var displayed rightSizingReport
			if err := json.Unmarshal([]byte(embedded), &displayed); err != nil || !reflect.DeepEqual(displayed, got) {
				t.Fatalf("HTML and persisted JSON disagree: %v", err)
			}
			for path, original := range map[string][]byte{input: peakData, utilizationInput: utilizationData} {
				unchanged, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(unchanged, original) {
					t.Fatalf("replay modified %s: %v", path, err)
				}
			}
		})
	}
}

func TestRenderRightSizingCommandRejectsInvalidUtilization(t *testing.T) {
	peaks, utilization := rightSizingReplayFixture()
	data, err := json.Marshal(utilization)
	if err != nil {
		t.Fatal(err)
	}
	valid := string(data)
	edited := func(edit func(*utilizationReport)) string {
		_, report := rightSizingReplayFixture()
		edit(&report)
		data, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	for _, tc := range []struct{ name, input, want string }{
		{"malformed", "{", "decode utilization input"},
		{"null", "null", "unsupported utilization schemaVersion"},
		{"unknown version", strings.Replace(valid, `"schemaVersion":1`, `"schemaVersion":2`, 1), "unsupported utilization schemaVersion"},
		{"unknown field", strings.Replace(valid, `"schemaVersion":`, `"extra":true,"schemaVersion":`, 1), "unknown field"},
		{"nested unknown field", strings.Replace(valid, `"requests":`, `"extra":true,"requests":`, 1), "unknown field"},
		{"trailing object", valid + "{}", "exactly one JSON report"},
		{"trailing junk", valid + "junk", "exactly one JSON report"},
		{"invalid timestamp", strings.Replace(valid, utilization.Start.Format(time.RFC3339), "not-a-time", 1), "decode utilization input"},
		{"zero generatedAt", edited(func(r *utilizationReport) { r.GeneratedAt = time.Time{} }), "timestamps"},
		{"zero start", edited(func(r *utilizationReport) { r.Start = time.Time{} }), "timestamps"},
		{"zero end", edited(func(r *utilizationReport) { r.End = time.Time{} }), "timestamps"},
		{"reversed window", edited(func(r *utilizationReport) { r.End = r.Start.Add(-time.Second) }), "start <= end"},
		{"zero snapshot", edited(func(r *utilizationReport) { r.Snapshots[0].Time = time.Time{} }), "report start/end window"},
		{"early snapshot", edited(func(r *utilizationReport) { r.Snapshots[0].Time = r.Start.Add(-time.Second) }), "report start/end window"},
		{"late snapshot", edited(func(r *utilizationReport) { r.Snapshots[0].Time = r.End.Add(time.Second) }), "report start/end window"},
		{"empty cluster", edited(func(r *utilizationReport) { r.Clusters = []string{" "} }), "unique nonempty names"},
		{"duplicate cluster", edited(func(r *utilizationReport) { r.Clusters = append(r.Clusters, r.Clusters[0]) }), "unique nonempty names"},
		{"undeclared cluster", edited(func(r *utilizationReport) { r.Snapshots[0].Workloads[0].Cluster = "other" }), "declared cluster"},
		{"empty container", edited(func(r *utilizationReport) { r.Snapshots[0].Workloads[0].Containers[0].Name = " " }), "container names"},
		{"duplicate container", edited(func(r *utilizationReport) {
			w := &r.Snapshots[0].Workloads[0]
			w.Containers = append(w.Containers, w.Containers[0])
		}), "container names"},
		{"negative requests", strings.Replace(valid, `"cpu":0.1`, `"cpu":-1`, 1), "finite and nonnegative"},
		{"negative pods", strings.Replace(valid, `"pods":1`, `"pods":-1`, 1), "pod counts"},
		{"negative pending", strings.Replace(valid, `"pendingPods":0`, `"pendingPods":-1`, 1), "pod counts"},
		{"excess pending", strings.Replace(valid, `"pendingPods":0`, `"pendingPods":2`, 1), "pendingPods cannot exceed pods"},
		{"negative unlimited CPU", strings.Replace(valid, `"unlimitedCPU":0`, `"unlimitedCPU":-1`, 1), "unlimited counts"},
		{"excess unlimited CPU", strings.Replace(valid, `"unlimitedCPU":0`, `"unlimitedCPU":2`, 1), "unlimited counts"},
		{"negative unlimited memory", strings.Replace(valid, `"unlimitedMemory":null`, `"unlimitedMemory":-1`, 1), "unlimited counts"},
		{"excess unlimited memory", strings.Replace(valid, `"unlimitedMemory":null`, `"unlimitedMemory":2`, 1), "unlimited counts"},
		{"missing pods", strings.Replace(valid, `"pods":1,`, ``, 1), "pods and pendingPods are required"},
		{"null pods", strings.Replace(valid, `"pods":1`, `"pods":null`, 1), "pods and pendingPods are required"},
		{"missing pending", strings.Replace(valid, `"pendingPods":0,`, ``, 1), "pods and pendingPods are required"},
		{"null pending", strings.Replace(valid, `"pendingPods":0`, `"pendingPods":null`, 1), "pods and pendingPods are required"},
		{"missing unlimited CPU", strings.Replace(valid, `"unlimitedCPU":0,`, ``, 1), "unlimitedCPU and unlimitedMemory are required"},
		{"missing unlimited memory", strings.Replace(valid, `,"unlimitedMemory":null`, ``, 1), "unlimitedCPU and unlimitedMemory are required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			input, baseline, output := filepath.Join(dir, "peaks.json"), filepath.Join(dir, "utilization.json"), filepath.Join(dir, "output")
			if err := os.WriteFile(input, dataForPeaks(t, peaks), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(baseline, []byte(tc.input), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := newRenderRightSizingCommand()
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs([]string{"--input", input, "--utilization-input", baseline, "--output", output})
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, err)
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatalf("invalid baseline created output: %v", err)
			}
			cmd = newRenderUtilizationCommand()
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs([]string{"--input", baseline, "--output", output})
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("render-utilization expected %q, got %v", tc.want, err)
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatalf("invalid utilization created output: %v", err)
			}
		})
	}
}

func TestDecodeUtilizationNullableCounters(t *testing.T) {
	zero, one := 0, 1
	for _, count := range []*int{nil, &zero, &one} {
		_, report := rightSizingReplayFixture()
		container := &report.Snapshots[0].Workloads[0].Containers[0]
		container.UnlimitedCPU, container.UnlimitedMemory = count, count
		data, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeUtilizationReport(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("explicit null and known counts must decode: %v", err)
		}
		if err := validateUtilizationReport(decoded); err != nil {
			t.Fatalf("explicit null and known counts must validate: %v", err)
		}
		if !reflect.DeepEqual(report, decoded) {
			t.Fatal("decoding must preserve unknown versus known counts")
		}
	}
}

func TestRenderRightSizingCommandProtectsUtilizationInput(t *testing.T) {
	peaks, utilization := rightSizingReplayFixture()
	data, err := json.Marshal(utilization)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"right-sizing.json", "right-sizing.html"} {
		for _, alias := range []string{"same path", "symlink", "hardlink"} {
			t.Run(name+"/"+alias, func(t *testing.T) {
				dir := t.TempDir()
				input, baseline := filepath.Join(dir, "replica-peaks.json"), filepath.Join(dir, "utilization.json")
				output := filepath.Join(dir, name)
				if alias == "same path" {
					baseline = output
				}
				if err := os.WriteFile(input, dataForPeaks(t, peaks), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(baseline, data, 0600); err != nil {
					t.Fatal(err)
				}
				switch alias {
				case "symlink":
					if err := os.Symlink(baseline, output); err != nil {
						t.Fatal(err)
					}
				case "hardlink":
					if err := os.Link(baseline, output); err != nil {
						t.Fatal(err)
					}
				}
				cmd := newRenderRightSizingCommand()
				cmd.SetOut(io.Discard)
				cmd.SetErr(io.Discard)
				cmd.SetArgs([]string{"--input", input, "--utilization-input", baseline, "--output", dir})
				if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "overwrite the input") {
					t.Fatalf("expected baseline overwrite rejection, got %v", err)
				}
				for path, original := range map[string][]byte{input: dataForPeaks(t, peaks), baseline: data} {
					unchanged, err := os.ReadFile(path)
					if err != nil || !bytes.Equal(unchanged, original) {
						t.Fatalf("input %s was modified: %v", path, err)
					}
				}
			})
		}
	}
}

func TestRenderRightSizingCommandRejectsInvalidInput(t *testing.T) {
	peaks := rightSizingFixture()
	peaks.GeneratedAt = peaks.End
	valid := string(dataForPeaks(t, peaks))
	sized, err := json.Marshal(buildRightSizingReport(peaks, 0.1))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, input, threshold, want string
	}{
		{"version", strings.Replace(valid, `"version":1`, `"version":2`, 1), "0.1", "version"},
		{"negative threshold", valid, "-0.1", "change-threshold"},
		{"large threshold", valid, "1.1", "change-threshold"},
		{"NaN threshold", valid, "NaN", "change-threshold"},
		{"infinite threshold", valid, "+Inf", "change-threshold"},
		{"trailing object", valid + "{}", "0.1", "trailing data"},
		{"trailing junk", valid + "junk", "0.1", "trailing data"},
		{"unknown field", strings.Replace(valid, `"version":1`, `"extra":true,"version":1`, 1), "0.1", "unknown field"},
		{"nested unknown field", strings.Replace(valid, `"queryName":`, `"extra":true,"queryName":`, 1), "0.1", "unknown field"},
		{"sizing report", string(sized), "0.1", "unknown field"},
		{"null", "null", "0.1", "version"},
		{"nonfinite value", strings.Replace(valid, `"max":0.1`, `"max":1e999`, 1), "0.1", "decode"},
		{"missing start", strings.Replace(valid, peaks.Start.Format(time.RFC3339), "0001-01-01T00:00:00Z", 1), "0.1", "timestamps"},
		{"missing generatedAt", strings.Replace(valid, `"generatedAt":"`+peaks.End.Format(time.RFC3339)+`"`, `"generatedAt":null`, 1), "0.1", "timestamps"},
		{"reversed", strings.Replace(valid, peaks.Start.Format(time.RFC3339), peaks.End.Add(time.Hour).Format(time.RFC3339), 1), "0.1", "start <= end"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			input, output := filepath.Join(dir, "input.json"), filepath.Join(dir, "output")
			if err := os.WriteFile(input, []byte(tc.input), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := newRenderRightSizingCommand()
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs([]string{"--input", input, "--output", output, "--change-threshold", tc.threshold})
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, err)
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatalf("invalid input created output: %v", err)
			}
		})
	}
}

func TestRenderRightSizingCommandProtectsInput(t *testing.T) {
	peaks := rightSizingFixture()
	peaks.GeneratedAt = peaks.End
	data := dataForPeaks(t, peaks)
	for _, alias := range []string{"same path", "symlink", "hardlink"} {
		t.Run(alias, func(t *testing.T) {
			dir := t.TempDir()
			input := filepath.Join(dir, "right-sizing.json")
			if alias != "same path" {
				input = filepath.Join(dir, "replica-peaks.json")
			}
			if err := os.WriteFile(input, data, 0600); err != nil {
				t.Fatal(err)
			}
			switch alias {
			case "symlink":
				if err := os.Symlink(input, filepath.Join(dir, "right-sizing.html")); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(input, filepath.Join(dir, "right-sizing.json")); err != nil {
					t.Fatal(err)
				}
			}
			cmd := newRenderRightSizingCommand()
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs([]string{"--input", input, "--output", dir})
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "overwrite the input") {
				t.Fatalf("expected input overwrite rejection, got %v", err)
			}
			unchanged, err := os.ReadFile(input)
			if err != nil || !bytes.Equal(unchanged, data) {
				t.Fatalf("input was modified: %v", err)
			}
		})
	}
}

func TestRenderRightSizingBrowser(t *testing.T) {
	report := buildRightSizingReport(rightSizingFixture(), 0.1)
	// Preserve fractional seconds: real collection windows have nanosecond precision.
	report.End = report.End.Add(867262418 * time.Nanosecond)
	base := report.Recommendations[0]
	report.Recommendations = nil
	for i := range 10000 {
		row := base
		row.Resource = "cpu"
		row.Workload = fmt.Sprintf("workload-%05d", i)
		row.Cluster = "large-cluster"
		request := float64(i+1)/1000 + *row.Suggested
		row.RequestMin, row.RequestMax = &request, &request
		row.Direction, row.Eligible, row.Actionable = "over", true, true
		report.Recommendations = append(report.Recommendations, row)
	}
	for _, resource := range []string{"cpu", "memory"} {
		for _, direction := range []string{"under", "matched", "unknown"} {
			row := base
			row.Resource, row.Direction = resource, direction
			row.Cluster, row.Workload = "small-cluster", direction+"-workload"
			row.Eligible, row.Actionable = direction != "unknown", direction == "under"
			row.AlertRisk = direction == "under"
			request := *row.Suggested / 2
			row.RequestMin, row.RequestMax = &request, &request
			if direction == "matched" {
				row.Direction = "over" // Nonzero but below tolerance, not exactly matched.
				request = *row.Suggested * 1.01
			}
			if direction == "unknown" {
				row.Peak, row.RequestMin, row.RequestMax, row.Suggested, row.Delta = nil, nil, nil, nil, nil
				row.Warnings = []string{"</script><img src=x onerror=window.injected=true>"}
				row.Container = row.Warnings[0]
				row.InitContainer = true
			}
			report.Recommendations = append(report.Recommendations, row)
		}
	}
	report.Warnings = []string{"<img src=x onerror=window.injected=true>"}
	html, err := renderRightSizingHTML(report)
	if err != nil {
		t.Fatal(err)
	}
	assertions := `<script>
try {
  function check(condition, message) { if (!condition) throw new Error(message); }
  function change(id, value) { const node = document.getElementById(id); if (node.type === 'checkbox') node.checked = value; else node.value = value; node.dispatchEvent(new Event('input', {bubbles: true})); }
  const cpu = document.getElementById('cpu-rows'), memory = document.getElementById('memory-rows');
  check(document.getElementById('changes').checked, 'changes-only default');
  change('changes', false);
  check(document.getElementById('editable').checked, 'editable-only default');
  change('editable', false); // Synthetic workloads have no updater mapping.
  check(!document.getElementById('method').open, 'technical details collapsed');
  check(document.querySelector('#method summary').textContent === 'Details (1 warnings)', 'report warnings visible without expanding details');
  check([...document.querySelectorAll('thead tr')].every(row => row.cells.length === 5 && [...row.cells].map(cell => cell.textContent).join('|') === 'Kind namespace/name|Requests|Usage|Suggested request|Samples'), 'exactly five concise columns');
  check([...document.querySelectorAll('thead th:last-child')].every(node => node.title === 'Conservative lower bound of distinct replica-minute usage observations, not raw scrapes. Unknown if any source count is missing.'), 'samples tooltip explains counting and missing counts for both resources');
  check(cpu.children.length === 30 && memory.children.length === 3, 'bounded independent pages, all evidence when editable filter disabled');
  check(cpu.rows[0].textContent.includes('workload-09999'), 'largest absolute per-container delta ranks first');
  check(cpu.rows[0].cells[1].textContent === '10,020m', 'requests are not multiplied by replicas');
  check(cpu.rows[0].cells[2].title.includes('200m'), 'CPU burst in tooltip');
  document.getElementById('cpu-next').click();
  check(cpu.rows[0].textContent.includes('workload-09969'), 'next page ranks');
  check(document.getElementById('memory-page').textContent === '1-3 of 3', 'paging resources independently');
  change('cluster', 'small-cluster');
  check(cpu.children.length === 3 && cpu.textContent.includes('matched-workload'), 'cluster filter resets pagination and retains within-tolerance rows');
  check(cpu.querySelector('.warning').title.includes('Alert risk'), 'alert risk on suggestion tooltip');
  change('direction', 'unknown');
  check(cpu.textContent.includes('unknown-workload') && cpu.textContent.includes('Unknown'), 'missing values are not zero');
  check(cpu.textContent.includes('(init container)'), 'init identity shown');
  check([...cpu.querySelectorAll('.warning')].some(node => node.title.includes('<img') && node.title.includes('Incomplete evidence')), 'literal evidence in suggestion tooltip');
  check(!document.querySelector('img') && !window.injected, 'injection cannot create executable DOM');
  change('direction', 'under');
  check(cpu.textContent.includes('under-workload') && memory.textContent.includes('under-workload'), 'under-requested filter');
  change('search', 'no-matches');
  check(cpu.textContent.includes('No matching rows') && document.getElementById('cpu-next').disabled, 'empty state');
  change('search', 'UNDER-WORKLOAD');
  check(cpu.textContent.includes('under-workload'), 'case insensitive search');
  document.getElementById('reset').click();
  check(document.getElementById('editable').checked, 'reset restores editable-only default');
  check(document.getElementById('changes').checked, 'reset restores changes-only default');
  change('changes', false);
  change('editable', false);
  check(cpu.children.length === 30 && document.getElementById('direction').value === 'all', 'reset restores defaults');
  change('direction', 'over');
  check(memory.textContent.includes('matched-workload') && cpu.rows[0].textContent.includes('workload-09999'), 'over filter includes within-tolerance rows and preserves rank');
  check(document.querySelectorAll('tbody tr').length <= 60, '10k rows do not build 10k DOM');
  check(document.querySelectorAll('*').length < 1000, '10k source rows have a small DOM');

  // Samples are tested at the JSON boundary, independently of the Go model field.
  // Three HCP namespace instances on two clusters must collapse without summing peaks.
  for (const row of rows) delete row.samples;
  document.getElementById('reset').click();
  change('changes', false);
  change('editable', false);
  check(cpu.rows[0].cells[4].textContent === 'Unknown', 'saved reports without samples must not invent counts');
  const source = {...rows[0], workload: 'hcp-operator', container: 'operator', samples: 10, requestMin: .1, requestMax: .2, peak: .04, suggested: .05, eligible: true, warnings: []};
  for (const resource of ['cpu', 'memory']) {
    rows.push({...source, resource, cluster: 'small-cluster', namespace: 'ocm-arohcpint-one'},
      {...source, resource, cluster: 'small-cluster', namespace: 'ocm-arohcpint-two', samples: 20, peak: .08, suggested: .1},
      {...source, resource, cluster: 'large-cluster', namespace: 'ocm-arohcpstg-three', samples: 30, requestMin: .3, requestMax: .4, peak: .2, suggested: .25, eligible: false, warnings: ['sparse evidence']});
  }
  document.getElementById('reset').click();
  change('changes', false);
  change('editable', false);
  change('search', 'hcp-operator');
  check(cpu.rows.length === 1 && memory.rows.length === 1, 'three HCP namespaces across two clusters form one group per resource');
  check(cpu.rows[0].cells[0].textContent.includes('Deployment ocm-arohcp*/hcp-operator'), 'normalized identity');
  check(cpu.rows[0].cells[1].textContent === '100m to 400m', 'request range across source rows');
  check(cpu.rows[0].cells[2].textContent === '200m' && cpu.rows[0].cells[3].textContent === '250m*', 'maximum usage and suggestion, including ineligible source');
  check(cpu.rows[0].cells[4].textContent === '60', 'samples sum source groups');
  check(cpu.rows[0].cells[3].querySelector('.warning').title.includes('sparse evidence'), 'one ineligible source warns without hiding group');
  const merged = grouped.find(row => row.resource === 'cpu' && row.workload === 'hcp-operator');
  check(merged.peak === .2 && merged.suggested === .25 && merged.samples === 60, 'group numeric maxima and count');
  change('search', 'ocm-arohcpint-one');
  check(cpu.rows[0].cells[4].textContent === '60', 'search does not drop other namespace instances from a group');
  change('cluster', 'small-cluster');
  check(cpu.rows.length === 1 && cpu.rows[0].cells[2].textContent === '80m' && cpu.rows[0].cells[3].textContent === '100m', 'cluster filter recomputes maxima before grouping');
  check(cpu.rows[0].cells[1].textContent === '100m to 200m' && cpu.rows[0].cells[4].textContent === '30', 'cluster filter recomputes requests and samples');

  rows.push({...source, cluster: 'small-cluster', namespace: 'ordinary-a'},
    {...source, cluster: 'large-cluster', namespace: 'ordinary-a', samples: 20},
    {...source, cluster: 'small-cluster', namespace: 'ordinary-b'},
    {...source, cluster: 'small-cluster', namespace: 'ocm-arohcpint-one', container: 'sidecar'},
    {...source, cluster: 'small-cluster', namespace: 'ocm-arohcpint-one', initContainer: true},
    {...source, cluster: 'small-cluster', namespace: 'ocm-arohcpint-one', kind: 'StatefulSet'},
    {...source, cluster: 'small-cluster', namespace: 'ocm-arohcpint-one', workload: 'hcp-operator-123'},
    {...source, cluster: 'small-cluster', namespace: 'ocm-arohcpint-four', samples: null, peak: null, requestMin: null, requestMax: null, suggested: null});
  document.getElementById('reset').click();
  change('changes', false);
  change('editable', false);
  change('search', 'hcp-operator');
  check(cpu.rows.length === 7, 'ordinary namespaces, kind, container, init and exact workload names stay distinct');
  const ordinary = [...cpu.rows].filter(row => row.cells[0].textContent.includes('ordinary-'));
  check(ordinary.length === 2 && ordinary[0].cells[4].textContent === '30', 'ordinary namespaces stay exact but aggregate across clusters');
  const partial = [...cpu.rows].find(row => row.cells[0].textContent === 'Deployment ocm-arohcp*/hcp-operatoroperator');
  check(partial.cells[4].textContent === 'Unknown', 'one unknown sample count makes grouped samples unknown');
  check(partial.cells[2].textContent === '200mUnknown in part' && partial.cells[1].textContent.includes('Unknown in part'), 'partial missing values flagged without erasing measured maxima');
  check(partial.cells[3].querySelector('.warning').title.includes('Unknown suggestion'), 'partial unknown suggestion flagged');
  change('direction', 'unknown');
  check(cpu.rows.length === 1 && cpu.textContent.includes('ocm-arohcp*'), 'unknown filter includes incomplete groups');
  check(rows.filter(row => row.workload === 'hcp-operator' && row.namespace === 'ocm-arohcpint-one').length === 5, 'display grouping does not mutate raw identity');
  check(document.documentElement.scrollWidth <= window.innerWidth, 'mobile has no page-level horizontal overflow');
  check(document.getElementById('window').scrollWidth <= document.getElementById('window').clientWidth, 'precise timestamps wrap within header');
  check([...document.querySelectorAll('.table-scroll')].every(node => node.tabIndex === 0), 'wide tables keyboard scrollable');
  const result = document.createElement('output'); result.id = 'browser-result'; result.textContent = 'PASS'; document.body.append(result);
} catch (error) {
  const result = document.createElement('output'); result.id = 'browser-result'; result.textContent = 'FAIL: ' + error.message; document.body.append(result);
}
</script>`
	page := []byte(strings.Replace(string(html), "</body>", assertions+"</body>", 1))
	for _, size := range [][2]int{{1440, 1000}, {390, 844}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			call := rightSizingBrowser(t, page, size[0], size[1])
			result := call("Runtime.evaluate", map[string]any{"expression": `document.getElementById('browser-result')?.textContent`, "returnByValue": true})
			var evaluation struct {
				Result struct{ Value string }
			}
			if err := json.Unmarshal(result, &evaluation); err != nil || evaluation.Result.Value != "PASS" {
				t.Fatalf("browser assertions: %s (%v)", result, err)
			}
		})
	}
}

func TestRenderRightSizingBrowserEditable(t *testing.T) {
	report := buildRightSizingReport(rightSizingFixture(), 0.1)
	base := report.Recommendations[0]
	report.Recommendations = nil
	for _, resource := range []string{"cpu", "memory"} {
		for _, identity := range []struct{ namespace, kind, workload, container string }{
			{"aro-hcp", "Deployment", "aro-hcp-backend", "aro-hcp-backend"},
			{"ocm-arohcpint-one", "StatefulSet", "etcd", "etcd"},
			{"ocm-arohcpstg-two", "StatefulSet", "etcd", "etcd"},
			{"aks-istio-system", "Deployment", "istiod", "discovery"},
			{"aro-hcp", "Deployment", "aro-hcp-backend", "unmapped-sidecar"},
		} {
			row := base
			row.Resource, row.Cluster = resource, "cluster-a"
			row.Namespace, row.Kind, row.Workload, row.Container = identity.namespace, identity.kind, identity.workload, identity.container
			peak, suggestion, request, samples := .1, .12, .2, 10
			row.Eligible, row.Actionable, row.Warnings = true, true, nil
			if identity.namespace == "ocm-arohcpstg-two" {
				row.Cluster, row.Eligible, row.Actionable = "cluster-b", false, false
				row.Warnings = []string{"sparse evidence"}
				peak, suggestion, request, samples = .5, .6, .8, 20
			}
			row.Peak, row.Suggested, row.RequestMin, row.RequestMax, row.Samples = &peak, &suggestion, &request, &request, &samples
			report.Recommendations = append(report.Recommendations, row)
		}
	}
	html, err := renderRightSizingHTML(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range [][2]int{{1440, 1000}, {390, 844}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			call := rightSizingBrowser(t, html, size[0], size[1])
			result := call("Runtime.evaluate", map[string]any{"expression": `(() => {
  function check(condition, message) { if (!condition) throw new Error(message); }
  function change(id, value) { const node = $(id); if (node.type === 'checkbox') node.checked = value; else node.value = value; node.dispatchEvent(new Event('input', {bubbles: true})); }
  try {
    const source = $('right-sizing-data').textContent;
    check(rows.length === 10 && rows.filter(row => row.editable).length === 6, 'raw JSON retains mapped and unmapped rows');
    check($('editable').checked && $('editable').defaultChecked, 'editable filter checked by default');
    check($('changes').checked && $('changes').defaultChecked, 'changes filter checked by default');
    check(!$('cpu-rows').textContent.includes('etcd'), 'ineligible group hidden by changes filter');
    change('changes', false);
    check($('filter-status').textContent === '4 of 8 resource rows (4 hidden by editable filter)', 'hidden groups counted');
    for (const resource of resources) {
      const body = $(resource + '-rows');
      check(body.rows.length === 2 && body.textContent.includes('etcd') && body.textContent.includes('aro-hcp-backend'), 'default shows mapped service and HCP only');
      check(!body.textContent.includes('istiod') && !body.textContent.includes('unmapped-sidecar'), 'managed and unknown containers hidden');
    }
    const etcd = grouped.find(row => row.resource === 'cpu' && row.workload === 'etcd');
    check(etcd.editable && etcd.peak === .5 && etcd.suggested === .6 && etcd.requestMin === .2 && etcd.requestMax === .8 && etcd.samples === 30, 'default groups all namespace and cluster evidence including ineligible higher peak');
    check(etcd.warnings.has('sparse evidence') && $('cpu-rows').querySelector('.warning'), 'ineligible evidence warns without changing mapping');
    change('search', 'ocm-arohcpint-one');
    check($('cpu-rows').rows.length === 1 && $('cpu-rows').rows[0].cells[2].textContent === '500m', 'namespace search retains other instances');
    change('cluster', 'cluster-a');
    check($('cpu-rows').rows[0].cells[2].textContent === '100m' && $('cpu-rows').rows[0].cells[4].textContent === '10', 'cluster selection recomputes aggregation');
    $('reset').click();
    change('changes', false);
    change('editable', false);
    check($('cpu-rows').rows.length === 4 && $('memory-rows').rows.length === 4, 'toggle restores all groups');
    check($('cpu-rows').textContent.includes('istiod') && $('cpu-rows').textContent.includes('unmapped-sidecar'), 'unmapped evidence restored');
    check($('filter-status').textContent === '8 of 8 resource rows', 'toggle clears hidden count');
    $('reset').click();
    check($('editable').checked && $('changes').checked && $('cpu-rows').rows.length === 1, 'reset restores default filtering');
    check($('right-sizing-data').textContent === source && rows.length === 10, 'filtering never changes source JSON');
    // Even inconsistent display metadata must not remove higher peaks before grouping.
    rows.find(row => row.resource === 'cpu' && row.namespace === 'ocm-arohcpstg-two').editable = false;
    $('reset').click();
    const mixed = grouped.find(row => row.resource === 'cpu' && row.workload === 'etcd');
    check(!mixed.editable && mixed.peak === .5 && mixed.samples === 30 && !$('cpu-rows').textContent.includes('etcd'), 'group requires every raw row editable and retains all evidence');
    change('changes', false);
    change('editable', false);
    check($('cpu-rows').textContent.includes('etcd'), 'mixed group restored when unchecked');
    check(document.documentElement.scrollWidth <= innerWidth, 'editable control does not overflow viewport');
    return 'PASS';
  } catch (error) { return 'FAIL: ' + error.message; }
})()`, "returnByValue": true})
			var evaluation struct {
				Result struct{ Value string }
			}
			if err := json.Unmarshal(result, &evaluation); err != nil || evaluation.Result.Value != "PASS" {
				t.Fatalf("editable browser assertions: %s (%v)", result, err)
			}
		})
	}
}

func TestRenderRightSizingBrowserChangesAndSavings(t *testing.T) {
	report := buildRightSizingReport(rightSizingFixture(), 0.1)
	report.Recommendations = nil
	html, err := renderRightSizingHTML(report)
	if err != nil {
		t.Fatal(err)
	}
	// Inject at the JSON boundary without depending on the concurrent snapshot Go model.
	page := []byte(strings.Replace(string(html), "const $ =", `report.savings = {time: '2026-01-01T12:00:00Z', resources: [
  {cluster: 'cluster-a', resource: 'cpu', before: 10, after: 6, reductions: 5, increases: 1, changedContainers: 3, excludedContainers: 2, unknownContainers: 1},
  {cluster: 'cluster-b', resource: 'cpu', before: 2, after: 4, reductions: 0, increases: 2, changedContainers: 1, excludedContainers: 0, unknownContainers: 0},
  {cluster: 'cluster-a', resource: 'memory', before: 104857600, after: 125829120, reductions: 0, increases: 20971520, changedContainers: 1, excludedContainers: 1, unknownContainers: 0}
]};
const $ =`, 1))
	for _, size := range [][2]int{{1440, 1000}, {390, 844}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			call := rightSizingBrowser(t, page, size[0], size[1])
			result := call("Runtime.evaluate", map[string]any{"expression": `(() => {
  function check(condition, message) { if (!condition) throw new Error(message); }
  function change(id, value) { const node = $(id); if (node.type === 'checkbox') node.checked = value; else node.value = value; node.dispatchEvent(new Event('input', {bubbles: true})); }
  try {
    const base = {resource: 'cpu', cluster: 'cluster-a', namespace: 'ocm-arohcpint-one', kind: 'Deployment', container: 'main', initContainer: false, editable: true, eligible: true, samples: 10, peak: .1, suggested: .12, requestMin: .12, requestMax: .12};
    const cases = [
      ['matched-headroom', {}, false],
      ['matched-rounded', {requestMin: .1, requestMax: .1, peak: .083333, suggested: .1}, false],
      ['below-tolerance', {requestMin: .11, requestMax: .11}, false],
      ['twenty-percent', {requestMin: .1, requestMax: .1}, true],
      ['ten-percent-increase', {requestMin: .1, requestMax: .1, suggested: .11}, false],
      ['ten-percent-decrease', {requestMin: .1, requestMax: .1, suggested: .09}, false],
      ['within-epsilon', {requestMin: .1, requestMax: .1, suggested: .11000000000005}, false],
      ['above-epsilon', {requestMin: .1, requestMax: .1, suggested: .1100000000002}, true],
      ['max-endpoint', {requestMax: .2}, true],
      ['ineligible', {requestMin: .2, requestMax: .2, eligible: false}, false],
      ['unknown-request', {requestMin: null}, false],
      ['unknown-suggestion', {suggested: null}, false],
      ['zero-request', {requestMin: 0, requestMax: 0}, true],
      ['zero-matched', {requestMin: 0, requestMax: 0, suggested: 0, peak: 0}, false],
      ['risk-bypass', {requestMin: .115, requestMax: .115, peak: .14}, true],
      ['risk-boundary', {requestMin: .115, requestMax: .115, peak: 1.2 * .115}, false]
    ];
    for (const resource of resources) for (const [workload, fields] of cases) rows.push({...base, resource, workload, ...fields});
    // Raw actionable/direction flags are deliberately contradictory: the group decides.
    rows.push({...base, workload: 'merged-match', requestMin: .12, requestMax: .12, suggested: .08, actionable: true},
      {...base, workload: 'merged-match', cluster: 'cluster-b', namespace: 'ocm-arohcpstg-two', actionable: false},
      {...base, workload: 'partial-request', requestMin: .2, requestMax: .2},
      {...base, workload: 'partial-request', namespace: 'ocm-arohcpstg-two', requestMax: null},
      {...base, workload: 'managed-unknown', editable: false, eligible: false, requestMin: null, requestMax: null, suggested: null});
    $('reset').click();
    for (const resource of resources) for (const [workload, , actionable] of cases) {
      const group = grouped.find(row => row.resource === resource && row.workload === workload);
      check(group.actionable === actionable, resource + '/' + workload + ' grouped change decision');
      check($(resource + '-rows').textContent.includes('/' + workload) === actionable, resource + '/' + workload + ' default visibility');
    }
    check(grouped.find(row => row.workload === 'matched-headroom').direction === 'matched', 'headroom match is matched, not over');
    check(!grouped.find(row => row.workload === 'merged-match').actionable, 'maximum suggestion is compared after grouping, not raw actionable flags');
    check(!grouped.find(row => row.workload === 'partial-request').actionable, 'partly unknown endpoint cannot authorize change');
    change('cluster', 'cluster-a');
    check(grouped.find(row => row.workload === 'merged-match').actionable, 'cluster selection recomputes grouped suggestion from selected source rows');
    $('reset').click();
    change('changes', false);
    check($('cpu-rows').textContent.includes('/ineligible') && $('cpu-rows').querySelector('.warning'), 'unchecked changes exposes estimates and warnings');
    change('changes', true);
    change('direction', 'unknown');
    check(!$('changes').checked && $('cpu-rows').textContent.includes('/unknown-request'), 'Unknown direction disables changes filter');
    check(!$('cpu-rows').textContent.includes('/managed-unknown'), 'Unknown direction preserves editable filter');
    change('editable', false);
    check($('cpu-rows').textContent.includes('/managed-unknown'), 'unmapped unknown visible only after disabling editable');
    $('reset').click();
    check($('editable').checked && $('changes').checked, 'both defaults restored');

    const summary = $('savings').textContent;
    check($('savings').querySelector('h2').textContent === 'Estimated request savings', 'summary is an estimate, not promised CLI savings');
    check(summary.includes('Hypothetical at 2026-01-01 12:00:00 UTC; known regular-container requests. Independent of table filters.'), 'snapshot timestamp, denominator and filter scope');
    check($('method').textContent.includes('exclude unknown requests and unsupported changes') && $('method').textContent.includes('dev/minimal HCP classes allow decreases') && $('method').textContent.includes('stale-request and limit checks can prevent edits') && $('method').textContent.includes('not node-cost savings'), 'details explain estimate exclusions, assumptions and edit guards');
    check($('savings-cpu').textContent.includes('12 cores -> 10 cores (net 16.67% reduction)'), 'CPU sums concurrent snapshots across clusters in cores');
    check($('savings-cpu').title === 'Reductions: 5 cores; increases: 3 cores', 'gross reductions and increases retained in tooltip');
    check($('savings-cpu').textContent.includes('Known requests (lower bound); 4 changed; 2 excluded; 1 unknown containers'), 'unknown requests cannot imply complete totals');
    check($('savings-memory').textContent.includes('100Mi -> 120Mi (net 20% increase)'), 'negative savings shown as increase');
    change('search', 'no-matching-rows');
    change('direction', 'unknown');
    change('editable', false);
    change('changes', true);
    check($('savings').textContent === summary, 'table filters never alter snapshot summary');
    change('cluster', 'cluster-b');
    check($('savings-cpu').textContent.includes('2 cores -> 4 cores (net 100% increase)'), 'cluster scopes summary independently of empty table');
    check(!$('savings-cpu').textContent.includes('lower bound'), 'fully known selected scope is not partial');
    check($('savings-memory').textContent === 'Memory: unavailable for this snapshot scope.', 'sparse resource is unavailable, not zero');
    change('cluster', 'cluster-a');
    check($('savings-cpu').textContent.includes('10 cores -> 6 cores (net 40% reduction)'), 'cluster-a scoped savings');
    const cpu = report.savings.resources[0];
    cpu.before = 0; cpu.after = 0; render();
    check($('savings-cpu').textContent.includes('0 cores -> 0 cores (no net change (0%))'), 'zero baseline and zero net do not divide by zero');
    cpu.after = 1; render();
    check($('savings-cpu').textContent.includes('net 1 cores increase') && !$('savings').textContent.includes('Infinity'), 'zero baseline increase uses absolute amount');
    cpu.before = cpu.after = 1; render();
    check($('savings-cpu').textContent.includes('no net change (0%)'), 'nonzero equal totals show zero net');
    delete cpu.before; render();
    check($('savings-cpu').textContent.includes('Unknown -> 1 cores (net change unavailable)'), 'missing aggregate is not fabricated');
    report.savings.resources = []; render();
    check(resources.every(resource => $('savings-' + resource).textContent.includes('unavailable')), 'empty snapshot resources stay unavailable');
    delete report.savings; render();
    check($('savings').textContent === 'Savings unavailable: no concurrent snapshot.', 'old reports have concise unavailable summary');
    check(document.documentElement.scrollWidth <= innerWidth, 'summary and new checkbox fit mobile viewport');
    return 'PASS';
  } catch (error) { return 'FAIL: ' + error.message; }
})()`, "returnByValue": true})
			var evaluation struct {
				Result struct{ Value string }
			}
			if err := json.Unmarshal(result, &evaluation); err != nil || evaluation.Result.Value != "PASS" {
				t.Fatalf("changes and savings browser assertions: %s (%v)", result, err)
			}
		})
	}
}

func TestRenderRightSizingBrowserEmptyWarnings(t *testing.T) {
	for _, warnings := range [][]string{nil, {"CPU collection failed", "Memory collection failed"}} {
		report := buildRightSizingReport(rightSizingFixture(), 0.1)
		report.Recommendations = nil
		report.Warnings = warnings
		html, err := renderRightSizingHTML(report)
		if err != nil {
			t.Fatal(err)
		}
		for _, size := range [][2]int{{1440, 1000}, {390, 844}} {
			t.Run(fmt.Sprintf("%d-warnings/%v", len(warnings), size), func(t *testing.T) {
				call := rightSizingBrowser(t, html, size[0], size[1])
				result := call("Runtime.evaluate", map[string]any{"expression": `(() => {
  const details = document.getElementById('method'), summary = details.querySelector('summary');
  const count = document.querySelectorAll('#warnings li').length;
  const rect = summary.getBoundingClientRect();
  return !details.open && rect.width > 0 && rect.height > 0 && rect.top >= 0 && rect.bottom <= innerHeight &&
    getComputedStyle(summary).visibility === 'visible' &&
    summary.textContent === (count ? 'Details (' + count + ' warnings)' : 'Details') &&
    summary.classList.contains('unknown') === (count > 0) &&
    getComputedStyle(summary).color === (count ? 'rgb(227, 179, 65)' : 'rgb(157, 167, 179)') &&
    document.getElementById('filter-status').textContent === '0 of 0 resource rows' &&
    [...document.querySelectorAll('tbody')].every(body => body.textContent === 'No matching rows.') &&
    document.documentElement.scrollWidth <= innerWidth;
})()`, "returnByValue": true})
				var evaluation struct {
					Result struct{ Value bool }
				}
				if err := json.Unmarshal(result, &evaluation); err != nil || !evaluation.Result.Value {
					t.Fatalf("empty report must keep warning classification visible with details collapsed: %s (%v)", result, err)
				}
			})
		}
	}
}

// Use Chrome's pipe transport so viewport emulation needs neither a new browser
// dependency nor a listening debugging port. --window-size has a 500px minimum
// on some Chrome builds and cannot verify a phone's CSS viewport.
func rightSizingBrowser(t *testing.T, html []byte, width, height int) func(string, map[string]any) json.RawMessage {
	t.Helper()
	browser, err := exec.LookPath("google-chrome")
	if err != nil {
		t.Skip("google-chrome unavailable; skipping optional browser test")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "right-sizing.html")
	if err := os.WriteFile(file, html, 0600); err != nil {
		t.Fatal(err)
	}
	toChrome, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = toChrome.Close(); _ = writer.Close() })
	reader, fromChrome, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close(); _ = fromChrome.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, browser, "--headless", "--no-sandbox", "--disable-gpu", "--disable-dev-shm-usage", "--disable-background-networking", "--no-first-run", "--remote-debugging-pipe", "--user-data-dir="+dir)
	cmd.ExtraFiles = []*os.File{toChrome, fromChrome}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() {
			t.Log(stderr.String())
		}
	})
	_ = toChrome.Close()
	_ = fromChrome.Close()
	responses := bufio.NewReader(reader)
	id, session := 0, ""
	call := func(method string, params map[string]any) json.RawMessage {
		t.Helper()
		id++
		request := map[string]any{"id": id, "method": method, "params": params}
		if session != "" {
			request["sessionId"] = session
		}
		data, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(append(data, 0)); err != nil {
			t.Fatal(err)
		}
		for {
			data, err := responses.ReadBytes(0)
			if err != nil {
				t.Fatalf("CDP %s: %v", method, err)
			}
			var response struct {
				ID     int             `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
			}
			if err := json.Unmarshal(data[:len(data)-1], &response); err != nil {
				t.Fatal(err)
			}
			if response.ID != id {
				continue
			}
			if len(response.Error) != 0 {
				t.Fatalf("CDP %s: %s", method, response.Error)
			}
			return response.Result
		}
	}
	var target struct{ TargetID string }
	if err := json.Unmarshal(call("Target.createTarget", map[string]any{"url": "about:blank"}), &target); err != nil {
		t.Fatal(err)
	}
	var attached struct{ SessionID string }
	if err := json.Unmarshal(call("Target.attachToTarget", map[string]any{"targetId": target.TargetID, "flatten": true}), &attached); err != nil {
		t.Fatal(err)
	}
	session = attached.SessionID
	call("Emulation.setDeviceMetricsOverride", map[string]any{"width": width, "height": height, "deviceScaleFactor": 1, "mobile": width < 650})
	pageURL := (&url.URL{Scheme: "file", Path: file}).String()
	call("Page.navigate", map[string]any{"url": pageURL})
	for {
		result := call("Runtime.evaluate", map[string]any{
			"expression":    fmt.Sprintf(`location.href === %q && document.readyState === 'complete' && innerWidth === %d && innerHeight === %d`, pageURL, width, height),
			"returnByValue": true,
		})
		var evaluation struct {
			Result struct{ Value bool }
		}
		if err := json.Unmarshal(result, &evaluation); err != nil {
			t.Fatal(err)
		}
		if evaluation.Result.Value {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("page failed to load at exact CSS viewport %dx%d: %s", width, height, result)
		case <-time.After(10 * time.Millisecond):
		}
	}
	return call
}

func TestRenderRightSizingBrowserReplay(t *testing.T) {
	input := os.Getenv("RIGHT_SIZING_REPORT")
	if input == "" {
		t.Skip("set RIGHT_SIZING_REPORT to replay a collected right-sizing.json")
	}
	data, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	var report rightSizingReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	html, err := renderRightSizingHTML(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []struct {
		name          string
		width, height int
	}{{"desktop", 1440, 1000}, {"mobile", 390, 844}} {
		t.Run(size.name, func(t *testing.T) {
			call := rightSizingBrowser(t, html, size.width, size.height)
			result := call("Runtime.evaluate", map[string]any{"expression": `JSON.stringify({width: innerWidth, height: innerHeight, scrollWidth: document.documentElement.scrollWidth, timestampWidth: document.getElementById('window').scrollWidth, timestampClientWidth: document.getElementById('window').clientWidth})`, "returnByValue": true})
			t.Logf("viewport measurements: %s", result)
			result = call("Runtime.evaluate", map[string]any{"expression": `document.documentElement.scrollWidth <= innerWidth && document.getElementById('window').scrollWidth <= document.getElementById('window').clientWidth`, "returnByValue": true})
			var evaluation struct {
				Result struct{ Value bool }
			}
			if err := json.Unmarshal(result, &evaluation); err != nil || !evaluation.Result.Value {
				t.Fatalf("actual report overflows viewport: %s (%v)", result, err)
			}
			if output := os.Getenv("RIGHT_SIZING_SCREENSHOT_DIR"); output != "" {
				var screenshot struct{ Data string }
				if err := json.Unmarshal(call("Page.captureScreenshot", map[string]any{"format": "png", "captureBeyondViewport": false}), &screenshot); err != nil {
					t.Fatal(err)
				}
				png, err := base64.StdEncoding.DecodeString(screenshot.Data)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(output, "right-sizing-"+size.name+".png"), png, 0644); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
