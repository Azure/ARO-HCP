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

package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"sigs.k8s.io/yaml"
)

// CompareFileWithFixture loads data from the outputFile and compares the content with a golden fixture.
func CompareFileWithFixture(t *testing.T, outputFile string, opts ...option) {
	t.Helper()
	raw, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("failed to read output: %v", err)
	}
	CompareWithFixture(t, raw, append(opts, WithExtension(filepath.Ext(outputFile)))...)
}

// CompareWithFixture will compare output with a test fixture and allows to automatically update them
// by setting the UPDATE env var.
// If output is not a []byte or string, it will get serialized as yaml prior to the comparison.
// The fixtures are stored in $PWD/testdata/prefix${testName}.yaml
func CompareWithFixture(t *testing.T, output interface{}, opts ...option) {
	t.Helper()
	options := &options{
		Extension: ".yaml",
	}
	for _, opt := range opts {
		opt(options)
	}

	var serializedOutput []byte
	switch v := output.(type) {
	case []byte:
		serializedOutput = v
	case string:
		serializedOutput = []byte(v)
	default:
		serialized, err := yaml.Marshal(v)
		if err != nil {
			t.Fatalf("failed to yaml marshal output of type %T: %v", output, err)
		}
		serializedOutput = serialized
	}

	golden, err := golden(t, options)
	if err != nil {
		t.Fatalf("failed to get absolute path to testdata file: %v", err)
	}
	if os.Getenv("UPDATE") != "" {
		if err := os.MkdirAll(filepath.Dir(golden), 0755); err != nil {
			t.Fatalf("failed to create fixture directory: %v", err)
		}
		if err := os.WriteFile(golden, serializedOutput, 0644); err != nil {
			t.Fatalf("failed to write updated fixture: %v", err)
		}
	}
	expected, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("failed to read testdata file: %v", err)
	}

	if diff := cmp.Diff(string(expected), string(serializedOutput)); diff != "" {
		t.Errorf("got diff between expected and actual result:\nfile: %s\ndiff:\n%s\n\nIf this is expected, re-run the test with `UPDATE=true go test ./...` to update the fixtures.", golden, diff)
	}
}

type options struct {
	Prefix    string
	Suffix    string
	Extension string

	SubDir string
}

type option func(*options)

func WithExtension(extension string) option {
	return func(opts *options) {
		opts.Extension = extension
	}
}

func WithSuffix(suffix string) option {
	return func(opts *options) {
		opts.Suffix = suffix
	}
}

func WithSubDir(subDir string) option {
	return func(opts *options) {
		opts.SubDir = subDir
	}
}

// golden determines the golden file to use
func golden(t *testing.T, opts *options) (string, error) {
	if opts.Extension == "" {
		opts.Extension = ".yaml"
	}
	return filepath.Abs(filepath.Join("../../testdata", opts.SubDir, sanitizeFilename(opts.Prefix+t.Name()+opts.Suffix)) + opts.Extension)
}

func sanitizeFilename(s string) string {
	result := strings.Builder{}
	for _, r := range s {
		if (r >= 'a' && r < 'z') || (r >= 'A' && r < 'Z') || r == '_' || r == '.' || (r >= '0' && r <= '9') {
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
