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

package validation

import (
	"context"
	"strings"
	"testing"

	"github.com/blang/semver/v4"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

func TestHostPort(t *testing.T) {
	ctx := context.Background()
	op := operation.Operation{Type: operation.Create}
	fldPath := field.NewPath("target")

	tests := []struct {
		name        string
		value       *string
		expectError bool
		errContains string
	}{
		{
			name:  "nil value accepted",
			value: nil,
		},
		{
			name:  "empty value accepted",
			value: ptr.To(""),
		},
		{
			name:  "valid DNS host with port",
			value: ptr.To("maestro.example.com:8090"),
		},
		{
			name:  "valid short DNS host with port",
			value: ptr.To("maestro:8090"),
		},
		{
			name:  "valid IPv4 with port",
			value: ptr.To("10.0.0.1:8090"),
		},
		{
			name:  "valid IPv6 with port",
			value: ptr.To("[::1]:8090"),
		},
		{
			name:        "missing port rejected",
			value:       ptr.To("maestro.example.com"),
			expectError: true,
			errContains: "must be host:port",
		},
		{
			name:        "empty host rejected",
			value:       ptr.To(":8090"),
			expectError: true,
			errContains: "host must not be empty",
		},
		{
			name:        "underscore in host rejected",
			value:       ptr.To("not_valid:8090"),
			expectError: true,
			errContains: "invalid host",
		},
		{
			name:        "uppercase in host rejected",
			value:       ptr.To("NOT-VALID:8090"),
			expectError: true,
			errContains: "invalid host",
		},
		{
			name:        "trailing dot in host rejected",
			value:       ptr.To("invalid.:8090"),
			expectError: true,
			errContains: "invalid host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := HostPort(ctx, op, fldPath, tt.value, nil)
			if tt.expectError {
				if len(errs) == 0 {
					t.Errorf("expected error containing %q, got none", tt.errContains)
					return
				}
				found := false
				for _, e := range errs {
					if strings.Contains(e.Error(), tt.errContains) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error containing %q, got %v", tt.errContains, errs)
				}
			} else {
				if len(errs) != 0 {
					t.Errorf("expected no errors, got %v", errs)
				}
			}
		})
	}
}

// mustSemverPtr parses a semantic version string and returns a pointer to it,
// panicking on parse errors (test helper only).
func mustSemverPtr(s string) *semver.Version {
	v := semver.MustParse(s)
	return &v
}

// nodePoolActiveVersions builds a []coreapi.HCPNodePoolActiveVersion from a list of
// version strings, representing the node pool's currently-active versions.
func nodePoolActiveVersions(versions ...string) []coreapi.ServiceProviderNodePoolActiveVersion {
	out := make([]coreapi.ServiceProviderNodePoolActiveVersion, 0, len(versions))
	for _, v := range versions {
		out = append(out, coreapi.ServiceProviderNodePoolActiveVersion{Version: mustSemverPtr(v)})
	}
	return out
}

