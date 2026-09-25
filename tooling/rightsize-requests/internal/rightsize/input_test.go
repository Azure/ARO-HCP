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

package rightsize

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/tooling/rightsize-requests/internal/editor"
)

func number(v float64) *float64 { return &v }

func inputRow(resource string, peak, suggested, current float64, quantity string) inputRecommendation {
	return inputRecommendation{
		Cluster: "svc", Namespace: "aro-hcp", Kind: "Deployment", Workload: "backend", Container: "aro-hcp-backend",
		Resource: resource, Replicas: 2, MeasuredReplicas: 2, Peak: number(peak), BurstPeak: number(peak),
		RequestMin: number(current), RequestMax: number(current), Suggested: number(suggested),
		SuggestedQuantity: quantity, Delta: number(suggested - current), Direction: "increase", Eligible: true, Warnings: []string{},
	}
}

func inputJSON(t *testing.T, rows ...inputRecommendation) string {
	t.Helper()
	if rows == nil {
		rows = []inputRecommendation{}
	}
	data, err := json.Marshal(inputReport{
		Version: 1, Start: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC),
		Headroom: 1.2, ChangeThreshold: 0.1, CPUWindow: "10m", Warnings: []string{}, Recommendations: rows,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func inputFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func fileContents(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Capture the user-visible skip reasons, not just the absence of edits.
func runInputOutput(t *testing.T, path string, opts Options) string {
	t.Helper()
	out, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	old := os.Stdout
	os.Stdout = out
	defer func() { os.Stdout = old }()
	if err := RunInput(context.Background(), logr.Discard(), path, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := out.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestInputValidation(t *testing.T) {
	valid := inputJSON(t, inputRow("cpu", .2, .24, .1, "240m"))
	tests := map[string]string{
		"valid":                  valid,
		"2m":                     strings.Replace(valid, `"10m"`, `"2m"`, 1),
		"signed delta":           strings.Replace(valid, `"delta":0.`, `"delta":-0.`, 1),
		"unknown field":          strings.Replace(valid, `"version":1`, `"version":1,"extra":true`, 1),
		"unknown row field":      strings.Replace(valid, `"eligible":true`, `"eligible":true,"extra":0`, 1),
		"duplicate":              strings.Replace(valid, `"version":1`, `"version":1,"version":1`, 1),
		"case insensitive field": strings.Replace(valid, `"eligible"`, `"Eligible"`, 1),
		"missing bool":           strings.Replace(valid, `"initContainer":false,`, ``, 1),
		"null bool":              strings.Replace(valid, `"initContainer":false`, `"initContainer":null`, 1),
		"null list":              strings.Replace(valid, `"warnings":[]`, `"warnings":null`, 1),
		"trailing object":        valid + `{}`,
		"trailing junk":          valid + `oops`,
		"top level null":         `null`,
		"version":                strings.Replace(valid, `"version":1`, `"version":2`, 1),
		"headroom":               strings.Replace(valid, `"headroom":1.2`, `"headroom":1.25`, 1),
		"threshold missing":      strings.Replace(valid, `"changeThreshold":0.1,`, ``, 1),
		"threshold negative":     strings.Replace(valid, `"changeThreshold":0.1`, `"changeThreshold":-0.1`, 1),
		"threshold too large":    strings.Replace(valid, `"changeThreshold":0.1`, `"changeThreshold":1.1`, 1),
		"threshold infinite":     strings.Replace(valid, `"changeThreshold":0.1`, `"changeThreshold":1e999`, 1),
		"threshold null":         strings.Replace(valid, `"changeThreshold":0.1`, `"changeThreshold":null`, 1),
		"actionable missing":     strings.Replace(valid, `"actionable":false,`, ``, 1),
		"actionable null":        strings.Replace(valid, `"actionable":false`, `"actionable":null`, 1),
		"actionable wrong type":  strings.Replace(valid, `"actionable":false`, `"actionable":1`, 1),
		"alert risk missing":     strings.Replace(valid, `"alertRisk":false,`, ``, 1),
		"alert risk null":        strings.Replace(valid, `"alertRisk":false`, `"alertRisk":null`, 1),
		"alert risk wrong type":  strings.Replace(valid, `"alertRisk":false`, `"alertRisk":"false"`, 1),
		"window":                 strings.Replace(valid, `"10m"`, `"5m"`, 1),
		"time":                   strings.Replace(valid, `2026-09-01`, `2026-09-03`, 1),
		"resource":               strings.Replace(valid, `"resource":"cpu"`, `"resource":"disk"`, 1),
		"negative":               strings.Replace(valid, `"peak":0.2`, `"peak":-0.2`, 1),
		"infinite":               strings.Replace(valid, `"peak":0.2`, `"peak":1e999`, 1),
		"nan quantity":           strings.Replace(valid, `"240m"`, `"NaN"`, 1),
		"negative replicas":      strings.Replace(valid, `"replicas":2`, `"replicas":-2`, 1),
		"fractional replicas":    strings.Replace(valid, `"replicas":2`, `"replicas":2.5`, 1),
		"too many measured":      strings.Replace(valid, `"measuredReplicas":2`, `"measuredReplicas":3`, 1),
		"range inverted":         strings.Replace(valid, `"requestMin":0.1`, `"requestMin":0.3`, 1),
		"quantity mismatch":      strings.Replace(valid, `"240m"`, `"250m"`, 1),
		"peak mismatch":          strings.Replace(valid, `"peak":0.2`, `"peak":0.3`, 1),
		"missing suggested":      strings.Replace(valid, `"suggested":0.24`, `"suggested":null`, 1),
		"newline injection":      strings.Replace(valid, `"240m"`, `"240m\n"`, 1),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := readInput(inputFile(t, "input.json", data))
			wantValid := name == "valid" || name == "2m" || name == "signed delta"
			if (err == nil) != wantValid {
				t.Fatalf("valid=%v, err=%v", wantValid, err)
			}
		})
	}
	for _, threshold := range []string{"0", "1"} {
		data := strings.Replace(valid, `"changeThreshold":0.1`, `"changeThreshold":`+threshold, 1)
		if _, err := readInput(inputFile(t, "input.json", data)); err != nil {
			t.Fatalf("valid threshold %s: %v", threshold, err)
		}
	}
}

func TestInputRounding(t *testing.T) {
	const mi = 1 << 20
	for _, row := range []inputRecommendation{
		inputRow("cpu", 0, .01, .1, "10m"),
		inputRow("cpu", .012, .01, .1, "10m"),  // equality at 120% is safe
		inputRow("cpu", .0124, .02, .1, "20m"), // guard overrides nearest rounding
		inputRow("cpu", .201, .24, .1, "240m"),
		inputRow("cpu", .207, .25, .1, "250m"),
		inputRow("cpu", .125, .15, .1, "150m"),
		inputRow("cpu", .1875, .23, .1, "230m"),
		inputRow("cpu", .1875-1e-10, .22, .1, "220m"),
		inputRow("cpu", .1875+1e-10, .23, .1, "230m"),
		inputRow("cpu", .1, .12, .1, "120m"),
		inputRow("memory", 0, 10*mi, 20*mi, "10Mi"),
		inputRow("memory", 12*mi, 10*mi, 20*mi, "10Mi"),
		inputRow("memory", 12.4*mi, 20*mi, 20*mi, "20Mi"),
		inputRow("memory", 100.1*mi, 120*mi, 20*mi, "120Mi"),
		inputRow("memory", 105*mi, 130*mi, 20*mi, "130Mi"),
		inputRow("memory", 100*mi, 120*mi, 20*mi, "120Mi"),
		inputRow("memory", 187.5*mi, 230*mi, 20*mi, "230Mi"),
		inputRow("memory", (187.5-1e-7)*mi, 220*mi, 20*mi, "220Mi"),
		inputRow("memory", (187.5+1e-7)*mi, 230*mi, 20*mi, "230Mi"),
	} {
		if _, err := readInput(inputFile(t, "input.json", inputJSON(t, row))); err != nil {
			t.Errorf("%s peak=%g: %v", row.Resource, *row.Peak, err)
		}
	}
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -1} {
		if finiteNonnegative(value) {
			t.Errorf("accepted %v", value)
		}
	}
	for _, row := range []inputRecommendation{
		inputRow("cpu", .201, .25, .1, "250m"),  // ceiling instead of nearest
		inputRow("cpu", .1875, .22, .1, "220m"), // half tie must round up despite noise
		inputRow("cpu", .1875-1e-10, .23, .1, "230m"),
		inputRow("cpu", .1875+1e-10, .22, .1, "220m"),
		inputRow("memory", 187.5*mi, 220*mi, 20*mi, "220Mi"),
		inputRow("memory", (187.5-1e-7)*mi, 230*mi, 20*mi, "230Mi"),
		inputRow("cpu", .0124, .01, .1, "10m"), // nearest rounding without guard
		inputRow("cpu", .012, .02, .1, "20m"),  // equality must not trigger guard
		inputRow("memory", 12.4*mi, 10*mi, 20*mi, "10Mi"),
		inputRow("memory", 12*mi, 20*mi, 20*mi, "20Mi"),
		inputRow("memory", 100.1*mi, 121*mi, 20*mi, "121Mi"),
		inputRow("memory", 100*mi, 128*mi, 20*mi, "128Mi"), // Grafana rounding
		inputRow("cpu", 0, 0, .1, "0m"),
		inputRow("memory", 0, mi, 20*mi, "1Mi"),
	} {
		if _, err := readInput(inputFile(t, "input.json", inputJSON(t, row))); err == nil {
			t.Errorf("accepted incorrect rounding: %+v", row)
		}
	}
}

func TestInputCanonicalQuantities(t *testing.T) {
	for _, resource := range []string{"cpu", "memory"} {
		suffix, scale := "m", .001
		if resource == "memory" {
			suffix, scale = "Mi", 1<<20
		}
		for _, digits := range []string{"0xfp+4", "0x1.ep7", "2.4e2", "240.0", "+240", "0240", "2_40", "240 ", " 240", "240\n", "0", "-240", "245", "240.0001"} {
			t.Run(resource+"/"+digits, func(t *testing.T) {
				row := inputRow(resource, 200*scale, 240*scale, 100*scale, digits+suffix)
				// Even with no peak, noncanonical/off-grid quantities must fail.
				row.Peak = nil
				if digits == "245" || digits == "240.0001" {
					value, err := inputParser(resource)(digits + suffix)
					if err != nil {
						t.Fatal(err)
					}
					row.Suggested = number(value)
				}
				if _, err := readInput(inputFile(t, "input.json", inputJSON(t, row))); err == nil {
					t.Fatalf("accepted noncanonical quantity %q", row.SuggestedQuantity)
				}
			})
		}
	}
	for _, row := range []inputRecommendation{
		inputRow("cpu", 1, 1.2, .1, "1.2"),
		inputRow("memory", 1000*(1<<20), 1200*(1<<20), 100*(1<<20), "1.171875Gi"),
		inputRow("cpu", .2, .2406, .1, "240m"),
	} {
		if _, err := readInput(inputFile(t, "input.json", inputJSON(t, row))); err == nil {
			t.Fatalf("accepted noncanonical or mismatched quantity %q", row.SuggestedQuantity)
		}
	}
}

func TestRunInputLimits(t *testing.T) {
	for _, resource := range []string{"cpu", "memory"} {
		t.Run(resource, func(t *testing.T) {
			suffix, scale := "m", .001
			if resource == "memory" {
				suffix, scale = "Mi", 1<<20
			}
			// A blank argument omits the limit; "''" represents a blank scalar.
			block := func(prefix, limit string) string {
				out := fmt.Sprintf("%sbackend:\n%s  k8s:\n%s    resources:\n%s      requests:\n%s        %s: 100%s\n", prefix, prefix, prefix, prefix, prefix, resource, suffix)
				if limit != "" {
					out += fmt.Sprintf("%s      limits:\n%s        %s: %s # retain limit\n", prefix, prefix, resource, limit)
				}
				return out
			}
			for _, tc := range []struct {
				name, global, dev, overlay, reason string
				separate                           bool
			}{
				{name: "default rejects", global: "200" + suffix, reason: "exceeds effective limit"},
				{name: "default equality", global: "240" + suffix},
				{name: "dev rejects", global: "500" + suffix, dev: "200" + suffix, reason: "exceeds effective limit"},
				{name: "dev equality overrides lower default", global: "200" + suffix, dev: "240" + suffix},
				{name: "overlay rejects", global: "500" + suffix, dev: "500" + suffix, overlay: "200" + suffix, separate: true, reason: "exceeds effective limit"},
				{name: "overlay equality", global: "200" + suffix, dev: "200" + suffix, overlay: "240" + suffix, separate: true},
				{name: "overlay falls back to source dev", global: "500" + suffix, dev: "200" + suffix, separate: true, reason: "exceeds effective limit"},
				{name: "overlay falls back to source defaults", global: "200" + suffix, separate: true, reason: "exceeds effective limit"},
				{name: "absent"},
				{name: "blank override", global: "200" + suffix, dev: "''"},
				{name: "zero override", global: "200" + suffix, dev: "0"},
				{name: "zero quantity", dev: "0" + suffix},
				{name: "unlimited overlay", global: "200" + suffix, dev: "200" + suffix, overlay: "unlimited", separate: true},
				{name: "NONE sentinel", global: "NONE"},
				{name: "malformed default", global: "oops", reason: "malformed effective limit"},
				{name: "malformed dev no fallback", global: "500" + suffix, dev: "oops", reason: "malformed effective limit"},
				{name: "malformed overlay no fallback", global: "500" + suffix, dev: "500" + suffix, overlay: "oops", separate: true, reason: "malformed effective limit"},
				{name: "mapping limit", dev: "{bad: value}", reason: "malformed effective limit"},
				{name: "sequence limit", dev: "[240]", reason: "malformed effective limit"},
				{name: "negative", dev: "-1", reason: "malformed effective limit"},
				{name: "nan", dev: "NaN", reason: "malformed effective limit"},
				{name: "infinite", dev: "Inf", reason: "malformed effective limit"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					before := "defaults:\n" + block("  ", tc.global)
					if tc.dev != "" {
						before += "clouds:\n  dev:\n    defaults:\n" + block("      ", tc.dev)
					}
					config := inputFile(t, "config.yaml", before)
					opts := Options{ConfigPath: config}
					writePath, writeBefore := config, before
					if tc.separate {
						writeBefore = "clouds:\n  dev:\n    defaults:\n" + block("      ", tc.overlay)
						opts.WritePath = inputFile(t, "overlay.yaml", writeBefore)
						writePath = opts.WritePath
					}
					row := inputRow(resource, 200*scale, 240*scale, 100*scale, "240"+suffix)
					input := inputFile(t, "input.json", inputJSON(t, row))
					output := runInputOutput(t, input, opts)
					if tc.reason != "" {
						if !strings.Contains(output, "WARNING limit: SKIP") || !strings.Contains(output, tc.reason) {
							t.Fatalf("expected %q warning: %s", tc.reason, output)
						}
						if fileContents(t, writePath) != writeBefore {
							t.Fatal("limit-blocked update changed config")
						}
					} else {
						if !strings.Contains(output, "CHANGE") {
							t.Fatalf("valid request blocked: %s", output)
						}
						want := writeBefore
						if tc.dev != "" || tc.separate {
							want = strings.Replace(want, "              "+resource+": 100"+suffix, "              "+resource+": 240"+suffix, 1)
						} else {
							want += "clouds:\n  dev:\n    defaults:\n" + strings.Replace(block("      ", ""), "100"+suffix, "240"+suffix, 1)
						}
						if got := fileContents(t, writePath); got != want {
							t.Fatalf("changed limits or wrong request edit:\ngot:\n%s\nwant:\n%s", got, want)
						}
					}
					if tc.separate && fileContents(t, config) != before {
						t.Fatal("modified source while writing overlay")
					}
				})
			}
		})
	}
}

func TestRunInputControllerWhitelist(t *testing.T) {
	for _, kind := range []string{"Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job", "CronJob", "Pod", "Node", "DeploymentConfig", "ReplicationController", "Unknown", "CustomController"} {
		t.Run(kind, func(t *testing.T) {
			row := inputRow("cpu", .2, .24, .2, "240m")
			row.Kind = kind
			config := inputFile(t, "config.yaml", inputConfig)
			input := inputFile(t, "input.json", inputJSON(t, row))
			output := runInputOutput(t, input, Options{ConfigPath: config})
			want := inputConfig
			switch kind {
			case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job", "CronJob":
				want = strings.Replace(want, "200m #", "240m #", 1)
			default:
				if !strings.Contains(output, "SKIP blocked") {
					t.Fatalf("missing unknown controller reason: %s", output)
				}
			}
			if fileContents(t, config) != want {
				t.Fatalf("unexpected update for controller %s", kind)
			}
		})
	}
}

func TestRunInputMalformedRequestOverrides(t *testing.T) {
	for name, requests := range map[string]string{
		"scalar ancestor":   "requests: invalid\n",
		"sequence ancestor": "requests: []\n",
		"mapping leaf":      "requests:\n              cpu: {}\n",
		"sequence leaf":     "requests:\n              cpu: [100m]\n",
	} {
		for _, location := range []string{"source dev", "source dev with separate target", "target dev"} {
			for _, dryRun := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/dryRun=%t", name, location, dryRun), func(t *testing.T) {
					malformed := "clouds:\n  dev:\n    defaults:\n      backend:\n        k8s:\n          resources:\n            " + requests
					sourceBefore := runConfig + malformed
					targetBefore := "# sparse overlay\n"
					if location == "target dev" {
						// Both source dev and global requests are valid, but neither
						// may replace a malformed higher-precedence target override.
						sourceBefore = inputConfig
						targetBefore = malformed
					}
					config := inputFile(t, "config.yaml", sourceBefore)
					opts := Options{ConfigPath: config, DryRun: dryRun}
					if location != "source dev" {
						opts.WritePath = inputFile(t, "overlay.yaml", targetBefore)
					}
					row := inputRow("cpu", .2, .24, .1, "240m")
					row.RequestMax = number(.2)
					input := inputFile(t, "input.json", inputJSON(t, row))
					output := runInputOutput(t, input, opts)
					if !strings.Contains(output, "WARNING request: SKIP clouds.dev.defaults.backend.k8s.resources.requests.cpu: malformed effective current request") || strings.Contains(output, "CHANGE") {
						t.Fatalf("expected explicit resource skip, got: %s", output)
					}
					if fileContents(t, config) != sourceBefore {
						t.Fatal("malformed request override modified source config")
					}
					if opts.WritePath != "" && fileContents(t, opts.WritePath) != targetBefore {
						t.Fatal("malformed request override modified target config")
					}
				})
			}
		}
	}
}

