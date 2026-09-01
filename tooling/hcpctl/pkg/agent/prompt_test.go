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

package agent

import (
	"fmt"
	"strings"
	"testing"
)

// TestSystemFragmentsResolve ensures every fragment referenced by
// systemFragments exists for both modes, so a mistyped path or a missing
// variant is caught rather than surfacing at runtime.
func TestSystemFragmentsResolve(t *testing.T) {
	for _, f := range systemFragments {
		paths := []string{f.path}
		if f.path == "" {
			paths = []string{
				fmt.Sprintf("%s.test.md", f.variant),
				fmt.Sprintf("%s.intent.md", f.variant),
			}
		}
		for _, p := range paths {
			if _, err := promptFS.ReadFile(p); err != nil {
				t.Errorf("system fragment %q not embedded: %v", p, err)
			}
		}
	}
	if _, err := buildSystemBody(ModeTest); err != nil {
		t.Errorf("buildSystemBody(ModeTest): %v", err)
	}
	if _, err := buildSystemBody(ModeIntent); err != nil {
		t.Errorf("buildSystemBody(ModeIntent): %v", err)
	}
}

// TestModesShareStructureDifferInFraming verifies that both modes assemble the
// same shared sections and differ only in the four mode-specific sections.
func TestModesShareStructureDifferInFraming(t *testing.T) {
	testBody, err := buildDomainContent(ModeTest)
	if err != nil {
		t.Fatalf("buildDomainContent(ModeTest): %v", err)
	}
	intentBody, err := buildDomainContent(ModeIntent)
	if err != nil {
		t.Fatalf("buildDomainContent(ModeIntent): %v", err)
	}

	// Test framing present only in test mode.
	testFraming := `The first question is always: **"Why did this test fail?"**`
	if !strings.Contains(testBody, testFraming) {
		t.Errorf("test prompt missing the fixed test first-question framing")
	}
	if strings.Contains(intentBody, testFraming) {
		t.Errorf("intent prompt still contains the fixed test first-question framing")
	}
	if strings.Contains(intentBody, "question MUST be exactly `"+`"Why did this test fail?"`+"`") {
		t.Errorf("intent prompt still contains the fixed first-question chain rule")
	}

	// Intent framing present only in intent mode.
	for _, want := range []string{
		"investigation objective",
		"do not assume a test failure",
		"Read the investigation objective carefully",
		"short (≤ 10 word) headline",
	} {
		if !strings.Contains(intentBody, want) {
			t.Errorf("intent prompt missing expected phrase %q", want)
		}
		if strings.Contains(testBody, want) {
			t.Errorf("test prompt unexpectedly contains intent phrase %q", want)
		}
	}

	// Shared sections must appear verbatim in both modes.
	for _, shared := range []string{
		"# Analysis Instructions",
		"## Standards For Proof",
		"## Output Schema",
		"## Markdown Formatting Rules",
		"## Available Data Sources",
		"### Kusto (via kusto_query tool)",
		"## Discovery",
		"## KQL Quality Rules",
		"## Epistemological Rules",
	} {
		if !strings.Contains(testBody, shared) {
			t.Fatalf("precondition: test body missing shared section %q", shared)
		}
		if !strings.Contains(intentBody, shared) {
			t.Errorf("intent prompt dropped shared section %q", shared)
		}
	}

	// Every mode-specific section heading must be present in both modes (the
	// heading is shared; only the body differs).
	for _, heading := range []string{
		"## The Recursive Why Method",
		"### Chain link rules",
		"## Debugging Methodology",
		"## Methodology",
	} {
		if !strings.Contains(testBody, heading) || !strings.Contains(intentBody, heading) {
			t.Errorf("mode-specific heading %q missing from one of the modes", heading)
		}
	}
}

// TestBuildIntentInitialPromptOmitsAbsentContext ensures optional test artifacts
// are only mentioned when present.
func TestBuildIntentInitialPromptOmitsAbsentContext(t *testing.T) {
	intent := "customer reports nodepool np-1 stuck scaling"

	withNone := BuildIntentInitialPrompt(intent, "{}", "", "", "", "/data", nil)
	if strings.Contains(withNone, "## Additional Context") {
		t.Errorf("did not expect Additional Context section when no logs present")
	}
	if !strings.Contains(withNone, "## Investigation Objective") || !strings.Contains(withNone, intent) {
		t.Errorf("intent prompt missing objective section or intent text")
	}

	withErr := BuildIntentInitialPrompt(intent, "{}", "boom", "", "", "/data", nil)
	if !strings.Contains(withErr, "## Additional Context") || !strings.Contains(withErr, "### Test Error Log") {
		t.Errorf("expected Additional Context + Test Error Log when error log present")
	}
}
