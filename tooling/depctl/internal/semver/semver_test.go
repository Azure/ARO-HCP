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

package semver

import "testing"

func TestComparePrecedence(t *testing.T) {
	cases := []struct {
		left, right string
		want        int
	}{
		{"1.0.0", "1.0.0", 0},
		{"v1.0.0", "1.0.0", 0},
		{"1.0.0", "1.0.1", -1},
		{"1.10.0", "1.9.0", 1},
		{"1.0.0-alpha", "1.0.0", -1},
		{"1.0.0-alpha", "1.0.0-beta", -1},
		{"1.0.0-alpha.2", "1.0.0-alpha.10", -1},
		{"1.0.0-alpha.1", "1.0.0-alpha.a", -1},
		{"1.0.0+build.1", "1.0.0+build.2", 0},
	}
	for _, testCase := range cases {
		got, err := CompareStrings(testCase.left, testCase.right)
		if err != nil {
			t.Fatalf("CompareStrings(%q, %q) error = %v", testCase.left, testCase.right, err)
		}
		if got != testCase.want {
			t.Fatalf("CompareStrings(%q, %q) = %d, want %d", testCase.left, testCase.right, got, testCase.want)
		}
	}
}

func TestParseRejectsInvalidVersions(t *testing.T) {
	for _, value := range []string{"", "1.0", "1.0.0.0", "01.0.0", "1.0.0-01", "latest", "v1.0.0-"} {
		if _, err := Parse(value); err == nil {
			t.Fatalf("Parse(%q) must fail", value)
		}
	}
}

func TestParseReportsStability(t *testing.T) {
	stable, err := Parse("2.3.4")
	if err != nil || !stable.Stable {
		t.Fatalf("Parse(2.3.4) = %+v, %v", stable, err)
	}
	prerelease, err := Parse("2.3.4-rc.1")
	if err != nil || prerelease.Stable {
		t.Fatalf("Parse(2.3.4-rc.1) = %+v, %v", prerelease, err)
	}
}
