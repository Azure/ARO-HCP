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

package mustgather

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateCosmosContent(t *testing.T) {
	mustGatherPath := t.TempDir()
	customPath := filepath.Join(mustGatherPath, CustomLogsDirectory)
	if err := os.MkdirAll(customPath, 0755); err != nil {
		t.Fatal(err)
	}
	input := strings.Join([]string{
		`{"content":{"_ts":1788199837,"properties":{"cosmosMetadata":{"instanceVersion":1}}},"cosmosContainer":"resources","resourceID":"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"}`,
		`{"content":{"_ts":1788199838,"properties":{"cosmosMetadata":{"instanceVersion":2}}},"cosmosContainer":"resources","resourceID":"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"}`,
	}, "\n")
	inputPath := filepath.Join(customPath, "custom-query_cosmosResourceSnapshots.jsonl")
	if err := os.WriteFile(inputPath, []byte(input), 0644); err != nil {
		t.Fatal(err)
	}

	if err := generateCosmosContent(t.Context(), mustGatherPath); err != nil {
		t.Fatal(err)
	}

	contentPath := filepath.Join(mustGatherPath, "cosmosContent")
	if _, err := os.Stat(filepath.Join(contentPath, ".git")); err != nil {
		t.Fatalf("expected generated Git repository: %v", err)
	}
	cmd := exec.Command("git", "rev-list", "--count", "HEAD")
	cmd.Dir = contentPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to count generated commits: %v: %s", err, output)
	}
	if strings.TrimSpace(string(output)) != "2" {
		t.Fatalf("expected 2 commits, got %q", output)
	}
}

func TestGenerateEmptyCosmosContent(t *testing.T) {
	mustGatherPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(mustGatherPath, CustomLogsDirectory), 0755); err != nil {
		t.Fatal(err)
	}

	if err := generateCosmosContent(t.Context(), mustGatherPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(mustGatherPath, "cosmosContent", ".git")); err != nil {
		t.Fatalf("expected initialized Git repository for empty result: %v", err)
	}
}
