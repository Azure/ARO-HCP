// Copyright 2025 Microsoft Corporation
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

package rightsize

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/tooling/rightsize-requests/internal/grafana"
)

type fakeGrafana struct {
	datasources []grafana.Datasource
	// results maps a substring found in the query ("memory"/"cpu") to samples.
	byDS map[string]map[string][]grafana.Sample
}

func (f *fakeGrafana) ListDatasources(context.Context) ([]grafana.Datasource, error) {
	return f.datasources, nil
}

func (f *fakeGrafana) InstantQuery(_ context.Context, dsUID, query string) ([]grafana.Sample, error) {
	kind := "cpu"
	if strings.Contains(query, "container_memory_working_set_bytes") {
		kind = "memory"
	}
	return f.byDS[dsUID][kind], nil
}

const runConfig = `defaults:
  backend:
    k8s:
      resources:
        requests:
          cpu: 100m
          memory: 1Gi
  frontend:
    k8s:
      resources:
        requests:
          cpu: 100m
          memory: 512Mi
`

func TestRunAppliesPeakAcrossClusters(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(runConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	const gi = 1024 * 1024 * 1024
	fg := &fakeGrafana{
		datasources: []grafana.Datasource{
			{UID: "c1", Name: "prod-eastus", Type: "prometheus"},
			{UID: "c2", Name: "prod-westus", Type: "prometheus"},
			{UID: "x", Name: "azure-monitor", Type: "grafana-azure-monitor-datasource"},
		},
		byDS: map[string]map[string][]grafana.Sample{
			"c1": {
				// backend: 0.35 cores, 1.2Gi -> peak here
				"cpu":    {{Labels: map[string]string{"namespace": "aro-hcp", "container": "aro-hcp-backend"}, Value: 0.35}},
				"memory": {{Labels: map[string]string{"namespace": "aro-hcp", "container": "aro-hcp-backend"}, Value: 1.2 * gi}},
			},
			"c2": {
				// backend cpu lower here, memory higher (1.6Gi) -> memory peak
				"cpu":    {{Labels: map[string]string{"namespace": "aro-hcp", "container": "aro-hcp-backend"}, Value: 0.20}},
				"memory": {{Labels: map[string]string{"namespace": "aro-hcp", "container": "aro-hcp-backend"}, Value: 1.6 * gi}},
			},
		},
	}

	err := Run(context.Background(), logr.Discard(), fg, Options{
		ConfigPath:      path,
		Window:          "14d",
		Step:            "5m",
		Margin:          1.25,
		FleetPercentile: 0, // max across the pod/cluster population
	})
	if err != nil {
		t.Fatal(err)
	}

	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)

	// backend cpu: max(0.35,0.20)=0.35 * 1.25 = 0.4375 cores -> ceil to 440m
	if !strings.Contains(got, "cpu: 440m") {
		t.Errorf("expected backend cpu 440m, got:\n%s", got)
	}
	// backend memory: max(1.2,1.6)=1.6Gi * 1.25 = 2.0Gi -> 2Gi
	if !strings.Contains(got, "memory: 2Gi") {
		t.Errorf("expected backend memory 2Gi, got:\n%s", got)
	}
	// frontend had no data; must remain unchanged.
	if !strings.Contains(got, "cpu: 100m") {
		t.Errorf("expected frontend cpu untouched at 100m, got:\n%s", got)
	}
}