// TestValidateNodePoolVersionChange verifies that node pool version validation
// compares only the major.minor of the desired version against the control
// plane version. A node pool is allowed to run a higher patch (z-stream) than
// the control plane; only a higher minor must be rejected.
func TestValidateNodePoolVersionChange(t *testing.T) {
	tests := []struct {
		name           string
		desiredVersion string
		activeVersions []string
		lowestCP       string // empty string means an unknown (nil) *semver.Version
		highestCP      string // empty string means an unknown (nil) *semver.Version
		allowMajor     bool
		expectError    bool
		errContains    string
	}{
		{
			// Bug case: NP wants 4.22.8 while CP is 4.22.7 (same minor, higher
			// patch). This must be ALLOWED but was rejected by the full-semver
			// GT() comparison.
			name:           "higher patch than control plane is allowed (bug case)",
			desiredVersion: "4.22.8",
			activeVersions: []string{"4.22.7"},
			lowestCP:       "4.22.7",
			highestCP:      "4.22.7",
			expectError:    false,
		},
		{
			// Higher minor than the control plane must still be rejected.
			name:           "higher minor than control plane is rejected",
			desiredVersion: "4.23.0",
			activeVersions: []string{"4.22.7"},
			lowestCP:       "4.22.7",
			highestCP:      "4.22.7",
			expectError:    true,
			errContains:    "invalid node pool version 4.23.0: cannot exceed control plane version 4.22.7",
		},
		{
			// Desired version equal to the control plane version is allowed.
			name:           "equal to control plane is allowed",
			desiredVersion: "4.22.7",
			activeVersions: []string{"4.22.6"},
			lowestCP:       "4.22.7",
			highestCP:      "4.22.7",
			expectError:    false,
		},
		{
			// Lower patch than the control plane is allowed.
			name:           "lower patch than control plane is allowed",
			desiredVersion: "4.22.6",
			activeVersions: []string{"4.22.5"},
			lowestCP:       "4.22.7",
			highestCP:      "4.22.7",
			expectError:    false,
		},
		{
			// Even a patch above the LOWEST control plane version (with a higher
			// CP also present) is allowed, since only major.minor is compared.
			name:           "higher patch than lowest control plane version is allowed",
			desiredVersion: "4.22.9",
			activeVersions: []string{"4.22.7"},
			lowestCP:       "4.22.7",
			highestCP:      "4.22.8",
			expectError:    false,
		},

		// --- Early return: desired version already active ---
		{
			// A desired version that already appears in the node pool's active
			// versions short-circuits and returns nil before any control plane
			// checks — even when the control plane versions are unknown (nil).
			name:           "desired version already active is allowed even with unknown control plane versions",
			desiredVersion: "4.22.7",
			activeVersions: []string{"4.22.7"},
			lowestCP:       "", // nil
			highestCP:      "", // nil
			expectError:    false,
		},

		// --- Unknown (nil) control plane versions ---
		{
			// A nil lowest control plane version cannot be validated against.
			name:           "nil lowest control plane version is rejected",
			desiredVersion: "4.22.0",
			activeVersions: []string{"4.21.0"},
			lowestCP:       "", // nil
			highestCP:      "4.22.0",
			expectError:    true,
			errContains:    "cannot validate node pool version change because lowest control plane version is not known",
		},
		{
			// A nil highest control plane version cannot be validated against.
			name:           "nil highest control plane version is rejected",
			desiredVersion: "4.22.0",
			activeVersions: []string{"4.21.0"},
			lowestCP:       "4.22.0",
			highestCP:      "", // nil
			expectError:    true,
			errContains:    "cannot validate node pool version change because highest control plane version is not known",
		},

		// --- major.minor must not exceed the lowest control plane version ---
		{
			// A higher MAJOR than the control plane must be rejected (exercises the
			// major-comparison branch of the exceed check, distinct from the minor
			// case above).
			name:           "higher major than control plane is rejected",
			desiredVersion: "5.0.0",
			activeVersions: []string{"4.22.0"},
			lowestCP:       "4.22.0",
			highestCP:      "4.22.0",
			expectError:    true,
			errContains:    "invalid node pool version 5.0.0: cannot exceed control plane version 4.22.0",
		},

		// --- N-2 skew below the highest control plane version ---
		{
			// A node pool more than 2 minor versions below the highest control
			// plane version violates the N-2 skew policy.
			name:           "more than 2 minor versions below highest control plane is rejected",
			desiredVersion: "4.19.0",
			activeVersions: []string{"4.20.0"},
			lowestCP:       "4.22.0",
			highestCP:      "4.22.0",
			expectError:    true,
			errContains:    "invalid node pool version 4.19.0: must be within 2 minor versions of control plane version 4.22.0",
		},
		{
			// Exactly 2 minor versions below the highest control plane version is
			// the boundary and must be allowed.
			name:           "exactly 2 minor versions below highest control plane is allowed",
			desiredVersion: "4.20.0",
			activeVersions: []string{"4.21.0"},
			lowestCP:       "4.22.0",
			highestCP:      "4.22.0",
			expectError:    false,
		},

		// --- Same-major NP change while the control plane is on a different major ---
		{
			// NP stays on major 4 while CP moved to major 5. Without the major
			// upgrade flag this is rejected.
			name:           "same-major node pool change with different-major control plane requires major upgrade flag",
			desiredVersion: "4.22.0",
			activeVersions: []string{"4.21.0"},
			lowestCP:       "5.0.0",
			highestCP:      "5.0.0",
			allowMajor:     false,
			expectError:    true,
			errContains:    "node pool version changes are not supported while the control plane is on a different major version (node pool major version 4 vs control plane major version 5)",
		},
		{
			// With the flag set and an allowed skew (NP 4.22 alongside CP 5.0),
			// the change succeeds via ValidateCrossMajorNodePoolSkew.
			name:           "same-major node pool change with allowed cross-major skew succeeds",
			desiredVersion: "4.22.0",
			activeVersions: []string{"4.21.0"},
			lowestCP:       "5.0.0",
			highestCP:      "5.0.0",
			allowMajor:     true,
			expectError:    false,
		},
		{
			// NP 4.20 has no entry in the cross-major skew map, so it cannot
			// coexist with a different-major control plane.
			name:           "node pool version not in cross-major skew map is rejected",
			desiredVersion: "4.20.0",
			activeVersions: []string{"4.19.0"},
			lowestCP:       "5.0.0",
			highestCP:      "5.0.0",
			allowMajor:     true,
			expectError:    true,
			errContains:    "node pool version 4.20.0 is not allowed to coexist with a different-major control plane",
		},
		{
			// NP 4.21 may only coexist with CP 5.0, so CP 5.1 is rejected.
			name:           "node pool cross-major skew with disallowed control plane line is rejected",
			desiredVersion: "4.21.0",
			activeVersions: []string{"4.20.0"},
			lowestCP:       "5.1.0",
			highestCP:      "5.1.0",
			allowMajor:     true,
			expectError:    true,
			errContains:    "node pool version 4.21.0 cannot coexist with control plane version 5.1.0",
		},

		// --- Cross-major upgrade (desired major above the active lowest) ---
		{
			// Cross-major upgrade without the flag is rejected.
			name:           "cross-major upgrade requires major upgrade flag",
			desiredVersion: "5.0.0",
			activeVersions: []string{"4.22.0"},
			lowestCP:       "5.0.0",
			highestCP:      "5.0.0",
			allowMajor:     false,
			expectError:    true,
			errContains:    "major version changes are not supported",
		},
		{
			// 4.22 -> 5.0 is an allowed upgrade path, so it succeeds.
			name:           "allowed cross-major upgrade path succeeds",
			desiredVersion: "5.0.0",
			activeVersions: []string{"4.22.0"},
			lowestCP:       "5.0.0",
			highestCP:      "5.0.0",
			allowMajor:     true,
			expectError:    false,
		},
		{
			// 4.22 may only upgrade to 5.0, so an upgrade to 5.1 is rejected.
			name:           "cross-major upgrade to disallowed target line is rejected",
			desiredVersion: "5.1.0",
			activeVersions: []string{"4.22.0"},
			lowestCP:       "5.1.0",
			highestCP:      "5.1.0",
			allowMajor:     true,
			expectError:    true,
			errContains:    "invalid upgrade path from 4.22.0 to 5.1.0: 4.22 can only upgrade to 5.0",
		},
		{
			// 4.21 is not a supported cross-major upgrade source line.
			name:           "cross-major upgrade from unsupported source line is rejected",
			desiredVersion: "5.0.0",
			activeVersions: []string{"4.21.0"},
			lowestCP:       "5.0.0",
			highestCP:      "5.0.0",
			allowMajor:     true,
			expectError:    true,
			errContains:    "invalid upgrade path from 4.21.0 to 5.0.0: major version upgrades are not supported",
		},

		// --- Cross-major downgrade (desired major below the active highest) ---
		{
			// Cross-major downgrade with the flag and an allowed skew (NP 4.22
			// alongside CP 5.0) succeeds via ValidateCrossMajorNodePoolSkew.
			name:           "cross-major downgrade with allowed skew succeeds",
			desiredVersion: "4.22.0",
			activeVersions: []string{"5.0.0"},
			lowestCP:       "5.0.0",
			highestCP:      "5.0.0",
			allowMajor:     true,
			expectError:    false,
		},

		// --- Minor skip (N-2 upgrade) policy ---
		{
			// Upgrading more than 2 minor versions above the active lowest is
			// rejected (4.20 -> 4.23 skips 4.21 and 4.22).
			name:           "upgrade skipping more than 2 minor versions is rejected",
			desiredVersion: "4.23.0",
			activeVersions: []string{"4.20.0"},
			lowestCP:       "4.25.0",
			highestCP:      "4.25.0",
			expectError:    true,
			errContains:    "invalid upgrade path from 4.20.0 to 4.23.0: skipping more than 2 minor versions is not allowed",
		},
		{
			// Upgrading exactly 2 minor versions (4.20 -> 4.22) is the boundary
			// and must be allowed.
			name:           "upgrade of exactly 2 minor versions is allowed",
			desiredVersion: "4.22.0",
			activeVersions: []string{"4.20.0"},
			lowestCP:       "4.22.0",
			highestCP:      "4.22.0",
			expectError:    false,
		},

		// --- No active versions (lowest/highest are nil) ---
		{
			// With no active versions and a matching control plane, all
			// active-version-derived checks are skipped and the change is allowed.
			name:           "no active versions with matching control plane is allowed",
			desiredVersion: "4.22.0",
			activeVersions: []string{},
			lowestCP:       "4.22.0",
			highestCP:      "4.22.0",
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// An empty lowestCP/highestCP string represents an unknown
			// (nil) control plane version.
			var lowestCP, highestCP *semver.Version
			if tt.lowestCP != "" {
				lowestCP = mustSemverPtr(tt.lowestCP)
			}
			if tt.highestCP != "" {
				highestCP = mustSemverPtr(tt.highestCP)
			}
			err := ValidateNodePoolVersionChange(
				semver.MustParse(tt.desiredVersion),
				nodePoolActiveVersions(tt.activeVersions...),
				lowestCP,
				highestCP,
				tt.allowMajor,
			)
			if tt.expectError {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errContains)
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Fatalf("expected error containing %q, got %q", tt.errContains, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got %q", err.Error())
			}
		})
	}
}
