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
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/internal/tabular"
)

func TestParseDraftChain_WithClassification(t *testing.T) {
	input := `{
		"root_cause": "test",
		"summary": "test summary",
		"classification": {
			"l1_category": "Product Failures",
			"l2_subcategory": "HyperShift",
			"confidence": 0.92
		},
		"chain": [{"question": "Why did this test fail?", "answer": "reason", "proof": [{"type": "log", "source": "error", "lines": [1, 2]}]}]
	}`

	chain, err := ParseDraftChain(input)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if chain.Classification == nil {
		t.Fatal("expected classification to be non-nil")
	}
	if chain.Classification.L1Category != L1ProductFailures {
		t.Errorf("L1Category = %q, want %q", chain.Classification.L1Category, L1ProductFailures)
	}
	if chain.Classification.L2Subcategory != L2HyperShift {
		t.Errorf("L2Subcategory = %q, want %q", chain.Classification.L2Subcategory, L2HyperShift)
	}
	if chain.Classification.Confidence != 0.92 {
		t.Errorf("Confidence = %g, want 0.92", chain.Classification.Confidence)
	}
}

func TestParseDraftChain_WithoutClassification(t *testing.T) {
	input := `{
		"root_cause": "test",
		"summary": "test summary",
		"chain": [{"question": "Why did this test fail?", "answer": "reason", "proof": [{"type": "log", "source": "error", "lines": [1, 2]}]}]
	}`

	chain, err := ParseDraftChain(input)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if chain.Classification != nil {
		t.Errorf("expected classification to be nil, got %+v", chain.Classification)
	}
}

// noopKustoClient is a KustoClient that returns an empty table for any query.
type noopKustoClient struct{}

func (n *noopKustoClient) Query(_ context.Context, _ string) (*tabular.Table, error) {
	return &tabular.Table{Columns: []string{"col"}, Rows: [][]string{{"val"}}}, nil
}

func validDraftWithClassification(c *Classification) *DraftChain {
	return &DraftChain{
		RootCause:      "root cause",
		Summary:        "summary",
		Classification: c,
		Chain: []ChainLink{
			{
				Question: FirstChainQuestion,
				Answer:   "answer",
				Proof: []ProofItem{
					{Type: "log", Source: "error", Lines: [2]int{1, 1}},
					{Type: "code", Repo: "ARO-HCP", File: "main.go", Lines: [2]int{1, 1}},
				},
			},
		},
	}
}

func newValidationContext(t *testing.T) *ValidationContext {
	t.Helper()
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("failed to create stub source file: %v", err)
	}
	return &ValidationContext{
		ValidRepos:    map[string]bool{"ARO-HCP": true},
		WorktreePaths: map[string]string{"ARO-HCP": worktree},
		TestError:     "error line 1\n",
	}
}

func classificationProblems(result ValidationResult) []ValidationProblem {
	var filtered []ValidationProblem
	for _, p := range result.Problems {
		switch p.Category {
		case "missing_classification", "invalid_l1_category",
			"invalid_l2_subcategory", "unexpected_l2_subcategory",
			"invalid_confidence":
			filtered = append(filtered, p)
		}
	}
	return filtered
}