// TestRunFleetPercentileIgnoresOutlier proves that a single anomalous pod (e.g.
// one cluster's runaway container) does not drive the fleet-wide request when a
// fleet percentile is used.
func TestRunFleetPercentileIgnoresOutlier(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(runConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	const gi = 1024 * 1024 * 1024
	// 20 backend pods across one datasource: 19 at ~0.30 cores / 1.0Gi, and one
	// runaway pod at 5 cores / 8Gi.
	var cpu, mem []grafana.Sample
	for i := 0; i < 19; i++ {
		lbl := map[string]string{"namespace": "aro-hcp", "container": "aro-hcp-backend", "pod": "backend-" + string(rune('a'+i))}
		cpu = append(cpu, grafana.Sample{Labels: lbl, Value: 0.30})
		mem = append(mem, grafana.Sample{Labels: lbl, Value: 1.0 * gi})
	}
	outlier := map[string]string{"namespace": "aro-hcp", "container": "aro-hcp-backend", "pod": "backend-runaway"}
	cpu = append(cpu, grafana.Sample{Labels: outlier, Value: 5.0})
	mem = append(mem, grafana.Sample{Labels: outlier, Value: 8.0 * gi})

	fg := &fakeGrafana{
		datasources: []grafana.Datasource{{UID: "c1", Name: "prod", Type: "prometheus"}},
		byDS:        map[string]map[string][]grafana.Sample{"c1": {"cpu": cpu, "memory": mem}},
	}

	if err := Run(context.Background(), logr.Discard(), fg, Options{
		ConfigPath: path, Window: "14d", Step: "5m", Margin: 1.25, FleetPercentile: 0.95,
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	s := string(got)

	// p95 of the population lands near the normal pods (~0.30 cores / 1.0Gi),
	// NOT the 5-core / 8Gi outlier (which under max would give 6250m / 10Gi).
	if strings.Contains(s, "cpu: 6250m") || strings.Contains(s, "memory: 10Gi") {
		t.Errorf("outlier drove the request; got:\n%s", s)
	}
	// backend should be right-sized to the fleet p95: 0.535 cores*1.25 -> 670m,
	// 1.35Gi*1.25 -> 1728Mi.
	if !strings.Contains(s, "cpu: 670m") {
		t.Errorf("expected backend cpu right-sized to 670m (fleet p95), got:\n%s", s)
	}
	if !strings.Contains(s, "memory: 1728Mi") {
		t.Errorf("expected backend memory right-sized to 1728Mi (fleet p95), got:\n%s", s)
	}
}

func TestRunDryRunDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(runConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	fg := &fakeGrafana{
		datasources: []grafana.Datasource{{UID: "c1", Name: "prod", Type: "prometheus"}},
		byDS: map[string]map[string][]grafana.Sample{
			"c1": {"cpu": {{Labels: map[string]string{"namespace": "aro-hcp", "container": "aro-hcp-backend"}, Value: 0.9}}},
		},
	}
	if err := Run(context.Background(), logr.Discard(), fg, Options{
		ConfigPath: path, Margin: 1.25, DryRun: true,
	}); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(path)
	if string(out) != runConfig {
		t.Errorf("dry-run modified the file:\n%s", out)
	}
}

// TestRunWritesOverlayWithLimits exercises the source->target flow: read current
// values from a base config, and write sparse overrides (with the limits=2x rule)
// into a separate overlay file, inserting missing blocks.
func TestRunWritesOverlayWithLimits(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "config.yaml")
	overlay := filepath.Join(dir, "overlay.yaml")

	const baseCfg = `defaults:
  arobit:
    forwarder:
      resources:
        requests:
          cpu: 100m
          memory: 256Mi
        limits:
          memory: 1248Mi
`
	const overlayCfg = `clouds:
  public:
    defaults:
      arobit:
        kusto:
          enabled: true
`
	if err := os.WriteFile(base, []byte(baseCfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overlay, []byte(overlayCfg), 0o644); err != nil {
		t.Fatal(err)
	}

	const gi = 1024 * 1024 * 1024
	fg := &fakeGrafana{
		datasources: []grafana.Datasource{{UID: "c1", Name: "prod", Type: "prometheus"}},
		byDS: map[string]map[string][]grafana.Sample{
			"c1": {
				"cpu":    {{Labels: map[string]string{"namespace": "arobit", "container": "fluentbit", "pod": "arobit-forwarder-1"}, Value: 0.5}},
				"memory": {{Labels: map[string]string{"namespace": "arobit", "container": "fluentbit", "pod": "arobit-forwarder-1"}, Value: 1.0 * gi}},
			},
		},
	}

	if err := Run(context.Background(), logr.Discard(), fg, Options{
		ConfigPath:      base,
		SourcePrefix:    "defaults",
		WritePath:       overlay,
		WritePrefix:     "clouds.public.defaults",
		Margin:          1.25,
		FleetPercentile: 0, // max, for a deterministic single-pod population
		LimitMultiple:   2.0,
	}); err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(overlay)
	want := `clouds:
  public:
    defaults:
      arobit:
        kusto:
          enabled: true
        forwarder:
          resources:
            requests:
              cpu: 630m
              memory: 1280Mi
            limits:
              memory: 2560Mi
`
	if string(got) != want {
		t.Fatalf("overlay mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}

	// Base config must be untouched.
	baseAfter, _ := os.ReadFile(base)
	if string(baseAfter) != baseCfg {
		t.Fatalf("base config was modified:\n%s", baseAfter)
	}
}

func TestBuildCommitMessage(t *testing.T) {
	changes := []change{
		{service: "backend", resource: "cpu", oldValue: "100m", newValue: "1780m", explore: "https://g/explore?x=1"},
		{service: "backend", resource: "memory", oldValue: "1Gi", newValue: "1888Mi", explore: "https://g/explore?x=1"},
		{service: "arobit.forwarder", resource: "cpu", oldValue: "100m", newValue: "210m"},
		{service: "arobit.forwarder", resource: "memory", oldValue: "256Mi", newValue: "1456Mi"},
		{service: "arobit.forwarder", resource: "mem-limit", oldValue: "1248Mi", newValue: "2896Mi", reason: "2x request"},
	}
	msg := buildCommitMessage(changes, Options{Window: "14d", Margin: 1.25, Percentile: 0.95, FleetPercentile: 0.95})

	for _, want := range []string{
		"hcp: right-size service CPU/memory requests from prod usage",
		"fleet p95 of",
		"p95-over-14d usage x1.25 margin",
		"- backend: cpu 100m -> 1780m, memory 1Gi -> 1888Mi",
		"  explore: https://g/explore?x=1",
		"- arobit.forwarder: cpu 100m -> 210m, memory 256Mi -> 1456Mi, limit 1248Mi -> 2896Mi (2x request)",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("commit message missing %q\n---\n%s", want, msg)
		}
	}
}

func TestNearestDSPicksRepresentativeNotOutlier(t *testing.T) {
	// arobit-like: two normal clusters near the p95, one outlier cluster.
	const gi = 1024 * 1024 * 1024
	byDS := map[string]float64{
		"services-uksouth":    1.29 * gi,
		"services-westeurope": 1.13 * gi,
		"services-eastus2":    7.5 * gi, // the outlier we exclude via fleet p95
	}
	chosen := 1.16 * gi // fleet p95 landed here
	got := nearestDS(byDS, chosen)
	if got == "services-eastus2" {
		t.Fatalf("link pinned to the excluded outlier cluster: %s", got)
	}
	if got != "services-westeurope" {
		t.Errorf("expected nearest to be services-westeurope, got %s", got)
	}
}