const inputConfig = `# preserve this file
defaults:
  backend:
    k8s:
      resources:
        requests:
          cpu: 100m
          memory: 100Mi
        limits:
          cpu: 2
          memory: 1Gi
clouds:
  public:
    defaults:
      backend:
        k8s:
          resources:
            requests:
              cpu: 900m
              memory: 900Mi
            limits:
              memory: 2Gi
  dev:
    defaults:
      backend:
        k8s:
          resources:
            requests:
              cpu: 200m # effective dev value
              memory: 200Mi
            limits:
              cpu: 1
              memory: 500Mi
`

func TestRunInputMaxScopeAndIdempotence(t *testing.T) {
	const mi = 1 << 20
	large := inputRow("cpu", .4, .48, .2, "480m")
	small := inputRow("cpu", .2, .24, .2, "240m")
	small.Cluster = "mgmt"
	memory := inputRow("memory", 200*mi, 240*mi, 200*mi, "240Mi")
	for _, rows := range [][]inputRecommendation{{large, small, memory}, {memory, small, large}} {
		config := inputFile(t, "config.yaml", inputConfig)
		input := inputFile(t, "input.json", inputJSON(t, rows...))
		opts := Options{ConfigPath: config}
		runInputOutput(t, input, opts)
		want := strings.Replace(inputConfig, "200m #", "480m #", 1)
		want = strings.Replace(want, "memory: 200Mi", "memory: 240Mi", 1)
		if got := fileContents(t, config); got != want {
			t.Fatalf("edits outside dev requests or incorrect max:\n%s", got)
		}
		if output := runInputOutput(t, input, opts); !strings.Contains(output, "NOOP") || strings.Contains(output, "stale") {
			t.Fatalf("expected idempotent noop, got %s", output)
		}
		if fileContents(t, config) != want {
			t.Fatal("second run modified config")
		}
	}
}

