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

package controller

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"sigs.k8s.io/yaml"
)

// CompareWithFixture will compare output with a test fixture and allows to automatically update them
// by setting the UPDATE env var.
// The output will be serialized as YAML prior to the comparison.
// The fixtures are stored in testdata/zz_fixture_${testName}.yaml
func CompareWithFixture(t *testing.T, output interface{}, opts ...cmp.Option) {
	t.Helper()

	serialized, err := yaml.Marshal(output)
	if err != nil {
		t.Fatalf("failed to yaml marshal output of type %T: %v", output, err)
	}

	golden, err := goldenPath(t)
	if err != nil {
		t.Fatalf("failed to get absolute path to testdata file: %v", err)
	}

	if os.Getenv("UPDATE") != "" {
		if err := os.MkdirAll(filepath.Dir(golden), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, serialized, 0644); err != nil {
			t.Fatalf("failed to write updated fixture: %v", err)
		}
	}

	expected, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("failed to read testdata file: %v", err)
	}

	// For YAML comparison, unmarshal both sides and compare the objects
	// This is more reliable than string comparison
	var expectedObj, actualObj interface{}
	if err := yaml.Unmarshal(expected, &expectedObj); err != nil {
		t.Fatalf("failed to unmarshal expected fixture: %v", err)
	}
	if err := yaml.Unmarshal(serialized, &actualObj); err != nil {
		t.Fatalf("failed to unmarshal actual output: %v", err)
	}

	if diff := cmp.Diff(expectedObj, actualObj, opts...); diff != "" {
		t.Errorf("got diff between expected and actual result:\nfile: %s\ndiff:\n%s\n\nIf this is expected, re-run the test with `UPDATE=true go test ./...` to update the fixtures.", golden, diff)
	}
}

func goldenPath(t *testing.T) (string, error) {
	path := filepath.Join("testdata", sanitizeFilename(t.Name())) + ".yaml"
	return filepath.Abs(path)
}

func sanitizeFilename(s string) string {
	result := strings.Builder{}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || r == '.' || (r >= '0' && r <= '9') {
			// The thing is documented as returning a nil error so lets just drop it
			_, _ = result.WriteRune(r)
			continue
		}
		if !strings.HasSuffix(result.String(), "_") {
			result.WriteRune('_')
		}
	}
	return "zz_fixture_" + result.String()
}
