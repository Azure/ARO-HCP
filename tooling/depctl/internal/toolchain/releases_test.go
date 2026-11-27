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

package toolchain

import "testing"

// TestLatestPatch covers ordering, stability, and release-line isolation.
func TestLatestPatch(t *testing.T) {
	releases := []goRelease{{"go1.27.3", true}, {"go1.26.9", true}, {"go1.26.11", false}, {"go1.26.10", true}, {"go1.26.12rc1", false}}
	for _, test := range []struct{ current, want string }{
		{"1.26.0", "1.26.10"}, {"go1.27.3", "1.27.3"}, {"1.26.12", ""}, {"1.29.0", ""}, {"1.26", ""},
	} {
		t.Run(test.current, func(t *testing.T) {
			got, err := latestPatch(test.current, releases)
			if got != test.want || (err != nil) != (test.want == "") {
				t.Fatalf("latestPatch(%q) = %q, %v; want %q", test.current, got, err, test.want)
			}
		})
	}
}