func TestRunInputUpsertAndEffectiveCurrent(t *testing.T) {
	for _, separate := range []bool{false, true} {
		config := inputFile(t, "base.yaml", runConfig)
		opts := Options{ConfigPath: config}
		if separate {
			opts.WritePath = inputFile(t, "overlay.yaml", "clouds:\n  public:\n    untouched: true\n")
		}
		input := inputFile(t, "input.json", inputJSON(t, inputRow("cpu", .2, .24, .1, "240m")))
		runInputOutput(t, input, opts)
		path := config
		if separate {
			path = opts.WritePath
			if fileContents(t, config) != runConfig {
				t.Fatal("source changed while writing overlay")
			}
		} else if !strings.HasPrefix(fileContents(t, config), runConfig) {
			t.Fatal("global defaults modified by upsert")
		}
		ed, err := editor.New(path)
		if err != nil {
			t.Fatal(err)
		}
		value, _, err := ed.Get("clouds.dev.defaults.backend.k8s.resources.requests.cpu")
		if err != nil || value != "240m" {
			t.Fatalf("upsert: value=%s err=%v", value, err)
		}
	}
	// A dev override in the base file still precedes defaults when writing a
	// separate overlay. The report observed 200m, not the global 100m.
	base := inputFile(t, "base.yaml", inputConfig)
	overlay := inputFile(t, "overlay.yaml", "# sparse overlay\n")
	input := inputFile(t, "input.json", inputJSON(t, inputRow("cpu", .2, .24, .2, "240m")))
	output := runInputOutput(t, input, Options{ConfigPath: base, WritePath: overlay})
	if !strings.Contains(output, "CHANGE") || fileContents(t, base) != inputConfig {
		t.Fatalf("effective base dev value not used: %s", output)
	}
}