func TestValidateDraft_Classification(t *testing.T) {
	ctx := context.Background()
	client := &noopKustoClient{}
	vc := newValidationContext(t)

	tests := []struct {
		name           string
		classification *Classification
		wantCategory   string
	}{
		{
			name:           "missing classification",
			classification: nil,
			wantCategory:   "missing_classification",
		},
		{
			name:           "invalid L1 category",
			classification: &Classification{L1Category: "Not Real", Confidence: 0.5},
			wantCategory:   "invalid_l1_category",
		},
		{
			name:           "Product Failures missing L2",
			classification: &Classification{L1Category: L1ProductFailures, Confidence: 0.8},
			wantCategory:   "invalid_l2_subcategory",
		},
		{
			name:           "Product Failures invalid L2",
			classification: &Classification{L1Category: L1ProductFailures, L2Subcategory: "NotAComponent", Confidence: 0.8},
			wantCategory:   "invalid_l2_subcategory",
		},
		{
			name:           "non-Product Failures with L2 set",
			classification: &Classification{L1Category: L1AzureProblems, L2Subcategory: L2HyperShift, Confidence: 0.9},
			wantCategory:   "unexpected_l2_subcategory",
		},
		{
			name:           "confidence too low",
			classification: &Classification{L1Category: L1AzureProblems, Confidence: -0.1},
			wantCategory:   "invalid_confidence",
		},
		{
			name:           "confidence too high",
			classification: &Classification{L1Category: L1AzureProblems, Confidence: 1.5},
			wantCategory:   "invalid_confidence",
		},
		{
			name:           "valid Azure Problems",
			classification: &Classification{L1Category: L1AzureProblems, Confidence: 0.85},
			wantCategory:   "",
		},
		{
			name:           "valid Product Failures with L2",
			classification: &Classification{L1Category: L1ProductFailures, L2Subcategory: L2ClusterService, Confidence: 0.95},
			wantCategory:   "",
		},
		{
			name:           "valid Test Reliability",
			classification: &Classification{L1Category: L1TestReliability, Confidence: 0.7},
			wantCategory:   "",
		},
		{
			name:           "valid Deployment Failures",
			classification: &Classification{L1Category: L1DeploymentFailures, Confidence: 0.6},
			wantCategory:   "",
		},
		{
			name:           "confidence at boundary 0",
			classification: &Classification{L1Category: L1AzureProblems, Confidence: 0},
			wantCategory:   "",
		},
		{
			name:           "confidence at boundary 1",
			classification: &Classification{L1Category: L1AzureProblems, Confidence: 1},
			wantCategory:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			draft := validDraftWithClassification(tt.classification)
			result := ValidateDraft(ctx, client, draft, vc)
			problems := classificationProblems(result)

			if tt.wantCategory == "" {
				if len(problems) > 0 {
					t.Errorf("expected no classification problems, got %d: %v", len(problems), problems)
				}
				return
			}

			found := false
			for _, p := range problems {
				if p.Category == tt.wantCategory {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected problem category %q, got problems: %v", tt.wantCategory, problems)
			}
		})
	}
}

func TestRenderMarkdown_WithClassification(t *testing.T) {
	chain := &HydratedChain{
		RootCause: "root cause",
		Summary:   "summary",
		Classification: &Classification{
			L1Category:    L1ProductFailures,
			L2Subcategory: L2Maestro,
			Confidence:    0.88,
		},
		Chain: []HydratedLink{
			{
				Question: FirstChainQuestion,
				Answer:   "answer",
				Proof:    []HydratedProofItem{{ProofItem: ProofItem{Type: "log", Source: "error", Lines: [2]int{1, 1}}}},
			},
		},
	}

	rendered := RenderMarkdown(chain, "TestExample")

	if !strings.Contains(rendered, "## Classification") {
		t.Error("rendered markdown missing Classification section")
	}
	if !strings.Contains(rendered, "Product Failures") {
		t.Error("rendered markdown missing L1 category")
	}
	if !strings.Contains(rendered, "Maestro") {
		t.Error("rendered markdown missing L2 component")
	}
	if !strings.Contains(rendered, "88%") {
		t.Error("rendered markdown missing confidence percentage")
	}
}

func TestRenderMarkdown_WithoutL2(t *testing.T) {
	chain := &HydratedChain{
		RootCause: "root cause",
		Summary:   "summary",
		Classification: &Classification{
			L1Category: L1AzureProblems,
			Confidence: 0.95,
		},
		Chain: []HydratedLink{
			{
				Question: FirstChainQuestion,
				Answer:   "answer",
				Proof:    []HydratedProofItem{{ProofItem: ProofItem{Type: "log", Source: "error", Lines: [2]int{1, 1}}}},
			},
		},
	}

	rendered := RenderMarkdown(chain, "TestExample")

	if !strings.Contains(rendered, "Azure Problems") {
		t.Error("rendered markdown missing L1 category")
	}
	if strings.Contains(rendered, "**Component:**") {
		t.Error("rendered markdown should not contain Component line when L2 is empty")
	}
}

func TestRenderMarkdown_WithoutClassification(t *testing.T) {
	chain := &HydratedChain{
		RootCause: "root cause",
		Summary:   "summary",
		Chain: []HydratedLink{
			{
				Question: FirstChainQuestion,
				Answer:   "answer",
				Proof:    []HydratedProofItem{{ProofItem: ProofItem{Type: "log", Source: "error", Lines: [2]int{1, 1}}}},
			},
		},
	}

	rendered := RenderMarkdown(chain, "TestExample")

	if strings.Contains(rendered, "## Classification") {
		t.Error("rendered markdown should not contain Classification section when nil")
	}
}
