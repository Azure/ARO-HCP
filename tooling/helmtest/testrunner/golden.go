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

package testrunner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-Tools/testutil"
)

type option func(*options)

type options struct {
	goldenDir string
	updateEnv string
	extension string
	suffix    string
}

func WithExtension(extension string) option {
	return func(opts *options) {
		opts.extension = extension
	}
}

func WithSuffix(suffix string) option {
	return func(opts *options) {
		opts.suffix = suffix
	}
}

func WithGoldenDir(goldenDir string) option {
	return func(opts *options) {
		opts.goldenDir = goldenDir
	}
}

func WithUpdateEnv(env string) option {
	return func(opts *options) {
		opts.updateEnv = env
	}
}

// CompareWithFixture compares output with a golden fixture. When WithGoldenDir
// is used, fixtures are stored in that directory. Otherwise delegates to
// testutil.CompareWithFixture which uses testdata/.
func CompareWithFixture(t *testing.T, output any, opts ...option) {
	t.Helper()
	o := &options{updateEnv: "UPDATE", extension: ".yaml"}
	for _, opt := range opts {
		opt(o)
	}

	if o.goldenDir == "" {
		testutil.CompareWithFixture(t, output,
			testutil.WithUpdateEnv(o.updateEnv),
			testutil.WithExtension(o.extension),
			testutil.WithSuffix(o.suffix),
		)
		return
	}

	compareWithFixtureInDir(t, output, o)
}

func compareWithFixtureInDir(t *testing.T, output any, o *options) {
	t.Helper()

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

	golden := filepath.Join(o.goldenDir, sanitizeFilename(t.Name()+o.suffix)) + o.extension
	if os.Getenv(o.updateEnv) != "" {
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
		t.Errorf("got diff between expected and actual result:\nfile: %s\ndiff:\n%s\n\nIf this is expected, re-run the test with `%s=true go test ./...` to update the fixtures.", golden, diff, o.updateEnv)
	}
}

// sanitizeFilename is based on the unexported function in github.com/Azure/ARO-Tools/testutil
// but uses inclusive upper bounds (<= 'z', <= 'Z') to correctly include all letters.
// Upstream uses exclusive bounds (< 'z', < 'Z') which excludes 'z' and 'Z'.
func sanitizeFilename(s string) string {
	result := strings.Builder{}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || r == '.' || (r >= '0' && r <= '9') {
			_, _ = result.WriteRune(r)
			continue
		}
		if !strings.HasSuffix(result.String(), "_") {
			result.WriteRune('_')
		}
	}
	return "zz_fixture_" + result.String()
}