func TestRunInputStaleRangesAndDecreases(t *testing.T) {
	for _, tc := range []struct {
		name, current, want string
		allow, dry          bool
	}{
		{name: "gap", current: "200m", want: "WARNING stale"},
		{name: "recent bump", current: "500m", want: "WARNING stale", allow: true},
		{name: "first range", current: "110m", want: "CHANGE"},
		{name: "second range", current: "310m", want: "requires --allow-decrease"},
		{name: "small decrease", current: "310m", want: "CHANGE", allow: true},
		{name: "equal outside ranges", current: "0.24", want: "NOOP"},
		{name: "dry run", current: "100m", want: "CHANGE", dry: true},
		{name: "sentinel", current: "NONE", want: "nonnumeric"},
		{name: "nan", current: "NaN", want: "nonnumeric"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := inputRow("cpu", .2, .24, .1, "240m")
			a.RequestMax = number(.11)
			b := a
			b.Cluster, b.RequestMin, b.RequestMax = "mgmt", number(.3), number(.31)
			before := strings.Replace(inputConfig, "200m #", tc.current+" #", 1)
			config := inputFile(t, "config.yaml", before)
			input := inputFile(t, "input.json", inputJSON(t, a, b))
			output := runInputOutput(t, input, Options{ConfigPath: config, AllowDecrease: tc.allow, DryRun: tc.dry})
			if !strings.Contains(output, tc.want) {
				t.Fatalf("expected %q, got %s", tc.want, output)
			}
			want := before
			if tc.want == "CHANGE" && !tc.dry {
				want = strings.Replace(before, tc.current+" #", "240m #", 1)
			}
			if fileContents(t, config) != want {
				t.Fatal("unexpected config edits")
			}
		})
	}
}

