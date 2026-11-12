// Copyright 2026 Microsoft Corporation
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

package configoverride

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", path, data, want)
	}
}

func TestWriteOverridePublishesCompleteFile(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "new file"
		if existing {
			name = "replace existing file"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config-override.yaml")
			if existing {
				if err := os.WriteFile(path, []byte("old override"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			data := "clouds:\n  dev: {}\n"
			if err := WriteOverride(path, []byte(data)); err != nil {
				t.Fatal(err)
			}
			assertFile(t, path, data)
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
				t.Fatalf("temporary file remains: %v", entries)
			}
		})
	}
}

func TestWriteOverridePreservesExistingOpenFile(t *testing.T) {
	path := fixture(t, "config-override.yaml", "old override")
	old, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if err := WriteOverride(path, []byte("new override")); err != nil {
		t.Fatal(err)
	}
	assertFile(t, path, "new override")
	data := make([]byte, len("old override"))
	if _, err := old.ReadAt(data, 0); err != nil {
		t.Fatal(err)
	}
	if string(data) != "old override" {
		t.Fatalf("existing inode was modified: %q", data)
	}
}

func TestWriteOverrideFailuresPreserveFilesAndCleanTemps(t *testing.T) {
	for _, failure := range []string{"parent is file", "missing parent", "rename over nonempty directory"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			protected := filepath.Join(dir, "existing-output.yaml")
			if err := os.WriteFile(protected, []byte("preserve existing output"), 0o600); err != nil {
				t.Fatal(err)
			}
			var path string
			switch failure {
			case "parent is file":
				path = filepath.Join(protected, "override.yaml")
			case "missing parent":
				path = filepath.Join(dir, "missing", "override.yaml")
			case "rename over nonempty directory":
				path = filepath.Join(dir, "output-directory")
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
				protected = filepath.Join(path, "existing-output.yaml")
				if err := os.WriteFile(protected, []byte("preserve existing output"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if err := WriteOverride(path, []byte("replacement")); err == nil {
				t.Fatal("expected publication error")
			}
			assertFile(t, protected, "preserve existing output")
			after, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			beforeNames, afterNames := make([]string, len(before)), make([]string, len(after))
			for i := range before {
				beforeNames[i] = before[i].Name()
			}
			for i := range after {
				afterNames[i] = after[i].Name()
			}
			if !reflect.DeepEqual(beforeNames, afterNames) {
				t.Fatalf("directory changed on failure: before %v, after %v", beforeNames, afterNames)
			}
		})
	}
}

func TestGenerateErrorsReturnNoPartialResult(t *testing.T) {
	for _, failure := range []string{"image", "unknown image", "mock catalog", "second helper", "identity bundle", "overlay"} {
		t.Run(failure, func(t *testing.T) {
			input := Inputs{DeployEnvironment: "ci01", Images: map[string]string{"BACKEND_IMAGE": "registry/backend@sha256:123"}, OCLoginRegistries: "existing.registry"}
			paths := Paths{}
			switch failure {
			case "image":
				input.Images["EXPORTER_IMAGE"] = "not-a-digest-ref"
			case "unknown image":
				input.Images["UNKNOWN_IMAGE"] = ""
			case "mock catalog":
				input.MSIMockLease = "missing"
				paths.MSIMockCatalog = fixture(t, "mock.yaml", mockCatalog)
			case "second helper":
				input.ARMHelperLeases = []string{"backend", "missing"}
				paths.ARMHelperCatalog = fixture(t, "helper.yaml", helperCatalog)
			case "identity bundle":
				input.InfrastructureIdentityBundle = "{}"
			case "overlay":
				paths.HypershiftOverlay = fixture(t, "overlay.yaml", "[")
			}
			result, err := Generate(input, paths)
			if err == nil {
				t.Fatal("expected generation error before publication")
			}
			if result.YAML != nil || result.OCLoginRegistries != "" {
				t.Fatalf("partial output returned: %#v", result)
			}
		})
	}
}
