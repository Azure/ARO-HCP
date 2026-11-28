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

package preview

import (
	"strings"
	"testing"

	"github.com/Azure/ARO-Tools/tools/grafanactl/cmd/dashboard"
)

// TestBuildPreviewUID pins the truncation to what hack/ci/grafana-preview.sh
// produced. A preview UID that changes between runs would leave a second
// dashboard behind on every push, so the expected values below are the output
// of the shell the Go code replaces.
func TestBuildPreviewUID(t *testing.T) {
	for _, test := range []struct {
		name        string
		pullNumber  int
		originalUID string
		want        string
	}{
		{
			name:        "a short uid is kept whole",
			pullNumber:  1234,
			originalUID: "frontend-slo",
			want:        "pr-1234-frontend-slo",
		},
		{
			name:        "a uid of exactly the limit is kept whole",
			pullNumber:  1234,
			originalUID: "1813f692a8e4ac77155348d4c7d2fba8",
			want:        "pr-1234-1813f692a8e4ac77155348d4c7d2fba8",
		},
		{
			name:        "one character past the limit is truncated",
			pullNumber:  12345,
			originalUID: "1813f692a8e4ac77155348d4c7d2fba8",
			want:        "pr-12345-1813f692a8e4ac77155348d4-c52564",
		},
		{
			name:        "a guid uid is truncated",
			pullNumber:  1234,
			originalUID: "e78df788-2208-4a2b-b9ba-5e9ea155d7ed",
			want:        "pr-1234-e78df788-2208-4a2b-b9ba-5-288111",
		},
		{
			name:        "the pull request number is part of the hash",
			pullNumber:  12345,
			originalUID: "e78df788-2208-4a2b-b9ba-5e9ea155d7ed",
			want:        "pr-12345-e78df788-2208-4a2b-b9ba--ccc11d",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := buildPreviewUID(test.pullNumber, test.originalUID)
			if got != test.want {
				t.Errorf("buildPreviewUID(%d, %q) = %q, want %q", test.pullNumber, test.originalUID, got, test.want)
			}
			if len(got) > dashboard.MaxUIDLength {
				t.Errorf("buildPreviewUID(%d, %q) is %d characters, Grafana accepts at most %d",
					test.pullNumber, test.originalUID, len(got), dashboard.MaxUIDLength)
			}
			if err := dashboard.ValidateUID(got); err != nil {
				t.Errorf("Grafana rejects %q: %v", got, err)
			}
		})
	}
}

func TestBuildPreviewUIDIsStable(t *testing.T) {
	long := strings.Repeat("a", 60)

	first := buildPreviewUID(7, long)
	second := buildPreviewUID(7, long)
	if first != second {
		t.Errorf("buildPreviewUID gave %q then %q; a second push would add a dashboard instead of replacing one", first, second)
	}

	if other := buildPreviewUID(8, long); first == other {
		t.Errorf("pull requests 7 and 8 share the preview UID %q", first)
	}
}

func TestBuildFolderNames(t *testing.T) {
	if got := buildFolderUID(1234); got != "pr-1234" {
		t.Errorf("buildFolderUID = %q, want pr-1234", got)
	}
	if got := buildFolderTitle(1234); got != "PR-1234" {
		t.Errorf("buildFolderTitle = %q, want PR-1234", got)
	}
}
