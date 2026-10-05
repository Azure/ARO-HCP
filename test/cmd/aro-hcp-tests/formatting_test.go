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

package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onsi/gomega"
	"github.com/onsi/gomega/format"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

type formattingFailures struct {
	messages []string
}

func (f *formattingFailures) Helper() {}

func (f *formattingFailures) Fatalf(format string, args ...any) {
	f.messages = append(f.messages, fmt.Sprintf(format, args...))
}

func useProductionGomegaFormatting(t *testing.T) {
	t.Helper()
	// These globals are process-wide; formatter tests must not run in parallel.
	maxLength, maxDepth := format.MaxLength, format.MaxDepth
	t.Cleanup(func() {
		format.MaxLength, format.MaxDepth = maxLength, maxDepth
	})
	configureGomegaFormatting()
}

func TestGomegaFailureOutput(t *testing.T) {
	useProductionGomegaFormatting(t)

	type properties struct {
		Version string
	}
	type cluster struct {
		Properties *properties
	}
	actualVersion, expectedVersion := "4.20.1", "4.20.2"
	// Exceed Gomega's string-diff threshold without triggering cmp's chunked representation.
	longActual, longExpected := strings.Repeat("a", 60), strings.Repeat("b", 60)
	unlimitedLength := strings.Repeat("a", 5000)

	for _, tc := range []struct {
		name     string
		actual   any
		expected any
		negative bool
		equal    bool
		want     []string
	}{
		{
			name:   "positive enum",
			actual: hcpsdk20240610preview.ProvisioningStateFailed, expected: hcpsdk20240610preview.ProvisioningStateSucceeded,
			want: []string{"diff:", "-", "+", `"Failed"`, `"Succeeded"`},
		},
		{
			name:   "negative enum",
			actual: hcpsdk20240610preview.ProvisioningStateFailed, expected: hcpsdk20240610preview.ProvisioningStateFailed,
			negative: true, want: []string{"not to be comparable to", ": Failed"},
		},
		{
			name:   "original negative Equal enum",
			actual: hcpsdk20240610preview.ProvisioningStateFailed, expected: hcpsdk20240610preview.ProvisioningStateFailed,
			negative: true, equal: true, want: []string{"not to equal", ": Failed"},
		},
		{
			name:   "scalar pointer",
			actual: &actualVersion, expected: &expectedVersion,
			want: []string{"diff:", "-", "+", actualVersion, expectedVersion},
		},
		{
			name:   "negative scalar pointer",
			actual: &actualVersion, expected: &actualVersion,
			negative: true, want: []string{"not to be comparable to", ": " + actualVersion},
		},
		{
			name:   "nested struct",
			actual: &cluster{Properties: &properties{Version: actualVersion}}, expected: &cluster{Properties: &properties{Version: expectedVersion}},
			want: []string{"diff:", "Properties:", "Version:", "-", "+", actualVersion, expectedVersion},
		},
		{
			name:   "positive long strings",
			actual: longActual, expected: longExpected,
			want: []string{"diff:", "-", "+", longActual, longExpected},
		},
		{
			name:   "negative long strings",
			actual: unlimitedLength, expected: unlimitedLength,
			negative: true, want: []string{"not to be comparable to", unlimitedLength},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, mode := range []string{"direct", "Eventually", "Consistently"} {
				t.Run(mode, func(t *testing.T) {
					failures := &formattingFailures{}
					g := gomega.NewWithT(failures)
					assert := func(g gomega.Gomega) {
						matcher := gomega.BeComparableTo(tc.expected)
						if tc.equal {
							matcher = gomega.Equal(tc.expected)
						}
						if tc.negative {
							g.Expect(tc.actual).NotTo(matcher, "inner resource assertion")
						} else {
							g.Expect(tc.actual).To(matcher, "inner resource assertion")
						}
					}
					switch mode {
					case "direct":
						assert(g)
					case "Eventually":
						// Poll once, then time out without depending on scheduler timing.
						g.Eventually(assert).WithTimeout(0).WithPolling(time.Hour).Should(gomega.Succeed(), "outer polling assertion")
					case "Consistently":
						g.Consistently(assert).WithTimeout(time.Second).Should(gomega.Succeed(), "outer polling assertion")
					}
					if len(failures.messages) != 1 {
						t.Fatalf("expected one captured failure, got %q", failures.messages)
					}
					output := failures.messages[0]
					want := append([]string{"inner resource assertion"}, tc.want...)
					if mode != "direct" {
						want = append(want, "outer polling assertion", "The function passed to "+mode+" failed at")
					}
					for _, fragment := range want {
						if !strings.Contains(output, fragment) {
							t.Errorf("failure output missing %q:\n%s", fragment, output)
						}
					}
					if strings.Contains(output, "...") || strings.Contains(output, "\u2026") || strings.Contains(output, "<truncated>") {
						t.Errorf("failure output unexpectedly truncated:\n%s", output)
					}
				})
			}
		})
	}
}

func TestGomegaZeroDepthHidesNegativeEqualEnum(t *testing.T) {
	useProductionGomegaFormatting(t)
	format.MaxDepth = 0 // Reproduce the original CLI configuration.
	failures := &formattingFailures{}
	gomega.NewWithT(failures).Expect(hcpsdk20240610preview.ProvisioningStateFailed).
		NotTo(gomega.Equal(hcpsdk20240610preview.ProvisioningStateFailed), "resource must not fail")
	if len(failures.messages) != 1 {
		t.Fatalf("expected one captured failure, got %q", failures.messages)
	}
	output := failures.messages[0]
	if !strings.Contains(output, "not to equal") || strings.Count(output, ": ...") != 2 || strings.Contains(output, "Failed") {
		t.Fatalf("expected the original configuration to hide both enum values:\n%s", output)
	}
}