func TestRunInputBlocksEntireMappingResource(t *testing.T) {
	for name, mutate := range map[string]func(*inputRecommendation){
		"ineligible":        func(r *inputRecommendation) { r.Eligible = false },
		"init":              func(r *inputRecommendation) { r.InitContainer = true },
		"unknown owner":     func(r *inputRecommendation) { r.Kind = "Unknown" },
		"missing owner":     func(r *inputRecommendation) { r.Workload = "" },
		"missing peak":      func(r *inputRecommendation) { r.Peak = nil },
		"missing suggested": func(r *inputRecommendation) { r.Suggested = nil; r.SuggestedQuantity = "" },
		"missing min":       func(r *inputRecommendation) { r.RequestMin = nil },
		"missing max":       func(r *inputRecommendation) { r.RequestMax = nil },
		"missing replicas":  func(r *inputRecommendation) { r.MeasuredReplicas = 1 },
		"zero replicas":     func(r *inputRecommendation) { r.Replicas = 0; r.MeasuredReplicas = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			large := inputRow("cpu", .4, .48, .2, "480m")
			mutate(&large)
			small := inputRow("cpu", .2, .24, .2, "240m")
			small.Cluster = "mgmt"
			memory := inputRow("memory", 200*(1<<20), 240*(1<<20), 200*(1<<20), "240Mi")
			for _, rows := range [][]inputRecommendation{{large, small, memory}, {small, memory, large}} {
				config := inputFile(t, "config.yaml", inputConfig)
				input := inputFile(t, "input.json", inputJSON(t, rows...))
				output := runInputOutput(t, input, Options{ConfigPath: config, AllowDecrease: true})
				if !strings.Contains(output, "SKIP blocked") {
					t.Fatalf("missing blocked warning: %s", output)
				}
				want := strings.Replace(inputConfig, "memory: 200Mi", "memory: 240Mi", 1)
				if fileContents(t, config) != want {
					t.Fatal("blocked cpu mapping edited or independent memory mapping blocked")
				}
			}
		})
	}
}

