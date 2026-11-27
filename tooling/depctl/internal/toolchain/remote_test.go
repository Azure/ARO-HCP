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

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	githublib "github.com/Azure/ARO-HCP/tooling/github"
)

// TestFindOpenUpgrade requires complete revision-bound evidence even for human PRs.
func TestFindOpenUpgrade(t *testing.T) {
	const name = "tooling/depctl/go.mod"
	base, head := strings.Repeat("a", 40), strings.Repeat("b", 40)
	inventoryError := errors.New("inventory incomplete")
	for _, test := range []struct {
		name, path, patch, fileList, wantError string
		inventoryError                         error
		wantUpgrade                            bool
	}{
		{name: "human compiler update", patch: "@@ -1 +1 @@\n-go 1.26.0\n+go 1.26.7", wantUpgrade: true},
		{name: "human dependency update", patch: "@@ -1 +1 @@\n-require example.org/lib v1.2.3\n+require example.org/lib v1.2.4"},
		{name: "config change without patch", path: devcontainerPath, wantUpgrade: true},
		{name: "missing manifest patch", wantUpgrade: true},
		{name: "truncated manifest patch", patch: "@@ -1 +1 @@\n-require example.org/lib v1.2.3", wantUpgrade: true},
		{name: "incomplete file inventory", fileList: `[[]]`, wantError: "complete PR file list"},
		{name: "incomplete open inventory", inventoryError: inventoryError, wantError: inventoryError.Error()},
	} {
		t.Run(test.name, func(t *testing.T) {
			list := func(context.Context) ([]githublib.PullRequest, error) {
				return []githublib.PullRequest{{Number: 17, Author: "human", HeadSHA: head}}, test.inventoryError
			}
			prefix := "repos/" + Repository
			endpoint := prefix + "/pulls/17"
			fileList := test.fileList
			if fileList == "" {
				filename := test.path
				if filename == "" {
					filename = name
				}
				if test.patch == "" {
					fileList = fmt.Sprintf(`[[{"filename":%q,"status":"modified"}]]`, filename)
				}
				if test.patch != "" {
					fileList = fmt.Sprintf(`[[{"filename":%q,"status":"modified","patch":%q,"additions":1,"deletions":1}]]`, filename, test.patch)
				}
			}
			responses := map[string]string{
				endpoint:                         fmt.Sprintf(`{"number":17,"state":"open","changed_files":1,"base":{"sha":%q,"repo":{"full_name":%q}},"head":{"sha":%q}}`, base, Repository, head),
				endpoint + "/files?per_page=100": fileList,
			}
			read := func(_ context.Context, endpoint string, result any, _ bool) error {
				raw, ok := responses[endpoint]
				if !ok {
					t.Fatalf("unexpected evidence request: %s", endpoint)
				}
				return json.Unmarshal([]byte(raw), result)
			}
			upgrades, err := findOpenUpgrade(context.Background(), Repository, list, read)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) || upgrades != nil {
					t.Fatalf("upgrades = %v, err = %v; want error containing %q", upgrades, err, test.wantError)
				}
				return
			}
			want := []Upgrade{}
			if test.wantUpgrade {
				want = append(want, Upgrade{Number: 17, URL: "https://github.com/" + Repository + "/pull/17"})
			}
			if err != nil || !slices.Equal(upgrades, want) {
				t.Fatalf("upgrades = %v, err = %v; want %v", upgrades, err, want)
			}
		})
	}
}
