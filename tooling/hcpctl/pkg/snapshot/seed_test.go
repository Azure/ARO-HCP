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

package snapshot

import (
	"strings"
	"testing"
)

// TestResolveClusterFromResourceGroup covers the three resolution outcomes of the
// subscription+resource-group identity seed: zero rows (wrong scope / outside
// window), exactly one (resolved), and more than one (ambiguous).
func TestResolveClusterFromResourceGroup(t *testing.T) {
	const (
		sub = "00000000-0000-0000-0000-000000000000"
		rg  = "rg-test"
		id1 = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg-test/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c1"
		id2 = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg-test/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c2"
	)

	row := func(v string) resultRow {
		return resultRow{columns: []string{"clusterResourceID"}, values: []string{v}}
	}

	t.Run("single row resolves", func(t *testing.T) {
		got, err := resolveClusterFromResourceGroup([]resultRow{row(id1)}, sub, rg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != id1 {
			t.Errorf("got %q, want %q", got, id1)
		}
	})

	t.Run("empty rows are ignored, resolving remaining single", func(t *testing.T) {
		got, err := resolveClusterFromResourceGroup([]resultRow{row(""), row(id1)}, sub, rg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != id1 {
			t.Errorf("got %q, want %q", got, id1)
		}
	})

	t.Run("zero rows errors naming sub and rg", func(t *testing.T) {
		_, err := resolveClusterFromResourceGroup(nil, sub, rg)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), sub) || !strings.Contains(err.Error(), rg) {
			t.Errorf("error %q should name subscription and resource group", err)
		}
	})

	t.Run("multiple rows errors listing candidates", func(t *testing.T) {
		_, err := resolveClusterFromResourceGroup([]resultRow{row(id1), row(id2)}, sub, rg)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), id1) || !strings.Contains(err.Error(), id2) {
			t.Errorf("error %q should list candidate cluster ids", err)
		}
		if !strings.Contains(err.Error(), "--cluster-resource-id") {
			t.Errorf("error %q should direct user to --cluster-resource-id", err)
		}
	})
}

// TestClusterByResourceGroupTemplateRenders guards the resolution query template,
// which is rendered directly by the gatherer (not registered in allQueries) and so
// is not covered by TestAllQueryTemplatesRender.
func TestClusterByResourceGroupTemplateRenders(t *testing.T) {
	const path = "queries/backend/clusterByResourceGroup/query.kql"
	data := fullQueryData()
	rendered, err := renderQuery(path, data)
	if err != nil {
		t.Fatalf("renderQuery(%s) failed: %v", path, err)
	}
	if strings.TrimSpace(rendered) == "" {
		t.Fatalf("renderQuery(%s) produced empty output", path)
	}
	if !strings.Contains(rendered, data.SubscriptionID) {
		t.Errorf("rendered query should filter by subscription id, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "hcpopenshiftclusters") {
		t.Errorf("rendered query should filter by cluster resource type, got:\n%s", rendered)
	}
}