func TestRunInputUnknownMappingAndWarnings(t *testing.T) {
	r := inputRow("cpu", .2, .24, .1, "240m")
	r.Container = "unknown"
	r.Warnings = []string{"unmapped row warning"}
	rows := make([]inputRecommendation, 10000)
	for i := range rows {
		rows[i] = r
	}
	mapped := inputRow("cpu", .2, .24, .2, "240m")
	mapped.Eligible = false
	mapped.Warnings = []string{"mapped row warning"}
	rows = append(rows, mapped)
	data := strings.Replace(inputJSON(t, rows...), `"warnings":[]`, `"warnings":["report warning"]`, 1)
	config := inputFile(t, "config.yaml", inputConfig)
	output := runInputOutput(t, inputFile(t, "input.json", data), Options{ConfigPath: config})
	for _, want := range []string{"SKIP unknown mappings: 10000 recommendation rows", "mapped row warning", "report warning", "SKIP blocked"} {
		if !strings.Contains(output, want) {
			t.Fatalf("missing %q: %s", want, output)
		}
	}
	if strings.Contains(output, "unmapped row warning") || strings.Count(output, "SKIP unknown mapping") != 1 || len(output) > 2000 {
		t.Fatalf("unbounded unmapped output: %d bytes", len(output))
	}
	if fileContents(t, config) != inputConfig {
		t.Fatal("unknown mapping modified config")
	}
}

