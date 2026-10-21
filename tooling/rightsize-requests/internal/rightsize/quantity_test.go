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

import "testing"

func TestParseCPUCores(t *testing.T) {
	cases := []struct {
		in      string
		want    float64
		wantErr bool
	}{
		{"100m", 0.1, false},
		{"1", 1, false},
		{"2500m", 2.5, false},
		{"NONE", 0, true},
		{"unlimited", 0, true},
		{"", 0, true},
	}
	for _, c := range cases {
		got, err := ParseCPUCores(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseCPUCores(%q): expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseCPUCores(%q): unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseCPUCores(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseMemoryBytes(t *testing.T) {
	cases := []struct {
		in      string
		want    float64
		wantErr bool
	}{
		{"512Mi", 512 * 1024 * 1024, false},
		{"1Gi", 1024 * 1024 * 1024, false},
		{"768Mi", 768 * 1024 * 1024, false},
		{"1000000", 1000000, false},
		{"NONE", 0, true},
	}
	for _, c := range cases {
		got, err := ParseMemoryBytes(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseMemoryBytes(%q): expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseMemoryBytes(%q): unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseMemoryBytes(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestFormatCPU(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0.1, "100m"},
		{0.125, "130m"}, // rounds up to nearest 10m
		{0.001, "10m"},  // floor is 10m
		{1.0, "1000m"},
		{0.1001, "110m"}, // just above a 10m boundary must round UP, not down
	}
	for _, c := range cases {
		if got := FormatCPU(c.in); got != c.want {
			t.Errorf("FormatCPU(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFormatNeverUndersizes is the safety property: the emitted request must
// never be smaller than the observed usage (rounding is always up).
func TestFormatNeverUndersizes(t *testing.T) {
	const mi = 1024 * 1024
	cpuInputs := []float64{0.001, 0.1, 0.1001, 0.1049, 0.19, 0.999, 1.0001, 2.734}
	for _, in := range cpuInputs {
		got, err := ParseCPUCores(FormatCPU(in))
		if err != nil {
			t.Fatalf("ParseCPUCores(%q): %v", FormatCPU(in), err)
		}
		if got < in {
			t.Errorf("FormatCPU(%v) = %s parses to %v, which is < input", in, FormatCPU(in), got)
		}
	}
	memInputs := []float64{1, 100 * mi, 100*mi + 1, 511*mi + 3, 1024 * mi, 1500.7 * mi}
	for _, in := range memInputs {
		got, err := ParseMemoryBytes(FormatMemory(in))
		if err != nil {
			t.Fatalf("ParseMemoryBytes(%q): %v", FormatMemory(in), err)
		}
		if got < in {
			t.Errorf("FormatMemory(%v) = %s parses to %v, which is < input", in, FormatMemory(in), got)
		}
	}
}

func TestFormatMemory(t *testing.T) {
	const mi = 1024 * 1024
	cases := []struct {
		in   float64
		want string
	}{
		{100 * mi, "112Mi"},   // 100 rounds up to 112 (nearest 16)
		{512 * mi, "512Mi"},   // already a multiple of 16
		{1024 * mi, "1Gi"},    // collapses to Gi
		{float64(mi), "16Mi"}, // floor 16Mi
		{1500 * mi, "1504Mi"}, // 1500 -> 1504 (nearest 16), not Gi
	}
	for _, c := range cases {
		if got := FormatMemory(c.in); got != c.want {
			t.Errorf("FormatMemory(%v bytes) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMappingNamespaces(t *testing.T) {
	ns := Namespaces()
	if len(ns) == 0 {
		t.Fatal("expected non-empty namespace list")
	}
	// aro-hcp must be present (hosts backend + frontend).
	found := false
	for _, n := range ns {
		if n == "aro-hcp" {
			found = true
		}
	}
	if !found {
		t.Error("expected aro-hcp in namespaces")
	}
}

func TestLookupDisambiguatesSharedNamespace(t *testing.T) {
	be, ok := Lookup("aro-hcp", "aro-hcp-backend")
	if !ok || be.ResourcePath != "backend.k8s.resources" {
		t.Errorf("backend lookup failed: %+v ok=%v", be, ok)
	}
	fe, ok := Lookup("aro-hcp", "aro-hcp-frontend")
	if !ok || fe.ResourcePath != "frontend.k8s.resources" {
		t.Errorf("frontend lookup failed: %+v ok=%v", fe, ok)
	}
	if _, ok := Lookup("aro-hcp", "unknown"); ok {
		t.Error("expected unknown container to be unmapped")
	}
}
