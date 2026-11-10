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

package discovery

import (
	"testing"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
)

func TestVersionStateAcceptsAgreeingMultiplePins(t *testing.T) {
	cases := []struct {
		name    string
		current string
		latest  string
		want    string
	}{
		{"five agreeing pins are current", "v0.34.3", "v0.34.3", model.VersionCurrent},
		{"five agreeing pins are outdated", "v0.34.3", "v0.34.4", model.VersionOutdated},
		{"agreeing pins ahead of upstream", "v0.34.4", "v0.34.3", model.VersionAhead},
		{"conflicting pins stay blocked", "", "v0.34.4", model.VersionBlocked},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			status, reason := versionState(testCase.current, testCase.latest)
			if status != testCase.want {
				t.Fatalf("status = %q (%s), want %q", status, reason, testCase.want)
			}
		})
	}
}
