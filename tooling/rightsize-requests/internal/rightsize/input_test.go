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
		Version: 3, Start: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC),
		Headroom: 1, ChangeThreshold: 0.1, CPUWindow: "10m", Warnings: []string{}, Recommendations: rows,
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
	valid := inputJSON(t, inputRow("cpu", .239, .24, .1, "240m"))
	if strings.Contains(valid, `"samples"`) {
		t.Fatal("version 3 fixture must omit optional samples")
	}
	tests := map[string]string{
		"valid":                  valid,
		"2m":                     strings.Replace(valid, `"10m"`, `"2m"`, 1),
		"signed delta":           strings.Replace(valid, `"delta":0.`, `"delta":-0.`, 1),
		"unknown field":          strings.Replace(valid, `"version":3`, `"version":3,"extra":true`, 1),
		"unknown row field":      strings.Replace(valid, `"eligible":true`, `"eligible":true,"extra":0`, 1),
		"duplicate":              strings.Replace(valid, `"version":3`, `"version":3,"version":3`, 1),
		"case insensitive field": strings.Replace(valid, `"eligible"`, `"Eligible"`, 1),
		"missing bool":           strings.Replace(valid, `"initContainer":false,`, ``, 1),
		"null bool":              strings.Replace(valid, `"initContainer":false`, `"initContainer":null`, 1),
		"null list":              strings.Replace(valid, `"warnings":[]`, `"warnings":null`, 1),
		"trailing object":        valid + `{}`,
		"trailing junk":          valid + `oops`,
		"top level null":         `null`,
		"old version":            strings.Replace(valid, `"version":3`, `"version":1`, 1),
		"old headroom version":   strings.Replace(valid, `"version":3`, `"version":2`, 1),
		"future version":         strings.Replace(valid, `"version":3`, `"version":4`, 1),
		"old headroom":           strings.Replace(valid, `"headroom":1`, `"headroom":1.2`, 1),
		"headroom":               strings.Replace(valid, `"headroom":1`, `"headroom":1.25`, 1),
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
		"negative":               strings.Replace(valid, `"peak":0.239`, `"peak":-0.2`, 1),
		"infinite":               strings.Replace(valid, `"peak":0.239`, `"peak":1e999`, 1),
		"nan quantity":           strings.Replace(valid, `"240m"`, `"NaN"`, 1),
		"negative replicas":      strings.Replace(valid, `"replicas":2`, `"replicas":-2`, 1),
		"negative samples":       strings.Replace(valid, `"replicas":2`, `"replicas":2,"samples":-1`, 1),
		"fractional samples":     strings.Replace(valid, `"replicas":2`, `"replicas":2,"samples":1.5`, 1),
		"string samples":         strings.Replace(valid, `"replicas":2`, `"replicas":2,"samples":"12"`, 1),
		"overflow samples":       strings.Replace(valid, `"replicas":2`, `"replicas":2,"samples":1e999`, 1),
		"duplicate samples":      strings.Replace(valid, `"replicas":2`, `"replicas":2,"samples":0,"samples":0`, 1),
		"case samples":           strings.Replace(valid, `"replicas":2`, `"replicas":2,"Samples":0`, 1),
		"fractional replicas":    strings.Replace(valid, `"replicas":2`, `"replicas":2.5`, 1),
		"too many measured":      strings.Replace(valid, `"measuredReplicas":2`, `"measuredReplicas":3`, 1),
		"range inverted":         strings.Replace(valid, `"requestMin":0.1`, `"requestMin":0.3`, 1),
		"quantity mismatch":      strings.Replace(valid, `"240m"`, `"250m"`, 1),
		"peak mismatch":          strings.Replace(valid, `"peak":0.239`, `"peak":0.3`, 1),
		"relabeled 120 percent":  strings.Replace(valid, `"peak":0.239`, `"peak":0.2`, 1),
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
			if (name == "old version" || name == "old headroom version") && !strings.Contains(err.Error(), "rerender replica peaks via render-right-sizing") {
				t.Fatalf("missing report migration guidance: %v", err)
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

func TestInputSamplesInformational(t *testing.T) {
	for _, value := range []string{"missing", "null", "0", "122"} {
		t.Run(value, func(t *testing.T) {
			data := inputJSON(t, inputRow("cpu", .239, .24, .2, "240m"))
			if value != "missing" {
				data = strings.Replace(data, `"replicas":2`, `"replicas":2,"samples":`+value, 1)
			}
			input := inputFile(t, "input.json", data)
			report, err := readInput(input)
			if err != nil {
				t.Fatal(err)
			}
			count := report.Recommendations[0].Samples
			if value == "missing" || value == "null" {
				if count != nil {
					t.Fatalf("missing/null samples must remain unknown: %d", *count)
				}
			} else if count == nil || fmt.Sprint(*count) != value {
				t.Fatalf("samples not retained: %v; want %s", count, value)
			}
			config := inputFile(t, "config.yaml", inputConfig)
			runInputOutput(t, input, Options{ConfigPath: config})
			if fileContents(t, config) != strings.Replace(inputConfig, "200m #", "240m #", 1) {
				t.Fatal("optional samples changed the sizing decision")
			}
		})
	}
}

const savingsJSON = `{"time":"2026-09-01T12:00:00Z","resources":[{"cluster":"svc","resource":"cpu","before":0,"after":0,"reductions":0,"increases":0,"changedContainers":0,"excludedContainers":2,"unknownContainers":3}]}`

func TestInputSavingsValidation(t *testing.T) {
	valid := inputJSON(t, inputRow("cpu", .239, .24, .2, "240m"))
	if strings.Contains(valid, `"savings"`) {
		t.Fatal("version 3 fixture must omit optional savings")
	}
	for name, summary := range map[string]string{
		"missing": "", "null": "null", "cpu": savingsJSON,
		"positive totals": strings.NewReplacer(`"before":0`, `"before":1.5`, `"after":0`, `"after":1.25`, `"reductions":0`, `"reductions":0.5`, `"increases":0`, `"increases":0.25`, `"changedContainers":0`, `"changedContainers":2`).Replace(savingsJSON),
		"memory":          strings.Replace(savingsJSON, `"cpu"`, `"memory"`, 1),
		"start":           strings.Replace(savingsJSON, "12:00:00", "00:00:00", 1),
		"end":             strings.Replace(savingsJSON, "2026-09-01T12:00:00Z", "2026-09-02T00:00:00Z", 1),
		"timezone":        strings.Replace(savingsJSON, "12:00:00Z", "13:00:00+01:00", 1),
		"empty resources": `{"time":"2026-09-01T12:00:00Z","resources":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			data := valid
			if summary != "" {
				data = strings.Replace(data, `"version":3`, `"version":3,"savings":`+summary, 1)
			}
			report, err := readInput(inputFile(t, "input.json", data))
			if err != nil {
				t.Fatal(err)
			}
			if (report.Savings == nil) != (name == "missing" || name == "null") {
				t.Fatalf("optional savings not retained: %+v", report.Savings)
			}
		})
	}
	invalid := map[string]string{
		"array": `[]`, "scalar": `1`, "empty object": `{}`,
		"duplicate savings":       savingsJSON + `,"savings":` + savingsJSON,
		"bad time":                strings.Replace(savingsJSON, "2026-09-01T12:00:00Z", "yesterday", 1),
		"before window":           strings.Replace(savingsJSON, "2026-09-01", "2026-08-31", 1),
		"after window":            strings.Replace(savingsJSON, "2026-09-01", "2026-09-03", 1),
		"zero time":               strings.Replace(savingsJSON, "2026-09-01T12:00:00Z", "0001-01-01T00:00:00Z", 1),
		"null time":               strings.Replace(savingsJSON, `"2026-09-01T12:00:00Z"`, `null`, 1),
		"missing time":            strings.Replace(savingsJSON, `"time":"2026-09-01T12:00:00Z",`, ``, 1),
		"missing resources":       `{"time":"2026-09-01T12:00:00Z"}`,
		"null resources":          `{"time":"2026-09-01T12:00:00Z","resources":null}`,
		"null resource row":       `{"time":"2026-09-01T12:00:00Z","resources":[null]}`,
		"unknown summary field":   strings.Replace(savingsJSON, `"time":`, `"extra":0,"time":`, 1),
		"duplicate summary field": strings.Replace(savingsJSON, `"time":`, `"resources":[],"time":`, 1),
		"unknown row field":       strings.Replace(savingsJSON, `"cluster":`, `"extra":0,"cluster":`, 1),
		"duplicate row field":     strings.Replace(savingsJSON, `"cluster":`, `"cluster":"svc","cluster":`, 1),
		"empty cluster":           strings.Replace(savingsJSON, `"svc"`, `" \t"`, 1),
		"null cluster":            strings.Replace(savingsJSON, `"svc"`, `null`, 1),
		"missing cluster":         strings.Replace(savingsJSON, `"cluster":"svc",`, ``, 1),
		"invalid resource":        strings.Replace(savingsJSON, `"cpu"`, `"disk"`, 1),
		"missing resource":        strings.Replace(savingsJSON, `"resource":"cpu",`, ``, 1),
		"case sensitive":          strings.Replace(savingsJSON, `"before"`, `"Before"`, 1),
	}
	for _, field := range []string{"before", "after", "reductions", "increases", "changedContainers", "excludedContainers", "unknownContainers"} {
		original := `"` + field + `":0`
		switch field {
		case "excludedContainers":
			original = `"excludedContainers":2`
		case "unknownContainers":
			original = `"unknownContainers":3`
		}
		for _, value := range []string{"-1", "1e999", "NaN", "null", `"1"`} {
			invalid[field+"/"+value] = strings.Replace(savingsJSON, original, `"`+field+`":`+value, 1)
		}
		invalid[field+"/missing"] = strings.Replace(savingsJSON, ","+original, "", 1)
		if strings.HasSuffix(field, "Containers") {
			invalid[field+"/fractional"] = strings.Replace(savingsJSON, original, `"`+field+`":1.5`, 1)
		}
	}
	for name, summary := range invalid {
		t.Run(name, func(t *testing.T) {
			data := strings.Replace(valid, `"version":3`, `"version":3,"savings":`+summary, 1)
			config := inputFile(t, "config.yaml", inputConfig)
			if err := RunInput(context.Background(), logr.Discard(), inputFile(t, "input.json", data), Options{ConfigPath: config}); err == nil {
				t.Fatal("accepted invalid savings")
			}
			if fileContents(t, config) != inputConfig {
				t.Fatal("invalid savings partially edited config")
			}
		})
	}
}

func TestInputRounding(t *testing.T) {
	const mi = 1 << 20
	for _, row := range []inputRecommendation{
		inputRow("cpu", 0, .01, .1, "10m"),
		inputRow("cpu", .012, .02, .1, "20m"),
		inputRow("cpu", .0124, .02, .1, "20m"),
		inputRow("cpu", .201, .21, .1, "210m"),
		inputRow("cpu", .207, .21, .1, "210m"),
		inputRow("cpu", .125, .13, .1, "130m"),
		inputRow("cpu", .1875, .19, .1, "190m"),
		inputRow("cpu", .1875-1e-10, .19, .1, "190m"),
		inputRow("cpu", .1875+1e-10, .19, .1, "190m"),
		inputRow("cpu", .1, .1, .1, "100m"),
		inputRow("cpu", .1-1e-10, .1, .1, "100m"),
		inputRow("cpu", .1+1e-10, .11, .1, "110m"),
		inputRow("cpu", math.Nextafter(.1, math.Inf(1)), .11, .1, "110m"),
		inputRow("memory", 0, 10*mi, 20*mi, "10Mi"),
		inputRow("memory", 12*mi, 20*mi, 20*mi, "20Mi"),
		inputRow("memory", 12.4*mi, 20*mi, 20*mi, "20Mi"),
		inputRow("memory", 100.1*mi, 110*mi, 20*mi, "110Mi"),
		inputRow("memory", 105*mi, 110*mi, 20*mi, "110Mi"),
		inputRow("memory", 100*mi, 100*mi, 20*mi, "100Mi"),
		inputRow("memory", (100-1e-7)*mi, 100*mi, 20*mi, "100Mi"),
		inputRow("memory", (100+1e-7)*mi, 110*mi, 20*mi, "110Mi"),
		inputRow("memory", math.Nextafter(100*mi, math.Inf(1)), 110*mi, 20*mi, "110Mi"),
		inputRow("memory", 187.5*mi, 190*mi, 20*mi, "190Mi"),
		inputRow("memory", (187.5-1e-7)*mi, 190*mi, 20*mi, "190Mi"),
		inputRow("memory", (187.5+1e-7)*mi, 190*mi, 20*mi, "190Mi"),
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
		inputRow("cpu", .201, .20, .1, "200m"), // nearest rounding is incompatible
		inputRow("cpu", .2, .24, .1, "240m"),   // version 2 headroom is incompatible
		inputRow("cpu", .1875, .22, .1, "220m"),
		inputRow("cpu", .1875-1e-10, .22, .1, "220m"),
		inputRow("cpu", .1875+1e-10, .22, .1, "220m"),
		inputRow("memory", 187.5*mi, 220*mi, 20*mi, "220Mi"),
		inputRow("memory", (187.5-1e-7)*mi, 220*mi, 20*mi, "220Mi"),
		inputRow("cpu", .0124, .01, .1, "10m"),
		inputRow("cpu", .012, .01, .1, "10m"),
		inputRow("cpu", .1, .11, .1, "110m"), // exact chunks must not round up again
		inputRow("cpu", .1+1e-10, .1, .1, "100m"),
		inputRow("cpu", math.Nextafter(.1, math.Inf(1)), .1, .1, "100m"),
		inputRow("memory", 12.4*mi, 10*mi, 20*mi, "10Mi"),
		inputRow("memory", 12*mi, 10*mi, 20*mi, "10Mi"),
		inputRow("memory", 100*mi, 110*mi, 20*mi, "110Mi"),
		inputRow("memory", (100+1e-7)*mi, 100*mi, 20*mi, "100Mi"),
		inputRow("memory", math.Nextafter(100*mi, math.Inf(1)), 100*mi, 20*mi, "100Mi"),
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
				row := inputRow(resource, 239*scale, 240*scale, 100*scale, digits+suffix)
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
					row := inputRow(resource, 239*scale, 240*scale, 100*scale, "240"+suffix)
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
			row := inputRow("cpu", .239, .24, .2, "240m")
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
					row := inputRow("cpu", .239, .24, .1, "240m")
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
	large := inputRow("cpu", .489, .49, .2, "490m")
	small := inputRow("cpu", .239, .24, .2, "240m")
	small.Cluster = "mgmt"
	memory := inputRow("memory", 249*mi, 250*mi, 200*mi, "250Mi")
	for _, rows := range [][]inputRecommendation{{large, small, memory}, {memory, small, large}} {
		config := inputFile(t, "config.yaml", inputConfig)
		input := inputFile(t, "input.json", inputJSON(t, rows...))
		opts := Options{ConfigPath: config}
		runInputOutput(t, input, opts)
		want := strings.Replace(inputConfig, "200m #", "490m #", 1)
		want = strings.Replace(want, "memory: 200Mi", "memory: 250Mi", 1)
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

func TestRunInputFreshReportConvergence(t *testing.T) {
	testFreshReportConvergence(t, false)
}

func testFreshReportConvergence(t *testing.T, sizing bool) {
	t.Helper()
	for _, tc := range []struct {
		name, reason string
		peak, target float64
		allow        bool
	}{
		{name: "ceiling increase", peak: 249, target: 250, reason: "CHANGE"},
		{name: "ceiling decrease", peak: 169, target: 170, allow: true, reason: "CHANGE"},
		{name: "decrease permission", peak: 169, target: 170, reason: "requires --allow-decrease"},
		{name: "inclusive increase deadband", peak: 219, target: 220, reason: "SKIP deadband"},
		{name: "inclusive decrease deadband", peak: 179, target: 180, allow: true, reason: "SKIP deadband"},
		{name: "inside deadband", peak: 209, target: 210, reason: "SKIP deadband"},
	} {
		for _, withSavings := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/savings=%t", tc.name, withSavings), func(t *testing.T) {
				before := inputConfig
				if sizing {
					before = sizingTemplate()
				}
				config := inputFile(t, "config.yaml", before)
				opts := Options{ConfigPath: config, ChangeThreshold: .1, AllowDecrease: tc.allow}
				run := func(path string) string {
					if sizing {
						return runSizingOutput(t, path, config, opts)
					}
					return runInputOutput(t, path, opts)
				}
				// Regenerate evidence from unchanged usage and the requests actually
				// deployed after each run, rather than replaying the original report.
				report := func(current float64, next bool) string {
					var rows []inputRecommendation
					for _, resource := range []string{"cpu", "memory"} {
						scale, suffix := .001, "m"
						if resource == "memory" {
							scale, suffix = 1<<20, "Mi"
						}
						row := inputRow(resource, tc.peak*scale, tc.target*scale, current*scale, fmt.Sprintf("%.0f%s", tc.target, suffix))
						if sizing {
							row = sizingRow(resource, tc.peak*scale, tc.target*scale, current*scale, row.SuggestedQuantity)
						}
						rows = append(rows, row)
					}
					data := inputJSON(t, rows...)
					if withSavings {
						// Zero totals and excluded/unknown counts must not block or
						// drive edits backed by eligible recommendation evidence.
						data = strings.Replace(data, `"version":3`, `"version":3,"savings":`+savingsJSON, 1)
					}
					if next {
						data = strings.NewReplacer("2026-09-01", "2026-09-02", "2026-09-02", "2026-09-03").Replace(data)
					}
					return inputFile(t, "input.json", data)
				}
				output := run(report(200, false))
				if strings.Count(output, tc.reason) != 2 || strings.Contains(output, "stale") {
					t.Fatalf("expected CPU and memory %q: %s", tc.reason, output)
				}
				want, current, reason := before, 200.0, tc.reason
				if tc.reason == "CHANGE" {
					current, reason = tc.target, "NOOP"
					if sizing {
						want = strings.Replace(want, "cpu: 200m # kube-apiserver", fmt.Sprintf("cpu: %.0fm # kube-apiserver", tc.target), 1)
						want = strings.Replace(want, "memory: 200Mi # kube-apiserver", fmt.Sprintf("memory: %.0fMi # kube-apiserver", tc.target), 1)
					} else {
						want = strings.Replace(want, "200m #", fmt.Sprintf("%.0fm #", tc.target), 1)
						want = strings.Replace(want, "memory: 200Mi", fmt.Sprintf("memory: %.0fMi", tc.target), 1)
					}
				}
				if fileContents(t, config) != want {
					t.Fatal("first report made incorrect or out-of-scope edits")
				}
				output = run(report(current, true))
				if strings.Count(output, reason) != 2 || strings.Contains(output, "CHANGE") || strings.Contains(output, "stale") {
					t.Fatalf("fresh report did not converge for CPU and memory: %s", output)
				}
				if fileContents(t, config) != want {
					t.Fatal("fresh report modified converged requests")
				}
			})
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
		input := inputFile(t, "input.json", inputJSON(t, inputRow("cpu", .239, .24, .1, "240m")))
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
	input := inputFile(t, "input.json", inputJSON(t, inputRow("cpu", .239, .24, .2, "240m")))
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
			a := inputRow("cpu", .239, .24, .1, "240m")
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
			large := inputRow("cpu", .479, .48, .2, "480m")
			mutate(&large)
			small := inputRow("cpu", .239, .24, .2, "240m")
			small.Cluster = "mgmt"
			memory := inputRow("memory", 239*(1<<20), 240*(1<<20), 200*(1<<20), "240Mi")
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

func identityConfig(path, current string) string {
	var b strings.Builder
	b.WriteString("defaults:\n")
	indent := "  "
	for _, part := range strings.Split(path, ".") {
		fmt.Fprintf(&b, "%s%s:\n", indent, part)
		indent += "  "
	}
	fmt.Fprintf(&b, "%srequests:\n%s  cpu: %s\n", indent, indent, current)
	return b.String()
}

func TestRunInputsIdentityCandidates(t *testing.T) {
	base := inputRow("cpu", .239, .24, .1, "240m")
	base.Namespace, base.Kind, base.Workload, base.Container, base.Cluster = "prometheus", "StatefulSet", "prom-agent-prometheus", "prometheus", "int-uksouth-svc-1"
	path := "svc.prometheus.prometheusSpec.resources"
	for _, tc := range []struct {
		name    string
		mutate  func(*inputRecommendation)
		blocked bool
	}{
		{"unknown kind", func(r *inputRecommendation) { r.Kind = "Unknown" }, true},
		{"wrong kind", func(r *inputRecommendation) { r.Kind = "Deployment" }, true},
		{"missing workload", func(r *inputRecommendation) { r.Workload = "" }, true},
		{"unknown workload", func(r *inputRecommendation) { r.Workload = "UNKNOWN" }, true},
		{"bare pod", func(r *inputRecommendation) { r.Kind, r.Workload = "Pod", "prom-agent-prometheus-0" }, true},
		{"unknown role", func(r *inputRecommendation) { r.Cluster = "contains-svc-but-not-a-role" }, true},
		{"same role other cluster", func(r *inputRecommendation) { r.Cluster, r.Eligible = "pers-usw3test-svc", false }, true},
		{"wrong role", func(r *inputRecommendation) { r.Cluster, r.Eligible = "int-uksouth-mgmt-1", false }, false},
		{"other workload", func(r *inputRecommendation) { r.Workload, r.Eligible = "other-prometheus", false }, false},
		{"unconfigured init", func(r *inputRecommendation) { r.InitContainer = true }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := base
			tc.mutate(&bad)
			for _, reverse := range []bool{false, true} {
				before := identityConfig(path, "100m")
				config := inputFile(t, "config.yaml", before)
				first, second := base, bad
				if reverse {
					first, second = second, first
				}
				inputs := []string{inputFile(t, "first.json", inputJSON(t, first)), inputFile(t, "second.json", inputJSON(t, second))}
				output, err := runDatasetOutput(t, false, inputs, config, Options{})
				if err != nil {
					t.Fatal(err)
				}
				ed, err := editor.New(config)
				if err != nil {
					t.Fatal(err)
				}
				got, _, getErr := ed.Get("clouds.dev.defaults." + path + ".requests.cpu")
				if tc.blocked {
					if fileContents(t, config) != before || !strings.Contains(output, "SKIP blocked clouds.dev.defaults."+path) {
						t.Fatalf("candidate failed to block: %s", output)
					}
				} else if getErr != nil || got != "240m" {
					t.Fatalf("unrelated identity blocked target: %s; %v", output, getErr)
				}
			}
		})
	}
}

func TestRunInputExplicitInitAndBaselineGuards(t *testing.T) {
	for _, target := range []struct{ namespace, kind, workload, container, path string }{
		{"maestro", "Deployment", "maestro-agent", "init", "maestro.agent.init.resources"},
		{"velero", "Deployment", "velero", "oadp-oadp-velero-plugin-for-microsoft-azure-rhel9", "velero.azurePlugin.resources"},
		{"velero", "Deployment", "velero", "oadp-oadp-hypershift-velero-plugin-rhel9", "velero.hypershiftPlugin.resources"},
		{"velero", "Job", "velero-install", "generate-manifest", "velero.installer.generate.resources"},
		{"prometheus", "StatefulSet", "prom-agent-prometheus", "init-config-reloader", "mgmt.prometheus.prometheusConfigReloader.resources"},
	} {
		path := target.path
		for _, tc := range []struct {
			name, current string
			mutate        func(*inputRecommendation)
			change        bool
		}{
			{"eligible configured init", "100m", func(*inputRecommendation) {}, true},
			{"short init", "100m", func(r *inputRecommendation) { r.Eligible = false; r.Samples = new(int) }, false},
			{"unmeasured init", "100m", func(r *inputRecommendation) {
				r.Peak, r.Suggested, r.SuggestedQuantity = nil, nil, ""
				r.MeasuredReplicas = 0
			}, false},
			{"zero replicas", "100m", func(r *inputRecommendation) { r.Replicas, r.MeasuredReplicas = 0, 0 }, false},
			{"NONE baseline", "NONE", func(*inputRecommendation) {}, false},
			{"unlimited baseline", "unlimited", func(*inputRecommendation) {}, false},
			{"unknown observed baseline", "100m", func(r *inputRecommendation) { r.RequestMin, r.RequestMax = nil, nil }, false},
			{"missing baseline", "", func(*inputRecommendation) {}, false},
		} {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				row := inputRow("cpu", .239, .24, .1, "240m")
				row.Namespace, row.Kind, row.Workload, row.Container, row.Cluster, row.InitContainer = target.namespace, target.kind, target.workload, target.container, "int-uksouth-mgmt-1", true
				tc.mutate(&row)
				before := identityConfig(path, tc.current)
				config := inputFile(t, "config.yaml", before)
				output := runInputOutput(t, inputFile(t, "input.json", inputJSON(t, row)), Options{ConfigPath: config})
				if tc.change {
					ed, err := editor.New(config)
					if err != nil {
						t.Fatal(err)
					}
					got, _, err := ed.Get("clouds.dev.defaults." + path + ".requests.cpu")
					if err != nil || got != "240m" {
						t.Fatalf("configured eligible init not edited: %s; %v", output, err)
					}
				} else if fileContents(t, config) != before || !strings.Contains(output, "SKIP") {
					t.Fatalf("unsafe init/baseline edit: %s", output)
				}
			})
		}
	}
}

func TestRunInputSharedPathsAndWorkloadIsolation(t *testing.T) {
	for _, tc := range []struct {
		name, path, namespace, kind, workload, container, otherNS, otherWorkload, otherContainer, cluster string
		blocked                                                                                           bool
	}{
		{"ACM finalize shared", "acm.resources.finalize", "multicluster-engine", "Job", "finalize-mce", "finalize", "multicluster-engine", "finalize-mce-config", "finalize", "int-uksouth-mgmt-1", true},
		{"ACM finalize isolated from storage hook", "acm.resources.finalize", "multicluster-engine", "Job", "finalize-mce", "finalize", "default-sc", "unannotate-default-sc", "finalize", "int-uksouth-mgmt-1", false},
		{"ACM addon shared", "acm.resources.workManager", "klusterlet-cluster1", "Deployment", "klusterlet-addon-workmgr", "acm-agent", "open-cluster-management-agent-addon", "klusterlet-addon-workmgr", "acm-agent", "int-uksouth-mgmt-1", true},
		{"guest KSM shared", "mgmtAgent.guestKSMResources", "ocm-arohcpint-cluster1", "Deployment", "kube-state-metrics-hcp", "kube-state-metrics", "ocm-arohcpint-cluster2", "kube-state-metrics-hcp", "kube-state-metrics", "int-uksouth-mgmt-1", true},
		{"Prometheus KSM releases shared", "mgmt.prometheus.kubeStateMetrics.resources", "prometheus", "Deployment", "arohcp-monitor-kube-state-metrics", "kube-state-metrics", "prometheus", "prometheus-kube-state-metrics", "kube-state-metrics", "int-uksouth-mgmt-1", true},
		{"Prometheus KSM isolated from guest", "mgmt.prometheus.kubeStateMetrics.resources", "prometheus", "Deployment", "arohcp-monitor-kube-state-metrics", "kube-state-metrics", "ocm-arohcpint-cluster1", "kube-state-metrics-hcp", "kube-state-metrics", "int-uksouth-mgmt-1", false},
		{"ops gateway isolated from AKS gateway", "svc.opsIngress.gateway.resources", "aks-istio-ingress", "Deployment", "ops-ingress-gateway-istio", "istio-proxy", "aks-istio-ingress", "aks-istio-ingressgateway-external", "istio-proxy", "int-uksouth-svc-1", false},
		{"certificate workload isolated", "frontend.certificateRefresher.resources", "aks-istio-ingress", "Deployment", "frontend-certificate-refresher", "init-container-msg-container-init", "aks-istio-ingress", "admin-api-certificate-refresher", "init-container-msg-container-init", "int-uksouth-svc-1", false},
		{"secret provider isolated", "secretSyncController.k8s.resources", "secret-sync-controller", "Deployment", "secrets-store-sync-controller-manager", "provider-azure-installer", "secret-sync-controller", "secrets-store-sync-controller-manager", "manager", "int-uksouth-mgmt-1", false},
		{"HSO requests without limits", "hypershift.operatorResources", "hypershift", "Deployment", "operator", "operator", "hypershift", "unrelated-operator", "operator", "int-uksouth-mgmt-1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			good := inputRow("cpu", .239, .24, .1, "240m")
			good.Namespace, good.Kind, good.Workload, good.Container, good.Cluster = tc.namespace, tc.kind, tc.workload, tc.container, tc.cluster
			bad := good
			bad.Namespace, bad.Workload, bad.Container, bad.Eligible = tc.otherNS, tc.otherWorkload, tc.otherContainer, false
			before := identityConfig(tc.path, "100m")
			config := inputFile(t, "config.yaml", before)
			output := runInputOutput(t, inputFile(t, "input.json", inputJSON(t, good, bad)), Options{ConfigPath: config})
			if tc.blocked {
				if fileContents(t, config) != before || !strings.Contains(output, "SKIP blocked") {
					t.Fatalf("shared target not blocked: %s", output)
				}
			} else {
				ed, err := editor.New(config)
				if err != nil {
					t.Fatal(err)
				}
				got, _, err := ed.Get("clouds.dev.defaults." + tc.path + ".requests.cpu")
				if err != nil || got != "240m" || strings.Contains(fileContents(t, config), "limits:") {
					t.Fatalf("wrong target or limit edit: %s; %v", output, err)
				}
			}
		})
	}
}

func TestRunInputReloaderSharedInitPolicy(t *testing.T) {
	for _, role := range []string{"svc", "mgmt"} {
		for _, eligible := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/initEligible=%t", role, eligible), func(t *testing.T) {
				regular := inputRow("cpu", .239, .24, .1, "240m")
				regular.Namespace, regular.Kind, regular.Workload, regular.Container, regular.Cluster = "prometheus", "StatefulSet", "prom-agent-prometheus", "config-reloader", "int-uksouth-"+role+"-1"
				init := regular
				init.Container, init.InitContainer, init.Eligible = "init-config-reloader", true, eligible
				init.Workload, init.Peak, init.Suggested, init.SuggestedQuantity = "prom-agent-prometheus-shard-2", number(.479), number(.48), "480m"
				path := role + ".prometheus.prometheusConfigReloader.resources"
				before := identityConfig(path, "100m")
				config := inputFile(t, "config.yaml", before)
				output := runInputOutput(t, inputFile(t, "input.json", inputJSON(t, regular, init)), Options{ConfigPath: config})
				if !eligible {
					if fileContents(t, config) != before || !strings.Contains(output, "SKIP blocked") {
						t.Fatalf("ineligible init did not block shared reloader policy: %s", output)
					}
					return
				}
				ed, err := editor.New(config)
				if err != nil {
					t.Fatal(err)
				}
				if got, _, err := ed.Get("clouds.dev.defaults." + path + ".requests.cpu"); err != nil || got != "480m" {
					t.Fatalf("shared policy did not take maximum init recommendation: %s; %v", output, err)
				}
			})
		}
	}
}

func TestRunInputArobitMixedRoles(t *testing.T) {
	svc := inputRow("cpu", .239, .24, .1, "240m")
	svc.Namespace, svc.Kind, svc.Workload, svc.Container, svc.Cluster = "arobit", "DaemonSet", "arobit-forwarder", "fluentbit", "test-svc-1"
	mgmt := svc
	mgmt.Cluster, mgmt.Peak, mgmt.Suggested, mgmt.SuggestedQuantity = "test-mgmt-1", number(.479), number(.48), "480m"
	before := identityConfig("svc.arobit.forwarder.resources", "100m") + strings.TrimPrefix(identityConfig("mgmt.arobit.forwarder.resources", "100m"), "defaults:\n")
	for _, badCluster := range []string{"none", "test-svc-1", "test-mgmt-1", "test-opstool-1", "unknown"} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reverse=%t", badCluster, reverse), func(t *testing.T) {
				rows := []inputRecommendation{svc, mgmt}
				if badCluster != "none" {
					bad := svc
					bad.Cluster, bad.Kind = badCluster, "Unknown"
					rows = append(rows, bad)
				}
				if reverse {
					for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
						rows[i], rows[j] = rows[j], rows[i]
					}
				}
				config := inputFile(t, "config.yaml", before)
				output := runInputOutput(t, inputFile(t, "input.json", inputJSON(t, rows...)), Options{ConfigPath: config})
				ed, err := editor.New(config)
				if err != nil {
					t.Fatal(err)
				}
				for role, want := range map[string]string{"svc": "240m", "mgmt": "480m"} {
					path := role + ".arobit.forwarder.resources.requests.cpu"
					got, _, err := ed.Get("clouds.dev.defaults." + path)
					blocked := badCluster == "test-"+role+"-1" || badCluster == "unknown"
					if blocked {
						if err == nil || !strings.Contains(output, "SKIP blocked") {
							t.Fatalf("unresolved role failed to block %s: %s", path, output)
						}
					} else if err != nil || got != want {
						t.Fatalf("role isolation failed for %s: got %q, want %q; %v; %s", path, got, want, err, output)
					}
					if got, _, err := ed.Get("defaults." + path); err != nil || got != "100m" {
						t.Fatalf("base request changed at %s: %q, %v", path, got, err)
					}
				}
			})
		}
	}
}

func TestRunInputPrometheusMixedRoles(t *testing.T) {
	for _, component := range []struct{ kind, workload, container, path string }{
		{"StatefulSet", "prom-agent-prometheus", "prometheus", "prometheusSpec"},
		{"Deployment", "arohcp-monitor-kube-state-metrics", "kube-state-metrics", "kubeStateMetrics"},
		{"Deployment", "prometheus-operator", "kube-prometheus-stack", "prometheusOperator"},
		{"StatefulSet", "prom-agent-prometheus", "config-reloader", "prometheusConfigReloader"},
		{"StatefulSet", "prom-agent-prometheus", "init-config-reloader", "prometheusConfigReloader"},
	} {
		for _, role := range []string{"svc", "mgmt", "opstool"} {
			for _, other := range []string{"svc", "mgmt", "opstool", "foo"} {
				t.Run(component.container+"/"+role+"/"+other, func(t *testing.T) {
					good := inputRow("cpu", .239, .24, .1, "240m")
					good.Namespace, good.Kind, good.Workload, good.Container, good.Cluster = "prometheus", component.kind, component.workload, component.container, "test-"+role+"-1"
					good.InitContainer = component.container == "init-config-reloader"
					bad := good
					bad.Cluster, bad.Kind = "test-"+other+"-1", "Unknown"
					path := role + ".prometheus." + component.path + ".resources"
					if role == "opstool" {
						path = "svc.prometheus." + component.path + ".resources"
						if component.container == "prometheus" {
							path = "svc.prometheus.opstoolResources"
						}
					}
					blocked := role == other || other == "foo" || (component.container != "prometheus" && role != "mgmt" && other != "mgmt")
					for _, reverse := range []bool{false, true} {
						before := identityConfig(path, "100m")
						config := inputFile(t, "config.yaml", before)
						rows := []inputRecommendation{good, bad}
						if reverse {
							rows[0], rows[1] = rows[1], rows[0]
						}
						output := runInputOutput(t, inputFile(t, "input.json", inputJSON(t, rows...)), Options{ConfigPath: config})
						if blocked {
							if fileContents(t, config) != before || !strings.Contains(output, "SKIP blocked") {
								t.Fatalf("shared/unknown role did not block %s: %s", path, output)
							}
							continue
						}
						ed, err := editor.New(config)
						if err != nil {
							t.Fatal(err)
						}
						if got, _, err := ed.Get("clouds.dev.defaults." + path + ".requests.cpu"); err != nil || got != "240m" {
							t.Fatalf("unrelated role blocked %s: %s; %v", path, output, err)
						}
					}
				})
			}
		}
	}
}

func TestRunInputUnknownMappingAndWarnings(t *testing.T) {
	r := inputRow("cpu", .239, .24, .1, "240m")
	r.Container = "unknown"
	r.Warnings = []string{"unmapped row warning"}
	rows := make([]inputRecommendation, 10000)
	for i := range rows {
		rows[i] = r
	}
	mapped := inputRow("cpu", .239, .24, .2, "240m")
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
	good := inputRow("cpu", .239, .24, .2, "240m")
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
		{name: "increase boundary", peak: .219, suggested: .22, quantity: "220m", threshold: .1, want: "SKIP deadband", rowActionable: true},
		{name: "increase below boundary", peak: .209, suggested: .21, quantity: "210m", threshold: .1, want: "SKIP deadband"},
		{name: "increase above boundary", peak: .239, suggested: .24, quantity: "240m", threshold: .1, want: "CHANGE"},
		{name: "decrease boundary", peak: .179, suggested: .18, quantity: "180m", threshold: .1, allow: true, want: "SKIP deadband"},
		{name: "decrease above boundary", peak: .169, suggested: .17, quantity: "170m", threshold: .1, allow: true, want: "CHANGE"},
		{name: "decrease permission", peak: .169, suggested: .17, quantity: "170m", threshold: .1, want: "requires --allow-decrease"},
		{name: "zero disables", peak: .209, suggested: .21, quantity: "210m", threshold: 0, want: "CHANGE"},
		{name: "fraction epsilon", peak: .219, suggested: .22, quantity: "220m", threshold: .1 - 1e-14, want: "SKIP deadband"},
		{name: "beyond epsilon", peak: .219, suggested: .22, quantity: "220m", threshold: .1 - 1e-8, want: "CHANGE"},
		{name: "risk bypass", peak: .241, suggested: .25, quantity: "250m", threshold: 1, want: "CHANGE"},
		{name: "risk equality", peak: .24, suggested: .24, quantity: "240m", threshold: 1, want: "SKIP deadband", rowRisk: true},
		{name: "ignore producer risk", peak: .219, suggested: .22, quantity: "220m", threshold: .1, want: "SKIP deadband", rowRisk: true},
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
			name: "max crosses deadband", threshold: .1, want: "240m",
			rows: []inputRecommendation{inputRow("cpu", .239, .24, .2, "240m"), inputRow("cpu", .209, .21, .2, "210m")},
		},
		{
			name: "max stays in deadband despite actionable decrease", threshold: .1, want: "200m",
			rows: []inputRecommendation{inputRow("cpu", .219, .22, .2, "220m"), inputRow("cpu", .119, .12, .2, "120m")},
		},
		{
			// Both round to 250m. Risk must consider the second peak, not
			// just whichever row first supplied the maximum suggestion.
			name: "risk from tied suggestion", threshold: 1, want: "250m",
			rows: []inputRecommendation{inputRow("cpu", .244, .25, .205, "250m"), inputRow("cpu", .247, .25, .205, "250m")},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.rows[1].Cluster = "mgmt"
			tc.rows[1].Actionable = true
			for _, rows := range [][]inputRecommendation{tc.rows, {tc.rows[1], tc.rows[0]}} {
				before := inputConfig
				if tc.name == "risk from tied suggestion" {
					before = strings.Replace(before, "200m #", "205m #", 1)
				}
				config := inputFile(t, "config.yaml", before)
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

func TestRunInputRiskBypassAndSafety(t *testing.T) {
	for _, tc := range []struct {
		name, current, want string
		peak, suggested     float64
		eligible            bool
	}{
		{name: "risk increase bypasses 100 percent deadband", current: "10m", peak: .0124, suggested: .02, eligible: true, want: "CHANGE"},
		{name: "risk equality respects deadband", current: "10m", peak: .012, suggested: .02, eligible: true, want: "SKIP deadband"},
		{name: "minimum noop", current: "10m", peak: .008, suggested: .01, eligible: true, want: "NOOP"},
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
				t.Fatal("unexpected risk bypass edits")
			}
		})
	}
}