func TestRunInputRejectsUnsafeOptionsAndInvalidReportBeforeWrites(t *testing.T) {
	for _, opts := range []Options{
		{Commit: true}, {RenderCmd: "false"}, {GrafanaURL: "https://example.com"},
		{SourcePrefix: "clouds.public.defaults"}, {WritePrefix: "defaults"},
		{ChangeThreshold: -.1}, {ChangeThreshold: 1.1}, {ChangeThreshold: math.NaN()}, {ChangeThreshold: math.Inf(1)}, {ChangeThreshold: math.Inf(-1)},
	} {
		if err := RunInput(context.Background(), logr.Discard(), "not-read", opts); err == nil {
			t.Errorf("accepted unsafe options: %+v", opts)
		}
	}
	good := inputRow("cpu", .2, .24, .2, "240m")
	bad := inputRow("memory", 200, 300, 100, "300Mi")
	config := inputFile(t, "config.yaml", inputConfig)
	input := inputFile(t, "input.json", inputJSON(t, good, bad))
	if err := RunInput(context.Background(), logr.Discard(), input, Options{ConfigPath: config}); err == nil {
		t.Fatal("accepted invalid report")
	}
	if fileContents(t, config) != inputConfig {
		t.Fatal("partially edited config before validation")
	}
}

func TestRunInputDeadband(t *testing.T) {
	for _, tc := range []struct {
		name, quantity, want          string
		peak, suggested, threshold    float64
		allow, rowActionable, rowRisk bool
	}{
		{name: "increase boundary", peak: .183, suggested: .22, quantity: "220m", threshold: .1, want: "SKIP deadband", rowActionable: true},
		{name: "increase below boundary", peak: .175, suggested: .21, quantity: "210m", threshold: .1, want: "SKIP deadband"},
		{name: "increase above boundary", peak: .192, suggested: .23, quantity: "230m", threshold: .1, want: "CHANGE"},
		{name: "decrease boundary", peak: .15, suggested: .18, quantity: "180m", threshold: .1, allow: true, want: "SKIP deadband"},
		{name: "decrease above boundary", peak: .142, suggested: .17, quantity: "170m", threshold: .1, allow: true, want: "CHANGE"},
		{name: "decrease permission", peak: .142, suggested: .17, quantity: "170m", threshold: .1, want: "requires --allow-decrease"},
		{name: "zero disables", peak: .175, suggested: .21, quantity: "210m", threshold: 0, want: "CHANGE"},
		{name: "fraction epsilon", peak: .183, suggested: .22, quantity: "220m", threshold: .1 - 1e-14, want: "SKIP deadband"},
		{name: "beyond epsilon", peak: .183, suggested: .22, quantity: "220m", threshold: .1 - 1e-8, want: "CHANGE"},
		{name: "risk bypass", peak: .241, suggested: .29, quantity: "290m", threshold: 1, want: "CHANGE"},
		{name: "risk equality", peak: .24, suggested: .29, quantity: "290m", threshold: 1, want: "SKIP deadband", rowRisk: true},
		{name: "ignore producer risk", peak: .183, suggested: .22, quantity: "220m", threshold: .1, want: "SKIP deadband", rowRisk: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := inputRow("cpu", tc.peak, tc.suggested, .2, tc.quantity)
			row.Actionable, row.AlertRisk = tc.rowActionable, tc.rowRisk
			// Observed requestMin is not the effective current request (200m).
			row.RequestMin = number(.1)
			config := inputFile(t, "config.yaml", inputConfig)
			input := inputFile(t, "input.json", inputJSON(t, row))
			output := runInputOutput(t, input, Options{ConfigPath: config, ChangeThreshold: tc.threshold, AllowDecrease: tc.allow})
			if !strings.Contains(output, tc.want) {
				t.Fatalf("expected %s: %s", tc.want, output)
			}
			want := inputConfig
			if tc.want == "CHANGE" {
				want = strings.Replace(want, "200m #", tc.quantity+" #", 1)
			}
			if fileContents(t, config) != want {
				t.Fatal("unexpected deadband edits")
			}
		})
	}
}

