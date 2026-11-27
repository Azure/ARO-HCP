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

package options

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validBundlesConfig = `bundles:
  control-plane:
  - cs
  - hypershift-stack
  add-ons:
  - platform-utils
images:
  clusters-service:
    group: cs
    source:
      image: quay.io/test/clusters-service
    targets:
    - jsonPath: image.digest
      filePath: config.yaml
  hypershift:
    group: hypershift-stack
    source:
      image: quay.io/test/hypershift
    targets:
    - jsonPath: image.digest
      filePath: config.yaml
  acrPull:
    group: platform-utils
    source:
      image: mcr.microsoft.com/test/acrpull
    targets:
    - jsonPath: image.digest
      filePath: config.yaml
`

// writeValidateConfig writes content to a temporary configuration file.
func writeValidateConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	return path
}

// TestValidateComplete covers the configuration and bundle checks.
func TestValidateComplete(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name:    "valid partition",
			content: validBundlesConfig,
		},
		{
			name: "bundle with no groups",
			content: strings.Replace(validBundlesConfig,
				"  add-ons:\n  - platform-utils\n",
				"  add-ons: []\n", 1),
			wantErr: `bundle "add-ons" has no groups`,
		},
		{
			name: "group outside every bundle",
			content: `bundles:
  control-plane:
  - cs
images:
  clusters-service:
    group: cs
    source:
      image: quay.io/test/clusters-service
    targets:
    - jsonPath: image.digest
      filePath: config.yaml
  acrPull:
    group: platform-utils
    source:
      image: mcr.microsoft.com/test/acrpull
    targets:
    - jsonPath: image.digest
      filePath: config.yaml
`,
			wantErr: "groups not in any bundle: platform-utils",
		},
		{
			name: "group in two bundles",
			content: strings.Replace(validBundlesConfig,
				"  add-ons:\n  - platform-utils\n",
				"  add-ons:\n  - platform-utils\n  - cs\n", 1),
			wantErr: `group "cs" is in bundles "add-ons" and "control-plane"`,
		},
		{
			name: "bundle references an unknown group",
			content: strings.Replace(validBundlesConfig,
				"  - platform-utils\nimages:",
				"  - platform-utils\n  - networking\nimages:", 1),
			wantErr: `bundle "add-ons" references unknown group "networking"`,
		},
		{
			name: "component without a group",
			content: strings.Replace(validBundlesConfig,
				"  clusters-service:\n    group: cs\n",
				"  clusters-service:\n", 1),
			wantErr: `image "clusters-service": group is required`,
		},
		{
			name: "unknown field in an image entry",
			content: strings.Replace(validBundlesConfig,
				"    group: hypershift-stack\n",
				"    group: hypershift-stack\n    typo: true\n", 1),
			wantErr: `field typo not found in type config.ImageConfig`,
		},
		{
			name: "group with surrounding whitespace",
			content: strings.Replace(validBundlesConfig,
				"    group: cs\n",
				"    group: \"cs \"\n", 1),
			wantErr: `image "clusters-service": group "cs " has surrounding whitespace`,
		},
		{
			name: "bundle name with a comma",
			content: strings.Replace(validBundlesConfig,
				"  add-ons:\n",
				"  \"add-ons,extra\":\n", 1),
			wantErr: `bundle "add-ons,extra" contains a comma`,
		},
		{
			name: "bundle group with a comma",
			content: strings.Replace(validBundlesConfig,
				"  - platform-utils\nimages:",
				"  - \"platform-utils,extra\"\nimages:", 1),
			wantErr: `bundle "add-ons": group "platform-utils,extra" contains a comma`,
		},
		{
			name: "no bundles",
			content: `images:
  clusters-service:
    group: cs
    source:
      image: quay.io/test/clusters-service
    targets:
    - jsonPath: image.digest
      filePath: config.yaml
`,
			wantErr: "no bundles configured",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeValidateConfig(t, test.content)

			raw := &RawValidateOptions{ConfigPath: path}
			validated, err := raw.Validate(context.Background())
			if err != nil {
				t.Fatalf("Validate returned an error: %v", err)
			}

			_, err = validated.Complete(context.Background())
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("Complete returned an error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Complete succeeded, want error containing %q", test.wantErr)
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Complete error %q does not contain %q", err.Error(), test.wantErr)
			}
		})
	}
}

// TestValidateReport covers the bundle report output.
func TestValidateReport(t *testing.T) {
	path := writeValidateConfig(t, validBundlesConfig)

	validated, err := (&RawValidateOptions{ConfigPath: path}).Validate(context.Background())
	if err != nil {
		t.Fatalf("Validate returned an error: %v", err)
	}
	completed, err := validated.Complete(context.Background())
	if err != nil {
		t.Fatalf("Complete returned an error: %v", err)
	}

	var out strings.Builder
	if err := completed.PrintReport(&out); err != nil {
		t.Fatalf("PrintReport returned an error: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"add-ons: 1 groups, 1 components\n",
		"control-plane: 2 groups, 2 components\n",
		"validated 3 components in 3 groups across 2 bundles\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("report %q does not contain %q", got, want)
		}
	}
}