func TestRunInputDeadbandAcrossClusters(t *testing.T) {
	for _, tc := range []struct {
		name      string
		rows      []inputRecommendation
		threshold float64
		want      string
	}{
		{
			name: "max crosses deadband", threshold: .1, want: "230m",
			rows: []inputRecommendation{inputRow("cpu", .192, .23, .2, "230m"), inputRow("cpu", .175, .21, .2, "210m")},
		},
		{
			name: "max stays in deadband despite actionable decrease", threshold: .1, want: "200m",
			rows: []inputRecommendation{inputRow("cpu", .183, .22, .2, "220m"), inputRow("cpu", .1, .12, .2, "120m")},
		},
		{
			// Both round to 290m. Risk must consider the second peak, not
			// just whichever row first supplied the maximum suggestion.
			name: "risk from tied suggestion", threshold: 1, want: "290m",
			rows: []inputRecommendation{inputRow("cpu", .24, .29, .2, "290m"), inputRow("cpu", .241, .29, .2, "290m")},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.rows[1].Cluster = "mgmt"
			tc.rows[1].Actionable = true
			for _, rows := range [][]inputRecommendation{tc.rows, {tc.rows[1], tc.rows[0]}} {
				config := inputFile(t, "config.yaml", inputConfig)
				input := inputFile(t, "input.json", inputJSON(t, rows...))
				output := runInputOutput(t, input, Options{ConfigPath: config, ChangeThreshold: tc.threshold, AllowDecrease: true})
				want := strings.Replace(inputConfig, "200m #", tc.want+" #", 1)
				if fileContents(t, config) != want {
					t.Fatalf("incorrect cross-cluster decision: %s", output)
				}
			}
		})
	}
}

func TestRunInputRiskGuardAndSafety(t *testing.T) {
	for _, tc := range []struct {
		name, current, want string
		peak, suggested     float64
		eligible            bool
	}{
		{name: "guard increase bypasses 100 percent deadband", current: "10m", peak: .0124, suggested: .02, eligible: true, want: "CHANGE"},
		{name: "guard boundary noop", current: "10m", peak: .012, suggested: .01, eligible: true, want: "NOOP"},
		{name: "risk does not bypass stale", current: "9m", peak: .0124, suggested: .02, eligible: true, want: "WARNING stale"},
		{name: "risk does not bypass ineligible", current: "10m", peak: .0124, suggested: .02, eligible: false, want: "SKIP blocked"},
		{name: "zero current increase", current: "0m", peak: 0, suggested: .01, eligible: true, want: "CHANGE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			quantity := "20m"
			if tc.suggested == .01 {
				quantity = "10m"
			}
			row := inputRow("cpu", tc.peak, tc.suggested, .01, quantity)
			row.Eligible = tc.eligible
			if tc.current == "0m" {
				row.RequestMin, row.RequestMax = number(0), number(0)
			}
			before := strings.Replace(inputConfig, "200m #", tc.current+" #", 1)
			config := inputFile(t, "config.yaml", before)
			input := inputFile(t, "input.json", inputJSON(t, row))
			output := runInputOutput(t, input, Options{ConfigPath: config, ChangeThreshold: 1})
			if !strings.Contains(output, tc.want) {
				t.Fatalf("expected %s: %s", tc.want, output)
			}
			want := before
			if tc.want == "CHANGE" {
				want = strings.Replace(want, tc.current+" #", quantity+" #", 1)
			}
			if fileContents(t, config) != want {
				t.Fatal("unexpected risk guard edits")
			}
		})
	}
}
